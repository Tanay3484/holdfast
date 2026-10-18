package diagnose

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Tanay3484/holdfast/internal/shape"
)

func TestRenamesAndExplain(t *testing.T) {
	// The demo's renamed field, exactly as shape.Diff reports it.
	changes := []shape.Change{
		{Path: "$.data.object.customer", Kind: shape.Added, Now: shape.Object},
		{Path: "$.data.object.customer.id", Kind: shape.Added, Now: shape.String},
		{Path: "$.data.object.customer_id", Kind: shape.Removed, Was: shape.String},
		{Path: "$.data.object.amount", Kind: shape.Retyped, Was: shape.Number, Now: shape.String},
		{Path: "$.data.object.note", Kind: shape.Added, Now: shape.String},
		{Path: "$.data.object.coupon", Kind: shape.Removed, Was: shape.String},
	}
	if got, want := Renames(changes), []Rename{{From: "data.object.customer_id", To: "data.object.customer.id"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Renames = %v, want %v", got, want)
	}
	want := []string{
		"Renamed: the sender now sends data.object.customer.id where it used to send data.object.customer_id.",
		"Different type: data.object.amount was a number and is now a string.",
		"New: data.object.note (a string).",
		"No longer sent: data.object.coupon (it was a string).",
	}
	if got := Explain(changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("Explain =\n%q\nwant\n%q", got, want)
	}
}

func TestPlainWordChecks(t *testing.T) {
	for _, s := range []string{
		"Asha's ₹450 payment couldn't reach Chai & Co.",
		"It can be sent again once the change is made.",
	} {
		if w := Jargon(s); w != "" {
			t.Errorf("Jargon(%q) = %q, want none", s, w)
		}
		if p := Promise(s); p != "" {
			t.Errorf("Promise(%q) = %q, want none", s, p)
		}
	}
	for _, s := range []string{"The notification is being kept safe and reviewed.", "It is being reviewed.",
		"The issue has been identified.", "Our team is on it.", "It will be sent shortly."} {
		if Promise(s) == "" {
			t.Errorf("Promise(%q) found nothing", s)
		}
	}
	if Jargon("The server returned error 503.") == "" {
		t.Error("an error number should count as jargon")
	}
}

func TestModelFixMustNameTheRenamedField(t *testing.T) {
	f := Facts{LastStatus: 400, ChangesKnown: true, Changes: []shape.Change{
		{Path: "$.data.customer.id", Kind: shape.Added, Now: shape.String},
		{Path: "$.data.customer_id", Kind: shape.Removed, Was: shape.String},
	}}
	vague := &fakeModel{reply: `{"cause":"payload_changed","summary":"Asha's payment couldn't be read.","fix":"Ensure the sender updates the id field."}`}
	if d := (Diagnoser{Model: vague}).Diagnose(context.Background(), f, Combined); !strings.Contains(d.Fix, "data.customer.id") {
		t.Fatalf("a fix that misses the rename must be replaced: %q", d.Fix)
	}
	good := &fakeModel{reply: `{"cause":"payload_changed","summary":"Asha's payment couldn't be read.","fix":"Read data.customer.id instead of customer_id."}`}
	if d := (Diagnoser{Model: good}).Diagnose(context.Background(), f, Combined); d.Fix != "Read data.customer.id instead of customer_id." {
		t.Fatalf("a good fix was replaced: %q", d.Fix)
	}
	if Promise("The app will resend the held events.") == "" {
		t.Fatal(`"will resend" is a promise`)
	}
}

func TestSettleCutsTheLoop(t *testing.T) {
	looped := "Kabir’s payment is kept safe.”} 2. Kabir’s payment is kept safe.”} 2. Kabir’s"
	if got := settle(looped); got != "Kabir’s payment is kept safe." {
		t.Fatalf("settle = %q", got)
	}
	if got := settle("Change the app to expect customer.id.” Resend the held events."); got != "Change the app to expect customer.id. Resend the held events." {
		t.Fatalf("stray quote: %q", got)
	}
	for s, want := range map[string]bool{"It is kept safe.": true, "It is kept safe and will": false, "Sent?": true, "": false} {
		if finished(s) != want {
			t.Errorf("finished(%q) = %v", s, !want)
		}
	}
}

func TestOwnerSummaryMayNotNameFields(t *testing.T) {
	for _, s := range []string{"It now uses data.object.customer.id instead.", "The customer_id field moved."} {
		if Jargon(s) == "" {
			t.Errorf("Jargon(%q) found nothing", s)
		}
	}
	if w := Jargon("Kabir's ₹1050 payment to Chai & Co. couldn't be processed."); w != "" {
		t.Errorf("plain sentence flagged: %q", w)
	}
}

func TestSchemaPutsTheSummaryBeforeTheFix(t *testing.T) {
	s := string(schema())
	if !strings.Contains(s, `"maxLength":480`) || strings.Index(s, `"summary"`) > strings.Index(s, `"fix"`) {
		t.Fatalf("schema = %s", s)
	}
	var v map[string]any
	if err := json.Unmarshal(schema(), &v); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}
