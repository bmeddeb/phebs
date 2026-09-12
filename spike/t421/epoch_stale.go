package t421

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

func returnStaleEpochBounds(plan Plan) (epochOneLimits, error) {
	bounds, err := returnEpochBounds(plan)
	deadlines := frozenPhaseDeadlines()
	if err != nil || plan.PhaseDeadlines[6] != deadlines[6] {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	bounds.lifetime += time.Duration(deadlines[6].DeadlineMS) * time.Millisecond
	// Phase6 D/O/F=3; handoff P/C/R=3; prep O/F=2; reopen=1;
	// phase7 D/O/F=3; final Pause=1 and receiver idle/EOF=1.
	bounds.controlPairs = 14
	return bounds, nil
}

// StartReturnAStale explicitly reserves the unchanged phase-six and phase-seven
// windows on the same retained epoch-three server. StartReturnA stays smaller.
func (run *ExecutionEpochOneRun) StartReturnAStale(ctx context.Context) (*ExecutionEpochOneRun, error) {
	return run.startReturnA(ctx, true, false)
}

// StaleLease observes actual preparation, held native hit/recovery and unchanged
// final authority. It does not construct a full measured recovery receipt.
func (run *ExecutionEpochOneRun) StaleLease(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.control == nil || run.epoch.Epoch != 3 {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || !run.staleAllowed || run.staleUsed || !run.returnUsed || run.returnDone == nil ||
		run.inspection == nil || run.phaseTimer == nil || !time.Now().Before(run.phaseDeadline) {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.returnDone:
	default:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.stop:
		run.mu.Unlock()
		return ErrExecutionEpochOne
	default:
	}
	if _, err := returnStaleEpochBounds(run.flow.plan); err != nil || !run.phaseTimer.Stop() {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	close(run.phaseDone)
	if !time.Now().Before(run.phaseDeadline) {
		run.err = ErrExecutionEpochOne
		run.stopOnce.Do(func() { close(run.stop) })
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	deadline := time.Now().Add(time.Duration(run.flow.plan.PhaseDeadlines[6].DeadlineMS) * time.Millisecond)
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	run.setPhaseDeadlineLocked(deadline)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	done := make(chan struct{})
	run.staleUsed, run.staleCancel, run.staleDone = true, cancel, done
	reader := run.inspection
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
	if reader.beginStale() != nil || run.advanceStale(ctx) != nil || run.control.OpenRequests(ctx) != nil ||
		reader.sampleRecoveryWorkspace(ctx, 0) != nil || reader.prepareStale(ctx) != nil || run.control.FenceRequests(ctx) != nil || run.control.ReopenOwners(ctx) != nil {
		return ErrExecutionEpochOne
	}
	// Neither held native observer can wait for X/T or owner drainage. Each R
	// is one blocking call; the native callback contributes its original5s cap.
	if reader.stale(ctx, store.GenerationStaleLeaseTransitionHit) != nil || reader.stale(ctx, store.GenerationStaleLeaseTransitionRecovered) != nil {
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
	if run.control.DrainOwners(ctx) != nil || run.control.OpenRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	if _, _, _, err := reader.Final(ctx); err != nil || reader.sampleRecoveryWorkspace(ctx, 2) != nil || run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	return reader.acceptInspectionPhase(ctx)
}

func (run *ExecutionEpochOneRun) advanceStale(ctx context.Context) error {
	return run.advanceReturnPhase(ctx, 7)
}

func (run *ExecutionEpochOneRun) advanceReturnPhase(ctx context.Context, phase uint32) error {
	flow := run.flow
	if run.control.Pause(ctx) != nil || flow.parent.Pause(ctx) != nil || flow.controller.Fence() != nil || flow.store.Fence() != nil ||
		run.control.Checkpoint(ctx) != nil || flow.parent.Checkpoint(ctx) != nil || run.processPhaseAdvance(ctx, phase) != nil ||
		flow.parent.Resume(phase) != nil || run.control.Resume(ctx) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

func (reader *executionEpochInspection) beginStale() error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || !reader.finalUsed || reader.projection.Phase != "return_a" || reader.returnAuthority.Phase != "return_a" {
		return errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(reader.plan, "stale_lease")
	rows, _, inventoryErr := correctedInspectionInventory(reader.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 7 || rows[6].Phase != "stale_lease" || rows[6].ServerEpoch != 3 {
		return errEpochInspection
	}
	reader.projection, reader.bounds = projection, rows[6]
	reader.progressCalls, reader.tailCalls, reader.progressReady = 0, 0, false
	reader.tail, reader.finalUsed = epochTailReadiness{}, false
	// Shared exact ordinal and already observed totals never reset at handoff.
	return nil
}

// Private actual native preparation fields, not a RecoveryPreparationResult:
// no invented event ordinals, cold opens, completion writes or full-work counts.
type epochStalePreparation struct {
	Schema             string                        `json:"schema"`
	Authority          epochFinalAuthority           `json:"authority"`
	TargetGeneration   string                        `json:"target_generation"`
	PriorSchedule      string                        `json:"prior_schedule"`
	RecoveryGeneration string                        `json:"recovery_generation"`
	RecoverySchedule   string                        `json:"recovery_schedule"`
	Domain             string                        `json:"domain"`
	Ordinal            int                           `json:"ordinal"`
	Offset             int                           `json:"offset"`
	PlanDigest         string                        `json:"plan_digest"`
	ResultIdentity     string                        `json:"result_identity"`
	ControlFileReads   uint64                        `json:"control_file_reads"`
	StoreReadAttempts  uint64                        `json:"store_read_attempts"`
	MemberReads        uint64                        `json:"member_reads"`
	StoreWriteAttempts uint64                        `json:"store_write_attempts"`
	Workspace          *epochRecoveryWorkspaceSample `json:"workspace,omitempty"`
}

func (reader *executionEpochInspection) prepareStale(ctx context.Context) (retErr error) {
	return reader.prepareRecovery(ctx, false)
}

func (reader *executionEpochInspection) prepareRecovery(ctx context.Context, checkpoint bool) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	phase, path, prepared := "stale_lease", "/api/t422/stale-lease/prepare", reader.stalePrepared
	if checkpoint {
		phase, path, prepared = "process_restart", "/api/t422/checkpoint/prepare", reader.checkpointPrepared
	}
	if ctx.Err() != nil || reader.err != nil || reader.projection.Phase != phase || prepared || reader.progressCalls != 0 || reader.run.control.RequestToken() == "" {
		return errEpochInspection
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+reader.run.epoch.Listen+path, nil)
	if err != nil {
		return errEpochInspection
	}
	request.Header.Set("Authorization", "Bearer "+reader.run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, reader.run.control.RequestToken())
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	reader.failureStatus, reader.failureBody = response.StatusCode, raw
	reader.failureOrdinal = 0 // Preparation POST consumes no exact-read ordinal.
	closeErr := response.Body.Close()
	var value epochStalePreparation
	if readErr != nil || closeErr != nil || len(raw) > 16<<10 || response.StatusCode != http.StatusOK || response.Uncompressed ||
		response.Header.Get("Content-Encoding") != "" || len(response.Trailer) != 0 || len(response.Header.Values(epochReadTrailer)) != 0 ||
		decodeEpochJSON(raw, &value, false) != nil || ctx.Err() != nil {
		return errEpochInspection
	}
	// Retain actual bounded response counters even if its target later refuses.
	if checkpoint {
		reader.checkpointPreparation = value
	} else {
		reader.stalePreparation = value
	}
	if err := reader.recordRecoveryPreparationWorkspace(value.Workspace, checkpoint); err != nil {
		return err
	}
	if reader.validateRecoveryPreparation(value, checkpoint) != nil {
		return errEpochInspection
	}
	if checkpoint {
		reader.checkpointPrepared = true
	} else {
		reader.stalePrepared = true
	}
	return nil
}

func (reader *executionEpochInspection) validateStalePreparation(value epochStalePreparation) error {
	return reader.validateRecoveryPreparation(value, false)
}

func (reader *executionEpochInspection) validateRecoveryPreparation(value epochStalePreparation, checkpoint bool) error {
	prior := reader.returnAuthority
	phase, priorPhase, schema, domain, ordinal := "stale_lease", "return_a", "t422-stale-preparation-observation-v1", "grpc-caller", 6
	if checkpoint {
		prior = reader.staleAuthority
		phase, priorPhase, schema, domain, ordinal = "process_restart", "stale_lease", "t422-checkpoint-preparation-observation-v1", "proto-contract", 2
	}
	if value.Schema != schema || prior.Phase != priorPhase || value.Domain != domain || value.Ordinal != ordinal || value.Offset < 0 {
		return errEpochInspection
	}
	var state epochFinalAuthority
	raw, err := json.Marshal(prior.AuthorityState)
	if err != nil || json.Unmarshal(raw, &state) != nil || state != value.Authority {
		return errEpochInspection
	}
	var selected *ExtractionRootResult
	var offset uint64
	for i := range prior.ExtractionRoots {
		root := &prior.ExtractionRoots[i]
		if root.Domain == value.Domain {
			selected = root
			break
		}
		offset += root.ApplicablePartitions
	}
	if selected == nil || len(selected.PartitionResults) <= value.Ordinal || offset+uint64(value.Ordinal) != uint64(value.Offset) ||
		value.TargetGeneration != selected.GenerationSHA256 || value.PlanDigest != selected.PlanSHA256 ||
		value.ResultIdentity != selected.PartitionResults[value.Ordinal].ResultIdentitySHA256 || !validDigest(value.PriorSchedule) {
		return errEpochInspection
	}
	pointIndex := slices.IndexFunc(reader.plan.FailurePoints, func(point FailurePoint) bool { return point.Phase == phase })
	if pointIndex < 0 {
		return errEpochInspection
	}
	point, partition := reader.plan.FailurePoints[pointIndex], selected.PartitionResults[value.Ordinal]
	if point.TargetDomain != value.Domain || point.TargetOrdinal != uint64(value.Ordinal) || partition.Kind != point.TargetKind ||
		partition.CallerPrefix != point.TargetCallerPrefix || partition.SourceStart != point.TargetSourceStart || partition.SourceEnd != point.TargetSourceEnd ||
		partition.MemberOrdinal != point.TargetMemberOrdinal || partition.Ordinal != uint64(value.Ordinal) {
		return errEpochInspection
	}
	rows := correctedRecoveryPreparations()
	index := slices.IndexFunc(rows, func(row RecoveryPreparation) bool { return row.Phase == phase })
	if index < 0 {
		return errEpochInspection
	}
	row := rows[index]
	generation := SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + value.TargetGeneration + "\x00" + value.PriorSchedule))
	schedule, scheduleErr := store.GenerationScheduleDigest(store.GenerationScheduleSpec{Repository: reader.run.epoch.Repository,
		Stage: extractionpublication.ScheduleStage, Generation: generation, ResourceClass: store.GenerationResourceExtraction,
		TotalItems: int64(row.Chunks), ChunkItems: extractionpublication.ScheduleChunkItems, MaxAttempts: extractionpublication.ScheduleMaxAttempts,
		RepositoryTokens: extractionpublication.ScheduleRepositoryTokens})
	files, reads, boundsErr := recoveryPreparationReadBounds(reader.plan, row)
	if scheduleErr != nil || boundsErr != nil || value.RecoveryGeneration != generation || value.RecoverySchedule != schedule || value.RecoverySchedule == value.PriorSchedule ||
		value.ControlFileReads < files.Minimum || value.ControlFileReads > files.Maximum+1 || value.StoreReadAttempts < reads.Minimum || value.StoreReadAttempts > reads.Maximum ||
		value.StoreWriteAttempts < 1 || value.StoreWriteAttempts > recoveryPreparationStoreAttempts || value.MemberReads > candidate.MaxWholeRepositoryStrictOpenMemberVisits() {
		return errEpochInspection
	}
	return nil
}

func (reader *executionEpochInspection) stale(ctx context.Context, point store.GenerationStaleLeaseTransitionPoint) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || reader.projection.Phase != "stale_lease" || !reader.stalePrepared || reader.progressCalls != 0 ||
		point != store.GenerationStaleLeaseTransitionHit && point != store.GenerationStaleLeaseTransitionRecovered ||
		point == store.GenerationStaleLeaseTransitionHit && reader.staleHit.Point != "" ||
		point == store.GenerationStaleLeaseTransitionRecovered && (reader.staleHit.Point == "" || reader.staleRecovered.Point != "") {
		return errEpochInspection
	}
	raw, status, report, err := reader.read(ctx, "/api/t422/stale-lease/"+string(point), 4<<10,
		epochInspectionReport{ControlFileReads: extractionpublication.StaleLeaseTransitionControlFileReads, StoreReadAttempts: store.GenerationStaleLeaseTransitionStoreReadAttempts})
	var value extractionpublication.StaleLeaseTransition
	if err != nil || status != http.StatusOK || report.ControlFileReads != extractionpublication.StaleLeaseTransitionControlFileReads ||
		report.StoreReadAttempts != store.GenerationStaleLeaseTransitionStoreReadAttempts || decodeEpochJSON(raw, &value, false) != nil || value.Point != point {
		return errEpochInspection
	}
	prepared := reader.stalePreparation
	chunk, chunkErr := store.GenerationChunkIdentity(prepared.RecoverySchedule, int64(prepared.Offset), 0)
	if chunkErr != nil || value.TargetGeneration != prepared.TargetGeneration || value.ScheduleGeneration != prepared.RecoveryGeneration ||
		value.PriorScheduleDigest != prepared.PriorSchedule || value.ScheduleDigest != prepared.RecoverySchedule || value.ChunkIdentity != chunk ||
		value.Domain != prepared.Domain || value.Ordinal != prepared.Ordinal || value.PlanDigest != prepared.PlanDigest || value.ResultIdentity != prepared.ResultIdentity {
		return errEpochInspection
	}
	if point == store.GenerationStaleLeaseTransitionHit {
		reader.staleHit = value
	} else {
		hit := reader.staleHit
		hit.Point = point
		if !reflect.DeepEqual(hit, value) {
			return errEpochInspection
		}
		reader.staleRecovered = value
	}
	return nil
}
