package t421

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

// Native lifecycle reports, not pressure receipts or allocated-data gauges.
// The bounded parent retains only the two completed cycles and four capacities.
type epochPressureObservations struct {
	step             uint8
	normal, recovery lifecycle.CycleObservation
	collect          lifecycle.Pressure80Observation
	refuse           lifecycle.Pressure90Observation
	latched          lifecycle.Pressure75Observation
	resumed          lifecycle.Pressure75RecoveryObservation
	recoveryFence    time.Time
	sampleOrdinal    uint8
	samples          ExecutionPressureSamples
}

func checkpointPressureEpochBounds(plan Plan) (epochOneLimits, error) {
	if _, err := returnCheckpointEpochBounds(plan); err != nil {
		return epochOneLimits{}, err
	}
	deadlines := frozenPhaseDeadlines()
	var lifetime time.Duration
	for i := 7; i <= 10; i++ {
		if plan.PhaseDeadlines[i] != deadlines[i] {
			return epochOneLimits{}, ErrExecutionEpochOne
		}
		lifetime += time.Duration(deadlines[i].DeadlineMS) * time.Millisecond
	}
	// Recovered phase8 five pairs; phase9 handoff3 + two request windows4;
	// phase10 handoff3 + two windows4; phase11 handoff3 + three windows6.
	// The extra phase10/11 windows sample the new phase before ballast changes,
	// then fence requests again for the mutation. Final Pause and receiver
	// idle/EOF remain included in the recovered-phase five-pair reservation.
	return epochOneLimits{lifetime: lifetime, health: 15 * time.Minute, outputBytes: 64 << 20, controlPairs: 5 + 7 + 7 + 9}, nil
}

func pressureStep(phase string, step uint8, operation string) bool {
	switch step {
	case 0:
		return phase == "process_restart" && operation == "park"
	case 1:
		return phase == "pressure_80" && operation == "drive-normal"
	case 2:
		return phase == "pressure_80" && operation == "normal-cycle"
	case 3:
		return phase == "pressure_80" && operation == "pressure-80"
	case 4:
		return phase == "pressure_90" && operation == "pressure-90"
	case 5:
		return phase == "pressure_75" && operation == "pressure-75"
	case 6:
		return phase == "pressure_75" && operation == "drive-recovery"
	case 7:
		return phase == "pressure_75" && operation == "recovery-cycle"
	case 8:
		return phase == "pressure_75" && operation == "recovered-normal"
	}
	return false
}

// All POST work remains in the actual request owner. It has no exact-read
// ordinal and cannot claim the native lifecycle turn or its returned R report.
func (reader *executionEpochInspection) pressureCommand(ctx context.Context, operation string, fence time.Time) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || !reader.run.pressureAllowed ||
		reader.run.epoch.Epoch != 4 || reader.run.control == nil || !pressureStep(reader.projection.Phase, reader.pressure.step, operation) ||
		(operation != "park" && operation != "drive-normal" && operation != "drive-recovery") ||
		(operation == "drive-recovery") == fence.IsZero() || !fence.IsZero() && fence.UnixNano() <= 0 {
		return errEpochInspection
	}
	token := reader.run.control.RequestToken()
	if token == "" {
		return errEpochInspection
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+reader.run.epoch.Listen+"/api/t422/lifecycle/"+operation, nil)
	if err != nil {
		return errEpochInspection
	}
	request.Header.Set("Authorization", "Bearer "+reader.run.epoch.APIKey)
	request.Header.Set(dispatchadmission.ProductionRequestHeader, token)
	if !fence.IsZero() {
		request.Header.Set("X-Phebs-T422-Ballast-Unix-Nano", strconv.FormatInt(fence.UnixNano(), 10))
	}
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errEpochInspection
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	reader.failureStatus, reader.failureBody, reader.failureOrdinal = response.StatusCode, raw, 0
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || ctx.Err() != nil || response.StatusCode != http.StatusOK || string(raw) != "{\"status\":\"complete\"}" ||
		response.Uncompressed || response.Header.Get("Content-Encoding") != "" || len(response.Trailer) != 0 || len(response.Header.Values(epochReadTrailer)) != 0 {
		return errEpochInspection
	}
	reader.pressure.step++
	if operation == "drive-recovery" {
		reader.pressure.recoveryFence = fence
	}
	return nil
}

