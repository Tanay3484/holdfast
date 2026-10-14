package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tanay3484/holdfast/internal/ollama"
	"github.com/Tanay3484/holdfast/internal/shape"
	"github.com/Tanay3484/holdfast/internal/store"
)

func TestRules(t *testing.T) {
	renamed := []shape.Change{{Path: "$.data.customer_id", Kind: shape.Removed, Was: shape.String}}
	added := []shape.Change{{Path: "$.data.note", Kind: shape.Added, Now: shape.String}}
	cases := []struct {
		name string
		f    Facts
		want Cause
	}{
		{"bad signature at the door", Facts{Signature: store.SigInvalid}, SignatureMismatch},
		{"missing signature", Facts{Signature: store.SigMissing}, SignatureMismatch},
		{"stale signature", Facts{Signature: store.SigStale}, StaleTimestamp},
		{"timeout", Facts{Signature: store.SigValid, LastTimedOut: true}, AppSlow},
		{"refused connection", Facts{LastConnFail: true}, AppDown},
		{"503", Facts{LastStatus: 503}, AppDown},
		{"404", Facts{LastStatus: 404}, EndpointMoved},
		{"301", Facts{LastStatus: 301}, EndpointMoved},
		{"400 with a removed field", Facts{LastStatus: 400, Changes: renamed, ChangesKnown: true}, PayloadChanged},
		{"400 with only an added field", Facts{LastStatus: 400, Changes: added, ChangesKnown: true}, RejectedByApp},
		{"401 that mentions the signature", Facts{Signature: store.SigValid, LastStatus: 401, Response: "Invalid signature"}, SignatureMismatch},
		{"422", Facts{LastStatus: 422, Response: "amount too small"}, RejectedByApp},
		{"never sent", Facts{}, Unknown},
	}
	for _, c := range cases {
		if got := Rules(c.f); got != c.want {
			t.Errorf("%s: Rules = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestEveryCauseHasPlainWords(t *testing.T) {
	f := Facts{App: "Chai & Co.", Source: "stripe", LastStatus: 400}
	for _, c := range Causes {
		if c.Title == "" || c.Definition == "" {
			t.Errorf("%s has no title or definition", c.Cause)
		}
		if w := Jargon(Summary(c.Cause, f)); w != "" {
			t.Errorf("summary for %s uses %q", c.Cause, w)
		}
		if Fix(c.Cause, f) == "" {
			t.Errorf("%s has no fix", c.Cause)
		}
	}
}

// fakeModel answers with a fixed JSON reply or error.
type fakeModel struct {
	reply string
	err   error
	got   []ollama.Message
}

func (m *fakeModel) ChatJSON(_ context.Context, msgs []ollama.Message, _ any, out any) error {
	m.got = msgs
	if m.err != nil {
		return m.err
	}
	return json.Unmarshal([]byte(m.reply), out)
}

func TestDiagnoseUsesTheModelWhenItBehaves(t *testing.T) {
	m := &fakeModel{reply: `{"cause":"payload_changed","summary":"The shop couldn't read this payment because its layout changed. It is kept safe.","fix":"Read customer.id instead of customer_id."}`}
	f := Facts{LastStatus: 400, Response: "missing customer_id"}
	d := Diagnoser{Model: m}.Diagnose(context.Background(), f, Combined)
	if d.By != "model" || d.Cause != PayloadChanged || d.RulesCause != RejectedByApp || d.Title != Title(PayloadChanged) {
		t.Fatalf("diagnosis: %+v", d)
	}
	if !strings.Contains(m.got[1].Content, "Rule-based checks suggest: rejected_by_app") {
		t.Fatal("combined mode must pass the rule-based hint")
	}
	Diagnoser{Model: m}.Diagnose(context.Background(), f, ModelOnly)
	if strings.Contains(m.got[1].Content, "Rule-based") {
		t.Fatal("model-only mode must not see the rule-based hint")
	}
}

func TestDiagnoseFallsBack(t *testing.T) {
	f := Facts{LastStatus: 503}
	cases := map[string]*fakeModel{
		"model down":    {err: errors.New("connection refused")},
		"unknown cause": {reply: `{"cause":"cosmic_rays","summary":"x","fix":"y"}`},
	}
	for name, m := range cases {
		d := Diagnoser{Model: m}.Diagnose(context.Background(), f, Combined)
		if d.By != "rules" || d.Cause != AppDown || d.Note == "" {
			t.Errorf("%s: %+v", name, d)
		}
	}
	if d := (Diagnoser{}).Diagnose(context.Background(), f, Combined); d.By != "rules" {
		t.Error("no model means rules")
	}
}

func TestDiagnoseReplacesAJargonSummary(t *testing.T) {
	m := &fakeModel{reply: `{"cause":"app_down","summary":"The endpoint returned HTTP 503.","fix":"Restart it."}`}
	d := Diagnoser{Model: m}.Diagnose(context.Background(), Facts{LastStatus: 503}, Combined)
	if d.By != "model" || Jargon(d.Summary) != "" || d.Fix != "Restart it." || d.Note == "" {
		t.Fatalf("diagnosis: %+v", d)
	}
}

func TestTemplateNote(t *testing.T) {
	ist, _ := time.LoadLocation("Asia/Kolkata")
	first := time.Date(2026, 10, 14, 8, 32, 0, 0, time.UTC)
	n := NoteFacts{App: "Chai & Co.", Held: 3, First: first, Last: first.Add(4 * time.Minute),
		Whats: map[string]int{"Payment received": 3}, Titles: map[string]int{"The app's server was down": 3}, Loc: ist}
	got := TemplateNote(n)
	want := "Between 14:02 and 14:06 on 14 October, 3 messages for Chai & Co. could not be passed on (3 × payment received)."
	if !strings.HasPrefix(got, want) {
		t.Fatalf("note:\n%s\nwant prefix:\n%s", got, want)
	}
	if Jargon(got) != "" {
		t.Fatalf("template note uses jargon: %s", got)
	}
}

func TestNoteRejectsInventedNumbers(t *testing.T) {
	n := NoteFacts{App: "Chai & Co.", Held: 2, First: time.Now(), Last: time.Now(), Whats: map[string]int{"Payment received": 2}}
	invent := &fakeModel{reply: `{"note":"5 payments were stuck but we fixed it."}`}
	if _, by := (Diagnoser{Model: invent}).Note(context.Background(), n); by != "rules" {
		t.Fatal("a note with a number that is not in the facts must be rejected")
	}
	good := &fakeModel{reply: `{"note":"2 payments for Chai & Co. are being kept safe until the shop can take them."}`}
	if text, by := (Diagnoser{Model: good}).Note(context.Background(), n); by != "model" || !strings.HasPrefix(text, "2 payments") {
		t.Fatalf("good note rejected: %q %s", text, by)
	}
}
