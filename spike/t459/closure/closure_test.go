package closure

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClosureReceiptIsStable(t *testing.T) {
	first, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("closure receipt is not deterministic")
	}
	retained, err := os.ReadFile(filepath.Join(root(t), "spike/t459/closure/closure.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatalf("retained closure differs\n%s", first)
	}
	got := Build()
	if got.Neutral.Outcome != "completed" || got.Target.Outcome != "not_run" || got.T459Acceptance != "OPEN" {
		t.Fatalf("closure posture: %+v %+v", got.Neutral.Outcome, got.Target)
	}
	if len(got.Neutral.Behaviors) != 10 || len(got.Inputs) != 6 {
		t.Fatalf("behaviors %d inputs %d", len(got.Neutral.Behaviors), len(got.Inputs))
	}
	if got.Measured.SatisfiesDesignTarget || TargetSatisfied(got.Target, got.Measured) {
		t.Fatal("neutral measurement satisfied the design target")
	}
	bound := got.Target
	bound.Corpus, bound.Commit, bound.Profile, bound.Tools, bound.Host = "example", "abc", "profile", "tools", "host"
	if TargetSatisfied(bound, got.Measured) {
		t.Fatal("bound target with the neutral measurement satisfied the design target")
	}
	met := Dimensions{AcceptedServiceIncarnations: DesignAcceptedServices, AdmittedSourceBlobBytes: DesignAdmittedBytes}
	if TargetSatisfied(got.Target, met) {
		t.Fatal("unbound target with a sufficient measurement was accepted")
	}
	if !TargetSatisfied(bound, met) {
		t.Fatal("bound target with a sufficient measurement was refused")
	}
}

