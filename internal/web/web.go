// Package web serves the website: the landing page, Demo mode (a pretend tea shop), Live mode (the
// visitor's own GitHub repo or Stripe account), event pages, and the resend actions.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tanay3484/holdfast/internal/buildinfo"
	"github.com/Tanay3484/holdfast/internal/demo"
	"github.com/Tanay3484/holdfast/internal/faults"
	"github.com/Tanay3484/holdfast/internal/github"
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/replay"
	"github.com/Tanay3484/holdfast/internal/session"
	"github.com/Tanay3484/holdfast/internal/store"
	"github.com/Tanay3484/holdfast/internal/testapp"
	"github.com/Tanay3484/holdfast/internal/unlock"
	"github.com/Tanay3484/holdfast/internal/wip"
)

//go:embed templates static
var assets embed.FS

// Options wires a Server.
type Options struct {
	Relay          *relay.Relay
	Shop           *demo.Shop   // nil when the demo is off
	Demo           *demo.Runner // nil when the demo is off
	TestApp        *testapp.App
	Sessions       *session.Manager
	GitHub         github.API // nil when GitHub sign-in is not configured
	DevLogin       bool       // local development only: sign in without GitHub, with a fake GitHub
	AdminToken     string
	PublicURL      string // e.g. https://holdfast.onrender.com; empty means "from the request"
	SelfURL        string // how this process reaches itself, e.g. http://127.0.0.1:8080
	MaxConnections int
	Loc            *time.Location
	Log            *slog.Logger
}

// Server is the web front end.
type Server struct {
	Options
	Board func() unlock.Board
	pages map[string]*template.Template
	asset string // fingerprint of the CSS and JS, so browsers fetch them again whenever they change
}

// New parses the templates.
func New(o Options) (*Server, error) {
	if o.MaxConnections <= 0 {
		o.MaxConnections = 5
	}
	if o.TestApp == nil {
		o.TestApp = testapp.New()
	}
	s := &Server{Options: o, Board: unlock.Current, pages: map[string]*template.Template{}, asset: fingerprint()}
	for _, page := range []string{"index.html", "demo.html", "live.html", "connect.html", "route.html", "event.html", "progress.html"} {
		t, err := template.New("").Funcs(s.funcs()).ParseFS(assets, "templates/base.html", "templates/partials.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", page, err)
		}
		s.pages[page] = t
	}
	return s, nil
}