// Called only after the actual parent completed the preceding F and phase
// handoff. It preserves the epoch-wide ordinal and positive read prefix.
func (reader *executionEpochInspection) beginPressure(phase uint32) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || reader.run == nil || !reader.run.pressureAllowed || reader.run.epoch.Epoch != 4 || !reader.finalUsed || reader.pressureBaseline == nil || phase < 9 || phase > 11 || len(reader.plan.PhaseOrder) < 11 {
		return errEpochInspection
	}
	wantStep := []uint8{1, 4, 5}[phase-9]
	wantPrior := []string{"process_restart", "pressure_80", "pressure_90"}[phase-9]
	if reader.pressure.step != wantStep || reader.projection.Phase != wantPrior {
		return errEpochInspection
	}
	projection, err := expectedStateProjectionForPhase(reader.plan, reader.plan.PhaseOrder[phase-1])
	rows, _, inventoryErr := correctedInspectionInventory(reader.plan.Profile)
	if err != nil || inventoryErr != nil || len(rows) < 11 || rows[phase-1].ServerEpoch != 4 || projection.CatalogSource.SHA256 != reader.run.epoch.CatalogSHA256 {
		return errEpochInspection
	}
	reader.projection, reader.bounds = projection, rows[phase-1]
	reader.progressCalls, reader.tailCalls, reader.progressReady = 0, 0, false
	reader.lifecycleCalls = 0
	reader.tail, reader.finalUsed = epochTailReadiness{}, false
	return nil
}

// The zero-read R responses still consume their genuine exact ordinal and
// trailer. Fences are native mutation timestamps, not supplied capacity claims.
func (reader *executionEpochInspection) pressureRead(ctx context.Context, operation string, fence time.Time) (retErr error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	defer func() { reader.fail(retErr) }()
	if ctx == nil || ctx.Err() != nil || reader.err != nil || reader.run == nil || !reader.run.pressureAllowed || reader.run.epoch.Epoch != 4 ||
		!pressureStep(reader.projection.Phase, reader.pressure.step, operation) {
		return errEpochInspection
	}
	var value any
	switch operation {
	case "normal-cycle":
		value = &reader.pressure.normal
	case "pressure-80":
		value = &reader.pressure.collect
	case "pressure-90":
		value = &reader.pressure.refuse
	case "pressure-75":
		value = &reader.pressure.latched
	case "recovery-cycle":
		value = &reader.pressure.recovery
	case "recovered-normal":
		value = &reader.pressure.resumed
	default:
		return errEpochInspection
	}
	wantsFence := operation == "pressure-80" || operation == "pressure-90" || operation == "pressure-75"
	if wantsFence == fence.IsZero() {
		return errEpochInspection
	}
	raw, status, report, err := reader.readWithFence(ctx, "/api/t422/lifecycle/"+operation, 16<<10, epochInspectionReport{}, fence)
	if err != nil || status != http.StatusOK || report.ControlFileReads != 0 || report.StoreReadAttempts != 0 || report.MemberVisits != 0 ||
		decodeEpochJSON(append(raw, '\n'), value, false) != nil {
		return errEpochInspection
	}
	if !reader.pressure.valid(operation, fence) {
		return errEpochInspection
	}
	reader.pressure.step++
	return nil
}

func (p epochPressureObservations) valid(operation string, fence time.Time) bool {
	switch operation {
	case "normal-cycle":
		return pressureCycleValid(p.normal, false) && p.normal.Capacity.UsedPercent < 75
	case "recovery-cycle":
		return pressureCycleValid(p.recovery, true) && p.recovery.Capacity.UsedPercent < 75 && p.recovery.FenceAt.Equal(p.recoveryFence) && !p.recovery.FenceAt.Before(p.latched.Capacity.ObservedAt)
	case "pressure-80":
		return p.collect.Schema == lifecycle.Pressure80ObservationSchema && p.collect.BallastFenceAt.Equal(fence) &&
			p.collect.PriorCapacityObservedAt.Equal(p.normal.Capacity.ObservedAt) && !fence.Before(p.normal.Capacity.ObservedAt) && p.collect.Capacity.UsedBytes > p.normal.Capacity.UsedBytes && pressureCapacityValid(p.collect.Capacity, lifecycle.PressureCollect, 80, fence)
	case "pressure-90":
		return p.refuse.Schema == lifecycle.Pressure90ObservationSchema && p.refuse.BallastFenceAt.Equal(fence) &&
			p.refuse.PriorCapacityObservedAt.Equal(p.collect.Capacity.ObservedAt) && !fence.Before(p.collect.Capacity.ObservedAt) && p.refuse.Capacity.UsedBytes > p.collect.Capacity.UsedBytes && pressureCapacityValid(p.refuse.Capacity, lifecycle.PressureRefuse, 90, fence)
	case "pressure-75":
		return p.latched.Schema == lifecycle.Pressure75ObservationSchema && p.latched.BallastFenceAt.Equal(fence) &&
			p.latched.PriorCapacityObservedAt.Equal(p.refuse.Capacity.ObservedAt) && !fence.Before(p.refuse.Capacity.ObservedAt) && p.latched.Capacity.UsedBytes < p.refuse.Capacity.UsedBytes && pressureCapacityValid(p.latched.Capacity, lifecycle.PressureRefuse, 75, fence)
	case "recovered-normal":
		return p.resumed.Schema == lifecycle.Pressure75RecoveryObservationSchema && p.resumed.PriorCapacityObservedAt.Equal(p.recovery.Capacity.ObservedAt) &&
			p.resumed.Capacity.UsedPercent < 75 && p.resumed.Capacity.UsedBytes <= p.recovery.Capacity.UsedBytes && pressureCapacityValid(p.resumed.Capacity, lifecycle.PressureNormal, p.resumed.Capacity.UsedPercent, p.recovery.Capacity.ObservedAt)
	}
	return false
}

