package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tanay3484/holdfast/internal/config"
	"github.com/Tanay3484/holdfast/internal/demo"
	"github.com/Tanay3484/holdfast/internal/diagnose"
	"github.com/Tanay3484/holdfast/internal/github"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/session"
	"github.com/Tanay3484/holdfast/internal/store"
)

type fixture struct {
	h   http.Handler
	st  *store.Store
	rl  *relay.Relay
	gh  *fakeGitHub
	srv *Server
}

// fakeGitHub records the webhooks it was asked to add.
type fakeGitHub struct {
	DevGitHub
	created []string
}

func (f *fakeGitHub) CreateHook(_ context.Context, _, repo, hookURL, _ string) (int64, error) {
	f.created = append(f.created, repo+" → "+hookURL)
	return 42, nil
}

func setup(t *testing.T) fixture {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := []relay.Route{
		{Route: config.Route{Name: demo.RouteName, Source: "stripe", Secret: demo.Secret, Client: demo.Client, Public: true,
			Destination: "http://127.0.0.1:1/demo-shop/webhook"}, Timeout: time.Second, Policy: demo.Policy},
		{Route: config.Route{Name: "client", Source: "github", Destination: "http://127.0.0.1:1/hook", Client: "Client"},
			Timeout: time.Second, Policy: policy.Default()},
	}
	rl := relay.New(st, routes, diagnose.Diagnoser{}, time.UTC, time.Hour, log)
	shop := demo.NewShop()
	sessions, _ := session.New("test-key")
	gh := &fakeGitHub{}
	s, err := New(Options{Relay: rl, Shop: shop, Demo: demo.NewRunner(shop, st, "http://127.0.0.1:1/in/demo-shop"),
		Sessions: sessions, GitHub: gh, DevLogin: true, AdminToken: "s3cret", PublicURL: "https://holdfast.example",
		SelfURL: "http://127.0.0.1:1", Loc: time.UTC, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Routes(mux)
	return fixture{h: mux, st: st, rl: rl, gh: gh, srv: s}
}

func do(h http.Handler, method, path string, body url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(body.Encode())
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func get(h http.Handler, path string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, m := range mod {
		m(req)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// signIn uses the development sign-in and returns the session cookie.
func signIn(t *testing.T, f fixture) *http.Cookie {
	t.Helper()
	w := do(f.h, http.MethodGet, "/auth/dev", nil)
	for _, c := range w.Result().Cookies() {
		if c.Name == "hf_session" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func TestPagesRender(t *testing.T) {
	f := setup(t)
	for path, want := range map[string]string{
		"/":                          "held fast",
		"/demo":                      "Break the shop.",
		"/live":                      "Continue with GitHub",
		"/progress":                  "Unlock board",
		"/fragments/board/demo-shop": "Waiting for the first payment",
		"/healthz":                   "ok",
		"/static/app.css":            "--brand",
	} {
		w := get(f.h, path)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Errorf("GET %s: %d, missing %q", path, w.Code, want)
		}
	}
	if w := get(f.h, "/events/999"); w.Code != http.StatusNotFound {
		t.Errorf("missing event: %d", w.Code)
	}
}

func TestConfigRoutesNeedThePassword(t *testing.T) {
	f := setup(t)
	if w := get(f.h, "/r/client"); w.Code != http.StatusUnauthorized {
		t.Fatalf("no password: %d", w.Code)
	}
	if w := get(f.h, "/r/client", func(r *http.Request) { r.SetBasicAuth("x", "s3cret") }); w.Code != http.StatusOK {
		t.Fatalf("right password: %d", w.Code)
	}
}

func TestDemoRefusesCrossSitePosts(t *testing.T) {
	f := setup(t)
	req := httptest.NewRequest(http.MethodPost, "/demo/healthy", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site demo post: %d", w.Code)
	}
}

func TestLiveGitHubConnection(t *testing.T) {
	f := setup(t)
	me := signIn(t, f)
	if w := do(f.h, http.MethodGet, "/live", nil, me); !strings.Contains(w.Body.String(), "Connect your first repository") {
		t.Fatalf("signed-in live page: %s", w.Body.String())
	}
	if w := do(f.h, http.MethodGet, "/live/new", nil, me); !strings.Contains(w.Body.String(), "dev-user/shop-backend") {
		t.Fatal("the repository list is missing")
	}

	w := do(f.h, http.MethodPost, "/live/connections", url.Values{"kind": {"github"}, "repo": {"dev-user/shop-backend"}, "target": {"test"}}, me)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	route := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/r/"), "?new=1")
	if len(f.gh.created) != 1 || f.gh.created[0] != "dev-user/shop-backend → https://holdfast.example/in/"+route {
		t.Fatalf("webhook not added correctly: %v", f.gh.created)
	}
	rt, ok := f.rl.Route(route)
	if !ok || rt.Secret == "" || rt.Destination != "http://127.0.0.1:1/test-app/"+route || rt.Owner == 0 || rt.Guarded {
		t.Fatalf("route not registered correctly: %+v", rt)
	}
	if w := do(f.h, http.MethodGet, "/r/"+route, nil, me); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Test app") {
		t.Fatalf("owner can't see the connection: %d", w.Code)
	}

	// Somebody else, and somebody signed out, must not see it.
	if w := do(f.h, http.MethodGet, "/r/"+route, nil); w.Code != http.StatusNotFound {
		t.Fatalf("signed out: %d", w.Code)
	}
	other, _ := f.st.UpsertUser(context.Background(), store.User{GitHubID: 999, Login: "someone-else"})
	w2 := httptest.NewRecorder()
	f.srv.Sessions.SignIn(w2, httptest.NewRequest(http.MethodGet, "/", nil), other)
	if w := do(f.h, http.MethodGet, "/r/"+route, nil, w2.Result().Cookies()[0]); w.Code != http.StatusNotFound {
		t.Fatalf("another user saw the connection: %d", w.Code)
	}
	if w := do(f.h, http.MethodPost, "/r/"+route+"/delete", url.Values{}, w2.Result().Cookies()[0]); w.Code != http.StatusNotFound {
		t.Fatalf("another user could delete it: %d", w.Code)
	}

	if w := do(f.h, http.MethodPost, "/r/"+route+"/test-app/down", url.Values{}, me); w.Code != http.StatusSeeOther {
		t.Fatalf("switching the test app: %d", w.Code)
	}
	if f.srv.TestApp.Mode(route) != "down" {
		t.Fatal("test app mode not changed")
	}
	if w := do(f.h, http.MethodPost, "/r/"+route+"/delete", url.Values{}, me); w.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, ok := f.rl.Route(route); ok {
		t.Fatal("deleted route still accepts events")
	}
}

func TestLiveRefusesPrivateDestinations(t *testing.T) {
	f := setup(t)
	me := signIn(t, f)
	for _, dest := range []string{"http://127.0.0.1:11434/api/chat", "http://holdfast-ollama:11434", "http://169.254.169.254/"} {
		w := do(f.h, http.MethodPost, "/live/connections", url.Values{"kind": {"generic"}, "target": {"own"}, "destination": {dest}}, me)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "private network") {
			t.Errorf("destination %s: %d", dest, w.Code)
		}
	}
	if conns, _ := f.st.Connections(context.Background(), 1); len(conns) != 0 {
		t.Fatalf("a refused connection was saved: %v", conns)
	}
}

func TestStripeConnectionNeedsWhsecPrefix(t *testing.T) {
	f := setup(t)
	me := signIn(t, f)
	w := do(f.h, http.MethodPost, "/live/connections", url.Values{"kind": {"stripe"}, "target": {"test"}, "secret": {"nope"}}, me)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad secret accepted: %d", w.Code)
	}
	w = do(f.h, http.MethodPost, "/live/connections", url.Values{"kind": {"stripe"}, "target": {"test"}, "label": {"Shop"}}, me)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("stripe without a secret yet: %d", w.Code)
	}
	route := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/r/"), "?new=1")
	page := do(f.h, http.MethodGet, "/r/"+route, nil, me).Body.String()
	if !strings.Contains(page, "Signatures aren't checked until you add it") || !strings.Contains(page, "https://holdfast.example/in/"+route) {
		t.Fatal("the Stripe steps are missing")
	}
	if w := do(f.h, http.MethodPost, "/r/"+route+"/secret", url.Values{"secret": {"whsec_abc"}}, me); w.Code != http.StatusSeeOther {
		t.Fatalf("saving the secret: %d", w.Code)
	}
	if rt, _ := f.rl.Route(route); rt.Secret != "whsec_abc" {
		t.Fatalf("route secret not updated: %q", rt.Secret)
	}
}

var _ github.API = (*fakeGitHub)(nil)
