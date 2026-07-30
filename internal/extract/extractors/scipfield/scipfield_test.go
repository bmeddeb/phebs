package scipfield

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type memoryCorpus struct {
	repo, commit string
	files        map[string]string
	typedPath    string
	inScope      func(string) bool
	included     func(string) bool
	reads        map[string]int
}

func (c memoryCorpus) RepoName() string { return c.repo }
func (c memoryCorpus) Commit() string   { return c.commit }
func (c memoryCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
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
	if c.reads != nil {
		c.reads[filePath]++
	}
	content, ok := c.files[filePath]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("missing %q", filePath)
	}
	return trustedBlob(content), nil
}
func (c memoryCorpus) ReadSCIPIndex(ctx context.Context) (sdk.SCIPInput, error) {
	typedPath := c.typedPath
	if typedPath == "" {
		typedPath = indexPath
	}
	if _, ok := c.files[typedPath]; !ok {
		return sdk.SCIPInput{Path: typedPath}, nil
	}
	blob, err := c.Read(ctx, typedPath)
	return sdk.SCIPInput{
		Path: typedPath, Blob: blob, Present: err == nil,
	}, err
}
func (c memoryCorpus) SCIPDocumentInScope(filePath string) bool {
	return c.inScope == nil || c.inScope(filePath)
}
func (c memoryCorpus) SCIPDocumentIncluded(filePath string) bool {
	return c.included == nil || c.included(filePath)
}

func trustedBlob(content string) sdk.Blob {
	sum := sha256.Sum256([]byte(content))
	return sdk.Blob{Content: content, Digest: "sha256:" + hex.EncodeToString(sum[:])}
}

func TestReferencesUseExactSCIPSpansAndClassification(t *testing.T) {
	corpus := fixtureCorpus(t, "example.com/consumer", "v1.0.0", "old_name", "OldName")
	facts, coverage := extractFacts(t, corpus)
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want getter read and field write", len(facts))
	}
	if got, want := coverage.Protocols, []string{"protobuf-generated-accessor-v1", "scip", "scip-package-lineage-v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("protocols = %v, want %v", got, want)
	}
	consumer := corpus.files["consumer/use.go"]
	classifications := make(map[string]sdk.Fact)
	for _, fact := range facts {
		if fact.Assertion.Predicate != "REFERENCES_PROTO_FIELD" || fact.Assertion.Object != "acme.v1.Item#1" {
			t.Fatalf("unexpected assertion: %+v", fact.Assertion)
		}
		if fact.Assertion.Tier != "exact" || !strings.HasPrefix(fact.Assertion.Lineage, "contract_scip_package_v1_") {
			t.Fatalf("tier/lineage = %q %q", fact.Assertion.Tier, fact.Assertion.Lineage)
		}
		cited := consumer[fact.Atom.StartByte:fact.Atom.EndByte]
		if cited != "GetOldName" && cited != "OldName" {
			t.Fatalf("cited source = %q", cited)
		}
		if fact.StartLine != 4 && fact.StartLine != 5 || fact.EndLine != fact.StartLine {
			t.Fatalf("line span = %d..%d", fact.StartLine, fact.EndLine)
		}
		for _, classification := range []string{"read", "write"} {
			if strings.Contains(fact.Assertion.Detail, `"classification":"`+classification+`"`) {
				classifications[classification] = fact
			}
		}
	}
	if len(classifications) != 2 {
		t.Fatalf("classifications = %v, want read and write", classifications)
	}
	if classifications["read"].Assertion.Subject == classifications["write"].Assertion.Subject {
		t.Fatal("distinct occurrences collapsed to one semantic subject")
	}
}

