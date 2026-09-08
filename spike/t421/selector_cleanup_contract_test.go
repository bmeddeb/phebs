package t421

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
)

func TestSelectorHandoffCleanupOmissionRetainsHistoricalPlans(t *testing.T) {
	for _, test := range []struct{ path, digest string }{
		{"plan.json", retainedPlanSHA256},
		{"plan-v2.json", retainedPlanV2SHA256},
	} {
		t.Run(test.path, func(t *testing.T) {
			raw, err := os.ReadFile(test.path)
			if err != nil || SHA256(raw) != test.digest {
				t.Fatalf("retained plan identity: %v", err)
			}
			var plan Plan
			if err := json.Unmarshal(raw, &plan); err != nil {
				t.Fatal(err)
			}
			encoded, err := MarshalCanonical(plan)
			if err != nil || !bytes.Equal(raw, encoded) || plan.SelectorHandoffCleanup != nil {
				t.Fatalf("retained plan bytes changed: %v", err)
			}
			if _, ok := SelectorHandoffCleanupForPhase(plan, "cold"); ok {
				t.Fatal("historical plan acquired cleanup admission")
			}
		})
	}
	plan := accountingTestPlan(t)
	if err := validatePlan(plan, &plan.Revisions); err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalCanonical(plan)
	if err != nil || bytes.Contains(raw, []byte(`"selector_handoff_cleanup"`)) {
		t.Fatalf("omitted V3 policy changed its encoding: %v", err)
	}
	var decoded Plan
	if err := json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(decoded, plan) {
		t.Fatalf("prior V3 round trip changed: %v", err)
	}
	for _, phase := range plan.PhaseOrder {
		if _, ok := SelectorHandoffCleanupForPhase(plan, phase); ok {
			t.Fatalf("prior V3 acquired %s cleanup admission", phase)
		}
	}
}

func selectorCleanupTestPlan(t *testing.T) Plan {
	t.Helper()
	plan := accountingTestPlan(t)
	if err := applySelectorHandoffCleanupCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestSelectorHandoffCleanupNativePhaseDerivation(t *testing.T) {
	plan, prior := selectorCleanupTestPlan(t), accountingTestPlan(t)
	if err := validatePlan(plan, &plan.Revisions); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		phase                                                            string
		epoch, rows, summaries, turns, submitted, deleted, reads, writes uint64
	}{
		{"cold", 1, 0, 0, 1, 0, 0, 3, 0},
		{"physical_delta_b", 1, 10_000, 1, 626, 10_001, 10_001, 4_384, 626},
		{"logical_delta_b", 2, 1, 1, 1, 2, 2, 9, 2},
		{"return_a", 3, 10_000, 1, 626, 10_001, 10_001, 4_384, 626},
	}
	policy := plan.SelectorHandoffCleanup
	if policy.Schema != SelectorHandoffCleanupSchema || policy.MaximumDeletesPerTurn != 16 || len(policy.Phases) != len(want) {
		t.Fatal("cleanup policy inventory differs")
	}
	var transactionDelta, rowDelta uint64
	for index, expected := range want {
		row, ok := SelectorHandoffCleanupForPhase(plan, expected.phase)
		wantRow := SelectorHandoffCleanupPhase{
			Phase: expected.phase, ServerEpoch: expected.epoch, Calls: exactInspectionCalls(1),
			PreimageRowsMaximum: expected.rows, SummaryPreimagesMaximum: expected.summaries,
			OwnerTurns:         CounterBound{Minimum: 1, Maximum: expected.turns},
			StoreTransactions:  CounterBound{Minimum: 1, Maximum: expected.turns},
			StoreRows:          CounterBound{Maximum: expected.submitted},
			LifecycleDeleted:   CounterBound{Maximum: expected.deleted},
			StoreReadAttempts:  CounterBound{Minimum: 3, Maximum: expected.reads},
			StoreWriteAttempts: CounterBound{Maximum: expected.writes},
		}
		if !ok || row != wantRow || policy.Phases[index] != wantRow {
			t.Fatalf("cleanup %s = %+v, want %+v", expected.phase, row, wantRow)
		}
		transactionDelta += row.StoreTransactions.Maximum
		rowDelta += row.StoreRows.Maximum
	}
	if transactionDelta != 1_254 || rowDelta != 20_004 {
		t.Fatal("cleanup admission lost actual deletion-row or native transaction cost")
	}
	for index, got := range plan.WorkEnvelope.Phases {
		before := prior.WorkEnvelope.Phases[index]
		cleanup, ok := SelectorHandoffCleanupForPhase(plan, got.Phase)
		if ok {
			for destination, addition := range map[*CounterBound]CounterBound{
				&before.StoreTransactions:   cleanup.StoreTransactions,
				&before.StoreRows:           cleanup.StoreRows,
				&before.LifecycleOwnerTurns: cleanup.OwnerTurns,
				&before.LifecycleDeleted:    cleanup.LifecycleDeleted,
			} {
				destination.Minimum += addition.Minimum
				destination.Maximum += addition.Maximum
			}
			before.ControlReads.Maximum += cleanup.StoreReadAttempts.Maximum
		}
		if !reflect.DeepEqual(got, before) {
			t.Fatalf("%s changed unrelated work or lost explicit cleanup work", got.Phase)
		}
	}
	withoutCleanup := plan
	withoutCleanup.SelectorHandoffCleanup = nil
	withoutCleanup.WorkEnvelope = prior.WorkEnvelope
	if !reflect.DeepEqual(withoutCleanup, prior) {
		t.Fatal("cleanup changed process/tool/authority/deadline/safety or other retained semantics")
	}
	raw, err := MarshalCanonical(plan)
	if err != nil || len(raw) > MaxPlanV3AuthorBytes || bytes.Count(raw, []byte{'\n'}) != 1 {
		t.Fatalf("prospective cleanup canonical bytes=%d: %v", len(raw), err)
	}
	var decoded Plan
	if err := json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(decoded, plan) {
		t.Fatalf("prospective cleanup round trip: %v", err)
	}
}

