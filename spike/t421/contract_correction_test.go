package t421

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

var correctedPlanOnce sync.Once
var correctedPlan Plan
var correctedPlanErr error

func correctedTestPlan(t *testing.T) Plan {
	t.Helper()
	correctedPlanOnce.Do(func() { correctedPlan, correctedPlanErr = BuildPlan(testSourceCommit) })
	if correctedPlanErr != nil {
		t.Fatal(correctedPlanErr)
	}
	return correctedPlan
}

func TestCorrectionPreservesRetainedV1BytesAndValidation(t *testing.T) {
	raw, err := os.ReadFile("plan.json")
	if err != nil {
		t.Fatal(err)
	}
	if SHA256(raw) != retainedPlanSHA256 {
		t.Fatal("retained freeze changed")
	}
	plan, err := DecodePlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != PlanSchema || plan.Correction != nil {
		t.Fatal("retained v1 gained prospective fields")
	}
	reencoded, err := MarshalCanonical(plan)
	if err != nil || !bytes.Equal(raw, reencoded) {
		t.Fatal("retained canonical bytes changed")
	}
}

func TestCorrectionSupersedesWithoutChangingCorpusOrSafety(t *testing.T) {
	prior, next := frozenTestPlan(t), correctedTestPlan(t)
	priorOracle, nextOracle := prior.Oracle, next.Oracle
	priorOracle.QueryCases, nextOracle.QueryCases = nil, nil
	if next.Schema != PlanV2Schema || next.Correction == nil || next.Correction.SupersedesSHA256 != retainedPlanSHA256 {
		t.Fatal("prospective contract lacks exact supersession")
	}
	if !reflect.DeepEqual(prior.SafetyEnvelope, next.SafetyEnvelope) || !reflect.DeepEqual(priorOracle, nextOracle) ||
		!reflect.DeepEqual(prior.Profile.Physical, next.Profile.Physical) || !reflect.DeepEqual(prior.Profile.Pipeline, next.Profile.Pipeline) ||
		next.Claims.ChangesProductionBehavior || next.Claims.AuthorizesExecution {
		t.Fatal("correction changed corpus, safety, production behavior, or execution authority")
	}
	for index, revision := range next.Revisions.Physical {
		old := prior.Revisions.Physical[index]
		if revision.ExpectedTree != old.ExpectedTree || revision.ExpectedCommit != old.ExpectedCommit ||
			!reflect.DeepEqual(revision.ExpectedCandidateInventories, old.ExpectedCandidateInventories) ||
			revision.ExpectedObservationInputInventory.Records != next.Profile.Pipeline.SupportedGoFiles ||
			revision.ExpectedObservationInputInventory == old.ExpectedObservationInputInventory {
			t.Fatal("Go-only observation correction changed source/candidate authority or retained the IDL error")
		}
	}
	if next.Profile.Bytes.CombinedObservationInputBytes+next.Profile.Bytes.OverlayIDLBytes != prior.Profile.Bytes.CombinedObservationInputBytes {
		t.Fatal("observation byte correction is not exactly the IDL exclusion")
	}
	raw, err := MarshalCanonical(next)
	if err != nil || len(raw) > MaxPlanBytes {
		t.Fatalf("corrected plan bytes=%d err=%v", len(raw), err)
	}
	decoded, err := DecodePlan(raw)
	if err != nil || !reflect.DeepEqual(next, decoded) {
		t.Fatalf("corrected round trip: %v", err)
	}
}

