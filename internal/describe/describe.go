// Package describe turns a raw webhook into a short line a non-technical person understands.
package describe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// EventType reads the provider's event name: the X-GitHub-Event header for GitHub, the top-level
// "type" (or "event") field for everyone else.
func EventType(source string, h http.Header, body []byte) string {
	if source == "github" {
		return h.Get("X-GitHub-Event")
	}
	var top struct {
		Type  string `json:"type"`
		Event string `json:"event"`
	}
	if json.Unmarshal(body, &top) != nil {
		return ""
	}
	if top.Type != "" {
		return top.Type
	}
	return top.Event
}

// What returns a one-line description, e.g. "Payment received · ₹450 · Asha · 2 × Masala chai tin".
func What(source, eventType string, body []byte) string {
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	switch source {
	case "github":
		return github(eventType, doc)
	case "stripe":
		return stripe(eventType, doc)
	}
	if eventType != "" {
		return Humanize(eventType)
	}
	return "Webhook received"
}

func github(event string, doc map[string]any) string {
	repo := str(doc, "repository", "full_name")
	switch event {
	case "ping":
		return "Connection test from GitHub"
	case "push":
		return join("Code pushed", repo)
	case "issues":
		return join("Issue "+str(doc, "action"), str(doc, "issue", "title"))
	case "pull_request":
		return join("Pull request "+str(doc, "action"), str(doc, "pull_request", "title"))
	case "star":
		return join("Repository starred", repo)
	case "":
		return "GitHub event"
	}
	return join("GitHub: "+Humanize(event), repo)
}

func stripe(event string, doc map[string]any) string {
	obj, _ := dig(doc, "data", "object").(map[string]any)
	label := map[string]string{
		"payment_intent.succeeded":      "Payment received",
		"payment_intent.payment_failed": "Payment failed",
		"charge.succeeded":              "Payment received",
		"charge.refunded":               "Refund issued",
		"checkout.session.completed":    "Checkout completed",
		"customer.created":              "New customer",
		"invoice.paid":                  "Invoice paid",
		"invoice.payment_failed":        "Invoice payment failed",
	}[event]
	if label == "" {
		label = Humanize(event)
	}
	parts := []string{label}
	if amt, ok := dig(obj, "amount").(float64); ok {
		parts = append(parts, money(amt, str(obj, "currency")))
	}
	if name := str(obj, "metadata", "customer_name"); name != "" {
		parts = append(parts, name)
	}
	if item := str(obj, "metadata", "item"); item != "" {
		parts = append(parts, item)
	}
	return strings.Join(parts, " · ")
}

// Humanize turns "payment_intent.succeeded" into "Payment intent succeeded".
func Humanize(s string) string {
	s = strings.NewReplacer("_", " ", ".", " ", "-", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func money(minor float64, currency string) string {
	symbol := map[string]string{"inr": "₹", "usd": "$", "eur": "€", "gbp": "£"}[strings.ToLower(currency)]
	v := minor / 100
	amount := fmt.Sprintf("%.2f", v)
	if v == float64(int64(v)) {
		amount = fmt.Sprintf("%d", int64(v))
	}
	if symbol == "" {
		return strings.TrimSpace(amount + " " + strings.ToUpper(currency))
	}
	return symbol + amount
}

func join(a, b string) string {
	if b == "" {
		return a
	}
	return a + " · " + b
}

func dig(v any, path ...string) any {
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func str(v any, path ...string) string {
	s, _ := dig(v, path...).(string)
	return s
}
