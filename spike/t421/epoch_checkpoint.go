package t421

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func returnCheckpointEpochBounds(plan Plan) (epochOneLimits, error) {
	bounds, err := returnStaleEpochBounds(plan)
	deadlines := frozenPhaseDeadlines()
	if err != nil || plan.PhaseDeadlines[7] != deadlines[7] {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	bounds.lifetime += time.Duration(deadlines[7].DeadlineMS) * time.Millisecond
	// Existing phase6/7 twelve pairs, handoff3, preparation3, terminal2,
	// receiver idle/EOF1. No normal Pause/Close is substituted for the hold.
	bounds.controlPairs = 21
	return bounds, nil
}

// StartReturnACheckpoint selects the unchanged phase6/7/8 windows. The older
// return-only and stale-only selectors retain their five/fourteen PC pairs.
func (run *ExecutionEpochOneRun) StartReturnACheckpoint(ctx context.Context) (*ExecutionEpochOneRun, error) {
	return run.startReturnA(ctx, true, true)
}

// Only actual prior F roots, preparation offset and the public native R hit
// cross stdin. This grants no authority to assert death or a restored lease.
type epochCheckpointRecoveryInput struct {
	Prior  epochFinalAuthority                               `json:"prior"`
	Roots  [9]extractionpublication.RecoveryPreparationRoot  `json:"roots"`
	Offset int                                               `json:"offset"`
	Hit    extractionpublication.CheckpointRestartTransition `json:"hit"`
}

func epochSemanticInput(planSHA string, epoch ExecutionEpochConfig, recovery *epochCheckpointRecoveryInput, archive ...*epochArchiveInput) ([]byte, error) {
	if epoch.Epoch < 1 || epoch.Epoch > 5 || (epoch.Epoch == 4) != (recovery != nil) {
		return nil, ErrExecutionEpochOne
	}
	if len(archive) > 1 {
		return nil, ErrExecutionEpochOne
	}
	var archiveInput *epochArchiveInput
	if len(archive) == 1 {
		archiveInput = archive[0]
	}
	if archiveInput != nil && (epoch.Epoch != 5 || !archiveInput.valid()) {
		return nil, ErrExecutionEpochOne
	}
	if epoch.Epoch == 3 {
		if !validGitObjectID(epoch.ReturnSourceCommit, "sha1") {
			return nil, ErrExecutionEpochOne
		}
	} else if epoch.ReturnSourceCommit != "" {
		return nil, ErrExecutionEpochOne
	}
	if epoch.SelectorHandoffCleanup != "" && epoch.SelectorHandoffCleanup != SelectorHandoffCleanupSchema {
		return nil, ErrExecutionEpochOne
	}
	if epoch.LogicalStoreWork != "" && (epoch.LogicalStoreWork != LogicalStoreWorkSchema || epoch.Epoch != 2 ||
		epoch.SelectorHandoffCleanup != SelectorHandoffCleanupSchema) {
		return nil, ErrExecutionEpochOne
	}
	raw, err := json.Marshal(struct {
		Schema                 string                        `json:"schema"`
		Recipe                 string                        `json:"recipe"`
		PlanSHA256             string                        `json:"plan_sha256"`
		ConfigSHA256           string                        `json:"config_sha256"`
		ServerEpoch            uint64                        `json:"server_epoch"`
		Repository             string                        `json:"repository"`
		CheckpointRecovery     *epochCheckpointRecoveryInput `json:"checkpoint_recovery,omitempty"`
		ReturnSourceCommit     string                        `json:"return_source_commit,omitempty"`
		SelectorHandoffCleanup string                        `json:"selector_handoff_cleanup,omitempty"`
		LogicalStoreWork       string                        `json:"logical_store_work,omitempty"`
		Archive                *epochArchiveInput            `json:"archive,omitempty"`
	}{"t422-semantic-launch-v3", "t422-fixed-phase-control-v3", planSHA, epoch.ConfigSHA256, epoch.Epoch, epoch.Repository, recovery, epoch.ReturnSourceCommit, epoch.SelectorHandoffCleanup, epoch.LogicalStoreWork, archiveInput})
	if err != nil || len(raw)+1 > 16<<10 {
		return nil, ErrExecutionEpochOne
	}
	return append(raw, '\n'), nil
}

// CheckpointRestart observes the real held hit, terminally fences epoch three,
// then transfers its retained source to epoch four in the SAME phase eight.
// Its result still requires Health and RecoverCheckpoint; no receipt is made.
func (run *ExecutionEpochOneRun) CheckpointRestart(ctx context.Context) (_ *ExecutionEpochOneRun, retErr error) {
	return run.checkpointRestart(ctx, false, false)
}

// CheckpointRestartPressure retains the fixed phase-eight deadline and reserves
// the three later pressure windows. It does not run ballast or prove pressure.
func (run *ExecutionEpochOneRun) CheckpointRestartPressure(ctx context.Context) (*ExecutionEpochOneRun, error) {
	return run.checkpointRestart(ctx, true, false)
}

// CheckpointRestartBackup binds the endpoint-retirement capability to the
// actual BackupAndStop consumer. It adds no server phase or lifetime time.
func (run *ExecutionEpochOneRun) CheckpointRestartBackup(ctx context.Context) (*ExecutionEpochOneRun, error) {
	return run.checkpointRestart(ctx, true, true)
}

func (run *ExecutionEpochOneRun) checkpointRestart(ctx context.Context, pressure, backup bool) (_ *ExecutionEpochOneRun, retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.control == nil || run.epoch.Epoch != 3 {
		return nil, ErrExecutionEpochOne
	}
	flow := run.flow
	flow.mu.Lock()
	run.mu.Lock()
	valid := !flow.closed && flow.retained == nil && !run.returnStarting && !run.stopping && run.err == nil &&
		run.checkpointAllowed && !run.checkpointUsed && run.staleUsed && run.staleDone != nil && run.inspection != nil &&
		run.phaseTimer != nil && time.Now().Before(run.phaseDeadline)
	select {
	case <-run.staleDone:
	default:
		valid = false
	}
	select {
	case <-run.stop:
		valid = false
	default:
	}
	_, boundsErr := returnCheckpointEpochBounds(flow.plan)
	pressureBounds := epochOneLimits{}
	if pressure {
		pressureBounds, boundsErr = checkpointPressureEpochBounds(flow.plan)
	}
	if !valid || boundsErr != nil || !run.phaseTimer.Stop() {
		run.mu.Unlock()
		flow.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	close(run.phaseDone)
	if !time.Now().Before(run.phaseDeadline) {
		run.err = ErrExecutionEpochOne
		run.stopOnce.Do(func() { close(run.stop) })
		run.mu.Unlock()
		flow.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	deadline := time.Now().Add(time.Duration(flow.plan.PhaseDeadlines[7].DeadlineMS) * time.Millisecond)
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	// Independent of the old server lifetime: finish cancels only that server.
	lifetimeDeadline := deadline
	if pressure {
		lifetimeDeadline = deadline.Add(pressureBounds.lifetime - 4*time.Hour)
	}
	lifetime, cancel := context.WithDeadline(ctx, lifetimeDeadline)
	operation, finishOperation := context.WithDeadline(lifetime, deadline)
	defer finishOperation()
	run.setPhaseDeadlineLocked(deadline) // Before the first handoff/control I/O.
	operationDone := make(chan struct{})
	run.checkpointUsed, run.checkpointCancel, run.checkpointDone = true, cancel, operationDone
	run.returnStarting, run.returnStartCancel, run.returnStartDone = true, cancel, make(chan struct{})
	reader := run.inspection
	run.mu.Unlock()
	flow.mu.Unlock()
	operationJoined := false
	defer func() {
		if !operationJoined {
			close(operationDone)
		}
		if retErr != nil {
			cancel()
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			run.stopOnce.Do(func() { close(run.stop) })
		}
		flow.mu.Lock()
		run.returnStarting, run.returnStartCancel = false, nil
		close(run.returnStartDone)
		flow.mu.Unlock()
	}()
	if reader.beginCheckpoint() != nil || run.advanceReturnPhase(operation, 8) != nil || run.control.OpenRequests(operation) != nil ||
		reader.prepareRecovery(operation, true) != nil || run.control.FenceRequests(operation) != nil || run.control.ReopenOwners(operation) != nil ||
		reader.checkpoint(operation, false) != nil {
		return nil, ErrExecutionEpochOne
	}
	handoff, err := reader.checkpointHandoff()
	if err != nil || run.enterTerminal(operation) != nil || run.control.TerminalQuiesce(operation) != nil || flow.parent.Pause(operation) != nil ||
		flow.controller.Fence() != nil || flow.store.Fence() != nil || run.control.Checkpoint(operation) != nil ||
		flow.store.ArmTerminalEOF(4, 8) != nil || flow.controller.ExpectHardDeath(4) != nil {
		return nil, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	run.mu.Lock()
	if run.stopping || run.err != nil || lifetime.Err() != nil || flow.closed || flow.retained != nil {
		run.mu.Unlock()
		flow.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	run.terminalRequested, run.terminalContext, run.retainParent = true, operation, true
	flow.retained = run
	// finish must never join this method while it waits for finish itself.
	close(operationDone)
	operationJoined = true
	run.mu.Unlock()
	flow.mu.Unlock()
	run.stopOnce.Do(func() { close(run.stop) })
	stopped, err := run.Wait(operation)
	if err != nil {
		return nil, ErrExecutionEpochOne
	}
	processIndex := slices.IndexFunc(stopped.ServerProcesses.Phases, func(value ExecutionServerProcessPhase) bool { return value.Phase == 8 })
	if !stopped.ServerProcesses.Joined || processIndex < 0 || !stopped.ServerProcesses.Phases[processIndex].Observation.Available {
		return nil, ErrExecutionEpochOne
	}
	processPrior := stopped.ServerProcesses.Phases[processIndex].Observation
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if operation.Err() != nil || flow.closed || flow.retained != run || !run.joinedEmpty() ||
		flow.parent.Checkpoint(operation) != nil || flow.store.ReopenAfterTerminalEOF(4, 5, 8) != nil ||
		flow.parent.ReopenAfterHardDeath(operation, 4, 5, 8) != nil {
		return nil, ErrExecutionEpochOne
	}
	prior := reader.staleAuthority
	next := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), done: make(chan struct{}),
		healthLimit: run.healthLimit, coldDeadline: deadline, lifetimeDeadline: lifetimeDeadline, cancelRun: cancel,
		checkpointRecovery: handoff, checkpointPrior: &prior, pressureAllowed: pressure, processPrior: &processPrior, backupAllowed: backup}
	next.setPhaseDeadlineLocked(deadline)
	bounds := epochOneLimits{health: run.healthLimit, outputBytes: 64 << 20, controlPairs: 5}
	if pressure {
		bounds = pressureBounds
	}
	result, err := flow.launchEpoch(lifetime, operation, cancel, next, bounds, 4)
	if result == nil {
		next.stopPhaseDeadline()
	}
	return result, err
}

func (run *ExecutionEpochOneRun) enterTerminal(ctx context.Context) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || run.stopping || run.err != nil || !run.checkpointAllowed || !run.checkpointUsed ||
		run.checkpointDone == nil || run.terminalEntered || run.epoch.Epoch != 3 || !time.Now().Before(run.phaseDeadline) {
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		return ErrExecutionEpochOne
	default:
	}
	run.terminalEntered = true
	return nil
}

func (reader *executionEpochInspection) beginCheckpoint() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || !reader.finalUsed || reader.projection.Phase != "stale_lease" || reader.staleAuthority.Phase != "stale_lease" {
		return errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(reader.plan, "process_restart")
	rows, _, inventoryErr := correctedInspectionInventory(reader.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 8 || rows[7].Phase != "process_restart" || rows[7].ServerEpoch != 4 {
		return errEpochInspection
	}
	reader.projection, reader.bounds = projection, rows[7]
	reader.progressCalls, reader.tailCalls, reader.progressReady = 0, 0, false
	reader.tail, reader.finalUsed = epochTailReadiness{}, false
	return nil
}

func (reader *executionEpochInspection) checkpointHandoff() (*epochCheckpointRecoveryInput, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || !reader.checkpointPrepared || reader.checkpointHit.Point != store.GenerationStaleLeaseTransitionCheckpointHit ||
		reader.staleAuthority.Phase != "stale_lease" || len(reader.staleAuthority.ExtractionRoots) != 9 {
		return nil, errEpochInspection
	}
	value := &epochCheckpointRecoveryInput{Prior: reader.checkpointPreparation.Authority, Offset: reader.checkpointPreparation.Offset, Hit: reader.checkpointHit}
	for i, root := range reader.staleAuthority.ExtractionRoots {
		value.Roots[i] = extractionpublication.RecoveryPreparationRoot{Domain: root.Domain, PlanDigest: root.PlanSHA256, RootDigest: root.RootSHA256}
	}
	return value, nil
}

func (reader *executionEpochInspection) checkpoint(ctx context.Context, recovered bool) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || reader.projection.Phase != "process_restart" || !reader.checkpointPrepared || reader.progressCalls != 0 ||
		!recovered && (reader.run.epoch.Epoch != 3 || reader.checkpointHit.Point != "") ||
		recovered && (reader.run.epoch.Epoch != 4 || reader.checkpointHit.Point == "" || reader.checkpointRecovered.Point != "") {
		return errEpochInspection
	}
	path := "/api/t422/checkpoint/hit"
	if recovered {
		path = "/api/t422/checkpoint/recovered"
	}
	raw, status, report, err := reader.read(ctx, path, 4<<10,
		epochInspectionReport{ControlFileReads: extractionpublication.CheckpointRestartTransitionControlFileReads, StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts})
	var value extractionpublication.CheckpointRestartTransition
	if err != nil || status != http.StatusOK || report.ControlFileReads != extractionpublication.CheckpointRestartTransitionControlFileReads ||
		report.StoreReadAttempts != store.GenerationStaleLeaseTransitionStoreReadAttempts || decodeEpochJSON(raw, &value, false) != nil ||
		reader.validateCheckpoint(value, recovered) != nil {
		return errEpochInspection
	}
	if recovered {
		reader.checkpointRecovered = value
	} else {
		reader.checkpointHit = value
	}
	return nil
}

func (reader *executionEpochInspection) validateCheckpoint(value extractionpublication.CheckpointRestartTransition, recovered bool) error {
	prepared := reader.checkpointPreparation
	chunk, err := store.GenerationChunkIdentity(prepared.RecoverySchedule, int64(prepared.Offset), 0)
	if err != nil || value.TargetGeneration != prepared.TargetGeneration || value.ScheduleGeneration != prepared.RecoveryGeneration ||
		value.PriorScheduleDigest != prepared.PriorSchedule || value.ScheduleDigest != prepared.RecoverySchedule || value.ChunkIdentity != chunk ||
		value.Domain != prepared.Domain || value.Ordinal != prepared.Ordinal || value.PlanDigest != prepared.PlanDigest || value.ResultIdentity != prepared.ResultIdentity ||
		value.Attempt != 0 || !value.CanonicalResultExists || !value.CompletionFileExists || value.CheckpointStateDigest != "" || value.PrivateLeaseTokenDigest != "" {
		return errEpochInspection
	}
	if recovered {
		want := reader.checkpointHit
		want.Point, want.Priority, want.ChunkStatus, want.Leased = store.GenerationStaleLeaseTransitionRecovered, store.GenerationPriorityStale, store.GenerationChunkDone, false
		want.CompletionBitSet, want.RootExists, want.Current = true, true, true
		want.ScheduleStatus = store.GenerationScheduleSettled
		for _, root := range reader.staleAuthority.ExtractionRoots {
			if root.Domain == value.Domain {
				want.RootDigest = root.RootSHA256
			}
		}
		if want.RootDigest == "" || value != want {
			return errEpochInspection
		}
		return nil
	}
	if value.Point != store.GenerationStaleLeaseTransitionCheckpointHit || value.Priority != store.GenerationPriorityNeverRun ||
		value.ScheduleStatus != store.GenerationScheduleActive || value.ChunkStatus != store.GenerationChunkRunning || !value.Leased ||
		value.CompletionBitSet || value.RootExists || value.Current || value.RootDigest != "" {
		return errEpochInspection
	}
	for _, digest := range []string{value.ResultDigest, value.ExpectationDigest, value.PartitionDigest, value.ExtractionPolicyDigest} {
		if !validDigest(digest) {
			return errEpochInspection
		}
	}
	for _, root := range reader.staleAuthority.ExtractionRoots {
		if root.Domain != value.Domain {
			continue
		}
		if value.Ordinal < 0 || value.Ordinal >= len(root.PartitionResults) {
			return errEpochInspection
		}
		part := root.PartitionResults[value.Ordinal]
		if value.ResultDigest != part.ResultDigestSHA256 || value.ExpectationDigest != part.ExpectationSHA256 || value.PartitionDigest != part.PartitionSHA256 ||
			value.CandidateGenerationDigest != root.CandidateGenerationSHA256 || value.SourceGenerationDigest != root.SourceGenerationSHA256 ||
			value.ObservationGenerationDigest != root.ObservationGenerationSHA256 || value.ExtractorVersion == "" || len(value.ExtractorVersion) > 128 || strings.ContainsAny(value.ExtractorVersion, "\x00\r\n") {
			return errEpochInspection
		}
		return nil
	}
	return errEpochInspection
}

// RecoverCheckpoint consumes the held recovered R before convergence polling.
// F must equal all prior authority fields and detailed partition results.
func (run *ExecutionEpochOneRun) RecoverCheckpoint(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.epoch.Epoch != 4 || run.checkpointRecovery == nil || run.checkpointPrior == nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || !run.healthy || run.checkpointUsed || !time.Now().Before(run.phaseDeadline) {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.healthDone:
	default:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	ctx, cancel := context.WithDeadline(ctx, run.phaseDeadline)
	done := make(chan struct{})
	run.checkpointUsed, run.checkpointCancel, run.checkpointDone = true, cancel, done
	run.mu.Unlock()
	defer func() {
		cancel()
		if retErr != nil {
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
			run.stopOnce.Do(func() { close(run.stop) })
		}
		close(done)
	}()
	reader, err := run.newCheckpointInspection(ctx)
	if err != nil || reader.checkpoint(ctx, true) != nil {
		return ErrExecutionEpochOne
	}
	for {
		value, _, err := reader.Progress(ctx)
		if err != nil {
			return ErrExecutionEpochOne
		}
		if value.Progress != nil && value.Progress.State == "current" {
			break
		}
		if epochInspectionDelay(ctx) != nil {
			return ErrExecutionEpochOne
		}
	}
	for {
		value, _, err := reader.Tail(ctx)
		if err != nil {
			return ErrExecutionEpochOne
		}
		if value.Status == "ready" {
			break
		}
		if epochInspectionDelay(ctx) != nil {
			return ErrExecutionEpochOne
		}
	}
	if run.pressureAllowed && reader.pressureCommand(ctx, "park", time.Time{}) != nil {
		return ErrExecutionEpochOne
	}
	if run.control.DrainOwners(ctx) != nil || run.control.OpenRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	if _, _, _, err := reader.Final(ctx); err != nil || run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.warm = true
	run.mu.Unlock()
	return reader.acceptInspectionPhase(ctx)
}

func (run *ExecutionEpochOneRun) newCheckpointInspection(ctx context.Context) (*executionEpochInspection, error) {
	author := run.flow.epochs.author
	author.mu.Lock()
	defer author.mu.Unlock()
	if ctx.Err() != nil || author.active || author.borrowedBy != run || author.next != 3 || author.previous == nil ||
		author.previous.Result != author.expected[2] || author.check(ctx) != nil {
		return nil, errEpochInspection
	}
	input := run.checkpointRecovery
	reader := &executionEpochInspection{run: run, plan: run.flow.plan, authored: author.previous.Result,
		projection: PhaseStateProjection{Phase: "stale_lease"}, finalUsed: true, staleAuthority: *run.checkpointPrior, next: 1}
	if reader.beginCheckpoint() != nil || reader.projection.CatalogSource.SHA256 != run.epoch.CatalogSHA256 {
		return nil, errEpochInspection
	}
	reader.checkpointPrepared, reader.checkpointHit = true, input.Hit
	reader.checkpointPreparation = epochStalePreparation{Authority: input.Prior, Offset: input.Offset,
		TargetGeneration: input.Hit.TargetGeneration, RecoveryGeneration: input.Hit.ScheduleGeneration, PriorSchedule: input.Hit.PriorScheduleDigest,
		RecoverySchedule: input.Hit.ScheduleDigest, Domain: input.Hit.Domain, Ordinal: input.Hit.Ordinal, PlanDigest: input.Hit.PlanDigest, ResultIdentity: input.Hit.ResultIdentity}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.inspection != nil || run.stopping || run.err != nil {
		return nil, errEpochInspection
	}
	run.inspection = reader
	return reader, nil
}

func epochCheckpointClosedPrefix(ctx context.Context, result ExecutionEpochOneResult, terminal bool) bool {
	return epochCheckpointClosedPrefixAt(ctx, result, terminal, 8)
}

func epochCheckpointClosedPrefixAt(ctx context.Context, result ExecutionEpochOneResult, terminal bool, phase uint32) bool {
	opened, ordinal := 4, uint64(7)
	if terminal {
		opened, ordinal = 3, 6
	}
	if ctx == nil || ctx.Err() != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Store.Opened != opened || result.Store.TerminalEOF != opened || result.Store.Store.Phase != phase || result.Store.Complete {
		return false
	}
	for _, id := range []uint32{1, 2, 3, 4, 5, 7, 8, 9} {
		if id == 5 && terminal {
			continue
		}
		found := false
		for _, p := range result.Accounting.Producers {
			if p.Producer == id {
				found = p.Attached && p.Active == 0 && p.Closed
				if id == 1 {
					found = p.Attached && p.Active == 0 && p.Closed != terminal && p.Ordinal == ordinal
				} else if id >= 7 {
					found = found && p.Ordinal == authorCustodyAttempts(int(id-7))
				} else if id == 4 {
					found = found && p.Checkpoint == 8
				}
			}
		}
		if !found {
			return false
		}
	}
	for id := uint32(2); id <= uint32(opened+1); id++ {
		found := false
		for _, p := range result.Store.Store.Producers {
			if p.Producer == id {
				found = p.Attached && p.Calls == 0 && p.Transactions == 0 && p.Closed
				if id == 4 {
					found = p.Attached && p.Calls == 0 && p.Transactions == 0 && !p.Closed && p.TerminalFencedEOF && p.TerminalPhase == 8 && p.Checkpoint == 8
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (reader *executionEpochInspection) checkpointFinalMatches(authority AuthorityPhaseResult) bool {
	prior := reader.staleAuthority
	prior.Phase = "process_restart"
	return reader.staleAuthority.Phase == "stale_lease" && reader.checkpointRecovered.Point == store.GenerationStaleLeaseTransitionRecovered && reflect.DeepEqual(authority, prior)
}
