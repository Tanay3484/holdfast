// Package replay decides which held events are safe to send again, and in what order.
//
//	╔══════════════════════════════════════════════════════════════════════╗
//	║  LEVEL 4 · SAFE RESEND                                  owner: @human ║
//	║                                                                      ║
//	║  Write DedupeKey and Plan below. Unlocks:                            ║
//	║   • the "Resend" and "Resend everything held" buttons                ║
//	║   • the "Provider sends the same order twice" demo, where the        ║
//	║     second copy is recognised and never reaches the shop             ║
//	║                                                                      ║
//	║  Check your progress:  go run ./cmd/unlock                           ║
//	║  Task: specs/001-holdfast-mvp/tasks.md → H4                          ║
//	╚══════════════════════════════════════════════════════════════════════╝
//
// Providers deliver at least once, so the same event can arrive twice. A resend must never turn one
// payment into two.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"
)

// DedupeKey returns a key that is the same for every copy of one provider event.
//
//   - source "github": "github:" + the X-GitHub-Delivery header, if present
//   - source "stripe": "stripe:" + the top-level "id" string in the JSON body, if present
//   - otherwise, or if that id is missing: "sha256:" + lowercase hex SHA-256 of the body
//
// Example: DedupeKey("generic", nil, []byte("hello")) →
// "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824".
//
// Until this is written the relay forwards every copy it receives.
func DedupeKey(source string, headers http.Header, body []byte) (string, error) {
	switch source {
	case "github":
		if id := headers.Get("X-GitHub-Delivery"); id != "" {
			return "github:" + id, nil
		}
	case "stripe":
		var ev struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &ev) == nil && ev.ID != "" {
			return "stripe:" + ev.ID, nil
		}
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Held is one event waiting to be resent.
type Held struct {
	ID              int64
	DedupeKey       string
	ReceivedAt      time.Time
	SignatureFailed bool // the signature check failed when it arrived
}

// Step is the verdict for one held event.
type Step struct {
	ID     int64
	Send   bool
	Reason string // why not, in plain words; empty when Send is true
}

// Plan returns one Step per held event, in the order they should be sent.
//
// Rules:
//   - order by ReceivedAt, oldest first; ties by ID
//   - SignatureFailed → do not send ("the signature did not match, so it may not be from the provider")
//   - DedupeKey already in delivered → do not send ("this was already delivered")
//   - the same DedupeKey appears more than once in held → send only the first; the rest are
//     "a copy of event #<id>"
//   - an empty DedupeKey never counts as a duplicate
//   - do not reorder or modify the caller's slice
//
// An empty input returns an empty (non-nil or nil, either is fine) slice and no error.
func Plan(held []Held, delivered map[string]bool) ([]Step, error) {
	// Work on a copy so the caller's slice keeps its order.
	ordered := slices.Clone(held)
	slices.SortStableFunc(ordered, func(a, b Held) int {
		if c := a.ReceivedAt.Compare(b.ReceivedAt); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})

	steps := make([]Step, 0, len(ordered))
	firstWith := map[string]int64{} // dedupe key → the event that will carry it
	for _, h := range ordered {
		step := Step{ID: h.ID}
		switch first, seen := firstWith[h.DedupeKey]; {
		case h.SignatureFailed:
			step.Reason = "the signature did not match, so it may not be from the provider"
		case h.DedupeKey != "" && delivered[h.DedupeKey]:
			step.Reason = "this was already delivered"
		case h.DedupeKey != "" && seen:
			step.Reason = fmt.Sprintf("a copy of event #%d", first)
		default:
			step.Send = true
			if h.DedupeKey != "" {
				firstWith[h.DedupeKey] = h.ID
			}
		}
		steps = append(steps, step)
	}
	return steps, nil
}
