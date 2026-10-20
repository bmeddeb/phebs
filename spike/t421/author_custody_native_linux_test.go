//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"debug/buildinfo"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// This required driver executes the current-source, independently reference-
// built actual author CLI. The full frozen population is never reduced. It
// qualifies standalone ownership, not shared live-server or epoch admission.
func TestLinuxAuthorRequiredNativeThreeLifetimes(t *testing.T) {
	if os.Getuid() == 0 || os.Geteuid() == 0 || runtime.GOARCH != "amd64" || runtime.Version() != "go1.26.5" {
		t.Fatal("required unprivileged Linux amd64 Go1.26.5 host; no skip")
	}
	referenceRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_GO_REFERENCE_REQUEST")
	candidate := readNativeGoReferenceRequest(t, "PHEBS_T42_REFERENCE_CANDIDATE_REQUEST")
	treeRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_INPUT_TREE_REQUEST")
	if referenceRequest.Role != linuxGoReferenceRole || referenceRequest.PlanSchema != linuxGoReferenceRecipe || candidate.Role != "t422-author" || candidate.PlanSchema != "" ||
		referenceRequest.SourceCommit != candidate.SourceCommit || referenceRequest.RepositoryRoot != candidate.RepositoryRoot || referenceRequest.PlanSourceCommit != candidate.PlanSourceCommit || referenceRequest.IntegratedMainCommit != candidate.IntegratedMainCommit || referenceRequest.GoRoot != candidate.GoRoot || referenceRequest.ModuleCache != candidate.ModuleCache || referenceRequest.GitBinary != candidate.GitBinary {
		t.Fatal("current-source exact helper/author and lineage requests required")
	}
	originals, err := snapshotGoReferenceOriginals(linuxGoReferencePlan{OriginalSDK: candidate.GoRoot, OriginalModules: candidate.ModuleCache, OriginalSource: candidate.RepositoryRoot})
	if err != nil {
		t.Fatal("original input snapshot")
	}
	hostRoot, err := goBuildImportHostRoot()
	if err != nil {
		t.Fatal("host propagation baseline")
	}
	t.Cleanup(func() {
		after, err := goBuildImportHostRoot()
		if err != nil || after != hostRoot {
			t.Error("host propagation changed")
		}
		for _, original := range originals {
			var after unix.Stat_t
			if unix.Lstat(original.path, &after) != nil || !sameLinuxInputStat(original.stat, after) {
				t.Error("host original changed or unavailable")
			}
		}
	})
	base, err := os.MkdirTemp("", "phebs-linux-serial-author-")
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, err := os.Lstat(base)
	if err != nil {
		t.Fatal("private fixture identity; retain", base)
	}
	parent := filepath.Join(base, "parent")
	if os.Mkdir(parent, 0o700) != nil {
		t.Fatal("private parent; retain", base)
	}
	var referenceHelper *LinuxGoReferenceHelper
	var treeHelper *LinuxInputTreeHelper
	var git *LinuxExecutionGitCustody
	var builds *LinuxExecutionGoBuildCustody
	var author *LinuxExecutionToolCustody
	var planInput *LinuxExecutionInputCustody
	var owner *LinuxExecutionAuthorCustody
	successful := false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if owner != nil {
			if owner.Close(ctx) != nil {
				t.Error("author Close sticky failure or cleanup refusal")
			}
			if !owner.released {
				t.Error("unknown author retains all prerequisites and fixture", base)
				return
			}
		}
		if author != nil && author.Close() != nil {
			t.Error("author image retained", base)
			return
		}
		if planInput != nil && planInput.Close() != nil {
			t.Error("sealed plan retained", base)
			return
		}
		if builds != nil {
			if builds.Close(ctx) != nil {
				t.Error("Go Close sticky failure or cleanup refusal")
			}
			if !builds.released {
				t.Error("Go owner retained", base)
				return
			}
		}
		if git != nil {
			if git.Close(ctx) != nil {
				t.Error("Git Close sticky failure or cleanup refusal")
			}
			if !git.released {
				t.Error("Git owner retained", base)
				return
			}
		}
		if referenceHelper != nil && referenceHelper.Close() != nil {
			t.Error("reference helper retained", base)
			return
		}
		if treeHelper != nil && treeHelper.Close() != nil {
			t.Error("tree helper retained", base)
			return
		}
		if !successful || t.Failed() {
			t.Log("failed exact fixture retained", base)
			return
		}
		current, err := os.Lstat(base)
		if err != nil || !os.SameFile(baseInfo, current) || os.RemoveAll(base) != nil {
			t.Error("exact joined fixture cleanup refused", base)
		}
	})
	referenceHelper, err = ProtectLinuxGoReferenceHelper(t.Context(), referenceRequest)
	if err != nil {
		t.Fatal("independently rebuilt current-source reference helper refused")
	}
	treeHelper, err = ProtectLinuxInputTreeHelper(t.Context(), treeRequest)
	if err != nil {
		t.Fatal("independently verified H2k helper refused")
	}
	git, err = ProtectLinuxExecutionGit(t.Context(), parent, candidate.GitBinary, treeHelper)
	if err != nil {
		t.Fatal("genuine H2p Git refused")
	}
	builds, err = ProtectLinuxExecutionGoBuildInputs(t.Context(), parent, LinuxExecutionGoBuildRequest{Git: git, RepositoryRoot: candidate.RepositoryRoot, PlanSourceCommit: candidate.PlanSourceCommit, IntegratedMainCommit: candidate.IntegratedMainCommit, SourceCommit: candidate.SourceCommit, GoRoot: candidate.GoRoot, ModuleCache: candidate.ModuleCache}, treeHelper)
	if err != nil {
		if builds != nil {
			t.Logf("Go refusal fixture=%s stage=%s inventory=%+v", base, builds.preparationStep, builds.Inventory())
		}
		t.Fatal("genuine Linux SDK/modules/source inputs refused")
	}

	input, err := builds.tree.Check(t.Context())
	inventory := builds.Inventory()
	if err != nil || input.Entries != uint64(inventory.Entries) || input.Files != uint64(inventory.Files) || input.Bytes != uint64(inventory.Bytes) {
		t.Fatal("constructor inventory reconciliation")
	}
	if _, err := os.Lstat(filepath.Join(builds.PrivateDirectory(), "payload")); !os.IsNotExist(err) {
		t.Fatal("mutable Go input payload not retired")
	}
	if _, err := builds.CheckGo(t.Context()); err == nil {
		t.Fatal("Go identity published before reference build")
	}
	expectedInfo, err := buildinfo.ReadFile(candidate.Binary)
	if err != nil {
		t.Fatal("current candidate full BuildInfo")
	}
	file, err := os.Open(candidate.Binary)
	if err != nil {
		t.Fatal("current candidate bytes")
	}
	expectedDigest, digestErr := digestExecutionReferenceFD(t.Context(), file)
	closeErr := file.Close()
	if digestErr != nil || closeErr != nil {
		t.Fatal("current candidate complete digest")
	}
	author, err = builds.ProtectReferenceTool(t.Context(), parent, "t422-author", candidate.Binary, referenceHelper)
	if err != nil {
		logNativeGoReferenceFailure(t, builds, base)
		t.Fatal("actual current-source author reference build refused")
	}
	identity, err := author.Check(t.Context(), "t422-author")
	if err != nil || identity.BuildVCSRevision != candidate.SourceCommit || identity.SHA256 != expectedDigest || author.referenceInputs != builds || !builds.last.joined || !builds.last.empty {
		t.Fatal("genuine author source/reference lineage")
	}

	if author.WithImage(t.Context(), "t422-author", func(_ ExecutionToolIdentity, image *os.File) error {
		actual, err := buildinfo.Read(image)
		if err != nil || !reflect.DeepEqual(actual, expectedInfo) {
			return ErrExecutionToolCustody
		}
		return nil
	}) != nil {
		t.Fatal("actual current candidate full BuildInfo differs")
	}
	if builds.Inventory() != inventory || builds.last.input != input {
		t.Fatal("reference build changed constructor inventory")
	}
	for _, tree := range []*LinuxExecutionInputTree{git.tree, builds.tree} {
		members, err := t4013.PrivateProcessSessionMembers(tree.pid)
		if err != nil || members != 0 || !tree.joined || !tree.empty {
			t.Fatal("constructor actual root/session proof")
		}
	}
	goIdentity, err := builds.CheckGo(t.Context())
	if err != nil || goIdentity.Version != "go version go1.26.5 linux/amd64" {
		t.Fatal("actual joined Go identity")
	}
	plan, err := BuildPlanV3(candidate.PlanSourceCommit)
	if err != nil || plan.Profile.Physical.CombinedRegularFiles != 2_031_604 {
		t.Fatal("full frozen source plan")
	}
	raw, err := MarshalCanonical(plan)
	if err != nil || len(raw) > MaxPlanBytes {
		t.Fatal("bounded canonical plan")
	}
	planPath := filepath.Join(base, "selected-plan")
	if os.WriteFile(planPath, raw, 0o600) != nil {
		t.Fatal("private selected plan")
	}
	planInput, err = ProtectLinuxExecutionInputs(t.Context(), []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}})
	if err != nil || os.Remove(planPath) != nil {
		t.Fatal("sealed plan/private mutable source retirement")
	}
	owner, err = PrepareLinuxExecutionAuthor(t.Context(), parent, LinuxExecutionAuthorRequest{Git: git, Builds: builds, Author: author, Plan: planInput})
	if err != nil {
		t.Fatal("Linux author constructor refused", base)
	}
	t.Logf("author_source=%s author_sha256=%s go_sha256=%s plan_sha256=%s reference_pid=%d input_entries=%d input_files=%d input_bytes=%d", identity.BuildVCSRevision, identity.SHA256, goIdentity.SHA256, SHA256(raw), builds.last.pid, builds.last.input.Entries, builds.last.input.Files, builds.last.input.Bytes)
	sourceRoot := owner.roots[1]
	observer := custodybytes.NewBorrowed(sourceRoot.file, sourceRoot.path, sourceRoot.info, sourceRoot.identity.FSID)
	for index, name := range []string{"a", "b", "a-return"} {
		result, err := owner.AuthorNext(t.Context())
		if err != nil {
			owner.mu.Lock()
			attempt := owner.active
			if attempt == nil {
				attempt = owner.last
			}
			if attempt != nil {
				select {
				case <-attempt.done:
					t.Logf("author refusal fixture=%s revision=%s step=%s pid=%d root_started=%t root_joined=%t session_empty=%t stderr=%q", base, name, attempt.step, attempt.pid, attempt.result.RootStarted, attempt.result.RootJoined, attempt.result.SessionEmpty, attempt.stderr)
				default:
					t.Logf("author refusal fixture=%s revision=%s done=false; mechanical fields unavailable", base, name)
				}
			}
			owner.mu.Unlock()
			t.Fatal("required actual full author lifetime refused; no retry", name, base)
		}
		wantChanged := uint64(1)
		if index == 0 {
			wantChanged = 2_031_604
		}
		if result.Revision != name || !result.Completed || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Response == nil || result.Response.Result != owner.projection.expected[index] || result.Response.ChangedPhysicalFiles == nil || !result.Response.ChangedPhysicalFiles.Complete || result.Response.ChangedPhysicalFiles.Count != wantChanged || result.Accounting.Attempts != authorCustodyAttempts(index) || !result.Accounting.Complete || !authorCustodyProducerComplete(result.Accounting, 1, authorCustodyAttempts(index)) {
			t.Fatal("canonical response/terminal/actual Wait/accounting fence", name)
		}
		owner.mu.Lock()
		pid := owner.last.pid
		owner.mu.Unlock()
		members, err := t4013.PrivateProcessSessionMembers(pid)
		if err != nil || members != 0 {
			t.Fatal("actual author session not empty")
		}
		sampleCtx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		sample, sampleErr := observer.Sample(sampleCtx, uint32(index+1))
		cancel()
		if sampleErr != nil {
			t.Fatal("bounded joined-source storage observation refused", name)
		}
		t.Logf("revision=%s joined_source_logical_bytes=%d joined_source_allocated_bytes=%d storage_scope=serial_metadata_observation_not_peak", name, sample.LogicalBytes, sample.AllocatedBytes)
		t.Logf("revision=%s pid=%d actual_root_joined=%t session_empty=%t git_attempts=%d changed_files=%d commit=%s tree=%s", name, pid, result.RootJoined, result.SessionEmpty, result.Accounting.Attempts, result.Response.ChangedPhysicalFiles.Count, result.Response.Result.Commit, result.Response.Result.Tree)
	}
	if len(owner.Results()) != 3 || owner.next != 3 || owner.active != nil {
		t.Fatal("three actual lifetimes lost progression")
	}
	if _, err := owner.AuthorNext(t.Context()); err == nil {
		t.Fatal("fourth revision admitted")
	}
	// No helper or SDK/module/source original is renamed on the host.
	for _, original := range originals {
		file, err := os.Open(original.path)
		if err != nil {
			t.Fatal("host original unavailable")
		}
		_, readErr := file.Readdirnames(1)
		closeErr := file.Close()
		if readErr != nil && readErr != io.EOF || closeErr != nil {
			t.Fatal("host original not readable")
		}
	}
	if owner.checkRoots(t.Context()) != nil || owner.checkSource(t.Context(), owner.previous) != nil {
		t.Fatal("final source/lease identity changed")
	}
	if owner.Close(t.Context()) != nil || !owner.released {
		t.Fatal("joined source close barrier")
	}
	if _, err := owner.AuthorNext(t.Context()); err == nil {
		t.Fatal("closed source owner admitted use")
	}
	successful = true
}
