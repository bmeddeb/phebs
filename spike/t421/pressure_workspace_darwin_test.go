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
		})
	}
	v := &executionPressureVolume{borrowed: true}
	if v.Close() == nil || v.closed || v.removeEmpty(t.Context()) == nil {
		t.Fatal("outstanding borrow released volume")
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