func TestCorrectionUsesProductionCurrentPriorReaderContract(t *testing.T) {
	prior, next := frozenTestPlan(t), correctedTestPlan(t)
	if prior.ReaderProbe.PostDeleteOutcome != "not_found" || prior.ReaderProbe.PostReleaseOutcome != "" ||
		next.ReaderProbe.Schema != "t422-revision-reader-probe-v2" ||
		next.ReaderProbe.Reader != "search-generation-current-prior-content-probe-v2" ||
		next.ReaderProbe.PostDeleteOutcome != "" || next.ReaderProbe.OldRoleAfterReplacement != "prior" ||
		next.ReaderProbe.NewRoleAfterReplacement != "current" || next.ReaderProbe.PostReleaseOutcome != "retained_prior" {
		t.Fatal("corrected reader contract does not describe the production current/prior root")
	}
	index := slices.IndexFunc(next.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
		return value.Phase == "physical_delta_b"
	})
	priorIndex := slices.IndexFunc(prior.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
		return value.Phase == "physical_delta_b"
	})
	readBound, err := correctedPhysicalTransitionReadBound(next.Profile)
	logicalReadBound, logicalErr := correctedLogicalTransitionReadBound()
	returnReadBound, returnErr := correctedReturnTransitionReadBound()
	staleReadBound, staleErr := correctedStaleLeaseTransitionReadBound()
	restartReadBound, restartErr := correctedCheckpointRestartReadBound()
	if index < 0 || next.WorkEnvelope.Phases[index].LifecycleOwnerTurns != (CounterBound{Minimum: 2, Maximum: 2}) ||
		priorIndex < 0 || err != nil || logicalErr != nil || returnErr != nil || staleErr != nil || restartErr != nil || next.WorkEnvelope.Phases[index].LifecycleDeleted != (CounterBound{}) ||
		next.WorkEnvelope.Phases[index].ControlReads != prior.WorkEnvelope.Phases[priorIndex].ControlReads ||
		next.WorkEnvelope.Phases[index].MemberReads != prior.WorkEnvelope.Phases[priorIndex].MemberReads ||
		readBound.ControlFileReads != exactInspectionCalls(41) || readBound.MemberReads != exactInspectionCalls(4_063_208) ||
		logicalReadBound.Calls != exactInspectionCalls(2) || logicalReadBound.StoreReadAttempts != exactInspectionCalls(10) ||
		returnReadBound.Calls != exactInspectionCalls(2) || returnReadBound.ControlFileReads != exactInspectionCalls(10) ||
		staleReadBound.Calls != exactInspectionCalls(2) || staleReadBound.ControlFileReads != exactInspectionCalls(8) || staleReadBound.StoreReadAttempts != exactInspectionCalls(8) ||
		restartReadBound.Calls != exactInspectionCalls(2) || restartReadBound.ControlFileReads != exactInspectionCalls(14) || restartReadBound.StoreReadAttempts != exactInspectionCalls(8) ||
		!strings.Contains(next.Correction.ReadAccountingPolicy, "R-physical-C=17+1+3+17+3=41") ||
		!strings.Contains(next.Correction.ReadAccountingPolicy, "R-physical-M=2*physical.combined_physical_owners") ||
		!strings.Contains(next.Correction.ReadAccountingPolicy, "R-logical-S=2*(selector+plan+schedule+unit+selector-confirm)=10") ||
		!strings.Contains(next.Correction.ReadAccountingPolicy, "R:r2C5;s2C4S4;p2C7S4") {
		t.Fatal("corrected transition reader work differs from the production contracts")
	}
}

func TestCorrectionUsesProductionShapedHiddenServiceSearch(t *testing.T) {
	prior, next := frozenTestPlan(t), correctedTestPlan(t)
	find := func(plan Plan) QueryCase {
		t.Helper()
		index := slices.IndexFunc(plan.Oracle.QueryCases, func(value QueryCase) bool {
			return value.Name == "hidden_repository_denied"
		})
		if index < 0 {
			t.Fatal("hidden-repository query case is absent")
		}
		return plan.Oracle.QueryCases[index]
	}
	old, corrected := find(prior), find(next)
	if !strings.Contains(old.HTTP.Path, "scope=all_code&repository=") ||
		corrected.Surface != "service_search" ||
		!strings.Contains(corrected.HTTP.Path, "scope=service&repository=$hidden_repository&service_key=") ||
		corrected.ExpectedStatus != 404 || corrected.ExpectedMCPCode != "unknown_repository" {
		t.Fatalf("hidden query correction = %+v", corrected)
	}
	expected := expectedQueryResult(corrected, next.ReceiptContract.QueryTransportSchema, zeroDigest(), next.Schema)
	for _, transport := range []QueryTransportResult{expected.HTTP, expected.MCP} {
		if transport.ControlReads != 1 || transport.MemberReads != 0 {
			t.Fatalf("hidden service-search native reads = %+v", transport)
		}
	}
}

