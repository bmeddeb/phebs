package t421

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestCorrectedInspectionInventoryIsPhaseAndEpochDerived(t *testing.T) {
	plan := correctedTestPlan(t)
	rows, epochs, err := correctedInspectionInventory(plan.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(frozenPhaseOrder()) || len(epochs) != len(correctedExecutionServerEpochs()) {
		t.Fatal("compact inspector inventory is incomplete")
	}
	for index, phase := range frozenPhaseOrder() {
		if rows[index].Phase != phase {
			t.Fatal("compact inspector inventory is out of phase order")
		}
		if phase == "preflight" || phase == "teardown" {
			if !reflect.DeepEqual(rows[index], phaseInspectionInventory{Phase: phase, TransitionReadClass: "none"}) {
				t.Fatalf("%s invented an acceptance inspector: %+v", phase, rows[index])
			}
			continue
		}
		if rows[index].ServerEpoch == 0 || rows[index].ExtractionProgressCalls.Minimum != 1 ||
			rows[index].TailReadinessCalls.Minimum != 1 || rows[index].FinalAuthorityPasses.Minimum < 1 {
			t.Fatalf("%s lacks its compact progress/final authority proof", phase)
		}
	}
	wantRows := []phaseInspectionInventory{
		{Phase: "preflight", TransitionReadClass: "none"},
		{Phase: "cold", ServerEpoch: 1, HealthCalls: CounterBound{Minimum: 1, Maximum: 3_601}, ExtractionProgressCalls: CounterBound{Minimum: 1, Maximum: 2_881}, FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: "none"},
		{Phase: "warm_noop", ServerEpoch: 1, ExtractionProgressCalls: exactInspectionCalls(1), FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: "none", ImmutableMemberReusePhase: "cold"},
		{Phase: "physical_delta_b", ServerEpoch: 1, ExtractionProgressCalls: CounterBound{Minimum: 1, Maximum: 2_881}, FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: correctedPhysicalTransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(1), ControlFileReads: exactInspectionCalls(41), StoreReadAttempts: exactInspectionCalls(0), MemberReads: exactInspectionCalls(4_063_208), StoreWriteAttempts: exactInspectionCalls(0)}},
		{Phase: "logical_delta_b", ServerEpoch: 2, HealthCalls: CounterBound{Minimum: 1, Maximum: 3_601}, ExtractionProgressCalls: CounterBound{Minimum: 1, Maximum: 2_881}, FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: correctedLogicalTransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(2), ControlFileReads: exactInspectionCalls(0), StoreReadAttempts: exactInspectionCalls(10), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}},
		{Phase: "return_a", ServerEpoch: 3, HealthCalls: CounterBound{Minimum: 1, Maximum: 3_601}, ExtractionProgressCalls: CounterBound{Minimum: 1, Maximum: 2_881}, FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: correctedReturnTransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(2), ControlFileReads: exactInspectionCalls(10), StoreReadAttempts: exactInspectionCalls(0), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}},
		{Phase: "stale_lease", ServerEpoch: 3, ExtractionProgressCalls: CounterBound{Minimum: 1, Maximum: 2_881}, FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: correctedStaleLeaseTransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(2), ControlFileReads: exactInspectionCalls(8), StoreReadAttempts: exactInspectionCalls(8), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}, ImmutableMemberReusePhase: "return_a"},
		{Phase: "process_restart", ServerEpoch: 4, HealthCalls: CounterBound{Minimum: 1, Maximum: 3_601}, ExtractionProgressCalls: CounterBound{Minimum: 1, Maximum: 2_881}, FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: correctedCheckpointRestartReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(2), ControlFileReads: exactInspectionCalls(14), StoreReadAttempts: exactInspectionCalls(8), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}, TransitionReadEpochs: []uint64{3, 4}},
		{Phase: "pressure_80", ServerEpoch: 4, ExtractionProgressCalls: exactInspectionCalls(1), FinalAuthorityPasses: exactInspectionCalls(1), LifecycleStatusCalls: CounterBound{Minimum: 1, Maximum: 241}, TransitionReadClass: correctedPressure80TransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(2), ControlFileReads: exactInspectionCalls(0), StoreReadAttempts: exactInspectionCalls(0), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}, ImmutableMemberReusePhase: "process_restart"},
		{Phase: "pressure_90", ServerEpoch: 4, ExtractionProgressCalls: exactInspectionCalls(1), FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: correctedPressure90TransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(1), ControlFileReads: exactInspectionCalls(0), StoreReadAttempts: exactInspectionCalls(0), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}, ImmutableMemberReusePhase: "pressure_80"},
		{Phase: "pressure_75", ServerEpoch: 4, ExtractionProgressCalls: exactInspectionCalls(1), FinalAuthorityPasses: exactInspectionCalls(1), LifecycleStatusCalls: CounterBound{Minimum: 1, Maximum: 241}, TransitionReadClass: correctedPressure75TransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(3), ControlFileReads: exactInspectionCalls(0), StoreReadAttempts: exactInspectionCalls(0), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}, ImmutableMemberReusePhase: "pressure_90"},
		{Phase: "archive_restore", ServerEpoch: 5, HealthCalls: CounterBound{Minimum: 1, Maximum: 3_601}, ExtractionProgressCalls: CounterBound{Minimum: 1, Maximum: 2_881}, FinalAuthorityPasses: exactInspectionCalls(1), TransitionReadClass: correctedArchiveTransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(1), ControlFileReads: exactInspectionCalls(1), StoreReadAttempts: exactInspectionCalls(0), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}},
		{Phase: "lifecycle_collection", ServerEpoch: 5, ExtractionProgressCalls: exactInspectionCalls(1), FinalAuthorityPasses: exactInspectionCalls(1), LifecycleStatusCalls: CounterBound{Minimum: 1, Maximum: 2_881}, TransitionReadClass: correctedLifecycleTransitionReadClass, TransitionRead: &inspectionReadBound{Calls: exactInspectionCalls(1), ControlFileReads: exactInspectionCalls(0), StoreReadAttempts: exactInspectionCalls(0), MemberReads: exactInspectionCalls(0), StoreWriteAttempts: exactInspectionCalls(0)}, ImmutableMemberReusePhase: "archive_restore"},
		{Phase: "product_queries", ServerEpoch: 5, ExtractionProgressCalls: exactInspectionCalls(1), FinalAuthorityPasses: exactInspectionCalls(2), TransitionReadClass: "none", ProductHTTPCalls: exactInspectionCalls(19), ProductMCPCalls: exactInspectionCalls(19), ProductControlFileReads: exactInspectionCalls(160), ProductStoreReadAttempts: exactInspectionCalls(164), ImmutableMemberReusePhase: "lifecycle_collection"},
		{Phase: "teardown", TransitionReadClass: "none"},
	}
	for index := range wantRows {
		if wantRows[index].Phase == "preflight" || wantRows[index].Phase == "teardown" {
			continue
		}
		wantRows[index].TailReadinessCalls = wantRows[index].ExtractionProgressCalls
		wantRows[index].TailControlFileReads = CounterBound{
			Minimum: correctedTailControlReads,
			Maximum: correctedTailControlReads * wantRows[index].TailReadinessCalls.Maximum,
		}
		wantRows[index].TailStoreReadAttempts = CounterBound{
			Minimum: correctedTailStoreReads,
			Maximum: correctedTailStoreReads * wantRows[index].TailReadinessCalls.Maximum,
		}
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("compact inspector phase inventory = %+v, want %+v", rows, wantRows)
	}
	wantEpochs := []epochInspectionInventory{
		{ServerEpoch: 1, HealthCallsMaximum: 3_601, ExtractionProgressCallsMaximum: 5_763, TailReadinessCallsMaximum: 5_763, TailControlFileReadsMaximum: 23_052, TailStoreReadAttemptsMaximum: 23_052, FinalAuthorityPassesMaximum: 3, TransitionReadCallsMaximum: 1, TransitionControlFileReadsMaximum: 41, TransitionMemberReadsMaximum: 4_063_208, AccountedServerRequestsMaximum: 11_530},
		{ServerEpoch: 2, HealthCallsMaximum: 3_601, ExtractionProgressCallsMaximum: 2_881, TailReadinessCallsMaximum: 2_881, TailControlFileReadsMaximum: 11_524, TailStoreReadAttemptsMaximum: 11_524, FinalAuthorityPassesMaximum: 1, TransitionReadCallsMaximum: 2, TransitionStoreReadAttemptsMaximum: 10, AccountedServerRequestsMaximum: 5_765},
		{ServerEpoch: 3, HealthCallsMaximum: 3_601, ExtractionProgressCallsMaximum: 5_762, TailReadinessCallsMaximum: 5_762, TailControlFileReadsMaximum: 23_048, TailStoreReadAttemptsMaximum: 23_048, FinalAuthorityPassesMaximum: 2, TransitionReadCallsMaximum: 5, TransitionControlFileReadsMaximum: 25, TransitionStoreReadAttemptsMaximum: 12, AccountedServerRequestsMaximum: 11_531},
		{ServerEpoch: 4, HealthCallsMaximum: 3_601, ExtractionProgressCallsMaximum: 2_884, TailReadinessCallsMaximum: 2_884, TailControlFileReadsMaximum: 11_536, TailStoreReadAttemptsMaximum: 11_536, FinalAuthorityPassesMaximum: 4, LifecycleStatusCallsMaximum: 482, TransitionReadCallsMaximum: 7, TransitionControlFileReadsMaximum: 7, TransitionStoreReadAttemptsMaximum: 4, AccountedServerRequestsMaximum: 6_261},
		{ServerEpoch: 5, HealthCallsMaximum: 3_601, ExtractionProgressCallsMaximum: 2_883, TailReadinessCallsMaximum: 2_883, TailControlFileReadsMaximum: 11_532, TailStoreReadAttemptsMaximum: 11_532, FinalAuthorityPassesMaximum: 4, LifecycleStatusCallsMaximum: 2_881, TransitionReadCallsMaximum: 2, TransitionControlFileReadsMaximum: 1, ProductHTTPCallsMaximum: 19, ProductMCPCallsMaximum: 19, ProductControlFileReadsMaximum: 160, ProductStoreReadAttemptsMaximum: 164, AccountedServerRequestsMaximum: 8_691},
	}
	if !reflect.DeepEqual(epochs, wantEpochs) {
		t.Fatalf("compact inspector epoch inventory = %+v, want %+v", epochs, wantEpochs)
	}
	var accountedMaximum uint64
	for _, epoch := range epochs {
		accountedMaximum += epoch.AccountedServerRequestsMaximum
	}
	if accountedMaximum != 43_778 {
		t.Fatalf("compact inspector accounted request maximum = %d", accountedMaximum)
	}
	changedProfile := plan.Profile
	changedProfile.Physical.CombinedPhysicalOwners++
	changedRows, _, err := correctedInspectionInventory(changedProfile)
	if err != nil || changedRows[slices.Index(plan.PhaseOrder, "physical_delta_b")].TransitionRead.MemberReads != exactInspectionCalls(4_063_210) {
		t.Fatal("physical transition member reads are not derived from the plan profile")
	}
	digest, err := correctedInspectionInventorySHA256(plan.Profile)
	if err != nil || plan.Correction.InspectionInventorySHA256 != digest {
		t.Fatalf("compact inspector inventory digest = %q, %v", digest, err)
	}
	changed := plan
	correction := *plan.Correction
	changed.Correction = &correction
	changed.Correction.InspectionInventorySHA256 = SHA256([]byte("changed inspection inventory"))
	if err := ValidatePlan(changed); err == nil {
		t.Fatal("changed compact inspector inventory digest was accepted")
	}
}

