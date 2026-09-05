package dispatchadmission

import (
	"context"
	"math"
	"math/bits"
	"sync"
)

// OwnerLimits bounds enrolled Go work, not processes or durable jobs. A loop
// occupies at most one active turn or one parked waiter. HTTP never parks.
// These construction bounds are not a frozen execution-flow inventory.
type OwnerLimits struct {
	Owners   int
	Requests int
}

// Owners fences complete application work units before dispatch is paused.
// Enter before claims, locks and leases; End only after child joins, callbacks,
// durable settlement and reports. Nested work stays inside its existing turn.
// A paused owner may still hold business resources until it finishes, so the
// caller's drain deadline must fail closed rather than claiming it was joined.
// This does not attest authority readiness, native inactivity or custody.
type Owners struct {
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelCauseFunc
	limits             OwnerLimits
	active             uint64
	requests           uint64
	generations        [64]uint64
	requestGenerations [64]uint64
	waiters            int
	paused             bool
	requestsFenced     bool
	pausedReady        bool
	requestsReady      bool
	changed            chan struct{}
	err                error
}

// OwnerTurn is a bounded active slot, not a dispatch permission. End exactly
// once. Slot generations prevent a copied/stale turn from ending another turn.
type OwnerTurn struct {
	owners     *Owners
	generation uint64
	slot       uint8
	request    bool
}

func NewOwners(ctx context.Context, limits OwnerLimits) (*Owners, error) {
	if ctx == nil || ctx.Err() != nil || limits.Owners < 1 || limits.Owners > 64 ||
		limits.Requests < 1 || limits.Requests > 64 {
		return nil, ErrConfig
	}
	owners := &Owners{limits: limits, changed: make(chan struct{})}
	owners.ctx, owners.cancel = context.WithCancelCause(ctx)
	return owners, nil
}

func (owners *Owners) Context() context.Context {
	if owners == nil {
		return context.Background()
	}
	return owners.ctx
}

