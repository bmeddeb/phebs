package t421

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"testing"
)

func retainedWorkPlan(t *testing.T) Plan {
	t.Helper()
	raw, err := os.ReadFile("plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestCorrectedFullPassChildrenExposeV1Counterexample(t *testing.T) {
	plan := retainedWorkPlan(t)
	before := frozenWorkEnvelope(plan.Profile)
	terms, err := correctedFullPassGitTerms(plan.Profile)
	if err != nil {
		t.Fatal(err)
	}
	want := []ChildBudgetTerm{
		{Name: "resolver_blob_materialization", Unit: "frozen_resolver_blob", Units: 10_002, ChildrenPerUnit: 1, MaximumAttempts: 3, MaximumChildren: 30_006},
		{Name: "caller_blob_materialization", Unit: "nongenerated_base_go_path", Units: 11_601, ChildrenPerUnit: 1, MaximumAttempts: 3, MaximumChildren: 34_803},
		{Name: "observation_batch_reads", Unit: "nonempty_go_source_member", Units: 16_384, ChildrenPerUnit: 1, MaximumAttempts: 5, MaximumChildren: 81_920},
		{Name: "extraction_batch_reads", Unit: "frozen_extraction_partition", Units: 56, ChildrenPerUnit: 1, MaximumAttempts: 5, MaximumChildren: 280},
	}
	if !reflect.DeepEqual(terms, want) {
		t.Fatalf("source child derivation = %+v, want %+v", terms, want)
	}
	if terms[0].Units <= before.Phases[1].ChildProcessRoles[0].Maximum {
		t.Fatal("one successful resolver build no longer exposes the V1 64-child contradiction")
	}
	total, err := sumChildBudgetTerms(terms)
	if err != nil || total != 147_009 {
		t.Fatalf("source children = %d, %v", total, err)
	}
	if !reflect.DeepEqual(before, frozenWorkEnvelope(plan.Profile)) ||
		!reflect.DeepEqual(before, plan.WorkEnvelope) {
		t.Fatal("prospective derivation changed the retained V1 envelope")
	}
}

func TestCorrectedWatcherBudgetCountsAllPhaseAlignments(t *testing.T) {
	for _, test := range []struct {
		name     string
		deadline uint64
		want     uint64
	}{
		{"partial tick", 1, 1},
		{"exact tick", 3_000, 1},
		{"straddled next tick", 3_001, 2},
		{"revalidation", 20 * 60 * 1_000, 400},
		{"convergence", 4 * 60 * 60 * 1_000, 4_800},
		{"maximum integer", math.MaxUint64, math.MaxUint64/3_000 + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			term, err := correctedWatcherChildTerm(test.deadline)
			if err != nil || term.MaximumChildren != test.want {
				t.Fatalf("watcher children = %+v, %v; want %d", term, err, test.want)
			}
		})
	}
	if _, err := correctedWatcherChildTerm(0); err == nil {
		t.Fatal("zero deadline accepted")
	}
}

