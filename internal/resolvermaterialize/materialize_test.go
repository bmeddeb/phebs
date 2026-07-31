package resolvermaterialize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/resolverinput"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	testRepository     = "example.invalid/resolver-fixture"
	testCommit         = "cccccccccccccccccccccccccccccccccccccccc"
	testManifestDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

type materializeTestExtractor struct {
	domain  string
	version string
}

func (extractor materializeTestExtractor) Domain() string  { return extractor.domain }
func (extractor materializeTestExtractor) Version() string { return extractor.version }
func (materializeTestExtractor) Candidate(string) bool     { return false }
func (materializeTestExtractor) Extract(
	context.Context,
	sdk.Corpus,
	sdk.Emit,
) (sdk.Coverage, error) {
	return sdk.Coverage{}, nil
}

type materializeTestManifest struct {
	identity            string
	files               []extract.CandidateManifestFile
	generatedModuleRows int
}

func (manifest *materializeTestManifest) Identity() string { return manifest.identity }
func (manifest *materializeTestManifest) CorpusFileCount() int {
	return len(manifest.files) + manifest.generatedModuleRows
}
func (*materializeTestManifest) GitlinkBoundaries() extract.CandidateManifestGitlinks {
	return extract.CandidateManifestGitlinks{}
}
func (manifest *materializeTestManifest) DomainScope(
	string,
	string,
) (extract.CandidateManifestScope, error) {
	return extract.CandidateManifestScope{
		ManifestDigest:  manifest.identity,
		CorpusFileCount: manifest.CorpusFileCount(),
	}, nil
}
func (*materializeTestManifest) TypedInput(
	string,
	string,
	string,
) (extract.CandidateManifestTypedInput, error) {
	return extract.CandidateManifestTypedInput{}, store.ErrNotFound
}
func (manifest *materializeTestManifest) PathInScope(filePath string) bool {
	for _, file := range manifest.files {
		if file.Path == filePath {
			return true
		}
	}
	return false
}
func (manifest *materializeTestManifest) ForEachRepositoryFile(
	ctx context.Context,
	_, _ string,
	visit func(extract.CandidateManifestFile) error,
) error {
	for _, file := range manifest.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(file); err != nil {
			return err
		}
	}
	for index := 0; index < manifest.generatedModuleRows; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(extract.CandidateManifestFile{
			Path:     fmt.Sprintf("modules/%06d/go.mod", index),
			ObjectID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Mode:     "100644", DeclaredBytes: resolverinput.MaxGoModuleBytes + 1,
			SourceLane: candidate.SourceLaneBase,
		}); err != nil {
			return err
		}
	}
	return nil
}

type materializeBlobFixture struct {
	next       int
	blobs      map[string][]byte
	paths      map[string]string
	errors     map[string]error
	readPaths  []string
	readLimits []int64
}

func newMaterializeBlobFixture() *materializeBlobFixture {
	return &materializeBlobFixture{
		blobs: make(map[string][]byte), paths: make(map[string]string),
		errors: make(map[string]error),
	}
}

func (fixture *materializeBlobFixture) add(
	filePath string,
	content string,
) extract.CandidateManifestFile {
	file := fixture.envelope(filePath, int64(len(content)))
	fixture.blobs[file.ObjectID] = []byte(content)
	return file
}

func (fixture *materializeBlobFixture) envelope(
	filePath string,
	declaredBytes int64,
) extract.CandidateManifestFile {
	fixture.next++
	oid := fmt.Sprintf("%040x", fixture.next)
	fixture.paths[oid] = filePath
	return extract.CandidateManifestFile{
		Path: filePath, ObjectID: oid, Mode: "100644",
		DeclaredBytes: declaredBytes, SourceLane: candidate.SourceLaneBase,
	}
}

func (fixture *materializeBlobFixture) read(
	ctx context.Context,
	_ string,
	oid string,
	limit int64,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fixture.readPaths = append(fixture.readPaths, fixture.paths[oid])
	fixture.readLimits = append(fixture.readLimits, limit)
	if err := fixture.errors[oid]; err != nil {
		return nil, err
	}
	content, present := fixture.blobs[oid]
	if !present {
		return nil, store.ErrNotFound
	}
	if int64(len(content)) > limit {
		return nil, gitobj.ErrTooLarge
	}
	return slices.Clone(content), nil
}

type materializeAssertionReader struct {
	rows  map[string][]store.Assertion
	calls []store.AssertionQuery
}

func (reader *materializeAssertionReader) ListAssertions(
	ctx context.Context,
	query store.AssertionQuery,
) ([]store.Assertion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader.calls = append(reader.calls, query)
	rows := slices.Clone(reader.rows[query.RunID])
	sort.Slice(rows, func(i, j int) bool {
		return assertionOrder(rows[i], rows[j]) < 0
	})
	filtered := rows[:0]
	for _, row := range rows {
		if row.Repo == query.Repo && row.RunID == query.RunID &&
			row.Predicate == query.Predicate {
			filtered = append(filtered, row)
		}
	}
	start := 0
	if query.After != nil {
		start = -1
		for index, row := range filtered {
			if row.Predicate == query.After.Predicate &&
				row.Subject == query.After.Subject &&
				row.Object == query.After.Object && row.ID == query.After.ID &&
				row.RunID == query.After.RunID {
				start = index + 1
				break
			}
		}
		if start < 0 {
			return nil, errors.New("assertion continuation was not found")
		}
	}
	limit := query.Limit
	if limit <= 0 {
		limit = len(filtered)
	}
	if query.AllowTruncate {
		limit++
	}
	end := min(start+limit, len(filtered))
	return slices.Clone(filtered[start:end]), nil
}