func TestCorrectedInspectionInventoryPinsFreshAndCacheableBoundaries(t *testing.T) {
	plan := correctedTestPlan(t)
	rows, _, err := correctedInspectionInventory(plan.Profile)
	if err != nil {
		t.Fatal(err)
	}
	byPhase := make(map[string]phaseInspectionInventory, len(rows))
	for _, row := range rows {
		byPhase[row.Phase] = row
	}
	for phase, prior := range map[string]string{
		"warm_noop": "cold", "stale_lease": "return_a", "pressure_80": "process_restart", "pressure_90": "pressure_80",
		"pressure_75": "pressure_90", "lifecycle_collection": "archive_restore", "product_queries": "lifecycle_collection",
	} {
		if byPhase[phase].ImmutableMemberReusePhase != prior {
			t.Fatalf("%s immutable-member reuse = %q, want %q", phase, byPhase[phase].ImmutableMemberReusePhase, prior)
		}
	}
	if !strings.Contains(correctedInspectionPolicy, "archive=R,T,F") ||
		byPhase["logical_delta_b"].ImmutableMemberReusePhase != "" ||
		byPhase["process_restart"].ImmutableMemberReusePhase != "" ||
		byPhase["archive_restore"].ImmutableMemberReusePhase != "" ||
		byPhase["archive_restore"].TransitionRead == nil ||
		byPhase["lifecycle_collection"].TransitionRead == nil ||
		byPhase["product_queries"].TransitionRead != nil ||
		byPhase["product_queries"].TransitionReadClass != "none" ||
		byPhase["logical_delta_b"].HealthCalls.Minimum != 1 ||
		byPhase["process_restart"].HealthCalls.Minimum != 1 ||
		byPhase["product_queries"].TailReadinessCalls != exactInspectionCalls(1) ||
		byPhase["product_queries"].TailControlFileReads != exactInspectionCalls(4) ||
		byPhase["product_queries"].TailStoreReadAttempts != exactInspectionCalls(4) ||
		byPhase["product_queries"].FinalAuthorityPasses != exactInspectionCalls(2) {
		t.Fatal("restart, restore, or product freshness was cached away")
	}
	product := byPhase["product_queries"]
	if product.ProductControlFileReads != exactInspectionCalls(160) ||
		product.ProductStoreReadAttempts != exactInspectionCalls(164) {
		t.Fatalf("product native controls = %+v/%+v", product.ProductControlFileReads, product.ProductStoreReadAttempts)
	}
}

