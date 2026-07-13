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
