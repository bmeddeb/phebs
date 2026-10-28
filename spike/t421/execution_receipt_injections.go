package t421

import (
	"slices"
	"time"
)

// Activation and marker observations are emitted only at their exact native
// hit/recovered control states. The paired accepted responses and four unique
// controller events prove one arm/hit/recovery/clear corridor. Frozen selector
// fields describe the installed injection; native digests identify its target.
//
//nolint:unused // Darwin execution flow only; unused in Linux builds.
func composeExecutionPublicationInjection(plan Plan, measurement PhaseMeasurement,
	authority map[string]AuthorityPhaseResult, observed executionTransitionObservations,
	events map[string]uint64, times map[string]time.Time,
) (InjectionTransition, error) {
	if !processAccountingPlanSemantics(plan.Schema) || measurement.Phase != "logical_delta_b" && measurement.Phase != "return_a" {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	index := slices.IndexFunc(plan.FailurePoints, func(p FailurePoint) bool { return p.Phase == measurement.Phase })
	if index < 0 {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	point := plan.FailurePoints[index]
	priorPhase := "physical_delta_b"
	if measurement.Phase == "return_a" {
		priorPhase = "logical_delta_b"
	}
	before, beforeOK := authorityIdentitySHA256(authority[priorPhase])
	after, afterOK := authorityIdentitySHA256(authority[measurement.Phase])
	if !beforeOK || !afterOK {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	prefix := "injection:" + measurement.Phase + ":"
	started, finished := times[prefix+"arm"], times[prefix+"recovered"]
	if started.IsZero() || !finished.After(started) {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	elapsed := finished.Sub(started)
	elapsedMS := uint64(elapsed / time.Millisecond)
	if elapsed%time.Millisecond != 0 {
		elapsedMS++
	}
	value := InjectionTransition{Schema: plan.ReceiptContract.TransitionSchema + "/injection-v2", FailurePoint: point.Name,
		ArmCount: 1, HitCount: 1, RecoveryCount: 1, ResidueAtHit: 1, SuccessCount: 1, RecoveredCandidates: 1,
		ArmEventOrdinal: events[prefix+"arm"], HitEventOrdinal: events[prefix+"hit"], RecoveryEventOrdinal: events[prefix+"recovered"],
		ClearEventOrdinal: events[prefix+"clear"], AuthorityBeforeSHA256: before, AuthorityAtHitSHA256: before,
		AuthorityAfterSHA256: after, ElapsedMS: elapsedMS, DeadlineMS: point.RecoveryDeadlineMS,
		Target: InjectionTargetProjection{Schema: plan.ReceiptContract.TransitionSchema + "/injection-target-v2", Phase: point.Phase,
			Domain: point.TargetDomain, Kind: point.TargetKind, Ordinal: point.TargetOrdinal,
			ServiceOrdinal: point.TargetServiceOrdinal, ServiceKeySHA256: point.TargetServiceKeySHA256,
			CallerPrefix: point.TargetCallerPrefix, SourceStart: point.TargetSourceStart, SourceEnd: point.TargetSourceEnd,
			MemberOrdinal: point.TargetMemberOrdinal, MemberRecordStart: point.TargetMemberStart,
			MemberRecordEnd: point.TargetMemberEnd, AuthoritySHA256: after},
	}
	if measurement.Phase == "logical_delta_b" {
		hit, recovered := observed.activationHit, observed.activationRecovered
		if hit.Schema != "t422-activation-observation-v1" || recovered.Schema != hit.Schema || hit.Point != "hit" || recovered.Point != "recovered" ||
			hit.SelectorDigest == recovered.SelectorDigest || hit.CatalogRootDigest != recovered.CatalogRootDigest ||
			hit.SearchGenerationDigest != recovered.SearchGenerationDigest || hit.PlanDigest != recovered.PlanDigest ||
			hit.ScheduleDigest != recovered.ScheduleDigest || hit.UnitDigest != recovered.UnitDigest ||
			recovered.SearchGenerationDigest != authority[priorPhase].SearchGenerationSHA256 {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
		value.Target.GenerationSHA256, value.Target.ScheduleSHA256, value.Target.PlanSHA256, value.Target.UnitSHA256 =
			recovered.CatalogRootDigest, recovered.ScheduleDigest, recovered.PlanDigest, recovered.UnitDigest
		value.TargetGenerationBefore, value.TargetGenerationAfter = authority[priorPhase].CatalogRootSHA256, recovered.CatalogRootDigest
		value.ObservedRecoveryBranch, value.RequeueCount = "resume_activation_schedule", 1
	} else {
		hit, recovered := observed.markerHit, observed.markerRecovered
		if hit.Schema != "t422-relationship-marker-observation-v3" || hit.Point != "hit" || recovered.Point != "recovered" {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
		hit.Point = recovered.Point
		if hit != recovered || recovered.PriorGenerationDigest != authority[priorPhase].RelationshipGenerationSHA256 ||
			recovered.PriorRootDigest != authority[priorPhase].RelationshipRootSHA256 || !validDigest(recovered.TargetAuthorityDigest) {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
		value.Target.GenerationSHA256, value.Target.ScheduleSHA256, value.Target.PlanSHA256, value.Target.UnitSHA256 =
			recovered.TargetGenerationDigest, recovered.ScheduleDigest, recovered.PlanDigest, recovered.TargetRootDigest
		value.TargetGenerationBefore, value.TargetGenerationAfter = recovered.PriorGenerationDigest, recovered.TargetGenerationDigest
		value.ObservedRecoveryBranch = "recover_marker_owned"
		var ok bool
		value.AuthorityAtHitSHA256, ok = interruptedPublicationAuthorityAtHitSHA256(authority[priorPhase], authority[measurement.Phase], plan)
		if !ok {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
	}
	if err := finishExecutionInjectionDigests(&value, point); err != nil ||
		validateInjectionTransition(point, value, measurement.StartEventOrdinal, measurement.FinishEventOrdinal,
			authority, measurement.Metrics, measurement.ChildProcessRoles, plan, ExecutionFreeze{}) != nil {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	return value, nil
}

//nolint:unused // Darwin execution flow only; unused in Linux builds.
func finishExecutionInjectionDigests(value *InjectionTransition, point FailurePoint) error {
	selector, err := injectionSelectorSHA256(value.Target)
	if err != nil {
		return err
	}
	value.StableTargetSHA256 = recipeDigest("t422-stable-injection-target-v2", selector, value.Target.Domain,
		value.Target.GenerationSHA256, value.Target.ScheduleSHA256, value.Target.PlanSHA256, value.Target.UnitSHA256)
	value.TargetSHA256 = recipeDigest("t422-injection-target-binding-v3", point.Phase, point.Name, point.Boundary,
		value.StableTargetSHA256, value.AuthorityBeforeSHA256, value.AuthorityAfterSHA256)
	value.TargetIdentitySHA256, err = receiptSHA256(value.Target)
	if err != nil {
		return err
	}
	value.HitReportSHA256, err = injectionHitReportSHA256(*value, point)
	if err != nil {
		return err
	}
	value.RecoveryProjectionSHA256, err = injectionRecoveryProjectionSHA256(*value, point)
	return err
}

// composeExecutionRecoveryInjection joins the native prepared schedule and its
// accepted hit/recovery snapshots. The caller supplies only actual joined roots;
// their image/birth identities cannot be derived from epoch permissions.
//
//nolint:unused // Darwin execution flow only; unused in Linux builds.
func composeExecutionRecoveryInjection(plan Plan, freeze ExecutionFreeze, measurement PhaseMeasurement,
	authority map[string]AuthorityPhaseResult, observed executionTransitionObservations,
	events map[string]uint64, times map[string]time.Time, beforeRoot, afterRoot ExecutionEpochOneResult,
) (InjectionTransition, error) {
	if !processAccountingPlanSemantics(plan.Schema) || measurement.Phase != "stale_lease" && measurement.Phase != "process_restart" {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	index := slices.IndexFunc(plan.FailurePoints, func(p FailurePoint) bool { return p.Phase == measurement.Phase })
	if index < 0 {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	point := plan.FailurePoints[index]
	priorPhase := "return_a"
	prepared := observed.stalePreparation
	if measurement.Phase == "process_restart" {
		priorPhase, prepared = "stale_lease", observed.checkpointPreparation
	}
	before, beforeOK := authorityIdentitySHA256(authority[priorPhase])
	after, afterOK := authorityIdentitySHA256(authority[measurement.Phase])
	prefix := "injection:" + measurement.Phase + ":"
	started, finished := times[prefix+"arm"], times[prefix+"recovered"]
	if !beforeOK || !afterOK || before != after || started.IsZero() || !finished.After(started) ||
		prepared.Operations == nil || !prepared.Operations.Completed || !prepared.Operations.DirectoriesSynced ||
		!prepared.Operations.LocksReleased || prepared.Operations.Chunks == 0 || prepared.MemberReads != 0 ||
		prepared.ControlFileReads > ^uint64(0)-prepared.StoreReadAttempts ||
		uint64(measurement.Metrics.ControlReads) < prepared.ControlFileReads+prepared.StoreReadAttempts {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	elapsed := finished.Sub(started)
	elapsedMS := uint64(elapsed / time.Millisecond)
	if elapsed%time.Millisecond != 0 {
		elapsedMS++
	}
	value := InjectionTransition{Schema: plan.ReceiptContract.TransitionSchema + "/injection-v2", FailurePoint: point.Name,
		ArmCount: 1, HitCount: 1, RecoveryCount: 1, ResidueAtHit: 1, SuccessCount: 1, RecoveredCandidates: 1, RequeueCount: 1,
		ArmEventOrdinal: events[prefix+"arm"], HitEventOrdinal: events[prefix+"hit"], RecoveryEventOrdinal: events[prefix+"recovered"],
		ClearEventOrdinal: events[prefix+"clear"], AuthorityBeforeSHA256: before, AuthorityAtHitSHA256: before, AuthorityAfterSHA256: after,
		ElapsedMS: elapsedMS, DeadlineMS: point.RecoveryDeadlineMS,
		Target: InjectionTargetProjection{Schema: plan.ReceiptContract.TransitionSchema + "/injection-target-v2", Phase: point.Phase,
			Domain: prepared.Domain, Kind: point.TargetKind, Ordinal: uint64(prepared.Ordinal), SourceStart: point.TargetSourceStart,
			SourceEnd: point.TargetSourceEnd, MemberOrdinal: point.TargetMemberOrdinal, MemberRecordStart: point.TargetMemberStart,
			MemberRecordEnd: point.TargetMemberEnd, GenerationSHA256: prepared.TargetGeneration, ScheduleSHA256: prepared.RecoverySchedule,
			PlanSHA256: prepared.PlanDigest, UnitSHA256: prepared.ResultIdentity, AuthoritySHA256: after},
		TargetGenerationBefore: prepared.TargetGeneration, TargetGenerationAfter: prepared.TargetGeneration,
	}
	p := &RecoveryPreparationResult{Schema: "t422-native-recovery-preparation-v1", Phase: measurement.Phase,
		PrepareEventOrdinal: events[prefix+"prepare"], AuthoritySHA256: before, PreservedRootsSHA256: authority[priorPhase].ExtractionRootsSHA256,
		TargetGenerationSHA256: prepared.TargetGeneration, PriorScheduleSHA256: prepared.PriorSchedule,
		RecoveryGenerationSHA256: prepared.RecoveryGeneration, RecoveryScheduleSHA256: prepared.RecoverySchedule,
		ScheduleWrites: prepared.Operations.ScheduleWrites, Chunks: prepared.Operations.Chunks,
		Starts: uint64(measurement.Metrics.JobAttempts), Requeues: 1,
		PreparationCompletionWrites: prepared.Operations.CompletionWrites, PreparationDeletes: prepared.Operations.Deletes,
		PublicationCalls: uint64(measurement.Metrics.PublicationWrites), ControlFileReads: prepared.ControlFileReads,
		StoreReadAttempts: prepared.StoreReadAttempts, MemberReads: prepared.MemberReads, StoreWriteAttempts: prepared.StoreWriteAttempts,
		OtherPhaseControlReads:  uint64(measurement.Metrics.ControlReads) - prepared.ControlFileReads - prepared.StoreReadAttempts,
		StoreAuthorityUnchanged: before == after, PreparedBeforeArm: events[prefix+"prepare"] < events[prefix+"arm"],
		DirectoriesSynced: prepared.Operations.DirectoriesSynced, LocksReleasedBeforeWait: prepared.Operations.LocksReleased}
	value.Preparation = p
	if measurement.Phase == "stale_lease" {
		hit, recovered := observed.staleHit, observed.staleRecovered
		if hit.Point != "hit" || recovered.Point != "recovered" || hit.ObservedScheduleChunks == 0 ||
			hit.ObservedScheduleChunks != recovered.ObservedScheduleChunks || recovered.ObservedScheduleChunks != p.Chunks ||
			recovered.ObservedScheduleSuccesses != recovered.ObservedScheduleChunks ||
			recovered.TargetGeneration != prepared.TargetGeneration || recovered.PriorScheduleDigest != prepared.PriorSchedule ||
			recovered.ScheduleGeneration != prepared.RecoveryGeneration || recovered.ScheduleDigest != prepared.RecoverySchedule ||
			recovered.Domain != prepared.Domain || recovered.Ordinal != prepared.Ordinal || recovered.PlanDigest != prepared.PlanDigest ||
			recovered.ResultIdentity != prepared.ResultIdentity {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
		hit.Point, hit.ObservedScheduleSuccesses = recovered.Point, recovered.ObservedScheduleSuccesses
		if hit != recovered {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
		p.Successes = recovered.ObservedScheduleSuccesses
		value.ObservedRecoveryBranch = "fence_stale_lease_requeue_then_complete"
	} else {
		hit, recovered := observed.checkpointHit, observed.checkpointRecovered
		if hit.Point != "checkpoint_hit" || recovered.Point != "recovered" || hit.ObservedScheduleChunks == 0 ||
			hit.ObservedScheduleChunks != recovered.ObservedScheduleChunks || recovered.ObservedScheduleChunks != p.Chunks ||
			recovered.ObservedScheduleSuccesses != recovered.ObservedScheduleChunks ||
			recovered.TargetGeneration != prepared.TargetGeneration || recovered.PriorScheduleDigest != prepared.PriorSchedule ||
			recovered.ScheduleGeneration != prepared.RecoveryGeneration || recovered.ScheduleDigest != prepared.RecoverySchedule ||
			recovered.Domain != prepared.Domain || recovered.Ordinal != prepared.Ordinal || recovered.PlanDigest != prepared.PlanDigest ||
			recovered.ResultIdentity != prepared.ResultIdentity || hit.ResultDigest != recovered.ResultDigest ||
			!beforeRoot.CheckpointHardDeath || !beforeRoot.RootJoined || !beforeRoot.SessionEmpty ||
			!beforeRoot.ServerProcesses.Joined || !afterRoot.RootStarted || !afterRoot.RootJoined || !afterRoot.SessionEmpty || !afterRoot.ServerProcesses.Joined ||
			hit.Priority < 0 || recovered.Priority < 0 || hit.Attempt < 0 || recovered.Attempt < 0 {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
		tool := slices.IndexFunc(freeze.Tools, func(t ExecutionToolIdentity) bool { return t.Role == "phebs" })
		if tool < 0 {
			return InjectionTransition{}, errExecutionReceiptTransition
		}
		p.Successes = recovered.ObservedScheduleSuccesses
		// The prepared checkpoint has one clear completion bit and no root.
		// Its immutable recovered snapshot proves the one idempotent completion
		// and root install for this exact result, with no result rewrite.
		if hit.CompletionFileExists && !hit.CompletionBitSet && recovered.CompletionBitSet {
			p.RecoveryCompletionWrites = 1
		}
		if !hit.RootExists && recovered.RootExists {
			p.RecoveryRootInstalls = 1
		}
		value.ObservedRecoveryBranch = "hard_restart_reap_and_reuse_checkpoint"
		value.ProcessEpochBefore, value.ProcessEpochAfter = beforeRoot.ServerProcesses.ServerEpoch, afterRoot.ServerProcesses.ServerEpoch
		value.ProcessIdentityBeforeSHA256, value.ProcessIdentityAfterSHA256 = beforeRoot.ServerProcesses.NativeIdentitySHA256, afterRoot.ServerProcesses.NativeIdentitySHA256
		value.ProcessImageSHA256 = freeze.Tools[tool].SHA256
		value.ProcessStopEventOrdinal, value.ProcessStartEventOrdinal = events[prefix+"process-stop"], afterRoot.ServerProcesses.StartEventOrdinal
		value.Checkpoint = &CheckpointRecovery{ResultIdentitySHA256: hit.ResultIdentity, ResultDigestSHA256: hit.ResultDigest,
			PlanSHA256: hit.PlanDigest, ExpectationSHA256: hit.ExpectationDigest, PartitionSHA256: hit.PartitionDigest,
			CandidateGenerationSHA256: hit.CandidateGenerationDigest, SourceGenerationSHA256: hit.SourceGenerationDigest,
			ObservationGenerationSHA256: hit.ObservationGenerationDigest, Domain: hit.Domain, ExtractorVersion: hit.ExtractorVersion,
			ExtractionPolicySHA256: hit.ExtractionPolicyDigest, ChunkIdentitySHA256: hit.ChunkIdentity,
			ScheduleStatusAtHit: hit.ScheduleStatus, ChunkStatusAtHit: hit.ChunkStatus, LeasedAtHit: hit.Leased,
			CanonicalResultExistsAtHit: hit.CanonicalResultExists, ResultDirectorySyncedAtHit: prepared.Operations.DirectoriesSynced,
			CompletionFileExistsAtHit: hit.CompletionFileExists, CompletionBitClearAtHit: !hit.CompletionBitSet,
			RootAbsentAtHit: !hit.RootExists, CurrentAbsentAtHit: !hit.Current, SameResultBytesReused: hit.ResultDigest == recovered.ResultDigest,
			CompletionExistsAfter: recovered.CompletionFileExists && recovered.CompletionBitSet, RootExistsAfter: recovered.RootExists,
			CurrentAfter: recovered.Current, ScheduleStatusAfter: recovered.ScheduleStatus, ChunkStatusAfter: recovered.ChunkStatus, UnleasedAfter: !recovered.Leased,
			StartCount: 2, CompletionCount: p.RecoveryCompletionWrites, PriorityBefore: uint64(hit.Priority), PriorityAfter: uint64(recovered.Priority),
			AttemptBefore: uint64(hit.Attempt), AttemptAfter: uint64(recovered.Attempt), PrivateLeaseTokenChanged: recovered.PrivateLeaseChanged, HardDeath: beforeRoot.CheckpointHardDeath}
	}
	if err := finishExecutionInjectionDigests(&value, point); err != nil ||
		validateInjectionTransition(point, value, measurement.StartEventOrdinal, measurement.FinishEventOrdinal, authority, measurement.Metrics, measurement.ChildProcessRoles, plan, freeze) != nil {
		return InjectionTransition{}, errExecutionReceiptTransition
	}
	return value, nil
}
