package diagnose

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tanay3484/holdfast/internal/ollama"
)

const system = `You explain why a webhook delivery failed. Code has collected the facts below; do not invent any.

Pick the single most likely cause from this list:
%s
Then write two things.

"summary" is read by the business owner, who is not technical. Write two or three plain, flowing sentences, not a list. First say what happened to this specific message, naming the customer and amount from the Event line if there are any. Then say why, in everyday words: if the facts say a field was renamed, say the sender changed how it lays out its messages and the owner's system still expects the old layout, without naming fields (the fix does that). End by saying it is kept safe and can be sent again once their developer has made the change.
Never use these words: HTTP, JSON, HMAC, payload, endpoint, status code, API, header, schema, timestamp, or an error number.
Do not say anyone is reviewing, fixing or investigating it, and do not promise anything.

"fix" is read by their developer. One or two concrete sentences: say which side changed (the sender or the app), name the exact fields or settings from the facts, and say what to change. Then: resend the held events.

Never give a percentage, a confidence score, or a number that is not in the facts.

Example, for a different case:
Facts: Event: Order paid · ₹1200 from Ravi. The app belongs to: Bloom Florists. Last attempt: the app answered HTTP 503 on all 5 tries.
{"cause":"app_down","summary":"Ravi's ₹1200 order payment couldn't reach Bloom Florists because their system wasn't answering at the time. Holdfast is keeping it safe, and it can be sent again once the system is back up.","fix":"The app answered 503 Service Unavailable on all 5 tries. Check that it is running and healthy, then resend the held events."}`

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
		b.WriteString("Compared with the last event of this type that the app accepted, the sender changed its message (the app did not change):\n")
		for _, line := range Explain(f.Changes) {
			fmt.Fprintf(&b, "- %s\n", line)
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

// schema is the JSON format the model must follow. It is written out by hand because the field order
// matters: llama.cpp makes the model write fields in the order listed, and Go would sort map keys into
// cause, fix, summary. Written that way, Gemma put the whole story in the developer's fix and left the
// owner a one-line summary. maxLength stops a reply that loops from running on.
func schema() json.RawMessage {
	enum, _ := json.Marshal(causeNames())
	return json.RawMessage(`{"type":"object","properties":{` +
		`"cause":{"type":"string","enum":` + string(enum) + `},` +
		`"summary":{"type":"string","maxLength":480},` +
		`"fix":{"type":"string","maxLength":300}},` +
		`"required":["cause","summary","fix"]}`)
}

func causeNames() []string {
	names := make([]string, 0, len(Causes))
	for _, c := range Causes {
		names = append(names, string(c.Cause))
	}
	return names
}
