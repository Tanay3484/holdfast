package policy

import (
	"strings"
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	p := Policy{MaxAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: time.Minute}
	cases := []struct {
		name    string
		attempt int
		o       Outcome
		action  Action
		wait    time.Duration
	}{
		{"accepted", 1, Outcome{StatusCode: 202}, Delivered, 0},
		{"accepted even on the last try", 5, Outcome{StatusCode: 200}, Delivered, 0},
		{"server error", 1, Outcome{StatusCode: 500}, Retry, 2 * time.Second},
		{"doubles", 4, Outcome{StatusCode: 503}, Retry, 16 * time.Second},
		{"Retry-After beats backoff", 3, Outcome{StatusCode: 429, RetryAfter: 30 * time.Second}, Retry, 30 * time.Second},
		{"Retry-After is capped", 1, Outcome{StatusCode: 429, RetryAfter: time.Hour}, Retry, time.Minute},
		{"gives up", 5, Outcome{StatusCode: 503}, Hold, 0},
		{"404", 1, Outcome{StatusCode: 404}, Hold, 0},
		{"never started", 0, Outcome{}, Hold, 0},
	}
	for _, c := range cases {
		d, err := p.Decide(c.attempt, c.o)
		if err != nil || d.Action != c.action || d.Wait != c.wait || d.Reason == "" {
			t.Errorf("%s: got %+v %v, want %s after %s", c.name, d, err, c.action, c.wait)
		}
	}
}

func TestBackoffNeverOverflows(t *testing.T) {
	p := Policy{MaxAttempts: 1000, BaseDelay: time.Second, MaxDelay: 10 * time.Minute}
	d, _ := p.Decide(999, Outcome{TimedOut: true})
	if d.Action != Retry || d.Wait != 10*time.Minute {
		t.Fatalf("attempt 999: %+v", d)
	}
}

func TestReasonsReadNaturally(t *testing.T) {
	p := Policy{MaxAttempts: 5, BaseDelay: 90 * time.Second, MaxDelay: 10 * time.Minute}
	d, _ := p.Decide(1, Outcome{StatusCode: 503})
	if !strings.HasSuffix(d.Reason, "Trying again in 1 min 30s.") {
		t.Fatalf("reason: %q", d.Reason)
	}
	for in, want := range map[time.Duration]string{4 * time.Second: "4s", time.Minute: "1 min", 150 * time.Second: "2 min 30s"} {
		if got := friendly(in); got != want {
			t.Errorf("friendly(%s) = %q, want %q", in, got, want)
		}
	}
}
