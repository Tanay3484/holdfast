package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.9": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.1.1": false, "0.0.0.0": false, "::1": false,
		"fd00::1": false, "::ffff:127.0.0.1": false,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestCheckURLWithoutNetwork(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{"http://127.0.0.1:11434/api", "https://localhost/hook", "http://holdfast-ollama:11434",
		"http://169.254.169.254/latest", "https://[::1]/x", "http://10.0.0.5/"} {
		if err := CheckURL(ctx, raw); err == nil {
			t.Errorf("CheckURL(%q) allowed a private address", raw)
		}
	}
	for _, raw := range []string{"ftp://example.com", "example.com/hook", "https://user:pw@example.com/"} {
		if err := CheckURL(ctx, raw); err == nil || errors.Is(err, ErrPrivate) {
			t.Errorf("CheckURL(%q) = %v, want a format error", raw, err)
		}
	}
	if err := CheckURL(ctx, "https://93.184.215.14/hook"); err != nil {
		t.Errorf("a public IP was refused: %v", err)
	}
}

func TestTransportRefusesLoopbackAtDialTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	_, err := (&http.Client{Transport: Transport()}).Get(srv.URL)
	if !errors.Is(err, ErrPrivate) {
		t.Fatalf("dialing %s: got %v, want ErrPrivate", srv.URL, err)
	}
}