// Routes registers every handler.
func (s *Server) Routes(mux *http.ServeMux) {
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(time.Hour, http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	// Providers send here.
	mux.HandleFunc("POST /in/{route}", s.Relay.HandleIngest)
	mux.Handle("POST /test-app/{conn}", s.TestApp)

	mux.HandleFunc("GET /{$}", s.handleIndex)
	if s.Shop != nil {
		mux.Handle("POST /demo-shop/webhook", s.Shop)
		mux.HandleFunc("GET /demo", s.handleDemoPage)
		mux.HandleFunc("POST /demo/{fault}", s.handleDemo)
	}
	s.liveRoutes(mux)

	mux.HandleFunc("GET /r/{route}", s.handleRoute)
	mux.HandleFunc("GET /fragments/board/{route}", s.handleBoardFragment)
	mux.HandleFunc("GET /events/{id}", s.handleEvent)
	mux.HandleFunc("GET /fragments/event/{id}", s.handleEventFragment)
	mux.HandleFunc("POST /events/{id}/resend", s.handleResendOne)
	mux.HandleFunc("POST /r/{route}/resend", s.handleResendAll)
	mux.HandleFunc("GET /progress", s.handleProgress)
}

// page is what every template receives.
type page struct {
	Title   string
	Version string
	Asset   string
	Mode    string // home | demo | live
	User    *store.User
	Game    unlock.Board
	Demos   []faults.Spec
	Board   *boardView
	Event   *eventDetail
	Live    *liveView
	Conn    *connView
	Connect *connectView
	Admin   bool
}

func (s *Server) newPage(r *http.Request, title, mode string) page {
	p := page{Title: title, Version: buildinfo.Version, Asset: s.asset, Mode: mode, Game: s.Board()}
	if u, ok := s.currentUser(r); ok {
		p.User = &u
	}
	return p
}

func (s *Server) render(w http.ResponseWriter, name, block string, data any) {
	var buf bytes.Buffer
	if err := s.pages[name].ExecuteTemplate(&buf, block, data); err != nil {
		s.Log.Error("render", "page", name, "block", block, "err", err)
		http.Error(w, "Something went wrong drawing this page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// ---------------------------------------------------------------------------------------------
// Pages

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, "index.html", "base", s.newPage(r, "Holdfast · every webhook, held fast", "home"))
}

func (s *Server) handleDemoPage(w http.ResponseWriter, r *http.Request) {
	p := s.newPage(r, "Demo · Holdfast", "demo")
	b, err := s.board(r.Context(), demo.RouteName)
	if err != nil {
		s.fail(w, err)
		return
	}
	p.Board, p.Demos = b, faults.Demo
	s.render(w, "demo.html", "base", p)
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("route")
	if name == demo.RouteName {
		http.Redirect(w, r, "/demo", http.StatusSeeOther)
		return
	}
	rt, ok := s.Relay.Route(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.canSee(w, r, rt) {
		return
	}
	b, err := s.board(r.Context(), name)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := s.newPage(r, b.Client+" · Holdfast", "live")
	p.Board, p.Admin = b, rt.Owner == 0
	if rt.Owner != 0 {
		if p.Conn, err = s.connView(r, rt); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.render(w, "route.html", "base", p)
}

func (s *Server) handleBoardFragment(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("route")
	rt, ok := s.Relay.Route(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.canSee(w, r, rt) {
		return
	}
	b, err := s.board(r.Context(), name)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := s.newPage(r, "", "")
	p.Board = b
	if rt.Owner != 0 {
		p.Conn, _ = s.connView(r, rt)
	}
	s.render(w, "route.html", "board", p)
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	s.render(w, "progress.html", "base", s.newPage(r, "Unlock board · Holdfast", ""))
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	d, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	mode := "live"
	if d.Public {
		mode = "demo"
	}
	p := s.newPage(r, d.What+" · Holdfast", mode)
	p.Event = d
	s.render(w, "event.html", "base", p)
}

func (s *Server) handleEventFragment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	p := s.newPage(r, "", "")
	p.Event = d
	s.render(w, "event.html", "event-live", p)
}

func (s *Server) eventFor(w http.ResponseWriter, r *http.Request) (*eventDetail, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	ev, err := s.Relay.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.fail(w, err)
		return nil, false
	}
	rt, ok := s.Relay.Route(ev.Route)
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	if !s.canSee(w, r, rt) {
		return nil, false
	}
	d, err := s.detail(r.Context(), rt, ev)
	if err != nil {
		s.fail(w, err)
		return nil, false
	}
	return d, true
}

// ---------------------------------------------------------------------------------------------
// Actions

func (s *Server) handleDemo(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "Cross-site request refused.", http.StatusForbidden)
		return
	}
	spec, ok := faults.Lookup(r.PathValue("fault"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.Demo.Allow(visitor(r)) {
		respond(w, r, http.StatusTooManyRequests, "One at a time, please. Try again in a moment.", "/demo")
		return
	}
	// The scenario can take a few seconds (it waits between sends), so it runs on its own.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Demo.Run(ctx, spec.Fault); err != nil {
			s.Log.Error("demo failed", "fault", spec.Fault, "err", err)
		}
	}()
	respond(w, r, http.StatusAccepted, "Sent: "+strings.ToLower(spec.Label[:1])+spec.Label[1:]+".", "/demo")
}

func (s *Server) handleResendOne(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || !sameOrigin(r) {
		http.Error(w, "Bad request.", http.StatusBadRequest)
		return
	}
	ev, err := s.Relay.Store.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rt, ok := s.Relay.Route(ev.Route)
	if !ok || !s.canSee(w, r, rt) {
		return
	}
	steps, err := s.Relay.Resend(r.Context(), ev.Route, []int64{id})
	s.afterResend(w, r, steps, err, fmt.Sprintf("/events/%d", id))
}

func (s *Server) handleResendAll(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("route")
	rt, ok := s.Relay.Route(name)
	if !ok || !sameOrigin(r) {
		http.Error(w, "Bad request.", http.StatusBadRequest)
		return
	}
	if !s.canSee(w, r, rt) {
		return
	}
	steps, err := s.Relay.Resend(r.Context(), name, nil)
	back := "/r/" + name
	if rt.Public {
		back = "/demo"
	}
	s.afterResend(w, r, steps, err, back)
}

func (s *Server) afterResend(w http.ResponseWriter, r *http.Request, steps []replay.Step, err error, back string) {
	switch {
	case wip.IsLocked(err):
		respond(w, r, http.StatusConflict, "Resending isn't available on this build.", back)
	case err != nil:
		s.Log.Error("resend", "err", err)
		respond(w, r, http.StatusInternalServerError, "Couldn't resend: "+err.Error(), back)
	case len(steps) == 0:
		respond(w, r, http.StatusOK, "Nothing is held, so there's nothing to send.", back)
	default:
		sent := 0
		for _, st := range steps {
			if st.Send {
				sent++
			}
		}
		msg := fmt.Sprintf("Sending %d again.", sent)
		if skipped := len(steps) - sent; skipped > 0 {
			msg += fmt.Sprintf(" %d kept back; the reason is on each one.", skipped)
		}
		respond(w, r, http.StatusOK, msg, back)
	}
}

// respond answers a form post: JSON for the page's script, a redirect for a plain form.
func respond(w http.ResponseWriter, r *http.Request, status int, msg, back string) {
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": msg, "ok": status < 300, "redirect": back})
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ---------------------------------------------------------------------------------------------
// Access

// canSee lets through the public demo, a Live connection's owner, and the admin for config routes.
func (s *Server) canSee(w http.ResponseWriter, r *http.Request, rt relay.Route) bool {
	if rt.Public {
		return true
	}
	if rt.Owner != 0 {
		if uid, ok := s.Sessions.User(r); ok && uid == rt.Owner {
			return true
		}
		// Someone else's connection: don't even confirm it exists.
		http.NotFound(w, r)
		return false
	}
	if s.AdminToken != "" {
		if _, pass, ok := r.BasicAuth(); ok && subtle.ConstantTimeCompare([]byte(pass), []byte(s.AdminToken)) == 1 {
			return true
		}
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="Holdfast", charset="UTF-8"`)
	http.Error(w, "This connection is private. Sign in with the admin password (any user name).", http.StatusUnauthorized)
	return false
}

// sameOrigin refuses form posts from other sites.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // old browsers and curl; sign-in and the admin password still apply
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func visitor(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.Log.Error("request failed", "err", err)
	http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
}

// fingerprint hashes the static files that pages link with ?v=.
func fingerprint() string {
	h := sha256.New()
	for _, name := range []string{"static/app.css", "static/app.js"} {
		b, _ := assets.ReadFile(name)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}
