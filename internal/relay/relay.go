// Package relay is the store-and-forward core: it accepts webhooks, stores them before acknowledging,
// delivers them with retries, holds what fails, diagnoses why, and resends on request.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tanay3484/holdfast/internal/config"
	"github.com/Tanay3484/holdfast/internal/describe"
	"github.com/Tanay3484/holdfast/internal/diagnose"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/replay"
	"github.com/Tanay3484/holdfast/internal/shape"
	"github.com/Tanay3484/holdfast/internal/store"
	"github.com/Tanay3484/holdfast/internal/verify"
	"github.com/Tanay3484/holdfast/internal/wip"
)

// MaxBody is the largest webhook accepted.
const MaxBody = 1 << 20

// StripeTolerance is how old a Stripe signature may be.
const StripeTolerance = 5 * time.Minute

// Route is a configured route with its delivery settings resolved.
type Route struct {
	config.Route
	Timeout time.Duration
	Policy  policy.Policy
}

// Relay holds the moving parts.
type Relay struct {
	Store     *store.Store
	Routes    map[string]Route
	Diagnoser diagnose.Diagnoser
	HTTP      *http.Client
	Loc       *time.Location
	Retention time.Duration
	Log       *slog.Logger
	Now       func() time.Time

	ingestMu sync.Mutex // makes the duplicate check and insert one step
	kick     chan struct{}
	diag     chan int64

	notesMu sync.Mutex
	notes   map[string]cachedNote
}

type cachedNote struct {
	fingerprint string
	text, by    string
}

// New wires a relay.
func New(st *store.Store, routes []Route, dg diagnose.Diagnoser, loc *time.Location, retention time.Duration, log *slog.Logger) *Relay {
	r := &Relay{
		Store:     st,
		Routes:    map[string]Route{},
		Diagnoser: dg,
		HTTP:      &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Loc:       loc,
		Retention: retention,
		Log:       log,
		Now:       time.Now,
		kick:      make(chan struct{}, 1),
		diag:      make(chan int64, 256),
		notes:     map[string]cachedNote{},
	}
	for _, rt := range routes {
		r.Routes[rt.Name] = rt
	}
	return r
}

// Run starts the delivery worker, the diagnosis worker and the pruner, and blocks until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	if held, err := r.Store.Undiagnosed(ctx); err == nil {
		for _, ev := range held {
			r.queueDiagnosis(ev.ID)
		}
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); r.deliverLoop(ctx) }()
	go func() { defer wg.Done(); r.diagnoseLoop(ctx) }()
	go func() { defer wg.Done(); r.pruneLoop(ctx) }()
	wg.Wait()
}

// Kick wakes the delivery worker.
func (r *Relay) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

func (r *Relay) queueDiagnosis(id int64) {
	select {
	case r.diag <- id:
	default: // full: it is picked up again on restart
	}
}

// ---------------------------------------------------------------------------------------------
// Receiving

