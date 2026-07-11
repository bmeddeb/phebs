package auth

import (
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