func TestCorrectionAllowsExactZeroMemberWarmProductResult(t *testing.T) {
	plan := correctedTestPlan(t)
	queryIndex := slices.IndexFunc(plan.Oracle.QueryCases, func(value QueryCase) bool {
		return value.Name == "unowned_excluded_from_service_scope"
	})
	workIndex := slices.IndexFunc(plan.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
		return value.Phase == "product_queries"
	})
	if queryIndex < 0 || workIndex < 0 {
		t.Fatal("corrected warm-empty product case or work bound is absent")
	}
	query := plan.Oracle.QueryCases[queryIndex]
	result := expectedQueryResult(query, plan.ReceiptContract.QueryTransportSchema, zeroDigest(), plan.Schema)
	result.HTTP.MemberReads, result.MCP.MemberReads = 0, 0
	if err := validateQueryResult(
		result, query, plan.ReceiptContract.QueryTransportSchema, zeroDigest(),
		plan.WorkEnvelope.Phases[workIndex], plan.Schema,
	); err != nil {
		t.Fatalf("warm empty product result was rejected: %v", err)
	}
	result.HTTP.ControlReads = 0
	if err := validateQueryResult(
		result, query, plan.ReceiptContract.QueryTransportSchema, zeroDigest(),
		plan.WorkEnvelope.Phases[workIndex], plan.Schema,
	); err == nil {
		t.Fatal("warm empty product result without a control read was accepted")
	}
}

func TestCorrectionRequiresOnlyNecessaryQueryMemberReads(t *testing.T) {
	plan := correctedTestPlan(t)
	workIndex := slices.IndexFunc(plan.WorkEnvelope.Phases, func(value PhaseWorkBounds) bool {
		return value.Phase == "product_queries"
	})
	if workIndex < 0 {
		t.Fatal("product-query work bound is absent")
	}
	validate := func(name string, mutate func(*QueryResult), wantOK bool) {
		t.Helper()
		index := slices.IndexFunc(plan.Oracle.QueryCases, func(value QueryCase) bool { return value.Name == name })
		if index < 0 {
			t.Fatalf("query %q is absent", name)
		}
		query := plan.Oracle.QueryCases[index]
		result := expectedQueryResult(query, plan.ReceiptContract.QueryTransportSchema, zeroDigest(), plan.Schema)
		mutate(&result)
		err := validateQueryResult(result, query, plan.ReceiptContract.QueryTransportSchema, zeroDigest(), plan.WorkEnvelope.Phases[workIndex], plan.Schema)
		if (err == nil) != wantOK {
			t.Fatalf("query %q member shape accepted=%t, want %t: %v", name, err == nil, wantOK, err)
		}
	}
	validate("first_service", func(*QueryResult) {}, true)
	validate("first_service", func(value *QueryResult) { value.HTTP.MemberReads = 0 }, false)
	validate("first_service", func(value *QueryResult) { value.MCP.MemberReads = 1 }, false)
	validate("chain_dependency", func(value *QueryResult) { value.HTTP.MemberReads = 0 }, false)
}

func TestCorrectionDerivesProductMembersFromQueryResults(t *testing.T) {
	plan := correctedTestPlan(t)
	if plan.Correction == nil ||
		!strings.Contains(plan.Correction.ReadAccountingPolicy, ";Q-M=checked-sum-plan-order(query_results.results[*].http.member_reads+query_results.results[*].mcp.member_reads);Q-W=0;") ||
		strings.Count(plan.Correction.ReadAccountingPolicy, ";Q-M=") != 1 {
		t.Fatalf("product member accounting is not exact: %+v", plan.Correction)
	}
}