func assertionOrder(left, right store.Assertion) int {
	for _, pair := range [][2]string{
		{left.Predicate, right.Predicate},
		{left.Subject, right.Subject},
		{left.Object, right.Object},
		{left.ID, right.ID},
		{left.RunID, right.RunID},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

type materializedImage struct {
	manifest []byte
	members  map[string][]byte
}

func TestBuildResolvedInputsAreDeterministicAndBlobBounded(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift)
	firstRequest, firstBlobs := resolvedMaterializeRequest(t, t.TempDir(), registry)
	first := buildMaterializedImage(t, firstRequest)

	secondRequest, secondBlobs := resolvedMaterializeRequest(t, t.TempDir(), registry)
	second := buildMaterializedImage(t, secondRequest)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("identical resolver inputs produced different manifest or member bytes")
	}
	var catalogManifest resolvercatalog.Manifest
	if err := json.Unmarshal(first.manifest, &catalogManifest); err != nil {
		t.Fatal(err)
	}
	for _, member := range catalogManifest.Members {
		var metadata memberMetadata
		if err := json.Unmarshal(member.Metadata, &metadata); err != nil {
			t.Fatal(err)
		}
		if member.Name != "go-module-v1.ndjson" &&
			metadata.RecordSchema != ProtocolMemberRecordSchema {
			t.Fatalf("protocol member %q schema = %q, want %q",
				member.Name, metadata.RecordSchema, ProtocolMemberRecordSchema)
		}
		if metadata.Policy.ModuleParsing !=
			"single-line-strict-token-factored-unsupported-v1" {
			t.Fatalf("member %q module parsing policy = %q",
				member.Name, metadata.Policy.ModuleParsing)
		}
		if metadata.Policy.MaxGoModuleBytes != resolverinput.MaxGoModuleBytes ||
			metadata.Policy.MaxSnapshotBlobBytes != extract.MaxBlobBytes {
			t.Fatalf("member %q per-input policy = module:%d snapshot:%d",
				member.Name, metadata.Policy.MaxGoModuleBytes,
				metadata.Policy.MaxSnapshotBlobBytes)
		}
	}

	moduleRecords := decodeMember[moduleRecord](t, first.members["go-module-v1.ndjson"])
	if len(moduleRecords) != 2 {
		t.Fatalf("module records = %+v", moduleRecords)
	}
	wantModules := []moduleRecord{
		{
			Path: "go.mod", ModuleRoot: ".", ModulePath: "example.invalid/root",
			State: StateResolved,
		},
		{
			Path: "services/payments/go.mod", ModuleRoot: "services/payments",
			ModulePath: "example.invalid/payments", State: StateResolved,
		},
	}
	for index, want := range wantModules {
		got := moduleRecords[index]
		if got.Path != want.Path || got.ModuleRoot != want.ModuleRoot ||
			got.ModulePath != want.ModulePath || got.State != want.State ||
			got.ContentDigest == "" {
			t.Errorf("module record %d = %+v, want identity %+v", index, got, want)
		}
	}

	grpcRecords := decodeMember[generatedRecord](
		t, first.members[GRPCGeneratedPackName+"-v1.ndjson"],
	)
	grpc := findGeneratedRecord(t, grpcRecords, "generated_mapping", "gen/grpc/orders_grpc.pb.go")
	if grpc.State != StateResolved || grpc.Protocol != ProtocolGRPC ||
		len(grpc.Candidates) != 1 ||
		grpc.Candidates[0].DeclarationPath != "idl/proto/orders.proto" {
		t.Fatalf("gRPC generated record = %+v", grpc)
	}
	thriftRecords := decodeMember[generatedRecord](
		t, first.members[ThriftGeneratedPackName+"-v1.ndjson"],
	)
	thrift := findGeneratedRecord(t, thriftRecords, "generated_mapping", "gen/thrift/ledger.go")
	if thrift.State != StateResolved || thrift.Protocol != ProtocolThrift ||
		len(thrift.Candidates) != 1 ||
		thrift.Candidates[0].DeclarationPath != "idl/thrift/ledger.thrift" {
		t.Fatalf("Thrift generated record = %+v", thrift)
	}

	wantReads := []string{
		resolverinput.LayoutSnapshotPath,
		resolverinput.GeneratedFromSnapshotPath,
		"go.mod",
		"services/payments/go.mod",
	}
	for index, got := range [][]string{firstBlobs.readPaths, secondBlobs.readPaths} {
		if !slices.Equal(got, wantReads) {
			t.Errorf("build %d blob reads = %v, want %v", index+1, got, wantReads)
		}
		for _, forbidden := range []string{
			"src/main.go", "unit-snapshot.json", "gen/grpc/orders_grpc.pb.go",
			"gen/thrift/ledger.go", "idl/proto/orders.proto",
			"idl/thrift/ledger.thrift",
		} {
			if slices.Contains(got, forbidden) {
				t.Errorf("build %d read forbidden non-resolver blob %q", index+1, forbidden)
			}
		}
	}
}

func TestBuildModuleStates(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	testCases := []struct {
		name, content, state, reason string
		oversized                    bool
	}{
		{
			name: "missing directive", content: "go 1.26\n",
			state: StateUnavailable, reason: string(resolverinput.ModuleMissingDirective),
		},
		{
			name: "malformed directive", content: "module example.invalid/root extra\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleMalformedDirective),
		},
		{
			name: "factored directive", content: "module (\n\texample.invalid/root\n)\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleFactoredDirective),
		},
		{
			name: "single quoted directive", content: "module 'example.invalid/root'\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleMalformedDirective),
		},
		{
			name: "backtick directive", content: "module `example.invalid/root`\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleMalformedDirective),
		},
		{
			name: "embedded quote directive", content: "module example.invalid/\"root\"\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleMalformedDirective),
		},
		{
			name: "punctuated directive", content: "module example.invalid/root)\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleMalformedDirective),
		},
		{
			name:    "block comment directive",
			content: "module example.invalid/root/*comment*/suffix\n",
			state:   StateUnsupported, reason: string(resolverinput.ModuleMalformedDirective),
		},
		{
			name: "control byte directive", content: "module example.invalid/\x01root\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleMalformedDirective),
		},
		{
			name: "invalid import path", content: "module example.invalid/root.\n",
			state: StateUnsupported, reason: string(resolverinput.ModuleInvalidPath),
		},
		{
			name:    "multiple directives",
			content: "module example.invalid/one\nmodule example.invalid/two\n",
			state:   StateAmbiguous, reason: string(resolverinput.ModuleMultipleDirectives),
		},
		{
			name: "oversized", state: StateUnsupported,
			reason: string(resolverinput.ModuleOversized), oversized: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			blobs := newMaterializeBlobFixture()
			var file extract.CandidateManifestFile
			if testCase.oversized {
				file = blobs.envelope("go.mod", resolverinput.MaxGoModuleBytes+1)
			} else {
				file = blobs.add("go.mod", testCase.content)
			}
			manifest := &materializeTestManifest{
				identity: testManifestDigest, files: []extract.CandidateManifestFile{file},
			}
			request := newMaterializeBuildRequest(
				t, t.TempDir(), registry, manifest, blobs,
				nil, &materializeAssertionReader{},
			)
			image := buildMaterializedImage(t, request)
			records := decodeMember[moduleRecord](t, image.members["go-module-v1.ndjson"])
			if len(records) != 1 || records[0].State != testCase.state ||
				records[0].Reason != testCase.reason || records[0].ModulePath != "" {
				t.Fatalf("module records = %+v, want state=%q reason=%q",
					records, testCase.state, testCase.reason)
			}
			if testCase.oversized && records[0].ContentDigest != "" ||
				!testCase.oversized && records[0].ContentDigest == "" {
				t.Fatalf("module content digest = %q, oversized=%v",
					records[0].ContentDigest, testCase.oversized)
			}
			wantReads := 1
			if testCase.oversized {
				wantReads = 0
			}
			if len(blobs.readPaths) != wantReads {
				t.Fatalf("blob reads = %v, want %d", blobs.readPaths, wantReads)
			}
		})
	}
}

