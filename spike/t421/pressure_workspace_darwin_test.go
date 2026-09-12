//go:build darwin

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutionPressureBorrowRefusals(t *testing.T) {
	for _, test := range []struct {
		name string
		v    *executionPressureVolume
	}{
		{"nil", nil},
		{"unprepared", &executionPressureVolume{}},
		{"borrowed", &executionPressureVolume{ready: true, borrowed: true}},
		{"already_bound", &executionPressureVolume{ready: true, flow: &ExecutionEpochOne{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, path, err := test.v.borrowWorkspace(t.Context()); err == nil || path != "" {
				t.Fatal("unavailable volume issued workspace")
			}
			if test.v.bindRehearsal(t.Context(), &ExecutionEpochOne{}) == nil || test.v.finishRehearsal(t.Context(), &ExecutionEpochOneRun{}) == nil {
				t.Fatal("unbound flow issued populated release")
			}
			if _, err := test.v.samplePreparation(t.Context()); err == nil {
				t.Fatal("unavailable volume supplied preparation bytes")
			}
			if _, err := test.v.sampleFlow(t.Context()); err == nil {
				t.Fatal("unbound flow supplied phase bytes")
			}
			if _, err := test.v.sampleJoined(t.Context(), &ExecutionEpochOneRun{}); err == nil {
				t.Fatal("unjoined run supplied phase bytes")
			}
		})
	}
	v := &executionPressureVolume{borrowed: true}
	if v.Close() == nil || v.closed || v.removeEmpty(t.Context()) == nil {
		t.Fatal("outstanding borrow released volume")
	}
}

// These are ownership-guard models, not measured bytes or native join proof.
func TestExecutionPressureSampleBoundary(t *testing.T) {
	for _, test := range []struct {
		name             string
		joined, retained bool
		mutate           func(*ExecutionEpochOne, *ExecutionEpochOneRun)
		want             bool
	}{
		{name: "post-author", want: true},
		{name: "same-phase-start", mutate: func(f *ExecutionEpochOne, _ *ExecutionEpochOneRun) { f.used = true }},
		{name: "author-active", mutate: func(f *ExecutionEpochOne, _ *ExecutionEpochOneRun) { f.epochs.author.active = true }},
		{name: "author-anchor-changed", mutate: func(f *ExecutionEpochOne, _ *ExecutionEpochOneRun) {
			f.authorStarted = f.authorStarted.Add(time.Second)
		}},
		{name: "latest-joined", joined: true, want: true},
		{name: "latest-retained", joined: true, retained: true, want: true},
		{name: "same-phase-successor", joined: true, mutate: func(f *ExecutionEpochOne, _ *ExecutionEpochOneRun) { f.epochs.released++ }},
		{name: "live-successor", joined: true, retained: true, mutate: func(f *ExecutionEpochOne, _ *ExecutionEpochOneRun) {
			f.epochs.author.borrowedBy = &ExecutionEpochOneRun{}
		}},
		{name: "retained-operation", joined: true, retained: true, mutate: func(_ *ExecutionEpochOne, r *ExecutionEpochOneRun) { r.returnStarting = true }},
		{name: "joined-author-active", joined: true, mutate: func(f *ExecutionEpochOne, _ *ExecutionEpochOneRun) { f.epochs.author.active = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now()
			flow := &ExecutionEpochOne{authored: true, authorStarted: started, epochs: &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}}}
			v := &executionPressureVolume{flow: flow}
			var run *ExecutionEpochOneRun
			if test.joined {
				flow.used = true
				flow.epochs.released = 3
				run = &ExecutionEpochOneRun{flow: flow, epoch: ExecutionEpochConfig{Epoch: 3}, result: ExecutionEpochOneResult{RootJoined: true, SessionEmpty: true}}
				if test.retained {
					flow.retained, flow.epochs.author.borrowedBy, flow.epochs.active = run, run, true
				}
			}
			if !v.sampleBoundary(run, started) {
				t.Fatal("valid initial boundary refused")
			}
			if test.mutate != nil {
				test.mutate(flow, run)
			}
			if got := v.sampleBoundary(run, started); got != test.want {
				t.Fatal("boundary", got, test.want)
			}
		})
	}
}

