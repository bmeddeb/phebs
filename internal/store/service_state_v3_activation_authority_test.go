package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestServiceStateV3ActivationAuthorityReadsSelectedNinthUnit(t *testing.T) {
	state := newServiceCatalogV3InternalStore(t)
	ctx := t.Context()
	repository := "example.com/acme/activation-authority"
	seedServiceCatalogV3Repo(t, state, repository, strings.Repeat("7", 40))
	services := make([]servicecatalog.Service, 5_121)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%05d", index), DisplayName: fmt.Sprintf("Service %d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	generationA := serviceStateV3Generation(
		t, repository, strings.Repeat("7", 40), "activation-authority-a", services,
	)
	if err := state.PublishServiceCatalogV3Candidate(ctx, generationA); err != nil {
		t.Fatal(err)
	}
	reconcile, err := state.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, state, reconcile)
	search := selectorTestDigest("8")
	activationA, err := state.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, state, activationA)
	pointerA, err := state.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	summaryA, err := state.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	referenceA := ServiceCatalogV3RelationshipReference{
		Repository: repository, RelationshipGenerationDigest: selectorTestDigest("9"),
		RelationshipRootDigest: selectorTestDigest("a"), CatalogRootDigest: pointerA.RootDigest,
		CatalogControlRevision: pointerA.ControlRevision,
		StateControlRevision:   summaryA.ControlRevision, StateSummaryDigest: summaryA.SummaryDigest,
	}
	if err := state.PinServiceCatalogV3RelationshipReference(ctx, referenceA); err != nil {
		t.Fatal(err)
	}
	priorSelector, err := state.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: repository,
		Target: ServiceRuntimeTarget{
			CatalogRootDigest: pointerA.RootDigest, CatalogControlRevision: pointerA.ControlRevision,
			StateControlRevision: summaryA.ControlRevision, StateSummaryDigest: summaryA.SummaryDigest,
			SearchGenerationDigest:       search,
			RelationshipGenerationDigest: referenceA.RelationshipGenerationDigest,
			RelationshipRootDigest:       referenceA.RelationshipRootDigest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanAuthority, err := state.ReadServiceStateV3ActivationAuthority(ctx, priorSelector)
	if err != nil || cleanAuthority != (ServiceStateV3ActivationAuthority{
		PlanDigest: activationA.Plan.Digest, ScheduleDigest: activationA.Schedule.Digest,
		UnitDigest: generationChunkID(activationA.Schedule.Digest, 9, 0),
	}) {
		t.Fatalf("clean activation authority = %+v, %v", cleanAuthority, err)
	}

	servicesB := slices.Clone(services)
	servicesB[5_000].DisplayName = "Service 5000 B"
	generationB := serviceStateV3Generation(
		t, repository, strings.Repeat("7", 40), "activation-authority-b", servicesB,
	)
	targetDescriptor := generationB.Root.ServiceMembers[ServiceStateV3ActivationTransitionTargetOffset]
	if targetDescriptor.Ordinal != int(ServiceStateV3ActivationTransitionTargetOffset) ||
		targetDescriptor.Records != MaxServiceStateV3ChunkRows ||
		targetDescriptor.First != "service-04608" || targetDescriptor.Last != "service-05119" ||
		targetDescriptor.First > servicesB[5_000].Key || targetDescriptor.Last < servicesB[5_000].Key {
		t.Fatalf("target member does not contain changed service: %+v", targetDescriptor)
	}
	if err := state.PublishServiceCatalogV3Candidate(ctx, generationB); err != nil {
		t.Fatal(err)
	}
	reconcile, err = state.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, state, reconcile)
	wrongActivation, err := state.BeginServiceStateV3Activation(
		ctx, repository, selectorTestDigest("d"),
	)
	if err != nil || wrongActivation.Plan == nil || wrongActivation.Schedule == nil {
		t.Fatalf("wrong-search activation = %+v, %v", wrongActivation, err)
	}
	expandServiceStateV3Plan(t, state, wrongActivation)
	targetNext := int(ServiceStateV3ActivationTransitionTargetOffset) + 1
	planResults, err := surrealdb.Query[[]serviceStateV3PlanRec](ctx, state.db, `
UPDATE service_state_v3_plan SET next_chunk = $next
	WHERE digest = $digest RETURN AFTER`, map[string]any{
		"next": targetNext, "digest": wrongActivation.Plan.Digest,
	})
	if err != nil || len(firstDomainRows(planResults)) != 1 {
		t.Fatalf("shape wrong-search activation plan: %v", err)
	}
	scheduleResults, err := surrealdb.Query[[]generationScheduleRec](ctx, state.db, `
UPDATE generation_schedule SET next_offset = total_items, materialized = total_chunks,
	pending = total_chunks - $next, running = 1, succeeded = $succeeded, failed = 0
	WHERE digest = $digest RETURN AFTER`, map[string]any{
		"next": targetNext, "succeeded": targetNext - 1,
		"digest": wrongActivation.Schedule.Digest,
	})
	if err != nil || len(generationScheduleRows(scheduleResults)) != 1 {
		t.Fatalf("shape wrong-search activation schedule: %v", err)
	}
	wrongUnitDigest := generationChunkID(
		wrongActivation.Schedule.Digest, ServiceStateV3ActivationTransitionTargetOffset, 0,
	)
	now := storeTimestamp(time.Now())
	unitResults, err := surrealdb.Query[[]generationChunkRec](ctx, state.db, `
UPDATE generation_schedule_chunk SET status = 'running', not_before = NONE,
	claimed_by = $worker, claimed_at = $now, heartbeat_at = $now,
	finished_at = NONE, error = '', lease_token = $lease
	WHERE identity = $identity RETURN AFTER`, map[string]any{
		"worker": "wrong-search", "now": now, "lease": strings.Repeat("a", 32),
		"identity": wrongUnitDigest,
	})
	if err != nil || len(generationChunkRows(unitResults)) != 1 {
		t.Fatalf("shape wrong-search activation unit: %v", err)
	}
	wrongCtx, wrongLedger, err := readaccounting.Start(
		ctx, readaccounting.Counts{
			StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongErr := state.ReadServiceStateV3ActivationTransition(
		wrongCtx, ServiceStateV3ActivationTransitionRequest{
			Point: ServiceStateV3ActivationTransitionHit, ExpectedSelector: priorSelector,
			PlanDigest: wrongActivation.Plan.Digest, ScheduleDigest: wrongActivation.Schedule.Digest,
			UnitDigest: wrongUnitDigest,
		},
	)
	wrongCounts, finishErr := wrongLedger.Finish()
	if !errors.Is(errors.Join(wrongErr, finishErr), ErrInvalidServiceStateV3) ||
		wrongCounts != (readaccounting.Counts{StoreReadAttempts: 2}) {
		t.Fatalf("wrong-search activation accepted: counts=%+v, err=%v", wrongCounts, wrongErr)
	}
	activation, err := state.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil || activation.Plan == nil || activation.Schedule == nil ||
		activation.Plan.ServiceMemberChunks <= int(ServiceStateV3ActivationTransitionTargetOffset) {
		t.Fatalf("activation = %+v, %v", activation, err)
	}
	expandServiceStateV3Plan(t, state, activation)
	unitDigest := generationChunkID(
		activation.Schedule.Digest, ServiceStateV3ActivationTransitionTargetOffset, 0,
	)
	hitRequest := ServiceStateV3ActivationTransitionRequest{
		Point: ServiceStateV3ActivationTransitionHit, ExpectedSelector: priorSelector,
		PlanDigest: activation.Plan.Digest, ScheduleDigest: activation.Schedule.Digest,
		UnitDigest: unitDigest,
	}
	readTransition := func(
		limit uint64,
		request ServiceStateV3ActivationTransitionRequest,
	) (ServiceStateV3ActivationTransition, readaccounting.Counts, error) {
		readCtx, ledger, startErr := readaccounting.Start(
			ctx, readaccounting.Counts{StoreReadAttempts: limit},
		)
		if startErr != nil {
			t.Fatal(startErr)
		}
		value, readErr := state.ReadServiceStateV3ActivationTransition(readCtx, request)
		counts, finishErr := ledger.Finish()
		return value, counts, errors.Join(readErr, finishErr)
	}
	updateUnit := func(assignments string, variables map[string]any) {
		t.Helper()
		if variables == nil {
			variables = make(map[string]any)
		}
		variables["identity"] = unitDigest
		results, updateErr := surrealdb.Query[[]generationChunkRec](ctx, state.db,
			"UPDATE generation_schedule_chunk SET "+assignments+
				" WHERE identity = $identity RETURN AFTER", variables,
		)
		if updateErr != nil || len(generationChunkRows(results)) != 1 {
			t.Fatalf("update activation transition unit: %v", updateErr)
		}
	}

	for offset := int64(0); offset <= ServiceStateV3ActivationTransitionTargetOffset; offset++ {
		chunk, claimErr := state.ClaimGenerationChunk(ctx, GenerationResourceCPU, "activation-transition")
		if claimErr != nil || chunk.Offset != offset {
			t.Fatalf("claim activation offset %d = %+v, %v", offset, chunk, claimErr)
		}
		result, processErr := state.ProcessServiceStateV3Chunk(ctx, *chunk)
		if processErr != nil || result.Settled {
			t.Fatalf("process activation offset %d = %+v, %v", offset, result, processErr)
		}
		wantApplied := 0
		if offset == ServiceStateV3ActivationTransitionTargetOffset {
			wantApplied = 1
		}
		if result.Applied != wantApplied || result.Read != MaxServiceStateV3ChunkRows {
			t.Fatalf("activation offset %d changed the wrong rows: %+v", offset, result)
		}
		if offset != ServiceStateV3ActivationTransitionTargetOffset {
			if err := state.CompleteGenerationChunk(ctx, *chunk); err != nil {
				t.Fatal(err)
			}
			continue
		}
		changed := serviceStateV3Row(t, state, repository, servicesB[5_000].Key)
		if changed.DisplayName != servicesB[5_000].DisplayName ||
			changed.ActiveCatalogGeneration != generationB.Root.Digest {
			t.Fatalf("target service was not committed by the frozen unit: %+v", changed)
		}
		got, counts, readErr := readTransition(
			ServiceStateV3ActivationTransitionStoreReadAttempts, hitRequest,
		)
		want := ServiceStateV3ActivationTransition{
			Point: ServiceStateV3ActivationTransitionHit, SelectorDigest: priorSelector.Digest,
			CatalogRootDigest: generationB.Root.Digest, SearchGenerationDigest: search,
			PlanDigest: activation.Plan.Digest, ScheduleDigest: activation.Schedule.Digest,
			UnitDigest: unitDigest,
		}
		if readErr != nil || got != want || counts != (readaccounting.Counts{
			StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts,
		}) {
			t.Fatalf("activation hit = %+v, counts=%+v, err=%v", got, counts, readErr)
		}
		validHit := *chunk
		restoreHit := func() {
			updateUnit(
				"priority = $priority, status = $status, not_before = NONE, "+
					"claimed_by = $worker, claimed_at = $claimed, heartbeat_at = $heartbeat, "+
					"finished_at = NONE, error = '', lease_token = $lease",
				map[string]any{
					"priority": validHit.Priority, "status": validHit.Status,
					"worker": validHit.ClaimedBy, "claimed": validHit.ClaimedAt,
					"heartbeat": validHit.HeartbeatAt, "lease": validHit.LeaseToken,
				},
			)
		}
		for name, mutation := range map[string]struct {
			assignments string
			variables   map[string]any
		}{
			"missing_claim":     {assignments: "claimed_at = NONE"},
			"missing_heartbeat": {assignments: "heartbeat_at = NONE"},
			"deferred":          {assignments: "not_before = $value", variables: map[string]any{"value": validHit.ClaimedAt}},
			"finished":          {assignments: "finished_at = $value", variables: map[string]any{"value": validHit.ClaimedAt}},
			"invalid_worker":    {assignments: "claimed_by = $value", variables: map[string]any{"value": " invalid"}},
			"invalid_lease":     {assignments: "lease_token = $value", variables: map[string]any{"value": "invalid"}},
			"early_heartbeat": {
				assignments: "heartbeat_at = $value",
				variables:   map[string]any{"value": validHit.ClaimedAt.Add(-time.Second)},
			},
		} {
			updateUnit(mutation.assignments, mutation.variables)
			if _, counts, err := readTransition(
				ServiceStateV3ActivationTransitionStoreReadAttempts, hitRequest,
			); !errors.Is(err, ErrInvalidServiceStateV3) || counts != (readaccounting.Counts{
				StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts - 1,
			}) {
				t.Fatalf("malformed hit %s accepted: counts=%+v, err=%v", name, counts, err)
			}
			restoreHit()
		}
		if next, err := state.ClaimGenerationChunk(
			ctx, GenerationResourceCPU, "activation-transition-concurrent",
		); !errors.Is(err, ErrNotFound) || next != nil {
			t.Fatalf("repository token admitted next activation chunk = %+v, %v", next, err)
		}
		if err := state.ReleaseGenerationChunk(
			ctx, *chunk, "activation transition interruption",
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, counts, err := readTransition(
		ServiceStateV3ActivationTransitionStoreReadAttempts, hitRequest,
	); !errors.Is(err, ErrInvalidServiceStateV3) || counts != (readaccounting.Counts{
		StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts - 1,
	}) {
		t.Fatalf("released target accepted as hit: counts=%+v, err=%v", counts, err)
	}

	var finalSelector ServiceRuntimeSelector
	var recoveryRequest ServiceStateV3ActivationTransitionRequest
	replayedTarget := false
	for {
		chunk, claimErr := state.ClaimGenerationChunk(ctx, GenerationResourceCPU, "activation-recovery")
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		result, processErr := state.ProcessServiceStateV3Chunk(ctx, *chunk)
		if processErr != nil {
			t.Fatal(processErr)
		}
		selectedNow := false
		if result.Settled && finalSelector.Digest == "" {
			if chunk.Offset != int64(activation.Plan.TotalChunks-1) {
				t.Fatalf("activation settled before terminal unit: %+v", chunk)
			}
			pointerB, pointerErr := state.GetServiceCatalogV3CandidatePointer(ctx, repository)
			if pointerErr != nil {
				t.Fatal(pointerErr)
			}
			summaryB, summaryErr := state.GetServiceStateV3SummaryPoint(ctx, repository)
			if summaryErr != nil {
				t.Fatal(summaryErr)
			}
			referenceB := ServiceCatalogV3RelationshipReference{
				Repository: repository, RelationshipGenerationDigest: selectorTestDigest("b"),
				RelationshipRootDigest: selectorTestDigest("c"), CatalogRootDigest: pointerB.RootDigest,
				CatalogControlRevision: pointerB.ControlRevision,
				StateControlRevision:   summaryB.ControlRevision, StateSummaryDigest: summaryB.SummaryDigest,
			}
			if err := state.PinServiceCatalogV3RelationshipReference(ctx, referenceB); err != nil {
				t.Fatal(err)
			}
			finalSelector, err = state.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
				Repository: repository, ExpectedControlRevision: priorSelector.ControlRevision,
				ExpectedDigest: priorSelector.Digest,
				Target: ServiceRuntimeTarget{
					CatalogRootDigest: pointerB.RootDigest, CatalogControlRevision: pointerB.ControlRevision,
					StateControlRevision: summaryB.ControlRevision, StateSummaryDigest: summaryB.SummaryDigest,
					SearchGenerationDigest:       search,
					RelationshipGenerationDigest: referenceB.RelationshipGenerationDigest,
					RelationshipRootDigest:       referenceB.RelationshipRootDigest,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			recoveryRequest = hitRequest
			recoveryRequest.Point = ServiceStateV3ActivationTransitionRecovered
			recoveryRequest.ExpectedSelector = finalSelector
			selectedNow = true
		}
		if result.Settled && !selectedNow {
			if chunk.Identity != unitDigest || chunk.Offset != ServiceStateV3ActivationTransitionTargetOffset ||
				chunk.Attempt != 0 || result.Applied != 0 || result.Read != 0 {
				t.Fatalf("replay committed target = chunk %+v, result %+v", chunk, result)
			}
			replayedTarget = true
		}
		if finalSelector.Digest != "" {
			if _, counts, err := readTransition(
				ServiceStateV3ActivationTransitionStoreReadAttempts, recoveryRequest,
			); !errors.Is(err, ErrInvalidServiceStateV3) || counts != (readaccounting.Counts{
				StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts - 1,
			}) {
				t.Fatalf("active schedule accepted as recovered: counts=%+v, err=%v", counts, err)
			}
		}
		if err := state.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
		settled, scheduleErr := state.GetGenerationSchedule(
			ctx, activation.Schedule.Repository, activation.Schedule.Stage,
		)
		if scheduleErr != nil {
			t.Fatal(scheduleErr)
		}
		if settled.Status == GenerationScheduleSettled {
			if !replayedTarget {
				t.Fatal("activation settled without replaying the released target")
			}
			break
		}
	}

	wantRecovery := ServiceStateV3ActivationTransition{
		Point: ServiceStateV3ActivationTransitionRecovered, SelectorDigest: finalSelector.Digest,
		CatalogRootDigest: generationB.Root.Digest, SearchGenerationDigest: search,
		PlanDigest: activation.Plan.Digest, ScheduleDigest: activation.Schedule.Digest,
		UnitDigest: unitDigest,
	}
	gotRecovery, counts, err := readTransition(
		ServiceStateV3ActivationTransitionStoreReadAttempts, recoveryRequest,
	)
	if err != nil || gotRecovery != wantRecovery || counts != (readaccounting.Counts{
		StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts,
	}) {
		t.Fatalf("activation recovery = %+v, counts=%+v, err=%v", gotRecovery, counts, err)
	}
	if _, err := state.ReadServiceStateV3ActivationTransition(ctx, recoveryRequest); err != nil {
		t.Fatalf("ordinary activation transition read: %v", err)
	}
	mismatch := recoveryRequest
	mismatch.ExpectedSelector = priorSelector
	if _, counts, err := readTransition(
		ServiceStateV3ActivationTransitionStoreReadAttempts, mismatch,
	); !errors.Is(err, ErrInvalidServiceStateV3) || counts != (readaccounting.Counts{StoreReadAttempts: 1}) {
		t.Fatalf("selector mismatch = counts %+v, err=%v", counts, err)
	}

	readAuthority := func(limit uint64) (ServiceStateV3ActivationAuthority, readaccounting.Counts, error) {
		ctx, ledger, startErr := readaccounting.Start(
			ctx, readaccounting.Counts{StoreReadAttempts: limit},
		)
		if startErr != nil {
			t.Fatal(startErr)
		}
		value, readErr := state.ReadServiceStateV3ActivationAuthority(ctx, finalSelector)
		counts, finishErr := ledger.Finish()
		return value, counts, errors.Join(readErr, finishErr)
	}
	want := ServiceStateV3ActivationAuthority{
		PlanDigest: activation.Plan.Digest, ScheduleDigest: activation.Schedule.Digest,
		UnitDigest: unitDigest,
	}
	got, counts, err := readAuthority(3)
	if err != nil || got != want || counts != (readaccounting.Counts{StoreReadAttempts: 3}) {
		t.Fatalf("authority = %+v, counts=%+v, err=%v", got, counts, err)
	}
	recoveredUnit, err := state.generationChunkByIdentity(ctx, unitDigest)
	if err != nil {
		t.Fatal(err)
	}
	restoreRecovery := func() {
		updateUnit(
			"priority = $priority, status = $status, not_before = NONE, "+
				"claimed_by = $worker, claimed_at = $claimed, heartbeat_at = NONE, "+
				"finished_at = $finished, error = $error, lease_token = ''",
			map[string]any{
				"priority": recoveredUnit.Priority, "status": recoveredUnit.Status,
				"worker": recoveredUnit.ClaimedBy, "claimed": recoveredUnit.ClaimedAt,
				"finished": recoveredUnit.FinishedAt, "error": recoveredUnit.Error,
			},
		)
	}
	for name, mutation := range map[string]struct {
		assignments string
		variables   map[string]any
	}{
		"deferred":          {assignments: "not_before = $value", variables: map[string]any{"value": recoveredUnit.ClaimedAt}},
		"missing_claim":     {assignments: "claimed_at = NONE"},
		"heartbeat":         {assignments: "heartbeat_at = $value", variables: map[string]any{"value": recoveredUnit.ClaimedAt}},
		"missing_finish":    {assignments: "finished_at = NONE"},
		"early_finish":      {assignments: "finished_at = $value", variables: map[string]any{"value": recoveredUnit.ClaimedAt.Add(-time.Second)}},
		"invalid_worker":    {assignments: "claimed_by = $value", variables: map[string]any{"value": " invalid"}},
		"unbounded_failure": {assignments: "error = $value", variables: map[string]any{"value": strings.Repeat("x", MaxGenerationErrorBytes+1)}},
	} {
		updateUnit(mutation.assignments, mutation.variables)
		if _, counts, err := readTransition(
			ServiceStateV3ActivationTransitionStoreReadAttempts, recoveryRequest,
		); !errors.Is(err, ErrInvalidServiceStateV3) || counts != (readaccounting.Counts{
			StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts - 1,
		}) {
			t.Fatalf("malformed recovery %s accepted: counts=%+v, err=%v", name, counts, err)
		}
		if _, counts, err := readAuthority(3); !errors.Is(err, ErrInvalidServiceStateV3) ||
			counts != (readaccounting.Counts{StoreReadAttempts: 3}) {
			t.Fatalf("malformed authority %s accepted: counts=%+v, err=%v", name, counts, err)
		}
		restoreRecovery()
	}

	// The ordinary no-op retires the current schedule pointer. The immutable
	// selected plan/schedule/unit authority must remain readable by identity.
	noop, err := state.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil || !noop.Noop {
		t.Fatalf("activation no-op = %+v, %v", noop, err)
	}
	got, counts, err = readAuthority(3)
	if err != nil || got != want || counts != (readaccounting.Counts{StoreReadAttempts: 3}) {
		t.Fatalf("retired-pointer authority = %+v, counts=%+v, err=%v", got, counts, err)
	}
	if gotRecovery, counts, err = readTransition(
		ServiceStateV3ActivationTransitionStoreReadAttempts, recoveryRequest,
	); err != nil || gotRecovery != wantRecovery || counts != (readaccounting.Counts{
		StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts,
	}) {
		t.Fatalf("retired-pointer recovery = %+v, counts=%+v, err=%v", gotRecovery, counts, err)
	}
	setCompletionResidue := func(priority int, message string) {
		t.Helper()
		results, updateErr := surrealdb.Query[[]generationChunkRec](ctx, state.db, `
UPDATE generation_schedule_chunk SET priority = $priority, error = $error
	WHERE identity = $identity RETURN AFTER`, map[string]any{
			"priority": priority, "error": message, "identity": unitDigest,
		})
		if updateErr != nil || len(generationChunkRows(results)) != 1 {
			t.Fatalf("set activation completion residue: %v", updateErr)
		}
	}
	for _, residue := range []struct {
		name     string
		priority int
		message  string
	}{
		{name: "clean_with_error", priority: GenerationPriorityNeverRun, message: "interrupted"},
		{name: "stale_without_error", priority: GenerationPriorityStale},
	} {
		setCompletionResidue(residue.priority, residue.message)
		if _, counts, err := readTransition(
			ServiceStateV3ActivationTransitionStoreReadAttempts, recoveryRequest,
		); !errors.Is(err, ErrInvalidServiceStateV3) || counts != (readaccounting.Counts{
			StoreReadAttempts: ServiceStateV3ActivationTransitionStoreReadAttempts - 1,
		}) {
			t.Fatalf("mixed transition residue %s accepted: counts=%+v, err=%v", residue.name, counts, err)
		}
		if _, counts, err := readAuthority(3); !errors.Is(err, ErrInvalidServiceStateV3) ||
			counts != (readaccounting.Counts{StoreReadAttempts: 3}) {
			t.Fatalf("mixed completion residue %s accepted: counts=%+v, err=%v", residue.name, counts, err)
		}
	}
	setCompletionResidue(GenerationPriorityStale, "activation transition interruption")

	for limit := uint64(0); limit < 3; limit++ {
		_, counts, err := readAuthority(limit)
		if !errors.Is(err, readaccounting.ErrLimit) ||
			counts != (readaccounting.Counts{StoreReadAttempts: limit + 1}) {
			t.Fatalf("authority limit %d = counts %+v, err=%v", limit, counts, err)
		}
	}
	for limit := uint64(0); limit < ServiceStateV3ActivationTransitionStoreReadAttempts; limit++ {
		_, counts, err := readTransition(limit, recoveryRequest)
		if !errors.Is(err, readaccounting.ErrLimit) ||
			counts != (readaccounting.Counts{StoreReadAttempts: limit + 1}) {
			t.Fatalf("transition limit %d = counts %+v, err=%v", limit, counts, err)
		}
	}
}