func TestBuildSnapshotStates(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	testCases := []struct {
		name, state, reason string
		files               func(*materializeBlobFixture) []extract.CandidateManifestFile
	}{
		{
			name: "missing", state: StateUnavailable,
			reason: "generated_snapshot_missing",
			files:  func(*materializeBlobFixture) []extract.CandidateManifestFile { return nil },
		},
		{
			name: "unsupported generated version", state: StateUnsupported,
			reason: "unsupported_generated_version",
			files: func(blobs *materializeBlobFixture) []extract.CandidateManifestFile {
				return []extract.CandidateManifestFile{blobs.add(
					resolverinput.GeneratedFromSnapshotPath,
					`{"version":"future-generated","mappings":[]}`,
				)}
			},
		},
		{
			name: "unsupported layout version", state: StateUnsupported,
			reason: "unsupported_layout_version",
			files: func(blobs *materializeBlobFixture) []extract.CandidateManifestFile {
				return []extract.CandidateManifestFile{
					blobs.add(resolverinput.LayoutSnapshotPath,
						`{"version":"future-layout","roots":[]}`),
					blobs.add(resolverinput.GeneratedFromSnapshotPath,
						`{"version":"t20-generated-from-v1","mappings":[]}`),
				}
			},
		},
		{
			name: "unsupported invocation protocol", state: StateUnsupported,
			reason: "unsupported_invocation_protocol",
			files: func(blobs *materializeBlobFixture) []extract.CandidateManifestFile {
				return []extract.CandidateManifestFile{blobs.add(
					resolverinput.GeneratedFromSnapshotPath,
					`{"version":"t20-generated-from-v1","mappings":[],`+
						`"invocations":[{"protocol":"bogus","generated_root":"gen",`+
						`"generator_invocation_root":"idl"}]}`,
				)}
			},
		},
		{
			name: "duplicate generated field", state: StateUnsupported,
			reason: "malformed_generated_snapshot",
			files: func(blobs *materializeBlobFixture) []extract.CandidateManifestFile {
				return []extract.CandidateManifestFile{blobs.add(
					resolverinput.GeneratedFromSnapshotPath,
					`{"version":"t20-generated-from-v1","mappings":[],"mappings":[]}`,
				)}
			},
		},
		{
			name: "duplicate layout root field", state: StateUnsupported,
			reason: "malformed_layout_snapshot",
			files: func(blobs *materializeBlobFixture) []extract.CandidateManifestFile {
				return []extract.CandidateManifestFile{
					blobs.add(resolverinput.LayoutSnapshotPath,
						`{"version":"t20-layout-snapshot-v1","roots":[`+
							`{"kind":"source","path":"src","path":"other"}]}`),
					blobs.add(resolverinput.GeneratedFromSnapshotPath,
						`{"version":"t20-generated-from-v1","mappings":[]}`),
				}
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			blobs := newMaterializeBlobFixture()
			manifest := &materializeTestManifest{
				identity: testManifestDigest, files: testCase.files(blobs),
			}
			request := newMaterializeBuildRequest(
				t, t.TempDir(), registry, manifest, blobs,
				nil, &materializeAssertionReader{},
			)
			image := buildMaterializedImage(t, request)
			records := decodeMember[generatedRecord](
				t, image.members[GRPCGeneratedPackName+"-v1.ndjson"],
			)
			status := findGeneratedRecord(t, records, "generated_attribution_input", "")
			if status.State != testCase.state || status.Reason != testCase.reason {
				t.Fatalf("snapshot status = %+v, want state=%q reason=%q",
					status, testCase.state, testCase.reason)
			}
		})
	}
}

