package t324

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t323"
)

func TestRetainedReceiptIsClosedAndInputBound(t *testing.T) {
	root := repositoryRoot(t)
	encoded, err := os.ReadFile(filepath.Join(root, "spike/t324/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeStrict[Receipt](encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	inputs, _, err := readInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Inputs != inputs {
		t.Fatalf("retained input bindings = %+v, want %+v", receipt.Inputs, inputs)
	}
	for _, forbidden := range []string{"/Users/", "phebs-private", "bootstrap_password", "clone_url"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("receipt contains forbidden private field or path %q", forbidden)
		}
	}
}

func TestNeutralCorrectnessReplaysAgainstZoekt(t *testing.T) {
	root := repositoryRoot(t)
	bin := zoektBinary(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	got, cost, err := measureNeutral(ctx, root, t.TempDir(), bin)
	if err != nil {
		t.Fatal(err)
	}
	retainedBytes, err := os.ReadFile(filepath.Join(root, "spike/t324/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := DecodeStrict[Receipt](retainedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, retained.Correctness) {
		t.Fatalf("replayed correctness = %+v, want %+v", got, retained.Correctness)
	}
	if cost.ResultDigest != retained.NeutralCost.ResultDigest || cost.Build.ShardCount < 1 ||
		cost.Reader.MMapCount != cost.Build.ShardCount {
		t.Fatalf("replayed neutral semantics/cost = %+v", cost)
	}
}

func TestServiceCompilerPlacesMembershipInsideZoektQuery(t *testing.T) {
	compiled, err := compileServiceQuery("Needle", "r0", []t323.Placement{
		{Path: "services/orders", Role: "primary"},
		{Path: "shared/trace/trace.go", Role: "shared"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !queryContainsPathPredicate(compiled) {
		t.Fatalf("compiled query has no filename predicate: %s", compiled)
	}
	text := compiled.String()
	for _, fragment := range []string{`branch="r0"`, "services/orders", "shared/trace/trace"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("compiled query %s omits %q", text, fragment)
		}
	}
}

func TestZoektChildMatchesCurrentModulePin(t *testing.T) {
	root := repositoryRoot(t)
	if err := verifyZoektBinary(zoektBinary(t, root), root); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProfileContentsRemainInventoryExact(t *testing.T) {
	for _, services := range []int{1_000, 5_000} {
		profile, err := t323.GenerateLoadProfile(services)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := t323.GenerateLoadProfileContents(services)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyProfileContents(profile, contents); err != nil {
			t.Fatalf("%d services: %v", services, err)
		}
	}
}

func TestNoOpIdentityUsesPublishedShardGeneration(t *testing.T) {
	root := repositoryRoot(t)
	repository := filepath.Join(t.TempDir(), "repository")
	if err := materializeRepository(repository, map[string][]byte{
		"main.go": []byte("package main\n\nconst T324NoOp = true\n"),
	}); err != nil {
		t.Fatal(err)
	}
	desired, err := runGit(repository, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(t.TempDir(), "index")
	if _, err := buildZoekt(context.Background(), zoektBinary(t, root), repository, indexDir, []string{"main"}); err != nil {
		t.Fatal(err)
	}
	active, err := publishedBranchVersion(indexDir, repositoryName, "main")
	if err != nil {
		t.Fatal(err)
	}
	if active != strings.TrimSpace(desired) {
		t.Fatalf("published branch version = %q, want desired HEAD %q", active, strings.TrimSpace(desired))
	}
}

func TestReceiptStrictnessAndDecisionValidation(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join(repositoryRoot(t), "spike/t324/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := append([]byte(nil), encoded[:len(encoded)-2]...)
	withUnknown = append(withUnknown, []byte(",\n  \"private_path\": \"forbidden\"\n}\n")...)
	if _, err := DecodeStrict[Receipt](withUnknown); err == nil {
		t.Fatal("strict decoder accepted an unknown field")
	}
	receipt, err := DecodeStrict[Receipt](encoded)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Decisions[0].Decision = "no_go"
	if err := ValidateReceipt(receipt); err == nil {
		t.Fatal("validator accepted a missing direct-topology GO")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func zoektBinary(t *testing.T, root string) string {
	t.Helper()
	if configured := os.Getenv("PHEBS_ZOEKT_GIT_INDEX"); configured != "" {
		return configured
	}
	candidate := filepath.Join(root, "bin", "zoekt-git-index")
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return candidate
	}
	if candidate, err := exec.LookPath("zoekt-git-index"); err == nil {
		return candidate
	}
	candidate = filepath.Join(t.TempDir(), "zoekt-git-index")
	command := exec.Command("go", "build", "-o", candidate, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build zoekt-git-index: %v\n%s", err, output)
	}
	return candidate
}
