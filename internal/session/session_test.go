package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func cookieFrom(w *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestSignInRoundTrip(t *testing.T) {
	m, _ := New("k")
	w := httptest.NewRecorder()
	m.SignIn(w, httptest.NewRequest(http.MethodGet, "/", nil), 42)
	if id, ok := m.User(cookieFrom(w)); !ok || id != 42 {
		t.Fatalf("User = %d %v", id, ok)
	}
	other, _ := New("different key")
	if _, ok := other.User(cookieFrom(w)); ok {
		t.Fatal("a cookie signed with another key must be rejected")
	}
	m.now = func() time.Time { return time.Now().Add(15 * 24 * time.Hour) }
	if _, ok := m.User(cookieFrom(w)); ok {
		t.Fatal("an expired cookie must be rejected")
	}
}

func TestTamperedCookieIsRejected(t *testing.T) {
	m, _ := New("k")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: "OTk6OTk5OTk5OTk5OQ.forged"}) // "99:9999999999"
	if _, ok := m.User(r); ok {
		t.Fatal("forged cookie accepted")
	}
}

func TestSealOpen(t *testing.T) {
	m, _ := New("k")
	sealed, err := m.Seal("gho_secret_token")
	if err != nil || sealed == "gho_secret_token" {
		t.Fatalf("Seal = %q, %v", sealed, err)
	}
	if got, err := m.Open(sealed); err != nil || got != "gho_secret_token" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	other, _ := New("other")
	if _, err := other.Open(sealed); err != ErrUnreadable {
		t.Fatalf("Open with another key: %v", err)
	}
}

func TestOAuthState(t *testing.T) {
	m, _ := New("k")
	w := httptest.NewRecorder()
	state, _ := m.NewState(w, httptest.NewRequest(http.MethodGet, "/auth/github", nil))
	if !m.CheckState(httptest.NewRecorder(), cookieFrom(w), state) {
		t.Fatal("matching state rejected")
	}
	if m.CheckState(httptest.NewRecorder(), cookieFrom(w), "attacker") {
		t.Fatal("wrong state accepted")
	}
}
