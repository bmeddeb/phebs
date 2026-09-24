package t421

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/recovery"
)

func restoreContinuityTestPlan(t *testing.T) Plan {
	t.Helper()
	plan := pressureContinuityTestPlan(t)
	if err := applyPressureContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestCallerRestoreContinuityV5Derivation(t *testing.T) {
	prior := restoreContinuityTestPlan(t)
	priorRaw, err := MarshalCanonical(prior)
	if err != nil {
		t.Fatal(err)
	}
	next := prior // The correction must detach its mutable contract fields.
	if err := applyCallerRestoreContinuityCorrection(&next); err != nil {
		t.Fatal(err)
	}
	if next.Schema != PlanV5Schema || next.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV5Schema ||
		next.ReceiptContract.Schema != ReceiptV5Schema ||
		next.Correction.InspectionInventorySHA256 == prior.Correction.InspectionInventorySHA256 ||
		reflect.DeepEqual(next.Correction.IdentityDerivations, prior.Correction.IdentityDerivations) {
		t.Fatal("V5 did not version and bind the changed continuity contract")
	}
	restored := next
	restored.Schema, restored.ToolPolicy.ExecutionFreezeSchema, restored.ReceiptContract.Schema = prior.Schema, prior.ToolPolicy.ExecutionFreezeSchema, prior.ReceiptContract.Schema
	correction := *next.Correction
	correction.IdentityDerivations = prior.Correction.IdentityDerivations
	correction.InspectionInventorySHA256 = prior.Correction.InspectionInventorySHA256
	correction.ReadAccountingPolicy = prior.Correction.ReadAccountingPolicy
	correction.RequiredReadiness = prior.Correction.RequiredReadiness
	restored.Correction = &correction
	restored.WorkEnvelope = prior.WorkEnvelope
	if !reflect.DeepEqual(restored, prior) {
		t.Fatal("V5 changed an unrelated V4 plan field")
	}
	again, err := MarshalCanonical(prior)
	if err != nil || !bytes.Equal(again, priorRaw) {
		t.Fatal("V5 mutated retained V4 canonical bytes", err)
	}
	if err := validatePlanExecutionContract(next); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Plan){
		func(plan *Plan) {
			plan.Correction.InspectionInventorySHA256 = prior.Correction.InspectionInventorySHA256
		},
		func(plan *Plan) { plan.Correction.IdentityDerivations = prior.Correction.IdentityDerivations },
		func(plan *Plan) { plan.Correction.ReadAccountingPolicy = prior.Correction.ReadAccountingPolicy },
		func(plan *Plan) { plan.Correction.RequiredReadiness = prior.Correction.RequiredReadiness },
		func(plan *Plan) {
			plan.Correction.RequiredReadiness = plan.Correction.RequiredReadiness[:len(plan.Correction.RequiredReadiness)-1]
		},
	} {
		bad := next
		contract := *next.Correction
		bad.Correction = &contract
		mutate(&bad)
		if err := validatePlanExecutionContract(bad); err == nil {
			t.Fatal("V5 accepted a retained V4 continuity binding")
		}
	}
}

