package t421

import (
	"context"
	"net/http"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func (run *ExecutionEpochOneRun) producer() uint32 {
	if run.epoch.Epoch == 4 {
		return 5
	}
	if run.epoch.Epoch == 2 {
		return 3
	}
	if run.epoch.Epoch == 3 {
		return 4
	}
	return 2
}

// joinedEmpty never substitutes OS cleanup for successful protocol evidence.
// Close may use it to abandon an unsuccessful, but fully joined, retained run.
func (run *ExecutionEpochOneRun) joinedEmpty() bool {
	select {
	case <-run.done:
	default:
		return false
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.result.RootJoined && run.result.SessionEmpty
}

func logicalEpochBounds(plan Plan) (epochOneLimits, error) {
	deadlines := frozenPhaseDeadlines()
	if plan.Schema != PlanV3Schema || len(plan.PhaseDeadlines) != len(deadlines) || plan.PhaseDeadlines[4] != deadlines[4] ||
		plan.SafetyEnvelope.ServerHealthDeadlineMS != frozenSafetyEnvelope().ServerHealthDeadlineMS {
		return epochOneLimits{}, ErrExecutionEpochOne
	}
	return epochOneLimits{lifetime: time.Duration(deadlines[4].DeadlineMS) * time.Millisecond,
		health:      time.Duration(plan.SafetyEnvelope.ServerHealthDeadlineMS) * time.Millisecond,
		outputBytes: 64 << 20, controlPairs: 5}, nil // Drain/Open/Fence/Pause + receiver idle/EOF.
}

// StartLogicalB is the only retained-parent stop. Ordinary Stop still closes
// producer one. Its phase-five deadline includes the old server's handoff.
func (run *ExecutionEpochOneRun) StartLogicalB(ctx context.Context) (_ *ExecutionEpochOneRun, retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil {
		return nil, ErrExecutionEpochOne
	}
	flow := run.flow
	flow.mu.Lock()
	bounds, err := logicalEpochBounds(flow.plan)
	if err != nil || flow.closed || flow.logicalUsed || flow.retained != nil {
		flow.mu.Unlock()
		return nil, ErrExecutionEpochOne
	}
	run.mu.Lock()
	valid := !run.stopping && run.err == nil && run.physicalUsed && run.physicalDone != nil && run.inspection != nil && run.phaseTimer != nil && time.Now().Before(run.phaseDeadline)
	if valid {
		select {
		case <-run.physicalDone:
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
	run.retainParent, flow.logicalUsed, flow.retained = true, true, run
	run.mu.Unlock()
	flow.mu.Unlock()
	deadline := time.Now().Add(bounds.lifetime)
	lifetime, cancel := context.WithDeadline(ctx, deadline)
	defer func() {
		if retErr != nil {
			cancel()
		}
	}()
	if _, err := run.Stop(lifetime); err != nil {
		return nil, ErrExecutionEpochOne
	}
	prior := run.inspection
	if prior.err != nil || !prior.finalUsed || !prior.retentionUsed || prior.physicalAuthority.Phase != "physical_delta_b" {
		return nil, ErrExecutionEpochOne
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.closed || flow.retained != run || lifetime.Err() != nil || run.advanceLogical(lifetime) != nil {
		return nil, ErrExecutionEpochOne
	}
	next := &ExecutionEpochOneRun{flow: flow, stop: make(chan struct{}), done: make(chan struct{}),
		healthLimit: bounds.health, coldDeadline: deadline, lifetimeDeadline: deadline, cancelRun: cancel,
		priorPhysical: &epochLogicalPrior{authored: prior.authored, cold: prior.cold, warmAuthority: prior.warmAuthority, physicalAuthority: prior.physicalAuthority}}
	next.setPhaseDeadlineLocked(deadline)
	result, err := flow.launchEpoch(lifetime, lifetime, cancel, next, bounds, 2)
	if result == nil {
		next.stopPhaseDeadline()
	}
	return result, err
}

// The old child has supplied both terminal EOFs; only the retained parent
// needs a phase-four checkpoint. There is no child resume past its last phase.
func (run *ExecutionEpochOneRun) advanceLogical(ctx context.Context) error {
	flow := run.flow
	if flow.parent.Checkpoint(ctx) != nil || flow.store.Fence() != nil || flow.controller.Advance() != nil || flow.store.Advance() != nil || flow.parent.Resume(5) != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// LogicalB observes native partial activation before X/T: the hit hook owns
// the ordinary worker, so waiting for convergence first would deadlock it.
func (run *ExecutionEpochOneRun) LogicalB(ctx context.Context) (retErr error) {
	if run == nil || ctx == nil || run.flow == nil || run.epoch.Epoch != 2 {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if run.stopping || run.err != nil || !run.healthy || run.logicalUsed || run.priorPhysical == nil || !time.Now().Before(run.phaseDeadline) {
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
	run.logicalUsed, run.logicalCancel, run.logicalDone = true, cancel, done
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
	reader, err := run.newLogicalInspection(ctx)
	if err != nil || reader.activation(ctx, "hit") != nil {
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
	if run.control.DrainOwners(ctx) != nil || run.control.OpenRequests(ctx) != nil || reader.activation(ctx, "recovered") != nil {
		return ErrExecutionEpochOne
	}
	if _, _, _, err := reader.Final(ctx); err != nil || reader.cleanupSelectorHandoff(ctx) != nil || run.control.FenceRequests(ctx) != nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.warm = true // The actual owner drain/request fence above is joined.
	run.mu.Unlock()
	return reader.acceptInspectionPhase(ctx)
}

type epochLogicalPrior struct {
	authored                               AuthoredExecutionRevision
	cold, warmAuthority, physicalAuthority AuthorityPhaseResult
}

func (run *ExecutionEpochOneRun) newLogicalInspection(ctx context.Context) (*executionEpochInspection, error) {
	prior := run.priorPhysical
	if ctx.Err() != nil || prior == nil || prior.physicalAuthority.Phase != "physical_delta_b" {
		return nil, errEpochInspection
	}
	author := run.flow.epochs.author
	author.mu.Lock()
	defer author.mu.Unlock()
	if author.active || author.borrowedBy != run || author.next != 2 || author.previous == nil || author.previous.Result != prior.authored || author.previous.Result != author.expected[1] || author.check(ctx) != nil {
		return nil, errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(run.flow.plan, "logical_delta_b")
	rows, _, inventoryErr := correctedInspectionInventory(run.flow.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 5 || rows[4].ServerEpoch != 2 || projection.CatalogSource.SHA256 != run.epoch.CatalogSHA256 {
		return nil, errEpochInspection
	}
	reader := &executionEpochInspection{run: run, plan: run.flow.plan, authored: prior.authored, projection: projection, bounds: rows[4], next: 1,
		cold: prior.cold, warmAuthority: prior.warmAuthority, physicalAuthority: prior.physicalAuthority}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.inspection != nil || run.stopping || run.err != nil {
		return nil, errEpochInspection
	}
	run.inspection = reader
	return reader, nil
}

type epochActivationObservation struct {
	Schema                 string `json:"schema"`
	Point                  string `json:"point"`
	SelectorDigest         string `json:"selector_digest"`
	CatalogRootDigest      string `json:"catalog_root_digest"`
	SearchGenerationDigest string `json:"search_generation_digest"`
	PlanDigest             string `json:"plan_digest"`
	ScheduleDigest         string `json:"schedule_digest"`
	UnitDigest             string `json:"unit_digest"`
}

func (reader *executionEpochInspection) activation(ctx context.Context, point string) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if reader.err != nil || reader.projection.Phase != "logical_delta_b" || point != "hit" && point != "recovered" ||
		point == "hit" && (reader.activationHit.Schema != "" || reader.progressCalls != 0) ||
		point == "recovered" && (reader.activationHit.Schema == "" || reader.activationRecovered.Schema != "" || reader.tail.Status != "ready") {
		return errEpochInspection
	}
	raw, status, report, err := reader.read(ctx, "/api/t422/logical-activation/"+point, 4<<10,
		epochInspectionReport{StoreReadAttempts: store.ServiceStateV3ActivationTransitionStoreReadAttempts})
	var value epochActivationObservation
	if err != nil || status != http.StatusOK || report.StoreReadAttempts != 5 || decodeEpochJSON(raw, &value, false) != nil ||
		value.Schema != "t422-activation-observation-v1" || value.Point != point {
		return errEpochInspection
	}
	for _, digest := range []string{value.SelectorDigest, value.CatalogRootDigest, value.SearchGenerationDigest, value.PlanDigest, value.ScheduleDigest, value.UnitDigest} {
		if !validDigest(digest) {
			return errEpochInspection
		}
	}
	prior := reader.physicalAuthority
	if value.SearchGenerationDigest != prior.SearchGenerationSHA256 {
		return errEpochInspection
	}
	if point == "hit" {
		// The native wire names the activation plan's target catalog, not
		// the prior selector's catalog. Both R points retain that target;
		// the later full F binds it to the protected logical-B projection.
		if value.CatalogRootDigest == prior.CatalogRootSHA256 {
			return errEpochInspection
		}
		reader.activationHit = value
	} else {
		hit := reader.activationHit
		if value.SelectorDigest != reader.tail.SelectedRuntimeSHA256 || value.SelectorDigest == hit.SelectorDigest || value.CatalogRootDigest != hit.CatalogRootDigest ||
			value.PlanDigest != hit.PlanDigest || value.ScheduleDigest != hit.ScheduleDigest || value.UnitDigest != hit.UnitDigest {
			return errEpochInspection
		}
		reader.activationRecovered = value
	}
	return nil
}
