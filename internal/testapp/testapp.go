// Package testapp is a stand-in for the user's own app in Live mode. A user with no app of their own
// can point a real GitHub or Stripe webhook at it, then make it fail on purpose to watch Holdfast
// hold the event, explain it and send it again.
package testapp

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Mode is how the test app answers.
type Mode string

const (
	Working Mode = "working" // 200
	Down    Mode = "down"    // 503, as if the server crashed
	Strict  Mode = "strict"  // 400, as if a validation rule rejected the new layout
	Slow    Mode = "slow"    // answers after Holdfast has stopped waiting
)

// Modes in display order, with plain labels.
var Modes = []struct {
	Mode  Mode
	Label string
	Hint  string
}{
	{Working, "Working normally", "Accepts everything."},
	{Down, "Server down", "Answers 503. Holdfast retries, then holds it."},
	{Strict, "Turns messages away", "Answers 400, as a strict validation rule would."},
	{Slow, "Too slow", "Takes longer than Holdfast waits."},
}

// Received is one message the test app accepted.
type Received struct {
	At      time.Time
	EventID string // Holdfast-Event-Id
	Repeat  bool   // it had already accepted this event id
}

// App keeps one mode and one log per connection, in memory.
type App struct {
	mu       sync.Mutex
	modes    map[string]Mode
	log      map[string][]Received
	slowFor  time.Duration
	maxPerID int
}

// New returns a test app with every connection working.
func New() *App {
	return &App{modes: map[string]Mode{}, log: map[string][]Received{}, slowFor: 15 * time.Second, maxPerID: 50}
}

// SetMode changes how a connection's test app answers.
func (a *App) SetMode(conn string, m Mode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.modes[conn] = m
}

// Mode returns a connection's current mode.
func (a *App) Mode(conn string) Mode {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m, ok := a.modes[conn]; ok {
		return m
	}
	return Working
}

// Log returns what a connection's test app accepted, newest first.
func (a *App) Log(conn string) []Received {
	a.mu.Lock()
	defer a.mu.Unlock()
	src := a.log[conn]
	out := make([]Received, len(src))
	for i := range src {
		out[i] = src[len(src)-1-i]
	}
	return out
}

// Forget drops a connection's state when it is deleted.
func (a *App) Forget(conn string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.modes, conn)
	delete(a.log, conn)
}

// ServeHTTP is POST /test-app/{conn}.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn := r.PathValue("conn")
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
	switch a.Mode(conn) {
	case Down:
		reply(w, http.StatusServiceUnavailable, "service unavailable")
		return
	case Strict:
		reply(w, http.StatusBadRequest, "validation failed: this app does not accept messages in this layout")
		return
	case Slow:
		select {
		case <-time.After(a.slowFor):
		case <-r.Context().Done():
			return
		}
	}
	id := r.Header.Get("Holdfast-Event-Id")
	a.mu.Lock()
	repeat := false
	for _, rec := range a.log[conn] {
		if id != "" && rec.EventID == id {
			repeat = true
		}
	}
	a.log[conn] = append(a.log[conn], Received{At: time.Now(), EventID: id, Repeat: repeat})
	if n := len(a.log[conn]); n > a.maxPerID {
		a.log[conn] = a.log[conn][n-a.maxPerID:]
	}
	a.mu.Unlock()
	reply(w, http.StatusOK, "")
}

func reply(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if msg == "" {
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