func TestCallerRestoreContinuityV5ArchiveInspectionInventory(t *testing.T) {
	prior := restoreContinuityTestPlan(t)
	oldRows, oldEpochs, err := planInspectionInventory(prior)
	if err != nil {
		t.Fatal(err)
	}
	retainedRows, retainedEpochs, err := correctedInspectionInventory(prior.Profile)
	if err != nil || !reflect.DeepEqual(oldRows, retainedRows) || !reflect.DeepEqual(oldEpochs, retainedEpochs) {
		t.Fatal("V4 inspection inventory changed", err)
	}
	next := prior
	if err := applyCallerRestoreContinuityCorrection(&next); err != nil {
		t.Fatal(err)
	}
	rows, epochs, err := planInspectionInventory(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(oldRows) || len(epochs) != len(oldEpochs) {
		t.Fatal("V5 inspection inventory changed its shape")
	}
	for index := range rows {
		if index != 11 && !reflect.DeepEqual(rows[index], oldRows[index]) {
			t.Fatalf("V5 changed %s inspection", rows[index].Phase)
		}
	}
	archive, oldArchive := rows[11], oldRows[11]
	if archive.Phase != "archive_restore" || archive.TailReadinessCalls.Maximum != 2_881 ||
		archive.TailControlFileReads != (CounterBound{Minimum: 7, Maximum: 7 * 2_881}) ||
		archive.TailStoreReadAttempts != (CounterBound{Minimum: 21, Maximum: 275 * 2_881}) {
		t.Fatal("V5 archive T limits differ", archive)
	}
	if strings.Contains(next.Correction.ReadAccountingPolicy, ";T-C=4;T-S=4;") ||
		!strings.Contains(next.Correction.ReadAccountingPolicy, ";T-default-C=4;T-default-S=4;T-archive-v5-C=7;T-archive-v5-S=[21,275];T-M=0;T-W=0;") {
		t.Fatal("V5 read-accounting policy contradicts selected archive T")
	}
	archive.TailControlFileReads, archive.TailStoreReadAttempts = oldArchive.TailControlFileReads, oldArchive.TailStoreReadAttempts
	if !reflect.DeepEqual(archive, oldArchive) {
		t.Fatal("V5 archive changed an unrelated inspection bound")
	}
	for index := range epochs {
		if index != 4 && !reflect.DeepEqual(epochs[index], oldEpochs[index]) {
			t.Fatalf("V5 changed epoch %d inventory", index+1)
		}
	}
	if epochs[4].TailControlFileReadsMaximum != oldEpochs[4].TailControlFileReadsMaximum+3*2_881 ||
		epochs[4].TailStoreReadAttemptsMaximum != oldEpochs[4].TailStoreReadAttemptsMaximum+271*2_881 ||
		epochs[4].AccountedServerRequestsMaximum != oldEpochs[4].AccountedServerRequestsMaximum {
		t.Fatal("V5 epoch-five T or request maximum differs", epochs[4])
	}
	epochs[4].TailControlFileReadsMaximum = oldEpochs[4].TailControlFileReadsMaximum
	epochs[4].TailStoreReadAttemptsMaximum = oldEpochs[4].TailStoreReadAttemptsMaximum
	if !reflect.DeepEqual(epochs[4], oldEpochs[4]) {
		t.Fatal("V5 epoch-five changed an unrelated inventory bound")
	}
	for index := range prior.WorkEnvelope.Phases {
		got, want := next.WorkEnvelope.Phases[index], prior.WorkEnvelope.Phases[index]
		if got.Phase == "archive_restore" {
			if got.ControlReads.Maximum != want.ControlReads.Maximum+274*2_881 {
				t.Fatal("V5 archive work ceiling did not include T", got.ControlReads.Maximum, want.ControlReads.Maximum)
			}
			got.ControlReads.Maximum = want.ControlReads.Maximum
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("V5 changed unrelated work bound in %s", got.Phase)
		}
	}
	if err := validatePlanExecutionContract(next); err != nil {
		t.Fatal(err)
	}
}

func TestCallerRestoreContinuityV5RequiresCompleteV4(t *testing.T) {
	if err := applyCallerRestoreContinuityCorrection(nil); err == nil {
		t.Fatal("nil plan acquired V5 authority")
	}
	for _, test := range []struct {
		name   string
		mutate func(*Plan)
	}{
		{"schema", func(plan *Plan) { plan.Schema = PlanV3Schema }},
		{"freeze", func(plan *Plan) { plan.ToolPolicy.ExecutionFreezeSchema = ExecutionFreezeV3Schema }},
		{"receipt", func(plan *Plan) { plan.ReceiptContract.Schema = ReceiptV3Schema }},
		{"correction", func(plan *Plan) { plan.Correction = nil }},
		{"accounting", func(plan *Plan) { plan.ProcessAccounting = nil }},
		{"logical", func(plan *Plan) { plan.LogicalStoreWork = nil }},
		{"selector", func(plan *Plan) { plan.SelectorHandoffCleanup = nil }},
		{"policy", func(plan *Plan) { plan.MeterPolicy.LifecycleSemantics += ";changed" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := restoreContinuityTestPlan(t)
			test.mutate(&plan)
			if err := applyCallerRestoreContinuityCorrection(&plan); err == nil {
				t.Fatal("incomplete V4 acquired V5 authority")
			}
		})
	}
	plan := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	if err := applyCallerRestoreContinuityCorrection(&plan); err == nil {
		t.Fatal("V5 correction applied twice")
	}
}

func TestCallerRestoreContinuityV5FrozenRoundTrip(t *testing.T) {
	plan, err := BuildPlanV5(testSourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(raw)
	if err != nil || !reflect.DeepEqual(decoded, plan) || bytes.Count(raw, []byte{'\n'}) != 1 {
		t.Fatal("V5 canonical plan round trip failed", err)
	}
	fixture := newExecutionFreezeCandidateTestFixtureForPlan(t, plan)
	freezeRaw, err := fixture.assemble()
	if err != nil {
		t.Fatal(err)
	}
	freeze, err := fixture.validate(freezeRaw)
	if err != nil || freeze.Schema != ExecutionFreezeV5Schema {
		t.Fatal("V5 candidate freeze round trip failed", err)
	}
	for _, value := range []any{Receipt{Schema: ReceiptV5Schema}, &Receipt{Schema: ReceiptV5Schema}} {
		encoded, err := MarshalCanonical(value)
		if err != nil || bytes.Count(encoded, []byte{'\n'}) != 1 {
			t.Fatal("V5 receipt lost compact canonical routing", err)
		}
	}
}

func TestCallerRestoreContinuityV5AuthorityFences(t *testing.T) {
	prior := AuthorityPhaseResult{Phase: "pressure_75", Outcome: "passed", AuthorityState: AuthorityState{
		Current: true, PhysicalCommit: "source", CallerGenerationSHA256: testDigest("caller"), CallerRootSHA256: testDigest("old caller root"),
		CallerContinuitySHA256: testDigest("caller content"), ResolverCatalogGenerationSHA256: testDigest("resolver"), ResolverCatalogRootSHA256: testDigest("resolver root"),
		RelationshipGenerationSHA256: testDigest("relationship"), RelationshipRootSHA256: testDigest("relationship root"), RelationshipProvenanceSHA256: testDigest("provenance"),
	}}
	for _, mode := range []string{"same", "rebuilt", "caller_generation", "caller_content", "missing_content", "invalid_content", "resolver_generation", "resolver_root", "relationship_root", "provenance_only", "physical", "not_current", "prior_not_current"} {
		t.Run(mode, func(t *testing.T) {
			before, current := prior, withPhase(prior, "archive_restore")
			if mode != "same" {
				current.CallerRootSHA256 = testDigest("new caller root")
				current.RelationshipGenerationSHA256, current.RelationshipRootSHA256, current.RelationshipProvenanceSHA256 = testDigest("new relationship"), testDigest("new relationship root"), testDigest("new provenance")
			}
			switch mode {
			case "caller_generation":
				current.CallerGenerationSHA256 = testDigest("other caller")
			case "caller_content":
				current.CallerContinuitySHA256 = testDigest("changed pair receipt or content")
			case "missing_content":
				current.CallerContinuitySHA256, before.CallerContinuitySHA256 = "", ""
			case "invalid_content":
				current.CallerContinuitySHA256, before.CallerContinuitySHA256 = "invalid", "invalid"
			case "resolver_generation":
				current.ResolverCatalogGenerationSHA256 = testDigest("other resolver")
			case "resolver_root":
				current.ResolverCatalogRootSHA256 = testDigest("other resolver root")
			case "relationship_root":
				current.RelationshipRootSHA256 = before.RelationshipRootSHA256
			case "provenance_only":
				current.RelationshipGenerationSHA256, current.RelationshipRootSHA256 = before.RelationshipGenerationSHA256, before.RelationshipRootSHA256
			case "physical":
				current.PhysicalCommit = "other source"
			case "not_current":
				current.Current = false
			case "prior_not_current":
				before.Current = false
			}
			if err := validateArchiveAuthorityContinuity(current, before, Plan{Schema: PlanV5Schema}); (err == nil) != (mode == "same" || mode == "rebuilt") {
				t.Fatal("V5 archive continuity fence", err)
			}
		})
	}
	prior.CallerContinuitySHA256 = ""
	current := withPhase(prior, "archive_restore")
	current.CallerRootSHA256 = testDigest("new caller root")
	for _, schema := range []string{PlanV2Schema, PlanV3Schema, PlanV4Schema} {
		if err := validateArchiveAuthorityContinuity(current, prior, Plan{Schema: schema}); err == nil {
			t.Fatal("historical archive accepted changed caller root", schema)
		}
	}
}

func TestCallerRestoreContinuityV5TailVersionAndPhaseFences(t *testing.T) {
	prior := tailReadinessIdentity{RelationshipGenerationSHA256: testDigest("relationship"), RelationshipRootSHA256: testDigest("relationship root"),
		CallerGenerationSHA256: testDigest("caller"), CallerRootSHA256: testDigest("caller root")}
	current := prior
	current.CallerRootSHA256 = testDigest("new caller root")
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema, PlanV4Schema, PlanV5Schema} {
		for _, phase := range []string{"archive_restore", "warm_noop", "stale_lease", "process_restart", "pressure_75", "lifecycle_collection", "product_queries"} {
			ready, err := planTailReadinessTransitionReady(schema, phase, &prior, current)
			if err != nil || ready != (schema == PlanV5Schema && phase == "archive_restore") {
				t.Fatalf("%s %s changed-root readiness = %t: %v", schema, phase, ready, err)
			}
		}
	}
	current.CallerGenerationSHA256 = testDigest("other caller")
	if ready, err := planTailReadinessTransitionReady(PlanV5Schema, "archive_restore", &prior, current); err != nil || ready {
		t.Fatal("V5 tail accepted changed caller generation", err)
	}
}

func TestCallerRestoreContinuityHistoricalOmissionAndRefusal(t *testing.T) {
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema, PlanV4Schema, PlanV5Schema} {
		t.Run(schema, func(t *testing.T) {
			value := AuthorityPhaseResult{Phase: "cold", Outcome: "not_run"}
			validate := func() error {
				return validateAuthorityResults([]AuthorityPhaseResult{value}, []string{"cold"}, map[string]string{"cold": "not_run"}, nil, nil, Plan{Schema: schema})
			}
			if err := validate(); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(value)
			if err != nil || bytes.Contains(raw, []byte("caller_continuity_sha256")) {
				t.Fatal("empty continuity changed historical wire bytes", err)
			}
			value.CallerContinuitySHA256 = testDigest("unexpected continuity")
			if err := validate(); err == nil {
				t.Fatal("not-run or historical authority accepted prospective continuity")
			}
		})
	}
}

