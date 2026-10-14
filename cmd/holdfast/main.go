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
	"github.com/Tanay3484/holdfast/internal/ollama"
	"github.com/Tanay3484/holdfast/internal/policy"
	"github.com/Tanay3484/holdfast/internal/relay"
	"github.com/Tanay3484/holdfast/internal/store"
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
	rl := relay.New(st, routes, diagnose.Diagnoser{Model: model}, cfg.TimeZone, cfg.Retention, log)

	var runner *demo.Runner
	if shop != nil {
		runner = demo.NewRunner(shop, st, "http://127.0.0.1"+cfg.Addr[strings.LastIndex(cfg.Addr, ":"):]+"/in/"+demo.RouteName)
	}
	site, err := web.New(rl, shop, runner, cfg.AdminToken, cfg.TimeZone, log)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	site.Routes(mux)

	board := unlock.Current()
	log.Info("holdfast starting", "version", buildinfo.Version, "addr", cfg.Addr, "routes", len(routes),
		"model", cfg.Model, "unlocked", board.Unlocked, "levels", board.Total)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	done := make(chan struct{})
	go func() { rl.Run(ctx); close(done) }()

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
