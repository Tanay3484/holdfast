package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"bytes"
	mrand "math/rand/v2"

	"github.com/Tanay3484/holdfast/internal/config"
	"github.com/Tanay3484/holdfast/internal/faults"
	"github.com/Tanay3484/holdfast/internal/github"
	"github.com/Tanay3484/holdfast/internal/netguard"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/store"
	"github.com/Tanay3484/holdfast/internal/testapp"
)

func (s *Server) liveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /live", s.handleLive)
	mux.HandleFunc("GET /live/new", s.handleConnectPage)
	mux.HandleFunc("POST /live/connections", s.handleCreateConnection)
	mux.HandleFunc("POST /r/{route}/test-app/{mode}", s.handleTestAppMode)
	mux.HandleFunc("POST /r/{route}/secret", s.handleSetSecret)
	mux.HandleFunc("POST /r/{route}/sample", s.handleSendSample)
	mux.HandleFunc("POST /r/{route}/delete", s.handleDeleteConnection)
	mux.HandleFunc("GET /auth/github", s.handleSignIn)
	mux.HandleFunc("GET /auth/github/callback", s.handleCallback)
	mux.HandleFunc("POST /auth/logout", s.handleSignOut)
	if s.DevLogin {
		mux.HandleFunc("GET /auth/dev", s.handleDevLogin)
	}
}

// ---------------------------------------------------------------------------------------------
// Who is signed in

func (s *Server) currentUser(r *http.Request) (store.User, bool) {
	if s.Sessions == nil {
		return store.User{}, false
	}
	id, ok := s.Sessions.User(r)
	if !ok {
		return store.User{}, false
	}
	u, err := s.Relay.Store.GetUser(r.Context(), id)
	return u, err == nil
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, ok := s.currentUser(r)
	if !ok {
		http.Redirect(w, r, "/live", http.StatusSeeOther)
	}
	return u, ok
}

// owned returns a connection the signed-in user owns, or answers 404.
func (s *Server) owned(w http.ResponseWriter, r *http.Request) (store.User, store.Connection, bool) {
	if !sameOrigin(r) {
		http.Error(w, "Cross-site request refused.", http.StatusForbidden)
		return store.User{}, store.Connection{}, false
	}
	u, ok := s.currentUser(r)
	if !ok {
		http.NotFound(w, r)
		return u, store.Connection{}, false
	}
	c, err := s.Relay.Store.Connection(r.Context(), r.PathValue("route"))
	if err != nil || c.UserID != u.ID {
		http.NotFound(w, r)
		return u, c, false
	}
	return u, c, true
}

