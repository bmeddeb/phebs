package generationscheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

var ErrTerminalHeartbeat = errors.New("generation terminal heartbeat unavailable or failed")

type terminalHeartbeatKey struct{}

// TerminalClaim is copied from the actual claim. It carries no owner slot,
// writable authority, raw lease token, timestamps, or native completion claim.
type TerminalClaim struct {
	Repository, Stage, Generation, ScheduleDigest, ChunkIdentity string
	ResourceClass                                                store.GenerationResourceClass
	Offset                                                       int64
	Length, Attempt, Priority                                    int
	LeaseTokenDigest                                             string
}

// TerminalHeartbeat is created only for an opted-in handler after an actual
// owned claim. A terminal attempt irreversibly retains that owner and lease;
// quiescence is neither SDK settlement nor proof of owned process death.
type TerminalHeartbeat struct {
	state *terminalHeartbeat // Copies retain the same one-shot authority.
}

type terminalHeartbeat struct {
	cap         TerminalHeartbeat
	mu          sync.Mutex
	ctx         context.Context
	turn        dispatchadmission.OwnerTurn
	claim       TerminalClaim
	stop, done  chan struct{}
	operation   chan struct{}
	used, ended bool
	stopping    bool
	err         error
	firstBeat   error
	beatErr     error // Published by done; only the heartbeat writes it.
}

func TerminalHeartbeatFromContext(ctx context.Context) *TerminalHeartbeat {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(terminalHeartbeatKey{}).(*TerminalHeartbeat)
	return value
}

func newTerminalHeartbeat(ctx context.Context, turn dispatchadmission.OwnerTurn, chunk store.GenerationChunk) (*terminalHeartbeat, error) {
	if ctx == nil || ctx.Err() != nil || turn == (dispatchadmission.OwnerTurn{}) || chunk.Identity == "" || chunk.ScheduleDigest == "" ||
		chunk.Repository == "" || chunk.Stage == "" || chunk.Generation == "" || chunk.LeaseToken == "" || chunk.ClaimedBy == "" ||
		chunk.Status != store.GenerationChunkRunning || chunk.Offset < 0 || chunk.Length < 1 || chunk.Attempt < 0 {
		return nil, ErrTerminalHeartbeat
	}
	terminal := &terminalHeartbeat{ctx: ctx, turn: turn, stop: make(chan struct{}), done: make(chan struct{}), claim: TerminalClaim{
		Repository: chunk.Repository, Stage: chunk.Stage, Generation: chunk.Generation, ScheduleDigest: chunk.ScheduleDigest,
		ChunkIdentity: chunk.Identity, ResourceClass: chunk.ResourceClass, Offset: chunk.Offset, Length: chunk.Length,
		Attempt: chunk.Attempt, Priority: chunk.Priority, LeaseTokenDigest: store.GenerationLeaseTokenDigest(chunk.LeaseToken),
	}}
	terminal.cap.state = terminal
	return terminal, nil
}

func (capability *TerminalHeartbeat) ClaimIdentity() (TerminalClaim, error) {
	if capability == nil || capability.state == nil {
		return TerminalClaim{}, ErrTerminalHeartbeat
	}
	terminal := capability.state
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if terminal.ctx == nil || terminal.ended || terminal.ctx.Err() != nil || terminal.err != nil {
		return TerminalClaim{}, ErrTerminalHeartbeat
	}
	return terminal.claim, nil
}

// Quiesce fences every other owner and all request tails while this handler's
// heartbeat remains live. It then stops only at the heartbeat's safe boundary
// and joins any naturally in-flight call under that call's existing timeout.
// Deadline failure does not cancel that call or authorize releasing the target.
// The caller must already hold the native checkpoint hook and have joined its
// authenticated report tail. This capability does not prove handler parking or
// select an execution phase; those are the owning control's prerequisites.
func (capability *TerminalHeartbeat) Quiesce(ctx context.Context) error {
	if capability == nil || capability.state == nil {
		return ErrTerminalHeartbeat
	}
	return capability.state.quiesce(ctx)
}