func TestCorrectedChildBudgetRejectsOverflowAndInventedTotals(t *testing.T) {
	for _, test := range []struct {
		name                      string
		units, children, attempts uint64
	}{
		{"per-unit multiplication", math.MaxUint64, 2, 1},
		{"attempt multiplication", math.MaxUint64, 1, 2},
		{"zero children", 1, 0, 1},
		{"zero attempts", 1, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := childBudgetTerm(test.name, "unit", test.units, test.children, test.attempts); err == nil {
				t.Fatal("invalid term accepted")
			}
		})
	}
	maximum, err := childBudgetTerm("maximum", "unit", math.MaxUint64, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	one, err := childBudgetTerm("one", "unit", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sumChildBudgetTerms([]ChildBudgetTerm{maximum, one}); err == nil {
		t.Fatal("overflowing sum accepted")
	}
	one.MaximumChildren++
	if _, err := sumChildBudgetTerms([]ChildBudgetTerm{one}); err == nil {
		t.Fatal("invented child total accepted")
	}
}

func TestCorrectedFullPassChildrenRejectInvalidPopulation(t *testing.T) {
	profile := retainedWorkPlan(t).Profile
	profile.Pipeline.GeneratedMappings = profile.Pipeline.SupportedGoFiles + 1
	if _, err := correctedFullPassGitTerms(profile); err == nil {
		t.Fatal("negative nongenerated caller population accepted")
	}
	profile = retainedWorkPlan(t).Profile
	profile.Pipeline.ResolverBlobReadsPerBuild = math.MaxUint64
	if _, err := correctedFullPassGitTerms(profile); err == nil {
		t.Fatal("overflowing resolver attempts accepted")
	}
}

func TestCorrectedServerEpochsFollowImmutableCatalogSelection(t *testing.T) {
	want := []ExecutionServerEpochProfile{
		{ServerEpoch: 1, LaunchPhase: "cold", Phases: []string{"cold", "warm_noop", "physical_delta_b"}},
		{ServerEpoch: 2, LaunchPhase: "logical_delta_b", Phases: []string{"logical_delta_b"}},
		{ServerEpoch: 3, LaunchPhase: "return_a", Phases: []string{"return_a", "stale_lease"}},
		{ServerEpoch: 4, LaunchPhase: "process_restart", Phases: []string{"process_restart", "pressure_80", "pressure_90", "pressure_75"}},
		{ServerEpoch: 5, LaunchPhase: "archive_restore", Phases: []string{"archive_restore", "lifecycle_collection", "product_queries"}},
	}
	got := correctedExecutionServerEpochs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("corrected epochs = %+v, want %+v", got, want)
	}
	got[0].Phases[0] = "changed"
	if !reflect.DeepEqual(correctedExecutionServerEpochs(), want) {
		t.Fatal("epoch phase slices are shared mutable state")
	}
}

func TestCorrectedAuthorInventoryHasFourColdAndThreeDeltaCommands(t *testing.T) {
	commands := correctedAuthorGitCommands()
	if len(commands) != 4 || commands[0].Name != "init_bare" ||
		commands[1].Name != "import_revision" || commands[2].Name != "verify_revision" ||
		commands[3].Name != "verify_tree_inventory" {
		t.Fatalf("author command inventory = %+v", commands)
	}
	for _, command := range commands {
		if command.ToolRole != "git" || command.EnvironmentClass != "recovery" {
			t.Fatalf("unclosed author command: %+v", command)
		}
	}
	// --verify accepts exactly one revision; a single two-OID command must
	// instead validate both returned full OID lines independently.
	if slices.Contains(commands[2].NormalizedArgv, "--verify") ||
		!slices.Contains(commands[2].NormalizedArgv, "HEAD^{tree}") {
		t.Fatal("author uses an invalid multi-revision --verify command")
	}
}