func (s *Server) publicURL(r *http.Request) string {
	if s.PublicURL != "" {
		return strings.TrimRight(s.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ---------------------------------------------------------------------------------------------
// Sign-in

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil {
		http.Redirect(w, r, "/live", http.StatusSeeOther)
		return
	}
	state, err := s.Sessions.NewState(w, r)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, s.GitHub.AuthURL(state, s.publicURL(r)+"/auth/github/callback"), http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil || !s.Sessions.CheckState(w, r, r.URL.Query().Get("state")) {
		http.Error(w, "That sign-in link has expired. Go back and try again.", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/live", http.StatusSeeOther) // the person pressed Cancel on GitHub
		return
	}
	ctx := r.Context()
	token, err := s.GitHub.Exchange(ctx, r.URL.Query().Get("code"), s.publicURL(r)+"/auth/github/callback")
	if err != nil {
		s.fail(w, err)
		return
	}
	gu, err := s.GitHub.User(ctx, token)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.signIn(w, r, gu, token)
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request, gu github.User, token string) {
	sealed, err := s.Sessions.Seal(token)
	if err != nil {
		s.fail(w, err)
		return
	}
	id, err := s.Relay.Store.UpsertUser(r.Context(), store.User{GitHubID: gu.ID, Login: gu.Login, Name: gu.Name,
		Avatar: gu.Avatar, Token: sealed})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.Sessions.SignIn(w, r, id)
	http.Redirect(w, r, "/live", http.StatusSeeOther)
}

func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	s.signIn(w, r, github.User{ID: -1, Login: "dev-user", Name: "Local developer"}, "dev-token")
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	s.Sessions.SignOut(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------------------------
// The Live page and the new-connection page

type connSummary struct {
	store.Connection
	Arrived, Held, Moving int
	Where                 string
}

type liveView struct {
	SignInReady bool
	DevLogin    bool
	Conns       []connSummary
	CanAdd      bool
	Max         int
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	p := s.newPage(r, "Live · Holdfast", "live")
	v := &liveView{SignInReady: s.GitHub != nil, DevLogin: s.DevLogin, Max: s.MaxConnections}
	if p.User != nil {
		conns, err := s.Relay.Store.Connections(r.Context(), p.User.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, c := range conns {
			counts, _ := s.Relay.Store.Counts(r.Context(), c.Name)
			v.Conns = append(v.Conns, connSummary{Connection: c, Arrived: counts[store.Delivered], Held: counts[store.Held],
				Moving: counts[store.Pending] + counts[store.Retrying], Where: where(c)})
		}
		v.CanAdd = len(conns) < s.MaxConnections
	}
	p.Live = v
	s.render(w, "live.html", "base", p)
}

func where(c store.Connection) string {
	if c.TestApp {
		return "Holdfast's test app"
	}
	if h := hostOf(c.Destination); h != "" {
		return h
	}
	return c.Destination
}

type connectView struct {
	Kind      string // github | stripe | generic
	Repos     []github.Repo
	RepoError string
	Error     string
	GitHubOK  bool
	Form      map[string]string
}

func (s *Server) handleConnectPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	s.renderConnect(w, r, u, connectView{Kind: r.URL.Query().Get("kind"), Form: map[string]string{}})
}

func (s *Server) renderConnect(w http.ResponseWriter, r *http.Request, u store.User, v connectView) {
	switch v.Kind {
	case "github", "stripe", "generic":
	default:
		v.Kind = "github"
	}
	v.GitHubOK = s.GitHub != nil
	if v.Kind == "github" && s.GitHub != nil {
		token, err := s.Sessions.Open(u.Token)
		if err == nil {
			v.Repos, err = s.GitHub.Repos(r.Context(), token)
		}
		if err != nil {
			s.Log.Warn("list repos", "user", u.Login, "err", err)
			v.RepoError = "Couldn't load your repositories from GitHub. Sign out and in again, then retry."
		}
	}
	p := s.newPage(r, "New connection · Holdfast", "live")
	p.Connect = &v
	s.render(w, "connect.html", "base", p)
}

func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "Cross-site request refused.", http.StatusForbidden)
		return
	}
	u, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form.", http.StatusBadRequest)
		return
	}
	form := map[string]string{}
	for k := range r.PostForm {
		form[k] = strings.TrimSpace(r.PostForm.Get(k))
	}
	v := connectView{Kind: form["kind"], Form: form}
	again := func(msg string) {
		v.Error = msg
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.renderConnect(w, r, u, v)
	}

	ctx := r.Context()
	existing, err := s.Relay.Store.Connections(ctx, u.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(existing) >= s.MaxConnections {
		again(fmt.Sprintf("You already have %d connections, the most this server allows. Remove one first.", s.MaxConnections))
		return
	}

	c := store.Connection{Name: newName(), UserID: u.ID, Source: v.Kind, TestApp: form["target"] != "own"}
	if !c.TestApp {
		if err := netguard.CheckURL(ctx, form["destination"]); err != nil {
			again("Your app's address: " + err.Error() + ".")
			return
		}
		c.Destination = form["destination"]
	}
	secret := ""
	switch v.Kind {
	case "github":
		if s.GitHub == nil {
			again("GitHub sign-in isn't set up on this server.")
			return
		}
		c.Repo, c.Label = form["repo"], form["repo"]
		if !strings.Contains(c.Repo, "/") {
			again("Choose a repository.")
			return
		}
		secret = randomHex(32)
		token, err := s.Sessions.Open(u.Token)
		if err != nil {
			again("Your sign-in has expired. Sign out and in again.")
			return
		}
		c.HookID, err = s.GitHub.CreateHook(ctx, token, c.Repo, s.publicURL(r)+"/in/"+c.Name, secret)
		if err != nil {
			s.Log.Warn("create hook", "repo", c.Repo, "err", err)
			again("GitHub didn't let Holdfast add the webhook to " + c.Repo + ". You need admin rights on it. (" + err.Error() + ")")
			return
		}
	case "stripe":
		c.Label = labelOr(form["label"], "My Stripe account")
		secret = form["secret"]
		if secret != "" && !strings.HasPrefix(secret, "whsec_") {
			again("A Stripe signing secret starts with whsec_. You can also leave it empty and add it later.")
			return
		}
	case "generic":
		c.Label = labelOr(form["label"], "My webhooks")
	default:
		again("Choose GitHub, Stripe or another service.")
		return
	}

	if c.Secret, err = s.Sessions.Seal(secret); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Relay.Store.AddConnection(ctx, c); err != nil {
		s.fail(w, err)
		return
	}
	s.Relay.SetRoute(RouteFor(c, secret, s.SelfURL))
	http.Redirect(w, r, "/r/"+c.Name+"?new=1", http.StatusSeeOther)
}

// RouteFor turns a stored connection into a relay route. secret is the unsealed signing secret.
func RouteFor(c store.Connection, secret, selfURL string) relay.Route {
	dest := c.Destination
	if c.TestApp {
		dest = strings.TrimRight(selfURL, "/") + "/test-app/" + c.Name
	}
	return relay.Route{
		Route:   config.Route{Name: c.Name, Source: c.Source, Secret: secret, Destination: dest, Client: c.Label},
		Timeout: 10 * time.Second,
		Policy:  policy.Default(),
		Owner:   c.UserID,
		Guarded: !c.TestApp,
	}
}

// ---------------------------------------------------------------------------------------------
// One connection's page: extra panels next to the board

type connView struct {
	store.Connection
	InURL       string
	Where       string
	NeedSecret  bool
	Modes       []modeView
	Received    []testapp.Received
	ShowWelcome bool
}

type modeView struct {
	Mode  testapp.Mode
	Label string
	Hint  string
	On    bool
}

func (s *Server) connView(r *http.Request, rt relay.Route) (*connView, error) {
	c, err := s.Relay.Store.Connection(r.Context(), rt.Name)
	if err != nil {
		return nil, err
	}
	v := &connView{Connection: c, InURL: s.publicURL(r) + "/in/" + c.Name, Where: where(c),
		NeedSecret: c.Source == "stripe" && c.Secret == "", ShowWelcome: r.URL.Query().Get("new") == "1"}
	if c.TestApp {
		cur := s.TestApp.Mode(c.Name)
		for _, m := range testapp.Modes {
			v.Modes = append(v.Modes, modeView{Mode: m.Mode, Label: m.Label, Hint: m.Hint, On: m.Mode == cur})
		}
		v.Received = s.TestApp.Log(c.Name)
		if len(v.Received) > 6 {
			v.Received = v.Received[:6]
		}
	}
	return v, nil
}

func (s *Server) handleTestAppMode(w http.ResponseWriter, r *http.Request) {
	_, c, ok := s.owned(w, r)
	if !ok {
		return
	}
	mode := testapp.Mode(r.PathValue("mode"))
	label := ""
	for _, m := range testapp.Modes {
		if m.Mode == mode {
			label = m.Label
		}
	}
	if label == "" || !c.TestApp {
		http.NotFound(w, r)
		return
	}
	s.TestApp.SetMode(c.Name, mode)
	respond(w, r, http.StatusOK, "Test app: "+strings.ToLower(label)+".", "/r/"+c.Name)
}

func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	_, c, ok := s.owned(w, r)
	if !ok {
		return
	}
	secret := strings.TrimSpace(r.PostFormValue("secret"))
	if c.Source != "stripe" || !strings.HasPrefix(secret, "whsec_") {
		respond(w, r, http.StatusUnprocessableEntity, "A Stripe signing secret starts with whsec_.", "/r/"+c.Name)
		return
	}
	sealed, err := s.Sessions.Seal(secret)
	if err == nil {
		err = s.Relay.Store.SetConnectionSecret(r.Context(), c.Name, sealed)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	c.Secret = sealed
	s.Relay.SetRoute(RouteFor(c, secret, s.SelfURL))
	respond(w, r, http.StatusOK, "Saved. Stripe's signatures are checked from now on.", "/r/"+c.Name)
}

// handleSendSample sends one made-up event through the real endpoint, for Stripe and other
// connections that have no provider sending yet. It is labelled as made up wherever it appears.
func (s *Server) handleSendSample(w http.ResponseWriter, r *http.Request) {
	_, c, ok := s.owned(w, r)
	if !ok {
		return
	}
	if s.Demo != nil && !s.Demo.Allow("sample:"+visitor(r)) {
		respond(w, r, http.StatusTooManyRequests, "One at a time, please.", "/r/"+c.Name)
		return
	}
	var body []byte
	headers := http.Header{"Content-Type": {"application/json"}, "User-Agent": {"holdfast-sample"}}
	switch c.Source {
	case "stripe":
		body = faults.Payment(mrand.New(mrand.NewPCG(uint64(time.Now().UnixNano()), 3)), time.Now(), false)
		if secret, err := s.Sessions.Open(c.Secret); err == nil && secret != "" {
			headers.Set("Stripe-Signature", faults.SignStripe(secret, body, time.Now()))
		}
	case "generic":
		body = []byte(fmt.Sprintf(`{"type":"sample.created","id":"sample_%s","note":"A made-up event sent from Holdfast's page"}`, randomHex(6)))
	default:
		respond(w, r, http.StatusBadRequest, "GitHub sends real events: push to the repository to see one.", "/r/"+c.Name)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(s.SelfURL, "/")+"/in/"+c.Name, bytes.NewReader(body))
	if err != nil {
		s.fail(w, err)
		return
	}
	req.Header = headers
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.fail(w, err)
		return
	}
	resp.Body.Close()
	respond(w, r, http.StatusOK, "Sent a made-up sample event.", "/r/"+c.Name)
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	u, c, ok := s.owned(w, r)
	if !ok {
		return
	}
	note := "Disconnected."
	if c.Source == "github" && c.HookID != 0 && s.GitHub != nil {
		token, err := s.Sessions.Open(u.Token)
		if err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			err = s.GitHub.DeleteHook(ctx, token, c.Repo, c.HookID)
			cancel()
		}
		if err != nil {
			s.Log.Warn("delete hook", "repo", c.Repo, "err", err)
			note = "Disconnected. GitHub didn't let Holdfast remove the webhook, so remove it in the repository's settings."
		} else {
			note = "Disconnected, and the webhook was removed from " + c.Repo + "."
		}
	}
	s.Relay.RemoveRoute(c.Name)
	s.TestApp.Forget(c.Name)
	if err := s.Relay.Store.DeleteConnection(r.Context(), c.Name); err != nil {
		s.fail(w, err)
		return
	}
	respond(w, r, http.StatusOK, note, "/live")
}

// ---------------------------------------------------------------------------------------------

// newName is an unguessable route name: the /in/ address is public, so it must not be enumerable.
func newName() string { return "c-" + randomHex(10) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b)
}

func labelOr(s, def string) string {
	if s == "" {
		return def
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func hostOf(raw string) string {
	_, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	return host
}

// DevGitHub is a pretend GitHub for local development with HF_DEV_LOGIN: it lists two repositories and
// "adds" webhooks without calling anyone. Never enabled on a deployed server.
type DevGitHub struct{}

func (DevGitHub) AuthURL(string, string) string { return "/auth/dev" }
func (DevGitHub) Exchange(context.Context, string, string) (string, error) {
	return "", errors.New("dev mode")
}
func (DevGitHub) User(context.Context, string) (github.User, error) {
	return github.User{ID: -1, Login: "dev-user"}, nil
}
func (DevGitHub) Repos(context.Context, string) ([]github.Repo, error) {
	return []github.Repo{{FullName: "dev-user/shop-backend"}, {FullName: "dev-user/side-project"}}, nil
}
func (DevGitHub) CreateHook(context.Context, string, string, string, string) (int64, error) {
	return 1, nil
}
func (DevGitHub) DeleteHook(context.Context, string, string, int64) error { return nil }
