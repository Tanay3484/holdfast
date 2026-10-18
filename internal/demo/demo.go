package demo

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/Tanay3484/holdfast/internal/faults"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/store"
)

// Route settings for the demo shop. The secret is public on purpose: it only signs demo events.
const (
	RouteName = "demo-shop"
	Secret    = "whsec_holdfast_demo"
	Client    = "Chai & Co."
)

// Timeout and Policy are short so a visitor sees retries within seconds.
const Timeout = 3 * time.Second

// Policy is the demo route's retry policy.
var Policy = policy.Policy{MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 8 * time.Second}

// Runner fires demo webhooks at the relay's own public endpoint, so they take the real path.
type Runner struct {
	Shop    *Shop
	Store   *store.Store
	InURL   string // e.g. http://127.0.0.1:8080/in/demo-shop
	HTTP    *http.Client
	rngMu   sync.Mutex
	rng     *rand.Rand
	limitMu sync.Mutex
	last    map[string]time.Time
}

// NewRunner returns a runner that sends to inURL.
func NewRunner(shop *Shop, st *store.Store, inURL string) *Runner {
	return &Runner{Shop: shop, Store: st, InURL: inURL, HTTP: &http.Client{Timeout: 10 * time.Second},
		rng: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 7)), last: map[string]time.Time{}}
}

// Allow is a small rate limit: one demo action per visitor every two seconds.
func (r *Runner) Allow(visitor string) bool {
	r.limitMu.Lock()
	defer r.limitMu.Unlock()
	now := time.Now()
	if now.Sub(r.last[visitor]) < 2*time.Second {
		return false
	}
	r.last[visitor] = now
	if len(r.last) > 10000 {
		r.last = map[string]time.Time{}
	}
	return true
}

func (r *Runner) payment(renamed bool) []byte {
	r.rngMu.Lock()
	defer r.rngMu.Unlock()
	return faults.Payment(r.rng, time.Now(), renamed)
}

// Run triggers one demo scenario.
func (r *Runner) Run(ctx context.Context, f faults.Fault) error {
	now := time.Now()
	switch f {
	case faults.Healthy:
		return r.post(ctx, r.payment(false), Secret, now)
	case faults.WrongSecret:
		return r.post(ctx, r.payment(false), "whsec_not_the_right_one", now)
	case faults.StaleTimestamp:
		return r.post(ctx, r.payment(false), Secret, now.Add(-time.Hour))
	case faults.RenamedField:
		// The comparison needs one payment the shop accepted; send one first if there is none yet.
		if !r.hasDelivered(ctx) {
			if err := r.post(ctx, r.payment(false), Secret, now); err != nil {
				return err
			}
			time.Sleep(1500 * time.Millisecond)
		}
		return r.post(ctx, r.payment(true), Secret, time.Now())
	case faults.AppDown:
		r.Shop.GoDown(5 * time.Second)
		return r.post(ctx, r.payment(false), Secret, now)
	case faults.SlowApp:
		r.Shop.GoSlow(25 * time.Second)
		return r.post(ctx, r.payment(false), Secret, now)
	case faults.Duplicate:
		body := r.payment(false)
		if err := r.post(ctx, body, Secret, now); err != nil {
			return err
		}
		time.Sleep(400 * time.Millisecond)
		return r.post(ctx, body, Secret, now)
	}
	return fmt.Errorf("unknown demo %q", f)
}

// Seed fills an empty demo with a short history once the server is up: two payments that arrive, an
// outage that recovers, a duplicate that is stopped, and one payment held because a field was renamed.
func (r *Runner) Seed(ctx context.Context) {
	if counts, err := r.Store.Counts(ctx, RouteName); err != nil || len(counts) > 0 {
		return
	}
	for _, f := range []faults.Fault{faults.Healthy, faults.Healthy, faults.AppDown, faults.Duplicate, faults.RenamedField} {
		select {
		case <-ctx.Done():
			return
		case <-time.After(1500 * time.Millisecond):
		}
		if err := r.Run(ctx, f); err != nil {
			return
		}
		if f == faults.AppDown {
			time.Sleep(9 * time.Second) // let the outage recover before the next payment
		}
	}
}

func (r *Runner) hasDelivered(ctx context.Context) bool {
	evs, err := r.Store.ByStatus(ctx, RouteName, store.Delivered)
	return err == nil && len(evs) > 0
}

func (r *Runner) post(ctx context.Context, body []byte, secret string, signedAt time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.InURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "Stripe/1.0 (+https://stripe.com/docs/webhooks) holdfast-demo")
	req.Header.Set("Stripe-Signature", faults.SignStripe(secret, body, signedAt))
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay answered %d", resp.StatusCode)
	}
	return nil
}
