// Package verify checks that a webhook really came from its provider.
//
//	╔══════════════════════════════════════════════════════════════════════╗
//	║  LEVEL 1 · SIGNATURE CHECK                              owner: @human ║
//	║                                                                      ║
//	║  Write GitHub and Stripe below. Unlocks:                             ║
//	║   • the "Is it really from the provider?" panel on every event page  ║
//	║   • the "Signed with the wrong key" and "An old event replayed"      ║
//	║     demo buttons, which are held instead of passed on                ║
//	║                                                                      ║
//	║  Check your progress:  go run ./cmd/unlock                           ║
//	║  Task: specs/001-holdfast-mvp/tasks.md → H1                          ║
//	╚══════════════════════════════════════════════════════════════════════╝
//
// Until this level is unlocked the relay records every signature as "unchecked" and passes the
// event on. Write your own tests in verify_test.go as you go.
package verify

import (
	"errors"
	"time"

	"github.com/Tanay3484/holdfast/internal/wip"
)

// The four ways a signature check can fail. Return these exactly (or wrap them with %w) so the
// relay can tell them apart: errors.Is(err, ErrBadSignature).
var (
	ErrMissingSignature = errors.New("signature header missing")
	ErrMalformedHeader  = errors.New("signature header malformed")
	ErrBadSignature     = errors.New("signature does not match")
	ErrStaleTimestamp   = errors.New("signature timestamp outside tolerance")
)

// GitHub checks the X-Hub-Signature-256 header that GitHub sends with every webhook.
//
// The header looks like "sha256=757107ea0eb2…": the prefix "sha256=" followed by the lowercase hex
// HMAC-SHA256 of the raw request body, keyed with the webhook secret.
// Docs: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
//
// Return:
//   - nil if the signature matches
//   - ErrMissingSignature if header is empty
//   - ErrMalformedHeader if the "sha256=" prefix is missing or the rest is not valid hex
//   - ErrBadSignature if it is well formed but does not match
//
// Compare MACs with hmac.Equal, never with == (a plain comparison leaks timing).
// Known answer: secret "It's a Secret to Everybody", body "Hello, World!" →
// "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17".
func GitHub(secret string, body []byte, header string) error {
	return wip.Locked("verify.GitHub") // TODO(@human, H1): replace this line
}

// Stripe checks the Stripe-Signature header.
//
// The header is a comma-separated list of key=value pairs, for example
// "t=1492774577,v1=5257a869…,v1=9a1b…,v0=6ffbb…". The signed payload is the timestamp, a dot, and
// the raw body: "<t>.<body>". Its HMAC-SHA256 (hex, keyed with the secret) must equal at least one of
// the v1 values; there can be several while a secret is being rotated. Ignore other schemes such as v0.
// Docs: https://docs.stripe.com/webhooks#verify-manually
//
// Return:
//   - nil if a v1 signature matches and the timestamp is within tolerance of now
//   - ErrMissingSignature if header is empty
//   - ErrMalformedHeader if there is no t, t is not an integer, or there is no v1
//   - ErrBadSignature if no v1 matches (check this before the timestamp)
//   - ErrStaleTimestamp if the signature matches but |now - t| > tolerance
func Stripe(secret string, body []byte, header string, now time.Time, tolerance time.Duration) error {
	return wip.Locked("verify.Stripe") // TODO(@human, H1): replace this line
}
