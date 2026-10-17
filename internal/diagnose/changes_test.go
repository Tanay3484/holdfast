package diagnose

import (
	"reflect"
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
