package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAgainstAFakeGitHub(t *testing.T) {
	var hookBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "good" {
				json.NewEncoder(w).Encode(map[string]string{"error_description": "bad code"})
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_x"})
		case r.Header.Get("Authorization") != "Bearer gho_x":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/user":
			json.NewEncoder(w).Encode(User{ID: 7, Login: "tanay"})
		case r.URL.Path == "/user/repos":
			w.Write([]byte(`[{"full_name":"tanay/holdfast","permissions":{"admin":true}},{"full_name":"other/x","permissions":{"admin":false}}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/tanay/holdfast/hooks":
			json.NewDecoder(r.Body).Decode(&hookBody)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":99}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/repos/tanay/holdfast/hooks/99":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()
	c := New("id", "secret")
	c.WebBase, c.APIBase = srv.URL, srv.URL
	ctx := context.Background()

	if u := c.AuthURL("st", "https://h.example/auth/github/callback"); !strings.Contains(u, "scope=admin%3Arepo_hook+read%3Auser") || !strings.Contains(u, "state=st") {
		t.Fatalf("AuthURL = %s", u)
	}
	if _, err := c.Exchange(ctx, "bad", "r"); err == nil {
		t.Fatal("a bad code must fail")
	}
	tok, err := c.Exchange(ctx, "good", "r")
	if err != nil || tok != "gho_x" {
		t.Fatalf("Exchange = %q %v", tok, err)
	}
	if u, err := c.User(ctx, tok); err != nil || u.Login != "tanay" {
		t.Fatalf("User = %+v %v", u, err)
	}
	repos, err := c.Repos(ctx, tok)
	if err != nil || len(repos) != 1 || repos[0].FullName != "tanay/holdfast" {
		t.Fatalf("Repos = %+v %v (only admin repos)", repos, err)
	}
	id, err := c.CreateHook(ctx, tok, "tanay/holdfast", "https://h.example/in/c-1", "s3")
	if err != nil || id != 99 {
		t.Fatalf("CreateHook = %d %v", id, err)
	}
	if cfg := hookBody["config"].(map[string]any); cfg["secret"] != "s3" || cfg["content_type"] != "json" {
		t.Fatalf("hook config = %v", cfg)
	}
	if err := c.DeleteHook(ctx, tok, "tanay/holdfast", 99); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteHook(ctx, tok, "tanay/holdfast", 12345); err != nil {
		t.Fatalf("deleting a hook that is already gone: %v", err)
	}
	if _, err := c.User(ctx, "wrong"); err == nil {
		t.Fatal("a bad token must fail")
	}
}
