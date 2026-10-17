// Package diagnose works out why a held event failed.
//
// Code collects the facts and has its own rule-based answer. The model reads the same facts, picks a
// cause from a fixed list and writes the explanation. Code then checks what the model wrote: the cause
// must be on the list, the client-facing summary must be free of jargon, and nothing it says replaces
// a number that code computed. If the model is slow, down or wrong, the rule-based answer is used.
package diagnose

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tanay3484/holdfast/internal/ollama"
	"github.com/Tanay3484/holdfast/internal/shape"
	"github.com/Tanay3484/holdfast/internal/store"
)

// Cause is one of a fixed set of reasons.
type Cause string

const (
	SignatureMismatch Cause = "signature_mismatch"
	StaleTimestamp    Cause = "stale_timestamp"
	PayloadChanged    Cause = "payload_changed"
	AppDown           Cause = "app_down"
	AppSlow           Cause = "app_slow"
	EndpointMoved     Cause = "endpoint_moved"
	RejectedByApp     Cause = "rejected_by_app"
	Unknown           Cause = "unknown"
)

// Causes is every cause with the definition the model is given.
var Causes = []struct {
	Cause      Cause
	Title      string // shown as the card heading
	Definition string // given to the model
}{
	{SignatureMismatch, "The signature didn't match", "the signature check failed, either at Holdfast on arrival or inside the app (wrong secret, or the app changed the body before checking it)"},
	{StaleTimestamp, "The event was too old to trust", "the signature was valid but its timestamp was too old, so it may be a replay"},
	{PayloadChanged, "The message arrived in a new layout", "the sender changed the layout of its messages (a field the app needs was renamed, removed or changed type) and the app rejected it"},
	{AppDown, "The app's server was down", "the app could not be reached or answered with a server error (5xx)"},
	{AppSlow, "The app took too long to answer", "the app did not answer before the timeout"},
	{EndpointMoved, "The app's address has changed", "the app answered not found, gone, or a redirect: the destination URL is wrong"},
	{RejectedByApp, "The app turned it away", "the app refused it (4xx) for a reason not covered above, such as its own validation"},
	{Unknown, "Something unexpected went wrong", "none of the above fits the facts"},
}

// Title returns the plain heading for a cause.
func Title(c Cause) string {
	for _, x := range Causes {
		if x.Cause == c {
			return x.Title
		}
	}
	return Title(Unknown)
}

func valid(c Cause) bool {
	for _, x := range Causes {
		if x.Cause == c {
			return true
		}
	}
	return false
}

// Facts is everything code knows about a held event.
type Facts struct {
	Source       string
	EventType    string
	What         string // describe.What
	App          string // the client's name, or "" for "your app"
	Signature    string // store.Sig*
	Attempts     int
	LastStatus   int
	LastTimedOut bool
	LastConnFail bool
	Response     string // the app's answer, truncated
	Changes      []shape.Change
	ChangesKnown bool // false when level 3 is locked or there is no earlier accepted event
}

// Diagnosis is what the event page shows.
type Diagnosis struct {
	Cause      Cause  `json:"cause"`
	Title      string `json:"title"`
	Summary    string `json:"summary"` // for the client: plain words
	Fix        string `json:"fix"`     // for the developer
	By         string `json:"by"`      // "model" or "rules"
	RulesCause Cause  `json:"rules_cause"`
	Note       string `json:"note,omitempty"`        // why the model's answer was not used, if it wasn't
	ModelError string `json:"model_error,omitempty"` // for the logs
}

// Mode chooses who answers. The product uses Combined; the evaluation compares all three.
type Mode string

const (
	RulesOnly Mode = "rules"
	ModelOnly Mode = "model"
	Combined  Mode = "combined"
)

// Model is the part of the Ollama client we use, so tests can fake it.
type Model interface {
	ChatJSON(ctx context.Context, messages []ollama.Message, schema any, out any) error
}

// Diagnoser answers with a model if it has one.
type Diagnoser struct {
	Model Model // nil means rules only
}

