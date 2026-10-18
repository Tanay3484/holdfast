// Command holdfast runs the relay, the website and the live demo in one process.
//
//	go run ./cmd/holdfast
//
// Settings come from environment variables; see .env.example.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // HF_TIMEZONE works in a bare container

	"github.com/Tanay3484/holdfast/internal/buildinfo"
	"github.com/Tanay3484/holdfast/internal/config"
	"github.com/Tanay3484/holdfast/internal/demo"
	"github.com/Tanay3484/holdfast/internal/diagnose"
	"github.com/Tanay3484/holdfast/internal/github"
	"github.com/Tanay3484/holdfast/internal/ollama"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/session"
	"github.com/Tanay3484/holdfast/internal/store"
	"github.com/Tanay3484/holdfast/internal/testapp"
	"github.com/Tanay3484/holdfast/internal/unlock"
	"github.com/Tanay3484/holdfast/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("holdfast stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	var routes []relay.Route
	for _, rc := range cfg.Routes {
		routes = append(routes, relay.Route{Route: rc, Timeout: rc.TimeoutOr(10 * time.Second), Policy: policy.Default()})
	}
	var shop *demo.Shop
	if cfg.Demo {
		shop = demo.NewShop()
		routes = append(routes, relay.Route{
			Route: config.Route{
				Name: demo.RouteName, Source: "stripe", Secret: demo.Secret, Client: demo.Client, Public: true,
				Destination: "http://127.0.0.1" + cfg.Addr[strings.LastIndex(cfg.Addr, ":"):] + "/demo-shop/webhook",
			},
			Timeout: demo.Timeout,
			Policy:  demo.Policy,
		})
	}

	model := ollama.New(cfg.OllamaURL, cfg.Model, cfg.ModelTimeout, cfg.ModelThreads)
	model.MaxTokens = cfg.ModelTokens
	rl := relay.New(st, routes, diagnose.Diagnoser{Model: model}, cfg.TimeZone, cfg.Retention, log)

	self := "http://127.0.0.1" + cfg.Addr[strings.LastIndex(cfg.Addr, ":"):]
	var runner *demo.Runner
	if shop != nil {
		runner = demo.NewRunner(shop, st, self+"/in/"+demo.RouteName)
	}

	// Live mode: sessions, GitHub sign-in, and the connections people made before a restart.
	sessions, err := session.New(cfg.SessionKey)
	if err != nil {
		return err
	}
	if cfg.SessionKey == "" {
		log.Warn("HF_SESSION_KEY is not set: sign-ins and stored GitHub tokens last only until restart")
	}
	var gh github.API
	switch {
	case cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "":
		gh = github.New(cfg.GitHubClientID, cfg.GitHubClientSecret)
	case cfg.DevLogin:
		gh = web.DevGitHub{}
		log.Warn("HF_DEV_LOGIN is on: sign-in and GitHub are simulated. Never use this on a public server.")
	}
	conns, err := st.AllConnections(context.Background())
	if err != nil {
		return err
	}
	for _, c := range conns {
		secret, err := sessions.Open(c.Secret)
		if err != nil {
			log.Warn("connection secret unreadable (HF_SESSION_KEY changed?)", "connection", c.Name)
		}
		rl.SetRoute(web.RouteFor(c, secret, self))
	}

	site, err := web.New(web.Options{
		Relay: rl, Shop: shop, Demo: runner, TestApp: testapp.New(), Sessions: sessions, GitHub: gh,
		DevLogin: cfg.DevLogin, AdminToken: cfg.AdminToken, PublicURL: cfg.PublicURL, SelfURL: self,
		MaxConnections: cfg.MaxConnections, Loc: cfg.TimeZone, Log: log,
	})
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	site.Routes(mux)

	board := unlock.Current()
	log.Info("holdfast starting", "version", buildinfo.Version, "addr", cfg.Addr, "routes", len(routes),
		"connections", len(conns), "github", gh != nil, "model", cfg.Model, "unlocked", board.Unlocked, "levels", board.Total)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	done := make(chan struct{})
	go func() { rl.Run(ctx); close(done) }()
	if runner != nil {
		go runner.Seed(ctx) // a fresh demo shows a few payments instead of an empty page
	}

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
			<-done
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	<-done
	return nil
}
