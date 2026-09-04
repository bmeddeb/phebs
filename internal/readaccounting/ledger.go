// Package readaccounting observes explicitly scoped native read work. Ordinary
// callers attach no ledger. Counts are operation attempts or decoded records,
// not byte counts, metadata syscalls, or estimates from publication topology.
package readaccounting

import (
	"context"
	"errors"
	"math"
	"sync"
)

type Kind uint8

const (
	ControlFileRead Kind = iota
	StoreReadAttempt
	MemberVisit
	StoreWriteAttempt
)

// Counts contains only fixed, source-free counters. A control read includes
// its failed preflight/open; independent metadata probes are not control reads.
// A member visit counts one application record successfully decoded from a
// bounded member payload, including rereads and records later rejected by
// framing, canonical, semantic, or consumer validation.
type Counts struct {
	ControlFileReads   uint64
	StoreReadAttempts  uint64
	MemberVisits       uint64
	StoreWriteAttempts uint64
}

var (
	ErrScope  = errors.New("read accounting scope is invalid")
	ErrEvent  = errors.New("read accounting event is invalid")
	ErrLimit  = errors.New("read accounting limit exceeded")
	ErrClosed = errors.New("read accounting scope is closed")
)

// IsError reports whether err is one of the fail-closed accounting errors.
func IsError(err error) bool {
	return errors.Is(err, ErrScope) || errors.Is(err, ErrEvent) ||
		errors.Is(err, ErrLimit) || errors.Is(err, ErrClosed)
}

type contextKey struct{}

// Ledger retains no paths, records, error causes, or unbounded event history.
// It must not be copied. Its owner must join all scoped operations before
// Finish; attaching the returned context never proves uninstrumented paths.
// Accounting does not replace the operation's own error/cancellation result.
type Ledger struct {
	// ponytail: one mutex per explicit scope; split only if measured observer
	// contention matters. No ordinary operation creates or locks this mutex.
	mu     sync.Mutex
	counts Counts
	limits Counts
	closed bool
	err    error
}

// Start attaches an independent ledger. Nested scopes refuse instead of
// silently hiding work from their parent. Zero limits forbid that category;
// MaxUint64 is excluded so the first over-limit attempt has an exact sentinel.
func Start(ctx context.Context, limits Counts) (context.Context, *Ledger, error) {
	if ctx == nil {
		return nil, nil, ErrScope
	}
	if parent, _ := ctx.Value(contextKey{}).(*Ledger); parent != nil {
		parent.mu.Lock()
		if parent.err == nil {
			parent.err = ErrScope
		}
		parent.mu.Unlock()
		return nil, nil, ErrScope
	}
	if limits.ControlFileReads == math.MaxUint64 || limits.StoreReadAttempts == math.MaxUint64 ||
		limits.MemberVisits == math.MaxUint64 || limits.StoreWriteAttempts == math.MaxUint64 {
		return nil, nil, ErrScope
	}
	ledger := &Ledger{limits: limits}
	return context.WithValue(ctx, contextKey{}, ledger), ledger, nil
}

// Charge records the operation before its I/O, or records successfully decoded
// members before their semantic/consumer checks. A refusal must be returned by
// the instrumented operation. Without an attached ledger this is a no-op.
// The first accounting error is sticky, even if a caller discards its error.
func Charge(ctx context.Context, kind Kind, count uint64) error {
	if ctx == nil {
		return nil
	}
	ledger, _ := ctx.Value(contextKey{}).(*Ledger)
	if ledger == nil {
		return nil
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.err != nil {
		return ledger.err
	}
	if ledger.closed {
		ledger.err = ErrClosed
		return ledger.err
	}
	var value *uint64
	var limit uint64
	switch kind {
	case ControlFileRead:
		value, limit = &ledger.counts.ControlFileReads, ledger.limits.ControlFileReads
	case StoreReadAttempt:
		value, limit = &ledger.counts.StoreReadAttempts, ledger.limits.StoreReadAttempts
	case MemberVisit:
		value, limit = &ledger.counts.MemberVisits, ledger.limits.MemberVisits
	case StoreWriteAttempt:
		value, limit = &ledger.counts.StoreWriteAttempts, ledger.limits.StoreWriteAttempts
	default:
		ledger.err = ErrEvent
		return ledger.err
	}
	if count == 0 {
		ledger.err = ErrEvent
	} else if count > limit-*value {
		*value = limit + 1
		ledger.err = ErrLimit
	} else {
		*value += count
	}
	return ledger.err
}

// Finish closes the scope and returns a copy of its counters, including a
// refusal sentinel if present. Errors prevent using the counters as a complete
// successful observation. Late charges refuse at their instrumentation point
// and remain visible to subsequent Finish calls; callers must not share a
// finished scope. A decoded-member charge occurs after that record's read.
func (ledger *Ledger) Finish() (Counts, error) {
	if ledger == nil {
		return Counts{}, ErrScope
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.closed = true
	return ledger.counts, ledger.err
}
