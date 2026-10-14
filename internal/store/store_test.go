package store

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func event(route string, at time.Time) *Event {
	return &Event{Route: route, Source: "stripe", EventType: "payment_intent.succeeded", ReceivedAt: at,
		Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"evt_1"}`),
		Signature: SigNone, DedupeKey: "stripe:evt_1", Status: Pending, NextAt: at}
}

func TestInsertAndGetRoundTrip(t *testing.T) {
	s, ctx := open(t), context.Background()
	at := time.UnixMilli(time.Now().UnixMilli())
	id, err := s.Insert(ctx, event("shop", at))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Route != "shop" || string(got.Body) != `{"id":"evt_1"}` || !got.ReceivedAt.Equal(at) ||
		got.Headers.Get("Content-Type") != "application/json" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if _, err := s.Get(ctx, id+1); err != ErrNotFound {
		t.Fatalf("missing event: got %v, want ErrNotFound", err)
	}
}

func TestDueOnlyReturnsWhatIsDue(t *testing.T) {
	s, ctx := open(t), context.Background()
	now := time.Now()
	early := event("shop", now)
	later := event("shop", now)
	later.Status, later.NextAt = Retrying, now.Add(time.Minute)
	held := event("shop", now)
	held.Status = Held
	for _, e := range []*Event{early, later, held} {
		if _, err := s.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.Due(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != early.ID {
		t.Fatalf("due = %v, want only event %d", ids(due), early.ID)
	}
	due, _ = s.Due(ctx, now.Add(2*time.Minute), 10)
	if len(due) != 2 {
		t.Fatalf("after a minute, due = %v, want 2 events", ids(due))
	}
}

func TestRecordAttemptMovesStatusAndKeepsHistory(t *testing.T) {
	s, ctx := open(t), context.Background()
	e := event("shop", time.Now())
	s.Insert(ctx, e)
	if err := s.RecordAttempt(ctx, Attempt{EventID: e.ID, At: time.Now(), StatusCode: 503, Action: "retry", Reason: "down"},
		Retrying, time.Now().Add(time.Second), "Trying again"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttempt(ctx, Attempt{EventID: e.ID, At: time.Now(), StatusCode: 400, Action: "hold", Reason: "no"},
		Held, time.Time{}, "Held"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, e.ID)
	if got.Status != Held || got.Attempts != 2 || got.Reason != "Held" {
		t.Fatalf("event after two attempts: %+v", got)
	}
	as, _ := s.Attempts(ctx, e.ID)
	if len(as) != 2 || as[0].StatusCode != 503 || as[1].StatusCode != 400 {
		t.Fatalf("attempts = %+v", as)
	}

	if err := s.Requeue(ctx, e.ID, "again"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, e.ID)
	if got.Status != Pending || got.Attempts != 0 {
		t.Fatalf("requeued event: %+v", got)
	}
	if as, _ := s.Attempts(ctx, e.ID); len(as) != 2 {
		t.Fatal("requeue must keep the attempt history")
	}
	if err := s.Requeue(ctx, e.ID, "again"); err == nil {
		t.Fatal("requeueing an event that is not held should fail")
	}
}

func TestHasKeyIgnoresDuplicates(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, found, _ := s.HasKey(ctx, "shop", "stripe:evt_1"); found {
		t.Fatal("empty store has no keys")
	}
	dup := event("shop", time.Now())
	dup.Status = Duplicate
	s.Insert(ctx, dup)
	if _, found, _ := s.HasKey(ctx, "shop", "stripe:evt_1"); found {
		t.Fatal("a duplicate row must not count as the original")
	}
	orig := event("shop", time.Now())
	s.Insert(ctx, orig)
	if id, found, _ := s.HasKey(ctx, "shop", "stripe:evt_1"); !found || id != orig.ID {
		t.Fatalf("HasKey = %d %v, want %d true", id, found, orig.ID)
	}
	if _, found, _ := s.HasKey(ctx, "other", "stripe:evt_1"); found {
		t.Fatal("keys are per route")
	}
}

func TestPrune(t *testing.T) {
	s, ctx := open(t), context.Background()
	now := time.Now()
	old, fresh := event("shop", now.Add(-100*time.Hour)), event("shop", now)
	s.Insert(ctx, old)
	s.Insert(ctx, fresh)
	n, err := s.Prune(ctx, now.Add(-72*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d (%v), want 1", n, err)
	}
	if _, err := s.Get(ctx, fresh.ID); err != nil {
		t.Fatal("fresh event was pruned")
	}
}

func TestSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hf.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := event("shop", time.Now())
	if _, err := s.Insert(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	due, _ := s2.Due(context.Background(), time.Now(), 10)
	if len(due) != 1 || due[0].ID != e.ID {
		t.Fatal("an acknowledged event must still be due after a restart")
	}
}

func ids(evs []Event) []int64 {
	out := make([]int64, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}
