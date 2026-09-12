//go:build darwin

package t421

import (
	"context"
	"errors"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// Private component facts only. Receipt/global event ordinals, whole-phase
// process/work composition and launcher acceptance are deliberately absent.
type executionTeardownResult struct {
	Started, Deadline                                                  time.Time
	Joined, CustodyAbsent, CleanupClosed                               bool
	Detached, ImageRemoved, RootRemoved                                bool
	Bytes                                                              custodyBytePhase
	ByteObservations                                                   uint32 // Point coverage is independent of retained maxima and operation success.
	ByteUnavailable, ByteLimitExceeded                                 bool
	InitialJoined, BeforeDetach, AfterDetach, AfterCleanup, FinalClose SessionCensusEvidence
	Accounting                                                         dispatchadmission.Snapshot
	Store                                                              storeaccounting.WireSnapshot
	AccountingError, StoreError                                        error // Private snapshot diagnostics; populated prefixes survive either error.
}

// The wall clock starts before fencing/shutdown. The first byte observation is
// deliberately later, at the first genuinely joined writer-free boundary.
// It is neither a relabeled phase14 WB nor a claimed pre-shutdown traversal.
// The existing process helper can still spend six seconds on emergency kill/join
// after expiry. That path always fails; it is not an extended acceptance budget.
func (v *executionPressureVolume) finishRestored(ctx context.Context, run *ExecutionEpochOneRun) (result executionTeardownResult, retErr error) {
	if v == nil || ctx == nil || ctx.Err() != nil || run == nil || run.flow == nil || run.inspection == nil ||
		run.flow.epochs == nil || run.flow.epochs.author == nil || run.flow.controller == nil ||
		run.flow.parent == nil || run.flow.store == nil || run.flow.release == nil {
		return result, errPressureVolume
	}
	flow := run.flow
	started := time.Now()
	deadlines := frozenPhaseDeadlines()
	if len(flow.plan.PhaseDeadlines) != len(deadlines) || flow.plan.PhaseDeadlines[14] != deadlines[14] {
		return result, errPressureVolume
	}
	deadline, err := archiveLifetimeDeadline(ctx, flow.plan, flow.authorStarted,
		started.Add(time.Duration(deadlines[14].DeadlineMS)*time.Millisecond))
	if err != nil {
		return result, err
	}
	op, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	v.mu.Lock()
	if v.teardownRun != nil || v.flow != flow || !v.borrowed || !v.ready || v.bytes == nil || v.byteErr != nil || v.check() != nil {
		v.mu.Unlock()
		return result, errPressureVolume
	}
	reader := run.inspection
	reader.mu.Lock()
	accepted := reader.err == nil && reader.projection.Phase == "product_queries" &&
		reader.restoredSamples.ProductComplete && reader.productFinalCalls == 2 &&
		validExecutionProductQueries(reader.productQueries) && len(reader.evidence.rows) == 3 &&
		reader.evidence.rows[2].SelectorAccepted
	reader.mu.Unlock()
	flow.mu.Lock()
	run.mu.Lock()
	valid := accepted && !flow.closed && !run.stopping && run.err == nil && run.epoch.Epoch == 5 &&
		run.warm && run.control != nil && run.control.RequestToken() == "" && run.restoredExecutionDone != nil
	if valid {
		select {
		case <-run.restoredExecutionDone:
		default:
			valid = false
		}
	}
	if !valid || op.Err() != nil {
		run.mu.Unlock()
		flow.mu.Unlock()
		v.mu.Unlock()
		return result, errPressureVolume
	}
	run.teardownContext, run.retainParent = op, true
	flow.retained, v.teardownRun = run, run
	result.Started, result.Deadline = started, deadline
	run.mu.Unlock()
	flow.mu.Unlock()
	v.mu.Unlock()
	releaseFlow := false
	defer func() {
		// Both snapshots return real accepted prefixes alongside any error.
		// Capture before canceling the shared controller lifetime on release.
		retErr = errors.Join(retErr, flow.teardownAccounting(&result))
		v.mu.Lock()
		v.teardownEvidence(&result)
		v.mu.Unlock()
		if releaseFlow {
			flow.release()
		}
	}()
	joined, err := run.Stop(op) // Existing reserved final Pause, receiver EOF and sole native Wait.
	if err != nil || !epochArchiveClosedPrefixWithParent(op, joined, 6, true) {
		return result, errPressureVolume
	}
	result.Joined = true
	// All seven actual SDK receivers are closed, including the earlier
	// explicitly terminal-fenced epoch3. No future-unopened tolerance applies.
	if joined.Store.Opened != 7 || joined.Store.TerminalEOF != 7 ||
		flow.store.Fence() != nil || flow.parent.Checkpoint(op) != nil ||
		flow.store.Advance() != nil || flow.controller.Advance() != nil ||
		flow.store.Fence() != nil || flow.parent.Resume(15) != nil {
		return result, errPressureVolume
	}
	v.mu.Lock()
	// Fresh zero scope is required before the first unguarded mounted walk,
	// not merely the historical SessionEmpty booleans of earlier owners.
	v.teardownInitial, err = v.censusTeardownSessions(op, true)
	if err == nil {
		_, err = v.sampleTeardownWorkspace(op)
	}
	v.mu.Unlock()
	if err != nil {
		return result, err
	}
	// Reuse existing joined input release, exact source-lease reacquisition,
	// nonforced detach and held image/root removal, but retain parent admission.
	if err := v.finishWorkspace(op, run, true); err != nil {
		return result, err
	}
	if op.Err() != nil || flow.parent.Pause(op) != nil || flow.controller.Fence() != nil ||
		flow.parent.Close(op) != nil {
		return result, errPressureVolume
	}
	result.Accounting, err = flow.controller.Snapshot()
	if err != nil || !result.Accounting.Complete {
		return result, errPressureVolume
	}
	result.Store, err = flow.store.Snapshot()
	if err != nil || result.Store.Opened != 7 || result.Store.TerminalEOF != 7 ||
		!result.Store.PrefixesClosed || result.Store.Store.Phase != 15 || flow.store.Close() != nil || op.Err() != nil {
		return result, errPressureVolume
	}
	flow.mu.Lock()
	flow.closed = true
	flow.mu.Unlock()
	releaseFlow = true // Final actual accounting is captured before cancellation.
	if err := v.Close(); err != nil || op.Err() != nil {
		return result, errPressureVolume
	}
	result.CleanupClosed = true
	return result, nil
}

// Called only with volume.mu held, after native server/engine and receiver join.
// This keeps the exact mounted workspace descriptor through both real walks.
func (v *executionPressureVolume) sampleTeardownWorkspace(ctx context.Context) (custodyByteSample, error) {
	if v.teardownRun == nil || v.flow == nil || v.bytes == nil || !v.ready || v.check() != nil {
		return custodyByteSample{}, v.failBytes()
	}
	confirm := func() bool {
		state, err := v.flow.store.Snapshot()
		return err == nil && state.Store.Phase == 15 && state.Opened == 7 && state.TerminalEOF == 7 &&
			state.PrefixesClosed && v.check() == nil && ctx.Err() == nil
	}
	if !confirm() {
		return custodyByteSample{}, v.failBytes()
	}
	value, err := v.bytes.sample(ctx, 15, confirm)
	if err == nil {
		v.teardownByteSamples++
	}
	return v.teardownSampleResult(value, err)
}

// Classify only the real walk result; never convert a ceiling crossing to loss.
func (v *executionPressureVolume) teardownSampleResult(value custodyByteSample, err error) (custodyByteSample, error) {
	if err != nil {
		return value, v.failBytes()
	}
	// A completed over-limit observation is evidence, not unavailable data.
	if v.teardownByteExceeded(value) {
		return value, errPressureVolume
	}
	return value, nil
}

// Called under volume.mu. Operation/descriptor errors do not reclassify actual
// completed byte observations. Incomplete points and failed cleanup remain
// visible independently; this result never asserts whole-phase coverage.
func (v *executionPressureVolume) teardownEvidence(result *executionTeardownResult) {
	snapshot := v.bytes.Snapshot()
	result.Bytes, result.ByteUnavailable = snapshot.Phases[14], snapshot.Unavailable
	result.ByteObservations = v.teardownByteSamples
	result.ByteLimitExceeded = v.teardownByteExceeded(result.Bytes.Maximum)
	result.InitialJoined, result.BeforeDetach = v.teardownInitial, v.teardownBefore
	result.AfterDetach, result.AfterCleanup, result.FinalClose = v.teardownPostDetach, v.teardownAfter, v.teardownClose
	// Absent custody is a separately proved fact, not a completed zero walk.
	result.Detached, result.ImageRemoved, result.RootRemoved = v.teardownDetached, v.teardownImageRemoved, v.removed
	result.CustodyAbsent = result.Detached && result.ImageRemoved && result.RootRemoved
}

func (v *executionPressureVolume) teardownByteExceeded(value custodyByteSample) bool {
	return value.LogicalBytes > v.flow.plan.WorkEnvelope.MaximumDataLogicalBytes ||
		value.AllocatedBytes > v.flow.plan.SafetyEnvelope.MaximumDataAllocatedBytes
}

func (flow *ExecutionEpochOne) teardownAccounting(result *executionTeardownResult) error {
	result.Accounting, result.AccountingError = flow.controller.Snapshot()
	result.Store, result.StoreError = flow.store.Snapshot()
	if result.AccountingError != nil || result.StoreError != nil {
		return errors.Join(errPressureVolume, result.AccountingError, result.StoreError)
	}
	return nil
}

// Retire only the already-joined borrow, not the still-needed root producer.
// Callers hold volume.mu; lock order matches the existing custody handoffs.
func (v *executionPressureVolume) releaseTeardownBorrow(run *ExecutionEpochOneRun) error {
	flow := v.flow
	flow.mu.Lock()
	defer flow.mu.Unlock()
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if flow.closed || flow.retained != run || author.active || author.borrowedBy != run ||
		!epochs.active || epochs.released != 5 || !run.joinedEmpty() {
		return errPressureVolume
	}
	author.borrowedBy, epochs.active, flow.retained = nil, false, nil
	return nil
}

// Called under volume.mu. Every wait and census shares the original operation
// deadline; successive sessions cannot gain thirteen fresh cleanup allowances.
func (v *executionPressureVolume) censusTeardownSessions(ctx context.Context, wait bool) (result SessionCensusEvidence, retErr error) {
	sessions := v.recordedSessionsLocked()
	result.RecordedSessions = uint64(len(sessions))
	for _, session := range sessions {
		if ctx == nil || ctx.Err() != nil {
			result.Errors++
			return result, errPressureVolume
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			result.Errors++
			return result, errPressureVolume
		}
		if short := time.Now().Add(5 * time.Second); short.Before(deadline) {
			deadline = short
		}
		if wait {
			if err := t4013.WaitPrivateProcessSession(session, deadline); err != nil {
				result.Errors++
				return result, errPressureVolume
			}
		}
		members, err := t4013.PrivateProcessSessionMembers(session)
		if err != nil || members < 0 {
			result.Errors++
			return result, errors.Join(errPressureVolume, err)
		}
		result.CompletedCensuses++
		result.ObservedProcesses += uint64(members)
		if ctx.Err() != nil {
			result.Errors++
			return result, errPressureVolume
		}
		if members != 0 {
			return result, errPressureVolume
		}
	}
	return result, nil
}
