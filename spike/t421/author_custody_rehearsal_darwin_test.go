//go:build darwin

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This is a serial full-population author rehearsal, not a ceremony. No server,
// pressure volume, execution profile or signature is created. Each revision
// must come from the actual independently reference-built command; failure
// retains its exact private source/input prefix and never retries that author.
func TestExecutionAuthorOptionalRealCLIRehearsal(t *testing.T) {
	if os.Getenv("PHEBS_T422_AUTHOR_REHEARSAL") != "1" {
		t.Skip("requires explicit serial full-population author admission")
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
	parent, err := os.MkdirTemp("", "t422-author-rehearsal-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	t.Cleanup(func() {
		if !completed || t.Failed() {
			t.Logf("retained exact author rehearsal custody (no automatic retry): %s", parent)
			return
		}
		current, err := os.Lstat(parent)
		if err != nil || !os.SameFile(parentInfo, current) {
			t.Error("author rehearsal cleanup parent changed; retaining it")
			return
		}
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Minute)
	defer cancel()
	var custody *ExecutionAuthorCustody
	canRelease := func() bool {
		if custody == nil {
			return true
		}
		custody.mu.Lock()
		defer custody.mu.Unlock()
		return !custody.active
	}
	started := time.Now()
	git, err := ProtectExecutionGit(ctx, parent, gitBinary)
	defer func() {
		if canRelease() {
			_ = git.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ProtectExecutionGoBuildInputs(ctx, parent, ExecutionGoBuildRequest{Git: git, RepositoryRoot: repository,
		PlanSourceCommit: commit, IntegratedMainCommit: commit, SourceCommit: commit, GoRoot: goRoot, ModuleCache: moduleCache})
	defer func() {
		if canRelease() {
			_ = inputs.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual author source/SDK/module custody: %s; %+v", time.Since(started), inputs.Inventory())
	workspace := filepath.Join(parent, "supplied-builds")
	for _, path := range []string{workspace, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	selected := productionRehearsalBuild(t, ctx, inputs, workspace, "t422-author")
	started = time.Now()
	author, err := inputs.ProtectReferenceTool(ctx, parent, "t422-author", selected)
	defer func() {
		if canRelease() {
			_ = author.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual protected author independent reference admission: %s", time.Since(started))
	plan, err := BuildPlanV3(commit)
	if err != nil || ctx.Err() != nil {
		t.Fatal("private unsealed rehearsal plan construction", err)
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
	defer func() {
		if planInput != nil && canRelease() {
			_ = planInput.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	custody, err = PrepareExecutionAuthor(ctx, parent, ExecutionAuthorRequest{Git: git, Builds: inputs, Author: author, Plan: planInput})
	defer func() {
		if canRelease() {
			_ = custody.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"a", "b", "a-return"} {
		started = time.Now()
		result, err := custody.AuthorNext(ctx)
		if err != nil || result.Revision != name || !result.Completed || result.Response == nil ||
			!result.RootStarted || !result.RootJoined || !result.SessionEmpty || !result.Accounting.Complete ||
			result.Accounting.Attempts != []uint64{4, 3, 3}[index] {
			t.Fatalf("actual full-population %s CLI prefix: %+v, %v", name, result, err)
		}
		t.Logf("actual full-population %s CLI/census/checkpoint/join: %s; attempts=%d wire=%d commit=%s",
			name, time.Since(started), result.Accounting.Attempts, result.Accounting.ReservedWireBytes, result.Response.Result.Commit)
	}
	if len(custody.Results()) != 3 || custody.Close() != nil || !canRelease() {
		t.Fatal("completed author sequence did not close its native source lease")
	}
	// These existing fixture cleaners are registered only after all actual
	// roots/sessions joined. Failed or uncertain execution never thaws inputs.
	gitCustodyTestCleanup(t, git)
	goBuildTestCleanup(t, inputs)
	inputCustodyTestCleanup(t, author.input, []ExecutionInputCopy{{Name: "t422-author"}})
	inputCustodyTestCleanup(t, planInput, []ExecutionInputCopy{{Name: "plan"}})
	completed = true
}