func TestFocusedFilterDropsTestDefinitionDocumentBeforeSourceReads(t *testing.T) {
	corpus := fixtureCorpus(
		t, "example.com/consumer", "v1.0.0", "old_name", "OldName",
	)
	index := decodeFixtureIndex(t, corpus.files[indexPath])
	generated := index.Documents[0]
	oldPath := generated.GetRelativePath()
	testPath := strings.TrimSuffix(oldPath, ".go") + "_test.go"
	generated.RelativePath = testPath
	corpus.files[testPath] = corpus.files[oldPath]
	delete(corpus.files, oldPath)
	corpus.files[indexPath] = encodeFixtureIndex(t, index)
	corpus.included = func(filePath string) bool {
		return !strings.HasSuffix(filePath, "_test.go")
	}
	corpus.reads = make(map[string]int)

	expectedDefinitions := 0
	for _, occurrence := range generated.GetOccurrences() {
		if occurrence.GetSymbolRoles()&
			int32(scip.SymbolRole_Definition) != 0 {
			expectedDefinitions++
		}
	}
	var facts []sdk.Fact
	coverage, err := New().Extract(
		context.Background(), corpus,
		func(fact sdk.Fact) error {
			facts = append(facts, fact)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("test-only definitions resolved base references: %+v", facts)
	}
	if corpus.reads[testPath] != 0 {
		t.Fatalf("excluded test source reads = %d", corpus.reads[testPath])
	}
	if coverage.ExcludedSCIPDocuments != 1 ||
		coverage.ExcludedSCIPDefinitions != expectedDefinitions ||
		coverage.ExcludedSCIPOccurrences != len(generated.GetOccurrences()) {
		t.Fatalf("excluded SCIP coverage = %+v", coverage)
	}

	index = decodeFixtureIndex(t, corpus.files[indexPath])
	index.Documents[0].Occurrences[0].Symbol =
		strings.Repeat("x", maxSymbolBytes+1)
	corpus.files[indexPath] = encodeFixtureIndex(t, index)
	if _, err := New().Extract(
		context.Background(), corpus, func(sdk.Fact) error { return nil },
	); err == nil || !strings.Contains(err.Error(), "symbol over") {
		t.Fatalf("excluded document escaped global validation: %v", err)
	}
}

func TestRenamedFieldAcrossConsumerVersionsKeepsCanonicalIdentity(t *testing.T) {
	v1 := fixtureCorpus(t, "example.com/consumer-one", "v1.0.0", "old_name", "OldName")
	v2 := fixtureCorpus(t, "example.com/consumer-two", "v2.0.0", "new_name", "NewName")
	facts1, _ := extractFacts(t, v1)
	facts2, _ := extractFacts(t, v2)
	if len(facts1) != 2 || len(facts2) != 2 {
		t.Fatalf("fact counts = %d, %d; want 2 each", len(facts1), len(facts2))
	}
	left, right := facts1[0].Assertion, facts2[0].Assertion
	if left.Object != "acme.v1.Item#1" || right.Object != left.Object {
		t.Fatalf("objects = %q and %q", left.Object, right.Object)
	}
	if right.Lineage != left.Lineage {
		t.Fatalf("lineages changed across dependency versions: %q != %q", left.Lineage, right.Lineage)
	}
	if left.Detail == right.Detail || !strings.Contains(left.Detail, `"name":"old_name"`) ||
		!strings.Contains(right.Detail, `"name":"new_name"`) {
		t.Fatalf("rename details = %s and %s", left.Detail, right.Detail)
	}
	if !strings.Contains(left.Detail, `"dependency_version":"v1.0.0"`) ||
		!strings.Contains(right.Detail, `"dependency_version":"v2.0.0"`) {
		t.Fatalf("dependency versions missing from details: %s / %s", left.Detail, right.Detail)
	}
}

func TestGeneratedSourceResolvesAgainstBufModuleRoot(t *testing.T) {
	corpus := fixtureCorpus(t, "example.com/consumer", "v1.0.0", "old_name", "OldName")
	source := corpus.files["api/item.proto"]
	delete(corpus.files, "api/item.proto")
	corpus.files["idl/proto/buf.yaml"] = "version: v1\n"
	corpus.files["idl/proto/api/item.proto"] = source

	facts, _ := extractFacts(t, corpus)
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want module-root field references", len(facts))
	}
	for _, fact := range facts {
		if fact.Assertion.Object != "acme.v1.Item#1" {
			t.Fatalf("module-root field binding = %+v", fact.Assertion)
		}
	}
}

func TestGeneratedSourceAmbiguousAcrossBufModulesAbstains(t *testing.T) {
	corpus := fixtureCorpus(t, "example.com/consumer", "v1.0.0", "old_name", "OldName")
	source := corpus.files["api/item.proto"]
	delete(corpus.files, "api/item.proto")
	for _, root := range []string{"idl/one", "idl/two"} {
		corpus.files[root+"/buf.yaml"] = "version: v1\n"
		corpus.files[root+"/api/item.proto"] = source
	}

	facts, _ := extractFacts(t, corpus)
	if len(facts) != 0 {
		t.Fatalf("facts = %+v, want ambiguous module-relative source to abstain", facts)
	}
}

