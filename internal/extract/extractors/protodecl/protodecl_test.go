package protodecl_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

type decodedImportContext struct {
	Count     int      `json:"count"`
	Digest    string   `json:"digest"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

type decodedTypeReference struct {
	Raw         string                `json:"raw"`
	Kind        string                `json:"kind"`
	Resolution  string                `json:"resolution"`
	Declaration string                `json:"declaration"`
	Reason      string                `json:"reason"`
	Imports     *decodedImportContext `json:"imports"`
}

type decodedOperationDetail struct {
	Schema          string               `json:"schema"`
	Request         decodedTypeReference `json:"request"`
	Response        decodedTypeReference `json:"response"`
	ClientStreaming bool                 `json:"client_streaming"`
	ServerStreaming bool                 `json:"server_streaming"`
}

type decodedMapShape struct {
	Key   decodedTypeReference `json:"key"`
	Value decodedTypeReference `json:"value"`
}

type decodedFieldDetail struct {
	Schema      string                `json:"schema"`
	Name        string                `json:"name"`
	Type        *decodedTypeReference `json:"type"`
	Cardinality string                `json:"cardinality"`
	Map         *decodedMapShape      `json:"map"`
	Oneof       string                `json:"oneof"`
}

type memoryCorpus struct {
	repo, commit string
	files        map[string]string
	readErrors   map[string]error
}

func (c memoryCorpus) RepoName() string { return c.repo }
func (c memoryCorpus) Commit() string   { return c.commit }
func (c memoryCorpus) WalkFiles(ctx context.Context, visit func(string) error) error {
	paths := make([]string, 0, len(c.files)+len(c.readErrors))
	for filePath := range c.files {
		paths = append(paths, filePath)
	}
	for filePath := range c.readErrors {
		if _, present := c.files[filePath]; !present {
			paths = append(paths, filePath)
		}
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
	if err := c.readErrors[filePath]; err != nil {
		return sdk.Blob{}, err
	}
	content, ok := c.files[filePath]
	if !ok {
		return sdk.Blob{}, errors.New("not found")
	}
	digest := sha256.Sum256([]byte(content))
	return sdk.Blob{Content: content, Digest: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func extractFacts(t *testing.T, corpus memoryCorpus) ([]sdk.Fact, sdk.Coverage, error) {
	t.Helper()
	var facts []sdk.Fact
	coverage, err := protodecl.New().Extract(context.Background(), corpus, func(fact sdk.Fact) error {
		facts = append(facts, fact)
		return nil
	})
	return facts, coverage, err
}

func findFact(t *testing.T, facts []sdk.Fact, object string) sdk.Fact {
	t.Helper()
	for _, fact := range facts {
		if fact.Assertion.Object == object {
			return fact
		}
	}
	t.Fatalf("fact %q not found in %+v", object, facts)
	return sdk.Fact{}
}

func findPredicateFact(t *testing.T, facts []sdk.Fact, predicate, object string) sdk.Fact {
	t.Helper()
	for _, fact := range facts {
		if fact.Assertion.Predicate == predicate && fact.Assertion.Object == object {
			return fact
		}
	}
	t.Fatalf("%s fact %q not found in %+v", predicate, object, facts)
	return sdk.Fact{}
}

func decodeDetail[T any](t *testing.T, fact sdk.Fact) T {
	t.Helper()
	var decoded T
	if err := json.Unmarshal([]byte(fact.Assertion.Detail), &decoded); err != nil {
		t.Fatalf("decode %s/%s detail %q: %v",
			fact.Assertion.Predicate, fact.Assertion.Object, fact.Assertion.Detail, err)
	}
	return decoded
}

func assertFactSpan(t *testing.T, content string, fact sdk.Fact, source string) {
	t.Helper()
	start, end := fact.Atom.StartByte, fact.Atom.EndByte
	if start < 0 || end <= start || end > len(content) {
		t.Fatalf("invalid emitted byte span [%d,%d)", start, end)
	}
	if got := content[start:end]; got != source {
		t.Fatalf("source[%d:%d] = %q, want %q", start, end, got, source)
	}
	lineAt := func(offset int) int {
		return 1 + strings.Count(content[:offset], "\n")
	}
	if want := lineAt(start); fact.StartLine != want {
		t.Fatalf("StartLine = %d, want %d", fact.StartLine, want)
	}
	if want := lineAt(end - 1); fact.EndLine != want {
		t.Fatalf("EndLine = %d, want %d", fact.EndLine, want)
	}
}

func atomID(fact sdk.Fact) string {
	return store.ComputeAtomID(store.EvidenceAtom{
		SchemaVersion: fact.Atom.SchemaVersion, BlobDigest: fact.Atom.BlobDigest,
		StartByte: fact.Atom.StartByte, EndByte: fact.Atom.EndByte,
		RuleID: fact.Atom.RuleID, ExtractorVersion: protodecl.New().Version(),
		AdapterConfigDigest: fact.Atom.AdapterConfigDigest,
		FactFingerprint:     fact.Atom.FactFingerprint,
	})
}

func TestFieldIdentitySurvivesRename(t *testing.T) {
	v1, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "v1", files: map[string]string{
			"api.proto": `syntax = "proto3"; package demo; message Item { string old_name = 7; }`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	v2, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "v2", files: map[string]string{
			"api.proto": `syntax = "proto3"; package demo; message Item { string new_name = 7; }`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldFact := findFact(t, v1, "demo.Item#7")
	newFact := findFact(t, v2, "demo.Item#7")
	if oldFact.Assertion.Lineage != newFact.Assertion.Lineage {
		t.Fatalf("rename changed lineage: %q != %q", oldFact.Assertion.Lineage, newFact.Assertion.Lineage)
	}
	if strings.Contains(oldFact.Assertion.Object, "old_name") ||
		strings.Contains(newFact.Assertion.Object, "new_name") {
		t.Fatalf("field name leaked into canonical object")
	}
	if !strings.Contains(oldFact.Assertion.Detail, `"old_name"`) ||
		!strings.Contains(newFact.Assertion.Detail, `"new_name"`) {
		t.Fatalf("versioned field detail missing names: %q / %q",
			oldFact.Assertion.Detail, newFact.Assertion.Detail)
	}
	toAssertion := func(fact sdk.Fact) store.Assertion {
		return store.Assertion{
			Predicate: fact.Assertion.Predicate, Subject: fact.Assertion.Subject,
			Object: fact.Assertion.Object, Lineage: fact.Assertion.Lineage,
			Tier: fact.Assertion.Tier, CodeRole: fact.Assertion.CodeRole,
			Repo: "r", Detail: fact.Assertion.Detail,
		}
	}
	if oldID, newID := store.ComputeAssertionID(toAssertion(oldFact)), store.ComputeAssertionID(toAssertion(newFact)); oldID != newID {
		t.Fatalf("field rename changed semantic assertion identity: %s != %s", oldID, newID)
	}
}

func TestCoverageMarksLineageAsProvisional(t *testing.T) {
	_, coverage, err := extractFacts(t, memoryCorpus{repo: "r", commit: "c", files: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(coverage.Protocols, ","); got != "protobuf,lineage-provisional-repo-path-v1" {
		t.Fatalf("coverage protocols = %q", got)
	}
}

func TestShapeFactsResolveSameFileTypesWithoutTraversingThem(t *testing.T) {
	content := `syntax = "proto3";
package demo;

service Catalog {
  rpc Get(Request) returns (stream Reply);
  rpc Put(stream Request) returns (Reply);
}
service Empty {}

message Request {
  string id = 1;
  repeated Item items = 2;
  map<string, Item> by_id = 3;
  oneof selector {
    string name = 4;
    Nested nested = 5;
  }
  message Nested {
    Item item = 1;
  }
}
message Item {
  Request owner = 1;
}
message Reply {}
message EmptyMessage {}
`
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{"shape.proto": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := protodecl.New().Version(), "3.0.0"; got != want {
		t.Fatalf("extractor version = %q, want %q", got, want)
	}
	for _, fact := range facts {
		if fact.Atom.SchemaVersion != "t17-v1" {
			t.Fatalf("%s/%s schema version = %q",
				fact.Assertion.Predicate, fact.Assertion.Object, fact.Atom.SchemaVersion)
		}
	}

	get := findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Catalog/Get")
	getDetail := decodeDetail[decodedOperationDetail](t, get)
	if getDetail.Schema != "proto-operation-detail-v1" ||
		getDetail.Request.Raw != "Request" ||
		getDetail.Request.Resolution != "same_file" ||
		getDetail.Request.Declaration != "demo.Request" ||
		getDetail.Response.Raw != "Reply" ||
		getDetail.Response.Resolution != "same_file" ||
		getDetail.Response.Declaration != "demo.Reply" ||
		getDetail.ClientStreaming || !getDetail.ServerStreaming {
		t.Fatalf("Get detail = %+v", getDetail)
	}
	put := decodeDetail[decodedOperationDetail](
		t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Catalog/Put"),
	)
	if !put.ClientStreaming || put.ServerStreaming {
		t.Fatalf("Put streaming flags = %+v", put)
	}

	assertField := func(
		object, cardinality, raw, resolution, declaration, oneof string,
	) decodedFieldDetail {
		t.Helper()
		detail := decodeDetail[decodedFieldDetail](
			t, findPredicateFact(t, facts, "DECLARES_FIELD", object),
		)
		if detail.Schema != "proto-field-detail-v2" || detail.Cardinality != cardinality ||
			detail.Type == nil || detail.Type.Raw != raw ||
			detail.Type.Resolution != resolution ||
			detail.Type.Declaration != declaration || detail.Oneof != oneof {
			t.Fatalf("%s detail = %+v", object, detail)
		}
		return detail
	}
	id := assertField("demo.Request#1", "singular", "string", "intrinsic", "", "")
	if id.Type.Kind != "scalar" {
		t.Fatalf("scalar field type = %+v", id.Type)
	}
	assertField("demo.Request#2", "repeated", "Item", "same_file", "demo.Item", "")
	assertField(
		"demo.Request#5", "singular", "Nested", "same_file",
		"demo.Request.Nested", "selector",
	)
	assertField(
		"demo.Request.Nested#1", "singular", "Item", "same_file",
		"demo.Item", "",
	)
	assertField("demo.Item#1", "singular", "Request", "same_file", "demo.Request", "")

	mapField := decodeDetail[decodedFieldDetail](
		t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Request#3"),
	)
	if mapField.Cardinality != "repeated" || mapField.Type != nil || mapField.Map == nil ||
		mapField.Map.Key.Raw != "string" || mapField.Map.Key.Kind != "scalar" ||
		mapField.Map.Key.Resolution != "intrinsic" ||
		mapField.Map.Value.Raw != "Item" ||
		mapField.Map.Value.Resolution != "same_file" ||
		mapField.Map.Value.Declaration != "demo.Item" {
		t.Fatalf("map field detail = %+v", mapField)
	}

	for _, declaration := range []struct {
		predicate, object string
	}{
		{"DECLARES_SERVICE", "demo.Catalog"},
		{"DECLARES_SERVICE", "demo.Empty"},
		{"DECLARES_MESSAGE", "demo.Request"},
		{"DECLARES_MESSAGE", "demo.Request.Nested"},
		{"DECLARES_MESSAGE", "demo.Reply"},
		{"DECLARES_MESSAGE", "demo.EmptyMessage"},
	} {
		findPredicateFact(t, facts, declaration.predicate, declaration.object)
	}
	assertFactSpan(t, content, get, "rpc Get(Request) returns (stream Reply);")
	assertFactSpan(
		t, content,
		findPredicateFact(t, facts, "DECLARES_MESSAGE", "demo.EmptyMessage"),
		"message EmptyMessage {}",
	)
	assertFactSpan(
		t, content,
		findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Request#3"),
		"map<string, Item> by_id = 3;",
	)

	// Request and Item reference each other. The extractor records links only;
	// it never follows them into recursive shape expansion.
	if len(facts) != 16 {
		t.Fatalf("recursive fixture emitted %d facts, want 16", len(facts))
	}
}

func TestUnresolvedTypeReasonsAreDistinctAndNeverClaimExternality(t *testing.T) {
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"external/types.proto": `syntax = "proto3"; package external; message Request {} message Reply {}`,
			"imported.proto": `syntax = "proto3";
package demo;
import "external/types.proto";
service Imported {
  rpc Use(.external.Request) returns (.external.Reply);
}`,
			"missing.proto": `syntax = "proto3";
package demo;
service Missing { rpc Use(Absent) returns (Absent); }`,
			"ambiguous.proto": `syntax = "proto3";
package demo;
message Duplicate {}
message Duplicate {}
service Ambiguous { rpc Use(Duplicate) returns (Duplicate); }
enum NotMessage { UNKNOWN = 0; }
service WrongKind { rpc Use(NotMessage) returns (NotMessage); }`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	imported := decodeDetail[decodedOperationDetail](
		t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Imported/Use"),
	)
	for _, reference := range []decodedTypeReference{imported.Request, imported.Response} {
		if reference.Resolution != "unresolved" ||
			reference.Reason != "IMPORT_LINKING_UNAVAILABLE" ||
			reference.Imports == nil || reference.Imports.Count != 1 ||
			len(reference.Imports.Paths) != 1 ||
			reference.Imports.Paths[0] != "external/types.proto" ||
			reference.Imports.Truncated ||
			!strings.HasPrefix(reference.Imports.Digest, "sha256:") {
			t.Fatalf("imported reference = %+v", reference)
		}
	}
	if strings.Contains(
		findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Imported/Use").Assertion.Detail,
		`"resolution":"external"`,
	) {
		t.Fatal("unlinked import was labeled external")
	}

	missing := decodeDetail[decodedOperationDetail](
		t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Missing/Use"),
	)
	if missing.Request.Reason != "DECLARATION_NOT_FOUND" ||
		missing.Response.Reason != "DECLARATION_NOT_FOUND" ||
		missing.Request.Imports == nil || missing.Request.Imports.Count != 0 {
		t.Fatalf("missing detail = %+v", missing)
	}

	ambiguous := decodeDetail[decodedOperationDetail](
		t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Ambiguous/Use"),
	)
	if ambiguous.Request.Reason != "AMBIGUOUS_SAME_FILE_DECLARATION" ||
		ambiguous.Response.Reason != "AMBIGUOUS_SAME_FILE_DECLARATION" {
		t.Fatalf("ambiguous detail = %+v", ambiguous)
	}

	wrongKind := decodeDetail[decodedOperationDetail](
		t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.WrongKind/Use"),
	)
	if wrongKind.Request.Reason != "INVALID_DECLARATION_KIND" ||
		wrongKind.Request.Kind != "enum" ||
		wrongKind.Request.Declaration != "demo.NotMessage" ||
		wrongKind.Response.Reason != "INVALID_DECLARATION_KIND" {
		t.Fatalf("wrong-kind detail = %+v", wrongKind)
	}
}

func TestShapeFactRunsAreByteIdentical(t *testing.T) {
	corpus := memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"b.proto": `syntax = "proto3"; package p; message B { A a = 1; } message A { B b = 1; }`,
			"a.proto": `syntax = "proto3"; package p; import "z.proto"; import "a.proto";
service S { rpc X(Missing) returns (Missing); }`,
		},
	}
	first, firstCoverage, err := extractFacts(t, corpus)
	if err != nil {
		t.Fatal(err)
	}
	second, secondCoverage, err := extractFacts(t, corpus)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := json.Marshal(struct {
		Facts    []sdk.Fact
		Coverage sdk.Coverage
	}{first, firstCoverage})
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := json.Marshal(struct {
		Facts    []sdk.Fact
		Coverage sdk.Coverage
	}{second, secondCoverage})
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("two complete runs differ:\n%s\n%s", firstBytes, secondBytes)
	}
}

func TestIdenticalCrossRepoBlobDeduplicatesAtom(t *testing.T) {
	content := `syntax = "proto3"; package demo; message Item { string name = 1; }`
	aFacts, _, err := extractFacts(t, memoryCorpus{
		repo: "host/a", commit: "a", files: map[string]string{"vendor/api.proto": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	bFacts, _, err := extractFacts(t, memoryCorpus{
		repo: "host/b", commit: "b", files: map[string]string{"vendor/api.proto": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := findFact(t, aFacts, "demo.Item#1")
	b := findFact(t, bFacts, "demo.Item#1")
	if a.Assertion.Lineage == b.Assertion.Lineage {
		t.Fatal("repository placement should keep assertion lineages separate")
	}
	if atomID(a) != atomID(b) {
		t.Fatalf("identical blobs did not deduplicate: %s != %s", atomID(a), atomID(b))
	}
}

func TestSameDirectoryDuplicateFQNsStaySeparate(t *testing.T) {
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"roots/a.proto": `syntax = "proto3"; package demo; message Item { string a = 1; }`,
			"roots/b.proto": `syntax = "proto3"; package demo; message Item { string b = 1; }`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var lineages []string
	for _, fact := range facts {
		if fact.Assertion.Object == "demo.Item#1" {
			lineages = append(lineages, fact.Assertion.Lineage)
		}
	}
	if len(lineages) != 2 || lineages[0] == lineages[1] {
		t.Fatalf("same-directory duplicate lineages = %v", lineages)
	}
	for _, lineage := range lineages {
		if len(lineage) != len("provisional_repo_path_v1_")+64 {
			t.Fatalf("lineage hash was truncated: %q", lineage)
		}
	}
}

func TestPackageLessAndPackageFirstPass(t *testing.T) {
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"plain.proto": `syntax = "proto3"; service Greeter { rpc Hello (Req) returns (Req); } message Req { string value = 1; }`,
			"late.proto":  `syntax = "proto3"; message Before { string value = 2; } package late;`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{"Greeter/Hello", "Req#1", "late.Before#2"} {
		fact := findFact(t, facts, object)
		if strings.HasPrefix(fact.Assertion.Object, ".") {
			t.Fatalf("package-less object has leading dot: %q", fact.Assertion.Object)
		}
	}
}

func TestEmittedSpansAreHalfOpenAndLineExact(t *testing.T) {
	assertSpan := func(t *testing.T, content string, fact sdk.Fact, source string) {
		t.Helper()
		start, end := fact.Atom.StartByte, fact.Atom.EndByte
		if start < 0 || end <= start || end > len(content) {
			t.Fatalf("invalid emitted byte span [%d,%d)", start, end)
		}
		if got := content[start:end]; got != source {
			t.Fatalf("source[%d:%d] = %q, want %q", start, end, got, source)
		}
		// EndByte is exclusive, so the persisted inclusive EndLine must be
		// derived from EndByte-1 (the trusted worker applies the same check).
		lineAt := func(offset int) int {
			return 1 + strings.Count(content[:offset], "\n")
		}
		if want := lineAt(start); fact.StartLine != want {
			t.Fatalf("StartLine = %d, want %d", fact.StartLine, want)
		}
		if want := lineAt(end - 1); fact.EndLine != want {
			t.Fatalf("EndLine = %d, want %d", fact.EndLine, want)
		}
	}

	content := `syntax = "proto3";
// A non-ASCII prefix verifies that offsets remain byte-oriented: café.
package demo;
service Greeter {
  rpc Multiline(Request) returns (Reply) {
}
}
message Request {
  string value = 1;
}
message Reply {}
`
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{"spans.proto": content},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		object, source string
	}{
		{
			object: "demo.Greeter/Multiline",
			source: "rpc Multiline(Request) returns (Reply) {\n}",
		},
		{object: "demo.Request#1", source: "string value = 1;"},
	}
	for _, test := range tests {
		t.Run(test.object, func(t *testing.T) {
			assertSpan(t, content, findFact(t, facts, test.object), test.source)
		})
	}

	t.Run("UTF-8 in emitted text without final newline", func(t *testing.T) {
		content := `syntax = "proto2";
message Request {
  optional string value = 1 [default = "café"];
}`
		facts, _, err := extractFacts(t, memoryCorpus{
			repo: "r", commit: "c", files: map[string]string{"utf8.proto": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSpan(t, content, findFact(t, facts, "Request#1"),
			`optional string value = 1 [default = "café"];`)
	})

	t.Run("CRLF multiline without final newline", func(t *testing.T) {
		content := strings.Join([]string{
			`syntax = "proto3";`,
			`service Greeter {`,
			`  rpc CRLF(Request) returns (Request) {`,
			`}`,
			`}`,
			`message Request {}`,
		}, "\r\n")
		facts, _, err := extractFacts(t, memoryCorpus{
			repo: "r", commit: "c", files: map[string]string{"crlf.proto": content},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSpan(t, content, findFact(t, facts, "Greeter/CRLF"),
			"rpc CRLF(Request) returns (Request) {\r\n}")
	})
}

func TestProto2GroupsAndEligibleFieldForms(t *testing.T) {
	content := `syntax = "proto2";
message Outer {
  optional group Key = 4 { required string id = 1; }
  oneof choice {
    string value = 5;
    group Choice = 6 { optional int32 nested = 1; }
  }
}`
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{"groups.proto": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{"Outer#4", "Outer.Key#1", "Outer#5", "Outer#6", "Outer.Choice#1"} {
		findFact(t, facts, object)
	}
	if got := findFact(t, facts, "Outer#4").Assertion.Detail; !strings.Contains(got, `"name":"key"`) {
		t.Fatalf("group descriptor field detail = %q, want lowercase field name", got)
	}
	if got := findFact(t, facts, "Outer#6").Assertion.Detail; !strings.Contains(got, `"name":"choice"`) {
		t.Fatalf("oneof group descriptor field detail = %q, want lowercase field name", got)
	}
}

func TestParserComplexityLimitsFailClosed(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{
			name:    "structural depth",
			content: `syntax = "proto3"; ` + strings.Repeat("message M {", 129),
			want:    "structural-depth limit",
		},
		{
			name:    "adjacent structural tokens",
			content: strings.Repeat("{", 129),
			want:    "structural-depth limit",
		},
		{
			name:    "token count",
			content: strings.Repeat(";", 500_001),
			want:    "token proto parser limit",
		},
		{
			name:    "source bytes",
			content: strings.Repeat(" ", (4<<20)+1),
			want:    "byte proto parser limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, coverage, err := extractFacts(t, memoryCorpus{
				repo: "r", commit: "c", files: map[string]string{"hostile.proto": test.content},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Extract error = %v", err)
			}
			if len(coverage.Protocols) != 0 {
				t.Fatalf("failed extraction returned coverage: %+v", coverage)
			}
		})
	}
}

func TestParserComplexityScanIgnoresCommentsAndStrings(t *testing.T) {
	content := `syntax = "proto3";
// {{{{{[[[[[((((
message M {
  string value = 1 [json_name = "{{{{{[[[[[(((("];
  /* {{{{{[[[[[(((( */
}`
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{"comments.proto": content},
	})
	if err != nil {
		t.Fatal(err)
	}
	findFact(t, facts, "M#1")
}

func TestDeclaredProtoRoleClassification(t *testing.T) {
	content := `syntax = "proto3"; message M { string value = 1; }`
	facts, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"tests/vendor/a.proto": content,
			"testdata/b.proto":     content,
			"api/c.proto":          content,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"tests/vendor/a.proto": "vendor",
		"testdata/b.proto":     "test",
		"api/c.proto":          "production",
	}
	for _, fact := range facts {
		if expected, ok := want[fact.Path]; ok && fact.Assertion.CodeRole != expected {
			t.Fatalf("role for %s = %q, want %q", fact.Path, fact.Assertion.CodeRole, expected)
		}
		delete(want, fact.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing facts for paths: %v", want)
	}
}

func TestRelativeExtensionFailsClosed(t *testing.T) {
	for _, extendee := range []string{"Base", ".demo.Base"} {
		content := `syntax = "proto2"; package demo; message Base { extensions 100 to max; }
extend ` + extendee + ` { optional string ambiguous = 100; }`
		_, coverage, err := extractFacts(t, memoryCorpus{
			repo: "r", commit: "c", files: map[string]string{"extension.proto": content},
		})
		if err == nil || !strings.Contains(err.Error(), "require descriptor linking") {
			t.Fatalf("extension %q error = %v", extendee, err)
		}
		if len(coverage.Protocols) != 0 {
			t.Fatalf("failed extraction returned successful coverage: %+v", coverage)
		}
	}
}

func TestCandidateReadAndParseFailuresFailClosed(t *testing.T) {
	tests := []memoryCorpus{
		{repo: "r", commit: "c", readErrors: map[string]error{"bad.proto": errors.New("read failed")}},
		{repo: "r", commit: "c", files: map[string]string{"bad.proto": `syntax = "proto3"; message`}},
	}
	for _, corpus := range tests {
		_, coverage, err := extractFacts(t, corpus)
		if err == nil {
			t.Fatal("candidate failure reported success")
		}
		if len(coverage.Protocols) != 0 {
			t.Fatalf("failed extraction returned successful coverage: %+v", coverage)
		}
	}
}

func TestEmitterErrorPropagates(t *testing.T) {
	want := errors.New("stage failed")
	corpus := memoryCorpus{
		repo: "r", commit: "c", files: map[string]string{
			"api.proto": `syntax = "proto3"; message M { string x = 1; }`,
		},
	}
	_, err := protodecl.New().Extract(context.Background(), corpus, func(sdk.Fact) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("Extract error = %v, want emitter failure", err)
	}
}