func (terminal *terminalHeartbeat) quiesce(ctx context.Context) (retErr error) {
	terminal.mu.Lock()
	if terminal.ctx == nil || terminal.ended || terminal.used {
		if terminal.used {
			terminal.err = ErrTerminalHeartbeat
		}
		terminal.mu.Unlock()
		return ErrTerminalHeartbeat
	}
	terminal.used, terminal.operation = true, make(chan struct{})
	defer func() {
		terminal.mu.Lock()
		if retErr != nil {
			terminal.err = errors.Join(terminal.err, retErr)
		}
		close(terminal.operation)
		terminal.mu.Unlock()
	}()
	if ctx == nil || ctx.Err() != nil || terminal.ctx.Err() != nil || terminal.firstBeat != nil {
		terminal.mu.Unlock()
		return ErrTerminalHeartbeat
	}
	if _, bounded := ctx.Deadline(); !bounded {
		terminal.mu.Unlock()
		return ErrTerminalHeartbeat
	}
	terminal.mu.Unlock()
	// Handler cleanup must unblock a concurrent owner-fence wait without
	// canceling its in-flight heartbeat before that heartbeat joins.
	fenceCtx, cancel := context.WithCancel(ctx)
	callbackDone := make(chan struct{})
	stopCallback := context.AfterFunc(terminal.ctx, func() { cancel(); close(callbackDone) })
	defer func() {
		if !stopCallback() {
			<-callbackDone
		}
		cancel()
	}()
	if err := terminal.turn.FenceTerminal(fenceCtx); err != nil {
		return errors.Join(ErrTerminalHeartbeat, err)
	}
	terminal.mu.Lock()
	if terminal.ended || terminal.ctx.Err() != nil || terminal.firstBeat != nil {
		terminal.mu.Unlock()
		return ErrTerminalHeartbeat
	}
	terminal.stopLocked()
	terminal.mu.Unlock()
	select {
	case <-ctx.Done():
		return errors.Join(ErrTerminalHeartbeat, ctx.Err())
	case <-terminal.done:
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if ctx.Err() != nil || terminal.ctx.Err() != nil || terminal.firstBeat != nil || terminal.beatErr != nil || terminal.ended || terminal.err != nil {
		return errors.Join(ErrTerminalHeartbeat, terminal.firstBeat, terminal.beatErr)
	}
	return nil
}

func (terminal *terminalHeartbeat) stopLocked() {
	if !terminal.stopping {
		terminal.stopping = true
		close(terminal.stop)
	}
}

func (terminal *terminalHeartbeat) finish(cancel context.CancelFunc) (bool, error) {
	terminal.mu.Lock()
	terminal.ended = true
	used, operation := terminal.used, terminal.operation
	if used {
		terminal.stopLocked()
	} else {
		cancel() // Unused opt-in preserves ordinary heartbeat cancellation.
	}
	terminal.mu.Unlock()
	<-terminal.done
	cancel() // A terminal heartbeat has naturally joined before cancellation.
	if operation != nil {
		<-operation
	}
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if used {
		return true, errors.Join(terminal.beatErr, terminal.firstBeat, terminal.err)
	}
	return false, terminal.beatErr
}

func (terminal *terminalHeartbeat) beat(scheduler *Scheduler, cancel context.CancelFunc, chunk store.GenerationChunk) {
	defer close(terminal.done)
	ticker := time.NewTicker(scheduler.HeartbeatEvery)
	defer ticker.Stop()
	lastConfirmed := time.Now()
	if chunk.HeartbeatAt != nil && chunk.HeartbeatAt.Before(lastConfirmed) {
		lastConfirmed = *chunk.HeartbeatAt
	}
	for {
		select {
		case <-terminal.ctx.Done():
			return
		case <-terminal.stop:
			return
		case <-ticker.C:
		}
		terminal.mu.Lock()
		if terminal.stopping || terminal.ctx.Err() != nil {
			terminal.mu.Unlock()
			return
		}
		// Admission of this beat precedes a concurrent stop request. No mutex
		// is held over the native call; Quiesce must join its actual return.
		terminal.mu.Unlock()
		started := time.Now()
		callCtx, callCancel := context.WithTimeout(terminal.ctx, scheduler.storeCallTimeout())
		err := scheduler.Store.HeartbeatGenerationChunk(callCtx, chunk)
		callCancel()
		if err == nil {
			lastConfirmed = started
			continue
		}
		terminal.mu.Lock()
		if terminal.firstBeat == nil {
			terminal.firstBeat = err // Retain one error, never a growing history.
		}
		terminal.mu.Unlock()
		if terminal.ctx.Err() != nil {
			return // Ordinary handler/outer cancellation precedence is retained.
		}
		if errors.Is(err, store.ErrGenerationLeaseLost) || errors.Is(err, store.ErrGenerationStale) || time.Since(lastConfirmed) >= scheduler.StaleAfter {
			terminal.beatErr = err
			cancel()
			return
		}
	}
}