func TestBuildRejectsDeclarationIdentityMismatch(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift)
	for _, testCase := range []struct {
		name, want string
		mutate     func(*BuildRequest)
	}{
		{
			name: "run differs from identity",
			want: "do not match generation identity",
			mutate: func(request *BuildRequest) {
				request.Declarations = slices.Clone(request.Declarations)
				request.Declarations[0].RunID = "run-forged"
			},
		},
		{
			name: "adapter differs from domain",
			want: "no enabled adapter",
			mutate: func(request *BuildRequest) {
				request.Declarations = slices.Clone(request.Declarations)
				request.Declarations[0].Protocol = ProtocolThrift
			},
		},
		{
			name: "input order differs",
			want: "not strictly ordered",
			mutate: func(request *BuildRequest) {
				request.Declarations = slices.Clone(request.Declarations)
				slices.Reverse(request.Declarations)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, _ := resolvedMaterializeRequest(t, t.TempDir(), registry)
			testCase.mutate(&request)
			prepared, err := Build(t.Context(), request)
			if prepared != nil {
				_ = prepared.Discard()
			}
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Build() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestBuildSnapshotCollectionLimitsAreExplicit(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	testCases := []struct {
		name, reason string
		content      func() any
	}{
		{
			name: "layout roots", reason: "layout_root_limit_exceeded",
			content: func() any {
				value := resolverinput.LayoutSnapshot{
					Version: resolverinput.LayoutSnapshotVersion,
				}
				for index := 0; index <= MaxLayoutRoots; index++ {
					value.Roots = append(value.Roots, resolverinput.LayoutRoot{
						Kind: "source", Path: fmt.Sprintf("source/%03d", index),
					})
				}
				return value
			},
		},
		{
			name: "generated mappings", reason: "generated_mapping_limit_exceeded",
			content: func() any {
				value := resolverinput.GeneratedFromSnapshot{
					Version: resolverinput.GeneratedFromSnapshotVersion,
				}
				for index := 0; index <= MaxGeneratedMappings; index++ {
					value.Mappings = append(value.Mappings, resolverinput.GeneratedFromMapping{
						GeneratedPath:   fmt.Sprintf("gen/%05d.go", index),
						DeclarationPath: fmt.Sprintf("idl/%05d.proto", index),
					})
				}
				return value
			},
		},
		{
			name: "generator invocations", reason: "generator_invocation_limit_exceeded",
			content: func() any {
				value := resolverinput.GeneratedFromSnapshot{
					Version: resolverinput.GeneratedFromSnapshotVersion,
				}
				for index := 0; index <= MaxGeneratorInvocations; index++ {
					value.Invocations = append(value.Invocations, resolverinput.GeneratorInvocation{
						Protocol: "grpc", GeneratedRoot: fmt.Sprintf("gen/%03d", index),
						GeneratorInvocationRoot: fmt.Sprintf("idl/%03d", index),
					})
				}
				return value
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			raw, err := json.Marshal(testCase.content())
			if err != nil {
				t.Fatal(err)
			}
			blobs := newMaterializeBlobFixture()
			inputPath := resolverinput.GeneratedFromSnapshotPath
			files := []extract.CandidateManifestFile{}
			if testCase.name == "layout roots" {
				inputPath = resolverinput.LayoutSnapshotPath
				files = append(files, blobs.add(
					resolverinput.GeneratedFromSnapshotPath,
					`{"version":"t20-generated-from-v1","mappings":[]}`,
				))
			}
			files = append(files, blobs.add(inputPath, string(raw)))
			manifest := &materializeTestManifest{
				identity: testManifestDigest, files: files,
			}
			request := newMaterializeBuildRequest(
				t, t.TempDir(), registry, manifest, blobs,
				nil, &materializeAssertionReader{},
			)
			image := buildMaterializedImage(t, request)
			records := decodeMember[generatedRecord](
				t, image.members[GRPCGeneratedPackName+"-v1.ndjson"],
			)
			status := findGeneratedRecord(t, records, "generated_attribution_input", "")
			if status.State != StateUnsupported || status.Reason != testCase.reason {
				t.Fatalf("snapshot status = %+v, want unsupported/%s", status, testCase.reason)
			}
		})
	}
}

func TestBlobBudgetAcceptsCapAndRefusesCapPlusOneBeforeRead(t *testing.T) {
	file := extract.CandidateManifestFile{
		Path: "go.mod", ObjectID: strings.Repeat("a", 40), Mode: "100644",
		DeclaredBytes: 2, SourceLane: candidate.SourceLaneBase,
	}
	for _, testCase := range []struct {
		name   string
		budget blobBudget
	}{
		{name: "reads", budget: blobBudget{reads: MaxInputBlobReads - 1}},
		{name: "bytes", budget: blobBudget{bytes: MaxInputBlobBytes - 2}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			budget := testCase.budget
			budget.read = func(context.Context, string, string, int64) ([]byte, error) {
				calls++
				return []byte("xx"), nil
			}
			if _, err := budget.load(t.Context(), file, 2); err != nil {
				t.Fatalf("exact-cap load = %v", err)
			}
			if _, err := budget.load(t.Context(), file, 2); !errors.Is(err, resolvercatalog.ErrLimit) {
				t.Fatalf("cap+1 error = %v, want ErrLimit", err)
			}
			if calls != 1 {
				t.Fatalf("blob reads = %d, want exact-cap read only", calls)
			}
		})
	}
}

func TestBlobBudgetBindsReadToCandidateDeclaredBytes(t *testing.T) {
	file := extract.CandidateManifestFile{
		Path: "go.mod", ObjectID: strings.Repeat("a", 40), Mode: "100644",
		DeclaredBytes: 1, SourceLane: candidate.SourceLaneBase,
	}
	called := false
	budget := blobBudget{read: func(
		_ context.Context,
		_, _ string,
		limit int64,
	) ([]byte, error) {
		called = true
		if limit != file.DeclaredBytes {
			t.Fatalf("read limit = %d, want declared size %d", limit, file.DeclaredBytes)
		}
		return nil, gitobj.ErrTooLarge
	}}
	if _, err := budget.load(t.Context(), file, 1<<20); !errors.Is(err, gitobj.ErrTooLarge) {
		t.Fatalf("load = %v, want ErrTooLarge", err)
	}
	if !called {
		t.Fatal("blob reader was not called")
	}
}

func TestDuplicateGeneratedAuthorityDoesNotBecomeResolved(t *testing.T) {
	current := newMaterializeTestRegistry(t, ProtocolGRPC).adapters[0]
	mapping := resolverinput.GeneratedFromMapping{
		Protocol: "grpc", GeneratedPath: "gen/api.pb.go",
		DeclarationPath: "idl/api.proto",
	}
	plan := &generatedPlan{
		generated: &resolverinput.GeneratedFromSnapshot{
			Mappings: []resolverinput.GeneratedFromMapping{mapping, mapping},
			Invocations: []resolverinput.GeneratorInvocation{
				{Protocol: "grpc", GeneratedRoot: "gen", GeneratorInvocationRoot: "idl"},
				{Protocol: "grpc", GeneratedRoot: "gen", GeneratorInvocationRoot: "idl"},
			},
		},
		generatedPaths:  map[string]bool{"gen/api.pb.go": true},
		invocationRoots: map[string]bool{"gen": true},
	}
	targets := &declarationTargets{byPath: map[string]map[string]struct{}{
		"idl/api.proto": {"lineage": {}},
	}}
	var raws []json.RawMessage
	write := func(raw json.RawMessage) error {
		raws = append(raws, slices.Clone(raw))
		return nil
	}
	if err := emitGeneratedMappings(
		t.Context(), current, plan, targets, &materializationBudget{}, write,
	); err != nil {
		t.Fatal(err)
	}
	if err := emitGeneratorInvocations(t.Context(), current, plan, targets, write); err != nil {
		t.Fatal(err)
	}
	for index, raw := range raws {
		var record generatedRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.State != StateUnsupported || record.Reason != map[bool]string{
			true: "duplicate_mapping", false: "duplicate_invocation",
		}[index == 0] {
			t.Fatalf("duplicate record %d = %+v", index, record)
		}
	}
}

func TestGeneratedMappingSelectorExpansionBoundary(t *testing.T) {
	current := newMaterializeTestRegistry(t, ProtocolGRPC).adapters[0]
	declarationPath := "idl/api.proto"
	for _, testCase := range []struct {
		name       string
		lineages   func() map[string]struct{}
		wantState  string
		wantReason string
		wantCount  int
	}{
		{
			name: "candidate-count-cap",
			lineages: func() map[string]struct{} {
				return generatedTestLineages(MaxGeneratedCandidatesPerSelector, 12)
			},
			wantState: StateAmbiguous, wantReason: "multiple_declaration_paths",
			wantCount: MaxGeneratedCandidatesPerSelector,
		},
		{
			name: "candidate-count-cap-plus-one",
			lineages: func() map[string]struct{} {
				return generatedTestLineages(MaxGeneratedCandidatesPerSelector+1, 12)
			},
			wantState:  StateUnsupported,
			wantReason: "candidate_expansion_limit_exceeded",
		},
		{
			name: "candidate-byte-cap",
			lineages: func() map[string]struct{} {
				firstBytes := MaxGeneratedCandidateBytesPerSelector/2 - len(declarationPath)
				secondBytes := MaxGeneratedCandidateBytesPerSelector -
					2*len(declarationPath) - firstBytes
				return map[string]struct{}{
					"a" + strings.Repeat("x", firstBytes-1):  {},
					"b" + strings.Repeat("y", secondBytes-1): {},
				}
			},
			wantState: StateAmbiguous, wantReason: "multiple_declaration_paths",
			wantCount: 2,
		},
		{
			name: "candidate-byte-cap-plus-one",
			lineages: func() map[string]struct{} {
				firstBytes := MaxGeneratedCandidateBytesPerSelector/2 - len(declarationPath)
				secondBytes := MaxGeneratedCandidateBytesPerSelector -
					2*len(declarationPath) - firstBytes + 1
				return map[string]struct{}{
					"a" + strings.Repeat("x", firstBytes-1):  {},
					"b" + strings.Repeat("y", secondBytes-1): {},
				}
			},
			wantState:  StateUnsupported,
			wantReason: "candidate_expansion_limit_exceeded",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := generatedExpansionTestPlan(1, declarationPath)
			targets := &declarationTargets{byPath: map[string]map[string]struct{}{
				declarationPath: testCase.lineages(),
			}}
			var records []generatedRecord
			err := emitGeneratedMappings(
				t.Context(), current, plan, targets, &materializationBudget{},
				func(raw json.RawMessage) error {
					var record generatedRecord
					if err := json.Unmarshal(raw, &record); err != nil {
						return err
					}
					records = append(records, record)
					return nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].State != testCase.wantState ||
				records[0].Reason != testCase.wantReason ||
				len(records[0].Candidates) != testCase.wantCount {
				t.Fatalf("selector expansion = %+v", records)
			}
		})
	}
}

func TestGeneratedMappingAggregateExpansionBoundary(t *testing.T) {
	current := newMaterializeTestRegistry(t, ProtocolGRPC).adapters[0]
	declarationPath := "idl/api.proto"
	const lineagesPerSelector = 1000
	if MaxGeneratedCandidateExpansions%lineagesPerSelector != 0 {
		t.Fatal("aggregate expansion test fixture no longer divides the frozen cap")
	}
	groups := MaxGeneratedCandidateExpansions / lineagesPerSelector
	for _, testCase := range []struct {
		name       string
		lineages   int
		wantLimit  bool
		wantWrites int
	}{
		{name: "cap", lineages: lineagesPerSelector, wantWrites: groups},
		{name: "cap-plus-one", lineages: lineagesPerSelector + 1, wantLimit: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := generatedExpansionTestPlan(groups, declarationPath)
			targets := &declarationTargets{byPath: map[string]map[string]struct{}{
				declarationPath: generatedTestLineages(testCase.lineages, 12),
			}}
			writes := 0
			err := emitGeneratedMappings(
				t.Context(), current, plan, targets, &materializationBudget{},
				func(json.RawMessage) error { writes++; return nil },
			)
			if testCase.wantLimit {
				if !errors.Is(err, resolvercatalog.ErrLimit) {
					t.Fatalf("aggregate cap+1 error = %v, want ErrLimit", err)
				}
			} else if err != nil {
				t.Fatalf("aggregate cap error = %v", err)
			}
			if writes != testCase.wantWrites {
				t.Fatalf("aggregate writes = %d, want %d", writes, testCase.wantWrites)
			}
		})
	}
}

func TestGeneratedMappingAggregateCapSpansAdaptersInBuild(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift)
	grpc := materializeTestDeclaration(ProtocolGRPC)
	thrift := materializeTestDeclaration(ProtocolThrift)
	const lineagesPerProtocol = 1000
	const grpcMappings = 12
	const thriftMappings = 13
	if (grpcMappings+thriftMappings)*lineagesPerProtocol !=
		MaxGeneratedCandidateExpansions {
		t.Fatal("dual-adapter fixture no longer equals the frozen expansion cap")
	}
	for _, testCase := range []struct {
		name      string
		extra     bool
		wantLimit bool
	}{
		{name: "cap"},
		{name: "cap-plus-one", extra: true, wantLimit: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			blobs := newMaterializeBlobFixture()
			mappings := make(
				[]resolverinput.GeneratedFromMapping,
				0, grpcMappings+thriftMappings+1,
			)
			files := make(
				[]extract.CandidateManifestFile,
				0, grpcMappings+thriftMappings+2,
			)
			for index := range grpcMappings {
				generatedPath := fmt.Sprintf("gen/grpc/%02d.pb.go", index)
				mappings = append(mappings, resolverinput.GeneratedFromMapping{
					Protocol: "grpc", GeneratedPath: generatedPath,
					DeclarationPath: "idl/api.proto",
				})
				files = append(files, blobs.add(generatedPath, "package gen\n"))
			}
			for index := range thriftMappings {
				generatedPath := fmt.Sprintf("gen/thrift/%02d.go", index)
				mappings = append(mappings, resolverinput.GeneratedFromMapping{
					Protocol: "thrift", GeneratedPath: generatedPath,
					DeclarationPath: "idl/api.thrift",
				})
				files = append(files, blobs.add(generatedPath, "package gen\n"))
			}
			rows := map[string][]store.Assertion{
				grpc.RunID:   make([]store.Assertion, lineagesPerProtocol),
				thrift.RunID: make([]store.Assertion, lineagesPerProtocol),
			}
			for index := range lineagesPerProtocol {
				rows[grpc.RunID][index] = materializeTestAssertion(
					grpc.RunID, "idl/api.proto", fmt.Sprintf("grpc-%04d", index), index,
				)
				rows[thrift.RunID][index] = materializeTestAssertion(
					thrift.RunID, "idl/api.thrift", fmt.Sprintf("thrift-%04d", index), index,
				)
			}
			if testCase.extra {
				generatedPath := "gen/thrift/extra.go"
				mappings = append(mappings, resolverinput.GeneratedFromMapping{
					Protocol: "thrift", GeneratedPath: generatedPath,
					DeclarationPath: "idl/extra.thrift",
				})
				files = append(files, blobs.add(generatedPath, "package gen\n"))
				rows[thrift.RunID] = append(rows[thrift.RunID], materializeTestAssertion(
					thrift.RunID, "idl/extra.thrift", "thrift-extra", lineagesPerProtocol,
				))
			}
			generated, err := json.Marshal(resolverinput.GeneratedFromSnapshot{
				Version:  resolverinput.GeneratedFromSnapshotVersion,
				Mappings: mappings,
			})
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, blobs.add(
				resolverinput.GeneratedFromSnapshotPath, string(generated),
			))
			manifest := &materializeTestManifest{
				identity: testManifestDigest, files: files,
			}
			request := newMaterializeBuildRequest(
				t, t.TempDir(), registry, manifest, blobs,
				[]DeclarationInput{grpc, thrift},
				&materializeAssertionReader{rows: rows},
			)
			prepared, err := Build(t.Context(), request)
			if prepared != nil {
				_ = prepared.Discard()
			}
			if testCase.wantLimit {
				if !errors.Is(err, resolvercatalog.ErrLimit) {
					t.Fatalf("dual-adapter expansion cap+1 error = %v, want ErrLimit", err)
				}
			} else if err != nil {
				t.Fatalf("dual-adapter expansion cap error = %v", err)
			}
		})
	}
}