// Rules is the rule-based cause.
func Rules(f Facts) Cause {
	switch f.Signature {
	case store.SigInvalid, store.SigMissing, store.SigMalformed:
		return SignatureMismatch
	case store.SigStale:
		return StaleTimestamp
	}
	switch s := f.LastStatus; {
	case f.LastTimedOut:
		return AppSlow
	case f.LastConnFail, s >= 500:
		return AppDown
	case s == 404, s == 410, s >= 300 && s < 400:
		return EndpointMoved
	case s >= 400:
		if breaking(f.Changes) {
			return PayloadChanged
		}
		if strings.Contains(strings.ToLower(f.Response), "signature") {
			return SignatureMismatch
		}
		return RejectedByApp
	}
	return Unknown
}

func breaking(changes []shape.Change) bool {
	for _, c := range changes {
		if c.Kind == shape.Removed || c.Kind == shape.Retyped {
			return true
		}
	}
	return false
}

// Diagnose answers in the given mode.
func (d Diagnoser) Diagnose(ctx context.Context, f Facts, mode Mode) Diagnosis {
	rc := Rules(f)
	rules := Diagnosis{Cause: rc, Title: Title(rc), Summary: Summary(rc, f), Fix: Fix(rc, f), By: "rules", RulesCause: rc}
	if mode == RulesOnly || d.Model == nil {
		return rules
	}

	var out struct {
		Cause   string `json:"cause"`
		Summary string `json:"summary"`
		Fix     string `json:"fix"`
	}
	if err := d.Model.ChatJSON(ctx, prompt(f, mode, rc), schema(), &out); err != nil {
		rules.Note = "The AI model did not answer, so this is the rule-based answer."
		rules.ModelError = err.Error()
		return rules
	}
	c := Cause(out.Cause)
	if !valid(c) {
		rules.Note = fmt.Sprintf("The AI model answered with an unknown cause (%q), so this is the rule-based answer.", out.Cause)
		return rules
	}
	dg := Diagnosis{Cause: c, Title: Title(c), Summary: strings.TrimSpace(out.Summary), Fix: strings.TrimSpace(out.Fix),
		By: "model", RulesCause: rc}
	switch {
	case dg.Summary == "" || len(dg.Summary) > 450:
		dg.Summary = Summary(c, f)
		dg.Note = "The AI's summary was empty or too long, so a plain one was put in its place."
	case Jargon(dg.Summary) != "":
		dg.Summary = Summary(c, f)
		dg.Note = "The AI's summary used technical words, so a plain one was put in its place."
	case Promise(dg.Summary) != "":
		dg.Summary = Summary(c, f)
		dg.Note = "The AI's summary promised something nobody has done, so a plain one was put in its place."
	}
	if dg.Fix == "" || !namesRenamedField(dg.Fix, f) {
		dg.Fix = Fix(c, f)
	}
	return dg
}

// namesRenamedField is true unless code found a rename and the model's fix does not mention where
// the field moved to: a fix that misses the one fact that matters is worse than the rules' fix.
func namesRenamedField(fix string, f Facts) bool {
	rs := Renames(f.Changes)
	if !f.ChangesKnown || len(rs) == 0 {
		return true
	}
	// Accept the full path or its last two segments ("customer.id"); one segment ("id") proves nothing.
	segs := strings.Split(rs[0].To, ".")
	short := strings.Join(segs[max(0, len(segs)-2):], ".")
	return strings.Contains(fix, rs[0].To) || strings.Contains(fix, short)
}

// Amounts such as ₹450 are fine; error numbers only count when they read like one.
var jargon = regexp.MustCompile(`(?i)\b(http|https|json|hmac|payload|endpoint|status code|api|header|schema|timestamp|(error|status|code)\s+[45]\d\d)\b`)

// Jargon returns the first technical word in s, or "".
func Jargon(s string) string {
	return jargon.FindString(s)
}

// Nobody is reviewing, fixing or investigating anything unless a person says so, so the client
// summary may not claim it.
var promise = regexp.MustCompile(`(?i)\b(being (reviewed|fixed|looked (at|into)|investigated|resolved|handled|worked on)|(has|have) been (fixed|resolved|identified|reviewed)|will be (fixed|resolved|sent|processed|delivered)|will (re-?send|resend|retry|fix|deliver|send it)|we are|we're|our team|investigat\w*|review(ed|ing)|is resolved)\b`)