func TestCorrectedTailReadinessFencesPhaseIntentBeforeFinalAuthority(t *testing.T) {
	prior := tailReadinessIdentity{
		RelationshipGenerationSHA256: "relationship-a", RelationshipRootSHA256: "relationship-root-a",
		CallerGenerationSHA256: "caller-a", CallerRootSHA256: "caller-root-a",
	}
	physical := tailReadinessIdentity{
		RelationshipGenerationSHA256: "relationship-b", RelationshipRootSHA256: "relationship-root-b",
		CallerGenerationSHA256: "caller-b", CallerRootSHA256: "caller-root-b",
	}
	logical := tailReadinessIdentity{
		RelationshipGenerationSHA256: "relationship-b", RelationshipRootSHA256: "relationship-root-b",
		CallerGenerationSHA256: prior.CallerGenerationSHA256, CallerRootSHA256: prior.CallerRootSHA256,
	}
	for _, test := range []struct {
		phase   string
		prior   *tailReadinessIdentity
		current tailReadinessIdentity
		ready   bool
	}{
		{phase: "cold", current: prior, ready: true},
		{phase: "warm_noop", prior: &prior, current: prior, ready: true},
		{phase: "physical_delta_b", prior: &prior, current: physical, ready: true},
		{phase: "logical_delta_b", prior: &prior, current: prior},
		{phase: "logical_delta_b", prior: &prior, current: logical, ready: true},
		{phase: "return_a", prior: &prior, current: physical, ready: true},
		{phase: "stale_lease", prior: &prior, current: prior, ready: true},
		{phase: "process_restart", prior: &prior, current: prior, ready: true},
		{phase: "pressure_80", prior: &prior, current: prior, ready: true},
		{phase: "pressure_90", prior: &prior, current: prior, ready: true},
		{phase: "pressure_75", prior: &prior, current: prior, ready: true},
		{phase: "archive_restore", prior: &prior, current: prior, ready: true},
		{phase: "archive_restore", prior: &prior, current: tailReadinessIdentity{
			RelationshipGenerationSHA256: physical.RelationshipGenerationSHA256,
			RelationshipRootSHA256:       physical.RelationshipRootSHA256,
			CallerGenerationSHA256:       prior.CallerGenerationSHA256,
			CallerRootSHA256:             prior.CallerRootSHA256,
		}, ready: true},
		{phase: "archive_restore", prior: &prior, current: tailReadinessIdentity{
			RelationshipGenerationSHA256: physical.RelationshipGenerationSHA256,
			RelationshipRootSHA256:       prior.RelationshipRootSHA256,
			CallerGenerationSHA256:       prior.CallerGenerationSHA256,
			CallerRootSHA256:             prior.CallerRootSHA256,
		}},
		{phase: "lifecycle_collection", prior: &prior, current: prior, ready: true},
		{phase: "product_queries", prior: &prior, current: prior, ready: true},
	} {
		ready, err := correctedTailReadinessTransitionReady(test.phase, test.prior, test.current)
		if err != nil || ready != test.ready {
			t.Fatalf("%s transition ready=%t err=%v, want %t", test.phase, ready, err, test.ready)
		}
	}

	ready, err := correctedTailReadinessTransitionReady("logical_delta_b", &prior, prior)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("old logical authority was accepted")
	}
	if _, err := correctedTailReadinessTransitionReady("unknown", &prior, prior); err == nil {
		t.Fatal("unknown tail transition was accepted")
	}
}