// HandleIngest is POST /in/{route}.
func (r *Relay) HandleIngest(w http.ResponseWriter, req *http.Request) {
	rt, ok := r.Routes[req.PathValue("route")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown route"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, MaxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "body larger than 1 MiB"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "could not read body"})
		return
	}
	ev, err := r.Receive(req.Context(), rt, req.Header, body)
	if err != nil {
		r.Log.Error("store failed", "route", rt.Name, "err", err)
		// Not stored, so tell the provider to try again later.
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "could not store the event; please retry"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received": ev.ID, "status": ev.Status})
}

// Receive checks and stores one webhook. When it returns without error, the event is on disk.
func (r *Relay) Receive(ctx context.Context, rt Route, h http.Header, body []byte) (store.Event, error) {
	now := r.Now()
	sig := r.checkSignature(rt, h, body, now)
	ev := store.Event{
		Route:      rt.Name,
		Source:     rt.Source,
		EventType:  describe.EventType(rt.Source, h, body),
		ReceivedAt: now,
		Headers:    keepHeaders(h),
		Body:       body,
		Signature:  sig,
		DedupeKey:  r.dedupeKey(rt.Source, h, body),
		Status:     store.Pending,
		NextAt:     now,
		Reason:     "Received and saved. Passing it on now.",
	}
	if store.SignatureFailed(sig) {
		ev.Status, ev.Reason = store.Held, signatureReason(sig)
	}

	r.ingestMu.Lock()
	if ev.Status == store.Pending && ev.DedupeKey != "" {
		if orig, dup, err := r.Store.HasKey(ctx, rt.Name, ev.DedupeKey); err == nil && dup {
			ev.Status = store.Duplicate
			ev.Reason = fmt.Sprintf("A copy of event #%d, which Holdfast already has. Not passed on again.", orig)
		}
	}
	_, err := r.Store.Insert(ctx, &ev)
	r.ingestMu.Unlock()
	if err != nil {
		return ev, err
	}

	switch ev.Status {
	case store.Held:
		r.queueDiagnosis(ev.ID)
	case store.Pending:
		r.Kick()
	}
	return ev, nil
}

func signatureReason(sig string) string {
	switch sig {
	case store.SigStale:
		return "The signature was correct but too old, so Holdfast kept it aside instead of passing it on."
	case store.SigMissing:
		return "It arrived without a signature, so Holdfast kept it aside instead of passing it on."
	}
	return "The signature didn't match, so Holdfast kept it aside instead of passing it on."
}

func (r *Relay) checkSignature(rt Route, h http.Header, body []byte, now time.Time) string {
	if rt.Secret == "" {
		return store.SigNone
	}
	var err error
	switch rt.Source {
	case "github":
		err = guard(func() error { return verify.GitHub(rt.Secret, body, h.Get("X-Hub-Signature-256")) })
	case "stripe":
		err = guard(func() error {
			return verify.Stripe(rt.Secret, body, h.Get("Stripe-Signature"), now, StripeTolerance)
		})
	default:
		return store.SigNone
	}
	switch {
	case err == nil:
		return store.SigValid
	case wip.IsLocked(err), errors.Is(err, errPanic):
		return store.SigUnchecked
	case errors.Is(err, verify.ErrMissingSignature):
		return store.SigMissing
	case errors.Is(err, verify.ErrMalformedHeader):
		return store.SigMalformed
	case errors.Is(err, verify.ErrStaleTimestamp):
		return store.SigStale
	}
	return store.SigInvalid
}

func (r *Relay) dedupeKey(source string, h http.Header, body []byte) string {
	var key string
	err := guard(func() error {
		var err error
		key, err = replay.DedupeKey(source, h, body)
		return err
	})
	if err != nil {
		return ""
	}
	return key
}

// keepHeaders drops credentials a provider would never send but a proxy might add.
func keepHeaders(h http.Header) http.Header {
	out := h.Clone()
	for _, k := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
		out.Del(k)
	}
	return out
}

// errPanic marks an @human function that panicked; the relay treats it like a locked one.
var errPanic = errors.New("panicked")

func guard(f func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%w: %v", errPanic, p)
		}
	}()
	return f()
}

// ---------------------------------------------------------------------------------------------
// Delivering

func (r *Relay) deliverLoop(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		due, err := r.Store.Due(ctx, r.Now(), 20)
		if err != nil && ctx.Err() == nil {
			r.Log.Error("due query failed", "err", err)
		}
		for _, ev := range due {
			if ctx.Err() != nil {
				return
			}
			r.Deliver(ctx, ev)
		}
		if len(due) == 20 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-tick.C:
		}
	}
}

// forwarded are the request headers passed on to the app.
var forwarded = []string{
	"Content-Type", "User-Agent",
	"X-GitHub-Event", "X-GitHub-Delivery", "X-GitHub-Hook-ID", "X-Hub-Signature", "X-Hub-Signature-256",
	"Stripe-Signature",
}

// Deliver makes one attempt and records what happened.
func (r *Relay) Deliver(ctx context.Context, ev store.Event) {
	rt, ok := r.Routes[ev.Route]
	if !ok {
		_ = r.Store.RecordAttempt(ctx, store.Attempt{EventID: ev.ID, At: r.Now(), Action: string(policy.Hold),
			Reason: "This route is no longer configured."}, store.Held, time.Time{}, "This route is no longer configured.")
		return
	}
	attempt := ev.Attempts + 1
	out, a := r.send(ctx, rt, ev, attempt)
	d := r.decide(rt, attempt, out)
	a.Action, a.Reason = string(d.Action), d.Reason

	status, next := store.Held, time.Time{}
	switch d.Action {
	case policy.Delivered:
		status = store.Delivered
	case policy.Retry:
		status, next = store.Retrying, r.Now().Add(d.Wait)
	}
	// Recording must survive shutdown: the attempt already happened.
	if err := r.Store.RecordAttempt(context.WithoutCancel(ctx), a, status, next, d.Reason); err != nil {
		r.Log.Error("record attempt failed", "event", ev.ID, "err", err)
		return
	}
	if status == store.Held {
		r.queueDiagnosis(ev.ID)
	}
}

