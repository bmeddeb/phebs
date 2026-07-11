package auth

import (
	"net"
	"net/http"
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

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}
