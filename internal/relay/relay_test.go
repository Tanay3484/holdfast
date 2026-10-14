package relay

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tanay3484/holdfast/internal/config"
	"github.com/Tanay3484/holdfast/internal/diagnose"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/store"
)

// These tests hold whether or not the @human levels are written: they use outcomes every policy
// treats the same way (2xx delivered, 400 held) and routes without secrets.

func newRelay(t *testing.T, dest string) (*Relay, Route) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rt := Route{Route: config.Route{Name: "shop", Source: "generic", Destination: dest, Client: "Test Shop"},
		Timeout: 2 * time.Second, Policy: policy.Default()}
	r := New(st, []Route{rt}, diagnose.Diagnoser{}, time.UTC, 72*time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return r, rt
}

func post(r *Relay, route, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/in/"+route, strings.NewReader(body))
	req.SetPathValue("route", route)
	w := httptest.NewRecorder()
	r.HandleIngest(w, req)
	return w
}

func TestIngestStoresBeforeAcknowledging(t *testing.T) {
	r, _ := newRelay(t, "http://127.0.0.1:1/never")
	w := post(r, "shop", `{"type":"order.paid"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	due, err := r.Store.Due(context.Background(), time.Now().Add(time.Second), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("acknowledged event not stored: %v %v", due, err)
	}
	if due[0].EventType != "order.paid" || due[0].Signature != store.SigNone {
		t.Fatalf("stored event: %+v", due[0])
	}
}

func TestIngestRejectsUnknownRouteAndHugeBody(t *testing.T) {
	r, _ := newRelay(t, "http://127.0.0.1:1/never")
	if w := post(r, "nope", `{}`); w.Code != http.StatusNotFound {
		t.Fatalf("unknown route: %d", w.Code)
	}
	if w := post(r, "shop", strings.Repeat("x", MaxBody+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("huge body: %d", w.Code)
	}
	if n, _ := r.Store.Counts(context.Background(), "shop"); len(n) != 0 {
		t.Fatalf("rejected requests must not be stored: %v", n)
	}
}

func TestDeliverForwardsBodyAndMarksDelivered(t *testing.T) {
	var got atomic.Value
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		got.Store(string(b) + "|" + req.Header.Get("Holdfast-Event-Id") + "|" + req.Header.Get("Holdfast-Attempt"))
		w.WriteHeader(http.StatusOK)
	}))
	defer app.Close()
	r, _ := newRelay(t, app.URL)
	post(r, "shop", `{"type":"order.paid"}`)

	ctx := context.Background()
	due, _ := r.Store.Due(ctx, time.Now().Add(time.Second), 10)
	r.Deliver(ctx, due[0])

	if got.Load() != `{"type":"order.paid"}|1|1` {
		t.Fatalf("app received %q", got.Load())
	}
	ev, _ := r.Store.Get(ctx, due[0].ID)
	if ev.Status != store.Delivered || ev.Attempts != 1 {
		t.Fatalf("after a 200: %+v", ev)
	}
}

func TestDeliverHoldsWhatTheAppRefuses(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"missing field customer_id"}`, http.StatusBadRequest)
	}))
	defer app.Close()
	r, _ := newRelay(t, app.URL)
	post(r, "shop", `{"type":"order.paid"}`)

	ctx := context.Background()
	due, _ := r.Store.Due(ctx, time.Now().Add(time.Second), 10)
	r.Deliver(ctx, due[0])

	ev, _ := r.Store.Get(ctx, due[0].ID)
	if ev.Status != store.Held {
		t.Fatalf("a 400 must be held, got %q", ev.Status)
	}
	as, _ := r.Store.Attempts(ctx, ev.ID)
	if len(as) != 1 || as[0].StatusCode != 400 || !strings.Contains(as[0].Response, "customer_id") {
		t.Fatalf("attempt not recorded: %+v", as)
	}

	f, err := r.Facts(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	if f.LastStatus != 400 || f.Attempts != 1 || f.App != "Test Shop" {
		t.Fatalf("facts: %+v", f)
	}
	if d := r.Diagnoser.Diagnose(ctx, f, diagnose.Combined); d.Cause != diagnose.RejectedByApp && d.Cause != diagnose.PayloadChanged {
		t.Fatalf("diagnosis: %+v", d)
	}
}

func TestAcknowledgedEventSurvivesARestart(t *testing.T) {
	var hits atomic.Int32
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer app.Close()
	r, rt := newRelay(t, app.URL)
	post(r, "shop", `{"type":"order.paid"}`)

	// A second relay on the same store stands in for the process after a crash.
	r2 := New(r.Store, []Route{rt}, diagnose.Diagnoser{}, time.UTC, time.Hour, r.Log)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go r2.deliverLoop(ctx)
	for hits.Load() == 0 && ctx.Err() == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatalf("the restarted relay delivered %d times, want 1", hits.Load())
	}
}

func TestRetryAfterParsing(t *testing.T) {
	for in, want := range map[string]time.Duration{"7": 7 * time.Second, " 3 ": 3 * time.Second, "": 0, "soon": 0, "-1": 0} {
		if got := retryAfter(in); got != want {
			t.Errorf("retryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}