// Err reports a latched barrier failure, not ordinary parent cancellation.
func (owners *Owners) Err() error {
	if owners == nil {
		return nil
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	return owners.err
}

func (owners *Owners) failLocked(err error) error {
	if owners.err == nil {
		owners.err = err
		owners.cancel(err)
		owners.notifyLocked()
	}
	return owners.err
}

func (owners *Owners) notifyLocked() {
	close(owners.changed)
	owners.changed = make(chan struct{})
}

// Enter parks only an unadmitted loop turn, without holding a state lock or
// allocating a token. Canceled parked calls consume no slot and are nonsticky.
// The nil form preserves ordinary execution without allocation or context work.
func (owners *Owners) Enter(ctx context.Context) (OwnerTurn, error) {
	if owners == nil {
		return OwnerTurn{}, nil
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	if ctx == nil {
		return OwnerTurn{}, owners.failLocked(ErrCanceled)
	}
	parked := false
	defer func() {
		if parked {
			owners.waiters--
		}
	}()
	for {
		if owners.err != nil {
			return OwnerTurn{}, owners.err
		}
		if err := ctx.Err(); err != nil {
			return OwnerTurn{}, err
		}
		if owners.ctx.Err() != nil {
			return OwnerTurn{}, ErrCanceled
		}
		if !owners.paused {
			return owners.enterLocked(false)
		}
		if !parked {
			if owners.waiters == owners.limits.Owners {
				return OwnerTurn{}, owners.failLocked(ErrLimit)
			}
			owners.waiters++
			parked = true
		}
		changed := owners.changed
		owners.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-owners.ctx.Done():
		case <-changed:
		}
		owners.mu.Lock()
	}
}

// EnterRequest never queues an HTTP goroutine. ErrFenced is an expected closed
// admission refusal and does not poison the barrier. OpenRequests supplies only
// mechanical capacity: the owning transport must separately authorize a probe.
func (owners *Owners) EnterRequest(ctx context.Context) (OwnerTurn, error) {
	if owners == nil {
		return OwnerTurn{}, nil
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	if ctx == nil {
		return OwnerTurn{}, owners.failLocked(ErrCanceled)
	}
	if owners.err != nil {
		return OwnerTurn{}, owners.err
	}
	if err := ctx.Err(); err != nil {
		return OwnerTurn{}, err
	}
	if owners.ctx.Err() != nil {
		return OwnerTurn{}, ErrCanceled
	}
	if owners.requestsFenced {
		return OwnerTurn{}, ErrFenced
	}
	return owners.enterLocked(true)
}

func (owners *Owners) enterLocked(request bool) (OwnerTurn, error) {
	active, generations, limit := &owners.active, &owners.generations, owners.limits.Owners
	if request {
		active, generations, limit = &owners.requests, &owners.requestGenerations, owners.limits.Requests
	}
	// ponytail: one word covers the constructor's at-most-64 active slots;
	// widen the explicit inventory before replacing this with a growing pool.
	available := ^*active & (math.MaxUint64 >> (64 - limit))
	if available == 0 {
		return OwnerTurn{}, owners.failLocked(ErrLimit)
	}
	slot := bits.TrailingZeros64(available)
	if generations[slot] == math.MaxUint64 {
		return OwnerTurn{}, owners.failLocked(ErrLimit)
	}
	generations[slot]++
	*active |= uint64(1) << slot
	return OwnerTurn{owners: owners, generation: generations[slot], slot: uint8(slot), request: request}, nil
}

func (turn OwnerTurn) End() {
	owners := turn.owners
	if owners == nil {
		return
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	active, generations := &owners.active, &owners.generations
	if turn.request {
		active, generations = &owners.requests, &owners.requestGenerations
	}
	mask := uint64(1) << turn.slot
	if turn.generation == 0 || *active&mask == 0 || generations[turn.slot] != turn.generation {
		_ = owners.failLocked(ErrProtocol)
		return
	}
	*active &^= mask
	if *active == 0 && (turn.request && owners.requestsFenced || !turn.request && owners.paused) {
		owners.notifyLocked()
	}
}

// Pause fences new loop turns and waits for complete active turns. Dispatch
// must remain open throughout this drain so multi-command owners can finish.
func (owners *Owners) Pause(ctx context.Context) error { return owners.fence(ctx, false) }

// FenceRequests closes outer HTTP admission and joins already-entered request
// stacks, including authentication, session persistence and response tails.
func (owners *Owners) FenceRequests(ctx context.Context) error { return owners.fence(ctx, true) }

func (owners *Owners) fence(ctx context.Context, request bool) error {
	if owners == nil {
		return nil
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	if ctx == nil {
		return owners.failLocked(ErrCanceled)
	}
	closed, active, ready := &owners.paused, &owners.active, &owners.pausedReady
	if request {
		closed, active, ready = &owners.requestsFenced, &owners.requests, &owners.requestsReady
	}
	if *closed {
		return owners.failLocked(ErrProtocol)
	}
	*closed = true
	owners.notifyLocked()
	for {
		if owners.err != nil {
			return owners.err
		}
		if ctx.Err() != nil || owners.ctx.Err() != nil {
			return owners.failLocked(ErrCanceled)
		}
		if *active == 0 {
			*ready = true
			return nil
		}
		changed := owners.changed
		owners.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-owners.ctx.Done():
		case <-changed:
		}
		owners.mu.Lock()
	}
}

// Resume reopens owner entry only after the parent has advanced dispatch and
// completed next-phase preparation. It neither wakes timers nor changes policy.
func (owners *Owners) Resume() error { return owners.reopen(false) }

func (owners *Owners) OpenRequests() error { return owners.reopen(true) }

func (owners *Owners) reopen(request bool) error {
	if owners == nil {
		return nil
	}
	owners.mu.Lock()
	defer owners.mu.Unlock()
	if owners.err != nil {
		return owners.err
	}
	if owners.ctx.Err() != nil {
		return owners.failLocked(ErrCanceled)
	}
	closed, active, ready := &owners.paused, &owners.active, &owners.pausedReady
	if request {
		closed, active, ready = &owners.requestsFenced, &owners.requests, &owners.requestsReady
	}
	if !*closed || !*ready || *active != 0 {
		return owners.failLocked(ErrBusy)
	}
	*closed = false
	*ready = false
	owners.notifyLocked()
	return nil
}
