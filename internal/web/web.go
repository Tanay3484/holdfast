// Package web serves the website: the live demo, event pages and the unlock board.
package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
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
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/replay"
	"github.com/Tanay3484/holdfast/internal/store"
	"github.com/Tanay3484/holdfast/internal/unlock"
	"github.com/Tanay3484/holdfast/internal/wip"
)

//go:embed templates static
var assets embed.FS

// Server is the web front end.
type Server struct {
	Relay      *relay.Relay
	Shop       *demo.Shop   // nil when the demo is off
	Demo       *demo.Runner // nil when the demo is off
	AdminToken string
	Loc        *time.Location
	Log        *slog.Logger
	Board      func() unlock.Board

	pages map[string]*template.Template
}

// New parses the templates.
func New(r *relay.Relay, shop *demo.Shop, runner *demo.Runner, adminToken string, loc *time.Location, log *slog.Logger) (*Server, error) {
	s := &Server{Relay: r, Shop: shop, Demo: runner, AdminToken: adminToken, Loc: loc, Log: log, Board: unlock.Current,
		pages: map[string]*template.Template{}}
	for _, page := range []string{"index.html", "event.html", "progress.html", "route.html"} {
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

	mux.HandleFunc("POST /in/{route}", s.Relay.HandleIngest)
	if s.Shop != nil {
		mux.Handle("POST /demo-shop/webhook", s.Shop)
		mux.HandleFunc("POST /demo/{fault}", s.handleDemo)
	}

	mux.HandleFunc("GET /{$}", s.handleIndex)
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
	Game    unlock.Board
	Demos   []demoButton
	Board   *boardView
	Event   *eventDetail
	Admin   bool
}

func (s *Server) newPage(title string) page {
	return page{Title: title, Version: buildinfo.Version, Game: s.Board()}
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

type demoButton struct {
	faults.Spec
	Level unlock.Result
	Gated bool // this demo behaves differently once its level is unlocked
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	p := s.newPage("Holdfast · every webhook, held fast")
	if s.Shop != nil {
		b, err := s.board(r.Context(), demo.RouteName)
		if err != nil {
			s.fail(w, err)
			return
		}
		p.Board = b
		for _, spec := range faults.Demo {
			btn := demoButton{Spec: spec}
			if spec.Level != "" {
				btn.Level, btn.Gated = p.Game.Get(spec.Level), true
			}
			p.Demos = append(p.Demos, btn)
		}
	}
	s.render(w, "index.html", "base", p)
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("route")
	rt, ok := s.Relay.Routes[name]
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
	p := s.newPage(b.Client + " · Holdfast")
	p.Board, p.Admin = b, !rt.Public
	s.render(w, "route.html", "base", p)
}

func (s *Server) handleBoardFragment(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("route")
	rt, ok := s.Relay.Routes[name]
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
	p := s.newPage("")
	p.Board = b
	s.render(w, "index.html", "board", p)
}

func (s *Server) handleProgress(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "progress.html", "base", s.newPage("Unlock board · Holdfast"))
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	d, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	p := s.newPage(d.What + " · Holdfast")
	p.Event = d
	s.render(w, "event.html", "base", p)
}

func (s *Server) handleEventFragment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	p := s.newPage("")
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
	rt, ok := s.Relay.Routes[ev.Route]
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
		respond(w, r, http.StatusTooManyRequests, "One at a time, please. Try again in a moment.", "/#demo")
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
	respond(w, r, http.StatusAccepted, "Sent: "+strings.ToLower(spec.Label[:1])+spec.Label[1:]+".", "/#demo")
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
	rt, ok := s.Relay.Routes[ev.Route]
	if !ok || !s.canSee(w, r, rt) {
		return
	}
	steps, err := s.Relay.Resend(r.Context(), ev.Route, []int64{id})
	s.afterResend(w, r, steps, err, fmt.Sprintf("/events/%d", id))
}

func (s *Server) handleResendAll(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("route")
	rt, ok := s.Relay.Routes[name]
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
		back = "/#demo"
	}
	s.afterResend(w, r, steps, err, back)
}

func (s *Server) afterResend(w http.ResponseWriter, r *http.Request, steps []replay.Step, err error, back string) {
	switch {
	case wip.IsLocked(err):
		respond(w, r, http.StatusConflict, "Resending is still locked. Write internal/replay/replay.go to unlock it.", back)
	case err != nil:
		s.Log.Error("resend", "err", err)
		respond(w, r, http.StatusInternalServerError, "Couldn't resend: "+err.Error(), back)
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
		_ = json.NewEncoder(w).Encode(map[string]any{"message": msg, "ok": status < 300})
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ---------------------------------------------------------------------------------------------
// Access

func (s *Server) canSee(w http.ResponseWriter, r *http.Request, rt relay.Route) bool {
	if rt.Public {
		return true
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
		return true // old browsers and curl; the admin password still applies
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

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}
