// Package github is the small slice of GitHub that Live mode needs: OAuth sign-in, listing the
// repositories a user administers, and adding or removing one webhook.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Scope is the only permission requested: add and remove webhooks. It cannot read or change code.
const Scope = "admin:repo_hook read:user"

// Events are the webhook events Holdfast subscribes a repository to. GitHub also sends a "ping".
var Events = []string{"push", "pull_request", "issues", "issue_comment", "release", "star", "create", "delete", "workflow_run"}

// User is the signed-in GitHub account.
type User struct {
	ID     int64  `json:"id"`
	Login  string `json:"login"`
	Name   string `json:"name"`
	Avatar string `json:"avatar_url"`
}

// Repo is a repository the user can add webhooks to.
type Repo struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	Pushed   string `json:"pushed_at"`
}

// API is what the web layer uses, so tests and local development can fake it.
type API interface {
	AuthURL(state, redirect string) string
	Exchange(ctx context.Context, code, redirect string) (token string, err error)
	User(ctx context.Context, token string) (User, error)
	Repos(ctx context.Context, token string) ([]Repo, error)
	CreateHook(ctx context.Context, token, repo, hookURL, secret string) (int64, error)
	DeleteHook(ctx context.Context, token, repo string, id int64) error
}

// Client talks to github.com.
type Client struct {
	ClientID, ClientSecret string
	WebBase, APIBase       string // base URLs; empty means github.com
	HTTP                   *http.Client
}

// New returns a client for an OAuth app.
func New(clientID, clientSecret string) *Client {
	return &Client{ClientID: clientID, ClientSecret: clientSecret, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) web() string {
	if c.WebBase != "" {
		return c.WebBase
	}
	return "https://github.com"
}

func (c *Client) api() string {
	if c.APIBase != "" {
		return c.APIBase
	}
	return "https://api.github.com"
}

// AuthURL is where the browser goes to sign in.
func (c *Client) AuthURL(state, redirect string) string {
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {redirect}, "scope": {Scope}, "state": {state},
		"allow_signup": {"false"}}
	return c.web() + "/login/oauth/authorize?" + q.Encode()
}

// Exchange trades the callback code for an access token.
func (c *Client) Exchange(ctx context.Context, code, redirect string) (string, error) {
	form := url.Values{"client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "code": {code}, "redirect_uri": {redirect}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.web()+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		Token string `json:"access_token"`
		Error string `json:"error_description"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("github sign-in failed: %s", out.Error)
	}
	return out.Token, nil
}

// User returns the account behind a token.
func (c *Client) User(ctx context.Context, token string) (User, error) {
	var u User
	return u, c.call(ctx, token, http.MethodGet, "/user", nil, &u)
}

// Repos lists repositories the user administers, most recently pushed first.
func (c *Client) Repos(ctx context.Context, token string) ([]Repo, error) {
	var all []struct {
		Repo
		Permissions struct {
			Admin bool `json:"admin"`
		} `json:"permissions"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/user/repos?per_page=100&sort=pushed&affiliation=owner,organization_member", nil, &all); err != nil {
		return nil, err
	}
	var out []Repo
	for _, r := range all {
		if r.Permissions.Admin {
			out = append(out, r.Repo)
		}
	}
	return out, nil
}

// CreateHook adds a webhook to repo ("owner/name") and returns its id.
func (c *Client) CreateHook(ctx context.Context, token, repo, hookURL, secret string) (int64, error) {
	body := map[string]any{
		"name": "web", "active": true, "events": Events,
		"config": map[string]string{"url": hookURL, "content_type": "json", "secret": secret, "insecure_ssl": "0"},
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.call(ctx, token, http.MethodPost, "/repos/"+repo+"/hooks", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// DeleteHook removes a webhook. A hook that is already gone is not an error.
func (c *Client) DeleteHook(ctx context.Context, token, repo string, id int64) error {
	err := c.call(ctx, token, http.MethodDelete, fmt.Sprintf("/repos/%s/hooks/%d", repo, id), nil, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// StatusError is a non-2xx answer from GitHub.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string { return fmt.Sprintf("github answered %d: %s", e.Code, e.Message) }

func (c *Client) call(ctx context.Context, token, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		return &StatusError{Code: resp.StatusCode, Message: e.Message}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