// Promise returns the first phrase in s that promises action nobody has taken, or "".
func Promise(s string) string {
	return promise.FindString(s)
}

func app(f Facts) string {
	if f.App != "" {
		return f.App
	}
	return "your app"
}

func provider(f Facts) string {
	switch f.Source {
	case "github":
		return "GitHub"
	case "stripe":
		return "Stripe"
	}
	return "the sender"
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Summary is the rule-based plain-language explanation for a cause.
func Summary(c Cause, f Facts) string {
	a := capital(app(f))
	safe := " It's kept safe here and can be sent again once that's fixed."
	switch c {
	case SignatureMismatch:
		if f.Signature == store.SigValid {
			return a + " couldn't confirm this message was genuine and refused it." + safe
		}
		return "Holdfast couldn't confirm this really came from " + provider(f) + ", so it kept it aside instead of passing it on. Nothing has been lost."
	case StaleTimestamp:
		return "This message was older than it should be, so Holdfast kept it aside in case someone was replaying an old one. Nothing has been lost."
	case PayloadChanged:
		return capital(provider(f)) + " changed the layout of its messages, and " + app(f) + " still expects the old one, so it couldn't read this one." + safe
	case AppDown:
		return a + "'s server wasn't answering, so this couldn't be passed on." + safe
	case AppSlow:
		return a + " took too long to answer, so Holdfast stopped waiting." + safe
	case EndpointMoved:
		return "The address " + app(f) + " uses for these messages doesn't seem to exist any more." + safe
	case RejectedByApp:
		return a + " refused this message." + safe
	}
	return "Something unexpected stopped this message from being passed on." + safe
}

// Fix is the rule-based suggestion for the developer.
func Fix(c Cause, f Facts) string {
	switch c {
	case SignatureMismatch:
		if f.Signature == store.SigValid {
			return "Holdfast checked the signature on arrival and it was correct, so the app is either changing the body before checking it (for example parsing the JSON first) or using a different secret. Verify against the raw request body."
		}
		return "Check that the signing secret configured in Holdfast matches the one in the " + provider(f) + " dashboard. If it does, this request may not be from " + provider(f) + "."
	case StaleTimestamp:
		return "The signature was valid but its timestamp was too old. Check the sender's clock, or whether an old event is being replayed."
	case PayloadChanged:
		if rs := Renames(f.Changes); f.ChangesKnown && len(rs) > 0 {
			r := rs[0]
			return fmt.Sprintf("%s now sends %s instead of %s, and the app still reads the old field. Read the new one (or accept both), deploy, then resend the held events.",
				capital(provider(f)), r.To, r.From)
		}
		if f.ChangesKnown && len(f.Changes) > 0 {
			return "These fields changed since the last event the app accepted: " + ChangeList(f.Changes) + ". Update the handler to read the new layout, then resend."
		}
		return "Compare this body with one the app accepted. The app answered: " + clip(f.Response, 200)
	case AppDown:
		if f.LastConnFail {
			return "Holdfast could not connect to the app. Check that it is running and reachable, then resend."
		}
		return fmt.Sprintf("The app answered %d. Check its logs and that it is running, then resend.", f.LastStatus)
	case AppSlow:
		return "The app did not answer before the timeout. Acknowledge webhooks straight away and do slow work in the background, then resend."
	case EndpointMoved:
		return fmt.Sprintf("The app answered %d. Check the destination URL configured in Holdfast.", f.LastStatus)
	case RejectedByApp:
		return fmt.Sprintf("The app answered %d: %s. Check its validation for this event type.", f.LastStatus, clip(f.Response, 200))
	}
	return "Look at the attempts below and the app's logs around the same time."
}

// ChangeList renders changes as "customer_id removed, customer.id added".
func ChangeList(changes []shape.Change) string {
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		p := strings.TrimPrefix(strings.TrimPrefix(c.Path, "$"), ".")
		switch c.Kind {
		case shape.Retyped:
			parts = append(parts, fmt.Sprintf("%s changed from %s to %s", p, c.Was, c.Now))
		default:
			parts = append(parts, p+" "+string(c.Kind))
		}
	}
	return strings.Join(parts, ", ")
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
