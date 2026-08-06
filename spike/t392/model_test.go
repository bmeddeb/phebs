package t392

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRetainedStoppedReceiptIsClosedAndPinned(t *testing.T) {
	data, err := os.ReadFile("results.json")
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(data))
	const want = "20e95fc8e7bf11fb9ae317aa57aeda2966fbf1c866d812f37fadb11aabd26a41"
	if got != want {
		t.Fatalf("results.json SHA-256 = %s, want %s", got, want)
	}
	receipt, err := DecodeReceipt(data)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "stopped" || receipt.Incremental.Outcome != "failed" ||
		len(receipt.Failures) != 1 || receipt.Failures[0] != (FailureObservation{
		Phase: "incremental", Class: "pipeline", Terminal: true,
	}) || receipt.NoOp.Outcome != "not_run" || receipt.Query.Outcome != "not_run" ||
		receipt.Recovery.Outcome != "not_run" || receipt.Restore.Outcome != "not_run" ||
		receipt.Retention.Outcome != "not_run" || receipt.Teardown.DerivedDataRetained {
		t.Fatalf("retained stopped receipt changed: %+v", receipt)
	}
}

func TestBuildReceiptIsDeterministicSourceFreeAndClosed(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	planBytes := encode(t, plan)
	observationBytes := encode(t, observation)

	first, err := BuildReceipt(planBytes, observationBytes, PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildReceipt(planBytes, observationBytes, PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identical private inputs produced different receipts")
	}
	if len(first) > MaxReceiptSize {
		t.Fatalf("receipt bytes = %d, want <= %d", len(first), MaxReceiptSize)
	}
	receipt, err := DecodeReceipt(first)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "completed" || !receipt.Claims.SourceFree ||
		!receipt.Claims.OneEnvironmentOnly || receipt.Claims.EstablishesGeneralSLO ||
		receipt.Claims.EstablishesAccuracy || receipt.Claims.AuthorizesRelease ||
		receipt.Claims.EstablishesMigrationComplete || receipt.Claims.EstablishesDecommissionSafety {
		t.Fatalf("receipt claims = %+v", receipt.Claims)
	}
	for _, private := range []string{
		plan.CommitmentNonce, plan.Authorization.Approver, plan.Authorization.Custodian,
		plan.Target.Repository, plan.Target.BaseCommit, plan.Target.TargetCommit,
		plan.Target.MirrorPath, plan.Artifact.BinarySHA256, plan.Artifact.ConfigSHA256,
		plan.Host.Identifier, plan.Authority.CatalogPath, plan.Authority.CatalogSHA256,
		plan.QueryBattery[0].Query, plan.Teardown.Owner, plan.Teardown.Procedure,
	} {
		if strings.Contains(string(first), private) {
			t.Fatalf("receipt leaked private value %q", private)
		}
	}
}

func TestBuildReceiptBindsFrozenInputs(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	planBytes := encode(t, plan)
	observationBytes := encode(t, observation)
	if _, err := BuildReceipt(planBytes, observationBytes, "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("mismatched frozen digest accepted")
	}
	first, err := BuildReceipt(planBytes, observationBytes, PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	plan.CommitmentNonce = "10112233445566778899aabbccddeeffffeeddccbbaa99887766554433221100"
	planBytes = encode(t, plan)
	second, err := BuildReceipt(planBytes, observationBytes, PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("changed private commitment produced identical receipt")
	}
}

func TestCompletedEnvelopeRefusesThresholdAndNoOpWork(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	observation.Cold.PeakRSSBytes = plan.Thresholds.ColdPeakRSSBytes + 1
	assertObservationRefused(t, plan, observation)

	observation = validObservation(plan)
	observation.NoOp.DurableWrites = 1
	assertObservationRefused(t, plan, observation)

	observation = validObservation(plan)
	observation.Incremental.RelationshipGenerationChecked = false
	assertObservationRefused(t, plan, observation)
}

func TestStoppedRunRetainsClassificationAndAllowsUnrunPhases(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	observation.Outcome = "stopped"
	observation.TotalWallMS = plan.Thresholds.TotalWallMS + 1
	observation.Cold.Outcome = "failed"
	observation.Incremental = IncrementalObservation{Outcome: "not_run"}
	observation.NoOp = NoOpObservation{Outcome: "not_run"}
	observation.Query = QueryObservation{Outcome: "not_run", QueryCount: len(plan.QueryBattery)}
	observation.Recovery = RecoveryObservation{Outcome: "not_run", InterruptedPhase: "source"}
	observation.Restore = RestoreObservation{Outcome: "not_run"}
	observation.Retention = RetentionObservation{Outcome: "not_run"}
	observation.Failures = []FailureObservation{{Phase: "cold", Class: "physical_index", Terminal: true}}
	observation.Topology = TopologyDecision{
		Direct: "stop", Cohorts: "reopen", P6: "not_triggered", Reason: "direct_failed_reopen_cohorts",
	}
	planBytes, observationBytes := encode(t, plan), encode(t, observation)
	receiptBytes, err := BuildReceipt(planBytes, observationBytes, PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeReceipt(receiptBytes)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "stopped" || len(receipt.Failures) != 1 {
		t.Fatalf("stopped receipt = %+v", receipt)
	}

	observation.Failures = nil
	assertObservationRefused(t, plan, observation)
}

func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	planBytes := encode(t, plan)
	planBytes = bytes.Replace(planBytes, []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1)
	if _, err := BuildReceipt(planBytes, encode(t, observation), PlanDigest(planBytes)); err == nil {
		t.Fatal("unknown private-plan field accepted")
	}
}

func TestValidatePrivatePlanRequiresClosedOperatingInputs(t *testing.T) {
	plan := validPrivatePlan()
	if err := ValidatePrivatePlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.Tools = plan.Tools[:len(plan.Tools)-1]
	if err := ValidatePrivatePlan(plan); err == nil {
		t.Fatal("incomplete tool identity set accepted")
	}
	plan = validPrivatePlan()
	plan.QueryBattery = plan.QueryBattery[:2]
	if err := ValidatePrivatePlan(plan); err == nil {
		t.Fatal("incomplete query battery accepted")
	}
	plan = validPrivatePlan()
	plan.Safety.NoAutomaticAuthority = false
	if err := ValidatePrivatePlan(plan); err == nil {
		t.Fatal("automatic authority accepted")
	}
}

func TestFinalizeCandidateChangesOnlyApprovalFields(t *testing.T) {
	plan := validPrivatePlan()
	wantTarget, wantArtifact := plan.Target, plan.Artifact
	wantThresholds, wantSafety := plan.Thresholds, plan.Safety
	plan.CommitmentNonce = "PENDING_AFTER_EXPLICIT_APPROVAL"
	plan.FrozenAt = "PENDING_AFTER_EXPLICIT_APPROVAL"
	plan.Authorization.ApprovedAt = "PENDING_AFTER_EXPLICIT_APPROVAL"
	plan.Authorization.ExpiresAt = "PENDING_AFTER_EXPLICIT_APPROVAL"
	plan.Authorization.Approver = "PENDING_EXPLICIT_APPROVER"
	plan.Authority.AuthorityConfirmed = false
	plan.Teardown.Deadline = "PENDING_AFTER_EXPLICIT_APPROVAL"
	finalized, err := FinalizeCandidate(
		encode(t, plan),
		"00112233445566778899aabbccddeeffffeeddccbbaa99887766554433221100",
		"2026-08-06T12:00:00Z", "2026-08-07T12:00:00Z", "ben",
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeStrict[PrivatePlan](finalized)
	if err != nil {
		t.Fatal(err)
	}
	if got.Target != wantTarget || got.Artifact != wantArtifact ||
		got.Thresholds != wantThresholds || got.Safety != wantSafety ||
		!got.Authority.AuthorityConfirmed || got.Authorization.Approver != "ben" {
		t.Fatalf("finalized plan changed substantive content: %+v", got)
	}
	if _, err := FinalizeCandidate(finalized, got.CommitmentNonce, got.FrozenAt,
		got.Authorization.ExpiresAt, "ben"); err == nil {
		t.Fatal("already-finalized plan accepted for a second freeze")
	}
}

func TestTemplatesTrackClosedSchemas(t *testing.T) {
	planBytes, err := os.ReadFile("private-plan.template.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStrict[PrivatePlan](planBytes); err != nil {
		t.Fatalf("private plan template shape: %v", err)
	}
	observationBytes, err := os.ReadFile("observation.template.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStrict[Observation](observationBytes); err != nil {
		t.Fatalf("observation template shape: %v", err)
	}
}

func assertObservationRefused(t *testing.T, plan PrivatePlan, observation Observation) {
	t.Helper()
	planBytes := encode(t, plan)
	if _, err := BuildReceipt(planBytes, encode(t, observation), PlanDigest(planBytes)); err == nil {
		t.Fatal("invalid observation accepted")
	}
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func validPrivatePlan() PrivatePlan {
	hashA := "sha256:" + strings.Repeat("a", 64)
	hashB := "sha256:" + strings.Repeat("b", 64)
	return PrivatePlan{
		Schema:          PrivatePlanSchema,
		CommitmentNonce: "00112233445566778899aabbccddeeffffeeddccbbaa99887766554433221100",
		FrozenAt:        "2026-08-06T12:00:00Z",
		Authorization: PrivateAuthorization{
			ApprovedAt: "2026-08-06T11:59:00Z", ExpiresAt: "2026-08-07T12:00:00Z",
			Approver: "private-approver", Custodian: "private-custodian",
		},
		Target: PrivateTarget{
			Repository: "private-target", BaseCommit: strings.Repeat("1", 40),
			TargetCommit: strings.Repeat("2", 40), MirrorPath: "/private/target.git",
		},
		Artifact: PrivateArtifact{
			PhebsCommit: strings.Repeat("3", 40), BinarySHA256: hashA, ConfigSHA256: hashB,
		},
		Tools: []PrivateTool{
			{Name: "git", Version: "git private", SHA256: hashA},
			{Name: "go", Version: "go private", SHA256: hashB},
			{Name: "surreal", Version: "surreal private", SHA256: hashA},
			{Name: "zoekt-git-index", Version: "zoekt private", SHA256: hashB},
		},
		Host: PrivateHost{
			Identifier: "private-host", OperatingSystem: "private-os", Architecture: "private-arch",
			LogicalCPUs: 10, MemoryBytes: 24 << 30, DataVolumeAvailableBytes: 50 << 30,
		},
		Authority: PrivateAuthority{
			CatalogPath: "/private/catalog.json", CatalogSHA256: hashA, CatalogVersion: "private-version",
			Services: 4, Memberships: 12, UnownedPaths: 2, MaxAcceptedFanout: 2,
			CanonicalBytes: 4096, AuthorityConfirmed: true,
		},
		Thresholds: PrivateThresholds{
			TotalWallMS: 4_000_000, ColdWallMS: 1_000_000, ColdPeakRSSBytes: 12 << 30,
			ColdShardAllocatedBytes: 25 << 30, ColdDerivedAllocatedBytes: 10 << 30,
			ColdAvailabilityMS: 1_000_000, IncrementalCatchupMS: 600_000,
			IncrementalPeakRSSBytes: 12 << 30, NoOpWallMS: 300_000,
			QueryColdMaxWallMS: 30_000, QueryWarmMaxWallMS: 5_000,
			RecoveryWallMS: 600_000, RestoreWallMS: 600_000, RetentionWallMS: 300_000,
			MinimumAvailableDataBytes: 10 << 30, MaximumLifecycleStatusBytes: LifecycleMaxBytes,
			MaximumFinalFailureCount: 0,
		},
		QueryBattery: []PrivateQuery{
			{ID: "q1", Class: "broad", Scope: "all_code", Query: "private broad"},
			{ID: "q2", Class: "literal", Scope: "service", ServiceKey: "svc-a", Query: "private literal"},
			{ID: "q3", Class: "path", Scope: "relationship", ServiceKey: "svc-a", Query: "private path"},
			{ID: "q4", Class: "symbol", Scope: "all_code", Query: "private symbol"},
		},
		PhaseOrder:     append([]string{}, phaseOrder...),
		FailureClasses: append([]string{}, failureClasses...),
		StopConditions: []PrivateStopCondition{{ID: "stop-authority", Class: "authority", Condition: "private condition"}},
		Teardown: PrivateTeardown{
			Owner: "private-owner", Deadline: "2026-08-07T11:00:00Z", Procedure: "private procedure",
		},
		Safety: PrivateSafety{
			DedicatedDataDirectory: true, RawArtifactsOutsideRepo: true,
			NoPlanMutationAfterFreeze: true, DirectTopologyOnly: true, NoAutomaticAuthority: true,
		},
	}
}

func validObservation(plan PrivatePlan) Observation {
	queryCount := len(plan.QueryBattery)
	return Observation{
		Schema: ObservationSchema, MeasuredOn: "2026-08-06", PhebsCommit: plan.Artifact.PhebsCommit,
		Outcome: "completed", TotalWallMS: 2_000_000,
		Preflight: Preflight{
			Outcome: "succeeded", ArtifactExact: true, ConfigExact: true, SourceExact: true,
			CatalogExact: true, HostExact: true, AuthorityConfirmed: true,
			ToolsExact: len(plan.Tools), AuthorizationValid: true,
		},
		Authority: AuthorityObservation{
			Services: plan.Authority.Services, Memberships: plan.Authority.Memberships,
			UnownedPaths: plan.Authority.UnownedPaths, MaxAcceptedFanout: plan.Authority.MaxAcceptedFanout,
			CanonicalBytes: plan.Authority.CanonicalBytes,
		},
		Cold: ColdObservation{
			Outcome: "succeeded", WallMS: 500_000, PeakRSSBytes: 1 << 30,
			ShardAllocatedBytes: 1 << 30, ShardCount: 1, DerivedAllocatedBytes: 1 << 30,
			SourceMemberBytes: 10_000, SourceMembers: 1, ObservationBytes: 8_000,
			ObservationRecords: 10, RelationshipBytes: 6_000, RelationshipRecords: 5,
			AvailabilityMS: 500_000, Current: true,
		},
		Incremental: IncrementalObservation{
			Outcome: "succeeded", WallMS: 100_000, PeakRSSBytes: 1 << 30, ChangedFiles: 1,
			ShardAllocatedBytesAfter: 1 << 30, SourceGenerationChanged: true,
			SearchGenerationChanged: true, ObservationGenerationChanged: true,
			RelationshipGenerationChecked: true, RelationshipGenerationChanged: true,
			UnaffectedServicesExact: 3, Current: true,
		},
		NoOp: NoOpObservation{Outcome: "succeeded", WallMS: 1_000},
		Query: QueryObservation{
			Outcome: "succeeded", QueryCount: queryCount,
			Cold:            QueryPass{Completed: queryCount, TotalWallMS: 4_000, MaxWallMS: 2_000},
			Warm:            QueryPass{Completed: queryCount, TotalWallMS: 2_000, MaxWallMS: 1_000},
			ResultSetsEqual: true, AuthorityReceipts: queryCount * 2,
			FinalFencesPassed: queryCount * 2, AuthorizationPassed: queryCount * 2,
		},
		Recovery: RecoveryObservation{
			Outcome: "succeeded", InterruptedPhase: "relationship", WallMS: 20_000,
			PriorPublicationPreserved: true, PartialInvisible: true, EventualCurrent: true,
		},
		Restore: RestoreObservation{
			Outcome: "succeeded", WallMS: 20_000, PreciousAuthorityExact: true,
			DerivedGenerationsExact: 4, EventualCurrent: true,
		},
		Retention: RetentionObservation{
			Outcome: "succeeded", WallMS: 1_000, OwnersObserved: LifecycleOwners,
			StatusBytes: 4_000, CapacityAvailable: true, AvailableBeforeBytes: 30 << 30,
			AvailableAfterBytes: 31 << 30, SweepTurns: 1, DeletedRows: 1,
			DeletedGenerations: 1, ProtectedRoots: 2,
		},
		Topology: TopologyDecision{
			Direct: "continue", Cohorts: "not_triggered", P6: "not_triggered",
			Reason: "direct_within_frozen_environment",
		},
		Teardown: TeardownObservation{Outcome: "destroyed", WallMS: 1_000, Custody: "released"},
	}
}
