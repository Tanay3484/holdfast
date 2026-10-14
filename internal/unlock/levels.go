package unlock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/replay"
	"github.com/Tanay3484/holdfast/internal/shape"
	"github.com/Tanay3484/holdfast/internal/verify"
	"github.com/Tanay3484/holdfast/internal/wip"
)

// Levels returns the four @human levels in play order.
func Levels() []Level {
	return []Level{signatureLevel(), retriesLevel(), changesLevel(), resendLevel()}
}

// expect compares an error with the one wanted. Locked errors pass straight through.
func expect(got, want error) error {
	if wip.IsLocked(got) {
		return got
	}
	if want == nil {
		if got != nil {
			return fmt.Errorf("expected success, got %q", got)
		}
		return nil
	}
	if !errors.Is(got, want) {
		if got == nil {
			return fmt.Errorf("expected %q, got success", want)
		}
		return fmt.Errorf("expected %q, got %q", want, got)
	}
	return nil
}

func mac(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func signatureLevel() Level {
	const ghSecret, ghBody = "It's a Secret to Everybody", "Hello, World!"
	const ghSig = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	const stSecret, stBody = "whsec_test", `{"id":"evt_1","type":"payment_intent.succeeded"}`
	ts := strconv.FormatInt(now.Unix(), 10)
	good := mac(stSecret, ts+"."+stBody)
	old := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	tol := 5 * time.Minute

	return Level{
		N: 1, Key: "signature", Title: "Signature check",
		File: "internal/verify/verify.go", Task: "H1",
		Reveals: "The \"Is it really from the provider?\" panel, and the wrong-key and replayed-event demos.",
		Checks: []Check{
			{"GitHub: accepts GitHub's own documented example", func() error {
				return expect(verify.GitHub(ghSecret, []byte(ghBody), ghSig), nil)
			}},
			{"GitHub: rejects a body changed after signing", func() error {
				return expect(verify.GitHub(ghSecret, []byte("Hello, World?"), ghSig), verify.ErrBadSignature)
			}},
			{"GitHub: reports a missing header", func() error {
				return expect(verify.GitHub(ghSecret, []byte(ghBody), ""), verify.ErrMissingSignature)
			}},
			{"GitHub: rejects a header without sha256=", func() error {
				return expect(verify.GitHub(ghSecret, []byte(ghBody), ghSig[len("sha256="):]), verify.ErrMalformedHeader)
			}},
			{"Stripe: accepts a fresh, correct signature", func() error {
				return expect(verify.Stripe(stSecret, []byte(stBody), "t="+ts+",v1="+good, now, tol), nil)
			}},
			{"Stripe: accepts when the second v1 matches (secret rotation)", func() error {
				h := "t=" + ts + ",v1=" + mac("whsec_old", ts+"."+stBody) + ",v1=" + good + ",v0=abc"
				return expect(verify.Stripe(stSecret, []byte(stBody), h, now, tol), nil)
			}},
			{"Stripe: rejects a wrong signature", func() error {
				h := "t=" + ts + ",v1=" + mac("whsec_wrong", ts+"."+stBody)
				return expect(verify.Stripe(stSecret, []byte(stBody), h, now, tol), verify.ErrBadSignature)
			}},
			{"Stripe: rejects a correct signature that is 10 minutes old", func() error {
				h := "t=" + old + ",v1=" + mac(stSecret, old+"."+stBody)
				return expect(verify.Stripe(stSecret, []byte(stBody), h, now, tol), verify.ErrStaleTimestamp)
			}},
			{"Stripe: rejects a header with no timestamp", func() error {
				return expect(verify.Stripe(stSecret, []byte(stBody), "v1="+good, now, tol), verify.ErrMalformedHeader)
			}},
			{"Stripe: reports a missing header", func() error {
				return expect(verify.Stripe(stSecret, []byte(stBody), "", now, tol), verify.ErrMissingSignature)
			}},
		},
	}
}

func retriesLevel() Level {
	p := policy.Policy{MaxAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: 10 * time.Second}
	want := func(attempt int, o policy.Outcome, action policy.Action, wait time.Duration) func() error {
		return func() error {
			d, err := p.Decide(attempt, o)
			if err != nil {
				return err
			}
			if d.Action != action {
				return fmt.Errorf("expected %q, got %q", action, d.Action)
			}
			if action == policy.Retry && d.Wait != wait {
				return fmt.Errorf("expected a wait of %s, got %s", wait, d.Wait)
			}
			if d.Reason == "" {
				return errors.New("Reason is empty; say what happened in plain words")
			}
			return nil
		}
	}
	return Level{
		N: 2, Key: "retries", Title: "Smart retries",
		File: "internal/policy/policy.go", Task: "H2",
		Reveals: "The attempt history on every event, and a shop outage that recovers on its own.",
		Checks: []Check{
			{"200 counts as delivered", want(1, policy.Outcome{StatusCode: 200}, policy.Delivered, 0)},
			{"204 counts as delivered", want(1, policy.Outcome{StatusCode: 204}, policy.Delivered, 0)},
			{"503 on the first try waits 2s", want(1, policy.Outcome{StatusCode: 503}, policy.Retry, 2*time.Second)},
			{"Backoff doubles: after the third try, wait 8s", want(3, policy.Outcome{StatusCode: 500}, policy.Retry, 8*time.Second)},
			{"Backoff never passes the cap (10s here)", want(4, policy.Outcome{StatusCode: 502}, policy.Retry, 10*time.Second)},
			{"429 waits for Retry-After", want(1, policy.Outcome{StatusCode: 429, RetryAfter: 7 * time.Second}, policy.Retry, 7*time.Second)},
			{"408 is retried", want(1, policy.Outcome{StatusCode: 408}, policy.Retry, 2*time.Second)},
			{"A timeout is retried", want(2, policy.Outcome{TimedOut: true}, policy.Retry, 4*time.Second)},
			{"A refused connection is retried", want(1, policy.Outcome{ConnFailed: true}, policy.Retry, 2*time.Second)},
			{"400 is held at once", want(1, policy.Outcome{StatusCode: 400}, policy.Hold, 0)},
			{"301 is held at once", want(1, policy.Outcome{StatusCode: 301}, policy.Hold, 0)},
			{"The last allowed try holds instead of retrying", want(5, policy.Outcome{StatusCode: 503}, policy.Hold, 0)},
		},
	}
}

func changesLevel() Level {
	of := func(body string, want shape.Shape) func() error {
		return func() error {
			got, err := shape.Of([]byte(body))
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("for %s\nexpected %v\ngot      %v", body, want, got)
			}
			return nil
		}
	}
	diff := func(good, bad string, want []shape.Change) func() error {
		return func() error {
			g, err := shape.Of([]byte(good))
			if err != nil {
				return err
			}
			b, err := shape.Of([]byte(bad))
			if err != nil {
				return err
			}
			got, err := shape.Diff(g, b)
			if err != nil {
				return err
			}
			if len(got) == 0 && len(want) == 0 {
				return nil
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("expected %v\ngot      %v", want, got)
			}
			return nil
		}
	}
	return Level{
		N: 3, Key: "changes", Title: "What changed",
		File: "internal/shape/shape.go", Task: "H3",
		Reveals: "The \"What changed since the last one that worked\" panel, and sharper diagnoses.",
		Checks: []Check{
			{"Finds every field and its type", of(
				`{"id":"evt_1","n":2,"ok":true,"none":null,"obj":{"s":"x"},"arr":[]}`,
				shape.Shape{"$": shape.Object, "$.id": shape.String, "$.n": shape.Number, "$.ok": shape.Bool,
					"$.none": shape.Null, "$.obj": shape.Object, "$.obj.s": shape.String, "$.arr": shape.Array})},
			{"Merges the items of an array", of(
				`{"items":[{"a":1},{"b":"x"}]}`,
				shape.Shape{"$": shape.Object, "$.items": shape.Array, "$.items[]": shape.Object,
					"$.items[].a": shape.Number, "$.items[].b": shape.String})},
			{"Marks array items that disagree as mixed", of(
				`{"v":[1,"x"]}`,
				shape.Shape{"$": shape.Object, "$.v": shape.Array, "$.v[]": shape.Mixed})},
			{"Rejects text that is not JSON", func() error {
				_, err := shape.Of([]byte("not json"))
				return expect(err, shape.ErrNotJSON)
			}},
			{"Spots a renamed field", diff(
				`{"data":{"customer_id":"c1","amount":450}}`,
				`{"data":{"customer":{"id":"c1"},"amount":450}}`,
				[]shape.Change{
					{Path: "$.data.customer", Kind: shape.Added, Now: shape.Object},
					{Path: "$.data.customer.id", Kind: shape.Added, Now: shape.String},
					{Path: "$.data.customer_id", Kind: shape.Removed, Was: shape.String},
				})},
			{"Spots a field that changed type", diff(
				`{"amount":450}`, `{"amount":"450"}`,
				[]shape.Change{{Path: "$.amount", Kind: shape.Retyped, Was: shape.Number, Now: shape.String}})},
			{"Finds nothing when the shape is the same", diff(
				`{"a":1,"b":[true]}`, `{"b":[false],"a":99}`, nil)},
		},
	}
}