func TestSelectorHandoffCleanupPageBoundariesAndOverflow(t *testing.T) {
	for _, test := range []struct{ rows, turns, reads, writes uint64 }{
		{0, 1, 9, 1},
		{1, 1, 9, 2},
		{15, 1, 9, 2},
		{16, 2, 16, 2},
		{17, 2, 16, 3},
		{31, 2, 16, 3},
		{32, 3, 23, 3},
		{10_000, 626, 4_384, 626},
	} {
		row, err := selectorHandoffCleanupPhase("test", 1, test.rows, 1)
		if err != nil || row.OwnerTurns.Maximum != test.turns ||
			row.StoreReadAttempts.Maximum != test.reads || row.StoreWriteAttempts.Maximum != test.writes ||
			row.LifecycleDeleted.Maximum != test.rows+1 || row.StoreRows.Maximum != test.rows+1 {
			t.Fatalf("rows=%d derivation=%+v: %v", test.rows, row, err)
		}
	}
	for _, test := range []struct{ rows, summaries uint64 }{
		{1, 0}, {0, 2}, {math.MaxUint64, 1},
	} {
		if _, err := selectorHandoffCleanupPhase("test", 1, test.rows, test.summaries); err == nil {
			t.Fatalf("invalid or overflowing inventory admitted: %+v", test)
		}
	}
}

func TestSelectorHandoffCleanupRejectsPolicyAndBudgetMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Plan)
	}{
		{"omit_after_budget_change", func(p *Plan) { p.SelectorHandoffCleanup = nil }},
		{"historical_schema", func(p *Plan) { p.Schema = PlanV2Schema }},
		{"unknown_policy", func(p *Plan) { p.SelectorHandoffCleanup.Schema = "unknown" }},
		{"delete_cap", func(p *Plan) { p.SelectorHandoffCleanup.MaximumDeletesPerTurn++ }},
		{"placement", func(p *Plan) { p.SelectorHandoffCleanup.PlacementPolicy += "changed" }},
		{"ownership", func(p *Plan) { p.SelectorHandoffCleanup.OwnershipPolicy += "changed" }},
		{"accounting", func(p *Plan) { p.SelectorHandoffCleanup.AccountingPolicy += "changed" }},
		{"missing_phase", func(p *Plan) { p.SelectorHandoffCleanup.Phases = p.SelectorHandoffCleanup.Phases[1:] }},
		{"wrong_epoch", func(p *Plan) { p.SelectorHandoffCleanup.Phases[2].ServerEpoch++ }},
		{"native_row_bound", func(p *Plan) { p.SelectorHandoffCleanup.Phases[1].PreimageRowsMaximum++ }},
		{"logical_dense_budget", func(p *Plan) { p.SelectorHandoffCleanup.Phases[2].StoreTransactions.Maximum = 626 }},
		{"invented_fence_write", func(p *Plan) { p.SelectorHandoffCleanup.Phases[0].StoreRows.Maximum = 1 }},
		{"phase_transactions", func(p *Plan) { p.WorkEnvelope.Phases[4].StoreTransactions.Maximum++ }},
		{"phase_deletes", func(p *Plan) { p.WorkEnvelope.Phases[3].LifecycleDeleted.Maximum++ }},
		{"phase_reads", func(p *Plan) { p.WorkEnvelope.Phases[5].ControlReads.Maximum++ }},
		{"member_reads", func(p *Plan) { p.SelectorHandoffCleanup.Phases[0].MemberVisits.Maximum++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := selectorCleanupTestPlan(t)
			test.mutate(&plan)
			if err := validatePlanExecutionContract(plan); err == nil {
				t.Fatal("mutated cleanup policy or work admission was accepted")
			}
		})
	}
	plan := selectorCleanupTestPlan(t)
	if err := applySelectorHandoffCleanupCorrection(&plan); err == nil {
		t.Fatal("cleanup budget correction was applied twice")
	}
	for _, phase := range []string{"warm_noop", "stale_lease", "process_restart", "unknown"} {
		if _, ok := SelectorHandoffCleanupForPhase(plan, phase); ok {
			t.Fatalf("unchanged selector phase %s acquired cleanup admission", phase)
		}
	}
}

func TestSelectorHandoffCleanupLookupDoesNotAdmitUnknownPolicy(t *testing.T) {
	for _, plan := range []Plan{
		{},
		{Schema: PlanV3Schema},
		{Schema: PlanV2Schema, SelectorHandoffCleanup: &SelectorHandoffCleanupContract{Schema: SelectorHandoffCleanupSchema}},
		{Schema: PlanV3Schema, SelectorHandoffCleanup: &SelectorHandoffCleanupContract{Schema: "unknown"}},
	} {
		if _, ok := SelectorHandoffCleanupForPhase(plan, "cold"); ok {
			t.Fatal("absent, historical or unknown cleanup policy was admitted")
		}
	}
}
