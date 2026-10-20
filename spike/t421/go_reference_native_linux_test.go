//go:build linux && phebs_t42_native

package t421

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// This required driver qualifies the implemented author reference recipe. It
// never executes the candidate and grants no aggregate or ceremony admission.
func TestLinuxGoReferenceRequiredNativeAuthor(t *testing.T) {
	if os.Getuid() == 0 || os.Geteuid() == 0 || runtime.GOARCH != "amd64" || runtime.Version() != "go1.26.5" {
		t.Fatal("required unprivileged Linux amd64 Go1.26.5 host; no skip")
	}
	helperRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_GO_REFERENCE_REQUEST")
	candidateRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_REFERENCE_CANDIDATE_REQUEST")
	treeRequest := readNativeGoReferenceRequest(t, "PHEBS_T42_INPUT_TREE_REQUEST")
	if helperRequest.Role != linuxGoReferenceRole || helperRequest.PlanSchema != linuxGoReferenceRecipe ||
		candidateRequest.Role != "t422-author" || candidateRequest.PlanSchema != "" ||
		helperRequest.RepositoryRoot != candidateRequest.RepositoryRoot || helperRequest.GitBinary != candidateRequest.GitBinary ||
		helperRequest.GoRoot != candidateRequest.GoRoot || helperRequest.ModuleCache != candidateRequest.ModuleCache ||
		helperRequest.SourceCommit != candidateRequest.SourceCommit || helperRequest.PlanSourceCommit != candidateRequest.PlanSourceCommit ||
		helperRequest.IntegratedMainCommit != candidateRequest.IntegratedMainCommit {
		t.Fatal("exact helper/author source, SDK, cache and lineage requests required")
	}
	plan := linuxGoReferencePlan{OriginalSDK: candidateRequest.GoRoot, OriginalModules: candidateRequest.ModuleCache, OriginalSource: candidateRequest.RepositoryRoot}
	originals, err := snapshotGoReferenceOriginals(plan)
	if err != nil {
		t.Fatal("original input snapshot refused")
	}
	for _, env := range []string{"PHEBS_T42_GO_REFERENCE_REQUEST", "PHEBS_T42_REFERENCE_CANDIDATE_REQUEST", "PHEBS_T42_INPUT_TREE_REQUEST"} {
		for _, original := range originals {
			if inputTreePathsOverlap(os.Getenv(env), original.path) {
				t.Fatal("request evidence must remain outside masked originals")
			}
		}
	}
	hostRoot, err := goBuildImportHostRoot()
	if err != nil {
		t.Fatal("host propagation baseline refused")
	}
	t.Cleanup(func() {
		after, err := goBuildImportHostRoot()
		if err != nil || after != hostRoot {
			t.Error("host-root propagation bracket changed")
		}
		for _, original := range originals {
			var after unix.Stat_t
			if unix.Lstat(original.path, &after) != nil || !sameLinuxInputStat(original.stat, after) {
				t.Error("host original input identity changed or became unavailable")
			}
		}
	})
	base, err := os.MkdirTemp("", "phebs-linux-go-reference-")
	if err != nil {
		t.Fatal("private native fixture creation refused")
	}
	parent := filepath.Join(base, "parent")
	if os.Mkdir(parent, 0o700) != nil {
		t.Fatal("private parent creation refused; retain", base)
	}
	var helper *LinuxGoReferenceHelper
	var treeHelper *LinuxInputTreeHelper
	var git *LinuxExecutionGitCustody
	var owner *LinuxExecutionGoBuildCustody
	var tool *LinuxExecutionToolCustody
	cleaned := false
	cleanup := func() {
		if cleaned {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if tool != nil && tool.Close() != nil {
			t.Error("protected author Close refused")
			return
		}
		if owner != nil {
			if owner.Close(ctx) != nil {
				t.Error("Go custody Close returned sticky refusal or cleanup error")
			}
			if !owner.released {
				t.Error("unjoined Go custody retains helpers and exact private fixture", base)
				return
			}
		}
		if git != nil {
			if git.Close(ctx) != nil {
				t.Error("Git custody Close returned sticky refusal or cleanup error")
			}
			if !git.released {
				t.Error("unjoined Git custody retains helper and exact private fixture", base)
				return
			}
		}
		if helper != nil && helper.Close() != nil {
			t.Error("reference helper Close refused; retain", base)
			return
		}
		if treeHelper != nil && treeHelper.Close() != nil {
			t.Error("input-tree helper Close refused; retain", base)
			return
		}
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 0 || os.Remove(parent) != nil || os.Remove(base) != nil {
			t.Error("exact joined fixture cleanup refused; retain", base)
			return
		}
		cleaned = true
	}
	t.Cleanup(cleanup)
	helper, err = ProtectLinuxGoReferenceHelper(t.Context(), helperRequest)
	if err != nil {
		t.Fatal("exact-source reference helper refused; no skip")
	}
	helperIdentity, err := helper.tool.Check(t.Context(), linuxGoReferenceRole)
	if err != nil || helperIdentity.Role != linuxGoReferenceRole || helperIdentity.BuildVCSRevision != helperRequest.SourceCommit {
		t.Fatal("independently rebuilt helper role/source identity refused")
	}
	t.Logf("reference_helper_sha256=%s source=%s", helperIdentity.SHA256, helperIdentity.BuildVCSRevision)
	// This helper has manual, dependent cleanup: automatic helper cleanup would
	// release its keeper if a later constructor retained an uncertain worker.
	treeHelper, err = ProtectLinuxInputTreeHelper(t.Context(), treeRequest)
	if err != nil {
		t.Fatal("qualified H2k helper prerequisite refused; no skip")
	}
	git, err = ProtectLinuxExecutionGit(t.Context(), parent, candidateRequest.GitBinary, treeHelper)
	if err != nil {
		t.Fatal("protected seven-image Git prerequisite refused")
	}
	request := LinuxExecutionGoBuildRequest{Git: git, RepositoryRoot: candidateRequest.RepositoryRoot,
		PlanSourceCommit: candidateRequest.PlanSourceCommit, IntegratedMainCommit: candidateRequest.IntegratedMainCommit,
		SourceCommit: candidateRequest.SourceCommit, GoRoot: candidateRequest.GoRoot, ModuleCache: candidateRequest.ModuleCache}
	owner, err = ProtectLinuxExecutionGoBuildInputs(t.Context(), parent, request, treeHelper)
	if err != nil {
		if owner != nil {
			t.Logf("Go constructor refusal private_fixture=%s stage=%s inventory=%+v", base, owner.preparationStep, owner.Inventory())
		}
		t.Fatal("protected Go input constructor refused; exact private fixture retained until joined", base)
	}
	inventory := owner.Inventory()
	input, err := owner.tree.Check(t.Context())
	if err != nil || !owner.tree.joined || !owner.tree.empty || inventory.Entries <= 0 || inventory.Files <= 0 || inventory.Bytes <= 0 ||
		input.Entries != uint64(inventory.Entries) || input.Files != uint64(inventory.Files) || input.Bytes != uint64(inventory.Bytes) {
		t.Fatal("single constructor inventory/tree reconciliation refused")
	}
	for _, tree := range []*LinuxExecutionInputTree{git.tree, owner.tree} {
		members, err := t4013.PrivateProcessSessionMembers(tree.pid)
		if tree.pid <= 0 || !tree.joined || !tree.empty || err != nil || members != 0 {
			t.Fatal("completed constructor operational session not independently empty")
		}
		t.Logf("constructor pid=%d actual_root_joined=%t session_empty=%t", tree.pid, tree.joined, tree.empty)
	}
	if _, err := os.Lstat(filepath.Join(owner.PrivateDirectory(), "payload")); !os.IsNotExist(err) {
		t.Fatal("mutable preparation payload remains")
	}
	if _, err := owner.CheckGo(t.Context()); err == nil {
		t.Fatal("Go identity published before a joined actual reference build")
	}
	expectedInfo, err := buildinfo.ReadFile(candidateRequest.Binary)
	if err != nil {
		t.Fatal("actual author candidate BuildInfo unavailable")
	}
	candidate, err := os.Open(candidateRequest.Binary)
	if err != nil {
		t.Fatal("actual author candidate unavailable")
	}
	expectedDigest, digestErr := digestExecutionReferenceFD(t.Context(), candidate)
	closeErr := candidate.Close()
	if digestErr != nil || closeErr != nil {
		t.Fatal("actual author candidate digest unavailable")
	}
	goDriver, err := os.Open(filepath.Join(candidateRequest.GoRoot, "bin", "go"))
	if err != nil {
		t.Fatal("selected original Go driver unavailable")
	}
	expectedGoDigest, digestErr := digestExecutionReferenceFD(t.Context(), goDriver)
	closeErr = goDriver.Close()
	if digestErr != nil || closeErr != nil {
		t.Fatal("selected original Go driver digest unavailable")
	}
	tool, err = owner.ProtectReferenceTool(t.Context(), parent, "t422-author", candidateRequest.Binary, helper)
	if err != nil {
		logNativeGoReferenceFailure(t, owner, base)
		t.Fatal("required implemented protected author reference build refused; no skip")
	}
	if tool.referenceInputs != owner {
		t.Fatal("tool lacks distinct Linux protected-input lineage")
	}
	identity, err := tool.Check(t.Context(), "t422-author")
	if err != nil || identity.SHA256 != expectedDigest || identity.BuildVCSRevision != candidateRequest.SourceCommit || identity.Provenance != "go-build-info-vcs-v1" {
		t.Fatal("protected author typed identity differs from actual candidate")
	}
	if tool.WithImage(t.Context(), "t422-author", func(_ ExecutionToolIdentity, image *os.File) error {
		actual, err := buildinfo.Read(image)
		if err != nil || !reflect.DeepEqual(expectedInfo, actual) {
			return ErrExecutionToolCustody
		}
		return nil
	}) != nil {
		t.Fatal("protected author full BuildInfo differs from actual candidate")
	}
	goIdentity, err := owner.CheckGo(t.Context())
	if err != nil || goIdentity.Role != "go" || goIdentity.Version != "go version go1.26.5 linux/amd64" || goIdentity.SHA256 != expectedGoDigest || goIdentity.SHA256 != owner.last.goDigest ||
		owner.last.toolDigest != expectedDigest || !owner.last.joined || !owner.last.empty || owner.last.pid <= 0 || owner.Inventory() != inventory || owner.last.input != input {
		t.Fatal("actual joined Go driver/completion/inventory identity refused")
	}
	members, err := t4013.PrivateProcessSessionMembers(owner.last.pid)
	if err != nil || members != 0 {
		t.Fatal("completed reference operational session not independently empty")
	}
	t.Logf("reference pid=%d actual_root_joined=%t session_empty=%t entries=%d files=%d bytes=%d go_sha256=%s author_sha256=%s", owner.last.pid,
		owner.last.joined, owner.last.empty, input.Entries, input.Files, input.Bytes, goIdentity.SHA256, identity.SHA256)
	cleanup()
	if !cleaned {
		t.Fatal("joined close barrier did not release exact fixture")
	}
	if _, err := tool.Check(t.Context(), "t422-author"); err == nil {
		t.Fatal("closed tool remained usable")
	}
	if owner.Check(t.Context()) == nil {
		t.Fatal("closed Go owner remained usable")
	}
	if _, err := owner.CheckGo(t.Context()); err == nil {
		t.Fatal("closed Go owner retained published driver identity")
	}
	if _, err := os.Lstat(base); !os.IsNotExist(err) {
		t.Fatal("exact private fixture remains after joined cleanup")
	}
}

func readNativeGoReferenceRequest(t *testing.T, environment string) ReferenceToolRequest {
	t.Helper()
	path := os.Getenv(environment)
	if !executionGitAbsolutePath(path) {
		t.Fatal("required exact closed reference request missing; no skip", environment)
	}
	if canonical, err := filepath.EvalSymlinks(path); err != nil || canonical != path {
		t.Fatal("canonical reference request required", environment)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("required reference request unreadable", environment)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	closeErr := file.Close()
	var request ReferenceToolRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if readErr != nil || closeErr != nil || len(raw) == 0 || len(raw) > 16<<10 || decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		t.Fatal("bounded closed reference request malformed", environment)
	}
	return request
}

func logNativeGoReferenceFailure(t *testing.T, owner *LinuxExecutionGoBuildCustody, base string) {
	t.Helper()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	attempt := owner.active
	if attempt == nil {
		t.Logf("reference refusal private_fixture=%s active=false", base)
		return
	}
	select {
	case <-attempt.done:
		pid, joined, empty := 0, false, false
		if attempt.tree != nil {
			pid, joined, empty = attempt.tree.pid, attempt.tree.joined, attempt.tree.empty
		}
		t.Logf("reference refusal private_fixture=%s done=true step=%s pid=%d actual_root_joined=%t session_empty=%t wait_error=%q helper_stderr=%q command_stderr=%q", base,
			attempt.step, pid, joined, empty, attempt.waitError, attempt.stderr, attempt.commandStderr)
	default:
		// Worker fields are mutable until done. Keepers and the exact prefix stay
		// retained; no speculative PID/session observation is made here.
		t.Logf("reference refusal private_fixture=%s done=false", base)
	}
}
