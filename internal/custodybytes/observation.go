package custodybytes

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const MaximumPathBytes = 4096

type borrowedRoot struct {
	file   *os.File
	path   string
	info   os.FileInfo
	volume [2]int32
}

// NewBorrowed borrows the already-owned descriptor; the caller joins samples before closing it.
// This native observer does not grant execution admission.
func NewBorrowed(file *os.File, path string, prior os.FileInfo, fsid [2]int32) *Observer {
	return newObservation(borrowedRoot{file: file, path: path, info: prior, volume: fsid})
}

var ErrUnavailable = errors.New("custody byte observation unavailable")

type Sample struct{ LogicalBytes, AllocatedBytes uint64 }
type Phase struct {
	Maximum   Sample
	Completed bool
}
type Snapshot struct {
	Phases      [15]Phase
	Unavailable bool
}

// Samples are non-atomic, per-linked-path totals, not unique physical allocation
// or complete instantaneous phase high-water. Only completed traversals update
// maxima. The owner is borrowed; this observer neither closes nor replaces it.
type Observer struct {
	owner  borrowedRoot
	turn   chan struct{}
	mu     sync.Mutex
	phase  uint32
	result Snapshot
	err    error
}

func newObservation(owner borrowedRoot) *Observer {
	return &Observer{owner: owner, turn: make(chan struct{}, 1)}
}

func (g *Observer) Snapshot() Snapshot {
	if g == nil {
		return Snapshot{Unavailable: true}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.result
}

func (g *Observer) Fail() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err, g.result.Unavailable = ErrUnavailable, true
	return g.err
}

// Phase labels are monotonic caller metadata, not controller admission proof.
// Skipped phases remain incomplete; later parent integration must bind actual
// required observation points. No default timeout extends the caller's budget.
func (g *Observer) Sample(ctx context.Context, phase uint32) (Sample, error) {
	return g.SampleConfirmed(ctx, phase, nil)
}

// The volume's real controller recheck runs after walking but before committing
// maxima. It holds no observer mutex and cannot supply replacement byte totals.
func (g *Observer) SampleConfirmed(ctx context.Context, phase uint32, confirm func() bool) (Sample, error) {
	return g.SampleGuarded(ctx, phase, nil, confirm)
}

// SampleGuarded runs exactly one synchronous real walk inside the existing
// engine guard; confirmation and maxima commit follow release of its locks.
func (g *Observer) SampleGuarded(ctx context.Context, phase uint32, guard func(context.Context, func(context.Context) error) error, confirm func() bool) (Sample, error) {
	if g == nil {
		return Sample{}, ErrUnavailable
	}
	if ctx == nil {
		return Sample{}, g.Fail()
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || !time.Now().Before(deadline) || ctx.Err() != nil || phase < 1 || phase > 15 {
		return Sample{}, g.Fail()
	}
	select {
	case g.turn <- struct{}{}:
	case <-ctx.Done():
		return Sample{}, g.Fail()
	}
	defer func() { <-g.turn }()
	g.mu.Lock()
	valid := g.err == nil && phase >= g.phase
	if valid {
		g.phase = phase
	}
	g.mu.Unlock()
	if !valid {
		return Sample{}, g.Fail()
	}
	var value Sample
	var err error
	if guard == nil {
		value, err = walkCustodyBytes(ctx, g.owner)
	} else {
		var walkMu sync.Mutex
		closed := false
		invoked := 0
		var completed atomic.Bool
		var walkErr error
		err = guard(ctx, func(context.Context) error {
			walkMu.Lock()
			defer walkMu.Unlock()
			if closed {
				return ErrUnavailable
			}
			invoked++
			if invoked != 1 {
				return ErrUnavailable
			}
			value, walkErr = walkCustodyBytes(ctx, g.owner)
			completed.Store(true)
			return walkErr
		})
		completedBeforeReturn := completed.Load()
		walkMu.Lock()
		closed = true
		err = errors.Join(err, walkErr)
		if invoked != 1 || !completedBeforeReturn {
			err = ErrUnavailable
		}
		walkMu.Unlock()
	}
	if err != nil || ctx.Err() != nil || confirm != nil && !confirm() {
		return Sample{}, g.Fail()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// A concurrent canceled waiter may already have latched unavailable.
	if g.err != nil || ctx.Err() != nil {
		g.err, g.result.Unavailable = ErrUnavailable, true
		return Sample{}, g.err
	}
	row := &g.result.Phases[phase-1]
	row.Completed = true
	row.Maximum.LogicalBytes = max(row.Maximum.LogicalBytes, value.LogicalBytes)
	row.Maximum.AllocatedBytes = max(row.Maximum.AllocatedBytes, value.AllocatedBytes)
	return value, nil
}

// Walk is preparation-only; it does not invent a phase or publish maxima.
func (g *Observer) Walk(ctx context.Context) (Sample, error) {
	if g == nil {
		return Sample{}, ErrUnavailable
	}
	return walkCustodyBytes(ctx, g.owner)
}
