package t421

import (
	"context"
	"net/http"
	"time"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
)

type epochReturnPrior struct {
	cold, warm, physical, logical AuthorityPhaseResult
}

func returnEpochBounds(plan Plan) (epochOneLimits, error) {
	deadlines := frozenPhaseDeadlines()
	if plan.Schema != PlanV3Schema || len(plan.PhaseDeadlines) != len(deadlines) || plan.PhaseDeadlines[5] != deadlines[5] ||
		plan.SafetyEnvelope.ServerHealthDeadlineMS != frozenSafetyEnvelope().ServerHealthDeadlineMS {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	return epochOneLimits{lifetime: time.Duration(deadlines[5].DeadlineMS) * time.Millisecond,
		health:      time.Duration(plan.SafetyEnvelope.ServerHealthDeadlineMS) * time.Millisecond,
		outputBytes: 64 << 20, controlPairs: 5}, nil // Drain/Open/Fence/Pause + receiver idle/EOF.
}

// StartReturnA retains the same parent and source borrow through the joined
// logical server, actual author nine, and atomic epoch-three transfer. Its one
// phase-six deadline begins before that handoff, not after authoring.
func (run *ExecutionEpochOneRun) StartReturnA(ctx context.Context) (_ *ExecutionEpochOneRun, retErr error) {
	return run.startReturnA(ctx, false, false)
}

func (run *ExecutionEpochOneRun) startReturnA(ctx context.Context, stale, checkpoint bool) (_ *ExecutionEpochOneRun, retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.epoch.Epoch != 2 {
		return nil, ErrExecutionEpochOne
	}
	flow := run.flow
	flow.mu.Lock()
	bounds, err := returnEpochBounds(flow.plan)
	phaseDuration := bounds.lifetime
	if stale {
		bounds, err = returnStaleEpochBounds(flow.plan)
	}
	if checkpoint {
		bounds, err = returnCheckpointEpochBounds(flow.plan)
	}
	if err != nil || flow.closed || flow.returnUsed || flow.retained != nil {
		flow.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	run.mu.Lock()
	valid := !run.stopping && run.err == nil && run.logicalUsed && run.logicalDone != nil && run.inspection != nil && time.Now().Before(run.phaseDeadline)
	if valid {
		select {
		case <-run.logicalDone:
		default:
			valid = false
		}
	}
	select {
	case <-run.stop:
		valid = false
	default:
	}
	if !valid {
		run.mu.Unlock()
		flow.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	started := time.Now()
	deadline, lifetimeDeadline := started.Add(phaseDuration), started.Add(bounds.lifetime)
	lifetime, cancel := context.WithDeadline(ctx, lifetimeDeadline)
	phaseContext, phaseCancel := context.WithDeadline(lifetime, deadline)
	defer phaseCancel()
	run.retainParent, flow.returnUsed, flow.retained = true, true, run
	run.returnStarting, run.returnStartCancel, run.returnStartDone = true, cancel, make(chan struct{})
	run.mu.Unlock()
	flow.mu.Unlock()
	defer func() {
		if retErr != nil {
			cancel()
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
		}
		flow.mu.Lock()
		run.returnStarting = false
		close(run.returnStartDone)
		// A later Stop of the already-joined predecessor must not cancel its
		// successfully transferred successor's lifetime.
		run.returnStartCancel = nil
		flow.mu.Unlock()
	}()
	// Do not call public Stop: it cancels and joins this authoring operation.
	run.stopOnce.Do(func() { close(run.stop) })
	if _, err := run.Wait(phaseContext); err != nil {
		return nil, ErrExecutionEpochOne
	}
	prior := run.inspection
	if prior.err != nil || !prior.finalUsed || prior.logicalAuthority.Phase != "logical_delta_b" {
		return nil, ErrExecutionEpochOne
	}
	if run.advanceReturn(phaseContext) != nil {
		return nil, ErrExecutionEpochOne
	}
	authored, err := flow.epochs.author.authorNext(phaseContext, flow.controller, flow.parent, 9, run)
	if err != nil || !authored.Completed || authored.Response == nil || authored.Revision != "a-return" {
		return nil, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed || flow.retained != run || phaseContext.Err() != nil {
		return nil, ErrExecutionEpochOne
	}
	next := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), done: make(chan struct{}),
		healthLimit: bounds.health, coldDeadline: deadline, lifetimeDeadline: lifetimeDeadline, cancelRun: cancel, staleAllowed: stale, checkpointAllowed: checkpoint,
		priorLogical: &epochReturnPrior{cold: prior.cold, warm: prior.warmAuthority, physical: prior.physicalAuthority, logical: prior.logicalAuthority}}
	next.setPhaseDeadlineLocked(deadline)
	result, err := flow.launchEpoch(lifetime, phaseContext, cancel, next, bounds, 3)
	if result == nil {
		next.stopPhaseDeadline()
	}
	return result, err
}

func (run *ExecutionEpochOneRun) advanceReturn(ctx context.Context) error {
	flow := run.flow
	if flow.parent.Checkpoint(ctx) != nil || flow.store.Fence() != nil || flow.controller.Advance() != nil || flow.store.Advance() != nil || flow.parent.Resume(6) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// ReturnA consumes both held marker reports before waiting for convergence:
// native recovery still owns the scheduler turn until recovered's joined tail.
func (run *ExecutionEpochOneRun) ReturnA(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.epoch.Epoch != 3 {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || !run.healthy || run.returnUsed || run.priorLogical == nil || !time.Now().Before(run.phaseDeadline) {
		run.mu.Unlock()
		return ErrExecutionEpochOne
	}
	select {
	case <-run.healthDone:
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
	ctx, cancel := context.WithDeadline(ctx, run.phaseDeadline)
	done := make(chan struct{})
	run.returnUsed, run.returnCancel, run.returnDone = true, cancel, done
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
	reader, err := run.newReturnInspection(ctx)
	if err != nil || reader.marker(ctx, "hit") != nil || reader.marker(ctx, "recovered") != nil {
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
	if _, _, _, err := reader.Final(ctx); err != nil || reader.cleanupSelectorHandoff(ctx) != nil || run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.warm = true
	run.mu.Unlock()
	return reader.acceptInspectionPhase(ctx)
}

func (run *ExecutionEpochOneRun) newReturnInspection(ctx context.Context) (*executionEpochInspection, error) {
	prior := run.priorLogical
	if ctx.Err() != nil || prior == nil || prior.logical.Phase != "logical_delta_b" {
		return nil, errEpochInspection
	}
	author := run.flow.epochs.author
	author.mu.Lock()
	defer author.mu.Unlock()
	if author.active || author.borrowedBy != run || author.next != 3 || author.previous == nil || author.previous.Result != author.expected[2] || author.check(ctx) != nil {
		return nil, errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(run.flow.plan, "return_a")
	rows, _, inventoryErr := correctedInspectionInventory(run.flow.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 6 || rows[5].ServerEpoch != 3 || projection.CatalogSource.SHA256 != run.epoch.CatalogSHA256 {
		return nil, errEpochInspection
	}
	reader := &executionEpochInspection{run: run, plan: run.flow.plan, authored: author.previous.Result, projection: projection, bounds: rows[5], next: 1,
		cold: prior.cold, warmAuthority: prior.warm, physicalAuthority: prior.physical, logicalAuthority: prior.logical}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.inspection != nil || run.stopping || run.err != nil {
		return nil, errEpochInspection
	}
	run.inspection = reader
	return reader, nil
}

type epochMarkerObservation struct {
	Schema                 string `json:"schema"`
	Point                  string `json:"point"`
	PlanDigest             string `json:"plan_digest"`
	ScheduleDigest         string `json:"schedule_digest"`
	PriorGenerationDigest  string `json:"prior_generation_digest"`
	PriorRootDigest        string `json:"prior_root_digest"`
	TargetGenerationDigest string `json:"target_generation_digest"`
	TargetRootDigest       string `json:"target_root_digest"`
	TargetAuthorityDigest  string `json:"target_authority_digest"`
}

func (reader *executionEpochInspection) marker(ctx context.Context, point string) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || reader.projection.Phase != "return_a" || reader.progressCalls != 0 || point != "hit" && point != "recovered" ||
		point == "hit" && reader.markerHit.Schema != "" || point == "recovered" && (reader.markerHit.Schema == "" || reader.markerRecovered.Schema != "") {
		return errEpochInspection
	}
	raw, status, report, err := reader.read(ctx, "/api/t422/return-a-marker/"+point, 4<<10,
		epochInspectionReport{ControlFileReads: relationshippublication.PublicationTransitionControlFileReadsV3})
	var value epochMarkerObservation
	if err != nil || status != http.StatusOK || report.ControlFileReads != relationshippublication.PublicationTransitionControlFileReadsV3 || decodeEpochJSON(raw, &value, false) != nil ||
		value.Schema != "t422-relationship-marker-observation-v3" || value.Point != point {
		return errEpochInspection
	}
	for _, digest := range []string{value.PlanDigest, value.ScheduleDigest, value.PriorGenerationDigest, value.PriorRootDigest,
		value.TargetGenerationDigest, value.TargetRootDigest, value.TargetAuthorityDigest} {
		if !validDigest(digest) {
			return errEpochInspection
		}
	}
	if value.PriorGenerationDigest != reader.logicalAuthority.RelationshipGenerationSHA256 || value.PriorRootDigest != reader.logicalAuthority.RelationshipRootSHA256 ||
		value.TargetGenerationDigest == value.PriorGenerationDigest || value.TargetRootDigest == value.PriorRootDigest {
		return errEpochInspection
	}
	if point == "hit" {
		reader.markerHit = value
	} else {
		hit := reader.markerHit
		hit.Point = point
		if value != hit {
			return errEpochInspection
		}
		reader.markerRecovered = value
	}
	return nil
}

func epochReturnClosedPrefix(ctx context.Context, result ExecutionEpochOneResult) bool {
	if ctx == nil || ctx.Err() != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Store.Opened != 3 || result.Store.TerminalEOF != 3 {
		return false
	}
	for _, id := range []uint32{1, 2, 3, 4, 7, 8, 9} {
		found := false
		for _, value := range result.Accounting.Producers {
			if value.Producer == id {
				found = value.Attached && value.Closed && value.Active == 0
				if id == 1 {
					found = found && value.Ordinal == 6
				} else if id >= 7 {
					found = found && value.Ordinal == authorCustodyAttempts(int(id-7))
				}
			}
		}
		if !found {
			return false
		}
	}
	for _, id := range []uint32{2, 3, 4} {
		found := false
		for _, value := range result.Store.Store.Producers {
			if value.Producer == id {
				found = value.Attached && value.Closed && value.Calls == 0 && value.Transactions == 0
			}
		}
		if !found {
			return false
		}
	}
	return true
}