func (r *Relay) send(ctx context.Context, rt Route, ev store.Event, attempt int) (policy.Outcome, store.Attempt) {
	a := store.Attempt{EventID: ev.ID, At: r.Now()}
	// An attempt in flight is allowed to finish even if the server is shutting down.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rt.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, rt.Destination, strings.NewReader(string(ev.Body)))
	if err != nil {
		a.ConnFailed, a.Response = true, err.Error()
		return policy.Outcome{ConnFailed: true}, a
	}
	for _, k := range forwarded {
		if v := ev.Headers.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Holdfast-Event-Id", fmt.Sprint(ev.ID))
	req.Header.Set("Holdfast-Attempt", fmt.Sprint(attempt))

	start := time.Now()
	resp, err := r.HTTP.Do(req)
	a.Duration = time.Since(start)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			a.TimedOut, a.Response = true, fmt.Sprintf("no answer within %s", rt.Timeout)
			return policy.Outcome{TimedOut: true}, a
		}
		a.ConnFailed, a.Response = true, err.Error()
		return policy.Outcome{ConnFailed: true}, a
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	a.StatusCode, a.Response = resp.StatusCode, string(snippet)
	return policy.Outcome{StatusCode: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}, a
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

func retryAfter(v string) time.Duration {
	var secs int
	if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &secs); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// decide asks the @human policy, falling back to "one try, then hold" while it is locked.
func (r *Relay) decide(rt Route, attempt int, o policy.Outcome) policy.Decision {
	var d policy.Decision
	err := guard(func() error {
		var err error
		d, err = rt.Policy.Decide(attempt, o)
		return err
	})
	if err == nil && d.Action != "" {
		if d.Reason == "" {
			d.Reason = string(d.Action)
		}
		return d
	}
	if o.StatusCode >= 200 && o.StatusCode < 300 {
		return policy.Decision{Action: policy.Delivered, Reason: "The app accepted it."}
	}
	return policy.Decision{Action: policy.Hold, Reason: "It didn't go through. Holdfast tried once and is keeping it safe."}
}

// ---------------------------------------------------------------------------------------------
// Resending

// Resend plans and queues held events. ids nil means every held event in the route.
func (r *Relay) Resend(ctx context.Context, route string, ids []int64) ([]replay.Step, error) {
	rt, ok := r.Routes[route]
	if !ok {
		return nil, fmt.Errorf("unknown route %q", route)
	}
	held, err := r.Store.ByStatus(ctx, route, store.Held)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var candidates []replay.Held
	for _, ev := range held {
		if ids != nil && !want[ev.ID] {
			continue
		}
		candidates = append(candidates, replay.Held{
			ID: ev.ID, DedupeKey: r.dedupeKey(rt.Source, ev.Headers, ev.Body), ReceivedAt: ev.ReceivedAt,
			SignatureFailed: store.SignatureFailed(ev.Signature),
		})
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	delivered, err := r.deliveredKeys(ctx, rt)
	if err != nil {
		return nil, err
	}
	var steps []replay.Step
	if err := guard(func() error {
		var err error
		steps, err = replay.Plan(candidates, delivered)
		return err
	}); err != nil {
		return nil, err
	}
	for _, s := range steps {
		if s.Send {
			err = r.Store.Requeue(ctx, s.ID, "Sent again by hand. On its way.")
		} else {
			err = r.Store.SetReason(ctx, s.ID, "Not sent again: "+s.Reason)
		}
		if err != nil {
			return steps, err
		}
	}
	r.Kick()
	return steps, nil
}

func (r *Relay) deliveredKeys(ctx context.Context, rt Route) (map[string]bool, error) {
	evs, err := r.Store.ByStatus(ctx, rt.Name, store.Delivered)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, ev := range evs {
		if k := r.dedupeKey(rt.Source, ev.Headers, ev.Body); k != "" {
			keys[k] = true
		}
	}
	return keys, nil
}

// ---------------------------------------------------------------------------------------------
// Diagnosing

func (r *Relay) diagnoseLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.diag:
			r.diagnose(ctx, id)
		}
	}
}

