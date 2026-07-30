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