func TestGeneratedMappingCrossProductRefusesBeforeExpansion(t *testing.T) {
	current := newMaterializeTestRegistry(t, ProtocolGRPC).adapters[0]
	declarationPath := "idl/api.proto"
	plan := generatedExpansionTestPlan(MaxGeneratedMappings, declarationPath)
	targets := &declarationTargets{byPath: map[string]map[string]struct{}{
		declarationPath: generatedTestLineages(MaxDeclarationRecords, 12),
	}}
	writes := 0
	err := emitGeneratedMappings(
		t.Context(), current, plan, targets, &materializationBudget{},
		func(json.RawMessage) error { writes++; return nil },
	)
	if !errors.Is(err, resolvercatalog.ErrLimit) {
		t.Fatalf("cross-product error = %v, want ErrLimit", err)
	}
	if writes != 0 {
		t.Fatalf("cross-product wrote %d partial records", writes)
	}
}

func generatedExpansionTestPlan(
	groups int,
	declarationPath string,
) *generatedPlan {
	mappings := make([]resolverinput.GeneratedFromMapping, groups)
	generatedPaths := make(map[string]bool, groups)
	for index := range mappings {
		generatedPath := fmt.Sprintf("gen/%05d.pb.go", index)
		mappings[index] = resolverinput.GeneratedFromMapping{
			Protocol: "grpc", GeneratedPath: generatedPath,
			DeclarationPath: declarationPath,
		}
		generatedPaths[generatedPath] = true
	}
	return &generatedPlan{
		generated:       &resolverinput.GeneratedFromSnapshot{Mappings: mappings},
		generatedPaths:  generatedPaths,
		invocationRoots: map[string]bool{},
	}
}

