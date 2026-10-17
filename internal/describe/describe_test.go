package describe

import (
	"net/http"
	"testing"
)

func TestWhat(t *testing.T) {
	cases := []struct {
		source, typ, body, want string
	}{
		{"stripe", "payment_intent.succeeded",
			`{"data":{"object":{"amount":45000,"currency":"inr","metadata":{"customer_name":"Asha","item":"2 × Masala chai tin"}}}}`,
			"Payment received · ₹450 from Asha · 2 × Masala chai tin"},
		{"stripe", "charge.refunded", `{"data":{"object":{"amount":1999,"currency":"usd"}}}`, "Refund issued · $19.99"},
		{"stripe", "invoice.upcoming", `{}`, "Invoice upcoming"},
		{"github", "push", `{"repository":{"full_name":"tanay/holdfast"}}`, "Code pushed · tanay/holdfast"},
		{"github", "ping", `{}`, "Connection test from GitHub"},
		{"generic", "order.created", `{}`, "Order created"},
		{"generic", "", `not json`, "Webhook received"},
	}
	for _, c := range cases {
		if got := What(c.source, c.typ, []byte(c.body)); got != c.want {
			t.Errorf("What(%s, %s) = %q, want %q", c.source, c.typ, got, c.want)
		}
	}
}

func TestEventType(t *testing.T) {
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	if got := EventType("github", h, nil); got != "push" {
		t.Errorf("github: %q", got)
	}
	if got := EventType("stripe", nil, []byte(`{"type":"charge.refunded"}`)); got != "charge.refunded" {
		t.Errorf("stripe: %q", got)
	}
	if got := EventType("generic", nil, []byte(`{"event":"order.paid"}`)); got != "order.paid" {
		t.Errorf("generic: %q", got)
	}
}