func TestClosureBindsRetainedEvidence(t *testing.T) {
	base := root(t)
	for _, input := range Build().Inputs {
		raw, err := os.ReadFile(filepath.Join(base, input.Path))
		if err != nil {
			t.Fatal(err)
		}
		if Digest(raw) != input.Digest {
			t.Errorf("%s digest = %s, want %s", input.Path, Digest(raw), input.Digest)
		}
		var header struct {
			Schema             string `json:"schema"`
			PrerequisiteStatus string `json:"prerequisite_status"`
			BridgeGate         string `json:"bridge_gate"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatal(err)
		}
		if header.Schema != input.Schema {
			t.Errorf("%s schema = %s, want %s", input.Path, header.Schema, input.Schema)
		}
		if input.Path == "spike/t459/settings_native_3.json" {
			if header.BridgeGate != "PASS" {
				t.Errorf("native bridge gate = %s", header.BridgeGate)
			}
		} else if header.PrerequisiteStatus != "PASS" {
			t.Errorf("%s status = %s", input.Path, header.PrerequisiteStatus)
		}
	}
	checkCoordinator(t, base)
	checkFaults(t, base)
	checkPressure(t, base)
	checkRestore(t, base)
	checkBrowser(t, base)
	checkNativeBridge(t, base)
}

func checkCoordinator(t *testing.T, base string) {
	t.Helper()
	var got struct {
		NativeCase struct {
			NativeLaunches int    `json:"native_launches"`
			ColdWarm       string `json:"cold_warm_definition_references_hover"`
		} `json:"native_case"`
		Acceptance struct {
			Services int64 `json:"accepted_service_incarnations"`
			Bytes    int64 `json:"admitted_regular_source_git_blob_bytes"`
		} `json:"acceptance_dimensions"`
	}
	read(t, base, "spike/t459/native_coordinator_1.json", &got)
	if got.NativeCase.NativeLaunches != 2 || got.NativeCase.ColdWarm != "exact_equal" || got.Acceptance.Services != 0 || got.Acceptance.Bytes != 176 {
		t.Fatalf("coordinator evidence: %+v", got)
	}
}

func checkFaults(t *testing.T, base string) {
	t.Helper()
	var got struct {
		Cases []struct {
			Case             string `json:"case"`
			Status           string `json:"status"`
			FreshRecovery    bool   `json:"fresh_recovery_process"`
			Replay           string `json:"interrupted_work_replay"`
			WorkspaceDrained bool   `json:"workspace_drained"`
			EngineJoined     bool   `json:"engine_joined"`
		} `json:"cases"`
	}
	read(t, base, "spike/t459/native_faults_1.json", &got)
	want := map[string]bool{"cancel": false, "wall": false, "hard-death": true}
	for _, c := range got.Cases {
		fresh, ok := want[c.Case]
		if !ok || c.Status != "PASS" {
			continue
		}
		delete(want, c.Case)
		if c.Case == "hard-death" && (!c.FreshRecovery || c.Replay != "refused" || !c.WorkspaceDrained || !c.EngineJoined || fresh != c.FreshRecovery) {
			t.Fatalf("hard-death evidence: %+v", c)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing fault cases: %v", want)
	}
}

func checkPressure(t *testing.T, base string) {
	t.Helper()
	var got struct {
		NativeCase struct {
			Proof struct {
				Turns int `json:"lifecycle_turns"`
			} `json:"proof"`
		} `json:"native_case"`
		Acceptance struct {
			Bytes int64 `json:"admitted_regular_source_git_blob_bytes"`
		} `json:"acceptance_dimensions"`
	}
	read(t, base, "spike/t459/native_pressure_lifecycle_1.json", &got)
	if got.NativeCase.Proof.Turns != 1211 || got.Acceptance.Bytes != 176 {
		t.Fatalf("pressure evidence: %+v", got)
	}
}

func checkRestore(t *testing.T, base string) {
	t.Helper()
	var got struct {
		NativeCase struct {
			Proof struct {
				Unavailable bool  `json:"restored_unavailable"`
				Epochs      []int `json:"profile_epochs"`
				Equal       bool  `json:"routed_content_equal"`
			} `json:"proof"`
		} `json:"native_case"`
	}
	read(t, base, "spike/t459/native_restore_1.json", &got)
	p := got.NativeCase.Proof
	if !p.Unavailable || !p.Equal || len(p.Epochs) != 2 || p.Epochs[0] != 1 || p.Epochs[1] != 2 {
		t.Fatalf("restore evidence: %+v", p)
	}
}

func checkBrowser(t *testing.T, base string) {
	t.Helper()
	var got struct {
		Rendered struct {
			Result string `json:"result"`
		} `json:"rendered_case"`
	}
	read(t, base, "spike/t459/settings_browser_2.json", &got)
	if got.Rendered.Result != "PASS" {
		t.Fatalf("rendered settings: %+v", got.Rendered)
	}
}

func checkNativeBridge(t *testing.T, base string) {
	t.Helper()
	var got struct {
		Native struct {
			Checks  []string `json:"checks"`
			Fixture struct {
				Publication struct {
					Bytes int64  `json:"sourceBytes"`
					State string `json:"state"`
				} `json:"publication"`
			} `json:"fixture"`
			Warm struct {
				Elapsed int64  `json:"elapsedMS"`
				State   string `json:"state"`
				Lookups int    `json:"lookups"`
			} `json:"warm"`
			Cleanup struct {
				Joined  bool `json:"runtimeJoined"`
				Drained bool `json:"hostScratchDrained"`
			} `json:"cleanup"`
		} `json:"native_case"`
		Post struct {
			Containers int `json:"containers"`
		} `json:"post_run"`
	}
	read(t, base, "spike/t459/settings_native_3.json", &got)
	checks := got.Native.Checks
	need := map[string]bool{
		"rendered native current and cold warm cross-member HTTP navigation": false,
		"ordinary polling preserves current without native replay":           false,
		"source transition fences rendered state and cached native reads":    false,
	}
	for _, name := range checks {
		if _, ok := need[name]; ok {
			need[name] = true
		}
	}
	for name, seen := range need {
		if !seen {
			t.Errorf("missing bridge check %s", name)
		}
	}
	pub := got.Native.Fixture.Publication
	warm := got.Native.Warm
	cleanup := got.Native.Cleanup
	if pub.Bytes != 176 || pub.State != "current" || warm.Elapsed < 31000 || warm.State != "current" || warm.Lookups != 1 || !cleanup.Joined || !cleanup.Drained || got.Post.Containers != 0 {
		t.Fatalf("bridge evidence: %+v", got)
	}
}

func read(t *testing.T, base, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(base, path))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

func root(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