func generatedTestLineages(count, width int) map[string]struct{} {
	lineages := make(map[string]struct{}, count)
	for index := 0; index < count; index++ {
		lineages[fmt.Sprintf("lineage-%0*d", width, index)] = struct{}{}
	}
	return lineages
}

func TestBuildPreservesAmbiguousGeneratedSelector(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	blobs := newMaterializeBlobFixture()
	layout := `{"version":"t20-layout-snapshot-v1","roots":[` +
		`{"kind":"idl","path":"idl","protocol":"grpc"},` +
		`{"kind":"generated","path":"gen","protocol":"grpc"}]}`
	generated := `{"version":"t20-generated-from-v1","mappings":[` +
		`{"generated_path":"gen/api_grpc.pb.go","generator_relative_path":"api.proto",` +
		`"declaration_path":"idl/a/api.proto"},` +
		`{"generated_path":"gen/api_grpc.pb.go","generator_relative_path":"api.proto",` +
		`"declaration_path":"idl/b/api.proto"}]}`
	files := []extract.CandidateManifestFile{
		blobs.add("gen/api_grpc.pb.go", "package gen\n"),
		blobs.add(resolverinput.LayoutSnapshotPath, layout),
		blobs.add(resolverinput.GeneratedFromSnapshotPath, generated),
		blobs.add("idl/a/api.proto", "syntax = \"proto3\";\n"),
		blobs.add("idl/b/api.proto", "syntax = \"proto3\";\n"),
	}
	declaration := materializeTestDeclaration(ProtocolGRPC)
	reader := &materializeAssertionReader{rows: map[string][]store.Assertion{
		declaration.RunID: {
			materializeTestAssertion(declaration.RunID, "idl/a/api.proto", "lineage-a", 1),
			materializeTestAssertion(declaration.RunID, "idl/b/api.proto", "lineage-b", 2),
		},
	}}
	manifest := &materializeTestManifest{identity: testManifestDigest, files: files}
	request := newMaterializeBuildRequest(
		t, t.TempDir(), registry, manifest, blobs,
		[]DeclarationInput{declaration}, reader,
	)
	image := buildMaterializedImage(t, request)
	records := decodeMember[generatedRecord](
		t, image.members[GRPCGeneratedPackName+"-v1.ndjson"],
	)
	mapping := findGeneratedRecord(t, records, "generated_mapping", "gen/api_grpc.pb.go")
	if mapping.State != StateAmbiguous || mapping.Reason != "multiple_declaration_paths" ||
		len(mapping.Candidates) != 2 {
		t.Fatalf("ambiguous selector record = %+v", mapping)
	}
	if got := blobs.readPaths; !slices.Equal(got, []string{
		resolverinput.LayoutSnapshotPath, resolverinput.GeneratedFromSnapshotPath,
	}) {
		t.Fatalf("ambiguous selector blob reads = %v", got)
	}
}

func TestBuildRejectsStaleBlobInputs(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	testCases := []struct {
		name          string
		configure     func(*materializeBlobFixture) extract.CandidateManifestFile
		wantError     error
		wantSubstring string
	}{
		{
			name: "missing object",
			configure: func(blobs *materializeBlobFixture) extract.CandidateManifestFile {
				return blobs.envelope("go.mod", int64(len("module example.invalid/root\n")))
			},
			wantError: store.ErrNotFound,
		},
		{
			name: "declared size mismatch",
			configure: func(blobs *materializeBlobFixture) extract.CandidateManifestFile {
				file := blobs.add("go.mod", "module example.invalid/root\n")
				file.DeclaredBytes++
				return file
			},
			wantSubstring: "length changed",
		},
		{
			name: "special module mode",
			configure: func(blobs *materializeBlobFixture) extract.CandidateManifestFile {
				file := blobs.add("go.mod", "module example.invalid/root\n")
				file.Mode = "120000"
				return file
			},
			wantSubstring: "invalid candidate envelope",
		},
		{
			name: "special fixed-input mode",
			configure: func(blobs *materializeBlobFixture) extract.CandidateManifestFile {
				file := blobs.add(
					resolverinput.LayoutSnapshotPath,
					`{"version":"t20-layout-snapshot-v1","roots":[]}`,
				)
				file.Mode = "120000"
				return file
			},
			wantSubstring: "invalid candidate envelope",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			blobs := newMaterializeBlobFixture()
			manifest := &materializeTestManifest{
				identity: testManifestDigest,
				files:    []extract.CandidateManifestFile{testCase.configure(blobs)},
			}
			request := newMaterializeBuildRequest(
				t, t.TempDir(), registry, manifest, blobs,
				nil, &materializeAssertionReader{},
			)
			prepared, err := Build(context.Background(), request)
			if prepared != nil {
				_ = prepared.Discard()
			}
			if err == nil {
				t.Fatal("Build() succeeded for stale resolver input")
			}
			if testCase.wantError != nil && !errors.Is(err, testCase.wantError) {
				t.Fatalf("Build() error = %v, want %v", err, testCase.wantError)
			}
			if testCase.wantSubstring != "" && !strings.Contains(err.Error(), testCase.wantSubstring) {
				t.Fatalf("Build() error = %v, want substring %q", err, testCase.wantSubstring)
			}
		})
	}
}