func (r *Relay) diagnose(ctx context.Context, id int64) {
	ev, err := r.Store.Get(ctx, id)
	if err != nil || ev.Status != store.Held {
		return
	}
	f, err := r.Facts(ctx, ev)
	if err != nil {
		r.Log.Error("facts failed", "event", id, "err", err)
		return
	}
	d := r.Diagnoser.Diagnose(ctx, f, diagnose.Combined)
	if d.ModelError != "" {
		r.Log.Warn("model unavailable; used rules", "event", id, "err", d.ModelError)
	}
	raw, _ := json.Marshal(d)
	if err := r.Store.SetDiagnosis(ctx, id, string(raw)); err != nil {
		r.Log.Error("save diagnosis failed", "event", id, "err", err)
		return
	}
	r.refreshNote(ctx, ev.Route)
}

// Facts collects what code knows about an event, for the diagnoser.
func (r *Relay) Facts(ctx context.Context, ev store.Event) (diagnose.Facts, error) {
	rt := r.Routes[ev.Route]
	f := diagnose.Facts{
		Source:    ev.Source,
		EventType: ev.EventType,
		What:      describe.What(ev.Source, ev.EventType, ev.Body),
		App:       rt.Client,
		Signature: ev.Signature,
	}
	attempts, err := r.Store.Attempts(ctx, ev.ID)
	if err != nil {
		return f, err
	}
	f.Attempts = len(attempts)
	if n := len(attempts); n > 0 {
		last := attempts[n-1]
		f.LastStatus, f.LastTimedOut, f.LastConnFail, f.Response = last.StatusCode, last.TimedOut, last.ConnFailed, last.Response
	}
	f.Changes, f.ChangesKnown = r.Changes(ctx, ev)
	return f, nil
}

// Changes compares an event with the last delivered one of the same type. ok is false when there is
// nothing to compare with or level 3 is locked.
func (r *Relay) Changes(ctx context.Context, ev store.Event) (changes []shape.Change, ok bool) {
	prev, found, err := r.Store.LastDelivered(ctx, ev.Route, ev.EventType, ev.ID)
	if err != nil || !found {
		return nil, false
	}
	err = guard(func() error {
		good, err := shape.Of(prev.Body)
		if err != nil {
			return err
		}
		bad, err := shape.Of(ev.Body)
		if err != nil {
			return err
		}
		changes, err = shape.Diff(good, bad)
		return err
	})
	return changes, err == nil
}

// ---------------------------------------------------------------------------------------------
// Client note

// Note returns the note for a route's currently held events. held is 0 when nothing is held.
func (r *Relay) Note(ctx context.Context, route string) (text, by string, held int, err error) {
	n, fp, err := r.noteFacts(ctx, route)
	if err != nil || n.Held == 0 {
		return "", "", 0, err
	}
	r.notesMu.Lock()
	c, ok := r.notes[route]
	r.notesMu.Unlock()
	if ok && c.fingerprint == fp {
		return c.text, c.by, n.Held, nil
	}
	return diagnose.TemplateNote(n), "rules", n.Held, nil
}

func (r *Relay) refreshNote(ctx context.Context, route string) {
	n, fp, err := r.noteFacts(ctx, route)
	if err != nil || n.Held == 0 {
		return
	}
	text, by := r.Diagnoser.Note(ctx, n)
	r.notesMu.Lock()
	r.notes[route] = cachedNote{fingerprint: fp, text: text, by: by}
	r.notesMu.Unlock()
}

func (r *Relay) noteFacts(ctx context.Context, route string) (diagnose.NoteFacts, string, error) {
	held, err := r.Store.ByStatus(ctx, route, store.Held)
	if err != nil {
		return diagnose.NoteFacts{}, "", err
	}
	n := diagnose.NoteFacts{App: r.Routes[route].Client, Held: len(held), Whats: map[string]int{}, Titles: map[string]int{}, Loc: r.Loc}
	var fp strings.Builder
	for i, ev := range held {
		if i == 0 || ev.ReceivedAt.Before(n.First) {
			n.First = ev.ReceivedAt
		}
		if ev.ReceivedAt.After(n.Last) {
			n.Last = ev.ReceivedAt
		}
		what, _, _ := strings.Cut(describe.What(ev.Source, ev.EventType, ev.Body), " · ")
		n.Whats[what]++
		var d diagnose.Diagnosis
		if json.Unmarshal([]byte(ev.Diagnosis), &d) == nil && d.Title != "" {
			n.Titles[d.Title]++
		}
		fmt.Fprintf(&fp, "%d:%t,", ev.ID, ev.Diagnosis != "")
	}
	return n, fp.String(), nil
}

// ---------------------------------------------------------------------------------------------

func (r *Relay) pruneLoop(ctx context.Context) {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		if n, err := r.Store.Prune(ctx, r.Now().Add(-r.Retention)); err == nil && n > 0 {
			r.Log.Info("pruned old events", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
