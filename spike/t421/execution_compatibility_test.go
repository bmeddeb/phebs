package t421

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestExecutionCompatibilityLegacyConfigBytes(t *testing.T) {
	// Captured from the unchanged V1/V2 constructor before adding the optional
	// field. This pins the complete serialized config, not only its omission.
	for _, test := range []struct{ schema, digest string }{
		{PlanSchema, "sha256:c936e1c6f7343f78c514ef0be4f523a10e64713db162bf9d1bc53442ba227e36"},
		{PlanV2Schema, "sha256:4a9719e7f8ae35bd4c7c66195b3d8106e6bd581f128e7647b49b0dc043d72c85"},
	} {
		value := frozenExecutionConfig(Plan{Schema: test.schema}, SHA256([]byte("compatibility-profile-fixture")))
		raw, err := json.Marshal(value)
		if err != nil || value.CompatibilityPosture != "" || bytes.Contains(raw, []byte("compatibility_posture")) || SHA256(raw) != test.digest {
			t.Fatal("retained compatibility config bytes changed", test.schema, err)
		}
	}
}

func TestExecutionCompatibilityV3ProfileBindsUnavailablePosture(t *testing.T) {
	plan := accountingTestPlan(t)
	tools, host := executionFreezeTestTools(plan, executionFreezeTestCommits()), executionFreezeTestHost()
	admission := executionProfileTestAdmission(t, plan, tools, host)
	profile, err := expectedExecutionProfile(plan, tools, host, admission)
	if err != nil || profile.Config.CompatibilityPosture != "unavailable-no-validation-zero-budget-v1" {
		t.Fatal("V3 profile omitted the selected unavailable compatibility posture", err)
	}
	raw, err := json.Marshal(profile.Config)
	if err != nil || !bytes.Contains(raw, []byte(`"compatibility_posture":"unavailable-no-validation-zero-budget-v1"`)) ||
		validateExecutionProfile(profile, plan, tools, host, admission) != nil {
		t.Fatal("V3 posture was not serialized and admitted by the existing validator", err)
	}
	for _, posture := range []string{"", "validated-native-sandbox"} {
		changed, binding := profile, admission
		changed.Config.CompatibilityPosture = posture
		changed.Config.ProjectionSHA256, err = executionConfigProjectionSHA256(changed.Config)
		if err != nil {
			t.Fatal(err)
		}
		binding.configProjectionSHA256 = changed.Config.ProjectionSHA256
		changed.InvocationSHA256, err = executionInvocationSHA256(changed, tools)
		if err != nil {
			t.Fatal(err)
		}
		binding.invocationSHA256 = changed.InvocationSHA256
		binding.profileSHA256, err = canonicalSHA256(changed)
		if err != nil {
			t.Fatal(err)
		}
		if validateExecutionProfile(changed, plan, tools, host, binding) == nil {
			t.Fatal("changed V3 compatibility posture self-admitted through recomputed digests")
		}
	}
}
