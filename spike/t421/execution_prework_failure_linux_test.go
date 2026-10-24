//go:build linux

package t421

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxExecutorPreworkPrivateFailureRecord(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	value := linuxExecutorPreworkSelection{PreparationRoot: root, Attempt: strings.Repeat("a", 32), SourceCommit: strings.Repeat("b", 40)}
	observation := &executionPreworkObservation{}
	if err := writeLinuxExecutorPreworkFailure(value, observation); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("healthy observation added filesystem work", err)
	}
	workspace := filepath.Join(t.TempDir(), "source-owned-retained-prefix")
	observation.retainWorkspace(workspace)
	if err := writeLinuxExecutorPreworkFailure(value, observation); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "executor-prework-"+value.Attempt+"-failure.json")
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 8<<10 {
		t.Fatal("bounded private record", err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode() != 0o600 {
		t.Fatal("private file mode", err)
	}
	var record linuxExecutorPreworkFailure
	if json.Unmarshal(raw, &record) != nil || record.Schema != "t422-linux-prework-private-failure-v1" ||
		record.Attempt != value.Attempt || record.SourceCommit != value.SourceCommit || !record.WorkspaceNotClean ||
		record.RetainedWorkspace != workspace || record.HelperPID != 0 || record.NativeWaitPresent || record.NativeCloseFailed || record.FirstObservedJoined || record.FirstObservedSessionEmpty {
		t.Fatal("record invented terminal/helper facts")
	}
	if writeLinuxExecutorPreworkFailure(value, observation) == nil || observation.nativeCloseFailed.Load() {
		t.Fatal("exclusive publication refusal became native uncertainty")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("first-observed record was overwritten", err)
	}
}

func TestLinuxExecutorPreworkPrivateFailureCloseOnlyRecord(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	observation := &executionPreworkObservation{}
	file, err := os.CreateTemp(t.TempDir(), "owned-protected-root")
	if err != nil || file.Close() != nil {
		t.Fatal("controlled owned close fixture", err)
	}
	// Exercise actual partial-factory cleanup after unexpected prior closure of
	// its test-owned descriptor; no generic operation error is the observation.
	if closeInputTreeSources([]*inputTreeSourceRoot{{file: file, observation: observation}}) == nil {
		t.Fatal("missing actual cleanup failure")
	}
	value := linuxExecutorPreworkSelection{PreparationRoot: root, Attempt: strings.Repeat("c", 32), SourceCommit: strings.Repeat("d", 40)}
	if err := writeLinuxExecutorPreworkFailure(value, observation); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "executor-prework-"+value.Attempt+"-failure.json"))
	var record linuxExecutorPreworkFailure
	if err != nil || len(raw) > 8<<10 || json.Unmarshal(raw, &record) != nil || !record.NativeCloseFailed ||
		record.WorkspaceNotClean || record.RetainedWorkspace != "" || record.HelperPID != 0 || record.NativeWaitPresent ||
		record.FirstObservedJoined || record.FirstObservedSessionEmpty || observation.settle(t.Context()) {
		t.Fatal("close-only diagnostic was lost or invented helper/retirement authority", err)
	}
}

func TestLinuxExecutorPreworkPrivateFailureRecordRefusals(t *testing.T) {
	for _, mode := range []string{"nonce", "source", "relative-workspace", "oversize-workspace", "public-root", "symlink-file", "symlink-root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			value := linuxExecutorPreworkSelection{PreparationRoot: root, Attempt: strings.Repeat("a", 32), SourceCommit: strings.Repeat("b", 40)}
			observation := &executionPreworkObservation{}
			observation.retainWorkspace(filepath.Join(root, "retained"))
			target := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "nonce":
				value.Attempt = "../forged"
			case "source":
				value.SourceCommit = "main"
			case "relative-workspace":
				observation.retainedWorkspace = "relative"
			case "oversize-workspace":
				observation.retainedWorkspace = "/" + strings.Repeat("a", maxInputCustodyPathBytes)
			case "public-root":
				if err := os.Chmod(root, 0o755); err != nil {
					t.Fatal(err)
				}
			case "symlink-file":
				if err := os.Symlink(target, filepath.Join(root, "executor-prework-"+value.Attempt+"-failure.json")); err != nil {
					t.Fatal(err)
				}
			case "symlink-root":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				value.PreparationRoot = link
			}
			if writeLinuxExecutorPreworkFailure(value, observation) == nil || observation.nativeCloseFailed.Load() {
				t.Fatal("forged/public/unbounded record accepted or operation refusal invented native uncertainty")
			}
			raw, err := os.ReadFile(target)
			if err != nil || string(raw) != "original" {
				t.Fatal("publication changed unrelated target", err)
			}
		})
	}
}
