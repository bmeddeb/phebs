package gocaller_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/spike/t201"
)

type memoryCorpus struct {
	repo        string
	commit      string
	files       map[string][]byte
	attribution sdk.AttributionSource
}

func (c memoryCorpus) RepoName() string { return c.repo }
func (c memoryCorpus) Commit() string   { return c.commit }
func (c memoryCorpus) WalkFiles(
	ctx context.Context,
	visit func(string) error,
) error {
	paths := make([]string, 0, len(c.files))
	for filePath := range c.files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(filePath); err != nil {
			return err
		}
	}
	return nil
}

func (c memoryCorpus) Read(_ context.Context, filePath string) (sdk.Blob, error) {
	content, ok := c.files[filePath]
	if !ok {
		return sdk.Blob{}, errors.New("missing fixture path")
	}
	sum := sha256.Sum256(content)
	return sdk.Blob{
		Content: string(content),
		Digest:  "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func (c memoryCorpus) ReadSCIPIndex(ctx context.Context) (sdk.Blob, error) {
	return c.Read(ctx, "index.scip")
}

func (c memoryCorpus) AttributionSource(context.Context) (sdk.AttributionSource, error) {
	return c.attribution, nil
}

type fixtureAttribution struct {
	repo      string
	relations map[string]sdk.GeneratedFromAttribution
}

func (f fixtureAttribution) Available() bool { return true }
func (f fixtureAttribution) Digest() string  { return "sha256:" + strings.Repeat("a", 64) }
func (f fixtureAttribution) Provenance() []sdk.SnapshotProvenance {
	return nil
}
func (f fixtureAttribution) ClassifyPath(string) sdk.PathClassification {
	return sdk.PathClassification{State: sdk.AttributionStateUnavailable}
}
func (f fixtureAttribution) ConsumerUnits(string, int) sdk.ConsumerUnitAttribution {
	return sdk.ConsumerUnitAttribution{State: sdk.AttributionStateUnavailable}
}
func (f fixtureAttribution) GeneratedFrom(
	protocol, generatedPath, generatorRelativePath string,
) sdk.GeneratedFromAttribution {
	key := protocol + "\x00" + generatedPath + "\x00" + generatorRelativePath
	if relation, ok := f.relations[key]; ok {
		return relation
	}
	return sdk.GeneratedFromAttribution{
		State: sdk.AttributionStateUnavailable, Reason: "no_snapshot_mapping",
	}
}

func TestTypedSCIPCallsMatchFrozenSmallOracle(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	attribution := frozenAttribution(profile)
	corpus := memoryCorpus{
		repo: "synthetic.invalid/mono", commit: strings.Repeat("1", 40),
		files: profile.Files, attribution: attribution,
	}
	tests := []struct {
		name      string
		extractor sdk.Extractor
		protocol  string
		want      int
	}{
		{"grpc", gocaller.NewGRPC(), "grpc", 4},
		{"thrift", gocaller.NewThrift(), "thrift", 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			run := func() ([]sdk.Fact, sdk.Coverage) {
				t.Helper()
				var facts []sdk.Fact
				coverage, err := testCase.extractor.Extract(
					context.Background(), corpus,
					func(fact sdk.Fact) error {
						facts = append(facts, fact)
						return nil
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				return facts, coverage
			}
			facts, coverage := run()
			secondFacts, secondCoverage := run()
			if !reflect.DeepEqual(facts, secondFacts) ||
				!reflect.DeepEqual(coverage, secondCoverage) {
				t.Fatal("typed caller extraction is not byte-order deterministic")
			}
			if coverage.UnresolvedCount != 0 || len(facts) != testCase.want {
				t.Fatalf("coverage/facts = %+v / %d: %+v", coverage, len(facts), facts)
			}
			if !slicesContain(
				coverage.Protocols,
				"attribution-"+strings.Repeat("a", 64),
			) {
				t.Fatalf("coverage does not bind attribution: %+v", coverage)
			}
			oracleByPath := make(map[string]t201.CallExpectation)
			for _, call := range profile.Oracle.Calls {
				if call.Protocol == testCase.protocol && call.Resolution == t201.ResolutionKnown {
					oracleByPath[call.Path] = call
				}
			}
			for _, fact := range facts {
				want, ok := oracleByPath[fact.Path]
				if !ok {
					t.Fatalf("unexpected typed fact: %+v", fact)
				}
				if fact.Assertion.Predicate != "CALLS_OPERATION" ||
					fact.Assertion.Object != want.CanonicalOperation ||
					fact.Assertion.Lineage == "" ||
					fact.Assertion.Tier != "derived" ||
					fact.Atom.StartByte != want.StartByte ||
					fact.Atom.EndByte != want.EndByte ||
					string(profile.Files[fact.Path][fact.Atom.StartByte:fact.Atom.EndByte]) == "" {
					t.Fatalf("typed fact = %+v, oracle = %+v", fact, want)
				}
			}
		})
	}
}

func TestTypedSCIPMappingAbstentionAndInputFailure(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	corpus := memoryCorpus{
		repo: "synthetic.invalid/mono", commit: strings.Repeat("2", 40),
		files: profile.Files, attribution: fixtureAttribution{},
	}
	var facts []sdk.Fact
	coverage, err := gocaller.NewGRPC().Extract(
		context.Background(), corpus,
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnresolvedCount != 4 || len(facts) != 4 {
		t.Fatalf("missing mapping coverage/facts = %+v / %d: %+v", coverage, len(facts), facts)
	}
	for _, fact := range facts {
		if fact.Assertion.Predicate != "UNRESOLVED_CALLER" ||
			fact.Assertion.Object != "/synthetic.orders.v1.Orders/Get" ||
			!strings.Contains(fact.Assertion.Detail, "no_snapshot_mapping") {
			t.Fatalf("mapping abstention = %+v", fact)
		}
	}

	conflictingAttribution := frozenAttribution(profile)
	key := "grpc\x00gen/proto/orders/v1/orders_grpc.pb.go\x00orders/v1/orders.proto"
	conflict := conflictingAttribution.relations[key]
	conflict.State = sdk.AttributionStateAmbiguous
	conflict.Reason = "multiple_declaration_paths"
	conflict.Candidates = append(conflict.Candidates, sdk.GeneratedFromCandidate{
		Protocol:              "grpc",
		GeneratedPath:         "gen/proto/orders/v1/orders_grpc.pb.go",
		GeneratorRelativePath: "orders/v1/orders.proto",
		DeclarationPath:       "vendor/proto/orders/v1/orders.proto",
		DeclarationLineage:    "provisional_repo_path_v1_" + strings.Repeat("b", 64),
	})
	conflictingAttribution.relations[key] = conflict
	facts = nil
	coverage, err = gocaller.NewGRPC().Extract(
		context.Background(),
		memoryCorpus{
			repo: corpus.repo, commit: corpus.commit, files: profile.Files,
			attribution: conflictingAttribution,
		},
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnresolvedCount != 4 || len(facts) != 4 {
		t.Fatalf("conflicting mapping coverage/facts = %+v / %d", coverage, len(facts))
	}
	for _, fact := range facts {
		if fact.Assertion.Predicate != "UNRESOLVED_CALLER" ||
			!strings.Contains(fact.Assertion.Detail, "multiple_declaration_paths") {
			t.Fatalf("conflicting mapping abstention = %+v", fact)
		}
	}

	duplicate := cloneFiles(profile.Files)
	const callerPath = "src/checkout/caller.go"
	duplicate[callerPath] = []byte(
		string(duplicate[callerPath]) +
			"\nfunc LoadAgain(ctx any, client interface{ Get(any, any, ...any) (any, error) }) {\n" +
			"\t_, _ = client.Get(ctx, \"order-5\")\n}\n",
	)
	duplicate["index.scip"] = appendSCIPReference(
		t, duplicate["index.scip"], callerPath, t201.ProtoGetSymbol,
		string(duplicate[callerPath]), "Get(ctx", 1,
	)
	facts = nil
	coverage, err = gocaller.NewGRPC().Extract(
		context.Background(),
		memoryCorpus{
			repo: corpus.repo, commit: corpus.commit, files: duplicate,
			attribution: fixtureAttribution{},
		},
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnresolvedCount != 5 || len(facts) != 5 {
		t.Fatalf("per-occurrence abstention coverage/facts = %+v / %d", coverage, len(facts))
	}

	malformed := cloneFiles(profile.Files)
	malformed["index.scip"] = []byte("not SCIP")
	facts = nil
	coverage, err = gocaller.NewGRPC().Extract(
		context.Background(),
		memoryCorpus{
			repo: corpus.repo, commit: corpus.commit, files: malformed,
			attribution: frozenAttribution(profile),
		},
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnresolvedCount != 1 || len(facts) != 1 ||
		facts[0].Assertion.Predicate != "CALLER_EXTRACTION_GAP" ||
		facts[0].Assertion.Object != "malformed_symbol_input" {
		t.Fatalf("malformed input = %+v / %+v", coverage, facts)
	}

	empty := cloneFiles(profile.Files)
	empty["index.scip"] = nil
	facts = nil
	coverage, err = gocaller.NewGRPC().Extract(
		context.Background(),
		memoryCorpus{
			repo: corpus.repo, commit: corpus.commit, files: empty,
			attribution: frozenAttribution(profile),
		},
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil || coverage.UnresolvedCount != 0 || len(facts) != 0 ||
		!slicesContain(coverage.Protocols, "scip-index-empty") {
		t.Fatalf("empty input = %+v / %+v / %v", coverage, facts, err)
	}

	stale := cloneFiles(profile.Files)
	delete(stale, callerPath)
	facts = nil
	coverage, err = gocaller.NewGRPC().Extract(
		context.Background(),
		memoryCorpus{
			repo: corpus.repo, commit: corpus.commit, files: stale,
			attribution: frozenAttribution(profile),
		},
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnresolvedCount != 1 || len(facts) != 1 ||
		facts[0].Assertion.Predicate != "CALLER_EXTRACTION_GAP" ||
		facts[0].Assertion.Object != "stale_symbol_input" {
		t.Fatalf("stale input = %+v / %+v", coverage, facts)
	}

	missing := cloneFiles(profile.Files)
	delete(missing, "index.scip")
	facts = nil
	coverage, err = gocaller.NewGRPC().Extract(
		context.Background(),
		memoryCorpus{repo: corpus.repo, commit: corpus.commit, files: missing},
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil || len(facts) != 0 ||
		!reflect.DeepEqual(coverage.Protocols, []string{
			"generated-from-snapshot-v1", "grpc", "resolution-scip-v1",
			"resolution-syntax-v1", "scip", "scip-index-absent",
		}) {
		t.Fatalf("missing index = %+v / %+v / %v", coverage, facts, err)
	}
}

func TestTypedSCIPNonCallAbstentionUsesTheIndexedSpan(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(profile.Files)
	const callerPath = "src/thrift/caller.go"
	files["gen/thrift/ledger/ledger.go"] = []byte(strings.Replace(
		string(files["gen/thrift/ledger/ledger.go"]),
		`c.Call(ctx, "get", request)`, `c.Call(ctx, "getLong", request)`, 1,
	))
	files[callerPath] = []byte("package thriftcaller\nGet")
	files["index.scip"] = replaceSCIPReferenceRange(
		t, files["index.scip"], callerPath, t201.ThriftGetSymbol,
		string(files[callerPath]), "Get", 0,
	)

	var facts []sdk.Fact
	coverage, err := gocaller.NewThrift().Extract(
		context.Background(),
		memoryCorpus{
			repo: "synthetic.invalid/mono", commit: strings.Repeat("6", 40),
			files: files, attribution: frozenAttribution(profile),
		},
		func(fact sdk.Fact) error {
			if fact.Path == callerPath {
				facts = append(facts, fact)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.UnresolvedCount != 1 || len(facts) != 1 {
		t.Fatalf("non-call abstention = %+v / %+v", coverage, facts)
	}
	fact := facts[0]
	if fact.Assertion.Predicate != "UNRESOLVED_CALLER" ||
		fact.Assertion.Object != "/ledger.Ledger/getLong" ||
		fact.Atom.StartByte != len(files[callerPath])-3 ||
		fact.Atom.EndByte != len(files[callerPath]) ||
		fact.Assertion.Subject != "src/thrift/caller.go:21-24" ||
		!strings.Contains(fact.Assertion.Detail, "scip_range_not_call_selector") {
		t.Fatalf("non-call indexed span = %+v", fact)
	}
}

func TestTypedSCIPResultSurvivesLocalVariableRename(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	attribution := frozenAttribution(profile)
	extractOne := func(files map[string][]byte) sdk.Fact {
		t.Helper()
		var selected []sdk.Fact
		_, err := gocaller.NewGRPC().Extract(
			context.Background(),
			memoryCorpus{
				repo: "synthetic.invalid/mono", commit: strings.Repeat("3", 40),
				files: files, attribution: attribution,
			},
			func(fact sdk.Fact) error {
				if fact.Path == "src/checkout/caller.go" {
					selected = append(selected, fact)
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(selected) != 1 {
			t.Fatalf("selected facts = %d: %+v", len(selected), selected)
		}
		return selected[0]
	}

	before := extractOne(profile.Files)
	renamed := cloneFiles(profile.Files)
	const callerPath = "src/checkout/caller.go"
	renamed[callerPath] = []byte(strings.ReplaceAll(
		string(renamed[callerPath]), "client", "ordersClient",
	))
	renamed["index.scip"] = replaceSCIPReferenceRange(
		t, renamed["index.scip"], callerPath, t201.ProtoGetSymbol,
		string(renamed[callerPath]), "Get(ctx", 0,
	)
	after := extractOne(renamed)
	if before.Assertion.Predicate != after.Assertion.Predicate ||
		before.Assertion.Object != after.Assertion.Object ||
		before.Assertion.Lineage != after.Assertion.Lineage ||
		before.Assertion.Tier != after.Assertion.Tier ||
		before.Assertion.CodeRole != after.Assertion.CodeRole {
		t.Fatalf("typed semantics changed after rename:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestPackageAwareSyntaxFallbackRulesAndDeterminism(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(profile.Files)
	files["gen/proto/orders/v1/orders_grpc.pb.go"] = append(
		files["gen/proto/orders/v1/orders_grpc.pb.go"],
		[]byte("\nfunc NewOrdersClient(any) OrdersClient { return nil }\n")...,
	)
	const ordersImport = "synthetic.invalid/mono/gen/proto/orders/v1"
	const collisionImport = "synthetic.invalid/mono/gen/proto/collision/v1"
	fixtures := map[string]string{
		"src/syntax/parameter.go": `package syntax
import orders "` + ordersImport + `"
func Parameter(ctx any, client orders.OrdersClient) { _, _ = client.Get(ctx, nil) }
`,
		"src/syntax/constructor.go": `package syntax
import orders "` + ordersImport + `"
func Constructor(ctx any) {
	client := orders.NewOrdersClient(nil)
	_, _ = client.Get(ctx, nil)
}
`,
		"src/syntax/field.go": `package syntax
import orders "` + ordersImport + `"
type FieldRunner struct { client orders.OrdersClient }
func (r FieldRunner) Run(ctx any) { _, _ = r.client.Get(ctx, nil) }
`,
		"src/syntax/embedded.go": `package syntax
import orders "` + ordersImport + `"
type EmbeddedRunner struct { orders.OrdersClient }
func (r EmbeddedRunner) Run(ctx any) { _, _ = r.Get(ctx, nil) }
`,
		"src/syntax/alias.go": `package syntax
import orders "` + ordersImport + `"
type LocalClient = orders.OrdersClient
func Alias(ctx any, client LocalClient) { _, _ = client.Get(ctx, nil) }
`,
		"src/syntax/dynamic.go": `package syntax
import orders "` + ordersImport + `"
func Dynamic(ctx any, client any) { _, _ = client.Get(ctx, nil) }
`,
		"src/syntax/ambiguous.go": `package syntax
import (
	orders "` + ordersImport + `"
	collision "` + collisionImport + `"
)
var _, _ = orders.OrdersClient(nil), collision.CommonClient(nil)
func Ambiguous(ctx any, client any) { _, _ = client.Get(ctx, nil) }
`,
		"src/syntax/ambiguous_receiver.go": `package syntax
import (
	orders "` + ordersImport + `"
	collision "` + collisionImport + `"
)
type AmbiguousRunner struct {
	orders.OrdersClient
	collision.CommonClient
}
func (r AmbiguousRunner) Run(ctx any) { _, _ = r.Get(ctx, nil) }
`,
		"src/syntax/dot.go": `package syntax
import . "` + ordersImport + `"
var _ OrdersClient
func Dot(ctx any, client any) { _, _ = client.Get(ctx, nil) }
`,
		"src/syntax/typed.go": `package syntax
import orders "` + ordersImport + `"
func Typed(ctx any, client orders.OrdersClient) { _, _ = client.Get(ctx, nil) }
`,
		"src/syntax/shadow.go": `package syntax
import orders "` + ordersImport + `"
func Shadow(ctx any, client orders.OrdersClient) {
	{
		client := new(any)
		_, _ = client.Get(ctx, nil)
	}
}
`,
	}
	for filePath, content := range fixtures {
		files[filePath] = []byte(content)
	}
	files["index.scip"] = appendSCIPDocument(
		t, files["index.scip"], "src/syntax/typed.go", t201.ProtoGetSymbol,
		fixtures["src/syntax/typed.go"], "client.Get(ctx",
	)
	files["go.mod"] = []byte("module \"synthetic.invalid/mono\"\n\ngo 1.26\n")

	attribution := frozenAttribution(profile)
	const collisionGenerated = "gen/proto/collision/v1/common_grpc.pb.go"
	const collisionRelative = "collision/v1/common.proto"
	attribution.relations["grpc\x00"+collisionGenerated+"\x00"+collisionRelative] = sdk.GeneratedFromAttribution{
		State: sdk.AttributionStateResolved,
		Candidates: []sdk.GeneratedFromCandidate{{
			Protocol: "grpc", GeneratedPath: collisionGenerated,
			GeneratorRelativePath: collisionRelative,
			DeclarationPath:       "vendor/contracts/orders.proto",
			DeclarationLineage:    "provisional_repo_path_v1_" + strings.Repeat("c", 64),
		}},
	}
	corpus := memoryCorpus{
		repo: "synthetic.invalid/mono", commit: strings.Repeat("4", 40),
		files: files, attribution: attribution,
	}
	run := func() ([]sdk.Fact, sdk.Coverage) {
		t.Helper()
		var facts []sdk.Fact
		coverage, err := gocaller.NewGRPC().Extract(
			context.Background(), corpus,
			func(fact sdk.Fact) error {
				facts = append(facts, fact)
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return facts, coverage
	}
	firstFacts, firstCoverage := run()
	secondFacts, secondCoverage := run()
	if !reflect.DeepEqual(firstFacts, secondFacts) ||
		!reflect.DeepEqual(firstCoverage, secondCoverage) {
		t.Fatal("syntax fallback is not byte-order deterministic")
	}

	resolvedSyntax, unresolvedSyntax, typed := 0, 0, 0
	reasons := map[string]int{}
	for _, fact := range firstFacts {
		switch {
		case strings.Contains(fact.Assertion.Detail, `"resolution":"syntax"`) &&
			fact.Assertion.Predicate == "CALLS_OPERATION":
			resolvedSyntax++
			if fact.Assertion.Object != "/synthetic.orders.v1.Orders/Get" ||
				fact.Assertion.Tier != "heuristic" || fact.Assertion.Lineage == "" {
				t.Fatalf("resolved fallback = %+v", fact)
			}
		case strings.Contains(fact.Assertion.Detail, `"resolution":"syntax"`) &&
			fact.Assertion.Predicate == "UNRESOLVED_CALLER":
			unresolvedSyntax++
			for _, reason := range []string{
				"unsupported_receiver_flow", "ambiguous_method_candidates",
				"ambiguous_receiver_provenance", "dot_import_unsupported",
			} {
				if strings.Contains(fact.Assertion.Detail, reason) {
					reasons[reason]++
				}
			}
		case fact.Path == "src/syntax/typed.go" &&
			strings.Contains(fact.Assertion.Detail, `"resolution":"scip"`):
			typed++
		}
	}
	if resolvedSyntax != 5 || unresolvedSyntax != 7 || typed != 1 ||
		firstCoverage.UnresolvedCount != 7 ||
		reasons["unsupported_receiver_flow"] != 2 ||
		reasons["ambiguous_method_candidates"] != 2 ||
		reasons["ambiguous_receiver_provenance"] != 2 ||
		reasons["dot_import_unsupported"] != 1 {
		t.Fatalf(
			"fallback counts resolved=%d unresolved=%d typed=%d coverage=%+v reasons=%v",
			resolvedSyntax, unresolvedSyntax, typed, firstCoverage, reasons,
		)
	}

	withoutIndex := cloneFiles(files)
	delete(withoutIndex, "index.scip")
	var fallbackOnly []sdk.Fact
	coverage, err := gocaller.NewGRPC().Extract(
		context.Background(),
		memoryCorpus{
			repo: corpus.repo, commit: corpus.commit, files: withoutIndex,
			attribution: attribution,
		},
		func(fact sdk.Fact) error {
			fallbackOnly = append(fallbackOnly, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolvedSyntax = 0
	for _, fact := range fallbackOnly {
		if fact.Assertion.Predicate == "CALLS_OPERATION" &&
			strings.Contains(fact.Assertion.Detail, `"resolution":"syntax"`) {
			resolvedSyntax++
		}
	}
	if resolvedSyntax != 6 || coverage.UnresolvedCount != 7 ||
		!slicesContain(coverage.Protocols, "scip-index-absent") {
		t.Fatalf("fallback without SCIP = %d / %+v", resolvedSyntax, coverage)
	}
}

func TestPackageAwareThriftFallbackUsesDirectMapping(t *testing.T) {
	profile, err := t201.GenerateProfile(t201.SmallProfileName)
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(profile.Files)
	files["gen/thrift/ledger/ledger.go"] = append(
		files["gen/thrift/ledger/ledger.go"],
		[]byte("\nfunc NewLedgerClient(any) *LedgerClient { return &LedgerClient{} }\n")...,
	)
	files["src/thrift/syntax.go"] = []byte(`package thriftcaller
import ledger "synthetic.invalid/mono/gen/thrift/ledger"
func Direct(ctx any, client ledger.LedgerClient) { _, _ = client.Get(ctx, nil) }
func Constructed(ctx any) {
	client := ledger.NewLedgerClient(nil)
	_, _ = client.Get(ctx, nil)
}
func Dynamic(ctx any, client any) { _, _ = client.Get(ctx, nil) }
`)
	var facts []sdk.Fact
	coverage, err := gocaller.NewThrift().Extract(
		context.Background(),
		memoryCorpus{
			repo: "synthetic.invalid/mono", commit: strings.Repeat("5", 40),
			files: files, attribution: frozenAttribution(profile),
		},
		func(fact sdk.Fact) error {
			if fact.Path == "src/thrift/syntax.go" {
				facts = append(facts, fact)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, unresolved := 0, 0
	for _, fact := range facts {
		if fact.Assertion.Object != "/ledger.Ledger/get" {
			t.Fatalf("Thrift fallback object = %+v", fact)
		}
		switch fact.Assertion.Predicate {
		case "CALLS_OPERATION":
			resolved++
		case "UNRESOLVED_CALLER":
			unresolved++
		}
	}
	if resolved != 2 || unresolved != 1 || coverage.UnresolvedCount != 1 {
		t.Fatalf(
			"Thrift fallback resolved=%d unresolved=%d coverage=%+v facts=%+v",
			resolved, unresolved, coverage, facts,
		)
	}
}

func frozenAttribution(profile t201.Profile) fixtureAttribution {
	result := fixtureAttribution{
		repo:      "synthetic.invalid/mono",
		relations: make(map[string]sdk.GeneratedFromAttribution),
	}
	for _, relation := range profile.Oracle.GeneratedFrom {
		if relation.State != "resolved" {
			continue
		}
		generatorRelative := ""
		if relation.Protocol == "grpc" {
			generatorRelative = "orders/v1/orders.proto"
		}
		candidate := sdk.GeneratedFromCandidate{
			Protocol: relation.Protocol, GeneratedPath: relation.GeneratedPath,
			GeneratorRelativePath: generatorRelative,
			DeclarationPath:       relation.DeclarationPath,
			DeclarationLineage:    relation.DeclarationLineage,
		}
		key := relation.Protocol + "\x00" + relation.GeneratedPath + "\x00" + generatorRelative
		result.relations[key] = sdk.GeneratedFromAttribution{
			State:      sdk.AttributionStateResolved,
			Candidates: []sdk.GeneratedFromCandidate{candidate},
		}
	}
	return result
}

func cloneFiles(input map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(input))
	for filePath, content := range input {
		out[filePath] = append([]byte(nil), content...)
	}
	return out
}

func appendSCIPReference(
	t *testing.T,
	encoded []byte,
	documentPath, symbol, content, needle string,
	occurrence int,
) []byte {
	t.Helper()
	index := decodeSCIPIndex(t, encoded)
	for _, document := range index.Documents {
		if document.GetRelativePath() != documentPath {
			continue
		}
		indexed := &scip.Occurrence{
			Symbol:      symbol,
			SymbolRoles: int32(scip.SymbolRole_ReadAccess),
		}
		indexed.SetSourceRange(scip.NewRangeUnchecked(
			sourceRange(content, needle, occurrence),
		))
		document.Occurrences = append(document.Occurrences, indexed)
		return encodeSCIPIndex(t, index)
	}
	t.Fatalf("SCIP document %q is absent", documentPath)
	return nil
}

func appendSCIPDocument(
	t *testing.T,
	encoded []byte,
	documentPath, symbol, content, needle string,
) []byte {
	t.Helper()
	index := decodeSCIPIndex(t, encoded)
	occurrence := &scip.Occurrence{
		Symbol: symbol, SymbolRoles: int32(scip.SymbolRole_ReadAccess),
	}
	occurrence.SetSourceRange(scip.NewRangeUnchecked(
		sourceRange(content, needle, 0),
	))
	index.Documents = append(index.Documents, &scip.Document{
		RelativePath:     documentPath,
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences:      []*scip.Occurrence{occurrence},
	})
	sort.Slice(index.Documents, func(i, j int) bool {
		return index.Documents[i].GetRelativePath() <
			index.Documents[j].GetRelativePath()
	})
	return encodeSCIPIndex(t, index)
}

func replaceSCIPReferenceRange(
	t *testing.T,
	encoded []byte,
	documentPath, symbol, content, needle string,
	occurrence int,
) []byte {
	t.Helper()
	index := decodeSCIPIndex(t, encoded)
	for _, document := range index.Documents {
		if document.GetRelativePath() != documentPath {
			continue
		}
		for _, indexed := range document.Occurrences {
			if indexed.GetSymbol() == symbol &&
				indexed.GetSymbolRoles()&int32(scip.SymbolRole_Definition) == 0 {
				indexed.SetSourceRange(scip.NewRangeUnchecked(
					sourceRange(content, needle, occurrence),
				))
				return encodeSCIPIndex(t, index)
			}
		}
	}
	t.Fatalf("SCIP reference %q in %q is absent", symbol, documentPath)
	return nil
}

func decodeSCIPIndex(t *testing.T, encoded []byte) *scip.Index {
	t.Helper()
	index := &scip.Index{}
	if err := proto.Unmarshal(encoded, index); err != nil {
		t.Fatal(err)
	}
	return index
}

func encodeSCIPIndex(t *testing.T, index *scip.Index) []byte {
	t.Helper()
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func sourceRange(content, needle string, occurrence int) []int32 {
	offset, from := -1, 0
	for range occurrence + 1 {
		next := strings.Index(content[from:], needle)
		if next < 0 {
			panic("fixture needle is absent")
		}
		offset = from + next
		from = offset + len(needle)
	}
	line := int32(strings.Count(content[:offset], "\n"))
	lineStart := strings.LastIndex(content[:offset], "\n") + 1
	character := int32(offset - lineStart)
	return []int32{line, character, character + int32(len(strings.TrimSuffix(needle, "(ctx")))}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
