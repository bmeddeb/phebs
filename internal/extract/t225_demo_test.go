package extract_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftfield"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type t225DemoReceipt struct {
	Schema        string   `json:"schema"`
	Repository    string   `json:"repository"`
	Commit        string   `json:"commit"`
	BundleSHA256  string   `json:"bundle_sha256"`
	BundleBytes   int      `json:"bundle_bytes"`
	IndexSHA256   string   `json:"index_sha256"`
	IndexBytes    int      `json:"index_bytes"`
	Documents     int      `json:"documents"`
	Occurrences   int      `json:"occurrences"`
	Definitions   int      `json:"definitions"`
	References    int      `json:"references"`
	Lineage       string   `json:"lineage"`
	Message       string   `json:"message"`
	FieldNumber   int      `json:"field_number"`
	Object        string   `json:"object"`
	Citation      string   `json:"citation"`
	IndexAuthor   string   `json:"index_author"`
	AuthorVersion string   `json:"index_author_version"`
	AuthorCommand []string `json:"authoring_command"`
	Observation   string   `json:"observation"`
}

func TestT225CommittedBundleExercisesThriftFieldZero(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	fixtureRoot := filepath.Join("..", "..", "docs", "fixtures", "thrift-field")
	var receipt t225DemoReceipt
	receiptBytes, err := os.ReadFile(filepath.Join(fixtureRoot, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(receiptBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("decode fixture receipt: %v", err)
	}
	if receipt.Schema != "t22.5-thrift-field-demo-v1" ||
		receipt.IndexAuthor != "phebs-t225-authored-preparer" ||
		receipt.AuthorVersion != "1" ||
		!reflect.DeepEqual(receipt.AuthorCommand, []string{
			"go", "run", "./docs/fixtures/thrift-field/cmd/author",
		}) ||
		receipt.Documents != 2 || receipt.Occurrences != 2 ||
		receipt.Definitions != 1 || receipt.References != 1 ||
		receipt.FieldNumber != 0 ||
		receipt.Message != "health.Meta_Health_Result" ||
		receipt.Citation != "consumer/use.go:6" ||
		receipt.Observation == "" {
		t.Fatalf("unexpected fixture receipt: %+v", receipt)
	}

	bundle := filepath.Join(fixtureRoot, "t225-thrift-field-demo.bundle")
	assertT225FileDigest(t, bundle, receipt.BundleSHA256, receipt.BundleBytes)
	assertT225FileDigest(
		t,
		filepath.Join(fixtureRoot, "repo", "index.scip"),
		receipt.IndexSHA256,
		receipt.IndexBytes,
	)

	ctx := context.Background()
	dataDir := t.TempDir()
	mirror := phebssync.RepoDir(dataDir, receipt.Repository)
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	absoluteBundle, err := filepath.Abs(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := phebssync.Mirror(ctx, absoluteBundle, mirror); err != nil {
		t.Fatalf("mirror fixture bundle: %v", err)
	}
	if head := t225Git(t, mirror, "rev-parse", "HEAD"); head != receipt.Commit {
		t.Fatalf("fixture HEAD = %s, want %s", head, receipt.Commit)
	}

	corpus := extract.GitCorpus(dataDir).New(receipt.Repository, receipt.Commit)
	var facts []sdk.Fact
	coverage, err := thriftfield.New().Extract(ctx, corpus, func(fact sdk.Fact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("extract committed demo: %v", err)
	}
	if !reflect.DeepEqual(coverage.Protocols, []string{
		"apache-thrift-tag-v1",
		"scip",
		"scip-package-lineage-v1",
		"thriftrw-module-digest-v1",
	}) {
		t.Fatalf("coverage protocols = %v", coverage.Protocols)
	}
	if len(facts) != 1 {
		t.Fatalf("field-zero facts = %+v", facts)
	}
	fact := facts[0]
	if fact.Assertion.Predicate != "REFERENCES_THRIFT_FIELD" ||
		fact.Assertion.Object != receipt.Object ||
		fact.Assertion.Lineage != receipt.Lineage ||
		fact.Assertion.Tier != "exact" ||
		fact.Path+":"+strconv.Itoa(fact.StartLine) != receipt.Citation ||
		!strings.Contains(fact.Assertion.Detail, `"generator":"thriftrw"`) ||
		!strings.Contains(fact.Assertion.Detail, `"source_binding":"module_digest"`) {
		t.Fatalf("field-zero fact = %+v", fact)
	}
	blob, err := corpus.Read(ctx, fact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := blob.Content[fact.Atom.StartByte:fact.Atom.EndByte]; got != "GetSuccess" {
		t.Fatalf("citation bytes = %q, want GetSuccess", got)
	}
}

func assertT225FileDigest(t *testing.T, path, wantDigest string, wantBytes int) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != wantDigest ||
		len(content) != wantBytes {
		t.Fatalf("%s = %s / %d bytes, want %s / %d", path, got, len(content), wantDigest, wantBytes)
	}
}

func t225Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