func resendLevel() Level {
	key := func(source string, h http.Header, body, want string) func() error {
		return func() error {
			got, err := replay.DedupeKey(source, h, []byte(body))
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("expected %q, got %q", want, got)
			}
			return nil
		}
	}
	sum := sha256.Sum256([]byte(`{"type":"x"}`))
	hashOfNoID := "sha256:" + hex.EncodeToString(sum[:])
	t0 := now
	plan := func(held []replay.Held, delivered map[string]bool, want []replay.Step) func() error {
		return func() error {
			before := append([]replay.Held(nil), held...)
			got, err := replay.Plan(held, delivered)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(held, before) {
				return errors.New("Plan changed the caller's slice; work on a copy")
			}
			if len(got) != len(want) {
				return fmt.Errorf("expected %d steps, got %d", len(want), len(got))
			}
			for i := range want {
				if got[i].ID != want[i].ID || got[i].Send != want[i].Send {
					return fmt.Errorf("step %d: expected event #%d send=%v, got event #%d send=%v",
						i+1, want[i].ID, want[i].Send, got[i].ID, got[i].Send)
				}
				if !got[i].Send && got[i].Reason == "" {
					return fmt.Errorf("step %d: skipped without a Reason", i+1)
				}
			}
			return nil
		}
	}
	return Level{
		N: 4, Key: "resend", Title: "Safe resend",
		File: "internal/replay/replay.go", Task: "H4",
		Reveals: "The Resend buttons, and a duplicate delivery that never reaches the shop twice.",
		Checks: []Check{
			{"GitHub copies share the delivery ID", key("github",
				http.Header{"X-Github-Delivery": {"72d3162e-cc78-11e3-81ab-4c9367dc0958"}}, `{}`,
				"github:72d3162e-cc78-11e3-81ab-4c9367dc0958")},
			{"Stripe copies share the event id", key("stripe", nil, `{"id":"evt_123","type":"x"}`, "stripe:evt_123")},
			{"Anything else falls back to a hash of the body", key("generic", nil, "hello",
				"sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")},
			{"Stripe without an id falls back to the hash", key("stripe", nil, `{"type":"x"}`, hashOfNoID)},
			{"Plan keeps arrival order", plan(
				[]replay.Held{{ID: 3, DedupeKey: "c", ReceivedAt: t0.Add(2 * time.Second)},
					{ID: 1, DedupeKey: "a", ReceivedAt: t0}, {ID: 2, DedupeKey: "b", ReceivedAt: t0.Add(time.Second)}},
				nil,
				[]replay.Step{{ID: 1, Send: true}, {ID: 2, Send: true}, {ID: 3, Send: true}})},
			{"Plan skips what was already delivered", plan(
				[]replay.Held{{ID: 1, DedupeKey: "a", ReceivedAt: t0}, {ID: 2, DedupeKey: "b", ReceivedAt: t0.Add(time.Second)}},
				map[string]bool{"a": true},
				[]replay.Step{{ID: 1, Send: false}, {ID: 2, Send: true}})},
			{"Plan sends a duplicate only once", plan(
				[]replay.Held{{ID: 1, DedupeKey: "a", ReceivedAt: t0}, {ID: 2, DedupeKey: "a", ReceivedAt: t0.Add(time.Second)}},
				nil,
				[]replay.Step{{ID: 1, Send: true}, {ID: 2, Send: false}})},
			{"Plan never resends a bad signature", plan(
				[]replay.Held{{ID: 1, DedupeKey: "a", ReceivedAt: t0, SignatureFailed: true}},
				nil,
				[]replay.Step{{ID: 1, Send: false}})},
			{"An empty key is never a duplicate", plan(
				[]replay.Held{{ID: 1, ReceivedAt: t0}, {ID: 2, ReceivedAt: t0.Add(time.Second)}},
				nil,
				[]replay.Step{{ID: 1, Send: true}, {ID: 2, Send: true}})},
			{"A plan of nothing is empty", plan(nil, nil, nil)},
		},
	}
}
