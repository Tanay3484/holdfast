package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sign(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func TestGitHub(t *testing.T) {
	const secret, body = "s3cret", `{"zen":"Keep it logically awesome."}`
	good := "sha256=" + sign(secret, body)
	cases := map[string]struct {
		header string
		want   error
	}{
		"matches":         {good, nil},
		"uppercase hex":   {"sha256=" + strings.ToUpper(sign(secret, body)), nil},
		"empty":           {"", ErrMissingSignature},
		"old sha1 header": {"sha1=" + sign(secret, body), ErrMalformedHeader},
		"not hex":         {"sha256=zzzz", ErrMalformedHeader},
		"wrong secret":    {"sha256=" + sign("other", body), ErrBadSignature},
	}
	for name, c := range cases {
		if err := GitHub(secret, []byte(body), c.header); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

func TestStripe(t *testing.T) {
	const secret, body = "whsec_x", `{"id":"evt_1"}`
	now := time.Unix(1_792_050_000, 0)
	tol := 5 * time.Minute
	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) }
	header := func(ts string, sigs ...string) string {
		h := "t=" + ts
		for _, s := range sigs {
			h += ",v1=" + s
		}
		return h
	}
	ts := at(0)
	good := sign(secret, ts+"."+body)
	cases := map[string]struct {
		header string
		want   error
	}{
		"fresh and correct":         {header(ts, good), nil},
		"pieces in any order":       {"v1=" + good + ",t=" + ts, nil},
		"second v1 matches":         {header(ts, sign("old", ts+"."+body), good), nil},
		"v0 and junk are ignored":   {header(ts, good) + ",v0=abc,junk", nil},
		"a v1 that is not hex":      {header(ts, "zz", good), nil},
		"empty":                     {"", ErrMissingSignature},
		"no timestamp":              {"v1=" + good, ErrMalformedHeader},
		"timestamp not a number":    {header("soon", good), ErrMalformedHeader},
		"no v1":                     {"t=" + ts + ",v0=abc", ErrMalformedHeader},
		"wrong secret":              {header(ts, sign("other", ts+"."+body)), ErrBadSignature},
		"signed a different body":   {header(ts, sign(secret, ts+".{}")), ErrBadSignature},
		"ten minutes old":           {header(at(-10*time.Minute), sign(secret, at(-10*time.Minute)+"."+body)), ErrStaleTimestamp},
		"ten minutes in the future": {header(at(10*time.Minute), sign(secret, at(10*time.Minute)+"."+body)), ErrStaleTimestamp},
		"old and wrong is wrong":    {header(at(-time.Hour), "abcd"), ErrBadSignature},
	}
	for name, c := range cases {
		if err := Stripe(secret, []byte(body), c.header, now, tol); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}
