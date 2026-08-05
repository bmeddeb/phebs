package t322

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRetainedReceipt(t *testing.T) {
	data, err := os.ReadFile("results.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > MaxReceiptSize {
		t.Fatalf("retained receipt bytes = %d, want <= %d", len(data), MaxReceiptSize)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(data)),
		"d1ec7b658eef84d2974c50c66d6dca00160a412fd49154c1ad4e232baae695ad"; got != want {
		t.Fatalf("retained receipt SHA-256 = %s, want %s", got, want)
	}

	receipt, err := decodeStrict[Receipt](data)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != ReceiptSchema || receipt.Outcome != "completed" ||
		!receipt.Claims.SourceFree || receipt.Claims.EstablishesSLO ||
		receipt.Claims.EstablishesAccuracy || receipt.Claims.SelectsTopology ||
		receipt.Claims.AuthorizesMultiServiceRelease {
		t.Fatalf("retained receipt authority = %+v", receipt.Claims)
	}
	if len(receipt.Extractions) != 10 {
		t.Fatalf("retained extraction domains = %d, want 10", len(receipt.Extractions))
	}
	nonempty, empty := 0, 0
	seenDomains := make(map[string]struct{}, len(receipt.Extractions))
	for index, extraction := range receipt.Extractions {
		if extraction.SerialOrdinal != index+1 || extraction.Outcome != "succeeded" ||
			!allowedDomains[extraction.Domain] {
			t.Fatalf("retained extraction %d = %+v", index, extraction)
		}
		if _, duplicate := seenDomains[extraction.Domain]; duplicate {
			t.Fatalf("duplicate retained extraction domain %q", extraction.Domain)
		}
		seenDomains[extraction.Domain] = struct{}{}
		switch extraction.Reason {
		case "published_nonempty":
			nonempty++
		case "published_empty":
			empty++
		default:
			t.Fatalf("retained extraction %q reason = %q", extraction.Domain, extraction.Reason)
		}
	}
	if nonempty != 6 || empty != 4 {
		t.Fatalf("retained extraction publication split = %d/%d, want 6/4", nonempty, empty)
	}
	if receipt.Retention.ComponentsObserved != 52 ||
		receipt.Retention.UnchangedComponents != 36 || len(receipt.Retention.Changed) != 16 {
		t.Fatalf("retained retention census = %+v", receipt.Retention)
	}
	if receipt.Recovery.Outcome != "succeeded" ||
		!receipt.Recovery.PriorPublicationPreserved || !receipt.Recovery.EventualCurrent {
		t.Fatalf("retained recovery = %+v", receipt.Recovery)
	}
	if receipt.Teardown.Outcome != "retained_under_authorization" ||
		!receipt.Teardown.SourceCopyRetained || receipt.Teardown.DerivedDataRetained {
		t.Fatalf("retained teardown = %+v", receipt.Teardown)
	}
}

func TestBuildReceiptIsSourceFreeDeterministicAndBounded(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	planBytes := marshalTestJSON(t, plan)
	observationBytes := marshalTestJSON(t, observation)

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

	receipt, err := decodeStrict[Receipt](first)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != ReceiptSchema || !receipt.Claims.SourceFree ||
		receipt.Claims.EstablishesSLO || receipt.Claims.EstablishesAccuracy ||
		receipt.Claims.SelectsTopology || receipt.Claims.AuthorizesMultiServiceRelease ||
		!isSHA256(receipt.RunCommitment) {
		t.Fatalf("receipt authority = %+v", receipt)
	}
	for _, private := range []string{
		plan.CommitmentNonce, plan.Authorization.Approver,
		plan.Authorization.Custodian, plan.Target.Repository,
		plan.Target.Commit, plan.Target.MirrorPath, plan.Host.Identifier,
		plan.QueryBattery[0].Query, plan.Teardown.Owner,
	} {
		if bytes.Contains(first, []byte(private)) {
			t.Fatalf("receipt leaked private value %q", private)
		}
	}
}