func TestCorrectedEpochConfigAdmission(t *testing.T) {
	plan := Plan{Schema: PlanV2Schema, PhaseStates: correctedPhaseStates()}
	plan.SafetyEnvelope.ServerHealthDeadlineMS = 15 * 60 * 1000
	for _, name := range []string{"a", "b", "a-return"} {
		plan.Revisions.Logical = append(plan.Revisions.Logical, LogicalRevision{
			Name: name, CatalogSource: CatalogSourceProfile{SHA256: SHA256([]byte(name))},
		})
	}
	configs := []string{"a", "b", "a-return", "a-return", "a-return"}
	for index := range configs {
		configs[index] = SHA256([]byte("config/" + configs[index]))
	}
	admission := ExecutionProfileAdmissionBinding{epochConfigBytesSHA256: configs}
	admission.configBytesSHA256, _ = canonicalSHA256(configs)
	epochs, err := admittedExecutionServerEpochs(plan, admission)
	if err != nil || len(epochs) != len(configs) {
		t.Fatalf("derived epoch admission: %v", err)
	}
	for index, epoch := range epochs {
		if epoch.ServerEpoch != uint64(index+1) || epoch.ConfigBytesSHA256 != configs[index] ||
			epoch.ServerHealthDeadlineMS != plan.SafetyEnvelope.ServerHealthDeadlineMS || !validDigest(epoch.CatalogSourceSHA256) {
			t.Fatal("epoch lacks its exact config/catalog/startup binding")
		}
	}
	for name, mutate := range map[string]func(*ExecutionProfileAdmissionBinding){
		"missing_epoch": func(v *ExecutionProfileAdmissionBinding) { v.epochConfigBytesSHA256 = v.epochConfigBytesSHA256[:4] },
		"unbound_set":   func(v *ExecutionProfileAdmissionBinding) { v.configBytesSHA256 = configs[0] },
		"invalid_bytes": func(v *ExecutionProfileAdmissionBinding) { v.epochConfigBytesSHA256[1] = "" },
		"unchanged_config_for_new_catalog": func(v *ExecutionProfileAdmissionBinding) {
			v.epochConfigBytesSHA256[1] = v.epochConfigBytesSHA256[0]
			v.configBytesSHA256, _ = canonicalSHA256(v.epochConfigBytesSHA256)
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := admission
			value.epochConfigBytesSHA256 = slices.Clone(configs)
			mutate(&value)
			if _, err := admittedExecutionServerEpochs(plan, value); err == nil {
				t.Fatal("invalid epoch config accepted")
			}
		})
	}
	if _, err := admittedExecutionServerEpochs(Plan{Schema: PlanSchema}, admission); err == nil {
		t.Fatal("retained v1 accepted prospective config epochs")
	}
	old := frozenExecutionConfig(Plan{Schema: PlanSchema}, configs[0])
	next := frozenExecutionConfig(plan, admission.configBytesSHA256)
	if old.Schema == next.Schema || old.Policy == next.Policy {
		t.Fatal("config-set digest is mislabeled as one raw config digest")
	}
}

func TestCorrectedExecutionEnvironmentEnablesOnlyTheProspectiveExactReader(t *testing.T) {
	admission := ExecutionProfileAdmissionBinding{}
	variable := "PHEBS_T421_EXACT_READS=source-free-v1"
	retained := frozenExecutionEnvironment(Plan{Schema: PlanSchema}, admission)
	corrected := frozenExecutionEnvironment(Plan{Schema: PlanV2Schema}, admission)
	if slices.Contains(retained.ServerVariables, variable) ||
		!slices.Contains(corrected.ServerVariables, variable) ||
		len(corrected.ServerVariables) != len(retained.ServerVariables)+1 {
		t.Fatal("exact read mode leaked into V1 or is absent from V2")
	}
}

func TestCorrectionVersionsThePrivateExactSemanticReader(t *testing.T) {
	prior, next := frozenTestPlan(t), correctedTestPlan(t)
	if prior.ReceiptContract.StateObservationSchema != "t422-observed-phase-state-v4" ||
		next.ReceiptContract.StateObservationSchema != "t422-observed-phase-state-v5" ||
		semanticReaderForObservationSchema(prior.ReceiptContract.StateObservationSchema) != "authorized-product-reader-canonical-projection-v1" ||
		semanticReaderForObservationSchema(next.ReceiptContract.StateObservationSchema) != "private-exact-current-authorized-canonical-projection-v1" {
		t.Fatal("semantic reader is not versioned across retained and corrected contracts")
	}
}
