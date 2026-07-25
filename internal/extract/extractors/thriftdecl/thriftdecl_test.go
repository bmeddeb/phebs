package thriftdecl_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

type decodedIncludeContext struct {
	Count     int      `json:"count"`
	Digest    string   `json:"digest"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

type decodedTypeReference struct {
	Raw         string                 `json:"raw"`
	Kind        string                 `json:"kind"`
	Resolution  string                 `json:"resolution"`
	Declaration string                 `json:"declaration"`
	Reason      string                 `json:"reason"`
	Imports     *decodedIncludeContext `json:"imports"`
}

type decodedOperationDetail struct {
	Schema   string               `json:"schema"`
	Request  decodedTypeReference `json:"request"`
	Response decodedTypeReference `json:"response"`
	OneWay   bool                 `json:"oneway"`
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
}

type decodedMessageDetail struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Synthetic bool   `json:"synthetic"`
}

type decodedServiceDetail struct {
	Schema  string `json:"schema"`
	Name    string `json:"name"`
	Extends string `json:"extends"`
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
	coverage, err := thriftdecl.New().Extract(context.Background(), corpus, func(fact sdk.Fact) error {
		facts = append(facts, fact)
		return nil
	})
	return facts, coverage, err
}

func mustExtract(t *testing.T, corpus memoryCorpus) ([]sdk.Fact, sdk.Coverage) {
	t.Helper()
	facts, coverage, err := extractFacts(t, corpus)
	if err != nil {
		t.Fatal(err)
	}
	return facts, coverage
}

func singleFileCorpus(source string) memoryCorpus {
	return memoryCorpus{
		repo: "github.com/acme/idl", commit: "c0ffee",
		files: map[string]string{"rpc/svc.thrift": source},
	}
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

func hasFact(facts []sdk.Fact, predicate, object string) bool {
	for _, fact := range facts {
		if fact.Assertion.Predicate == predicate && fact.Assertion.Object == object {
			return true
		}
	}
	return false
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

func assertLineSpan(t *testing.T, content string, fact sdk.Fact, wantLine int, wantPrefix string) {
	t.Helper()
	start, end := fact.Atom.StartByte, fact.Atom.EndByte
	if start < 0 || end <= start || end > len(content) {
		t.Fatalf("invalid emitted byte span [%d,%d)", start, end)
	}
	span := content[start:end]
	if !strings.HasPrefix(span, wantPrefix) {
		t.Fatalf("span %q does not start with %q", span, wantPrefix)
	}
	if strings.Contains(span, "\n") {
		t.Fatalf("span %q crosses a line boundary", span)
	}
	if fact.StartLine != wantLine || fact.EndLine != wantLine {
		t.Fatalf("lines [%d,%d], want %d", fact.StartLine, fact.EndLine, wantLine)
	}
	if got := 1 + strings.Count(content[:start], "\n"); got != wantLine {
		t.Fatalf("start byte on line %d, want %d", got, wantLine)
	}
}

const demoIDL = `namespace go acme.demo
namespace java com.acme.demo

include "shared.thrift"

typedef i64 UserID
typedef UserID AccountID

enum Status {
  OK = 1
}

struct Tag {
  1: required string key
  2: optional string value
  3: i32 weight
}

union Payload {
  1: string text
  2: binary blob
}

exception NotFound {
  1: string message
}

service Registry {
  Tag get(1: UserID user, 2: shared.Options opts) throws (1: NotFound missing)
  oneway void ping(1: i32 nonce)
  void touch(1: AccountID account)
  list<Tag> all(1: map<string, Tag> filter, 2: set<i32> ids)
}
`

func TestServiceOperationAndSyntheticShapes(t *testing.T) {
	corpus := singleFileCorpus(demoIDL)
	facts, coverage := mustExtract(t, corpus)
	content := corpus.files["rpc/svc.thrift"]

	if got := coverage.Protocols; !reflect.DeepEqual(got, []string{"lineage-provisional-repo-path-v1", "thrift"}) {
		t.Fatalf("protocols = %v", got)
	}
	if coverage.UnresolvedCount != 0 {
		t.Fatalf("unresolved = %d", coverage.UnresolvedCount)
	}

	service := findPredicateFact(t, facts, "DECLARES_SERVICE", "demo.Registry")
	serviceDetail := decodeDetail[decodedServiceDetail](t, service)
	if serviceDetail.Schema != "thrift-service-detail-v1" || serviceDetail.Name != "Registry" || serviceDetail.Extends != "" {
		t.Fatalf("service detail = %+v", serviceDetail)
	}
	assertLineSpan(t, content, service, 28, "service Registry")

	operation := findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Registry/get")
	get := decodeDetail[decodedOperationDetail](t, operation)
	if get.Schema != "thrift-operation-detail-v1" || get.OneWay ||
		get.Request.Declaration != "demo.Registry.get_args" ||
		get.Request.Resolution != "same_file" ||
		get.Response.Declaration != "demo.Registry.get_result" {
		t.Fatalf("get detail = %+v", get)
	}
	assertLineSpan(t, content, operation, 29, "Tag get(")

	args := findPredicateFact(t, facts, "DECLARES_MESSAGE", "demo.Registry.get_args")
	argsDetail := decodeDetail[decodedMessageDetail](t, args)
	if !argsDetail.Synthetic || argsDetail.Kind != "struct" || argsDetail.Name != "get_args" {
		t.Fatalf("args detail = %+v", argsDetail)
	}

	user := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Registry.get_args#1"))
	if user.Type == nil || user.Type.Raw != "UserID" || user.Type.Kind != "scalar" ||
		user.Type.Resolution != "intrinsic" || user.Cardinality != "default" {
		t.Fatalf("typedef param = %+v", user)
	}

	opts := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Registry.get_args#2"))
	if opts.Type == nil || opts.Type.Resolution != "unresolved" ||
		opts.Type.Reason != "IMPORT_LINKING_UNAVAILABLE" ||
		opts.Type.Imports == nil || opts.Type.Imports.Count != 1 ||
		opts.Type.Imports.Paths[0] != "shared.thrift" {
		t.Fatalf("include-qualified param = %+v", opts)
	}

	success := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Registry.get_result#0"))
	if success.Name != "success" || success.Type == nil || success.Type.Declaration != "demo.Tag" ||
		success.Type.Kind != "message" || success.Type.Resolution != "same_file" {
		t.Fatalf("success field = %+v", success)
	}
	missing := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Registry.get_result#1"))
	if missing.Name != "missing" || missing.Type == nil || missing.Type.Declaration != "demo.NotFound" {
		t.Fatalf("throws field = %+v", missing)
	}
}

func TestOnewayAndVoidResultShapes(t *testing.T) {
	facts, _ := mustExtract(t, singleFileCorpus(demoIDL))

	ping := decodeDetail[decodedOperationDetail](t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Registry/ping"))
	if !ping.OneWay || ping.Response.Raw != "void" || ping.Response.Resolution != "intrinsic" {
		t.Fatalf("oneway detail = %+v", ping)
	}
	if hasFact(facts, "DECLARES_MESSAGE", "demo.Registry.ping_result") {
		t.Fatal("oneway function must not declare a result struct")
	}

	touch := decodeDetail[decodedOperationDetail](t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Registry/touch"))
	if touch.OneWay || touch.Response.Declaration != "demo.Registry.touch_result" {
		t.Fatalf("void non-oneway detail = %+v", touch)
	}
	if !hasFact(facts, "DECLARES_MESSAGE", "demo.Registry.touch_result") {
		t.Fatal("void non-oneway function must declare a result struct")
	}
	if hasFact(facts, "DECLARES_FIELD", "demo.Registry.touch_result#0") {
		t.Fatal("void return must not declare a success field")
	}
}

func TestContainersAndMessageKinds(t *testing.T) {
	facts, _ := mustExtract(t, singleFileCorpus(demoIDL))

	all := decodeDetail[decodedOperationDetail](t, findPredicateFact(t, facts, "DECLARES_OPERATION", "demo.Registry/all"))
	if all.Response.Declaration != "demo.Registry.all_result" {
		t.Fatalf("all detail = %+v", all)
	}
	success := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Registry.all_result#0"))
	if success.Type == nil || success.Type.Raw != "list<Tag>" ||
		success.Type.Declaration != "demo.Tag" || success.Type.Resolution != "same_file" {
		t.Fatalf("list return = %+v", success)
	}
	filter := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Registry.all_args#1"))
	if filter.Map == nil || filter.Map.Key.Raw != "string" ||
		filter.Map.Value.Declaration != "demo.Tag" || filter.Type != nil {
		t.Fatalf("map param = %+v", filter)
	}
	ids := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Registry.all_args#2"))
	if ids.Type == nil || ids.Type.Raw != "set<i32>" || ids.Type.Resolution != "intrinsic" {
		t.Fatalf("set param = %+v", ids)
	}

	for object, wantKind := range map[string]string{
		"demo.Tag":      "struct",
		"demo.Payload":  "union",
		"demo.NotFound": "exception",
	} {
		detail := decodeDetail[decodedMessageDetail](t, findPredicateFact(t, facts, "DECLARES_MESSAGE", object))
		if detail.Kind != wantKind || detail.Synthetic {
			t.Fatalf("%s detail = %+v", object, detail)
		}
	}

	tag := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Tag#1"))
	if tag.Cardinality != "required" {
		t.Fatalf("required field = %+v", tag)
	}
	value := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Tag#2"))
	if value.Cardinality != "optional" {
		t.Fatalf("optional field = %+v", value)
	}
	weight := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "demo.Tag#3"))
	if weight.Cardinality != "default" {
		t.Fatalf("default field = %+v", weight)
	}

	if hasFact(facts, "DECLARES_MESSAGE", "demo.Status") || hasFact(facts, "DECLARES_SERVICE", "demo.Status") {
		t.Fatal("enums must stay resolution-only")
	}
}

func TestScopePrecedence(t *testing.T) {
	cases := []struct {
		name, header, wantScope string
	}{
		{"go namespace last segment", "namespace go acme.billing.v1\n", "v1"},
		{"wildcard namespace ignored", "namespace * acme.billing\n", "svc"},
		{"basename fallback", "", "svc"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := testCase.header + "service S { void f(1: i32 x) }\n"
			facts, _ := mustExtract(t, singleFileCorpus(source))
			want := testCase.wantScope + ".S"
			if !hasFact(facts, "DECLARES_SERVICE", want) {
				t.Fatalf("missing %s in %+v", want, facts)
			}
		})
	}
}

func TestTypedefChaseCycleAndNestedContainers(t *testing.T) {
	source := `typedef B A
typedef A B
typedef list<list<i32>> Deep
struct S {
  1: A looped
  2: Deep nested
  3: Missing gone
}
service V { void f(1: i32 x) }
`
	facts, _ := mustExtract(t, singleFileCorpus(source))
	looped := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "svc.S#1"))
	if looped.Type == nil || looped.Type.Resolution != "unresolved" || looped.Type.Reason != "TYPEDEF_CHAIN_LIMIT" {
		t.Fatalf("typedef cycle = %+v", looped)
	}
	nested := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "svc.S#2"))
	if nested.Type == nil || nested.Type.Resolution != "unresolved" || nested.Type.Reason != "NESTED_CONTAINER_SHAPE" {
		t.Fatalf("nested container = %+v", nested)
	}
	gone := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "svc.S#3"))
	if gone.Type == nil || gone.Type.Reason != "DECLARATION_NOT_FOUND" {
		t.Fatalf("missing declaration = %+v", gone)
	}
}

func TestDuplicateDeclarationsAreAmbiguous(t *testing.T) {
	source := `struct Dup { 1: i32 a }
enum Dup { X = 1 }
struct S { 1: Dup member }
`
	facts, _ := mustExtract(t, singleFileCorpus(source))
	member := decodeDetail[decodedFieldDetail](t, findPredicateFact(t, facts, "DECLARES_FIELD", "svc.S#1"))
	if member.Type == nil || member.Type.Reason != "AMBIGUOUS_SAME_FILE_DECLARATION" {
		t.Fatalf("duplicate resolution = %+v", member)
	}
}

func TestServiceExtendsRecordedNotExpanded(t *testing.T) {
	source := `service Base { void ping(1: i32 n) }
service Derived extends Base { void extra(1: i32 n) }
`
	facts, _ := mustExtract(t, singleFileCorpus(source))
	derived := decodeDetail[decodedServiceDetail](t, findPredicateFact(t, facts, "DECLARES_SERVICE", "svc.Derived"))
	if derived.Extends != "Base" {
		t.Fatalf("extends = %+v", derived)
	}
	if hasFact(facts, "DECLARES_OPERATION", "svc.Derived/ping") {
		t.Fatal("inherited operations must not be expanded")
	}
}

func TestImplicitFieldIDFailsClosedAsFileGap(t *testing.T) {
	source := `struct Loose {
  string name
  2: i32 ok
}
service S { void f(1: i32 x) }
`
	corpus := singleFileCorpus(source)
	facts, coverage := mustExtract(t, corpus)
	if len(facts) != 1 || coverage.UnresolvedCount != 1 {
		t.Fatalf("facts = %d, unresolved = %d; want a single gap fact", len(facts), coverage.UnresolvedCount)
	}
	gap := facts[0]
	if gap.Assertion.Predicate != "THRIFT_EXTRACTION_GAP" || gap.Assertion.Object != "implicit_field_id" ||
		gap.Assertion.Tier != "unresolved" || gap.Assertion.Lineage != "" {
		t.Fatalf("gap fact = %+v", gap.Assertion)
	}
	assertLineSpan(t, source, gap, 2, "string name")
}

func TestParseAndComplexityFailuresFailClosed(t *testing.T) {
	cases := []struct {
		name, source string
	}{
		{"syntax error", "service {"},
		{"unterminated comment", "/* forever\nservice S { void f(1: i32 x) }"},
		{"unterminated string", `const string X = "open`},
		{"nesting bomb", strings.Repeat("<", 200)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := extractFacts(t, singleFileCorpus(testCase.source))
			if err == nil {
				t.Fatal("expected extraction error")
			}
		})
	}
	if _, _, err := extractFacts(t, memoryCorpus{
		repo: "r", commit: "c",
		files:      map[string]string{"a.thrift": "service S { void f(1: i32 x) }\n"},
		readErrors: map[string]error{"b.thrift": errors.New("boom")},
	}); err == nil {
		t.Fatal("candidate read failure must fail the run")
	}
}

