package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Tanay3484/holdfast/internal/demo"
	"github.com/Tanay3484/holdfast/internal/describe"
	"github.com/Tanay3484/holdfast/internal/diagnose"
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/shape"
	"github.com/Tanay3484/holdfast/internal/store"
	"github.com/Tanay3484/holdfast/internal/unlock"
)

// ---------------------------------------------------------------------------------------------
// The board: verdict, client note, timeline and (for the demo) the shop's order book

type verdict struct {
	Tone  string // ok | held | busy | quiet
	Title string
	Sub   string
}

type eventView struct {
	ID          int64
	What        string
	Status      string
	StatusLabel string
	Reason      string
	Received    time.Time
	Diagnosis   *diagnose.Diagnosis
	Attempts    int
}

type boardView struct {
	Route      string
	Client     string
	Noun       string // "payment" or "event"
	Public     bool
	Verdict    verdict
	Counts     map[string]int
	Held       int
	Note       string
	NoteByAI   bool
	Events     []eventView
	IsDemo     bool
	ShopStatus string
	Orders     []demo.Order
	Recorded   int
	Repeats    int
}

func (s *Server) board(ctx context.Context, route string) (*boardView, error) {
	rt := s.Relay.Routes[route]
	b := &boardView{Route: route, Client: rt.Client, Noun: "event", Public: rt.Public, IsDemo: route == demo.RouteName && s.Shop != nil}
	if b.Client == "" {
		b.Client = rt.Name
	}
	if b.IsDemo {
		b.Noun = "payment"
		b.ShopStatus = s.Shop.Status()
		b.Orders = s.Shop.Orders(8)
		b.Recorded, b.Repeats = s.Shop.Totals()
	}
	var err error
	if b.Counts, err = s.Relay.Store.Counts(ctx, route); err != nil {
		return nil, err
	}
	b.Held = b.Counts[store.Held]
	evs, err := s.Relay.Store.List(ctx, []string{route}, 30)
	if err != nil {
		return nil, err
	}
	for _, ev := range evs {
		b.Events = append(b.Events, view(ev))
	}
	text, by, _, err := s.Relay.Note(ctx, route)
	if err != nil {
		return nil, err
	}
	b.Note, b.NoteByAI = text, by == "model"
	b.Verdict = verdictFor(b)
	return b, nil
}

func verdictFor(b *boardView) verdict {
	c := b.Counts
	total := 0
	for _, n := range c {
		total += n
	}
	moving := c[store.Pending] + c[store.Retrying]
	switch {
	case total == 0:
		sub := "Nothing has arrived yet."
		if b.IsDemo {
			sub = "Press a button to send one."
		}
		return verdict{"quiet", "Waiting for the first " + b.Noun, sub}
	case b.Held > 0:
		return verdict{"held", fmt.Sprintf("%s held safely", plural(b.Held, b.Noun)),
			"Nothing is lost. Each one says what went wrong and what to do."}
	case moving > 0:
		return verdict{"busy", "Delivering…", fmt.Sprintf("%s on the way to %s.", plural(moving, b.Noun), b.Client)}
	}
	sub := fmt.Sprintf("%s delivered to %s.", plural(c[store.Delivered], b.Noun), b.Client)
	if d := c[store.Duplicate]; d > 0 {
		sub += fmt.Sprintf(" %s stopped.", plural(d, "duplicate"))
	}
	return verdict{"ok", "Everything arrived", sub}
}

func view(ev store.Event) eventView {
	v := eventView{ID: ev.ID, What: describe.What(ev.Source, ev.EventType, ev.Body), Status: ev.Status,
		StatusLabel: statusLabel(ev.Status), Reason: ev.Reason, Received: ev.ReceivedAt, Attempts: ev.Attempts}
	if ev.Diagnosis != "" {
		var d diagnose.Diagnosis
		if json.Unmarshal([]byte(ev.Diagnosis), &d) == nil {
			v.Diagnosis = &d
		}
	}
	return v
}

