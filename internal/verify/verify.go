// Package verify checks that a webhook really came from its provider.
//
//	╔══════════════════════════════════════════════════════════════════════╗
//	║  LEVEL 1 · SIGNATURE CHECK                              owner: @human║
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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
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
	if header == "" {
		return ErrMissingSignature
	} else if !(strings.HasPrefix(header, "sha256=")) {
		return ErrMalformedHeader
	}
	signature, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return ErrMalformedHeader
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ErrBadSignature
	}
	return nil
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
	if header == "" {
		return ErrMissingSignature
	}

	// 1. Split "t=…,v1=…,v1=…,v0=…" into the timestamp and every v1 signature.
	var ts string
	var v1s []string
	for _, pair := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(pair, "=") // "t=1792050000" → "t", "1792050000", true
		if !ok {
			continue // no "=" in this piece: skip it
		}
		switch key {
		case "t":
			ts = value // one timestamp
		case "v1":
			v1s = append(v1s, value) // can repeat while a secret is rotated
		} // v0 and anything else is ignored
	}
	if ts == "" || len(v1s) == 0 {
		return ErrMalformedHeader
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrMalformedHeader
	}

	// 2. Make our own seal, exactly as in GitHub, but of "<t>.<body>" instead of the body alone.
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + "." + string(body)))
	want := mac.Sum(nil)

	// 3. If any v1 matches our seal, it's genuine. A v1 that isn't hex simply doesn't match.
	matched := false
	for _, v := range v1s {
		got, err := hex.DecodeString(v)
		if err == nil && hmac.Equal(got, want) {
			matched = true
			break
		}
	}
	if !matched {
		return ErrBadSignature
	}

	// 4. Genuine, but is it fresh? Too far from now in either direction could be a replay.
	age := now.Sub(time.Unix(t, 0))
	if age < 0 {
		age = -age
	}
	if age > tolerance {
		return ErrStaleTimestamp
	}
	return nil
}