func TestRunsAreByteIdentical(t *testing.T) {
	corpus := memoryCorpus{
		repo: "github.com/acme/idl", commit: "c0ffee",
		files: map[string]string{
			"rpc/svc.thrift": demoIDL,
			"vendor/dep.thrift": "service Dep { void f(1: i32 x) }\n",
			"tests/fixture.thrift": "struct F { 1: i32 n }\n",
		},
	}
	first, _ := mustExtract(t, corpus)
	second, _ := mustExtract(t, corpus)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("extraction is not deterministic")
	}
	vendorFact := findPredicateFact(t, first, "DECLARES_SERVICE", "dep.Dep")
	if vendorFact.Assertion.CodeRole != "vendor" {
		t.Fatalf("vendor role = %q", vendorFact.Assertion.CodeRole)
	}
	testFact := findPredicateFact(t, first, "DECLARES_MESSAGE", "fixture.F")
	if testFact.Assertion.CodeRole != "test" {
		t.Fatalf("test role = %q", testFact.Assertion.CodeRole)
	}
}

func TestIdenticalCrossRepoBlobsShareAtomIdentity(t *testing.T) {
	source := "service S { void f(1: i32 x) }\n"
	left, _ := mustExtract(t, memoryCorpus{
		repo: "github.com/acme/a", commit: "c1",
		files: map[string]string{"same/path.thrift": source},
	})
	right, _ := mustExtract(t, memoryCorpus{
		repo: "github.com/acme/b", commit: "c2",
		files: map[string]string{"same/path.thrift": source},
	})
	leftFact := findPredicateFact(t, left, "DECLARES_SERVICE", "path.S")
	rightFact := findPredicateFact(t, right, "DECLARES_SERVICE", "path.S")
	if !reflect.DeepEqual(leftFact.Atom, rightFact.Atom) {
		t.Fatalf("atoms differ: %+v vs %+v", leftFact.Atom, rightFact.Atom)
	}
	if leftFact.Assertion.Lineage == rightFact.Assertion.Lineage {
		t.Fatal("lineage must be repository-scoped")
	}
}