func TestClassificationPrecedenceAndCodeRoles(t *testing.T) {
	rows := []struct {
		name  string
		roles int32
		want  string
	}{
		{name: "write beats all", roles: int32(scip.SymbolRole_WriteAccess | scip.SymbolRole_ReadAccess | scip.SymbolRole_Test), want: "write"},
		{name: "read beats test", roles: int32(scip.SymbolRole_ReadAccess | scip.SymbolRole_Test), want: "read"},
		{name: "test beats generated", roles: int32(scip.SymbolRole_Test | scip.SymbolRole_Generated), want: "test"},
		{name: "generated", roles: int32(scip.SymbolRole_Generated), want: "generated"},
		{name: "unknown", roles: 0, want: "unknown"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := classify(row.roles); got != row.want {
				t.Fatalf("classify(%d) = %q, want %q", row.roles, got, row.want)
			}
		})
	}
	if got := codeRole("vendor/tests/x_test.go", int32(scip.SymbolRole_Test)); got != "vendor" {
		t.Fatalf("vendor precedence = %q", got)
	}
	if got := codeRole("pkg/generated.go", int32(scip.SymbolRole_Generated|scip.SymbolRole_Test)); got != "generated" {
		t.Fatalf("generated precedence = %q", got)
	}
}

func TestAmbiguousSymbolBindingAbstains(t *testing.T) {
	corpus := fixtureCorpus(t, "example.com/consumer", "v1.0.0", "old_name", "OldName")
	generated := corpus.files["contract/item.pb.go"]
	otherGenerated := strings.Replace(generated, "// source: api/item.proto", "// source: other/item.proto", 1)
	fieldSymbol := fieldSymbol("v1.0.0", "OldName")
	getterSymbol := getterSymbol("v1.0.0", "OldName")
	index := makeIndex(t, generated, corpus.files["consumer/use.go"], fieldSymbol, getterSymbol)
	// The same valid SCIP symbols are defined in two generated documents whose
	// source protos give the field different message full names. Neither symbol
	// may be guessed into one contract.
	index.Documents = append(index.Documents, &scip.Document{
		RelativePath:     "other/item.pb.go",
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{
			{Range: rangeFor(t, otherGenerated, "OldName", 0), Symbol: fieldSymbol, SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated)},
			{Range: rangeFor(t, otherGenerated, "GetOldName", 0), Symbol: getterSymbol, SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated)},
		},
	})
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	corpus.files["other/item.pb.go"] = otherGenerated
	corpus.files["other/item.proto"] = strings.Replace(corpus.files["api/item.proto"], "package acme.v1", "package other.v1", 1)
	corpus.files[indexPath] = string(data)
	facts, _ := extractFacts(t, corpus)
	if len(facts) != 0 {
		t.Fatalf("facts = %+v, want ambiguous symbols to abstain", facts)
	}
}

func TestMissingIndexIsAStableEmptyResult(t *testing.T) {
	corpus := memoryCorpus{repo: "example.com/no-index", commit: strings.Repeat("a", 40), files: map[string]string{"x.go": "package x\n"}}
	facts, coverage := extractFacts(t, corpus)
	if len(facts) != 0 || !reflect.DeepEqual(coverage.Protocols, []string{"scip-index-absent"}) {
		t.Fatalf("facts/coverage = %v / %+v", facts, coverage)
	}
}

func TestConfiguredTypedPathAndScopedDocumentAdmission(t *testing.T) {
	corpus := fixtureCorpus(
		t, "example.com/consumer", "v1.0.0", "old_name", "OldName",
	)
	const typedPath = "service/typed/service.scip"
	corpus.files[typedPath] = corpus.files[indexPath]
	delete(corpus.files, indexPath)
	corpus.typedPath = typedPath
	facts, _ := extractFacts(t, corpus)
	if len(facts) != 2 {
		t.Fatalf("configured typed path facts = %d, want 2", len(facts))
	}

	corpus.inScope = func(filePath string) bool {
		return filePath != "consumer/use.go"
	}
	if _, err := New().Extract(
		context.Background(), corpus, func(sdk.Fact) error { return nil },
	); err == nil || !strings.Contains(err.Error(), "outside the committed analysis unit") {
		t.Fatalf("out-of-unit SCIP document error = %v", err)
	}
}

