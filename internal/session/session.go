// Package session signs the sign-in cookie and encrypts secrets kept on disk (GitHub tokens, webhook
// signing secrets). Both keys come from one HF_SESSION_KEY.
package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	cookieName = "hf_session"
	stateName  = "hf_oauth_state"
	lifetime   = 14 * 24 * time.Hour
)

// Manager holds the keys.
type Manager struct {
	sign []byte
	aead cipher.AEAD
	now  func() time.Time
}

// New derives the keys from secret. An empty secret gets a random one: sessions and stored tokens
// then last only until the process restarts, which is fine for local development.
func New(secret string) (*Manager, error) {
	if secret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		secret = string(b)
	}
	sk := sha256.Sum256([]byte("holdfast-sign:" + secret))
	ek := sha256.Sum256([]byte("holdfast-seal:" + secret))
	block, err := aes.NewCipher(ek[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Manager{sign: sk[:], aead: aead, now: time.Now}, nil
}

func (m *Manager) mac(s string) string {
	h := hmac.New(sha256.New, m.sign)
	h.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// SignIn sets the session cookie for a user.
func (m *Manager) SignIn(w http.ResponseWriter, r *http.Request, userID int64) {
	payload := fmt.Sprintf("%d:%d", userID, m.now().Add(lifetime).Unix())
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + m.mac(payload)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true, Secure: secure(r),
		SameSite: http.SameSiteLaxMode, MaxAge: int(lifetime.Seconds())})
}

// SignOut clears the session cookie.
func (m *Manager) SignOut(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure(r), MaxAge: -1})
}

// User returns the signed-in user's id, if the cookie is valid and not expired.
func (m *Manager) User(r *http.Request) (int64, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return 0, false
	}
	enc, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || subtle.ConstantTimeCompare([]byte(sig), []byte(m.mac(string(raw)))) != 1 {
		return 0, false
	}
	idS, expS, ok := strings.Cut(string(raw), ":")
	if !ok {
		return 0, false
	}
	id, err1 := strconv.ParseInt(idS, 10, 64)
	exp, err2 := strconv.ParseInt(expS, 10, 64)
	if err1 != nil || err2 != nil || m.now().Unix() > exp || id <= 0 {
		return 0, false
	}
	return id, true
}

// NewState sets a short-lived random OAuth state cookie and returns its value.
func (m *Manager) NewState(w http.ResponseWriter, r *http.Request) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: stateName, Value: state, Path: "/auth/", HttpOnly: true, Secure: secure(r),
		SameSite: http.SameSiteLaxMode, MaxAge: 600})
	return state, nil
}

// CheckState reports whether got matches the state cookie, and clears it.
func (m *Manager) CheckState(w http.ResponseWriter, r *http.Request, got string) bool {
	c, err := r.Cookie(stateName)
	http.SetCookie(w, &http.Cookie{Name: stateName, Value: "", Path: "/auth/", MaxAge: -1})
	return err == nil && got != "" && subtle.ConstantTimeCompare([]byte(c.Value), []byte(got)) == 1
}

// Seal encrypts a secret for storage.
func (m *Manager) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(m.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// ErrUnreadable means a stored secret was sealed with another key (HF_SESSION_KEY changed).
var ErrUnreadable = errors.New("stored secret can't be read with this key")

// Open decrypts a sealed secret.
func (m *Manager) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	n := m.aead.NonceSize()
	if err != nil || len(raw) < n {
		return "", ErrUnreadable
	}
	plain, err := m.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", ErrUnreadable
	}
	return string(plain), nil
}