// This tiny populated fixture tests the detach barrier, not an actual epoch
// run or a manufactured successful flow. The real bound release remains in
// the separately selected epoch rehearsal, which this test does not start.
func TestExecutionPressureWorkspaceOptionalNative(t *testing.T) {
	if os.Getenv("PHEBS_T422_VOLUME_CUSTODY_REHEARSAL") != "1" {
		t.Skip("requires explicit tiny populated APFS custody gate; no corpus/ballast")
	}
	requireExternalToolFrozenHost(t)
	parent, err := os.MkdirTemp("", "t422-populated-volume-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	v, err := prepareExecutionPressureVolume(ctx, parent)
	if v != nil {
		defer func() { _ = v.Close() }()
	}
	if err != nil {
		t.Fatalf("retained native volume %s: %v", parent, err)
	}
	marked := withExecutionPreparationParent(ctx, v.workspace.path)
	if _, err := ObserveExecutionExternalTool(marked, "git", "/Library/Developer/CommandLineTools/usr/bin/git"); err != nil {
		t.Fatal("marked actual Git probe", err)
	}
	entries, err := os.ReadDir(v.workspace.path)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "phebs-t422-external-") {
		t.Fatal("native probe scratch was not retained on volume", err)
	}
	source := filepath.Join(v.workspace.path, "source")
	raw := []byte("retained protected fixture\n")
	if err := os.WriteFile(source, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	inputs, err := ProtectExecutionInputs(ctx, v.workspace.path, []ExecutionInputCopy{{Name: "fixture", Path: source, SHA256: SHA256(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.inputOnWorkspace(inputs) {
		t.Fatal("genuine protected input did not bind native workspace")
	}
	protected := filepath.Join(inputs.Directory(), "fixture")
	if err := inputs.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(protected); err != nil || !inputCustodyProtected(info) {
		t.Fatal("closing input owner thawed the protected bytes", err)
	}
	if v.removeEmpty(ctx) == nil {
		t.Fatal("empty-only API erased populated custody")
	}
	// These are actual sibling files on the held private workspace, not
	// supplied sample totals or a fictitious protected flow/phase binding.
	for _, name := range []string{"fixture-data", "fixture-home", "fixture-tmp", "fixture-backup", "fixture-source"} {
		directory := filepath.Join(v.workspace.path, name)
		if err = os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "actual"), []byte("actual sibling bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(v.workspace.path, "fixture-ballast"), make([]byte, 512<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := v.samplePreparation(ctx)
	if err != nil || actual != actualCustodyByteFixtureTotal(t, v.workspace.path) {
		t.Fatal("whole native workspace sample", actual, err)
	}
	preparation, phases := v.byteSnapshot()
	if !preparation.Completed || preparation.Maximum != actual || phases.Unavailable {
		t.Fatal("preparation prefix", preparation, phases)
	}
	for _, phase := range phases.Phases {
		if phase.Completed {
			t.Fatal("preparation fabricated a phase sample")
		}
	}
	// Mechanical populated-removal seam only: no fictitious run/lease evidence.
	v.mu.Lock()
	err = v.remove(ctx, false)
	v.mu.Unlock()
	if err != nil {
		t.Fatalf("retained populated fixture %s: %v", parent, err)
	}
	if !v.removed || v.Close() != nil {
		t.Fatal("native populated fixture did not close")
	}
	if _, err := os.Lstat(v.root.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("volume root still exists", err)
	}
	if os.Remove(filepath.Join(parent, ".t4013-operation.lock")) != nil || os.Remove(parent) != nil {
		t.Fatal("empty outer controls were not removed")
	}
	t.Log("protected populated fixture detached and removed without thawing; full bound epoch release unmeasured")
}

// This uses real protected native-image copies and held filesystem roots, but
// supplies the private reference-lineage linkage. It tests holder ownership,
// not an actual Buf/focused reference build or a complete profile/epoch.
func TestExecutionProfileToolHolders(t *testing.T) {
	for _, mode := range []string{"bound", "other-build", "closed-build", "other-workspace", "closed", "partial", "late"} {
		t.Run(mode, func(t *testing.T) {
			parent, _ := inputCustodyTestFixture(t)
			builds := &ExecutionGoBuildCustody{}
			author := &ExecutionAuthorCustody{parent: parent, request: ExecutionAuthorRequest{Builds: builds}}
			flow := &ExecutionEpochOne{plan: Plan{Schema: PlanV3Schema}, epochs: &ExecutionEpochConfigCustody{author: author}}
			var tools [2]*ExecutionToolCustody
			for index, role := range [2]string{"buf", "phebs-focused-index"} {
				selectedParent := parent
				if mode == "other-workspace" && index == 1 {
					selectedParent, _ = inputCustodyTestFixture(t)
				}
				copy := inputCustodyTestSpec(t, role, "/usr/bin/true", true)
				input, err := inputCustodyTestProtect(t, t.Context(), selectedParent, []ExecutionInputCopy{copy})
				if err != nil {
					t.Fatal(err)
				}
				tools[index] = &ExecutionToolCustody{input: input, referenceInputs: builds,
					identity: ExecutionToolIdentity{Role: role, SHA256: copy.SHA256, FileType: regularFileType}}
			}
			root, err := openProductionRoot(parent)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.file.Close(); err != nil {
					t.Error(err)
				}
			})
			v := &executionPressureVolume{workspace: root}
			if got := v.inputOnWorkspace(tools[1].input); got != (mode != "other-workspace") {
				t.Fatal("actual held input accepted the wrong workspace")
			}
			switch mode {
			case "other-build":
				tools[1].referenceInputs = &ExecutionGoBuildCustody{}
			case "closed-build":
				builds.closed = true
			case "closed":
				if err := tools[1].Close(); err != nil {
					t.Fatal(err)
				}
			case "partial":
				tools[1] = nil
			case "late":
				flow.authored = true
			}
			err = flow.bindProfileTools(t.Context(), tools[0], tools[1])
			if mode != "bound" {
				if err == nil || flow.profileTools != ([2]*ExecutionToolCustody{}) {
					t.Fatal("failed pair issued or partially retained holders")
				}
				return
			}
			if err != nil || flow.profileTools != tools {
				t.Fatal("protected pair was not retained", err)
			}
			if flow.bindProfileTools(t.Context(), tools[0], tools[1]) == nil || flow.profileTools != tools {
				t.Fatal("one-shot binding changed existing holders")
			}
		})
	}
}

// Source-bound ordering check only: the opt-in whole-flow fixture owns actual
// input release/detach. This test does not supply a successful native teardown.
func TestExecutionProfileToolReleaseOrder(t *testing.T) {
	raw, err := os.ReadFile("pressure_workspace_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(raw), "func (v *executionPressureVolume) finishWorkspace(")
	if !ok {
		t.Fatal("missing actual release implementation")
	}
	previous := -1
	for _, point := range []string{
		"for _, tool := range flow.profileTools",
		"tool != nil && tool.Close() != nil",
		"author.request.Builds.Close()",
		"v.sampleTeardownWorkspace(ctx)",
		"return v.remove(ctx, false)",
	} {
		position := strings.Index(body, point)
		if position <= previous {
			t.Fatal("profile tool descriptors must close before input-release sample and detach")
		}
		previous = position
	}
}