func TestModuleMaterializationRefusesLifecycleRecordCapPlusOne(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	manifest := &materializeTestManifest{
		identity:            testManifestDigest,
		generatedModuleRows: resolvercatalog.MaxRecordsPerMember + 1,
	}
	writes := 0
	err := emitModulesAndValidateGeneratedPaths(
		context.Background(), manifest, registry.adapters[0],
		&blobBudget{read: func(context.Context, string, string, int64) ([]byte, error) {
			return nil, errors.New("oversized module must not be read")
		}},
		&generatedPlan{
			generatedPaths: make(map[string]bool), invocationRoots: make(map[string]bool),
		},
		func(json.RawMessage) error {
			writes++
			return nil
		},
	)
	if !errors.Is(err, resolvercatalog.ErrLimit) {
		t.Fatalf("module cap+1 error = %v, want ErrLimit", err)
	}
	if writes != resolvercatalog.MaxRecordsPerMember {
		t.Fatalf("module writes = %d, want lifecycle cap %d",
			writes, resolvercatalog.MaxRecordsPerMember)
	}
}

func TestDeclarationMaterializationRefusesCapPlusOne(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	declaration := materializeTestDeclaration(ProtocolGRPC)
	rows := make([]store.Assertion, MaxDeclarationRecords+1)
	for index := range rows {
		rows[index] = materializeTestAssertion(
			declaration.RunID,
			fmt.Sprintf("idl/%05d.proto", index),
			fmt.Sprintf("lineage-%05d", index),
			index,
		)
	}
	reader := &materializeAssertionReader{rows: map[string][]store.Assertion{
		declaration.RunID: rows,
	}}
	identity := newMaterializeTestIdentity(
		t, registry, testManifestDigest, []DeclarationInput{declaration},
	)
	writes := 0
	_, err := emitDeclarationTargets(
		context.Background(),
		BuildRequest{Identity: identity, Assertions: reader},
		registry.adapters[0], &declaration, &materializationBudget{},
		func(json.RawMessage) error {
			writes++
			return nil
		},
	)
	if !errors.Is(err, resolvercatalog.ErrLimit) {
		t.Fatalf("declaration cap+1 error = %v, want ErrLimit", err)
	}
	if writes != MaxDeclarationRecords {
		t.Fatalf("declaration writes = %d, want cap %d", writes, MaxDeclarationRecords)
	}
	if len(reader.calls) < 2 {
		t.Fatalf("declaration reader calls = %d, want bounded pagination", len(reader.calls))
	}
}

func TestDeclarationMaterializationAggregateCapSpansAdapters(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift)
	grpc := materializeTestDeclaration(ProtocolGRPC)
	thrift := materializeTestDeclaration(ProtocolThrift)
	grpcCount := MaxDeclarationRecords / 2
	thriftCount := MaxDeclarationRecords - grpcCount
	for _, testCase := range []struct {
		name      string
		extra     int
		wantLimit bool
	}{
		{name: "cap"},
		{name: "cap-plus-one", extra: 1, wantLimit: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rows := map[string][]store.Assertion{
				grpc.RunID:   make([]store.Assertion, grpcCount),
				thrift.RunID: make([]store.Assertion, thriftCount+testCase.extra),
			}
			for index := range rows[grpc.RunID] {
				rows[grpc.RunID][index] = materializeTestAssertion(
					grpc.RunID, fmt.Sprintf("idl/proto/%05d.proto", index),
					fmt.Sprintf("grpc-lineage-%05d", index), index,
				)
			}
			for index := range rows[thrift.RunID] {
				rows[thrift.RunID][index] = materializeTestAssertion(
					thrift.RunID, fmt.Sprintf("idl/thrift/%05d.thrift", index),
					fmt.Sprintf("thrift-lineage-%05d", index), index,
				)
			}
			request, _ := resolvedMaterializeRequest(t, t.TempDir(), registry)
			request.Assertions = &materializeAssertionReader{rows: rows}
			prepared, err := Build(t.Context(), request)
			if prepared != nil {
				_ = prepared.Discard()
			}
			if testCase.wantLimit {
				if !errors.Is(err, resolvercatalog.ErrLimit) {
					t.Fatalf("dual-adapter cap+1 error = %v, want ErrLimit", err)
				}
			} else if err != nil {
				t.Fatalf("dual-adapter cap error = %v", err)
			}
		})
	}
}

func TestDeclarationMaterializationPathBytesBoundary(t *testing.T) {
	registry := newMaterializeTestRegistry(t, ProtocolGRPC)
	declaration := materializeTestDeclaration(ProtocolGRPC)
	rows := make([]store.Assertion, MaxDeclarationPathBytes/repopath.MaxBytes)
	for index := range rows {
		prefix := fmt.Sprintf("idl/%04x/", index)
		filePath := prefix + strings.Repeat(
			"a", repopath.MaxBytes-len(prefix)-len(".proto"),
		) + ".proto"
		if len(filePath) != repopath.MaxBytes {
			t.Fatalf("fixture path bytes = %d, want %d", len(filePath), repopath.MaxBytes)
		}
		rows[index] = materializeTestAssertion(
			declaration.RunID, filePath, fmt.Sprintf("lineage-%04x", index), index,
		)
	}
	identity := newMaterializeTestIdentity(
		t, registry, testManifestDigest, []DeclarationInput{declaration},
	)
	for _, testCase := range []struct {
		name      string
		extraPath bool
		wantLimit bool
	}{
		{name: "cap"},
		{name: "cap-plus-one", extraPath: true, wantLimit: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			currentRows := slices.Clone(rows)
			if testCase.extraPath {
				currentRows = append(currentRows, materializeTestAssertion(
					declaration.RunID, "z", "lineage-extra", len(currentRows),
				))
			}
			reader := &materializeAssertionReader{rows: map[string][]store.Assertion{
				declaration.RunID: currentRows,
			}}
			writes := 0
			_, err := emitDeclarationTargets(
				t.Context(),
				BuildRequest{Identity: identity, Assertions: reader},
				registry.adapters[0], &declaration, &materializationBudget{},
				func(json.RawMessage) error { writes++; return nil },
			)
			if testCase.wantLimit {
				if !errors.Is(err, resolvercatalog.ErrLimit) {
					t.Fatalf("path-byte cap+1 error = %v, want ErrLimit", err)
				}
			} else if err != nil {
				t.Fatalf("path-byte cap error = %v", err)
			}
			if writes != len(rows) {
				t.Fatalf("path-byte writes = %d, want %d", writes, len(rows))
			}
		})
	}
}