func TestCurrentV2ReadCapsCannotAdmitCompactInspector(t *testing.T) {
	plan := correctedTestPlan(t)
	work := func(phase string) PhaseWorkBounds {
		for _, row := range plan.WorkEnvelope.Phases {
			if row.Phase == phase {
				return row
			}
		}
		t.Fatalf("phase %q is absent", phase)
		return PhaseWorkBounds{}
	}
	domains := uint64(len(plan.Profile.Pipeline.ExtractionDomains))
	exactProgressMaximum := 2 + domains + 2 + 2*store.MaxGenerationScheduleReadAttempts
	for _, phase := range []string{"warm_noop", "logical_delta_b"} {
		if work(phase).ControlReads.Maximum >= exactProgressMaximum {
			t.Fatalf("%s no longer exposes the zero-control counterexample", phase)
		}
	}
	fullScan := 1 + 6*domains + plan.Profile.Physical.CombinedModeledPartitions + 4*domains
	for _, phase := range []string{"cold", "physical_delta_b", "return_a"} {
		if work(phase).ControlReads.Maximum >= 2*fullScan {
			t.Fatalf("%s no longer exposes the inherited double-scan counterexample", phase)
		}
	}
}

func TestMaximumInspectionAttemptsIncludesDeadlineTie(t *testing.T) {
	for _, test := range []struct {
		deadline, cadence uint64
		want              uint64
	}{
		{1, 5_000, 1},
		{5_000, 5_000, 2},
		{20 * 60 * 1_000, 5_000, 241},
		{4 * 60 * 60 * 1_000, 5_000, 2_881},
		{15 * 60 * 1_000, 250, 3_601},
	} {
		if got, err := maximumInspectionAttempts(test.deadline, test.cadence); err != nil || got != test.want {
			t.Fatalf("attempts(%d,%d) = %d, %v; want %d", test.deadline, test.cadence, got, err, test.want)
		}
	}
	if _, err := maximumInspectionAttempts(0, 5_000); err == nil {
		t.Fatal("zero deadline was accepted")
	}
}
