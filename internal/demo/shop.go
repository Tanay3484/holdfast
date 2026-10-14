// Package demo is the live demo: a small online tea shop ("Chai & Co.") that receives payment
// webhooks through Holdfast, and the buttons that break things on purpose.
package demo

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Order is one payment the shop recorded.
type Order struct {
	At       time.Time
	EventID  string
	Customer string
	Item     string
	Amount   int  // in paise
	Repeat   bool // the shop had already recorded this payment: it was counted twice
}

// Shop is a deliberately simple webhook handler, like the one a small business might really run:
// it reads customer_id, records the payment, and does not check for repeats.
type Shop struct {
	mu        sync.Mutex
	downUntil time.Time
	slowUntil time.Time
	slowFor   time.Duration
	seen      map[string]bool
	orders    []Order
	now       func() time.Time
}

// NewShop returns an empty shop.
func NewShop() *Shop {
	return &Shop{seen: map[string]bool{}, now: time.Now, slowFor: 5 * time.Second}
}

// GoDown makes the shop answer 503 for d.
func (s *Shop) GoDown(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downUntil = s.now().Add(d)
}

// GoSlow makes the shop take longer than Holdfast waits, for d.
func (s *Shop) GoSlow(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slowUntil = s.now().Add(d)
}

// Status says what the shop is doing right now, for the page.
func (s *Shop) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch now := s.now(); {
	case now.Before(s.downUntil):
		return "down"
	case now.Before(s.slowUntil):
		return "slow"
	}
	return "open"
}

// Orders returns the newest orders first, at most n.
func (s *Shop) Orders(n int) []Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Order, 0, n)
	for i := len(s.orders) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, s.orders[i])
	}
	return out
}

// Totals returns how many payments were recorded and how many of those were repeats.
func (s *Shop) Totals() (recorded, repeats int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.orders {
		recorded++
		if o.Repeat {
			repeats++
		}
	}
	return recorded, repeats
}

// ServeHTTP is the shop's webhook endpoint.
func (s *Shop) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	now, down, slow, slowFor := s.now(), s.downUntil, s.slowUntil, s.slowFor
	s.mu.Unlock()

	if now.Before(down) {
		answer(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	if now.Before(slow) {
		select {
		case <-time.After(slowFor):
		case <-r.Context().Done():
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		answer(w, http.StatusBadRequest, "could not read body")
		return
	}
	var ev struct {
		ID   string `json:"id"`
		Data struct {
			Object struct {
				Amount     int     `json:"amount"`
				CustomerID *string `json:"customer_id"`
				Metadata   struct {
					CustomerName string `json:"customer_name"`
					Item         string `json:"item"`
				} `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		answer(w, http.StatusBadRequest, "could not parse body")
		return
	}
	obj := ev.Data.Object
	if obj.CustomerID == nil {
		answer(w, http.StatusBadRequest, "missing required field customer_id")
		return
	}

	s.mu.Lock()
	repeat := s.seen[ev.ID]
	s.seen[ev.ID] = true
	s.orders = append(s.orders, Order{At: s.now(), EventID: ev.ID, Customer: obj.Metadata.CustomerName,
		Item: obj.Metadata.Item, Amount: obj.Amount, Repeat: repeat})
	if len(s.orders) > 500 {
		s.orders = s.orders[len(s.orders)-500:]
	}
	s.mu.Unlock()
	answer(w, http.StatusOK, "")
}

func answer(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if msg == "" {
		_, _ = w.Write([]byte(`{"status":"recorded"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
