// Package netguard keeps user-supplied destinations on the public internet.
//
// A Live user types the URL Holdfast forwards their webhooks to. Without this, they could point it at
// Holdfast's own private network (the Ollama service, the cloud metadata address) and read the answers
// on the event page. The URL is checked when it is saved, and every connection is checked again at
// dial time, so a hostname that later resolves to a private address is still refused.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrPrivate is returned for an address that is not on the public internet.
var ErrPrivate = errors.New("that address is on a private network")

var blocked = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// Public reports whether ip is a normal public unicast address.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// CheckURL validates a destination a user typed: http(s), a host, and only public addresses.
func CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("enter a full address starting with https://")
	}
	if u.User != nil {
		return errors.New("addresses with a user name or password are not allowed")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if !Public(ip) {
			return ErrPrivate
		}
		return nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") ||
		!strings.Contains(host, ".") {
		return ErrPrivate
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("couldn't find %s on the internet", host)
	}
	for _, a := range addrs {
		if !Public(a) {
			return ErrPrivate
		}
	}
	return nil
}

// Transport is an HTTP transport that refuses to connect to non-public addresses.
func Transport() *http.Transport {
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !Public(ap.Addr()) {
				return ErrPrivate
			}
			return nil
		},
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil // a proxy would dial on our behalf and skip the check
	t.DialContext = d.DialContext
	return t
}
