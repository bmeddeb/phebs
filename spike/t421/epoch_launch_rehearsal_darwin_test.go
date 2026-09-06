//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Only startup, one authenticated health request and a joined stop are claimed.
// This uses the actual full-population A author and protected epoch-one inputs;
// it does not wait for cold convergence, advance a semantic phase, sign evidence,
// admit a host/profile, create a pressure volume or execute a ceremony.
func TestExecutionEpochOneOptionalRealStartRehearsal(t *testing.T) {
	if os.Getenv("PHEBS_T422_EPOCH_ONE_REHEARSAL") != "1" {
		t.Skip("requires explicit serial protected epoch-one startup rehearsal")
	}
	requireExternalToolFrozenHost(t)
	repository := os.Getenv("PHEBS_T422_PRODUCTION_REPOSITORY")
	commit := os.Getenv("PHEBS_T422_PRODUCTION_COMMIT")
	goRoot := os.Getenv("PHEBS_T422_PRODUCTION_GOROOT")
	moduleCache := os.Getenv("PHEBS_T422_PRODUCTION_MODULE_CACHE")
	gitBinary := os.Getenv("PHEBS_T422_PRODUCTION_GIT")
	if !validCommit(commit) || !executionGitAbsolutePath(repository) || !executionGitAbsolutePath(goRoot) ||
		!executionGitAbsolutePath(moduleCache) || !executionGitAbsolutePath(gitBinary) {
		t.Fatal("explicit exact source/native Git/SDK/offline cache selections required")
	}
	surrealBinary := toolCustodyExternalSurreal(t)
	directory, err := os.MkdirTemp("", "t422-epoch-one-rehearsal-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("retained new rehearsal directory %s: %v", directory, err)
	}
	identity, err := os.Lstat(parent)
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	t.Cleanup(func() {
		if !completed || t.Failed() {
			t.Logf("retained exact epoch-one custody; no automatic retry: %s", parent)
			return
		}
		current, err := os.Lstat(parent)
		if err != nil || !os.SameFile(identity, current) {
			t.Error("epoch-one cleanup parent changed; retaining custody")
			return
		}
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	var author *ExecutionAuthorCustody
	var epochs *ExecutionEpochConfigCustody
	var flow *ExecutionEpochOne
	var run *ExecutionEpochOneRun
	canRelease := func() bool {
		if author != nil {
			author.mu.Lock()
			defer author.mu.Unlock()
			if author.active {
				return false
			}
		}
		if epochs != nil {
			epochs.mu.Lock()
			defer epochs.mu.Unlock()
			return !epochs.active
		}
		return true
	}
	git, err := ProtectExecutionGit(ctx, parent, gitBinary)
	if git != nil {
		defer func() {
			if canRelease() {
				_ = git.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	inputs, err := ProtectExecutionGoBuildInputs(ctx, parent, ExecutionGoBuildRequest{Git: git, RepositoryRoot: repository,
		PlanSourceCommit: commit, IntegratedMainCommit: commit, SourceCommit: commit, GoRoot: goRoot, ModuleCache: moduleCache})
	if inputs != nil {
		defer func() {
			if canRelease() {
				_ = inputs.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual source/SDK/module custody: %s; %+v", time.Since(started), inputs.Inventory())
	workspace := filepath.Join(parent, "supplied-builds")
	for _, path := range []string{workspace, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var tools []*ExecutionToolCustody
	defer func() {
		if canRelease() {
			for _, tool := range tools {
				_ = tool.Close()
			}
		}
	}()
	for _, role := range []string{"t422-author", "phebs", "zoekt-git-index"} {
		selected := productionRehearsalBuild(t, ctx, inputs, workspace, role)
		started = time.Now()
		tool, err := inputs.ProtectReferenceTool(ctx, parent, role, selected)
		if tool != nil {
			tools = append(tools, tool)
		}
		if err != nil {
			t.Fatalf("actual protected %s reference admission: %v", role, err)
		}
		t.Logf("actual protected %s reference admission: %s", role, time.Since(started))
	}
	surreal, err := ProtectExecutionExternalTool(ctx, parent, "surreal", surrealBinary)
	if surreal != nil {
		tools = append(tools, surreal)
	}
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlanV3(commit)
	if err != nil || ctx.Err() != nil {
		t.Fatal("private unsealed plan construction", err)
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(parent, "unsealed-plan-input.json")
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	planInput, err := ProtectExecutionInputs(ctx, parent, []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}})
	if planInput != nil {
		defer func() {
			if canRelease() {
				_ = planInput.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	author, err = PrepareExecutionAuthor(ctx, parent, ExecutionAuthorRequest{Git: git, Builds: inputs, Author: tools[0], Plan: planInput})
	if author != nil {
		defer func() {
			if canRelease() {
				_ = author.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	epochs, err = PrepareExecutionEpochConfigs(ctx, author)
	if epochs != nil {
		defer func() { _ = epochs.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	flow, err = PrepareExecutionEpochOne(ctx, epochs, tools[1], tools[2], surreal)
	if flow != nil {
		defer func() { _ = flow.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	result, err := flow.AuthorA(ctx)
	if err != nil || !result.Completed || !result.RootJoined || !result.SessionEmpty || result.Revision != "a" {
		t.Fatalf("actual shared author A: %+v; %v", result, err)
	}
	started = time.Now()
	run, err = flow.Start(ctx)
	if run != nil {
		defer func() {
			stopCtx, stop := context.WithTimeout(context.Background(), time.Minute)
			defer stop()
			result, err := run.Stop(stopCtx)
			if err != nil || !result.RootJoined || !result.SessionEmpty {
				t.Errorf("retained epoch-one stopped prefix: %+v; %v", result, err)
			}
			if t.Failed() && result.RootJoined && run.output != nil {
				// Native Wait has also joined the combined-output copier. This
				// bounded private diagnostic is not returned public evidence.
				file, err := os.OpenFile(filepath.Join(parent, "server.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err != nil {
					t.Error("private joined-server diagnostic could not be retained", err)
				} else {
					_, writeErr := file.Write(run.output.buffer.Bytes())
					if closeErr := file.Close(); writeErr != nil || closeErr != nil {
						t.Error("private joined-server diagnostic was not completely retained")
					}
				}
			}
		}()
	}
	if err != nil {
		t.Fatal("actual epoch-one Start", err)
	}
	if epochs.Close() == nil || author.Close() == nil {
		t.Fatal("active native server lost protected input/source custody")
	}
	if err := run.Health(ctx); err != nil {
		t.Fatal("actual authenticated epoch-one health", err)
	}
	stopCtx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	stopped, err := run.Stop(stopCtx)
	if err != nil || !stopped.RootStarted || !stopped.RootJoined || !stopped.SessionEmpty {
		t.Fatalf("actual epoch-one owner-drained stop: %+v; %v", stopped, err)
	}
	t.Logf("epoch-one startup/health/stop ONLY: %s; %+v; no cold/phase/receipt/freeze claim", time.Since(started), stopped)
	if !canRelease() || flow.Close() != nil || epochs.Close() != nil || author.Close() != nil || planInput.Close() != nil {
		t.Fatal("joined epoch-one owner/input closure failed; retaining custody")
	}
	for _, tool := range tools {
		if tool.Close() != nil {
			t.Fatal("joined protected tool closure failed")
		}
	}
	if inputs.Close() != nil || git.Close() != nil {
		t.Fatal("joined protected build/Git closure failed")
	}
	gitCustodyTestCleanup(t, git)
	goBuildTestCleanup(t, inputs)
	for index, role := range []string{"t422-author", "phebs", "zoekt-git-index", "surreal"} {
		inputCustodyTestCleanup(t, tools[index].input, []ExecutionInputCopy{{Name: role}})
	}
	inputCustodyTestCleanup(t, planInput, []ExecutionInputCopy{{Name: "plan"}})
	inputCustodyTestCleanup(t, epochs.catalogs, []ExecutionInputCopy{{Name: "catalog-a"}, {Name: "catalog-b"}, {Name: "catalog-a-return"}})
	inputCustodyTestCleanup(t, epochs.configs, []ExecutionInputCopy{{Name: "config-1"}, {Name: "config-2"}, {Name: "config-3"}, {Name: "config-4"}, {Name: "config-5"}})
	completed = true
}
