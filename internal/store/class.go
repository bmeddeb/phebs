package store

import (
	"errors"
	"time"
)

// ErrClass is the T3.3 failure taxonomy. Errors are classified once, at the
// boundary that understands them (git exec, index child), and consumed by
// the runner's backoff and the metrics labels.
type ErrClass string

const (
	ClassAuth    ErrClass = "auth"          // clone/fetch credential failures
	ClassOOM     ErrClass = "oom"           // index child killed (memory)
	ClassCorrupt ErrClass = "corrupt-shard" // shard integrity failures
	ClassExtract ErrClass = "extract"       // evidence extraction failures (T12.2)
	ClassGeneric ErrClass = "generic"
)

// Classified carries a taxonomy class through error wrapping.
type Classified struct {
	Class ErrClass
	Err   error
}

func (c *Classified) Error() string { return string(c.Class) + ": " + c.Err.Error() }
func (c *Classified) Unwrap() error { return c.Err }

// WithClass tags err with a taxonomy class.
func WithClass(class ErrClass, err error) error {
	return &Classified{Class: class, Err: err}
}

// Terminal marks an error the runner must not retry: a deterministic refusal
// that re-running the identical work cannot clear. It is a separate typed
// marker rather than a new ErrClass because ErrClass is the five-value backoff
// hint and every extraction error is already ClassExtract — the class says how
// long to wait, this says not to wait at all. It composes with WithClass in
// either order, since both lookups use errors.As.
type Terminal struct {
	Err error
}

func (t *Terminal) Error() string { return "terminal: " + t.Err.Error() }
func (t *Terminal) Unwrap() error { return t.Err }

// WithTerminal marks err terminal, preserving its class and message chain. A
// nil error is not terminal: there is nothing to refuse.
func WithTerminal(err error) error {
	if err == nil {
		return nil
	}
	return &Terminal{Err: err}
}

// IsTerminal reports whether a terminal marker appears anywhere in the chain.
func IsTerminal(err error) bool {
	var terminal *Terminal
	return errors.As(err, &terminal)
}

// Yield marks bounded, durable progress that needs another execution but must
// not consume the runner's failure-attempt budget. It is used when one
// aggregate-bounded job settles some work and durably defers the remainder.
type Yield struct {
	Err error
}

func (y *Yield) Error() string { return "yield: " + y.Err.Error() }
func (y *Yield) Unwrap() error { return y.Err }

// WithYield marks err as scheduler continuation. A nil error has no remaining
// work and therefore cannot yield.
func WithYield(err error) error {
	if err == nil {
		return nil
	}
	return &Yield{Err: err}
}

// IsYield reports whether a scheduler-yield marker appears in the chain.
func IsYield(err error) bool {
	var yield *Yield
	return errors.As(err, &yield)
}

// Deferral marks work whose required authority or shared mutation boundary is
// temporarily unavailable. Unlike a Yield, a deferral returns the same
// lease-fenced job or generation chunk to delayed pending state without
// consuming an attempt; the runner may keep draining ready siblings.
type Deferral struct {
	Err error
}

func (d *Deferral) Error() string { return "deferred: " + d.Err.Error() }
func (d *Deferral) Unwrap() error { return d.Err }

// WithDeferral marks a temporary prerequisite wait. A nil error has no reason
// to defer and therefore remains nil.
func WithDeferral(err error) error {
	if err == nil {
		return nil
	}
	return &Deferral{Err: err}
}

// IsDeferral reports whether an upstream-readiness marker appears in the
// error chain.
func IsDeferral(err error) bool {
	var deferral *Deferral
	return errors.As(err, &deferral)
}

// SuccessorRetry marks a retryable failure after the handler invoked the
// durable successor transaction for the same target. The transaction either
// committed or may have committed before its response failed. Ordinary retries
// merge into any pending row through RequeueJob. On the final allowed attempt
// the runner invokes a provenance-aware transition: it exhausts a recovery row
// still owned exclusively by this handler, or preserves a coalesced freshness
// event while failing only the active claim.
type SuccessorRetry struct {
	Err error
}

func (s *SuccessorRetry) Error() string { return s.Err.Error() }
func (s *SuccessorRetry) Unwrap() error { return s.Err }

// WithSuccessorRetry records a crossed or commit-ambiguous durable queue
// boundary without changing the error's class or displayed message.
func WithSuccessorRetry(err error) error {
	if err == nil {
		return nil
	}
	if IsSuccessorRetry(err) {
		return err
	}
	return &SuccessorRetry{Err: err}
}

// IsSuccessorRetry reports whether the handler crossed or may have committed
// its durable recovery-successor boundary.
func IsSuccessorRetry(err error) bool {
	var retry *SuccessorRetry
	return errors.As(err, &retry)
}

// Classify extracts the class from anywhere in the chain; unclassified
// errors are generic.
func Classify(err error) ErrClass {
	var c *Classified
	if errors.As(err, &c) {
		return c.Class
	}
	return ClassGeneric
}

// DefaultBackoff schedules retries by failure class: credential and memory
// failures won't heal in seconds, so retrying fast just burns the queue.
func DefaultBackoff(err error, attempts int) time.Duration {
	base := 30 * time.Second
	switch Classify(err) {
	case ClassAuth:
		base = 10 * time.Minute
	case ClassOOM:
		base = 5 * time.Minute
	case ClassCorrupt:
		base = time.Second // delete-and-rebuild usually fixes it; retry soon
	case ClassExtract:
		base = 2 * time.Minute // usually deterministic parse issues; fast retries won't heal them
	}
	d := base << attempts
	if max := 1 * time.Hour; d > max {
		d = max
	}
	return d
}
