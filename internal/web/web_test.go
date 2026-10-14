package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tanay3484/holdfast/internal/config"
	"github.com/Tanay3484/holdfast/internal/demo"
	"github.com/Tanay3484/holdfast/internal/diagnose"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/store"
)

func server(t *testing.T) http.Handler {
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
	s, err := New(rl, shop, demo.NewRunner(shop, st, "http://127.0.0.1:1/in/demo-shop"), "s3cret", time.UTC, log)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Routes(mux)
	return mux
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

func TestPagesRender(t *testing.T) {
	h := server(t)
	for path, want := range map[string]string{
		"/":                          "held fast",
		"/progress":                  "Unlock board",
		"/fragments/board/demo-shop": "Waiting for the first payment",
		"/healthz":                   "ok",
		"/static/app.css":            "--brand",
	} {
		w := get(h, path)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Errorf("GET %s: %d, missing %q", path, w.Code, want)
		}
	}
	if w := get(h, "/events/999"); w.Code != http.StatusNotFound {
		t.Errorf("missing event: %d", w.Code)
	}
}

func TestPrivateRoutesNeedThePassword(t *testing.T) {
	h := server(t)
	if w := get(h, "/r/client"); w.Code != http.StatusUnauthorized {
		t.Fatalf("no password: %d", w.Code)
	}
	if w := get(h, "/r/client", func(r *http.Request) { r.SetBasicAuth("x", "wrong") }); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", w.Code)
	}
	if w := get(h, "/r/client", func(r *http.Request) { r.SetBasicAuth("x", "s3cret") }); w.Code != http.StatusOK {
		t.Fatalf("right password: %d", w.Code)
	}
}

func TestDemoRefusesCrossSitePosts(t *testing.T) {
	h := server(t)
	req := httptest.NewRequest(http.MethodPost, "/demo/healthy", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site demo post: %d", w.Code)
	}
}
