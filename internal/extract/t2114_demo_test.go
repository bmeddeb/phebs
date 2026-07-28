package extract

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

type t2114DemoReceipt struct {
	SchemaVersion string `json:"schema_version"`
	Repository    string `json:"repository"`
	Commit        string `json:"commit"`
}

func TestT2114CommittedBundleProducesDeclarationAndCallerLineage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	fixtureRoot := filepath.Join(
		"..", "..", "docs", "fixtures", "change-workbench",
	)
	receiptBytes, err := os.ReadFile(filepath.Join(fixtureRoot, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt t2114DemoReceipt
	decoder := json.NewDecoder(strings.NewReader(string(receiptBytes)))
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SchemaVersion != "t21.14-workbench-closure-receipt-v1" {
		t.Fatalf("receipt schema = %q", receipt.SchemaVersion)
	}

	ctx := context.Background()
	dataDir := t.TempDir()
	mirror := phebssync.RepoDir(dataDir, receipt.Repository)
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle, err := filepath.Abs(filepath.Join(
		fixtureRoot, "t2114-workbench-closure.bundle",
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := phebssync.Mirror(ctx, bundle, mirror); err != nil {
		t.Fatalf("mirror fixture bundle: %v", err)
	}
	if head := t2114Git(t, mirror, "rev-parse", "HEAD"); head != receipt.Commit {
		t.Fatalf("fixture HEAD = %s, want %s", head, receipt.Commit)
	}

	extractFacts := func(extractor sdk.Extractor) ([]sdk.Fact, sdk.Coverage) {
		t.Helper()
		corpus := newVerifiedCorpus(
			GitCorpus(dataDir).New(receipt.Repository, receipt.Commit),
		)
		if err := corpus.Inventory(ctx, extractor.Candidate); err != nil {
			t.Fatalf("%s inventory: %v", extractor.Domain(), err)
		}
		var facts []sdk.Fact
		coverage, err := extractor.Extract(ctx, corpus, func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		})
		corpus.Close()
		if err != nil {
			t.Fatalf("%s extraction: %v", extractor.Domain(), err)
		}
		return facts, coverage
	}

	protoFacts, _ := extractFacts(protodecl.New())
	grpcCallerFacts, grpcCallerCoverage := extractFacts(gocaller.NewGRPC())
	grpcConsumerFacts, _ := extractFacts(grpcgo.New())
	thriftFacts, _ := extractFacts(thriftdecl.New())
	thriftCallerFacts, thriftCallerCoverage := extractFacts(gocaller.NewThrift())
	thriftConsumerFacts, _ := extractFacts(thriftgo.New())

	assertT2114LineageJoin(
		t,
		protoFacts,
		"demo.search.v1.CodeSearch/Search",
		grpcCallerFacts,
		"/demo.search.v1.CodeSearch/Search",
		"src/checkout/exact_caller.go",
	)
	assertT2114LineageJoin(
		t,
		protoFacts,
		"demo.search.v1.CodeSearch/SearchV2",
		grpcCallerFacts,
		"/demo.search.v1.CodeSearch/SearchV2",
		"src/migration/replacement_caller.go",
	)
	assertT2114LineageJoin(
		t,
		thriftFacts,
		"search.CodeSearch/search",
		thriftCallerFacts,
		"/search.CodeSearch/search",
		"src/thrift/client.go",
	)
	assertT2114PredicateObject(
		t, grpcConsumerFacts, "REGISTERS_GRPC_SERVICE",
		"demo.search.v1.CodeSearch",
	)
	assertT2114PredicateObject(
		t, thriftConsumerFacts, "REGISTERS_THRIFT_SERVICE",
		"search.CodeSearch",
	)
	if !containsT2114Protocol(grpcCallerCoverage.Protocols, "attribution-") ||
		!containsT2114Protocol(thriftCallerCoverage.Protocols, "attribution-") {
		t.Fatalf(
			"caller coverage omitted immutable attribution: grpc=%v thrift=%v",
			grpcCallerCoverage.Protocols,
			thriftCallerCoverage.Protocols,
		)
	}
}

func assertT2114LineageJoin(
	t *testing.T,
	declarations []sdk.Fact,
	declarationObject string,
	callers []sdk.Fact,
	callerObject string,
	callerPath string,
) {
	t.Helper()
	declaration := findT2114Fact(
		t, declarations, "DECLARES_OPERATION", declarationObject,
	)
	caller := findT2114Fact(t, callers, "CALLS_OPERATION", callerObject)
	if declaration.Assertion.Lineage == "" ||
		caller.Assertion.Lineage != declaration.Assertion.Lineage ||
		caller.Path != callerPath ||
		!strings.Contains(caller.Assertion.Detail, `"resolution":"syntax"`) ||
		!strings.Contains(caller.Assertion.Detail, `"generated_path"`) {
		t.Fatalf(
			"caller did not join retained declaration: declaration=%+v caller=%+v",
			declaration,
			caller,
		)
	}
}

func assertT2114PredicateObject(
	t *testing.T,
	facts []sdk.Fact,
	predicate string,
	object string,
) {
	t.Helper()
	_ = findT2114Fact(t, facts, predicate, object)
}

func findT2114Fact(
	t *testing.T,
	facts []sdk.Fact,
	predicate string,
	object string,
) sdk.Fact {
	t.Helper()
	for _, fact := range facts {
		if fact.Assertion.Predicate == predicate &&
			fact.Assertion.Object == object {
			return fact
		}
	}
	t.Fatalf("missing %s %s in %+v", predicate, object, facts)
	return sdk.Fact{}
}

func containsT2114Protocol(protocols []string, prefix string) bool {
	for _, protocol := range protocols {
		if strings.HasPrefix(protocol, prefix) {
			return true
		}
	}
	return false
}

func t2114Git(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}
