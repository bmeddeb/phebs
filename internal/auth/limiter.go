package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type loginAttempt struct {
	failures int
	inFlight int
	resetAt  time.Time
}

type attemptReservation struct {
	limiter *loginLimiter
	key     string
	active  bool
}

// loginLimiter is deliberately local and bounded. It is a first line of
// defense for the standalone profile; fleet deployments should add a shared
// edge limiter before authentication traffic reaches replicas.
type loginLimiter struct {
	mu         sync.Mutex
	attempts   map[string]loginAttempt
	max        int
	window     time.Duration
	maxEntries int
}

func newLoginLimiter(max int, window time.Duration, maxEntries int) *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt), max: max, window: window, maxEntries: maxEntries}
}

func (l *loginLimiter) reserve(key string, now time.Time) (*attemptReservation, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.makeRoom(key, now) {
		return nil, false
	}
	attempt := l.attempts[key]
	if !attempt.resetAt.After(now) && attempt.inFlight == 0 {
		attempt = loginAttempt{resetAt: now.Add(l.window)}
	}
	if attempt.failures+attempt.inFlight >= l.max {
		return nil, false
	}
	attempt.inFlight++
	l.attempts[key] = attempt
	return &attemptReservation{limiter: l, key: key, active: true}, true
}

func (l *loginLimiter) makeRoom(key string, now time.Time) bool {
	if _, exists := l.attempts[key]; exists {
		return true
	}
	if len(l.attempts) >= l.maxEntries {
		for existing, attempt := range l.attempts {
			if attempt.inFlight == 0 && !attempt.resetAt.After(now) {
				delete(l.attempts, existing)
			}
		}
	}
	return len(l.attempts) < l.maxEntries
}

func (l *loginLimiter) consume(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.makeRoom(key, now) {
		return false
	}
	attempt := l.attempts[key]
	if !attempt.resetAt.After(now) && attempt.inFlight == 0 {
		attempt = loginAttempt{resetAt: now.Add(l.window)}
	}
	if attempt.failures >= l.max {
		return false
	}
	attempt.failures++
	l.attempts[key] = attempt
	return true
}

func (r *attemptReservation) cancel() {
	if r == nil || !r.active {
		return
	}
	r.limiter.mu.Lock()
	attempt := r.limiter.attempts[r.key]
	if attempt.inFlight > 0 {
		attempt.inFlight--
	}
	if attempt.failures == 0 && attempt.inFlight == 0 {
		delete(r.limiter.attempts, r.key)
	} else {
		r.limiter.attempts[r.key] = attempt
	}
	r.limiter.mu.Unlock()
	r.active = false
}

func (r *attemptReservation) fail(now time.Time) {
	if r == nil || !r.active {
		return
	}
	r.limiter.mu.Lock()
	attempt := r.limiter.attempts[r.key]
	if !attempt.resetAt.After(now) {
		attempt.failures = 0
		attempt.resetAt = now.Add(r.limiter.window)
	}
	if attempt.inFlight > 0 {
		attempt.inFlight--
	}
	attempt.failures++
	r.limiter.attempts[r.key] = attempt
	r.limiter.mu.Unlock()
	r.active = false
}

func (r *attemptReservation) success() {
	if r == nil || !r.active {
		return
	}
	r.limiter.mu.Lock()
	attempt := r.limiter.attempts[r.key]
	if attempt.inFlight > 0 {
		attempt.inFlight--
	}
	attempt.failures = 0
	if attempt.inFlight == 0 {
		delete(r.limiter.attempts, r.key)
	} else {
		r.limiter.attempts[r.key] = attempt
	}
	r.limiter.mu.Unlock()
	r.active = false
}

type clientIPResolver struct {
	trustedProxies []*net.IPNet
}

func newClientIPResolver(cidrs []string) (clientIPResolver, error) {
	resolver := clientIPResolver{trustedProxies: make([]*net.IPNet, 0, len(cidrs))}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return clientIPResolver{}, fmt.Errorf("invalid CIDR %q", cidr)
		}
		resolver.trustedProxies = append(resolver.trustedProxies, network)
	}
	return resolver, nil
}

func (c clientIPResolver) clientKey(r *http.Request) string {
	peer := remoteIP(r.RemoteAddr)
	if peer == nil {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
			return host
		}
		if r.RemoteAddr != "" {
			return r.RemoteAddr
		}
		return "unknown"
	}
	if !c.trusted(peer) {
		return peer.String()
	}

	// Walk from the proxy nearest to phebs toward the originating client.
	// The first address outside the configured proxy boundary is the client;
	// this prevents a spoofed left-most value from overriding an appended
	// chain. A malformed chain fails closed to the direct peer bucket.
	values := r.Header.Values("X-Forwarded-For")
	forwarded := strings.Split(strings.Join(values, ","), ",")
	var furthest net.IP
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate := net.ParseIP(strings.TrimSpace(forwarded[i]))
		if candidate == nil {
			return peer.String()
		}
		furthest = candidate
		if !c.trusted(candidate) {
			return candidate.String()
		}
	}
	if furthest != nil {
		return furthest.String()
	}
	return peer.String()
}

func (c clientIPResolver) trusted(ip net.IP) bool {
	for _, network := range c.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		if unzoned, _, ok := strings.Cut(host, "%"); ok {
			host = unzoned
		}
		return net.ParseIP(host)
	}
	if unzoned, _, ok := strings.Cut(remoteAddr, "%"); ok {
		remoteAddr = unzoned
	}
	return net.ParseIP(remoteAddr)
}