func TestMalformedIndexFailsWithoutPartialFacts(t *testing.T) {
	corpus := memoryCorpus{
		repo: "example.com/bad-index", commit: strings.Repeat("a", 40),
		files: map[string]string{indexPath: "not a SCIP protobuf"},
	}
	emitted := 0
	if _, err := New().Extract(context.Background(), corpus, func(sdk.Fact) error {
		emitted++
		return nil
	}); err == nil {
		t.Fatal("malformed index succeeded")
	}
	if emitted != 0 {
		t.Fatalf("malformed index emitted %d partial facts", emitted)
	}
}

func TestPolyglotSCIPIgnoresForeignSourceDocuments(t *testing.T) {
	corpus := fixtureCorpus(
		t, "example.com/consumer", "v1.0.0", "old_name", "OldName",
	)
	wantFacts, wantCoverage := extractFacts(t, corpus)
	index := decodeFixtureIndex(t, corpus.files[indexPath])
	index.Documents = append(index.Documents, &scip.Document{
		RelativePath:     "consumer/Use.java",
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{
			Range:       []int32{0, 0, 1},
			Symbol:      getterSymbol("v1.0.0", "OldName"),
			SymbolRoles: int32(scip.SymbolRole_ReadAccess),
		}},
	})
	corpus.files[indexPath] = encodeFixtureIndex(t, index)

	gotFacts, gotCoverage := extractFacts(t, corpus)
	if !reflect.DeepEqual(gotFacts, wantFacts) ||
		!reflect.DeepEqual(gotCoverage, wantCoverage) {
		t.Fatalf(
			"foreign SCIP document changed extraction:\nwant=%+v / %+v\ngot=%+v / %+v",
			wantFacts, wantCoverage, gotFacts, gotCoverage,
		)
	}
}

func TestPolyglotSCIPStillValidatesForeignDocuments(t *testing.T) {
	corpus := fixtureCorpus(
		t, "example.com/consumer", "v1.0.0", "old_name", "OldName",
	)
	index := decodeFixtureIndex(t, corpus.files[indexPath])
	index.Documents = append(index.Documents, &scip.Document{
		RelativePath:     "consumer/Use.java",
		PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		Occurrences: []*scip.Occurrence{{
			Range:  []int32{0, 0, 1},
			Symbol: strings.Repeat("x", maxSymbolBytes+1),
		}},
	})
	corpus.files[indexPath] = encodeFixtureIndex(t, index)
	if _, err := New().Extract(
		context.Background(), corpus, func(sdk.Fact) error { return nil },
	); err == nil || !strings.Contains(err.Error(), "symbol over") {
		t.Fatalf("oversized foreign SCIP symbol error = %v", err)
	}
}

func TestMissingEligibleSCIPSourcesRemainFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *memoryCorpus)
	}{
		{
			name: "Go document",
			mutate: func(_ *testing.T, corpus *memoryCorpus) {
				delete(corpus.files, "consumer/use.go")
			},
		},
		{
			name: "protobuf document",
			mutate: func(t *testing.T, corpus *memoryCorpus) {
				index := decodeFixtureIndex(t, corpus.files[indexPath])
				index.Documents = append(index.Documents, &scip.Document{
					RelativePath:     "000-missing.proto",
					PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
					Occurrences: []*scip.Occurrence{{
						Range:       []int32{0, 0, 1},
						Symbol:      getterSymbol("v1.0.0", "OldName"),
						SymbolRoles: int32(scip.SymbolRole_ReadAccess),
					}},
				})
				corpus.files[indexPath] = encodeFixtureIndex(t, index)
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			corpus := fixtureCorpus(
				t, "example.com/consumer", "v1.0.0", "old_name", "OldName",
			)
			testCase.mutate(t, &corpus)
			if _, err := New().Extract(
				context.Background(), corpus, func(sdk.Fact) error { return nil },
			); err == nil || !strings.Contains(err.Error(), "absent from the immutable tree") {
				t.Fatalf("missing eligible source error = %v", err)
			}
		})
	}
}

