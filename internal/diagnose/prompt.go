package diagnose

import (
	"fmt"
	"strings"

	"github.com/Tanay3484/holdfast/internal/ollama"
)

const system = `You explain why a webhook delivery failed. Code has collected the facts below; do not invent any.

Pick the single most likely cause from this list:
%s
Then write:
- "summary": for the business owner, who is not technical. At most two short sentences. Say what happened and that the message is kept safe. Do not say the problem is fixed, being fixed or already identified, and do not promise anything. Never use these words: HTTP, JSON, HMAC, payload, endpoint, status code, API, header, schema, timestamp, or any three-digit error number.
- "fix": for their developer. One or two concrete sentences, naming the field or setting involved if the facts show it.

Never give a percentage, a confidence score, or a number that is not in the facts.`

func prompt(f Facts, mode Mode, rulesCause Cause) []ollama.Message {
	var list strings.Builder
	for _, c := range Causes {
		fmt.Fprintf(&list, "- %s: %s\n", c.Cause, c.Definition)
	}
	return []ollama.Message{
		{Role: "system", Content: fmt.Sprintf(system, list.String())},
		{Role: "user", Content: factsText(f, mode, rulesCause)},
	}
}

func factsText(f Facts, mode Mode, rulesCause Cause) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Event: %s (from %s, type %q)\n", f.What, provider(f), f.EventType)
	fmt.Fprintf(&b, "The app belongs to: %s\n", app(f))
	fmt.Fprintf(&b, "Signature check when it arrived at Holdfast: %s\n", sigText(f.Signature))
	fmt.Fprintf(&b, "Delivery attempts made: %d\n", f.Attempts)
	switch {
	case f.Attempts == 0:
		b.WriteString("It was never sent to the app.\n")
	case f.LastTimedOut:
		b.WriteString("Last attempt: the app did not answer before the timeout.\n")
	case f.LastConnFail:
		b.WriteString("Last attempt: could not connect to the app.\n")
	default:
		fmt.Fprintf(&b, "Last attempt: the app answered HTTP %d.\n", f.LastStatus)
	}
	if strings.TrimSpace(f.Response) != "" {
		fmt.Fprintf(&b, "The app's response body: %s\n", clip(f.Response, 500))
	}
	switch {
	case !f.ChangesKnown:
		b.WriteString("Field comparison with an earlier accepted event: not available.\n")
	case len(f.Changes) == 0:
		b.WriteString("Field comparison with the last accepted event of this type: no differences.\n")
	default:
		b.WriteString("Fields that differ from the last accepted event of this type:\n")
		for _, c := range f.Changes {
			fmt.Fprintf(&b, "- %s: %s (was %q, now %q)\n", c.Path, c.Kind, c.Was, c.Now)
		}
	}
	if mode == Combined {
		fmt.Fprintf(&b, "Rule-based checks suggest: %s. Agree only if the facts support it.\n", rulesCause)
	}
	return b.String()
}

func sigText(sig string) string {
	switch sig {
	case "valid":
		return "valid"
	case "invalid":
		return "did not match"
	case "missing":
		return "no signature was sent"
	case "malformed":
		return "the signature was garbled"
	case "stale":
		return "matched, but the timestamp was too old"
	case "none":
		return "not checked (no secret configured)"
	}
	return "not checked"
}

func schema() map[string]any {
	enum := make([]string, 0, len(Causes))
	for _, c := range Causes {
		enum = append(enum, string(c.Cause))
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cause":   map[string]any{"type": "string", "enum": enum},
			"summary": map[string]any{"type": "string"},
			"fix":     map[string]any{"type": "string"},
		},
		"required": []string{"cause", "summary", "fix"},
	}
}