func newMaterializeTestRegistry(t *testing.T, protocols ...Protocol) *Registry {
	t.Helper()
	var extractors []extract.Extractor
	for _, protocol := range protocols {
		switch protocol {
		case ProtocolGRPC:
			extractors = append(extractors,
				materializeTestExtractor{domain: "grpc-caller", version: "1.5.0"},
				materializeTestExtractor{domain: "proto-contract", version: "3.0.0"},
			)
		case ProtocolThrift:
			extractors = append(extractors,
				materializeTestExtractor{domain: "thrift-caller", version: "1.5.0"},
				materializeTestExtractor{domain: "thrift-contract", version: "1.0.0"},
			)
		default:
			t.Fatalf("unsupported test protocol %q", protocol)
		}
	}
	registry, err := NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func newMaterializeBuildRequest(
	t *testing.T,
	root string,
	registry *Registry,
	manifest *materializeTestManifest,
	blobs *materializeBlobFixture,
	declarations []DeclarationInput,
	assertions *materializeAssertionReader,
) BuildRequest {
	t.Helper()
	if manifest.identity == "" {
		manifest.identity = testManifestDigest
	}
	return BuildRequest{
		Root: root, RepositoryDir: "/neutral/resolver-fixture.git",
		Identity: newMaterializeTestIdentity(
			t, registry, manifest.identity, declarations,
		),
		Registry: registry, Manifest: manifest, Declarations: declarations,
		Assertions: assertions, ReadBlob: blobs.read,
	}
}

func newMaterializeTestIdentity(
	t *testing.T,
	registry *Registry,
	manifestDigest string,
	declarations []DeclarationInput,
) resolvercatalog.Identity {
	t.Helper()
	publications := make(
		[]resolvercatalog.DeclarationPublication, 0, len(declarations),
	)
	for _, declaration := range declarations {
		publications = append(publications, resolvercatalog.DeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
		})
	}
	sort.Slice(publications, func(i, j int) bool {
		return publications[i].Domain < publications[j].Domain
	})
	if publications == nil {
		publications = []resolvercatalog.DeclarationPublication{}
	}
	identity, err := resolvercatalog.NewIdentity(
		testRepository, testCommit, "", manifestDigest, publications, registry.Packs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func materializeTestDeclaration(protocol Protocol) DeclarationInput {
	suffix := "a"
	domain := "proto-contract"
	if protocol == ProtocolThrift {
		suffix, domain = "b", "thrift-contract"
	}
	return DeclarationInput{
		Protocol: protocol, Domain: domain, RunID: "run-" + string(protocol),
		GenerationDigest: "extraction_generation_v1_" + strings.Repeat(suffix, 64),
	}
}

func materializeTestAssertion(
	runID, filePath, lineage string,
	index int,
) store.Assertion {
	return store.Assertion{
		ID:        fmt.Sprintf("assertion-%08d", index),
		Predicate: "DECLARES_OPERATION", Subject: filePath,
		Object: "Service.Operation", Lineage: lineage, Tier: "exact",
		Repo: testRepository, RunID: runID,
	}
}

func resolvedMaterializeRequest(
	t *testing.T,
	root string,
	registry *Registry,
) (BuildRequest, *materializeBlobFixture) {
	t.Helper()
	blobs := newMaterializeBlobFixture()
	layout := `{"version":"t20-layout-snapshot-v1","roots":[` +
		`{"kind":"idl","path":"idl/proto","protocol":"grpc"},` +
		`{"kind":"idl","path":"idl/thrift","protocol":"thrift"},` +
		`{"kind":"generated","path":"gen/grpc","protocol":"grpc"},` +
		`{"kind":"generated","path":"gen/thrift","protocol":"thrift"}]}`
	generated := `{"version":"t20-generated-from-v1","mappings":[` +
		`{"protocol":"grpc","generated_path":"gen/grpc/orders_grpc.pb.go",` +
		`"generator_relative_path":"orders.proto","declaration_path":"idl/proto/orders.proto"},` +
		`{"protocol":"thrift","generated_path":"gen/thrift/ledger.go",` +
		`"declaration_path":"idl/thrift/ledger.thrift"}]}`
	files := []extract.CandidateManifestFile{
		blobs.add("go.mod", "module \"example.invalid/root\"\n"),
		blobs.add("src/main.go", "package main\n"),
		blobs.add("unit-snapshot.json", `{"version":"t20-unit-snapshot-v1","mappings":[]}`),
		blobs.add("services/payments/go.mod", "module example.invalid/payments\n"),
		blobs.add("gen/grpc/orders_grpc.pb.go", "package gen\n"),
		blobs.add("gen/thrift/ledger.go", "package gen\n"),
		blobs.add(resolverinput.LayoutSnapshotPath, layout),
		blobs.add(resolverinput.GeneratedFromSnapshotPath, generated),
		blobs.add("idl/proto/orders.proto", "syntax = \"proto3\";\n"),
		blobs.add("idl/thrift/ledger.thrift", "service Ledger {}\n"),
	}
	grpc := materializeTestDeclaration(ProtocolGRPC)
	thrift := materializeTestDeclaration(ProtocolThrift)
	assertions := &materializeAssertionReader{rows: map[string][]store.Assertion{
		grpc.RunID: {
			materializeTestAssertion(grpc.RunID, "idl/proto/orders.proto", "lineage-grpc", 1),
		},
		thrift.RunID: {
			materializeTestAssertion(thrift.RunID, "idl/thrift/ledger.thrift", "lineage-thrift", 2),
		},
	}}
	manifest := &materializeTestManifest{identity: testManifestDigest, files: files}
	return newMaterializeBuildRequest(
		t, root, registry, manifest, blobs,
		[]DeclarationInput{grpc, thrift}, assertions,
	), blobs
}

func buildMaterializedImage(t *testing.T, request BuildRequest) materializedImage {
	t.Helper()
	prepared, err := Build(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := prepared.Publish(
		context.Background(),
		func(context.Context, resolvercatalog.State) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := resolvercatalog.Open(context.Background(), request.Root, state)
	if err != nil {
		t.Fatal(err)
	}
	manifest := publication.Manifest()
	manifestBytes, err := os.ReadFile(filepath.Join(request.Root, state.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	image := materializedImage{
		manifest: manifestBytes, members: make(map[string][]byte, len(manifest.Members)),
	}
	for _, member := range manifest.Members {
		memberBytes, err := os.ReadFile(filepath.Join(
			request.Root,
			resolvercatalogid.ArtifactPrefix(
				manifest.Identity.Repository, manifest.Identity.GenerationDigest,
			)+member.Name,
		))
		if err != nil {
			t.Fatal(err)
		}
		image.members[member.Name] = memberBytes
	}
	return image
}

func decodeMember[T any](t *testing.T, content []byte) []T {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	values := make([]T, len(lines))
	for index, line := range lines {
		if err := json.Unmarshal([]byte(line), &values[index]); err != nil {
			t.Fatalf("decode record %d: %v", index, err)
		}
	}
	return values
}

func findGeneratedRecord(
	t *testing.T,
	records []generatedRecord,
	kind, generatedPath string,
) generatedRecord {
	t.Helper()
	for _, record := range records {
		if record.Kind == kind &&
			(generatedPath == "" || record.GeneratedPath == generatedPath) {
			return record
		}
	}
	t.Fatalf("generated record kind=%q path=%q not found in %+v", kind, generatedPath, records)
	return generatedRecord{}
}