func TestRunCommitmentBindsPrivatePlanAndObservation(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	planBytes := marshalTestJSON(t, plan)
	observationBytes := marshalTestJSON(t, observation)
	baselineBytes, err := BuildReceipt(planBytes, observationBytes, PlanDigest(planBytes))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := decodeStrict[Receipt](baselineBytes)
	if err != nil {
		t.Fatal(err)
	}

	plan.Host.Identifier = "private-host-b"
	changedPlanBytes := marshalTestJSON(t, plan)
	changedBytes, err := BuildReceipt(
		changedPlanBytes, observationBytes, PlanDigest(changedPlanBytes),
	)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := decodeStrict[Receipt](changedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if changed.RunCommitment == baseline.RunCommitment {
		t.Fatal("private plan change did not change the run commitment")
	}

	observation.Index.ShardBytes++
	changedObservation := marshalTestJSON(t, observation)
	changedBytes, err = BuildReceipt(
		planBytes, changedObservation, PlanDigest(planBytes),
	)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = decodeStrict[Receipt](changedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if changed.RunCommitment == baseline.RunCommitment {
		t.Fatal("observation change did not change the run commitment")
	}
}

func TestBuildReceiptRejectsUnfrozenOrOpenInputs(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	planBytes := marshalTestJSON(t, plan)
	observationBytes := marshalTestJSON(t, observation)

	if _, err := BuildReceipt(
		planBytes, observationBytes, "sha256:"+strings.Repeat("0", 64),
	); err == nil || !strings.Contains(err.Error(), "does not match frozen digest") {
		t.Fatalf("frozen digest error = %v", err)
	}

	var open map[string]any
	if err := json.Unmarshal(observationBytes, &open); err != nil {
		t.Fatal(err)
	}
	open["private_repository"] = "secret.invalid/repository"
	openBytes := marshalTestJSON(t, open)
	if _, err := BuildReceipt(planBytes, openBytes, PlanDigest(planBytes)); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("open observation error = %v", err)
	}

	observation = validObservation(plan)
	observation.Failures = []FailureObservation{{
		Phase: "index", Class: "private_failure_name", Terminal: true,
	}}
	if _, err := BuildReceipt(
		planBytes, marshalTestJSON(t, observation), PlanDigest(planBytes),
	); err == nil || !strings.Contains(err.Error(), "open or invalid class") {
		t.Fatalf("open failure class error = %v", err)
	}
}

func TestCompletedReceiptEnforcesFrozenCeilings(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	observation.Index.PeakRSSBytes = plan.Ceilings.IndexPeakRSSBytes + 1
	planBytes := marshalTestJSON(t, plan)

	if _, err := BuildReceipt(
		planBytes, marshalTestJSON(t, observation), PlanDigest(planBytes),
	); err == nil || !strings.Contains(err.Error(), "index measurement") {
		t.Fatalf("ceiling error = %v", err)
	}

	observation.Outcome = "stopped"
	observation.Failures = []FailureObservation{{
		Phase: "index", Class: "physical_index", Terminal: true,
	}}
	if _, err := BuildReceipt(
		planBytes, marshalTestJSON(t, observation), PlanDigest(planBytes),
	); err != nil {
		t.Fatalf("valid classified stop: %v", err)
	}
}

func TestEarlyIndexStopRetainsClassifiedSourceFreeReceipt(t *testing.T) {
	plan := validPrivatePlan()
	observation := validObservation(plan)
	observation.Outcome = "stopped"
	observation.Index = IndexObservation{Outcome: "stopped", Publication: "unavailable"}
	observation.Restart = RestartObservation{Outcome: "not_run"}
	observation.Search = SearchObservation{
		Outcome: "not_run", QueryCount: len(plan.QueryBattery),
	}
	observation.Candidate = CandidateObservation{Outcome: "not_run", Decision: "not_ready"}
	observation.Extractions = []ExtractionObservation{{
		SerialOrdinal: 1, Domain: "proto-contract", Outcome: "not_run", Reason: "not_ready",
	}}
	observation.Recovery = RecoveryObservation{Outcome: "not_run", InterruptedPhase: "index"}
	observation.Failures = []FailureObservation{{
		Phase: "index", Class: "physical_index", Terminal: true,
	}}
	planBytes := marshalTestJSON(t, plan)
	if _, err := BuildReceipt(
		planBytes, marshalTestJSON(t, observation), PlanDigest(planBytes),
	); err != nil {
		t.Fatalf("classified early stop: %v", err)
	}

	observation.Failures = nil
	if _, err := BuildReceipt(
		planBytes, marshalTestJSON(t, observation), PlanDigest(planBytes),
	); err == nil || !strings.Contains(err.Error(), "lacks a fixed failure classification") {
		t.Fatalf("missing phase classification error = %v", err)
	}
}

func TestRetentionDeltaUsesClosedRegistry(t *testing.T) {
	if len(allowedRetentionComponents) != 52 {
		t.Fatalf("retention allowlist = %d, want 52", len(allowedRetentionComponents))
	}
	plan := validPrivatePlan()
	observation := validObservation(plan)
	before, after := int64(1), int64(2)
	observation.Retention.UnchangedComponents = 51
	observation.Retention.Changed = []RetentionComponentDelta{{
		Component:   "private_table_name",
		CountBefore: RetentionMetric{Value: &before, Completeness: "exact"},
		CountAfter:  RetentionMetric{Value: &after, Completeness: "exact"},
	}}
	planBytes := marshalTestJSON(t, plan)
	if _, err := BuildReceipt(
		planBytes, marshalTestJSON(t, observation), PlanDigest(planBytes),
	); err == nil || !strings.Contains(err.Error(), "unknown retention component") {
		t.Fatalf("retention allowlist error = %v", err)
	}
}

func validPrivatePlan() PrivatePlan {
	return PrivatePlan{
		Schema:          PrivatePlanSchema,
		CommitmentNonce: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		FrozenAt:        "2026-08-04T12:00:00Z",
		Authorization: PrivateAuthorization{
			ApprovedAt: "2026-08-04T10:00:00Z",
			ExpiresAt:  "2026-08-06T10:00:00Z",
			Approver:   "private-approver",
			Custodian:  "private-custodian",
		},
		Target: PrivateTarget{
			Repository: "private.invalid/authorized/monorepo",
			Commit:     strings.Repeat("b", 40),
			MirrorPath: "/private/authorized/mirror.git",
		},
		Artifact: PrivateArtifact{
			PhebsCommit:  strings.Repeat("c", 40),
			BinarySHA256: "sha256:" + strings.Repeat("d", 64),
			ConfigSHA256: "sha256:" + strings.Repeat("e", 64),
		},
		Host: PrivateHost{
			Identifier: "private-host-a", OperatingSystem: "private-os",
			Architecture: "private-arch", LogicalCPUs: 8,
			MemoryBytes: 16 << 30, DataVolumeAvailableBytes: 100 << 30,
		},
		Ceilings: PrivateCeilings{
			TotalWallMS: 3_600_000, IndexWallMS: 1_800_000,
			IndexPeakRSSBytes: 8 << 30, IndexShardBytes: 50 << 30,
			RestartWallMS: 120_000, SearchColdMaxWallMS: 30_000,
			SearchWarmMaxWallMS: 10_000, CandidateWallMS: 900_000,
			CandidatePeakSpoolBytes: 10 << 30, ExtractionWallMS: 900_000,
			RecoveryWallMS: 900_000, RetentionWallMS: 120_000,
			MinimumAvailableDataBytes: 1 << 30,
		},
		QueryBattery: []PrivateQuery{
			{ID: "q1", Class: "broad", Query: "private broad query"},
			{ID: "q2", Class: "literal", Query: "private literal query"},
			{ID: "q3", Class: "path", Query: "private path query"},
			{ID: "q4", Class: "symbol", Query: "private symbol query"},
		},
		CloneFetchPosture:         "excluded",
		ExpectedExtractionDomains: []string{"proto-contract"},
		FailureClasses:            slicesClone(failureClasses),
		RetainedScalarFields:      slicesClone(requiredRetainedFields),
		PhaseOrder:                slicesClone(phaseOrder),
		StopConditions: []PrivateStopCondition{{
			ID: "resource-stop", Class: "environment",
			Condition: "private frozen resource condition",
		}},
		Teardown: PrivateTeardown{
			Owner: "private-owner", Deadline: "2026-08-06T09:00:00Z",
			Procedure: "private teardown procedure",
		},
		ProvisionalPacksOffForIndex: true,
		OneExtractionPackAtATime:    true,
		RawArtifactsOutsideRepo:     true,
	}
}

func validObservation(plan PrivatePlan) Observation {
	volumeTotal, volumeAvailable := int64(100<<30), int64(80<<30)
	return Observation{
		Schema: ObservationSchema, MeasuredOn: "2026-08-04",
		PhebsCommit: plan.Artifact.PhebsCommit, Outcome: "completed",
		TotalWallMS: 10_000,
		CloneFetch:  CloneFetchObservation{Posture: "excluded"},
		Index: IndexObservation{
			Outcome: "succeeded", WallMS: 1_000, PeakRSSBytes: 100 << 20,
			ShardBytes: 10 << 20, ShardCount: 4, Publication: "visible",
		},
		Restart: RestartObservation{
			Outcome: "succeeded", WallMS: 100, AlreadyCurrent: true,
		},
		Search: SearchObservation{
			Outcome: "succeeded", QueryCount: len(plan.QueryBattery),
			Cold:            SearchPass{Completed: len(plan.QueryBattery), TotalWallMS: 40, MaxWallMS: 15},
			Warm:            SearchPass{Completed: len(plan.QueryBattery), TotalWallMS: 20, MaxWallMS: 8},
			ResultSetsEqual: true,
		},
		Candidate: CandidateObservation{
			Outcome: "succeeded", Decision: "rebuild", TotalMS: 1_000,
			PeakSpoolBytes: 1 << 20, DeclaredSourceBytes: 2 << 20,
			RepositoryRecords: 10, CallerRecords: 4,
		},
		Extractions: []ExtractionObservation{{
			SerialOrdinal: 1, Domain: "proto-contract", Outcome: "succeeded",
			Reason: "published_nonempty", TotalMS: 1_000,
			CandidateFiles: 10, OpenedFiles: 10, OpenedBytes: 1 << 20,
			Facts: 20,
		}},
		Recovery: RecoveryObservation{
			Outcome: "succeeded", InterruptedPhase: "index", WallMS: 1_000,
			PriorPublicationPreserved: true, EventualCurrent: true,
		},
		Retention: RetentionObservation{
			Outcome: "succeeded", WallMS: 100, ComponentsObserved: 52,
			UnchangedComponents: 52,
			DataVolumeBefore: RetentionDataVolume{
				TotalBytes:     RetentionMetric{Value: &volumeTotal, Completeness: "exact"},
				AvailableBytes: RetentionMetric{Value: &volumeAvailable, Completeness: "exact"},
			},
			DataVolumeAfter: RetentionDataVolume{
				TotalBytes:     RetentionMetric{Value: &volumeTotal, Completeness: "exact"},
				AvailableBytes: RetentionMetric{Value: &volumeAvailable, Completeness: "exact"},
			},
		},
		Teardown: TeardownObservation{Outcome: "destroyed", WallMS: 100},
	}
}

func marshalTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

func slicesClone(values []string) []string {
	return append([]string(nil), values...)
}
