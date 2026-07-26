package thriftfield

import (
	"context"
	"crypto/sha1" //nolint:gosec // fixture follows thriftrw's descriptor identity.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type memoryCorpus struct {
	repo, commit string
	files        map[string]string
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
	content, ok := c.files[filePath]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("missing %q", filePath)
	}
	return trustedBlob(content), nil
}

func (c memoryCorpus) ReadSCIPIndex(ctx context.Context) (sdk.Blob, error) {
	return c.Read(ctx, indexPath)
}

func trustedBlob(content string) sdk.Blob {
	sum := sha256.Sum256([]byte(content))
	return sdk.Blob{Content: content, Digest: "sha256:" + hex.EncodeToString(sum[:])}
}

func TestReferencesUseExactSCIPSpansAndClassification(t *testing.T) {
	corpus := fixtureCorpus(t, "v1.0.0", "old_name", "OldName", 10)
	facts, coverage := extractFacts(t, corpus)
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want getter read and field write", len(facts))
	}
	wantProtocols := []string{"scip", "scip-package-lineage-v1", "thriftrw-module-digest-v1"}
	if !reflect.DeepEqual(coverage.Protocols, wantProtocols) {
		t.Fatalf("protocols = %v, want %v", coverage.Protocols, wantProtocols)
	}
	consumer := corpus.files["consumer/use.go"]
	classifications := make(map[string]sdk.Fact)
	for _, fact := range facts {
		if fact.Assertion.Predicate != "REFERENCES_THRIFT_FIELD" ||
			fact.Assertion.Object != "shared.Item#10" {
			t.Fatalf("unexpected assertion: %+v", fact.Assertion)
		}
		if fact.Assertion.Tier != "exact" ||
			!strings.HasPrefix(fact.Assertion.Lineage, "contract_scip_package_v1_") {
			t.Fatalf("tier/lineage = %q %q", fact.Assertion.Tier, fact.Assertion.Lineage)
		}
		cited := consumer[fact.Atom.StartByte:fact.Atom.EndByte]
		if cited != "GetOldName" && cited != "OldName" {
			t.Fatalf("cited source = %q", cited)
		}
		if !strings.Contains(fact.Assertion.Detail, `"generator":"thriftrw"`) ||
			!strings.Contains(fact.Assertion.Detail, `"source_binding":"module_digest"`) {
			t.Fatalf("detail = %s", fact.Assertion.Detail)
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

func TestRenamedFieldAcrossConsumerVersionsKeepsCanonicalIdentity(t *testing.T) {
	leftFacts, _ := extractFacts(t, fixtureCorpus(t, "v1.0.0", "old_name", "OldName", 10))
	rightFacts, _ := extractFacts(t, fixtureCorpus(t, "v2.0.0", "new_name", "NewName", 10))
	if len(leftFacts) != 2 || len(rightFacts) != 2 {
		t.Fatalf("fact counts = %d, %d; want 2 each", len(leftFacts), len(rightFacts))
	}
	left, right := leftFacts[0].Assertion, rightFacts[0].Assertion
	if left.Object != "shared.Item#10" || right.Object != left.Object {
		t.Fatalf("objects = %q and %q", left.Object, right.Object)
	}
	if left.Lineage != right.Lineage {
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

func TestDuplicateDefinitionAndDuplicateMessageIDAbstain(t *testing.T) {
	t.Run("duplicate SCIP definition", func(t *testing.T) {
		corpus := fixtureCorpus(t, "v1.0.0", "name", "Name", 1)
		generated := corpus.files["generated/shared.go"]
		index := decodeIndex(t, corpus.files[indexPath])
		index.Documents = append(index.Documents, &scip.Document{
			RelativePath:     "generated/copy.go",
			PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
			Occurrences: []*scip.Occurrence{
				{
					Range:       rangeFor(t, generated, "GetName", 0, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart),
					Symbol:      getterSymbol("v1.0.0", "Name"),
					SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated),
				},
			},
		})
		corpus.files["generated/copy.go"] = generated
		corpus.files[indexPath] = encodeIndex(t, index)
		facts, _ := extractFacts(t, corpus)
		for _, fact := range facts {
			if strings.Contains(fact.Assertion.Detail, `"classification":"read"`) {
				t.Fatalf("duplicate getter definition emitted read fact: %+v", fact)
			}
		}
	})

	t.Run("duplicate message field ID", func(t *testing.T) {
		raw := "namespace go example.shared\nstruct Item {\n  1: optional string first\n  1: optional string second\n}\n"
		generated := generatedStructSource(t, raw, "shared", "Item", "First", 1)
		consumer := "package consumer\nfunc use(v *shared.Item) { _ = v.GetFirst() }\n"
		symbol := getterSymbol("v1.0.0", "First")
		index := makeIndex(t, generated, consumer, "First", "GetFirst", symbol, "v1.0.0")
		corpus := corpusFromFiles(generated, consumer, index)
		facts, _ := extractFacts(t, corpus)
		if len(facts) != 0 {
			t.Fatalf("duplicate Item#1 identity emitted facts: %+v", facts)
		}
	})
}

func TestFieldZeroIsFirstClass(t *testing.T) {
	raw := "namespace go example.health\nservice Meta {\n  string health()\n}\n"
	generated := generatedStructSource(t, raw, "health", "Meta_Health_Result", "Success", 0)
	consumer := "package consumer\nfunc use(v *health.Meta_Health_Result) { _ = v.GetSuccess() }\n"
	symbol := "scip-go gomod example.com/contracts v1.0.0 pkg/Meta_Health_Result#GetSuccess()."
	index := makeIndex(t, generated, consumer, "Success", "GetSuccess", symbol, "v1.0.0")
	facts, _ := extractFacts(t, corpusFromFiles(generated, consumer, index))
	if len(facts) != 1 || facts[0].Assertion.Object != "health.Meta_Health_Result#0" {
		t.Fatalf("field-zero facts = %+v", facts)
	}
}

func TestMissingAndMalformedIndexesFailClosed(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		corpus := memoryCorpus{
			repo: "example.invalid/no-index", commit: strings.Repeat("a", 40),
			files: map[string]string{"x.go": "package x\n"},
		}
		facts, coverage := extractFacts(t, corpus)
		if len(facts) != 0 ||
			!reflect.DeepEqual(coverage.Protocols, []string{"scip-index-absent"}) {
			t.Fatalf("facts/coverage = %v / %+v", facts, coverage)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		corpus := memoryCorpus{
			repo: "example.invalid/bad-index", commit: strings.Repeat("a", 40),
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
	})

	t.Run("encoding absent at both levels", func(t *testing.T) {
		index := &scip.Index{
			Metadata:  &scip.Metadata{},
			Documents: []*scip.Document{{RelativePath: "x.go"}},
		}
		corpus := memoryCorpus{
			repo: "example.invalid/no-encoding", commit: strings.Repeat("a", 40),
			files: map[string]string{
				indexPath: encodeIndex(t, index),
				"x.go":    "package x\n",
			},
		}
		emitted := 0
		if _, err := New().Extract(context.Background(), corpus, func(sdk.Fact) error {
			emitted++
			return nil
		}); err == nil || !strings.Contains(err.Error(), "unspecified position encoding") {
			t.Fatalf("missing encoding error = %v", err)
		}
		if emitted != 0 {
			t.Fatalf("missing encoding emitted %d partial facts", emitted)
		}
	})
}

func TestAdmissionLimitsAreExplicitlyNarrowed(t *testing.T) {
	const (
		inheritedIndexBytes  = 64 << 20
		inheritedDocuments   = 100_000
		inheritedOccurrences = 1_000_000
		inheritedSymbolBytes = 16 << 10
	)
	if maxIndexBytes >= inheritedIndexBytes ||
		maxDocuments >= inheritedDocuments ||
		maxOccurrences >= inheritedOccurrences ||
		maxSymbolBytes >= inheritedSymbolBytes {
		t.Fatalf(
			"unmeasured inherited limits resurfaced: index=%d documents=%d occurrences=%d symbol=%d",
			maxIndexBytes, maxDocuments, maxOccurrences, maxSymbolBytes,
		)
	}
	if maxGeneratedBytes != 4<<20 {
		t.Fatalf("generated-file bound = %d, want measured 4 MiB", maxGeneratedBytes)
	}
}

func TestRealSCIPGoFixtureBindsProductionAccessorShapes(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "t222-realindex")
	files := make(map[string]string)
	for _, filePath := range []string{
		indexPath,
		"generated/shared.go",
		"consumer/use.go",
	} {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filePath)))
		if err != nil {
			t.Fatalf("read real-index fixture %s: %v", filePath, err)
		}
		files[filePath] = string(content)
	}
	receiptBytes, err := os.ReadFile(filepath.Join(root, "receipt.json"))
	if err != nil {
		t.Fatalf("read real-index receipt: %v", err)
	}
	var receipt realIndexReceipt
	decoder := json.NewDecoder(strings.NewReader(string(receiptBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("decode real-index receipt: %v", err)
	}
	indexDigest := sha256.Sum256([]byte(files[indexPath]))
	if receipt.Schema != "t22.2-real-index-fixture-v1" ||
		receipt.Indexer != "scip-go" ||
		receipt.IndexerVersion != "0.2.7" ||
		receipt.IndexSHA256 != "sha256:"+hex.EncodeToString(indexDigest[:]) ||
		receipt.IndexBytes != len(files[indexPath]) {
		t.Fatalf("real-index receipt disagrees with fixture: %+v", receipt)
	}
	index := decodeIndex(t, files[indexPath])
	metadata := index.GetMetadata()
	if metadata.GetToolInfo().GetName() != receipt.Indexer ||
		metadata.GetToolInfo().GetVersion() != receipt.IndexerVersion ||
		metadata.GetProjectRoot() != receipt.ProjectRoot ||
		metadata.GetTextDocumentEncoding().String() != receipt.PositionEncoding {
		t.Fatalf("real-index metadata disagrees with receipt: %+v", metadata)
	}
	recordedCommand := append([]string{receipt.Indexer}, metadata.GetToolInfo().GetArguments()...)
	if !reflect.DeepEqual(receipt.Command, recordedCommand) {
		t.Fatalf("real-index command = %v, metadata command = %v", receipt.Command, recordedCommand)
	}
	var occurrences, definitions int
	for _, document := range index.GetDocuments() {
		occurrences += len(document.GetOccurrences())
		for _, occurrence := range document.GetOccurrences() {
			if occurrence.GetSymbolRoles()&int32(scip.SymbolRole_Definition) != 0 {
				definitions++
			}
		}
	}
	if len(index.GetDocuments()) != receipt.Documents ||
		occurrences != receipt.Occurrences ||
		definitions != receipt.Definitions ||
		occurrences-definitions != receipt.References {
		t.Fatalf(
			"real-index counts = documents=%d occurrences=%d definitions=%d references=%d; receipt=%+v",
			len(index.GetDocuments()), occurrences, definitions, occurrences-definitions, receipt,
		)
	}

	facts, _ := extractFacts(t, memoryCorpus{
		repo: "example.invalid/t222-realindex", commit: strings.Repeat("2", 40), files: files,
	})
	consumer := files["consumer/use.go"]
	var consumerFacts []sdk.Fact
	for _, fact := range facts {
		if fact.Path == "consumer/use.go" {
			consumerFacts = append(consumerFacts, fact)
		}
	}
	if len(consumerFacts) != 2 {
		t.Fatalf("real index emitted %d consumer facts, want getter and field references: %+v", len(consumerFacts), facts)
	}
	citations := make(map[string]string)
	for _, fact := range consumerFacts {
		if fact.Assertion.Object != "shared.Item#10" ||
			fact.Assertion.Tier != "exact" ||
			!strings.Contains(fact.Assertion.Detail, `"classification":"read"`) ||
			!strings.Contains(fact.Assertion.Detail, `"dependency_version":"v0.0.0"`) {
			t.Fatalf("real-index consumer assertion = %+v", fact.Assertion)
		}
		citation := consumer[fact.Atom.StartByte:fact.Atom.EndByte]
		citations[citation] = fact.Assertion.Subject
	}
	for _, want := range []string{"GetWorkflowId", "WorkflowId"} {
		if citations[want] == "" {
			t.Fatalf("real index citations = %v, missing %q", citations, want)
		}
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
}

func TestClassificationPrecedenceAndDeterminism(t *testing.T) {
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

	corpus := fixtureCorpus(t, "v1.0.0", "old_name", "OldName", 10)
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

func fixtureCorpus(t *testing.T, dependencyVersion, thriftName, goName string, fieldID int) memoryCorpus {
	t.Helper()
	raw := fmt.Sprintf(
		"namespace go example.shared\nstruct Item {\n  %d: optional string %s\n}\n",
		fieldID, thriftName,
	)
	generated := generatedStructSource(t, raw, "shared", "Item", goName, fieldID)
	consumer := fmt.Sprintf(`package consumer

func use(v *shared.Item) {
	_ = v.Get%s()
	v.%s = nil
}
`, goName, goName)
	index := makeIndex(
		t, generated, consumer, goName, "Get"+goName,
		getterSymbol(dependencyVersion, goName), dependencyVersion,
	)
	// Add the generated field definition and matching write reference beside
	// the getter rows built by makeIndex.
	field := fieldSymbol(dependencyVersion, goName)
	index.Documents[0].Occurrences = append(index.Documents[0].Occurrences, &scip.Occurrence{
		Range:       rangeFor(t, generated, goName, 0, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart),
		Symbol:      field,
		SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated),
	})
	index.Documents[1].Occurrences = append(index.Documents[1].Occurrences, &scip.Occurrence{
		Range:       rangeFor(t, consumer, goName, 1, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart),
		Symbol:      field,
		SymbolRoles: int32(scip.SymbolRole_WriteAccess),
	})
	return corpusFromFiles(generated, consumer, index)
}

func corpusFromFiles(generated, consumer string, index *scip.Index) memoryCorpus {
	return memoryCorpus{
		repo: "example.invalid/consumer", commit: strings.Repeat("a", 40),
		files: map[string]string{
			indexPath:             encodeIndexRaw(index),
			"generated/shared.go": generated,
			"consumer/use.go":     consumer,
		},
	}
}

func generatedStructSource(
	t *testing.T,
	raw, scope, message, goName string,
	fieldID int,
) string {
	t.Helper()
	sum := sha1.Sum([]byte(raw)) //nolint:gosec // fixture descriptor identity.
	return fmt.Sprintf(`package %s

import (
	"go.uber.org/thriftrw/thriftreflect"
	"go.uber.org/thriftrw/wire"
)

type %s struct {
	%s *string
}

func (v *%s) ToWire() (wire.Value, error) {
	var fields [1]wire.Field
	if v.%s != nil {
		fields[0] = wire.Field{ID: %d, Value: wire.NewValueString(*v.%s)}
	}
	return wire.NewValueStruct(wire.Struct{Fields: fields[:]}), nil
}

func (v *%s) Get%s() string {
	if v != nil && v.%s != nil {
		return *v.%s
	}
	return ""
}

func (v *%s) IsSet%s() bool { return v != nil && v.%s != nil }

var ThriftModule = &thriftreflect.ThriftModule{
	Name: %q, Package: %q, FilePath: %q, SHA1: %q, Raw: rawIDL,
}

const rawIDL = %s
`,
		scope,
		message, goName,
		message, goName, fieldID, goName,
		message, goName, goName, goName,
		message, goName, goName,
		scope, "example.invalid/contracts/"+scope, scope+".thrift",
		hex.EncodeToString(sum[:]), strconv.Quote(raw),
	)
}

func makeIndex(
	t *testing.T,
	generated, consumer, fieldName, getterName, getter, dependencyVersion string,
) *scip.Index {
	t.Helper()
	_ = dependencyVersion
	return &scip.Index{
		Metadata: &scip.Metadata{},
		Documents: []*scip.Document{
			{
				RelativePath:     "generated/shared.go",
				PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
				Occurrences: []*scip.Occurrence{
					{
						Range:       rangeFor(t, generated, getterName, 0, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart),
						Symbol:      getter,
						SymbolRoles: int32(scip.SymbolRole_Definition | scip.SymbolRole_Generated),
					},
				},
			},
			{
				RelativePath:     "consumer/use.go",
				PositionEncoding: scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
				Occurrences: []*scip.Occurrence{
					{
						Range:       rangeFor(t, consumer, getterName, 0, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart),
						Symbol:      getter,
						SymbolRoles: int32(scip.SymbolRole_ReadAccess),
					},
				},
			},
		},
	}
}

func fieldSymbol(version, goName string) string {
	return "scip-go gomod example.com/contracts " + version + " pkg/Item#" + goName + "."
}

func getterSymbol(version, goName string) string {
	return "scip-go gomod example.com/contracts " + version + " pkg/Item#Get" + goName + "()."
}

func rangeFor(
	t *testing.T,
	content, needle string,
	occurrence int,
	encoding scip.PositionEncoding,
) []int32 {
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
	character := positionUnits(content[lineStart:offset], encoding)
	end := character + positionUnits(content[offset:offset+len(needle)], encoding)
	return []int32{line, character, end}
}

func positionUnits(value string, encoding scip.PositionEncoding) int32 {
	switch encoding {
	case scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart:
		var count int32
		for _, character := range value {
			if character > 0xffff {
				count += 2
			} else {
				count++
			}
		}
		return count
	case scip.PositionEncoding_UTF32CodeUnitOffsetFromLineStart:
		return int32(len([]rune(value)))
	default:
		return int32(len(value))
	}
}

func encodeIndex(t *testing.T, index *scip.Index) string {
	t.Helper()
	return encodeIndexRaw(index)
}

func encodeIndexRaw(index *scip.Index) string {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func decodeIndex(t *testing.T, content string) *scip.Index {
	t.Helper()
	index := &scip.Index{}
	if err := proto.Unmarshal([]byte(content), index); err != nil {
		t.Fatal(err)
	}
	return index
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

type realIndexReceipt struct {
	Schema           string   `json:"schema"`
	Indexer          string   `json:"indexer"`
	IndexerVersion   string   `json:"indexer_version"`
	Command          []string `json:"command"`
	ProjectRoot      string   `json:"project_root"`
	IndexSHA256      string   `json:"index_sha256"`
	IndexBytes       int      `json:"index_bytes"`
	PositionEncoding string   `json:"position_encoding"`
	Documents        int      `json:"documents"`
	Occurrences      int      `json:"occurrences"`
	Definitions      int      `json:"definitions"`
	References       int      `json:"references"`
	Observation      string   `json:"observation"`
}