func statusLabel(s string) string {
	return map[string]string{
		store.Pending:   "On its way",
		store.Retrying:  "Trying again",
		store.Delivered: "Arrived",
		store.Held:      "Held safely",
		store.Duplicate: "Duplicate stopped",
	}[s]
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ---------------------------------------------------------------------------------------------
// One event

type attemptView struct {
	N      int
	At     time.Time
	Answer string
	Tone   string
	Action string
	Reason string
}

type header struct{ Name, Value string }

type eventDetail struct {
	eventView
	Route         string
	Client        string
	Public        bool
	Source        string
	Provider      string
	EventType     string
	Signature     string
	SignatureText string
	SignatureTone string
	History       []attemptView
	Changes       []shape.Change
	ChangesKnown  bool
	Headers       []header
	Body          string
	LastResponse  string
	Final         bool
}

func (s *Server) detail(ctx context.Context, rt relay.Route, ev store.Event) (*eventDetail, error) {
	d := &eventDetail{eventView: view(ev), Route: rt.Name, Client: rt.Client, Public: rt.Public, Source: ev.Source,
		Provider: providerName(ev.Source), EventType: ev.EventType, Signature: ev.Signature}
	if d.Client == "" {
		d.Client = "the app"
	}
	d.SignatureText, d.SignatureTone = signatureText(ev.Signature, d.Provider)
	attempts, err := s.Relay.Store.Attempts(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	for i, a := range attempts {
		d.History = append(d.History, attemptView{N: i + 1, At: a.At, Answer: answerText(a), Tone: toneFor(a.Action), Action: a.Action, Reason: a.Reason})
	}
	if n := len(attempts); n > 0 {
		d.LastResponse = strings.TrimSpace(attempts[n-1].Response)
	}
	d.Changes, d.ChangesKnown = s.Relay.Changes(ctx, ev)
	for name, vals := range ev.Headers {
		d.Headers = append(d.Headers, header{name, strings.Join(vals, ", ")})
	}
	sort.Slice(d.Headers, func(i, j int) bool { return d.Headers[i].Name < d.Headers[j].Name })
	var pretty bytes.Buffer
	if json.Indent(&pretty, ev.Body, "", "  ") == nil {
		d.Body = pretty.String()
	} else {
		d.Body = string(ev.Body)
	}
	if len(d.Body) > 20000 {
		d.Body = d.Body[:20000] + "\n…"
	}
	d.Final = ev.Status == store.Delivered || ev.Status == store.Duplicate || (ev.Status == store.Held && ev.Diagnosis != "")
	return d, nil
}

func providerName(source string) string {
	switch source {
	case "github":
		return "GitHub"
	case "stripe":
		return "Stripe"
	}
	return "the sender"
}

func signatureText(sig, provider string) (string, string) {
	switch sig {
	case store.SigValid:
		return "Yes. Its signature matched, so it really came from " + provider + ".", "ok"
	case store.SigInvalid:
		return "Not confirmed. The signature didn't match, so it may not be from " + provider + ". Holdfast did not pass it on.", "bad"
	case store.SigMissing:
		return "Not confirmed. It arrived with no signature at all, so Holdfast did not pass it on.", "bad"
	case store.SigMalformed:
		return "Not confirmed. The signature was garbled, so Holdfast did not pass it on.", "bad"
	case store.SigStale:
		return "The signature was genuine but an old one, so this may be a replay. Holdfast did not pass it on.", "bad"
	case store.SigNone:
		return "Not checked: no signing secret is set for this connection.", "quiet"
	}
	return "Not checked yet.", "quiet"
}

func answerText(a store.Attempt) string {
	switch {
	case a.TimedOut:
		return "No answer in time"
	case a.ConnFailed:
		return "Couldn't connect"
	case a.StatusCode >= 200 && a.StatusCode < 300:
		return fmt.Sprintf("Accepted (%d) in %s", a.StatusCode, a.Duration.Round(time.Millisecond))
	case a.StatusCode > 0:
		return fmt.Sprintf("%s (%d)", http.StatusText(a.StatusCode), a.StatusCode)
	}
	return "Not sent"
}

func toneFor(action string) string {
	switch action {
	case "delivered":
		return "ok"
	case "retry":
		return "busy"
	}
	return "held"
}

// ---------------------------------------------------------------------------------------------
// Template helpers

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"level":    func(key string) unlock.Result { return s.Board().Get(key) },
		"unlocked": func(key string) bool { return s.Board().Is(key) },
		"clock": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.In(s.Loc).Format("15:04:05")
		},
		"date": func(t time.Time) string { return t.In(s.Loc).Format("2 Jan 2006, 15:04:05 MST") },
		"iso":  func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"rupees": func(paise int) string {
			if paise%100 == 0 {
				return fmt.Sprintf("₹%d", paise/100)
			}
			return fmt.Sprintf("₹%.2f", float64(paise)/100)
		},
		"path":  func(p string) string { return strings.TrimPrefix(strings.TrimPrefix(p, "$"), ".") },
		"lower": strings.ToLower,
		"add":   func(a, b int) int { return a + b },
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
		"percent": func(r unlock.Result) int { return r.Percent() },
	}
}
