package diagnose

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tanay3484/holdfast/internal/ollama"
)

// NoteFacts is what code knows about the events currently held for one client.
type NoteFacts struct {
	App    string
	Held   int
	First  time.Time
	Last   time.Time
	Whats  map[string]int // "Payment received" → 3
	Titles map[string]int // cause title → count
	Loc    *time.Location
}

// TemplateNote is the plain note code writes on its own.
func TemplateNote(n NoteFacts) string {
	loc := n.Loc
	if loc == nil {
		loc = time.UTC
	}
	who := n.App
	if who == "" {
		who = "your app"
	}
	when := "At " + n.First.In(loc).Format("15:04")
	if n.Held > 1 && n.Last.Sub(n.First) >= time.Minute {
		when = "Between " + n.First.In(loc).Format("15:04") + " and " + n.Last.In(loc).Format("15:04")
	}
	when += " on " + n.First.In(loc).Format("2 January")
	msgs := "message"
	if n.Held != 1 {
		msgs = "messages"
	}
	s := fmt.Sprintf("%s, %d %s for %s could not be passed on", when, n.Held, msgs, who)
	if what := counted(n.Whats); what != "" {
		s += " (" + what + ")"
	}
	s += "."
	if why := keys(n.Titles); why != "" {
		s += " What happened: " + why + "."
	}
	return s + " None of them have been lost: Holdfast is keeping them safe, and they can be sent again once the problem is fixed."
}

func counted(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("%d × %s", m[k], strings.ToLower(k[:1])+k[1:]))
	}
	return strings.Join(parts, ", ")
}

func keys(m map[string]int) string {
	ks := sortedKeys(m)
	for i, k := range ks {
		ks[i] = strings.ToLower(k[:1]) + k[1:]
	}
	return strings.Join(ks, "; ")
}

func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			ks = append(ks, k)
		}
	}
	sort.Slice(ks, func(i, j int) bool {
		if m[ks[i]] != m[ks[j]] {
			return m[ks[i]] > m[ks[j]]
		}
		return ks[i] < ks[j]
	})
	return ks
}

var digits = regexp.MustCompile(`\d+`)

// Note asks the model to rewrite the template note warmly. Code keeps the facts: every number in the
// model's note must already be in the template, and no jargon is allowed. Otherwise the template wins.
func (d Diagnoser) Note(ctx context.Context, n NoteFacts) (note string, by string) {
	tmpl := TemplateNote(n)
	if d.Model == nil || n.Held == 0 {
		return tmpl, "rules"
	}
	var out struct {
		Note string `json:"note"`
	}
	msgs := []ollama.Message{
		{Role: "system", Content: "Rewrite this note from a developer to their non-technical client so it reads warmly and calmly. " +
			"Keep every fact, time and number exactly as given and add none. At most three sentences. No technical words."},
		{Role: "user", Content: tmpl},
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string", "maxLength": 600}}, "required": []string{"note"}}
	if err := d.Model.ChatJSON(ctx, msgs, schema, &out); err != nil {
		return tmpl, "rules"
	}
	note = strings.TrimSpace(out.Note)
	if note == "" || len(note) > 600 || Jargon(note) != "" || !numbersFrom(note, tmpl) {
		return tmpl, "rules"
	}
	return note, "model"
}

func numbersFrom(note, source string) bool {
	allowed := map[string]bool{}
	for _, d := range digits.FindAllString(source, -1) {
		allowed[d] = true
	}
	for _, d := range digits.FindAllString(note, -1) {
		if !allowed[d] {
			return false
		}
	}
	return true
}
