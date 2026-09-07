//go:build darwin

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

// This opt-in gate proves the private overlay image and its native go-git
// offer boundary, not server forwarding, whole-phase accounting or readiness.
// The supplied and reference builds use independently located fresh caches
// through the existing custody verifier; its acceptance compares entire images.
func TestZoektOfferOptionalNativeRehearsal(t *testing.T) {
	if os.Getenv("PHEBS_T422_ZOEKT_OFFER_REHEARSAL") != "1" {
		t.Skip("requires explicit serial private overlay builds and tiny native indexing")
	}
	requireExternalToolFrozenHost(t)
	repository := os.Getenv("PHEBS_T422_PRODUCTION_REPOSITORY")
	commit := os.Getenv("PHEBS_T422_PRODUCTION_COMMIT")
	goRoot := os.Getenv("PHEBS_T422_PRODUCTION_GOROOT")
	moduleCache := os.Getenv("PHEBS_T422_PRODUCTION_MODULE_CACHE")
	gitBinary := os.Getenv("PHEBS_T422_PRODUCTION_GIT")
	if !validCommit(commit) || !executionGitAbsolutePath(repository) || !executionGitAbsolutePath(goRoot) ||
		!executionGitAbsolutePath(moduleCache) || !executionGitAbsolutePath(gitBinary) {
		t.Fatal("explicit clean source, native Git, SDK and offline module cache required")
	}
	parent, err := os.MkdirTemp("", "t422-zoekt-offer-rehearsal-")
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
			t.Logf("retained native overlay rehearsal custody (no automatic retry): %s", parent)
			return
		}
		current, err := os.Lstat(parent)
		if err != nil || !os.SameFile(parentInfo, current) {
			t.Error("native overlay rehearsal parent changed; retaining it")
			return
		}
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	// Build allowance only; this is not a prospective ceremony phase budget.
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	git, err := ProtectExecutionGit(ctx, parent, gitBinary)
	if git != nil {
		defer func() {
			if err := git.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ProtectExecutionGoBuildInputs(ctx, parent, ExecutionGoBuildRequest{Git: git, RepositoryRoot: repository,
		PlanSourceCommit: commit, IntegratedMainCommit: commit, SourceCommit: commit, GoRoot: goRoot, ModuleCache: moduleCache})
	if inputs != nil {
		defer func() {
			if err := inputs.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(parent, "supplied-builds")
	for _, path := range []string{workspace, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	supplied := productionRehearsalBuildSchema(t, ctx, inputs, workspace, "zoekt-git-index", PlanV3Schema)
	suppliedDigest, err := executableidentity.Digest(supplied)
	if err != nil {
		t.Fatal(err)
	}
	tool, err := inputs.ProtectReferenceToolV3(ctx, parent, "zoekt-git-index", supplied)
	if tool != nil {
		defer func() {
			if err := tool.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	if err != nil {
		t.Fatalf("independently located complete overlay image comparison: %v", err)
	}
	identity, protected, err := tool.Check(ctx, "zoekt-git-index")
	policy := frozenToolPolicy()
	policy.ZoektBuildRecipe = zoektOfferBuildRecipe
	if err != nil || protected == supplied || identity.SHA256 != suppliedDigest ||
		identity.Provenance != zoektOfferProvenance || identity.BuildRecipeSHA256 != zoektOfferRecipe(policy, commit) {
		t.Fatal("independent overlay image/provenance binding differs")
	}
	t.Logf("independent private overlay images byte-identical: %s", identity.SHA256)
	source, objects := zoektOfferNativeFixture(t, git, parent)
	zoektOfferNativeAttempt(t, ctx, git, tool, parent, source, "healthy", 3, false)
	// A missing blob is deliberately skipped upstream, so corrupt the existing
	// second loose object's compression stream instead. The sorted tree remains
	// intact; a.txt is readable, b.txt fails at createDocument, c.txt is unreached.
	second := filepath.Join(source, "objects", objects[1][:2], objects[1][2:])
	info, err := os.Lstat(second)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		t.Fatal("second neutral loose blob has unexpected shape")
	}
	if err := os.Chmod(second, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("not a zlib object\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zoektOfferNativeAttempt(t, ctx, git, tool, parent, source, "corrupt-second", 2, true)
	for _, position := range []int{0, 2} {
		got := gitCustodyTestRun(t, git, parent, nil, "-C", source, "cat-file", "-p", objects[position])
		if string(got) != fmt.Sprintf("neutral native input %d\n", position) {
			t.Fatal("first or unreached third blob changed")
		}
	}
	if err := inputs.Check(ctx); err != nil {
		t.Fatal(err)
	}
	// Register exact-descriptor fixture cleanup only after both native attempts
	// and independent reproduction pass. Close itself deliberately never thaws.
	gitCustodyTestCleanup(t, git)
	goBuildTestCleanup(t, inputs)
	inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: "zoekt-git-index"}})
	completed = true
}

func zoektOfferNativeFixture(t *testing.T, git *ExecutionGitCustody, parent string) (string, [3]string) {
	t.Helper()
	source := filepath.Join(parent, "source.git")
	gitCustodyTestRun(t, git, parent, nil, "init", "--bare", "--template=", "--initial-branch=main", source)
	var objects [3]string
	var tree strings.Builder
	for index := range objects {
		content := fmt.Sprintf("neutral native input %d\n", index)
		objects[index] = strings.TrimSpace(string(gitCustodyTestRun(t, git, parent, strings.NewReader(content),
			"-C", source, "hash-object", "-w", "--stdin")))
		if !validCommit(objects[index]) {
			t.Fatal("neutral blob identity invalid")
		}
		fmt.Fprintf(&tree, "100644 blob %s\t%c.txt\n", objects[index], 'a'+index)
	}
	treeID := strings.TrimSpace(string(gitCustodyTestRun(t, git, parent, strings.NewReader(tree.String()), "-C", source, "mktree")))
	commit := strings.TrimSpace(string(gitCustodyTestRun(t, git, parent, strings.NewReader("neutral native offer fixture\n"),
		"-C", source, "-c", "user.name=Neutral", "-c", "user.email=neutral@example.invalid", "commit-tree", treeID)))
	if !validCommit(treeID) || !validCommit(commit) {
		t.Fatal("neutral tree or commit identity invalid")
	}
	gitCustodyTestRun(t, git, parent, nil, "-C", source, "update-ref", "refs/heads/main", commit)
	return source, objects
}

func zoektOfferNativeAttempt(t *testing.T, parentCtx context.Context, git *ExecutionGitCustody, tool *ExecutionToolCustody,
	parent, source, name string, want uint64, failed bool,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parentCtx, time.Minute)
	defer cancel()
	_, binary, err := tool.Check(ctx, "zoekt-git-index")
	if err != nil {
		t.Fatal(err)
	}
	environment, err := git.Environment(ctx, parent, parent)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(parent, name+"-index")
	if err := os.Mkdir(index, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "-index", index, "-incremental=false", "-submodules=false",
		"-file_limit=2097152", "-shard_limit=104857600", "-max_trigram_count=20000", source)
	command.Dir, command.Env = parent, append(environment, "PHEBS_T422_INDEX_OFFERS=v1", "ZOEKT_DISABLE_CATFILE_BATCH=true")
	command.WaitDelay = time.Second
	if err := prepareReferenceCommand(command); err != nil {
		t.Fatal(err)
	}
	output := checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}
	command.Stdout, command.Stderr = &output, &output
	runErr := command.Run() // Wait joins the shared bounded output pump before reading.
	raw := output.buffer.String()
	if err := os.WriteFile(filepath.Join(parent, name+".log"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if ctx.Err() != nil || command.ProcessState == nil || !command.ProcessState.Exited() ||
		(!failed && runErr != nil) || (failed && (!errors.As(runErr, &exit) || exit.ExitCode() <= 0 || !strings.Contains(raw, "zlib"))) {
		t.Fatalf("native %s attempt has unexpected exit or cause: %v", name, runErr)
	}
	// Test-local native wire assertion only: no synthetic server phase or input
	// binding is manufactured from this raw child stream.
	if !strings.HasSuffix(raw, "\n") {
		t.Fatal("native child output has a partial tail")
	}
	var began, ended bool
	var offers uint64
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		switch {
		case line == "ZIB1" && !began && !ended:
			began = true
		case line == "ZI1" && began && !ended:
			offers++
		case strings.HasPrefix(line, "ZIE1:") && began && !ended:
			if line != "ZIE1:"+strconv.FormatUint(offers, 10) {
				t.Fatal("native neutral end tally differs from actual offer tokens")
			}
			ended = true
		case strings.Contains(line, "ZI"):
			t.Fatal("native offer token is malformed or out of order")
		}
	}
	if !began || !ended || offers != want {
		t.Fatalf("native %s offers: begin=%t end=%t count=%d want=%d", name, began, ended, offers, want)
	}
	t.Logf("native %s: joined exit=%d actual offers=%d neutral terminal=%d", name, command.ProcessState.ExitCode(), offers, offers)
}
