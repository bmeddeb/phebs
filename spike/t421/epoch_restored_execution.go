package t421

import (
	"context"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

// Actual synchronous workspace boundaries, not complete archive-transition or
// whole-work evidence. Unavailable suffixes retain every completed maximum.
type ExecutionRestoredSamples struct {
	Phases                                               [3]ExecutionWorkspaceBytePhase
	ArchiveComplete, CollectionComplete, ProductComplete bool
	Unavailable, LimitExceeded                           bool
}

func epochRestoredClosedEvidence(result ExecutionEpochOneResult, phase uint32) bool {
	samples := result.RestoredSamples
	if phase != 13 && phase != 14 || !samples.ArchiveComplete || !samples.CollectionComplete || samples.Unavailable || samples.LimitExceeded ||
		len(result.Inspection) != int(phase-11) || phase == 14 && (!samples.ProductComplete || result.ProductFinals != 2 || !validExecutionProductQueries(result.ProductQueries)) {
		return false
	}
	if phase == 14 && (result.ProductFirstFinalOrdinal == 0 || result.ProductFirstFinalOrdinal == ^uint64(0) ||
		result.ProductQueries[0].FirstOrdinal != result.ProductFirstFinalOrdinal+1 || result.Inspection[2].Final == nil ||
		result.ProductQueries[len(result.ProductQueries)-1].LastOrdinal == ^uint64(0) ||
		result.Inspection[2].Final.Ordinal != result.ProductQueries[len(result.ProductQueries)-1].LastOrdinal+1) {
		return false
	}
	for i, name := range []string{"archive_restore", "lifecycle_collection", "product_queries"}[:phase-11] {
		row := result.Inspection[i]
		count := min(uint64(i+1), 2)
		if row.Phase != name || row.ServerEpoch != 5 || row.Final == nil || !row.SelectorAccepted ||
			samples.Phases[i].Attempts != count || samples.Phases[i].Completed != count {
			return false
		}
	}
	return true
}

// CompleteArchive consumes the actual restored command manifest, converges X/T,
// then joins ordinary owners and the final request/report tail. Health alone
// cannot establish this boundary. No synthetic archive events are supplied.
func (run *ExecutionEpochOneRun) CompleteArchive(ctx context.Context) (retErr error) {
	op, cancel, done, err := run.beginRestoredExecution(ctx, 12)
	if err != nil {
		return err
	}
	defer func() { run.finishRestoredExecution(cancel, done, retErr) }()
	reader, err := run.newArchiveInspection(op)
	if err != nil {
		return err
	}
	if _, _, err := reader.Archive(op); err != nil {
		return err
	}
	for {
		progress, _, err := reader.Progress(op)
		if err != nil {
			return err
		}
		if progress.Progress != nil && progress.Progress.State == "current" {
			break
		}
		if err := epochInspectionDelay(op); err != nil {
			return err
		}
	}
	for {
		tail, _, err := reader.Tail(op)
		if err != nil {
			return err
		}
		if tail.Status == "ready" {
			break
		}
		if err := epochInspectionDelay(op); err != nil {
			return err
		}
	}
	if reader.restoredCommand(op, "park") != nil || run.control.DrainOwners(op) != nil || run.control.OpenRequests(op) != nil {
		return ErrExecutionEpochOne
	}
	if _, _, _, err := reader.Final(op); err != nil {
		return err
	}
	if _, err := reader.restoredSample(op, "archive_finish"); err != nil {
		return err
	}
	return run.acceptRestoredExecution(op, 12)
}

// CollectRestored performs one genuine selected fresh-owner cycle in phase13.
// Its fixed phase clock starts before the actual reducer/server handoff. The
// following phase14 queries and final receipt composition remain separate.
func (run *ExecutionEpochOneRun) CollectRestored(ctx context.Context) (retErr error) {
	op, cancel, done, err := run.beginRestoredExecution(ctx, 13)
	if err != nil {
		return err
	}
	defer func() { run.finishRestoredExecution(cancel, done, retErr) }()
	reader := run.inspection
	if err := run.startRestoredPhaseDeadline(op, 13); err != nil {
		return err
	}
	run.mu.Lock()
	deadline := run.phaseDeadline
	run.mu.Unlock()
	op, phaseCancel := context.WithDeadline(op, deadline)
	defer phaseCancel()
	if run.advanceReturnPhase(op, 13) != nil || reader.beginCollection() != nil || run.control.OpenRequests(op) != nil {
		return ErrExecutionEpochOne
	}
	if _, err := reader.restoredSample(op, "start"); err != nil {
		return err
	}
	if reader.restoredCommand(op, "drive-fresh") != nil || reader.freshCycle(op) != nil {
		return ErrExecutionEpochOne
	}
	// DriveFresh already waits for the actual completed cycle. L is one
	// truthful status snapshot, not another owner cycle or invented polling.
	if _, _, err := reader.LifecycleStatus(op); err != nil {
		return err
	}
	progress, _, err := reader.Progress(op)
	if err != nil || progress.Progress == nil || progress.Progress.State != "current" {
		return errEpochInspection
	}
	tail, _, err := reader.Tail(op)
	if err != nil || tail.Status != "ready" {
		return errEpochInspection
	}
	if _, _, _, err := reader.Final(op); err != nil {
		return err
	}
	if _, err := reader.restoredSample(op, "finish"); err != nil {
		return err
	}
	return run.acceptRestoredExecution(op, 13)
}

func (run *ExecutionEpochOneRun) beginRestoredExecution(ctx context.Context, phase uint32) (context.Context, context.CancelFunc, chan struct{}, error) {
	if phase < 12 || phase > 14 || run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.flow.workspace == nil || run.control == nil || run.stop == nil || run.done == nil {
		return nil, nil, nil, ErrExecutionEpochOne
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.stopping || run.err != nil || run.epoch.Epoch != 5 || !run.healthy || run.healthDone == nil || run.archiveInput == nil || run.archivePrior == nil ||
		run.phaseDeadline.IsZero() || !time.Now().Before(run.phaseDeadline) || run.lifetimeDeadline.Before(run.phaseDeadline) {
		return nil, nil, nil, ErrExecutionEpochOne
	}
	select {
	case <-run.healthDone:
	default:
		return nil, nil, nil, ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		return nil, nil, nil, ErrExecutionEpochOne
	default:
	}
	if run.restoredExecutionDone != nil {
		select {
		case <-run.restoredExecutionDone:
		default:
			return nil, nil, nil, ErrExecutionEpochOne
		}
	}
	deadline := run.phaseDeadline
	if phase != 12 {
		if !run.archiveExecutionUsed || phase == 13 && run.collectionExecutionUsed || phase == 14 && (!run.collectionExecutionUsed || run.productExecutionUsed) ||
			!run.warm || run.inspection == nil || run.control.RequestToken() != "" {
			return nil, nil, nil, ErrExecutionEpochOne
		}
		deadline = run.lifetimeDeadline
		if phase == 13 {
			run.collectionExecutionUsed = true
		} else {
			run.productExecutionUsed = true
		}
	} else {
		if run.archiveExecutionUsed || run.inspection != nil || run.control.RequestToken() == "" {
			return nil, nil, nil, ErrExecutionEpochOne
		}
		run.archiveExecutionUsed = true
	}
	op, cancel := context.WithDeadline(ctx, deadline)
	done := make(chan struct{})
	run.restoredExecutionCancel, run.restoredExecutionDone = cancel, done
	return op, cancel, done, nil
}

func (run *ExecutionEpochOneRun) finishRestoredExecution(cancel context.CancelFunc, done chan struct{}, err error) {
	cancel()
	if err != nil {
		run.mu.Lock()
		run.err = ErrExecutionEpochOne
		run.mu.Unlock()
		run.stopOnce.Do(func() { close(run.stop) })
	}
	close(done) // finish may now own PC and native cleanup; never wait for it here.
}

func (run *ExecutionEpochOneRun) startRestoredPhaseDeadline(ctx context.Context, phase uint32) error {
	if ctx == nil || phase != 13 && phase != 14 {
		return ErrExecutionEpochOne
	}
	reader := run.inspection
	reader.mu.Lock()
	valid := reader.err == nil && reader.finalUsed && len(reader.evidence.rows) > 0 && reader.evidence.rows[len(reader.evidence.rows)-1].SelectorAccepted &&
		(phase == 13 && reader.projection.Phase == "archive_restore" && reader.restoredSamples.ArchiveComplete && reader.archiveAuthority.Phase == "archive_restore" ||
			phase == 14 && reader.projection.Phase == "lifecycle_collection" && reader.restoredSamples.CollectionComplete && reader.collectionAuthority.Phase == "lifecycle_collection")
	reader.mu.Unlock()
	if !valid || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	deadlines := frozenPhaseDeadlines()
	if run.stopping || run.err != nil || len(run.flow.plan.PhaseDeadlines) != len(deadlines) || run.flow.plan.PhaseDeadlines[phase-1] != deadlines[phase-1] ||
		run.phaseTimer == nil || !time.Now().Before(run.phaseDeadline) || !run.phaseTimer.Stop() {
		return ErrExecutionEpochOne
	}
	close(run.phaseDone)
	deadline := time.Now().Add(time.Duration(deadlines[phase-1].DeadlineMS) * time.Millisecond)
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	run.setPhaseDeadlineLocked(deadline)
	return nil
}

func (run *ExecutionEpochOneRun) acceptRestoredExecution(ctx context.Context, phase uint32) error {
	reader := run.inspection
	reader.mu.Lock()
	samples := reader.restoredSamples
	valid := reader.err == nil && reader.finalUsed && !samples.Unavailable && !samples.LimitExceeded &&
		(phase == 12 && reader.projection.Phase == "archive_restore" && reader.restoredStep == 1 && samples.Phases[0].Attempts == 1 && samples.Phases[0].Completed == 1 ||
			phase == 13 && reader.projection.Phase == "lifecycle_collection" && reader.restoredStep == 3 && samples.ArchiveComplete && samples.Phases[1].Attempts == 2 && samples.Phases[1].Completed == 2 ||
			phase == 14 && reader.projection.Phase == "product_queries" && reader.productFinalCalls == 2 && reader.productQueriesComplete &&
				validExecutionProductQueries(reader.productQueries) && samples.CollectionComplete && samples.Phases[2].Attempts == 2 && samples.Phases[2].Completed == 2)
	reader.mu.Unlock()
	if !valid {
		return ErrExecutionEpochOne
	}
	if run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	// This records the actual PC drainage state for Stop, not selector success:
	// even a later acceptance failure must not issue a duplicate DrainOwners.
	run.warm = true
	run.mu.Unlock()
	if err := reader.acceptInspectionPhase(ctx); err != nil {
		return err
	}
	reader.mu.Lock()
	switch phase {
	case 13:
		reader.restoredSamples.CollectionComplete = true
	case 12:
		reader.restoredSamples.ArchiveComplete = true
	case 14:
		reader.restoredSamples.ProductComplete = true
	}
	reader.mu.Unlock()
	return nil
}

func (reader *executionEpochInspection) restoredSample(ctx context.Context, point string) (value custodybytes.Sample, retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() {
		reader.fail(retErr)
		if retErr != nil && !reader.restoredSamples.LimitExceeded {
			reader.restoredSamples.Unavailable = true
		}
	}()
	phase := reader.projection.Phase
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || reader.run.epoch.Epoch != 5 || reader.run.flow == nil ||
		reader.run.flow.workspace == nil || reader.run.control == nil || reader.run.control.RequestToken() == "" {
		return value, errEpochInspection
	}
	index := 0
	switch phase {
	case "lifecycle_collection":
		index = 1
	case "product_queries":
		index = 2
	}
	row := &reader.restoredSamples.Phases[index]
	valid := phase == "archive_restore" && point == "archive_finish" && reader.restoredStep == 1 && reader.finalUsed && row.Attempts == 0 ||
		phase == "lifecycle_collection" && point == "start" && reader.restoredStep == 1 && !reader.finalUsed && row.Attempts == 0 ||
		phase == "lifecycle_collection" && point == "finish" && reader.restoredStep == 3 && reader.finalUsed && row.Attempts == 1 && row.Completed == 1 ||
		phase == "product_queries" && point == "start" && reader.restoredStep == 3 && reader.productFinalCalls == 0 && !reader.finalUsed && row.Attempts == 0 ||
		phase == "product_queries" && point == "finish" && reader.productFinalCalls == 2 && reader.productQueriesComplete && reader.finalUsed && row.Attempts == 1 && row.Completed == 1
	if !valid {
		return value, errEpochInspection
	}
	row.Attempts++
	value, err := reader.readWorkspaceSample(ctx, point)
	if err != nil {
		return value, err
	}
	row.Completed++
	row.Maximum.LogicalBytes = max(row.Maximum.LogicalBytes, value.LogicalBytes)
	row.Maximum.AllocatedBytes = max(row.Maximum.AllocatedBytes, value.AllocatedBytes)
	if value.LogicalBytes > reader.plan.WorkEnvelope.MaximumDataLogicalBytes || value.AllocatedBytes > reader.plan.SafetyEnvelope.MaximumDataAllocatedBytes {
		reader.restoredSamples.LimitExceeded = true
		return value, errEpochInspection
	}
	if ctx.Err() != nil {
		return value, errEpochInspection
	}
	return value, nil
}
