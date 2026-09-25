package t421

import (
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"time"
)

func pressureTransitionSchema(plan Plan) string {
	suffix := "/pressure-v1"
	if pressureContinuityPlanSemantics(plan.Schema) {
		suffix = "/pressure-v2"
	}
	return plan.ReceiptContract.TransitionSchema + suffix
}

// The pressure receipt retains actual mutation snapshots and exact workspace
// points. Phase maxima are not substituted for either endpoint.
func composeExecutionPressureTransitions(plan Plan, freeze ExecutionFreeze, outcomes map[string]string,
	authority map[string]AuthorityPhaseResult, observed epochPressureObservations, serverEpoch uint64,
	events map[string]uint64,
) (map[string]PressureTransition, error) {
	result := make(map[string]PressureTransition, 3)
	prior := SHA256([]byte("t422-pressure-sequence-start-v1"))
	for index, phase := range []string{"pressure_80", "pressure_90", "pressure_75"} {
		if outcomes[phase] != "passed" {
			break
		}
		mutation := observed.ballast[index]
		beforePoint, afterPoint := []int{1, 4, 7}[index], []int{2, 5, 8}[index]
		if !mutation.Attempted || !mutation.Complete || mutation.Mutation.Fence.IsZero() ||
			!observed.samples.Points[beforePoint].Observed || !observed.samples.Points[afterPoint].Observed ||
			!observed.prePressureWorkspaceObserved || len(freeze.Pressure.Targets) != 3 || serverEpoch == 0 {
			return nil, errExecutionReceiptTransition
		}
		identity, ok := authorityIdentitySHA256(authority[phase])
		if !ok {
			return nil, errExecutionReceiptTransition
		}
		target := freeze.Pressure.Targets[index]
		label := []string{"80", "90", "75"}[index]
		prefix := "pressure:" + label + ":"
		capacity := []lifecycle.TransitionCapacityObservation{observed.collect.Capacity, observed.refuse.Capacity, observed.latched.Capacity}[index]
		fence := []time.Time{observed.collect.BallastFenceAt, observed.refuse.BallastFenceAt, observed.latched.BallastFenceAt}[index]
		if !fence.Equal(mutation.Mutation.Fence) || !observed.valid(plan.WorkEnvelope.LifecycleOwners, []string{"pressure-80", "pressure-90", "pressure-75"}[index], fence) || capacity.Completeness != lifecycle.Exact || capacity.UsedPercent < 0 {
			return nil, errExecutionReceiptTransition
		}
		value := PressureTransition{Schema: pressureTransitionSchema(plan), TargetUsedPercent: target.TargetUsedPercent,
			Action: target.Action, ExpectedDisposition: target.ExpectedDisposition, ObservedDisposition: string(capacity.Pressure),
			GateOutcome: "err_pressure_refusal", PriorGateSequenceSHA256: prior, ServerEpoch: serverEpoch,
			VolumeAvailableBytesBefore: mutation.Mutation.Before.Available, VolumeAvailableBytesAfter: mutation.Mutation.After.Available,
			VolumeUsedBytesBefore: mutation.Mutation.Before.Used, VolumeUsedBytesAfter: mutation.Mutation.After.Used, ObservedUsedPercent: uint64(capacity.UsedPercent),
			BallastAllocatedBytesBefore: mutation.Mutation.Before.Allocated, BallastAllocatedBytesAfter: mutation.Mutation.After.Allocated,
			DataAllocatedBytesBefore: observed.samples.Points[beforePoint].Value.AllocatedBytes, DataAllocatedBytesAtTarget: observed.samples.Points[afterPoint].Value.AllocatedBytes,
			BallastMutationEventOrdinal: events[prefix+"ballast"], GateEventOrdinal: events[prefix+"gate"],
			DataVolumeIdentity: freeze.Host.DataVolumeIdentity, BallastVolumeIdentity: freeze.Host.BallastVolumeIdentity,
			AuthorityBeforeSHA256: identity, AuthorityAfterSHA256: identity}
		if index != 1 {
			cycle := observed.normal
			if index == 2 {
				cycle = observed.recovery
			}
			if !pressureCycleValid(cycle, plan.WorkEnvelope.LifecycleOwners, true) || cycle.FenceAt.UnixMilli() <= 0 || cycle.Capacity.ObservedAt.UnixMilli() <= 0 {
				return nil, errExecutionReceiptTransition
			}
			value.LifecycleFenceUnixMS, value.CapacityObservedUnixMS = uint64(cycle.FenceAt.UnixMilli()), uint64(cycle.Capacity.ObservedAt.UnixMilli())
			value.LifecycleFenceEventOrdinal, value.CapacityObservedEventOrdinal = events[prefix+"lifecycle-fence"], events[prefix+"capacity"]
			value.LifecycleScanned, value.LifecycleDeleted, value.LifecycleLogicalBytes = cycle.Scanned, cycle.Deleted, cycle.LogicalBytes
			value.LifecycleRootBytes, value.LifecycleMemberBytes, value.LifecycleOwnerTurns = cycle.RootBytes, cycle.MemberBytes, cycle.OwnerTurns
			value.Owners = make([]LifecycleOwnerResult, len(cycle.Owners))
			for i, owner := range cycle.Owners {
				if owner.AttemptedAt.UnixMilli() <= 0 {
					return nil, errExecutionReceiptTransition
				}
				value.Owners[i] = LifecycleOwnerResult{Name: owner.Name, State: owner.State, Completeness: string(owner.Completeness), Scanned: owner.Scanned, Deleted: owner.Deleted,
					LogicalBytes: owner.LogicalBytes, RootBytes: owner.RootBytes, MemberBytes: owner.MemberBytes, Backlog: owner.Backlog, AttemptedAtUnixMS: uint64(owner.AttemptedAt.UnixMilli())}
			}
			var err error
			value.LifecycleCycleSHA256, err = pressureLifecycleCycleSHA256(value)
			if err != nil {
				return nil, errExecutionReceiptTransition
			}
		}
		if index == 0 {
			if observed.prePressureWorkspace != observed.samples.Points[1].Value {
				return nil, errExecutionReceiptTransition
			}
			value.GateOutcome = "success"
			value.PrePressureDeletedUnits, value.PrePressureAllocatedBytes = observed.normal.Deleted, observed.prePressureWorkspace.AllocatedBytes
		}
		if index == 2 {
			removed := observed.ballast[3]
			if !removed.Attempted || !removed.Complete || !observed.samples.Points[10].Observed ||
				observed.resumed.Capacity.UsedBytes < 0 || observed.resumed.Capacity.AvailableBytes < 0 || observed.resumed.Capacity.UsedPercent < 0 ||
				observed.resumed.Capacity.Completeness != lifecycle.Exact || !observed.valid(plan.WorkEnvelope.LifecycleOwners, "recovered-normal", time.Time{}) {
				return nil, errExecutionReceiptTransition
			}
			value.RecoveryUsedBytes, value.RecoveryAvailableBytes = uint64(observed.resumed.Capacity.UsedBytes), uint64(observed.resumed.Capacity.AvailableBytes)
			value.RecoveryUsedPercent = uint64(observed.resumed.Capacity.UsedPercent)
			value.RecoveryDataAllocatedBytes = observed.samples.Points[10].Value.AllocatedBytes
			value.RecoveryDisposition, value.RecoveryGateOutcome = string(observed.resumed.Capacity.Pressure), "success"
			value.RecoveryBallastAllocatedBytes = removed.Mutation.After.Allocated
			value.RecoveryBallastEventOrdinal, value.RecoveryGateEventOrdinal = events[prefix+"recovery-ballast"], events[prefix+"recovery-gate"]
		}
		var err error
		value.GateSequenceSHA256, err = pressureSequenceSHA256(phase, value)
		if err != nil {
			return nil, errExecutionReceiptTransition
		}
		result[phase] = value
		prior = value.GateSequenceSHA256
	}
	return result, nil
}