func pressureCapacityValid(c lifecycle.TransitionCapacityObservation, pressure lifecycle.Pressure, percent int, fence time.Time) bool {
	return c.Completeness == lifecycle.Exact && c.Pressure == pressure && c.TotalBytes == 96<<30 && c.UsedBytes >= 0 && c.AvailableBytes >= 0 &&
		c.UsedBytes <= c.TotalBytes && c.AvailableBytes == c.TotalBytes-c.UsedBytes && c.ProjectedBytes == c.UsedBytes && c.UsedPercent == percent &&
		percent >= 0 && percent <= 100 && uint64(percent) == usedPercentCeiling(uint64(c.UsedBytes), uint64(c.TotalBytes)) && !fence.IsZero() && !c.ObservedAt.Before(fence)
}

func pressureCycleValid(c lifecycle.CycleObservation, allowJobBacklog bool) bool {
	if c.Schema != lifecycle.CycleObservationSchema || c.FenceAt.UnixMilli() <= 0 || c.OwnerTurns == 0 || c.OwnerTurns > uint64(lifecycle.MaxCycleObservationTurns) || len(c.Owners) != 16 ||
		!pressureCapacityValid(c.Capacity, lifecycle.PressureNormal, c.Capacity.UsedPercent, c.FenceAt) {
		return false
	}
	names := correctedLifecycleOwners()
	rows := make([]LifecycleOwnerResult, 0, len(c.Owners))
	prior := c.FenceAt
	for i, owner := range c.Owners {
		completeness := lifecycle.Exact
		if owner.Name == lifecycle.JobOwner {
			completeness = lifecycle.LowerBound
		}
		if owner.Name != names[i] || owner.State != "ok" || owner.Completeness != completeness || owner.Backlog && (owner.Name != lifecycle.JobOwner || !allowJobBacklog) || owner.AttemptedAt.Before(prior) || c.Capacity.ObservedAt.Before(owner.AttemptedAt) ||
			owner.Scanned > uint64(lifecycle.MaxCandidatesPerTick) || owner.Deleted > uint64(lifecycle.MaxDeletesPerTick) {
			return false
		}
		prior = owner.AttemptedAt
		rows = append(rows, LifecycleOwnerResult{Name: owner.Name, State: owner.State, Completeness: string(owner.Completeness), Backlog: owner.Backlog,
			Scanned: owner.Scanned, Deleted: owner.Deleted, LogicalBytes: owner.LogicalBytes, RootBytes: owner.RootBytes, MemberBytes: owner.MemberBytes,
			AttemptedAtUnixMS: uint64(owner.AttemptedAt.UnixMilli())})
	}
	plan := Plan{Schema: PlanV3Schema, WorkEnvelope: WorkEnvelope{LifecycleOwners: names}}
	final, err := validateLifecycleOwners(rows, plan, uint64(c.FenceAt.UnixMilli()), uint64(c.Capacity.ObservedAt.UnixMilli()))
	total := lifecycleAggregate{scanned: c.Scanned, deleted: c.Deleted, logicalBytes: c.LogicalBytes, rootBytes: c.RootBytes, memberBytes: c.MemberBytes}
	return err == nil && validateLifecycleTotals(total, final, c.OwnerTurns, uint64(len(rows)), plan.Schema) == nil
}
