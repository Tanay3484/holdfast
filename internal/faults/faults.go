// Package faults builds webhooks with known problems, for the live demo and the evaluation.
package faults

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"
)

// Fault is one kind of problem.
type Fault string

const (
	Healthy        Fault = "healthy"
	WrongSecret    Fault = "wrong_secret"
	StaleTimestamp Fault = "stale_timestamp"
	RenamedField   Fault = "renamed_field"
	AppDown        Fault = "app_down"
	SlowApp        Fault = "slow_app"
	Duplicate      Fault = "duplicate"
)

// Spec describes a fault for the demo buttons.
type Spec struct {
	Fault   Fault
	Label   string // button text
	Explain string // one line under the button
	Level   string // the unlock level that changes how Holdfast handles it ("" if none)
}

// Demo is the list of demo buttons in display order.
var Demo = []Spec{
	{Healthy, "A normal payment", "Everything works. Watch it arrive.", ""},
	{AppDown, "The shop goes down for a few seconds", "Holdfast keeps trying until the shop is back.", "retries"},
	{RenamedField, "The payment service renames a field", "The shop can't read it. Holdfast holds it and finds what changed.", "changes"},
	{Duplicate, "The same payment is sent twice", "Only one copy should reach the shop.", "resend"},
	{WrongSecret, "Signed with the wrong key", "It may not be genuine, so it is held, not passed on.", "signature"},
	{SlowApp, "The shop is too slow to answer", "Holdfast stops waiting, retries, then holds it.", ""},
	{StaleTimestamp, "An old payment is replayed", "The signature is real but an hour old.", "signature"},
}

// Lookup returns the spec for a fault name.
func Lookup(name string) (Spec, bool) {
	for _, s := range Demo {
		if string(s.Fault) == name {
			return s, true
		}
	}
	return Spec{}, false
}

var customers = []struct{ Name, Item string }{
	{"Asha", "2 × Masala chai tin"},
	{"Rohan", "Cardamom chai, 250 g"},
	{"Meera", "Gift box: 4 teas"},
	{"Kabir", "Ginger chai, 500 g"},
	{"Zoya", "Tulsi green tea, 100 g"},
	{"Dev", "Clay cups, set of 6"},
	{"Ishaan", "Assam CTC, 1 kg"},
	{"Nisha", "Kashmiri kahwa, 100 g"},
}

type metadata struct {
	CustomerName string `json:"customer_name"`
	Item         string `json:"item"`
}

type paymentObject struct {
	ID         string   `json:"id"`
	Object     string   `json:"object"`
	Amount     int      `json:"amount"`
	Currency   string   `json:"currency"`
	CustomerID string   `json:"customer_id"`
	Status     string   `json:"status"`
	Metadata   metadata `json:"metadata"`
}

type renamedObject struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Amount   int    `json:"amount"`
	Currency string `json:"currency"`
	Customer struct {
		ID string `json:"id"`
	} `json:"customer"`
	Status   string   `json:"status"`
	Metadata metadata `json:"metadata"`
}

type event[T any] struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Type     string `json:"type"`
	Created  int64  `json:"created"`
	Livemode bool   `json:"livemode"`
	Data     struct {
		Object T `json:"object"`
	} `json:"data"`
}

// Payment builds a Stripe-style payment_intent.succeeded event for the demo shop. With renamed set,
// customer_id is replaced by customer.id, which the demo shop cannot read.
func Payment(rng *rand.Rand, now time.Time, renamed bool) []byte {
	c := customers[rng.IntN(len(customers))]
	id := func(prefix string) string { return prefix + strconv.FormatUint(rng.Uint64(), 36) }
	amount := (150 + rng.IntN(20)*50) * 100
	obj := paymentObject{ID: id("pi_"), Object: "payment_intent", Amount: amount, Currency: "inr",
		CustomerID: id("cus_"), Status: "succeeded", Metadata: metadata{c.Name, c.Item}}

	if renamed {
		ev := event[renamedObject]{ID: id("evt_"), Object: "event", Type: "payment_intent.succeeded", Created: now.Unix()}
		r := renamedObject{ID: obj.ID, Object: obj.Object, Amount: obj.Amount, Currency: obj.Currency,
			Status: obj.Status, Metadata: obj.Metadata}
		r.Customer.ID = obj.CustomerID
		ev.Data.Object = r
		b, _ := json.Marshal(ev)
		return b
	}
	ev := event[paymentObject]{ID: id("evt_"), Object: "event", Type: "payment_intent.succeeded", Created: now.Unix()}
	ev.Data.Object = obj
	b, _ := json.Marshal(ev)
	return b
}

// SignStripe returns a Stripe-Signature header for body signed at t.
func SignStripe(secret string, body []byte, t time.Time) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return fmt.Sprintf("t=%s,v1=%s", ts, mac(secret, ts+"."+string(body)))
}

// SignGitHub returns an X-Hub-Signature-256 header for body.
func SignGitHub(secret string, body []byte) string {
	return "sha256=" + mac(secret, string(body))
}

func mac(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}