// The native commitment is modeled here; callerpublication tests prove its
// actual derivation. This verifies live F decoding and downstream handoff fences.
func TestCallerRestoreContinuityV5FinalAndDownstream(t *testing.T) {
	base, native := epochTestFinal(t)
	plan := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&plan); err != nil {
		t.Fatal(err)
	}
	physical := plan.Revisions.Physical[2]
	projection, err := expectedStateProjectionForPhase(plan, "archive_restore")
	if err != nil {
		t.Fatal(err)
	}
	native.Authority.PhysicalCommit, native.Authority.PhysicalTree = physical.ExpectedCommit, physical.ExpectedTree
	native.Authority.CallerContinuitySHA256 = testDigest("caller content")
	var state AuthorityState
	if err := json.Unmarshal(epochTestJSON(t, native.Authority, false), &state); err != nil {
		t.Fatal(err)
	}
	native.ExtractionRoots = testExtractionRoots(t, base.plan, physical, state, "restore-continuity-fixture")
	for index := range native.ExtractionRoots {
		native.ExtractionRoots[index].ScheduleSHA256 = ""
		native.ExtractionRoots[index].Members, err = extractionResultMembers(native.ExtractionRoots[index].PartitionResults)
		if err != nil {
			t.Fatal(err)
		}
	}
	native.Authority.ExtractionRootsSHA256 = mustReceiptSHA256(t, native.ExtractionRoots)
	if err := json.Unmarshal(epochTestJSON(t, native.Authority, false), &state); err != nil {
		t.Fatal(err)
	}
	state.PhysicalRevision, state.LogicalRevision = "a-return", "a-return"
	prior := AuthorityPhaseResult{Phase: "pressure_75", Outcome: "passed", AuthorityState: state, ExtractionRoots: native.ExtractionRoots}
	native.Authority.CallerRootSHA256 = testDigest("rebuilt caller root")
	native.Authority.RelationshipGenerationSHA256, native.Authority.RelationshipRootSHA256, native.Authority.RelationshipProvenanceSHA256 = testDigest("rebuilt relationship"), testDigest("rebuilt relationship root"), testDigest("rebuilt provenance")
	reader := &executionEpochInspection{run: &ExecutionEpochOneRun{epoch: ExecutionEpochConfig{Epoch: 5}}, plan: plan,
		projection: projection, archivePrior: prior, archiveManifest: &recovery.ArchiveTransitionManifest{},
		tail: epochTailReadiness{Status: "ready", RelationshipGenerationSHA256: native.Authority.RelationshipGenerationSHA256,
			RelationshipRootSHA256: native.Authority.RelationshipRootSHA256, CallerGenerationSHA256: native.Authority.CallerGenerationSHA256, CallerRootSHA256: native.Authority.CallerRootSHA256}}
	for _, phase := range []string{"archive_restore", "lifecycle_collection", "product_queries"} {
		reader.projection.Phase = phase
		if err := json.Unmarshal(epochTestJSON(t, reader.projection, false), &native.Projection); err != nil {
			t.Fatal(err)
		}
		native.Projection.Schema = "t421-final-state-projection-source-free-v1"
		if phase == "product_queries" {
			native.QueryAuthority = &epochQueryAuthority{CatalogSourceGenerationSHA256: testDigest("catalog source"), ResolverNamespaceGenerationSHA256: testDigest("namespace"), ResolverNamespaceRootSHA256: testDigest("namespace root")}
		}
		result, finalProjection, err := reader.decodeFinal(epochTestJSON(t, native, true))
		if err != nil || result.CallerRootSHA256 != native.Authority.CallerRootSHA256 || result.CallerContinuitySHA256 != state.CallerContinuitySHA256 {
			t.Fatalf("%s did not retain actual rebuilt root and continuity: %v", phase, err)
		}
		final := ExecutionInspectionFinal{CatalogPopulation: reader.finalCatalogPopulation, CallerPublication: reader.finalCallerPublication,
			ResolverCatalogCounts: reader.finalResolverCatalogCounts, RPCPostings: reader.finalRPCPostings}
		rebuilt, err := epochFinalBody(result.AuthorityState, result.ExtractionRoots, finalProjection, final, native.QueryAuthority)
		if err != nil || !bytes.Equal(rebuilt, epochTestJSON(t, native, true)) {
			t.Fatalf("%s V5 final-body re-encoding changed native bytes: %v", phase, err)
		}
		for _, mode := range []string{"missing", "invalid", "changed", "changed_generation", "changed_root"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				bad := native
				changedReader := &executionEpochInspection{run: reader.run, plan: reader.plan, projection: reader.projection, tail: reader.tail,
					archivePrior: reader.archivePrior, archiveManifest: reader.archiveManifest, archiveAuthority: reader.archiveAuthority,
					collectionAuthority: reader.collectionAuthority, restoredSamples: reader.restoredSamples}
				switch mode {
				case "missing":
					bad.Authority.CallerContinuitySHA256 = ""
				case "invalid":
					bad.Authority.CallerContinuitySHA256 = "invalid"
				case "changed":
					bad.Authority.CallerContinuitySHA256 = testDigest("other content")
				case "changed_generation":
					bad.Authority.CallerGenerationSHA256 = testDigest("other generation")
					changedReader.tail.CallerGenerationSHA256 = bad.Authority.CallerGenerationSHA256
				case "changed_root":
					if phase == "archive_restore" {
						return // Archive alone permits a provenance-only root change.
					}
					bad.Authority.CallerRootSHA256 = testDigest("later root")
					changedReader.tail.CallerRootSHA256 = bad.Authority.CallerRootSHA256
				}
				if _, _, err := changedReader.decodeFinal(epochTestJSON(t, bad, true)); err == nil {
					t.Fatal("changed final authority passed")
				}
			})
		}
		reader.archiveAuthority, reader.collectionAuthority = withPhase(result, "archive_restore"), withPhase(result, "lifecycle_collection")
		reader.restoredSamples.ArchiveComplete, reader.restoredSamples.CollectionComplete = true, true
	}
}

func TestActiveExecutionPlanIsV5(t *testing.T) {
	plan, err := buildActiveExecutionPlan(testSourceCommit)
	if err != nil || plan.Schema != activeExecutionPlanSchema ||
		plan.ToolPolicy.ExecutionFreezeSchema != ExecutionFreezeV5Schema || plan.ReceiptContract.Schema != ReceiptV5Schema {
		t.Fatalf("active execution plan = %q/%q/%q, %v", plan.Schema, plan.ToolPolicy.ExecutionFreezeSchema, plan.ReceiptContract.Schema, err)
	}
}
