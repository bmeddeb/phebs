package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginLimiterAtomicallyReservesInFlightAttempts(t *testing.T) {
	now := time.Now()
	limiter := newLoginLimiter(3, time.Minute, 16)
	reservations := make([]*attemptReservation, 0, 3)
	for range 3 {
		reservation, ok := limiter.reserve("client", now)
		if !ok {
			t.Fatal("reservation rejected below limit")
		}
		reservations = append(reservations, reservation)
	}
	if _, ok := limiter.reserve("client", now); ok {
		t.Fatal("concurrent attempt exceeded atomic reservation limit")
	}
	reservations[0].cancel()
	replacement, ok := limiter.reserve("client", now)
	if !ok {
		t.Fatal("canceled infrastructure attempt did not release reservation")
	}
	replacement.fail(now)
	reservations[1].fail(now)
	reservations[2].cancel()
	if _, ok := limiter.reserve("client", now); !ok {
		t.Fatal("two failures should leave one attempt available")
	}
}

func TestFixedWindowConsumeIsBounded(t *testing.T) {
	now := time.Now()
	limiter := newLoginLimiter(2, time.Minute, 16)
	if !limiter.consume("client", now) {
		t.Fatal("fixed-window limiter rejected first attempt")
	}
	if !limiter.consume("client", now) {
		t.Fatal("fixed-window limiter rejected second attempt")
	}
	if limiter.consume("client", now) {
		t.Fatal("fixed-window limiter accepted attempt above configured bound")
	}
	if !limiter.consume("client", now.Add(2*time.Minute)) {
		t.Fatal("fixed-window limiter did not reset")
	}
}

func TestLimiterNeverEvictsActiveReservations(t *testing.T) {
	now := time.Now()
	limiter := newLoginLimiter(2, time.Minute, 2)
	first, ok := limiter.reserve("first", now)
	if !ok {
		t.Fatal("first reservation rejected")
	}
	second, ok := limiter.reserve("second", now)
	if !ok {
		t.Fatal("second reservation rejected")
	}
	if _, ok := limiter.reserve("churn", now.Add(2*time.Minute)); ok {
		t.Fatal("new key evicted an active reservation")
	}
	first.cancel()
	if _, ok := limiter.reserve("churn", now.Add(2*time.Minute)); !ok {
		t.Fatal("released reservation did not make bounded-map capacity available")
	}
	second.cancel()
}

func TestClientIPResolverTrustsOnlyConfiguredProxyChains(t *testing.T) {
	resolver, err := newClientIPResolver([]string{"10.0.0.0/8", "fd00::/8"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{name: "untrusted peer ignores spoofed header", remoteAddr: "192.0.2.10:1234", forwarded: "198.51.100.1", want: "192.0.2.10"},
		{name: "trusted proxy separates clients", remoteAddr: "10.0.0.5:1234", forwarded: "198.51.100.2", want: "198.51.100.2"},
		{name: "walks trusted proxy chain", remoteAddr: "10.0.0.5:1234", forwarded: "198.51.100.3, 10.0.0.4", want: "198.51.100.3"},
		{name: "rejects spoofed leftmost address", remoteAddr: "10.0.0.5:1234", forwarded: "203.0.113.9, 198.51.100.4", want: "198.51.100.4"},
		{name: "malformed chain falls back to peer", remoteAddr: "10.0.0.5:1234", forwarded: "not-an-ip", want: "10.0.0.5"},
		{name: "trusted IPv6 proxy", remoteAddr: "[fd00::1]:1234", forwarded: "2001:db8::1", want: "2001:db8::1"},
		{name: "zoned IPv6 peer has stable key", remoteAddr: "[fe80::1%25eth0]:1234", forwarded: "198.51.100.5", want: "fe80::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			r.Header.Set("X-Forwarded-For", tt.forwarded)
			if got := resolver.clientKey(r); got != tt.want {
				t.Fatalf("clientKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIPResolverRejectsInvalidCIDR(t *testing.T) {
	if _, err := newClientIPResolver([]string{"127.0.0.1"}); err == nil {
		t.Fatal("invalid trusted proxy CIDR was accepted")
	}
}