func TestSCIPMetadataEncodingAppliesWhenDocumentOverrideIsAbsent(t *testing.T) {
	rows := []struct {
		name     string
		metadata scip.TextEncoding
		document scip.PositionEncoding
		want     scip.PositionEncoding
		wantErr  bool
	}{
		{
			name:     "metadata UTF-8",
			metadata: scip.TextEncoding_UTF8,
			want:     scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
		},
		{
			name:     "metadata UTF-16",
			metadata: scip.TextEncoding_UTF16,
			want:     scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart,
		},
		{
			name:     "document override",
			metadata: scip.TextEncoding_UTF8,
			document: scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart,
			want:     scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart,
		},
		{name: "neither level", wantErr: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			index := &scip.Index{
				Metadata: &scip.Metadata{TextDocumentEncoding: row.metadata},
				Documents: []*scip.Document{{
					RelativePath: "consumer/use.go", PositionEncoding: row.document,
				}},
			}
			data, err := proto.Marshal(index)
			if err != nil {
				t.Fatal(err)
			}
			documents, err := parseIndex(context.Background(), string(data))
			if row.wantErr {
				if err == nil {
					t.Fatal("missing metadata/document encoding succeeded")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIndex: %v", err)
			}
			if len(documents) != 1 || documents[0].encoding != row.want {
				t.Fatalf("documents = %+v, want encoding %s", documents, row.want)
			}
		})
	}
}

func TestMalformedReferenceRangeAbstainsWithoutDiscardingIndex(t *testing.T) {
	corpus := fixtureCorpus(t, "example.com/consumer", "v1.0.0", "old_name", "OldName")
	index := &scip.Index{}
	if err := proto.Unmarshal([]byte(corpus.files[indexPath]), index); err != nil {
		t.Fatal(err)
	}
	index.Documents[1].Occurrences[0].SetSourceRange(scip.Range{
		Start: scip.Position{Line: 3, Character: 999},
		End:   scip.Position{Line: 3, Character: 1009},
	})
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	corpus.files[indexPath] = string(data)
	facts, _ := extractFacts(t, corpus)
	if len(facts) != 1 || !strings.Contains(facts[0].Assertion.Detail, `"classification":"write"`) {
		t.Fatalf("facts = %+v, want the valid write only", facts)
	}
}

func TestSCIPPositionEncodingsResolveExactBytes(t *testing.T) {
	content := "🙂Field\n"
	starts := lineStarts(content)
	rows := []struct {
		name     string
		encoding scip.PositionEncoding
		start    int32
		end      int32
	}{
		{name: "utf8", encoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, start: 4, end: 9},
		{name: "utf16", encoding: scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, start: 2, end: 7},
		{name: "utf32", encoding: scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart, start: 1, end: 6},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			start, end, ok := byteSpan(content, starts, scip.Range{
				Start: scip.Position{Line: 0, Character: row.start},
				End:   scip.Position{Line: 0, Character: row.end},
			}, row.encoding)
			if !ok || content[start:end] != "Field" {
				t.Fatalf("span = %d..%d ok=%v text=%q", start, end, ok, content[start:end])
			}
		})
	}
	if _, _, ok := byteSpan(content, starts, scip.Range{
		Start: scip.Position{Line: 0, Character: 1},
		End:   scip.Position{Line: 0, Character: 2},
	}, scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart); ok {
		t.Fatal("UTF-16 range splitting a surrogate pair was accepted")
	}
	if _, _, ok := byteSpan(content, starts, scip.Range{
		Start: scip.Position{Line: 0, Character: 2},
		End:   scip.Position{Line: 0, Character: 4},
	}, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart); ok {
		t.Fatal("UTF-8 range splitting a rune was accepted")
	}
}

func TestDeterministicAndNoAccuracyLanguage(t *testing.T) {
	corpus := fixtureCorpus(t, "example.com/consumer", "v1.0.0", "old_name", "OldName")
	first, firstCoverage := extractFacts(t, corpus)
	second, secondCoverage := extractFacts(t, corpus)
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstCoverage, secondCoverage) {
		t.Fatalf("two runs differ:\n%+v\n%+v", first, second)
	}
	for _, fact := range first {
		output := strings.ToLower(fmt.Sprintf("%+v", fact))
		for _, banned := range []string{"accuracy", "precision", "recall", "validated"} {
			if strings.Contains(output, banned) {
				t.Fatalf("output contains %q: %s", banned, output)
			}
		}
	}
}

