package t421

import (
	"testing"
)

func TestComposeExecutionReaderTransition(t *testing.T) {
	for _, schema := range []string{PlanV3Schema, PlanV5Schema} {
		t.Run(schema, func(t *testing.T) {
			plan := accountingTestPlan(t)
			if schema == PlanV5Schema {
				plan = receiptHandoffTestPlan(t)
			}
			testComposeExecutionReaderTransition(t, plan)
		})
	}
}

func testComposeExecutionReaderTransition(t *testing.T, plan Plan) {
	t.Helper()
	before := AuthorityPhaseResult{Phase: "warm_noop", Outcome: "passed", AuthorityState: AuthorityState{Current: true, SearchGenerationSHA256: testDigest("old-search")}}
	after := AuthorityPhaseResult{Phase: "physical_delta_b", Outcome: "passed", AuthorityState: AuthorityState{Current: true, SearchGenerationSHA256: testDigest("new-search")}}
	authority := map[string]AuthorityPhaseResult{before.Phase: before, after.Phase: after}
	measurement := PhaseMeasurement{Phase: after.Phase, StartEventOrdinal: 1, FinishEventOrdinal: 20, Metrics: ReceiptMetrics{LifecycleOwnerTurns: 2}}
	if plan.Schema == PlanV5Schema {
		measurement.SelectorCleanup = &SelectorCleanupEvidence{
			Schema: SelectorHandoffCleanupSchema, Phase: 4, InputSHA256: testDigest("input"), SelectedRuntimeSHA256: testDigest("selected"),
			Turns: 2, Deleted: 17, MaxDeleted: 16, StoreReadAttempts: 16, StoreWriteAttempts: 2, Done: true,
		}
		measurement.Metrics.LifecycleOwnerTurns, measurement.Metrics.LifecycleDeleted, measurement.Metrics.MaxLifecycleDeletesTurn = 4, 17, 16
		// Independently supplied accounting totals, not minted by composition.
		measurement.Metrics.ControlReads, measurement.Metrics.StoreTransactions, measurement.Metrics.StoreRows = 16, 2, 17
	}
	observation := epochRetentionObservation{Schema: "t422-current-prior-observation-v1", OldSearchGenerationSHA256: before.SearchGenerationSHA256,
		NewSearchGenerationSHA256: after.SearchGenerationSHA256, QuerySHA256: plan.ReaderProbe.QuerySHA256,
		OldProjectionSHA256: plan.ReaderProbe.OldProjectionSHA256, NewProjectionSHA256: plan.ReaderProbe.NewProjectionSHA256,
		PostReleaseProjectionSHA256: plan.ReaderProbe.OldProjectionSHA256, OldRecords: plan.ReaderProbe.ExpectedRecords,
		NewRecords: plan.ReaderProbe.ExpectedRecords, PostReleaseRecords: plan.ReaderProbe.ExpectedRecords,
		PinnedAtUnixNano: 1, ReleasedAtUnixNano: 2, Held: epochRetentionSweep{Attempt: 1, Completeness: "exact"},
		Released: epochRetentionSweep{Attempt: 2, Completeness: "exact"}, OldReaderHeldThroughReprobe: true}
	events := map[string]uint64{}
	for index, name := range []string{"lease-acquire", "new-current", "held-lifecycle", "old-held-query", "new-held-query", "lease-release", "post-release-lifecycle", "post-release-old-query"} {
		events["reader:"+name] = uint64(index + 2)
	}
	value, err := composeExecutionReaderTransition(plan, measurement, authority, observation, events)
	if err != nil || value.PostReleaseOldRecords != observation.PostReleaseRecords {
		t.Fatal("actual reader observation rejected", err)
	}
	for _, mode := range []string{"missing event", "changed post-release query", "failed sweep"} {
		t.Run(mode, func(t *testing.T) {
			changed := observation
			switch mode {
			case "missing event":
				events["reader:old-held-query"] = 0
				defer func() { events["reader:old-held-query"] = 5 }()
			case "changed post-release query":
				changed.PostReleaseProjectionSHA256 = testDigest("changed")
			case "failed sweep":
				changed.Held.Failed = true
			}
			if _, err := composeExecutionReaderTransition(plan, measurement, authority, changed, events); err == nil {
				t.Fatal("invalid reader evidence accepted")
			}
		})
	}
	if plan.Schema == PlanV5Schema {
		for _, mutate := range []func(*PhaseMeasurement){
			func(v *PhaseMeasurement) { v.SelectorCleanup = nil },
			func(v *PhaseMeasurement) { v.Metrics.LifecycleOwnerTurns++ },
			func(v *PhaseMeasurement) { v.Metrics.LifecycleDeleted++ },
			func(v *PhaseMeasurement) { v.Metrics.MaxLifecycleDeletesTurn++ },
		} {
			changed := cloneAccountingMeasurement(t, measurement)
			mutate(&changed)
			if _, err := composeExecutionReaderTransition(plan, changed, authority, observation, events); err == nil {
				t.Fatal("reader composition accepted absent or conflicting cleanup")
			}
		}
	}
}

func TestComposeExecutionLifecycleTransition(t *testing.T) {
	plan := accountingTestPlan(t)
	cycle := pressureTestCycle()
	measurement := PhaseMeasurement{Phase: "lifecycle_collection", StartEventOrdinal: 1, FinishEventOrdinal: 10,
		Metrics: ReceiptMetrics{LifecycleOwnerTurns: CountMetric(cycle.OwnerTurns), LifecycleDeleted: CountMetric(cycle.Deleted)}}
	authority := map[string]AuthorityPhaseResult{}
	for _, phase := range []string{"archive_restore", "lifecycle_collection"} {
		authority[phase] = AuthorityPhaseResult{Phase: phase, Outcome: "passed", AuthorityState: AuthorityState{Current: true, SearchGenerationSHA256: testDigest("same-generation")}}
	}
	events := map[string]uint64{"lifecycle:fence": 2, "lifecycle:capacity": 3}
	value, err := composeExecutionLifecycleTransition(plan, measurement, authority, cycle, events)
	if err != nil || value.OwnerTurns != cycle.OwnerTurns || value.Owners[0].AttemptedAtUnixMS != uint64(cycle.Owners[0].AttemptedAt.UnixMilli()) {
		t.Fatal("native cycle changed", err)
	}
	cycle.Owners[0].Deleted++
	if _, err := composeExecutionLifecycleTransition(plan, measurement, authority, cycle, events); err == nil {
		t.Fatal("incoherent cycle accepted")
	}
	if value.Owners[0].Deleted != 0 {
		t.Fatal("cycle projection aliases owner")
	}
}
