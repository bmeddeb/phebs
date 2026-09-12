package t421

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// StartPhysicalB reserves cold, warm and physical-B observation on epoch one.
// The same frozen phase deadlines and work ceilings apply; no full metrics or
// later epoch is issued by this private parent composition.
func (flow *ExecutionEpochOne) StartPhysicalB(ctx context.Context) (*ExecutionEpochOneRun, error) {
	return flow.start(ctx, epochOnePhysicalB)
}

// PhysicalB pins actual warm A before authoring B, converges the live server,
// and observes its current/prior readers. Success stays fenced in phase four.
func (run *ExecutionEpochOneRun) PhysicalB(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || run.flow == nil || run.flow.epochs == nil || run.control == nil || run.stop == nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || !run.physicalAllowed || run.physicalUsed || !run.warmUsed || run.warmDone == nil || run.inspection == nil ||
		run.physicalLimit <= 0 || run.phaseTimer == nil || !time.Now().Before(run.phaseDeadline) {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.warmDone:
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
	// Stop won only if no phase callback can still own phaseDone. Physical
	// time includes the accounting handoff and pin, not merely B's authoring.
	if ctx.Err() != nil || !run.phaseTimer.Stop() {
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
	deadline := time.Now().Add(run.physicalLimit)
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	run.setPhaseDeadlineLocked(deadline)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	done := make(chan struct{})
	run.physicalUsed, run.physicalCancel, run.physicalDone = true, cancel, done
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
	if run.advancePhysical(ctx) != nil || run.pinPhysical(ctx) != nil {
		return ErrExecutionEpochOne
	}
	authored, err := run.authorPhysical(ctx)
	if err != nil || !authored.Completed || authored.Response == nil || reader.beginPhysical(authored.Response.Result) != nil || run.reopenMeasuredPhysical(ctx) != nil {
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
	if _, _, _, err := reader.Final(ctx); err != nil {
		return ErrExecutionEpochOne
	}
	observation, err := reader.retention(ctx, run.pinStarted, run.pinJoined)
	if err != nil || reader.cleanupSelectorHandoff(ctx) != nil || reader.sampleMidphaseWorkspace(ctx, 1) != nil || run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.physicalResult = observation
	run.mu.Unlock()
	return reader.acceptInspectionPhase(ctx)
}

func (run *ExecutionEpochOneRun) advancePhysical(ctx context.Context) error {
	flow := run.flow
	if run.control.Pause(ctx) != nil || flow.parent.Pause(ctx) != nil || flow.controller.Fence() != nil || flow.store.Fence() != nil ||
		run.control.Checkpoint(ctx) != nil || flow.parent.Checkpoint(ctx) != nil || run.processPhaseAdvance(ctx, 4) != nil ||
		flow.parent.Resume(4) != nil || run.control.Resume(ctx) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

func (run *ExecutionEpochOneRun) pinPhysical(ctx context.Context) error {
	if run.control.OpenRequests(ctx) != nil || run.inspection.sampleMidphaseWorkspace(ctx, 0) != nil {
		return ErrExecutionEpochOne
	}
	started := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+run.epoch.Listen+"/api/t422/retention/pin", nil)
	if err != nil || run.control.RequestToken() == "" {
		return ErrExecutionEpochOne
	}
	request.Header.Set("Authorization", "Bearer "+run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, run.control.RequestToken())
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return ErrExecutionEpochOne
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || string(raw) != `{"status":"complete"}` ||
		response.Uncompressed || response.Header.Get("Content-Encoding") != "" || len(response.Trailer) != 0 ||
		len(response.Header.Values(epochReadTrailer)) != 0 || ctx.Err() != nil || run.control.FenceRequests(ctx) != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.stopping || run.err != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.pinStarted, run.pinJoined, run.physicalPinned = started, time.Now(), true
	return nil
}

// Only this run-bound path may author while the server borrows source custody.
// The operation's cancellation/join remains owned by PhysicalB and Stop.
func (run *ExecutionEpochOneRun) authorPhysical(ctx context.Context) (ExecutionAuthorResult, error) {
	if run == nil || run.control == nil {
		return ExecutionAuthorResult{}, ErrExecutionAuthorCustody
	}
	run.mu.Lock()
	valid := ctx != nil && ctx.Err() == nil && !run.stopping && run.err == nil && run.physicalUsed && run.physicalPinned &&
		run.physicalDone != nil && run.flow != nil && run.flow.epochs != nil && run.control.RequestToken() == ""
	run.mu.Unlock()
	if !valid {
		return ExecutionAuthorResult{}, ErrExecutionAuthorCustody
	}
	flow := run.flow
	if !flow.parent.OnController(flow.controller) {
		return ExecutionAuthorResult{}, ErrExecutionAuthorCustody
	}
	if flow.epochs.author == nil {
		return ExecutionAuthorResult{}, ErrExecutionAuthorCustody
	}
	result, err := flow.epochs.author.authorNext(ctx, flow.controller, flow.parent, 8, run)
	if err != nil {
		_ = flow.controller.Fence()
		result.Accounting, _ = flow.controller.Snapshot()
	}
	return result, err
}

func (reader *executionEpochInspection) beginPhysical(authored AuthoredExecutionRevision) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || !reader.finalUsed || reader.projection.Phase != "warm_noop" || reader.warmAuthority.Phase != "warm_noop" ||
		len(reader.plan.Revisions.Physical) < 2 || authored.Name != "b" || authored.Commit != reader.plan.Revisions.Physical[1].ExpectedCommit || authored.Tree != reader.plan.Revisions.Physical[1].ExpectedTree {
		return errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(reader.plan, "physical_delta_b")
	rows, _, inventoryErr := correctedInspectionInventory(reader.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 4 || rows[3].Phase != "physical_delta_b" || rows[3].ServerEpoch != 1 {
		return errEpochInspection
	}
	reader.authored, reader.projection, reader.bounds = authored, projection, rows[3]
	reader.progressCalls, reader.tailCalls, reader.progressReady = 0, 0, false
	reader.tail, reader.finalUsed = epochTailReadiness{}, false
	return nil
}

// Exact source-free wire emitted by the existing native current/prior reader.
// The two sweeps are real zero-delete current/prior protection observations,
// not the retained V1 deletion contract or manufactured event ordinals.
type epochRetentionSweep struct {
	Attempt      uint64 `json:"attempt"`
	Scanned      int    `json:"scanned"`
	Deleted      int    `json:"deleted"`
	More         bool   `json:"more"`
	Completeness string `json:"completeness"`
	Failed       bool   `json:"failed"`
}

type epochRetentionObservation struct {
	Schema                      string              `json:"schema"`
	OldSearchGenerationSHA256   string              `json:"old_search_generation_sha256"`
	NewSearchGenerationSHA256   string              `json:"new_search_generation_sha256"`
	QuerySHA256                 string              `json:"query_sha256"`
	OldProjectionSHA256         string              `json:"old_projection_sha256"`
	NewProjectionSHA256         string              `json:"new_projection_sha256"`
	PostReleaseProjectionSHA256 string              `json:"post_release_projection_sha256"`
	OldRecords                  uint64              `json:"old_records"`
	NewRecords                  uint64              `json:"new_records"`
	PostReleaseRecords          uint64              `json:"post_release_records"`
	PinnedAtUnixNano            int64               `json:"pinned_at_unix_nano"`
	ReleasedAtUnixNano          int64               `json:"released_at_unix_nano"`
	Held                        epochRetentionSweep `json:"held"`
	Released                    epochRetentionSweep `json:"released"`
	OldReaderHeldThroughReprobe bool                `json:"old_reader_held_through_reprobe"`
}

func (reader *executionEpochInspection) retention(ctx context.Context, pinStarted, pinJoined time.Time) (value epochRetentionObservation, retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || reader.projection.Phase != "physical_delta_b" || reader.physicalAuthority.Phase != "physical_delta_b" || !reader.finalUsed || reader.retentionUsed {
		return value, errEpochInspection
	}
	reader.retentionUsed = true
	bound, err := correctedPhysicalTransitionReadBound(reader.plan.Profile)
	if err != nil {
		return value, errEpochInspection
	}
	maximum := epochInspectionReport{ControlFileReads: bound.ControlFileReads.Maximum, StoreReadAttempts: bound.StoreReadAttempts.Maximum,
		MemberVisits: bound.MemberReads.Maximum, StoreWriteAttempts: bound.StoreWriteAttempts.Maximum}
	raw, status, report, err := reader.read(ctx, "/api/t422/retention/current-prior", 16<<10, maximum)
	if err != nil || status != http.StatusOK || report.ControlFileReads != maximum.ControlFileReads || report.StoreReadAttempts != 0 ||
		report.MemberVisits != maximum.MemberVisits || decodeEpochJSON(append(raw, '\n'), &value, false) != nil ||
		reader.validateRetention(value, pinStarted, pinJoined, time.Now()) != nil {
		return value, errEpochInspection
	}
	return value, nil
}

func (reader *executionEpochInspection) validateRetention(value epochRetentionObservation, pinStarted, pinJoined, finished time.Time) error {
	probe := reader.plan.ReaderProbe
	if pinStarted.IsZero() || pinJoined.Before(pinStarted) || value.Schema != "t422-current-prior-observation-v1" ||
		value.OldSearchGenerationSHA256 != reader.warmAuthority.SearchGenerationSHA256 || value.NewSearchGenerationSHA256 != reader.physicalAuthority.SearchGenerationSHA256 ||
		value.OldSearchGenerationSHA256 == value.NewSearchGenerationSHA256 || value.QuerySHA256 != probe.QuerySHA256 ||
		value.OldProjectionSHA256 != probe.OldProjectionSHA256 || value.NewProjectionSHA256 != probe.NewProjectionSHA256 ||
		value.PostReleaseProjectionSHA256 != probe.OldProjectionSHA256 || value.OldRecords != probe.ExpectedRecords ||
		value.NewRecords != probe.ExpectedRecords || value.PostReleaseRecords != probe.ExpectedRecords || !value.OldReaderHeldThroughReprobe ||
		value.PinnedAtUnixNano < pinStarted.UnixNano() || value.PinnedAtUnixNano > pinJoined.UnixNano() ||
		value.ReleasedAtUnixNano <= pinJoined.UnixNano() || value.ReleasedAtUnixNano > finished.UnixNano() ||
		value.Held != (epochRetentionSweep{Attempt: 1, Completeness: "exact"}) || value.Released != (epochRetentionSweep{Attempt: 2, Completeness: "exact"}) {
		return errEpochInspection
	}
	return nil
}
