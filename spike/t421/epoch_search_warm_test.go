package t421

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestEpochSearchWarmObservation(t *testing.T) {
	final := AuthorityPhaseResult{AuthorityState: AuthorityState{SearchGenerationSHA256: testDigest("selected-search")}}
	valid := epochSearchWarmObservation{Schema: SearchWarmSchema, Phase: 14, SharedValidated: true,
		SelectedSearchGenerationSHA256: final.SearchGenerationSHA256, ElapsedMS: 42180}
	raw := epochTestJSON(t, valid, false)
	decoded, ok := decodeEpochSearchWarm(raw)
	if !ok || decoded != valid || !validEpochSearchWarm(decoded, final) {
		t.Fatalf("valid warm = %+v %v", decoded, ok)
	}
	exact := valid
	exact.SharedValidated, exact.SharedExactSHA256 = false, testDigest("shared-exact")
	if !validEpochSearchWarm(exact, final) {
		t.Fatal("exact all-code fallback was refused")
	}
	for name, mutate := range map[string]func(*epochSearchWarmObservation){
		"schema":            func(value *epochSearchWarmObservation) { value.Schema = "t422-search-warm-v0" },
		"phase":             func(value *epochSearchWarmObservation) { value.Phase = 13 },
		"selected differs":  func(value *epochSearchWarmObservation) { value.SelectedSearchGenerationSHA256 = testDigest("other") },
		"selected invalid":  func(value *epochSearchWarmObservation) { value.SelectedSearchGenerationSHA256 = "sha256:short" },
		"both shared forms": func(value *epochSearchWarmObservation) { value.SharedExactSHA256 = testDigest("shared-exact") },
		"no shared form":    func(value *epochSearchWarmObservation) { value.SharedValidated = false },
		"bad shared exact": func(value *epochSearchWarmObservation) {
			value.SharedValidated, value.SharedExactSHA256 = false, "sha256:short"
		},
		"over product bound": func(value *epochSearchWarmObservation) { value.ElapsedMS = 10*60*1000 + 1 },
	} {
		value := valid
		mutate(&value)
		if validEpochSearchWarm(value, final) {
			t.Fatalf("%s was accepted", name)
		}
	}
	for name, body := range map[string][]byte{
		"unknown field": bytes.Replace(raw, []byte(`{"schema"`), []byte(`{"extra":1,"schema"`), 1),
		"no newline":    bytes.TrimSuffix(raw, []byte("\n")),
		"trailing":      append(bytes.Clone(raw), []byte("{}\n")...),
		"not canonical": bytes.Replace(raw, []byte(`,"phase"`), []byte(`, "phase"`), 1),
		"empty":         nil,
	} {
		if _, ok := decodeEpochSearchWarm(body); ok {
			t.Fatalf("%s decoded", name)
		}
	}
}

func TestEpochSearchWarmLaunchOptIn(t *testing.T) {
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema, PlanV4Schema, PlanV5Schema} {
		for epoch := uint64(1); epoch <= 5; epoch++ {
			want := ""
			if epoch == 5 && schema == PlanV5Schema {
				want = SearchWarmSchema
			}
			if got := epochSearchWarmOptIn(epoch, schema); got != want {
				t.Fatalf("%s epoch %d opt-in = %q, want %q", schema, epoch, got, want)
			}
		}
	}
	epoch := ExecutionEpochConfig{Epoch: 5, Repository: "fixture/repo", ConfigSHA256: testDigest("config")}
	plain, err := epochSemanticInput(testDigest("plan"), epoch, nil, nil)
	if err != nil || bytes.Contains(plain, []byte("search_warm")) {
		t.Fatalf("launch without opt-in changed bytes: %s %v", plain, err)
	}
	epoch.SearchWarm = SearchWarmSchema
	opted, err := epochSemanticInput(testDigest("plan"), epoch, nil, nil)
	want := append(bytes.TrimSuffix(plain, []byte("}\n")), []byte(`,"search_warm":"`+SearchWarmSchema+"\"}\n")...)
	if err != nil || !bytes.Equal(opted, want) {
		t.Fatalf("opted-in launch = %s, want the unchanged request plus a final search_warm field: %v", opted, err)
	}
	for _, bad := range []ExecutionEpochConfig{
		{Epoch: 4, Repository: "fixture/repo", ConfigSHA256: testDigest("config"), SearchWarm: SearchWarmSchema},
		{Epoch: 5, Repository: "fixture/repo", ConfigSHA256: testDigest("config"), SearchWarm: "t422-search-warm-v0"},
	} {
		if _, err := epochSemanticInput(testDigest("plan"), bad, nil, nil); err == nil {
			t.Fatalf("launch input admitted %+v", bad)
		}
	}
}

func TestV5SearchWarmPolicy(t *testing.T) {
	v5 := restoreContinuityTestPlan(t)
	if err := applyCallerRestoreContinuityCorrection(&v5); err != nil {
		t.Fatal(err)
	}
	if v5.Correction == nil || !slices.Contains(v5.Correction.RequiredReadiness, v5SearchWarmPolicy) ||
		!strings.HasPrefix(v5SearchWarmPolicy, "phase14-search-warm-v1:") {
		t.Fatal("V5 readiness does not bind the phase-14 search warm")
	}
	v4 := restoreContinuityTestPlan(t)
	if v4.Correction != nil && slices.Contains(v4.Correction.RequiredReadiness, v5SearchWarmPolicy) {
		t.Fatal("the retained V4 contract gained the V5 search warm policy")
	}
}
