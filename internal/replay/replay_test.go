package replay

import (
	"net/http"
	"testing"
	"time"
)

func TestDedupeKey(t *testing.T) {
	gh := http.Header{}
	gh.Set("X-GitHub-Delivery", "abc-123")
	cases := []struct {
		name, source string
		h            http.Header
		body, want   string
	}{
		{"github delivery id", "github", gh, `{"zen":"x"}`, "github:abc-123"},
		{"github without the header", "github", http.Header{}, "hello", "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"stripe event id", "stripe", nil, `{"id":"evt_9","type":"x"}`, "stripe:evt_9"},
		{"stripe body that is not JSON", "stripe", nil, "hello", "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"generic", "generic", nil, "hello", "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
	}
	for _, c := range cases {
		if got, err := DedupeKey(c.source, c.h, []byte(c.body)); err != nil || got != c.want {
			t.Errorf("%s: got %q %v, want %q", c.name, got, err, c.want)
		}
	}
}

func TestPlan(t *testing.T) {
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	held := []Held{
		{ID: 5, DedupeKey: "b", ReceivedAt: t0.Add(time.Minute)},
		{ID: 2, DedupeKey: "a", ReceivedAt: t0, SignatureFailed: true},
		{ID: 3, DedupeKey: "a", ReceivedAt: t0}, // same time as #2: the tie breaks on ID
		{ID: 4, DedupeKey: "a", ReceivedAt: t0.Add(time.Second)},
		{ID: 6, DedupeKey: "done", ReceivedAt: t0.Add(2 * time.Minute)},
		{ID: 7, ReceivedAt: t0.Add(3 * time.Minute)},
		{ID: 8, ReceivedAt: t0.Add(4 * time.Minute)},
	}
	got, err := Plan(held, map[string]bool{"done": true})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id   int64
		send bool
	}{{2, false}, {3, true}, {4, false}, {5, true}, {6, false}, {7, true}, {8, true}}
	if len(got) != len(want) {
		t.Fatalf("got %d steps, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Send != w.send || (!w.send && got[i].Reason == "") {
			t.Errorf("step %d: got %+v, want #%d send=%v", i+1, got[i], w.id, w.send)
		}
	}
	if got[2].Reason != "a copy of event #3" {
		t.Errorf("a forged first copy must not block the genuine one: %q", got[2].Reason)
	}
	if held[0].ID != 5 {
		t.Error("Plan reordered the caller's slice")
	}
}
