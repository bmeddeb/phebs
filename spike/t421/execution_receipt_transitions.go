package t421

import (
	"errors"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

var errExecutionReceiptTransition = errors.New("execution receipt transition observations are incomplete")

func composeExecutionReaderTransition(plan Plan, measurement PhaseMeasurement, authority map[string]AuthorityPhaseResult,
	observed epochRetentionObservation, events map[string]uint64,
) (ReaderTransition, error) {
	if !processAccountingPlanSemantics(plan.Schema) || measurement.Phase != "physical_delta_b" ||
		observed.Schema != "t422-current-prior-observation-v1" || observed.PinnedAtUnixNano <= 0 ||
		observed.ReleasedAtUnixNano <= observed.PinnedAtUnixNano || !observed.OldReaderHeldThroughReprobe ||
		observed.Held != (epochRetentionSweep{Attempt: 1, Completeness: "exact"}) ||
		observed.Released != (epochRetentionSweep{Attempt: 2, Completeness: "exact"}) {
		return ReaderTransition{}, errExecutionReceiptTransition
	}
	before, beforeOK := authorityIdentitySHA256(authority["warm_noop"])
	after, afterOK := authorityIdentitySHA256(authority["physical_delta_b"])
	if !beforeOK || !afterOK {
		return ReaderTransition{}, errExecutionReceiptTransition
	}
	heldScanned, releasedScanned := uint64(observed.Held.Scanned), uint64(observed.Released.Scanned)
	value := ReaderTransition{
		Schema: plan.ReceiptContract.TransitionSchema + "/reader-v2", Reader: plan.ReaderProbe.Reader,
		QuerySHA256: observed.QuerySHA256, OldSearchGenerationSHA256: observed.OldSearchGenerationSHA256,
		NewSearchGenerationSHA256: observed.NewSearchGenerationSHA256, OldHeldRecords: observed.OldRecords,
		NewHeldRecords: observed.NewRecords, OldHeldProjectionSHA256: observed.OldProjectionSHA256,
		NewHeldProjectionSHA256: observed.NewProjectionSHA256,
		// The one native lease response contains both observed pin timestamps
		// and successful old/new/reprobe reads around the two recorded sweeps.
		LeaseAcquired: 1, LeaseReleased: 1, OldVisibleWhileHeld: observed.OldReaderHeldThroughReprobe,
		NewCurrentWhileHeld:     observed.NewSearchGenerationSHA256 == authority["physical_delta_b"].SearchGenerationSHA256,
		OldRoleAfterReplacement: "prior", NewRoleAfterReplacement: "current",
		LifecycleAttemptsWhileHeld: observed.Held.Attempt, OldRootProtectedWhileHeld: 1,
		HeldLifecycleScanned: &heldScanned, HeldLifecycleOutcome: "exact_drained",
		LifecycleAttemptsAfterRelease: observed.Released.Attempt - observed.Held.Attempt, OldRootProtectedAfterRelease: 1,
		PostReleaseLifecycleScanned: &releasedScanned, PostReleaseLifecycleOutcome: "exact_drained",
		PostReleaseOldRecords: observed.PostReleaseRecords, PostReleaseOldProjectionSHA256: observed.PostReleaseProjectionSHA256,
		PostReleaseOldOutcome: "retained_prior", OldReaderHeldThroughReprobe: observed.OldReaderHeldThroughReprobe,
		LeaseAcquireEventOrdinal: events["reader:lease-acquire"], NewCurrentEventOrdinal: events["reader:new-current"],
		HeldLifecycleEventOrdinal: events["reader:held-lifecycle"], OldHeldQueryEventOrdinal: events["reader:old-held-query"],
		NewHeldQueryEventOrdinal: events["reader:new-held-query"], LeaseReleaseEventOrdinal: events["reader:lease-release"],
		PostReleaseLifecycleOrdinal: events["reader:post-release-lifecycle"], PostReleaseOldQueryOrdinal: events["reader:post-release-old-query"],
		AuthorityBeforeSHA256: before, AuthorityAfterSHA256: after,
	}
	metrics, err := readerTransitionMetrics(measurement, plan)
	if err != nil {
		return ReaderTransition{}, errExecutionReceiptTransition
	}
	if err := validateReaderTransition(value, measurement.StartEventOrdinal, measurement.FinishEventOrdinal,
		authority, metrics, plan); err != nil {
		return ReaderTransition{}, errExecutionReceiptTransition
	}
	return value, nil
}

func composeExecutionLifecycleTransition(plan Plan, measurement PhaseMeasurement, authority map[string]AuthorityPhaseResult,
	cycle lifecycle.CycleObservation, events map[string]uint64,
) (LifecycleTransition, error) {
	if !processAccountingPlanSemantics(plan.Schema) || measurement.Phase != "lifecycle_collection" || !pressureCycleValid(cycle, true) ||
		cycle.FenceAt.UnixMilli() <= 0 || cycle.Capacity.ObservedAt.UnixMilli() <= 0 {
		return LifecycleTransition{}, errExecutionReceiptTransition
	}
	before, beforeOK := authorityIdentitySHA256(authority["archive_restore"])
	after, afterOK := authorityIdentitySHA256(authority["lifecycle_collection"])
	if !beforeOK || !afterOK {
		return LifecycleTransition{}, errExecutionReceiptTransition
	}
	value := LifecycleTransition{Schema: plan.ReceiptContract.TransitionSchema + "/lifecycle-v1",
		Scanned: cycle.Scanned, Deleted: cycle.Deleted, LogicalBytes: cycle.LogicalBytes, RootBytes: cycle.RootBytes,
		MemberBytes: cycle.MemberBytes, OwnerTurns: cycle.OwnerTurns,
		LifecycleFenceUnixMS: uint64(cycle.FenceAt.UnixMilli()), CapacityObservedUnixMS: uint64(cycle.Capacity.ObservedAt.UnixMilli()),
		LifecycleFenceEventOrdinal: events["lifecycle:fence"], CapacityObservedEventOrdinal: events["lifecycle:capacity"],
		AuthorityBeforeSHA256: before, AuthorityAfterSHA256: after, Owners: make([]LifecycleOwnerResult, len(cycle.Owners)),
	}
	for index, owner := range cycle.Owners {
		if owner.AttemptedAt.UnixMilli() <= 0 {
			return LifecycleTransition{}, errExecutionReceiptTransition
		}
		value.Owners[index] = LifecycleOwnerResult{Name: owner.Name, State: owner.State, Completeness: string(owner.Completeness),
			Scanned: owner.Scanned, Deleted: owner.Deleted, LogicalBytes: owner.LogicalBytes, RootBytes: owner.RootBytes,
			MemberBytes: owner.MemberBytes, Backlog: owner.Backlog, AttemptedAtUnixMS: uint64(owner.AttemptedAt.UnixMilli())}
	}
	var err error
	value.CycleSHA256, err = lifecycleCycleSHA256(value)
	if err != nil || validateLifecycleTransition(value, measurement.StartEventOrdinal, measurement.FinishEventOrdinal,
		authority, measurement.Metrics, plan) != nil {
		return LifecycleTransition{}, errExecutionReceiptTransition
	}
	return value, nil
}
