//go:build darwin

package t421

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// This opt-in rehearsal runs three protected, full-population author CLIs on
// one genuine parent producer and the unchanged fifteen-phase accounting
// configuration. Seven never-launched producers are explicitly canceled unused.
// Empty accounting phases prove no server/archive/pressure/recovery work: this
// is not a five-epoch pipeline, full profile admission, freeze or ceremony.
func TestExecutionAuthorOptionalSharedControllerRehearsal(t *testing.T) {
	if os.Getenv("PHEBS_T422_SHARED_AUTHOR_REHEARSAL") != "1" {
		t.Skip("requires explicit serial protected shared-author rehearsal")
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
	directory, err := os.MkdirTemp("", "t422-shared-author-rehearsal-")
	if err != nil {
		t.Fatal(err)
	}
	parentPath, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("retained new shared-author directory %s: %v", directory, err)
	}
	parentInfo, err := os.Lstat(parentPath)
	if err != nil {
		t.Fatalf("retained new shared-author directory %s: %v", parentPath, err)
	}
	completed := false
	t.Cleanup(func() {
		if !completed || t.Failed() {
			t.Logf("retained exact shared-author custody (no automatic retry): %s", parentPath)
			return
		}
		current, err := os.Lstat(parentPath)
		if err != nil || !os.SameFile(parentInfo, current) {
			t.Error("shared-author cleanup parent changed; retaining it")
			return
		}
		if err := os.RemoveAll(parentPath); err != nil {
			t.Error(err)
		}
	})
	// The same existing ninety-minute fixture deadline only shortens each
	// selected plan deadline. Contextless plan decoding remains cooperative.
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Minute)
	defer cancel()
	var custody *ExecutionAuthorCustody
	var parent *dispatchadmission.LocalProducer
	canRelease := func() bool {
		if parent != nil {
			count, _ := parent.Count() // Preserve the actual row even on terminal failure.
			if count.Producer != executionRootProducer || count.Active != 0 {
				return false
			}
		}
		if custody == nil {
			return true
		}
		custody.mu.Lock()
		defer custody.mu.Unlock()
		return !custody.active
	}
	started := time.Now()
	git, err := ProtectExecutionGit(ctx, parentPath, gitBinary)
	defer func() {
		if git != nil && canRelease() {
			_ = git.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ProtectExecutionGoBuildInputs(ctx, parentPath, ExecutionGoBuildRequest{Git: git, RepositoryRoot: repository,
		PlanSourceCommit: commit, IntegratedMainCommit: commit, SourceCommit: commit, GoRoot: goRoot, ModuleCache: moduleCache})
	defer func() {
		if inputs != nil && canRelease() {
			_ = inputs.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual shared-author source/SDK/module custody: %s; %+v", time.Since(started), inputs.Inventory())
	workspace := filepath.Join(parentPath, "supplied-builds")
	for _, path := range []string{workspace, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	selected := productionRehearsalBuild(t, ctx, inputs, workspace, "t422-author")
	started = time.Now()
	author, err := inputs.ProtectReferenceTool(ctx, parentPath, "t422-author", selected)
	defer func() {
		if author != nil && canRelease() {
			_ = author.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual protected shared-author independent reference admission: %s", time.Since(started))
	plan, err := BuildPlanV3(commit)
	if err != nil || ctx.Err() != nil {
		t.Fatal("private unsealed shared-author plan construction", err)
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(parentPath, "unsealed-plan-input.json")
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	planInput, err := ProtectExecutionInputs(ctx, parentPath, []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}})
	defer func() {
		if planInput != nil && canRelease() {
			_ = planInput.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	custody, err = PrepareExecutionAuthor(ctx, parentPath, ExecutionAuthorRequest{Git: git, Builds: inputs, Author: author, Plan: planInput})
	defer func() {
		if custody != nil && canRelease() {
			_ = custody.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	var bindings [executionProducerCount][32]byte
	for index := range bindings {
		if _, err := rand.Read(bindings[index][:]); err != nil {
			t.Fatal(err)
		}
	}
	config, err := executionDispatchConfig(plan, bindings)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := dispatchadmission.New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !completed || t.Failed() {
			snapshot, err := controller.Snapshot()
			t.Logf("actual stopped shared-author accounting prefix: %+v; %v", snapshot, err)
		}
	}()
	parent, err = controller.NewLocalProducer(ctx, executionRootProducer)
	defer func() {
		if parent != nil {
			_ = parent.Close(context.Background())
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	var expectedTotal uint64
	for phase := uint32(1); phase <= executionPhaseCount; phase++ {
		if phase == 2 || phase == 4 || phase == 6 {
			index := int(phase/2) - 1
			name := []string{"a", "b", "a-return"}[index]
			started = time.Now()
			result, err := custody.AuthorNextOn(ctx, controller, parent, uint32(7+index))
			expectedTotal += 1 + authorCustodyAttempts(index)
			if err != nil || result.Revision != name || result.ProducerID != uint32(7+index) || !result.Completed ||
				result.Response == nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty ||
				result.Accounting.Complete || result.Accounting.Attempts != expectedTotal ||
				!authorCustodyProducerComplete(result.Accounting, result.ProducerID, authorCustodyAttempts(index)) {
				t.Fatalf("actual full-population shared %s CLI prefix: %+v; %v", name, result, err)
			}
			root, err := parent.Count()
			if err != nil || root.Producer != executionRootProducer || !root.Attached || root.Closed || root.Active != 0 || root.Ordinal != uint64(index+1) {
				t.Fatal("actual direct-author parent Start/Wait count differs", root, err)
			}
			t.Logf("actual full-population shared %s CLI/census/Pause-ACK/root-handle-join: %s; total=%d root=%d Git=%d commit=%s",
				name, time.Since(started), result.Accounting.Attempts, root.Ordinal, authorCustodyAttempts(index), result.Response.Result.Commit)
		}
		if parent.Pause(ctx) != nil || controller.Fence() != nil {
			t.Fatal("shared rehearsal actual root pause/fence failed", phase)
		}
		if phase == 1 {
			for _, producer := range []uint32{2, 3, 4, 5, 6, 10, 11} {
				if err := controller.CancelUnused(producer); err != nil {
					t.Fatal("never-launched producer was not closed unused", producer, err)
				}
			}
		}
		if phase == executionPhaseCount {
			if err := parent.Close(ctx); err != nil {
				t.Fatal("actual final root lifetime did not close/join", err)
			}
		} else if parent.Checkpoint(ctx) != nil || controller.Advance() != nil || parent.Resume(phase+1) != nil {
			t.Fatal("same-controller accounting handoff failed", phase)
		}
	}
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Attempts != 13 || snapshot.Digest == ([32]byte{}) ||
		len(snapshot.Phases) != executionPhaseCount || len(snapshot.Producers) != executionProducerCount {
		t.Fatal("actual shared-author mechanical closure incomplete", snapshot, err)
	}
	for _, phase := range snapshot.Phases {
		gitAttempts, authorAttempts := uint64(0), uint64(0)
		if phase.Phase == 2 || phase.Phase == 4 || phase.Phase == 6 {
			gitAttempts, authorAttempts = authorCustodyAttempts(int(phase.Phase/2)-1), 1
		}
		if phase.Attempts != gitAttempts+authorAttempts || len(phase.Roles) != 7 {
			t.Fatal("an empty phase acquired work or author phase lost actual work", phase)
		}
		for _, role := range phase.Roles {
			want := uint64(0)
			switch role.Role {
			case dispatchadmission.RoleGit:
				want = gitAttempts
			case executionRoleAuthor:
				want = authorAttempts
			}
			if role.Attempts != want {
				t.Fatal("non-author role acquired work or parent/nested accounting differs", phase.Phase, role)
			}
		}
	}
	for _, producer := range snapshot.Producers {
		want, attached := uint64(0), false
		if producer.Producer == executionRootProducer {
			want, attached = 3, true
		} else if producer.Producer >= 7 && producer.Producer <= 9 {
			want, attached = authorCustodyAttempts(int(producer.Producer-7)), true
		}
		if !producer.Closed || producer.Active != 0 || producer.Attached != attached || producer.Ordinal != want || !attached && producer.Checkpoint != 0 {
			t.Fatal("genuine launched/unused producer closure differs", producer)
		}
	}
	registerEpochCleanup := rehearseExecutionEpochConfigs(t, ctx, custody)
	if len(custody.Results()) != 3 || !canRelease() || custody.Close() != nil ||
		planInput.Close() != nil || author.Close() != nil || inputs.Close() != nil || git.Close() != nil {
		t.Fatal("joined shared-author inputs did not close; retaining protected custody")
	}
	t.Logf("shared-author ONLY: fifteen accounting phases, three joined real authors, seven never-launched producers, attempts=%d wire=%d; no server/archive/pressure/recovery or ceremony claim",
		snapshot.Attempts, snapshot.ReservedWireBytes)
	// Capture exact private fixture identities only after every child/session,
	// local receiver, source lease and protected descriptor has closed. Any
	// preceding failure retains the protected positive prefix without thawing.
	registerEpochCleanup()
	gitCustodyTestCleanup(t, git)
	goBuildTestCleanup(t, inputs)
	inputCustodyTestCleanup(t, author.input, []ExecutionInputCopy{{Name: "t422-author"}})
	inputCustodyTestCleanup(t, planInput, []ExecutionInputCopy{{Name: "plan"}})
	completed = true
}