func TestCorrectedWorkBudgetsCountCensusWatcherAndEveryRole(t *testing.T) {
	profile := retainedWorkPlan(t).Profile
	work, derivations, err := correctedWorkEnvelope(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(work.Phases) != len(derivations) || work.MaximumChildProcessesPerPhase <= 132 {
		t.Fatal("prospective lifetime budget retains the peak-shaped v1 ceiling")
	}
	var phaseMaximum uint64
	for index, value := range work.Phases {
		derivation := derivations[index]
		if derivation.Phase != value.Phase {
			t.Fatal("budget phase order differs")
		}
		gitMaximum, sumErr := sumChildBudgetTerms(derivation.GitTerms)
		if sumErr != nil || gitMaximum != derivation.MaximumGitChildren {
			t.Fatalf("%s Git formula: %d, %v", value.Phase, gitMaximum, sumErr)
		}
		var roleMaximum uint64
		for _, role := range value.ChildProcessRoles {
			roleMaximum += role.Maximum
			if role.Name == "git" && role.Maximum != gitMaximum {
				t.Fatalf("%s role does not match derived Git lifetimes", value.Phase)
			}
			if role.Name == "git-transport-shell" {
				want := uint64(len(derivation.SyncIntentSources)) * frozenExecutionRuntime(Plan{}).StoreRunnerMaxAttempts
				if role.Minimum != 0 || role.Maximum != want {
					t.Fatalf("%s transport shell epochs omitted or unbounded", value.Phase)
				}
				if want > 0 && (derivation.TransportShellTerm == nil || derivation.TransportShellTerm.MaximumChildren != want) ||
					want == 0 && derivation.TransportShellTerm != nil {
					t.Fatalf("%s transport shell formula differs from its role bound", value.Phase)
				}
			}
		}
		if roleMaximum != derivation.MaximumChildrenAllRoles {
			t.Fatalf("%s all-role total differs: %d != %d", value.Phase, roleMaximum, derivation.MaximumChildrenAllRoles)
		}
		phaseMaximum = max(phaseMaximum, roleMaximum)
		if value.Phase == "preflight" {
			if gitMaximum != 0 {
				t.Fatal("preflight invented a running watcher")
			}
			continue
		}
		watcherIndex := slices.IndexFunc(derivation.GitTerms, func(term ChildBudgetTerm) bool { return term.Name == "ordinary_watcher" })
		if watcherIndex < 0 {
			t.Fatalf("%s silently omitted the live watcher", value.Phase)
		}
		watcher := derivation.GitTerms[watcherIndex]
		if watcher.MaximumChildren != derivation.DeadlineMS/3_000 {
			t.Fatalf("%s counts watcher per epoch instead of per phase deadline", value.Phase)
		}
		marginIndex := slices.IndexFunc(derivation.GitTerms, func(term ChildBudgetTerm) bool {
			return term.Name == "delayed_watcher_tick_at_phase_boundary"
		})
		if value.Phase == "cold" {
			if marginIndex >= 0 {
				t.Fatal("cold invented an inherited watcher tick")
			}
		} else if marginIndex < 0 || derivation.GitTerms[marginIndex].MaximumChildren != 1 {
			t.Fatalf("%s omits or multiplies the single inherited delayed-tick margin", value.Phase)
		}
		if value.Phase == "logical_delta_b" {
			if value.GitReads != (CounterBound{}) || value.ResolverBlobReads != (CounterBound{}) || value.ResolverBlobBytes != (CounterBound{}) {
				t.Fatal("logical-only phase permits source-content or resolver rematerialization")
			}
			if value.CensusChildren != (CounterBound{Minimum: 1, Maximum: 1}) ||
				value.CensusRecords != (CounterBound{Minimum: profile.Physical.CombinedRegularFiles, Maximum: profile.Physical.CombinedRegularFiles}) ||
				gitMaximum <= watcher.MaximumChildren || derivation.ServerEpochStarts != 1 {
				t.Fatal("logical-only zero-work claim hides the actual census/startup work")
			}
		}
		if value.Phase == "archive_restore" && (value.CensusChildren != (CounterBound{}) || value.CensusRecords != (CounterBound{})) {
			t.Fatal("exact restored binding invented a catalog census")
		}
	}
	if work.MaximumChildProcessesPerPhase != phaseMaximum {
		t.Fatal("global child ceiling is not the maximum summed phase-role ceiling")
	}
}

func TestCorrectedWorkPreservesIndependentSafetyCeilings(t *testing.T) {
	profile := retainedWorkPlan(t).Profile
	before := frozenWorkEnvelope(profile)
	after, _, err := correctedWorkEnvelope(profile)
	if err != nil {
		t.Fatal(err)
	}
	// Only the new work schema, child lifetime total, and per-phase accounting
	// can differ. Every independent envelope cap and owner list remains exact.
	if !reflect.DeepEqual(after.ChildProcessRoles, slices.Insert(before.ChildProcessRoles, 1, "git-transport-shell")) {
		t.Fatal("native transport introduced an unexplained process role")
	}
	before.Schema, before.MaximumChildProcessesPerPhase, before.Phases = after.Schema, after.MaximumChildProcessesPerPhase, after.Phases
	before.ChildProcessRoles = after.ChildProcessRoles
	if !reflect.DeepEqual(before, after) {
		t.Fatal("prospective work correction widened an independent safety ceiling")
	}
}

func TestCorrectedNativeGitHelperInventoryIncludesActualTransportAlternatives(t *testing.T) {
	profile := retainedWorkPlan(t).Profile
	_, derivations, err := correctedWorkEnvelope(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, derivation := range derivations {
		var helperNames []string
		for _, term := range derivation.GitTerms {
			if slices.Contains([]string{
				"native_git_upload_pack", "native_git_pack_objects",
				"native_git_one_of_index_or_unpack_objects", "native_git_rev_list", "native_git_maintenance",
			}, term.Name) {
				helperNames = append(helperNames, term.Name)
				if term.Units != uint64(len(derivation.SyncIntentSources)) || term.ChildrenPerUnit != 1 || term.MaximumAttempts != 3 {
					t.Fatalf("%s helper %s lacks its per-sync-attempt derivation", derivation.Phase, term.Name)
				}
			}
			if term.Name == "native_git_index_pack" {
				t.Fatal("obsolete index-pack-only assumption excludes ordinary small fetches")
			}
		}
		wantCount := 0
		if len(derivation.SyncIntentSources) > 0 {
			wantCount = 5
		}
		if len(helperNames) != wantCount {
			t.Fatalf("%s native helpers = %v, want %d", derivation.Phase, helperNames, wantCount)
		}
	}
}

func TestCorrectedArchiveBudgetsActualRebuildDespiteSemanticIdentityReuse(t *testing.T) {
	plan := retainedWorkPlan(t)
	work, derivations, err := correctedWorkEnvelope(plan.Profile)
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(work.Phases, func(value PhaseWorkBounds) bool { return value.Phase == "archive_restore" })
	value, derivation := work.Phases[index], derivations[index]
	old := plan.WorkEnvelope.Phases[index]
	if old.GitReads.Maximum != 0 || old.ResolverBlobReads.Maximum != 0 {
		t.Fatal("retained archive counterexample changed")
	}
	for _, name := range []string{
		"resolver_blob_materialization", "caller_blob_materialization", "extraction_batch_reads",
		"candidate_commit_type", "candidate_tree_census",
	} {
		if !slices.ContainsFunc(derivation.GitTerms, func(term ChildBudgetTerm) bool { return term.Name == name }) {
			t.Fatalf("restore silently omitted %s", name)
		}
	}
	if slices.ContainsFunc(derivation.GitTerms, func(term ChildBudgetTerm) bool {
		return term.Name == "observation_batch_reads" || term.Name == "catalog_binding_census" || term.Name == "revision_author"
	}) {
		t.Fatal("restore invented observation/census/author work for retained exact inputs")
	}
	if value.ResolverBlobReads != (CounterBound{Minimum: 10_002, Maximum: 10_002}) ||
		value.ResolverBlobBytes != (CounterBound{Minimum: plan.Profile.Pipeline.ResolverBlobBytesPerBuild, Maximum: plan.Profile.Pipeline.ResolverBlobBytesPerBuild}) ||
		value.GitReads.Minimum == 0 || value.GitReads.Maximum != derivation.NativeSourceReadMaximum {
		t.Fatal("restored semantic identity reused despite uncounted native rematerialization")
	}
	for _, role := range value.ChildProcessRoles {
		if role.Name == "zoekt-git-index" && role.Maximum != 0 {
			t.Fatal("exact search archive invented a rebuild")
		}
	}
	full, fullErr := correctedNativeSourceReadMaximum(plan.Profile)
	if fullErr != nil || full-value.GitReads.Maximum != plan.Profile.Pipeline.SupportedGoFiles*frozenExecutionRuntime(Plan{}).GenerationMaxAttempts {
		t.Fatal("restore read formula does not exactly omit preserved Go observation work")
	}
}

func TestCorrectedWorkRejectsOverflowBeforeRetainedArithmetic(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CombinedProfile)
	}{
		{name: "regular plus partitions", mutate: func(profile *CombinedProfile) { profile.Physical.CombinedRegularFiles = math.MaxUint64 }},
		{name: "supported input sum", mutate: func(profile *CombinedProfile) { profile.Pipeline.SupportedIDLFiles = math.MaxUint64 }},
		{name: "service multiplier", mutate: func(profile *CombinedProfile) { profile.Logical.AcceptedServices = math.MaxUint64 }},
		{name: "native typed input sum", mutate: func(profile *CombinedProfile) { profile.Pipeline.ExtractionDomains[0].TypedPartitions = math.MaxUint64 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := retainedWorkPlan(t).Profile
			test.mutate(&profile)
			if _, _, err := correctedWorkEnvelope(profile); err == nil {
				t.Fatal("overflowing corrected work profile accepted")
			}
		})
	}
}