func fixtureCorpus(t *testing.T, repo, dependencyVersion, protoName, goName string) memoryCorpus {
	t.Helper()
	generated := fmt.Sprintf(`// Code generated by protoc-gen-go. DO NOT EDIT.
// source: api/item.proto

package contract

type Item struct {
	%s string `+"`"+`protobuf:"bytes,1,opt,name=%s,json=%s,proto3"`+"`"+`
}

func (x *Item) Get%s() string {
	return x.%s
}
`, goName, protoName, goName, goName, goName)
	consumer := fmt.Sprintf(`package consumer

func use(x *contract.Item) {
	_ = x.Get%s()
	x.%s = "next"
}
`, goName, goName)
	protoSource := fmt.Sprintf(`syntax = "proto3";
package acme.v1;
message Item {
  string %s = 1;
}
`, protoName)
	index := makeIndex(t, generated, consumer, fieldSymbol(dependencyVersion, goName), getterSymbol(dependencyVersion, goName))
	data, err := proto.Marshal(index)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	return memoryCorpus{
		repo: repo, commit: strings.Repeat("a", 40),
		files: map[string]string{
			indexPath:             string(data),
			"api/item.proto":      protoSource,
			"contract/item.pb.go": generated,
			"consumer/use.go":     consumer,
		},
	}
}

func makeIndex(t *testing.T, generated, consumer, field, getter string) *scip.Index {
	t.Helper()
	return &scip.Index{
		Metadata: &scip.Metadata{},
		Documents: []*scip.Document{
			{
				RelativePath:     "contract/item.pb.go",
				PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
				Occurrences: []*scip.Occurrence{
					{Range: rangeFor(t, generated, strings.TrimSuffix(field[strings.LastIndex(field, "#")+1:], "."), 0), Symbol: field, SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated)},
					{Range: rangeFor(t, generated, "Get"+methodFieldName(getter), 0), Symbol: getter, SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated)},
				},
			},
			{
				RelativePath:     "consumer/use.go",
				PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
				Occurrences: []*scip.Occurrence{
					{Range: rangeFor(t, consumer, "Get"+methodFieldName(getter), 0), Symbol: getter, SymbolRoles: int32(scip.SymbolRole_ReadAccess)},
					{Range: rangeFor(t, consumer, strings.TrimSuffix(field[strings.LastIndex(field, "#")+1:], "."), 0), Symbol: field, SymbolRoles: int32(scip.SymbolRole_WriteAccess)},
				},
			},
		},
	}
}

func decodeFixtureIndex(t *testing.T, content string) *scip.Index {
	t.Helper()
	index := &scip.Index{}
	if err := proto.Unmarshal([]byte(content), index); err != nil {
		t.Fatal(err)
	}
	return index
}

func encodeFixtureIndex(t *testing.T, index *scip.Index) string {
	t.Helper()
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fieldSymbol(version, goName string) string {
	return "go gomod example.com/contracts " + version + " api/Item#" + goName + "."
}

func getterSymbol(version, goName string) string {
	return "go gomod example.com/contracts " + version + " api/Item#Get" + goName + "()."
}

func methodFieldName(symbol string) string {
	start := strings.LastIndex(symbol, "#Get")
	end := strings.LastIndex(symbol, "().")
	return symbol[start+len("#Get") : end]
}

func rangeFor(t *testing.T, content, needle string, occurrence int) []int32 {
	t.Helper()
	offset := -1
	remaining := content
	base := 0
	for index := 0; index <= occurrence; index++ {
		found := strings.Index(remaining, needle)
		if found < 0 {
			t.Fatalf("needle %q occurrence %d not found", needle, occurrence)
		}
		offset = base + found
		base = offset + len(needle)
		remaining = content[base:]
	}
	line := int32(strings.Count(content[:offset], "\n"))
	lineStart := strings.LastIndex(content[:offset], "\n") + 1
	character := int32(offset - lineStart)
	return []int32{line, character, character + int32(len(needle))}
}

func extractFacts(t *testing.T, corpus memoryCorpus) ([]sdk.Fact, sdk.Coverage) {
	t.Helper()
	var facts []sdk.Fact
	coverage, err := New().Extract(context.Background(), corpus, func(fact sdk.Fact) error {
		facts = append(facts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return facts, coverage
}
