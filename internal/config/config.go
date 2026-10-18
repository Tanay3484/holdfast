// Package config reads every setting from environment variables prefixed HF_ (plus PORT).
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Route is one provider → app connection. Providers send to /in/<Name>.
type Route struct {
	Name        string `json:"name"`
	Source      string `json:"source"`      // github | stripe | generic
	Secret      string `json:"secret"`      // webhook signing secret; empty means "don't check"
	Destination string `json:"destination"` // the app's webhook URL
	Client      string `json:"client"`      // who the app belongs to, for the client note
	Public      bool   `json:"public"`      // show this route's events without the admin password
	Timeout     string `json:"timeout"`     // per-attempt timeout, e.g. "10s"
}

// Config is the whole configuration.
type Config struct {
	Addr         string
	DB           string
	OllamaURL    string
	Model        string
	ModelTimeout time.Duration
	ModelThreads int
	ModelTokens  int
	AdminToken   string
	Routes       []Route
	Retention    time.Duration
	TimeZone     *time.Location
	Demo         bool

	// Live mode
	GitHubClientID     string
	GitHubClientSecret string
	SessionKey         string
	PublicURL          string // e.g. https://holdfast.onrender.com; defaults to Render's RENDER_EXTERNAL_URL
	DevLogin           bool   // local development only: sign in without GitHub
	MaxConnections     int
}

var routeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// Load reads the environment.
func Load() (Config, error) {
	c := Config{
		Addr:       ":" + env("PORT", "8080"),
		DB:         env("HF_DB", "data/holdfast.db"),
		OllamaURL:  env("HF_OLLAMA_URL", "http://localhost:11434"),
		Model:      env("HF_MODEL", "gemma3:4b"),
		AdminToken: os.Getenv("HF_ADMIN_TOKEN"),
		Demo:       env("HF_DEMO", "true") == "true",

		GitHubClientID:     os.Getenv("HF_GITHUB_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("HF_GITHUB_CLIENT_SECRET"),
		SessionKey:         os.Getenv("HF_SESSION_KEY"),
		PublicURL:          env("HF_PUBLIC_URL", os.Getenv("RENDER_EXTERNAL_URL")),
		DevLogin:           os.Getenv("HF_DEV_LOGIN") == "true",
	}
	// Render's fromService hostport has no scheme.
	if !strings.Contains(c.OllamaURL, "://") {
		c.OllamaURL = "http://" + c.OllamaURL
	}
	var err error
	if c.ModelTimeout, err = time.ParseDuration(env("HF_MODEL_TIMEOUT", "90s")); err != nil {
		return c, fmt.Errorf("HF_MODEL_TIMEOUT: %w", err)
	}
	if c.ModelTokens, err = strconv.Atoi(env("HF_MODEL_MAX_TOKENS", "400")); err != nil {
		return c, fmt.Errorf("HF_MODEL_MAX_TOKENS: %w", err)
	}
	if c.ModelThreads, err = strconv.Atoi(env("HF_MODEL_THREADS", "0")); err != nil {
		return c, fmt.Errorf("HF_MODEL_THREADS: %w", err)
	}
	if c.MaxConnections, err = strconv.Atoi(env("HF_MAX_CONNECTIONS", "5")); err != nil {
		return c, fmt.Errorf("HF_MAX_CONNECTIONS: %w", err)
	}
	if c.Retention, err = time.ParseDuration(env("HF_RETENTION", "72h")); err != nil {
		return c, fmt.Errorf("HF_RETENTION: %w", err)
	}
	if c.TimeZone, err = time.LoadLocation(env("HF_TIMEZONE", "UTC")); err != nil {
		return c, fmt.Errorf("HF_TIMEZONE: %w", err)
	}
	if raw := os.Getenv("HF_ROUTES"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.Routes); err != nil {
			return c, fmt.Errorf("HF_ROUTES is not valid JSON: %w", err)
		}
	}
	for _, r := range c.Routes {
		if err := r.Validate(); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Validate checks one route.
func (r Route) Validate() error {
	if !routeName.MatchString(r.Name) {
		return fmt.Errorf("route %q: name must be lowercase letters, digits and dashes", r.Name)
	}
	switch r.Source {
	case "github", "stripe", "generic":
	default:
		return fmt.Errorf("route %q: source must be github, stripe or generic", r.Name)
	}
	u, err := url.Parse(r.Destination)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("route %q: destination must be an http(s) URL", r.Name)
	}
	if r.Timeout != "" {
		if _, err := time.ParseDuration(r.Timeout); err != nil {
			return fmt.Errorf("route %q: timeout: %w", r.Name, err)
		}
	}
	return nil
}

// TimeoutOr returns the route's timeout, or def if none is set.
func (r Route) TimeoutOr(def time.Duration) time.Duration {
	if d, err := time.ParseDuration(r.Timeout); err == nil && d > 0 {
		return d
	}
	return def
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
