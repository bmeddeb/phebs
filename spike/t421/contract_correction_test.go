package t421

import (
	"bytes"
	"os"
	"reflect"
	"slices"
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
	if next.Schema != PlanV2Schema || next.Correction == nil || next.Correction.SupersedesSHA256 != retainedPlanSHA256 {
		t.Fatal("prospective contract lacks exact supersession")
	}
	if !reflect.DeepEqual(prior.SafetyEnvelope, next.SafetyEnvelope) || !reflect.DeepEqual(prior.Oracle, next.Oracle) ||
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
