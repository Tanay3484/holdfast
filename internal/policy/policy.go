// Package policy decides what happens after each delivery attempt: done, try again later, or hold.
//
//	╔══════════════════════════════════════════════════════════════════════╗
//	║  LEVEL 2 · SMART RETRIES                                owner: @human║
//	║                                                                      ║
//	║  Write Decide below. Unlocks:                                        ║
//	║   • the attempt history on every event page ("Trying again in 4s")   ║
//	║   • the "Shop goes down for a few seconds" demo, which recovers      ║
//	║     on its own instead of being held after one try                   ║
//	║                                                                      ║
//	║  Check your progress:  go run ./cmd/unlock                           ║
//	║  Task: specs/001-holdfast-mvp/tasks.md → H2                          ║
//	╚══════════════════════════════════════════════════════════════════════╝
//
// This is the same idea as a backout threshold on a queue: retry what might succeed later, and move
// what never will out of the way instead of retrying it forever.
//
// Until this level is unlocked the relay tries each event once and holds it on any failure.
package policy

import (
	"strconv"
	"time"
)

// Outcome is what happened on one delivery attempt.
type Outcome struct {
	StatusCode int           // HTTP status the app answered with; 0 if no answer arrived
	TimedOut   bool          // the app did not answer within the delivery timeout
	ConnFailed bool          // could not connect at all (refused, DNS, TLS)
	RetryAfter time.Duration // from the app's Retry-After header; 0 if absent
}

// Action is what to do next.
type Action string

const (
	Delivered Action = "delivered" // the app accepted it; stop
	Retry     Action = "retry"     // try again after Decision.Wait
	Hold      Action = "hold"      // stop trying; keep it safe until a person resends it
)

// Decision is the result of Decide.
type Decision struct {
	Action Action
	Wait   time.Duration // only for Retry
	Reason string        // one plain sentence shown to the user, e.g. "The shop was busy. Trying again in 4s."
}

// Policy holds the knobs. The relay passes a different Policy per route (the demo uses short delays).
type Policy struct {
	MaxAttempts int           // total attempts including the first; after this many, hold
	BaseDelay   time.Duration // wait after the first failed attempt
	MaxDelay    time.Duration // waits never exceed this
}

// Default is the policy for real routes.
func Default() Policy {
	return Policy{MaxAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: time.Minute}
}

// Decide returns what to do after attempt number `attempt` (1 for the first try) ended with o.
//
// Rules, in this order:
//  1. 2xx → Delivered.
//  2. If attempt >= p.MaxAttempts → Hold ("gave up after N tries").
//  3. Retryable → Retry: timeouts, connection failures, 408, 429 and every 5xx.
//     Wait = BaseDelay × 2^(attempt−1), capped at MaxDelay.
//     If o.RetryAfter > 0, wait for RetryAfter instead (still capped at MaxDelay).
//  4. Everything else (other 4xx, 3xx) → Hold at once: sending the same thing again will not help.
//
// Reason must never be empty; it is shown to a non-technical reader, so say what happened in words
// ("The shop's server was down") rather than codes alone.
func (p Policy) Decide(attempt int, o Outcome) (Decision, error) {
	if attempt <= 0 {
		return Decision{
			Action: Hold,
			Reason: "The delivery could not be started.",
		}, nil
	}

	if 200 <= o.StatusCode && o.StatusCode < 300 {
		return Decision{
			Action: Delivered,
			Reason: "The app accepted it.",
		}, nil
	}

	if attempt >= p.MaxAttempts {
		return Decision{
			Action: Hold,
			Reason: "The app still hadn't accepted it after " + strconv.Itoa(attempt) + " tries, so it is held safely.",
		}, nil
	}

	retryable := o.TimedOut || o.ConnFailed || o.StatusCode == 408 || o.StatusCode == 429 || (500 <= o.StatusCode && o.StatusCode < 600)
	if retryable {
		// Double the wait once per earlier try, stopping at the cap before it can overflow.
		wait := p.BaseDelay
		for i := 1; i < attempt; i++ {
			if p.MaxDelay > 0 && wait >= p.MaxDelay/2 {
				wait = p.MaxDelay
				break
			}
			wait *= 2
		}
		if o.RetryAfter > 0 {
			wait = o.RetryAfter // the app said how long to wait
		}
		if p.MaxDelay > 0 && wait > p.MaxDelay {
			wait = p.MaxDelay
		}

		reason := "The app was busy."
		switch {
		case o.TimedOut:
			reason = "The app took too long to answer."
		case o.ConnFailed:
			reason = "The app couldn't be reached."
		case o.StatusCode == 408:
			reason = "The app ran out of time while handling it."
		case o.StatusCode >= 500:
			reason = "The app's server was down."
		}
		reason += " Trying again in " + friendly(wait) + "."

		return Decision{Action: Retry, Wait: wait, Reason: reason}, nil
	}

	return Decision{
		Action: Hold,
		Reason: "The app turned it away, and sending the same thing again won't help, so it is held safely.",
	}, nil
}

// friendly writes a wait the way a person would say it: "4s", "1 min", "1 min 30s".
func friendly(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	mins, secs := int(d/time.Minute), int(d%time.Minute/time.Second)
	if secs == 0 {
		return strconv.Itoa(mins) + " min"
	}
	return strconv.Itoa(mins) + " min " + strconv.Itoa(secs) + "s"
}
