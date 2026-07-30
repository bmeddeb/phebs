package extract

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t201"
)

const t207Layout = `{
  "version": "t20-layout-snapshot-v1",
  "roots": [
    {"kind":"idl","path":"idl/proto","protocol":"grpc"},
    {"kind":"idl","path":"idl/thrift","protocol":"thrift"},
    {"kind":"generated","path":"gen/proto","protocol":"grpc"},
    {"kind":"generated","path":"gen/thrift","protocol":"thrift"},
    {"kind":"source","path":"src"}
  ]
}`

const t207Units = `{
  "version": "t20-unit-snapshot-v1",
  "mappings": [
    {"source_path":"src/checkout/call.go","build_targets":["//src/checkout:lib"],"deployables":["checkout-api"],"logical_services":["checkout"],"owners":["team-checkout"]},
    {"source_path":"src/shared/call.go","start_line":7,"build_targets":["//src/shared:a"],"logical_services":["shared-a"]},
    {"source_path":"src/shared/call.go","start_line":7,"build_targets":["//src/shared:b"],"logical_services":["shared-b"]},
    {"source_path":"src/empty/call.go","state":"unavailable","source":"unit-snapshot-v1"}
  ]
}`

const t207Generated = `{
  "version": "t20-generated-from-v1",
  "mappings": [
    {"generated_path":"gen/proto/direct/direct_grpc.pb.go","generator_relative_path":"direct/direct.proto","declaration_path":"idl/proto/direct/direct.proto"},
    {"generated_path":"gen/thrift/ledger/ledger.go","declaration_path":"idl/thrift/ledger.thrift"},
    {"generated_path":"gen/thrift/conflict/conflict.go","declaration_path":"idl/thrift/ledger.thrift"},
    {"generated_path":"gen/thrift/conflict/conflict.go","declaration_path":"idl/thrift/other.thrift"}
  ],
  "invocations": [
    {"protocol":"grpc","generated_root":"gen/proto/invocation","generator_invocation_root":"idl/proto"}
  ]
}`

func TestT207SeparatedRootsAndTypedAttribution(t *testing.T) {
	files := t207BaseFiles()
	files[layoutSnapshotPath] = t207Layout
	files[unitSnapshotPath] = t207Units
	files[generatedFromSnapshotPath] = t207Generated
	verified, inner, source := loadAttributionTestSource(t, files)

	if !source.Available() || source.Digest() == "" {
		t.Fatalf("source availability/digest = %v / %q", source.Available(), source.Digest())
	}
	if got, want := inner.reads, attributionSnapshotPaths; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot reads = %v, want fixed paths %v", got, want)
	}
	provenance := source.Provenance()
	if len(provenance) != 3 {
		t.Fatalf("provenance = %+v", provenance)
	}
	for _, item := range provenance {
		if item.Kind != "repository_revision" ||
			item.Repository != inner.repo || item.Commit != inner.commit ||
			!strings.HasPrefix(item.Digest, "sha256:") {
			t.Fatalf("provenance item = %+v", item)
		}
	}

	for _, testCase := range []struct {
		path, kind, root, protocol string
	}{
		{"idl/proto/orders/v1/orders.proto", "idl", "idl/proto", "grpc"},
		{"gen/thrift/ledger/ledger.go", "generated", "gen/thrift", "thrift"},
		{"src/checkout/call.go", "source", "src", ""},
	} {
		got := source.ClassifyPath(testCase.path)
		if got.State != sdk.AttributionStateResolved ||
			got.Kind != testCase.kind || got.Root != testCase.root ||
			got.Protocol != testCase.protocol {
			t.Errorf("ClassifyPath(%q) = %+v", testCase.path, got)
		}
	}
	if got := source.ClassifyPath("docs/unmatched.txt"); got.State != sdk.AttributionStateUnavailable {
		t.Fatalf("unmatched classification = %+v", got)
	}

	resolved := source.ConsumerUnits("src/checkout/call.go", 12)
	if resolved.State != sdk.AttributionStateResolved || len(resolved.Candidates) != 1 ||
		!reflect.DeepEqual(resolved.Candidates[0].LogicalServices, []string{"checkout"}) {
		t.Fatalf("resolved units = %+v", resolved)
	}
	ambiguous := source.ConsumerUnits("src/shared/call.go", 7)
	if ambiguous.State != sdk.AttributionStateAmbiguous ||
		ambiguous.Reason != "multiple_unit_candidates" ||
		len(ambiguous.Candidates) != 2 {
		t.Fatalf("ambiguous units = %+v", ambiguous)
	}
	unavailable := source.ConsumerUnits("src/empty/call.go", 3)
	if unavailable.State != sdk.AttributionStateUnavailable ||
		unavailable.Reason != "mapping_has_no_attribution" ||
		len(unavailable.Candidates) != 1 {
		t.Fatalf("explicit unavailable units = %+v", unavailable)
	}
	if got := source.ConsumerUnits("src/unmapped/call.go", 1); got.State != sdk.AttributionStateUnavailable || got.Reason != "no_unit_mapping" {
		t.Fatalf("unmapped units = %+v", got)
	}

	invocation := source.GeneratedFrom(
		"grpc", "gen/proto/invocation/orders_grpc.pb.go", "orders/v1/orders.proto")
	if invocation.State != sdk.AttributionStateResolved ||
		len(invocation.Candidates) != 1 ||
		invocation.Candidates[0].DeclarationPath != "idl/proto/orders/v1/orders.proto" {
		t.Fatalf("invocation-root relation = %+v", invocation)
	}
	direct := source.GeneratedFrom(
		"grpc", "gen/proto/direct/direct_grpc.pb.go", "direct/direct.proto")
	if direct.State != sdk.AttributionStateResolved ||
		direct.Candidates[0].DeclarationPath != "idl/proto/direct/direct.proto" {
		t.Fatalf("direct protobuf relation = %+v", direct)
	}
	thrift := source.GeneratedFrom("thrift", "gen/thrift/ledger/ledger.go", "")
	if thrift.State != sdk.AttributionStateResolved ||
		thrift.Candidates[0].DeclarationPath != "idl/thrift/ledger.thrift" {
		t.Fatalf("direct Thrift relation = %+v", thrift)
	}
	conflict := source.GeneratedFrom("thrift", "gen/thrift/conflict/conflict.go", "")
	if conflict.State != sdk.AttributionStateAmbiguous ||
		conflict.Reason != "multiple_declaration_paths" ||
		len(conflict.Candidates) != 2 {
		t.Fatalf("conflicting Thrift relation = %+v", conflict)
	}

	// Results are defensive copies: an extractor cannot mutate future lookups.
	resolved.Candidates[0].Owners[0] = "mutated"
	if got := source.ConsumerUnits("src/checkout/call.go", 12); got.Candidates[0].Owners[0] != "team-checkout" {
		t.Fatalf("consumer-unit result was mutable: %+v", got)
	}
	provenance[0].Digest = "mutated"
	if source.Provenance()[0].Digest == "mutated" {
		t.Fatal("provenance result was mutable")
	}

	stats, err := verified.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.corpusFileCount != len(files) || stats.readFileCount != 3 {
		t.Fatalf("corpus stats = %+v, want all %d files inventoried and three snapshots read",
			stats, len(files))
	}
	var walked []string
	if err := verified.WalkFiles(context.Background(), func(path string) error {
		walked = append(walked, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !containsString(walked, "docs/unmatched.txt") {
		t.Fatalf("unmatched regular file disappeared from inventory: %v", walked)
	}
}

func TestT207LayoutRootsAloneNeverProveGeneratedFrom(t *testing.T) {
	files := t207BaseFiles()
	files[layoutSnapshotPath] = t207Layout
	_, _, source := loadAttributionTestSource(t, files)
	got := source.GeneratedFrom(
		"grpc", "gen/proto/invocation/orders_grpc.pb.go", "orders/v1/orders.proto")
	if got.State != sdk.AttributionStateUnavailable ||
		got.Reason != "no_snapshot_mapping" || len(got.Candidates) != 0 {
		t.Fatalf("layout root became generated-from proof: %+v", got)
	}
}

func TestT207AbsentSnapshotsAreStableUnavailable(t *testing.T) {
	_, inner, source := loadAttributionTestSource(t, t207BaseFiles())
	if source.Available() || source.Digest() != "" ||
		len(source.Provenance()) != 0 || len(inner.reads) != 0 {
		t.Fatalf("absent snapshot source = available %v digest %q provenance %+v reads %v",
			source.Available(), source.Digest(), source.Provenance(), inner.reads)
	}
	if got := source.ConsumerUnits("src/checkout/call.go", 1); got.State != sdk.AttributionStateUnavailable || got.Reason != "no_unit_mapping" {
		t.Fatalf("absent unit source = %+v", got)
	}
}

func TestAttributionUsesExactTreeLookupOutsidePlannedRows(t *testing.T) {
	f := newCorpusGitFixture(t)
	f.commitFile("src/call.go", "package src\n", "source")
	f.commitFile("gen/api.go", "package gen\n", "generated")
	f.commitFile("idl/api.proto", `syntax = "proto3";`+"\n", "declaration")
	f.commitFile(layoutSnapshotPath, `{
		"version":"t20-layout-snapshot-v1",
		"roots":[
			{"kind":"source","path":"src"},
			{"kind":"generated","path":"gen","protocol":"grpc"},
			{"kind":"idl","path":"idl","protocol":"grpc"}
		]
	}`, "layout")
	f.commitFile(unitSnapshotPath, `{
		"version":"t20-unit-snapshot-v1",
		"mappings":[
			{"source_path":"src/call.go","logical_services":["svc"]}
		]
	}`, "units")
	head := f.commitFile(generatedFromSnapshotPath, `{
		"version":"t20-generated-from-v1",
		"mappings":[],
		"invocations":[
			{"protocol":"grpc","generated_root":"gen","generator_invocation_root":"idl"}
		]
	}`, "generated-from")
	f.cloneMirror()

	inner := GitCorpus(f.dataDir).New(f.repoName, head).(*gitCorpus)
	manifest := &memoryCandidateManifest{
		identity:    "sha256:" + strings.Repeat("e", 64),
		corpusFiles: 6,
		gitlinks: CandidateManifestGitlinks{
			Digest: emptyGitlinkInventory().digest,
		},
	}
	for _, filePath := range attributionSnapshotPaths {
		entry, present, err := inner.lookupRegular(context.Background(), filePath)
		if err != nil || !present {
			t.Fatalf("lookup %q = %+v, %v, %v", filePath, entry, present, err)
		}
		manifest.records = append(manifest.records, CandidateManifestFile{
			Path: filePath, ObjectID: entry.oid, Mode: entry.mode,
			DeclaredBytes: entry.size,
			SourceLane:    candidate.SourceLaneForPath(filePath),
		})
	}
	sort.Slice(manifest.records, func(i, j int) bool {
		return manifest.records[i].Path < manifest.records[j].Path
	})
	verified := newVerifiedCorpus(inner)
	if err := verified.InventoryCandidateManifest(
		context.Background(), manifest, "grpc-caller", "test-v1",
		func(string) bool { return false },
	); err != nil {
		t.Fatal(err)
	}
	if len(verified.enumerated) != len(attributionSnapshotPaths) {
		t.Fatalf("planned rows retained complete tree: %v", verified.enumeratedOrder)
	}
	source, err := verified.AttributionSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := source.ConsumerUnits("src/call.go", 1); got.State != sdk.AttributionStateResolved {
		t.Fatalf("exact source-path validation = %+v", got)
	}
	if got := source.GeneratedFrom(
		"grpc", "gen/api.go", "api.proto",
	); got.State != sdk.AttributionStateResolved {
		t.Fatalf("exact generated/declaration validation = %+v", got)
	}
}

func TestT207MalformedSnapshotsFailClosed(t *testing.T) {
	tests := []struct {
		name, snapshotPath, snapshot, want string
	}{
		{
			name: "unknown version", snapshotPath: layoutSnapshotPath,
			snapshot: `{"version":"future","roots":[]}`,
			want:     "unsupported version",
		},
		{
			name: "unknown field", snapshotPath: unitSnapshotPath,
			snapshot: `{"version":"t20-unit-snapshot-v1","mappings":[],"catalog_url":"https://invalid"}`,
			want:     "unknown field",
		},
		{
			name: "overlapping roots", snapshotPath: layoutSnapshotPath,
			snapshot: `{"version":"t20-layout-snapshot-v1","roots":[` +
				`{"kind":"source","path":"src"},` +
				`{"kind":"source","path":"src/checkout"}]}`,
			want: "overlap",
		},
		{
			name: "nonadjacent overlapping roots", snapshotPath: layoutSnapshotPath,
			snapshot: `{"version":"t20-layout-snapshot-v1","roots":[` +
				`{"kind":"source","path":"src"},` +
				`{"kind":"source","path":"src-check"},` +
				`{"kind":"source","path":"src/checkout"}]}`,
			want: "overlap",
		},
		{
			name: "unsafe root", snapshotPath: layoutSnapshotPath,
			snapshot: `{"version":"t20-layout-snapshot-v1","roots":[` +
				`{"kind":"source","path":"../src"}]}`,
			want: "invalid corpus path",
		},
		{
			name: "stale unit path", snapshotPath: unitSnapshotPath,
			snapshot: `{"version":"t20-unit-snapshot-v1","mappings":[` +
				`{"source_path":"src/missing.go","build_targets":["//src:missing"]}]}`,
			want: "absent from the corpus",
		},
		{
			name: "lying unit state", snapshotPath: unitSnapshotPath,
			snapshot: `{"version":"t20-unit-snapshot-v1","mappings":[` +
				`{"source_path":"src/checkout/call.go","build_targets":["//src:one","//src:two"],"state":"resolved"}]}`,
			want: "does not match derived",
		},
		{
			name: "unit disagrees with layout", snapshotPath: unitSnapshotPath,
			snapshot: `{"version":"t20-unit-snapshot-v1","mappings":[` +
				`{"source_path":"docs/unmatched.txt","build_targets":["//docs:bad"]}]}`,
			want: "not classified as source",
		},
		{
			name: "duplicate generated relation", snapshotPath: generatedFromSnapshotPath,
			snapshot: `{"version":"t20-generated-from-v1","mappings":[` +
				`{"generated_path":"gen/thrift/ledger/ledger.go","declaration_path":"idl/thrift/ledger.thrift"},` +
				`{"generated_path":"gen/thrift/ledger/ledger.go","declaration_path":"idl/thrift/ledger.thrift"}]}`,
			want: "duplicates",
		},
		{
			name: "Thrift invocation root", snapshotPath: generatedFromSnapshotPath,
			snapshot: `{"version":"t20-generated-from-v1","mappings":[],"invocations":[` +
				`{"protocol":"thrift","generated_root":"gen/thrift","generator_invocation_root":"idl/thrift"}]}`,
			want: "Thrift requires direct mappings",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			files := t207BaseFiles()
			if testCase.name == "unit disagrees with layout" {
				files[layoutSnapshotPath] = t207Layout
			}
			files[testCase.snapshotPath] = testCase.snapshot
			inner := &attributionTestCorpus{
				repo: "synthetic.invalid/t207", commit: unitCommit, files: files,
			}
			verified := newVerifiedCorpus(inner)
			if err := verified.Inventory(context.Background(), func(string) bool { return false }); err != nil {
				t.Fatal(err)
			}
			if _, err := verified.AttributionSource(context.Background()); err == nil ||
				!strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("AttributionSource error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestT207MetadataChangesDigestNotSourceAssertionIdentity(t *testing.T) {
	filesA := t207BaseFiles()
	filesA[unitSnapshotPath] = `{"version":"t20-unit-snapshot-v1","mappings":[` +
		`{"source_path":"src/checkout/call.go","owners":["team-a"]}]}`
	_, _, sourceA := loadAttributionTestSource(t, filesA)
	filesB := t207BaseFiles()
	filesB[unitSnapshotPath] = `{"version":"t20-unit-snapshot-v1","mappings":[` +
		`{"source_path":"src/checkout/call.go","owners":["team-b"]}]}`
	_, _, sourceB := loadAttributionTestSource(t, filesB)
	if sourceA.Digest() == sourceB.Digest() {
		t.Fatalf("unit metadata change retained attribution digest %q", sourceA.Digest())
	}
	left := store.Assertion{
		Predicate: "CALLS_OPERATION", Subject: "src/checkout/call.go:1",
		Object: "/orders.Orders/Get", Lineage: "declaration", Repo: "synthetic.invalid/t207",
		Detail: `{"attribution_digest":"` + sourceA.Digest() + `"}`,
	}
	right := left
	right.Detail = `{"attribution_digest":"` + sourceB.Digest() + `"}`
	if store.ComputeAssertionID(left) != store.ComputeAssertionID(right) {
		t.Fatal("attribution-only change altered source assertion identity")
	}
}

func TestT207FrozenProfilesMatchUnitAndGeneratedFromOracle(t *testing.T) {
	for _, profileName := range []string{t201.SmallProfileName, t201.ScaleProfileName} {
		t.Run(profileName, func(t *testing.T) {
			profile, err := t201.GenerateProfile(profileName)
			if err != nil {
				t.Fatal(err)
			}
			files := make(map[string]string, len(profile.Files))
			for filePath, content := range profile.Files {
				files[filePath] = string(content)
			}
			_, _, source := loadAttributionTestSourceRepo(
				t, "synthetic.invalid/mono", files)
			if !source.Available() || source.Digest() == "" {
				t.Fatal("frozen attribution snapshots are unavailable")
			}
			for _, mapping := range profile.Oracle.UnitMappings {
				got := source.ConsumerUnits(mapping.SourcePath, mapping.StartLine)
				if got.State != mapping.State {
					t.Fatalf("%s units = %+v, oracle %+v", mapping.CallID, got, mapping)
				}
				if mapping.State == sdk.AttributionStateUnavailable &&
					len(got.Candidates) == 0 {
					continue
				}
				if len(got.Candidates) != 1 {
					t.Fatalf("%s candidates = %+v, want one oracle mapping", mapping.CallID, got)
				}
				candidate := got.Candidates[0]
				if !reflect.DeepEqual(candidate.BuildTargets, sortedCopy(mapping.BuildTargets)) ||
					!reflect.DeepEqual(candidate.Deployables, sortedCopy(mapping.Deployables)) ||
					!reflect.DeepEqual(candidate.LogicalServices, sortedCopy(mapping.LogicalService)) ||
					!reflect.DeepEqual(candidate.Owners, sortedCopy(mapping.Owners)) {
					t.Fatalf("%s candidate = %+v, oracle %+v", mapping.CallID, candidate, mapping)
				}
			}
			for _, relation := range profile.Oracle.GeneratedFrom {
				relativePath := ""
				if relation.Protocol == "grpc" &&
					strings.Contains(relation.GeneratedPath, "orders/v1/") {
					relativePath = "orders/v1/orders.proto"
				}
				got := source.GeneratedFrom(relation.Protocol, relation.GeneratedPath, relativePath)
				wantState := relation.State
				if wantState == "missing" {
					wantState = sdk.AttributionStateUnavailable
				}
				if wantState == "conflict" {
					wantState = sdk.AttributionStateAmbiguous
				}
				if got.State != wantState {
					t.Fatalf("%s generated-from = %+v, oracle %+v",
						relation.GeneratedPath, got, relation)
				}
				if wantState == sdk.AttributionStateResolved &&
					(len(got.Candidates) != 1 ||
						got.Candidates[0].DeclarationPath != relation.DeclarationPath ||
						got.Candidates[0].DeclarationLineage != relation.DeclarationLineage) {
					t.Fatalf("%s resolved relation = %+v, oracle %+v",
						relation.GeneratedPath, got, relation)
				}
			}
		})
	}
}

type attributionTestCorpus struct {
	repo, commit string
	files        map[string]string
	reads        []string
}

func (c *attributionTestCorpus) RepoName() string { return c.repo }
func (c *attributionTestCorpus) Commit() string   { return c.commit }
func (c *attributionTestCorpus) WalkFiles(
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
func (c *attributionTestCorpus) Read(_ context.Context, filePath string) (sdk.Blob, error) {
	content, ok := c.files[filePath]
	if !ok {
		return sdk.Blob{}, fmt.Errorf("missing %q", filePath)
	}
	c.reads = append(c.reads, filePath)
	return trustedCorpusBlob(content), nil
}

func loadAttributionTestSource(
	t *testing.T,
	files map[string]string,
) (*verifiedCorpus, *attributionTestCorpus, sdk.AttributionSource) {
	t.Helper()
	return loadAttributionTestSourceRepo(t, "synthetic.invalid/t207", files)
}

func loadAttributionTestSourceRepo(
	t *testing.T,
	repository string,
	files map[string]string,
) (*verifiedCorpus, *attributionTestCorpus, sdk.AttributionSource) {
	t.Helper()
	inner := &attributionTestCorpus{
		repo: repository, commit: unitCommit, files: files,
	}
	verified := newVerifiedCorpus(inner)
	if err := verified.Inventory(context.Background(), func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	source, err := verified.AttributionSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return verified, inner, source
}

func t207BaseFiles() map[string]string {
	return map[string]string{
		"idl/proto/orders/v1/orders.proto":       "syntax = \"proto3\";\n",
		"idl/proto/direct/direct.proto":          "syntax = \"proto3\";\n",
		"idl/thrift/ledger.thrift":               "service Ledger {}\n",
		"idl/thrift/other.thrift":                "service Other {}\n",
		"gen/proto/invocation/orders_grpc.pb.go": "package orders\n",
		"gen/proto/direct/direct_grpc.pb.go":     "package direct\n",
		"gen/thrift/ledger/ledger.go":            "package ledger\n",
		"gen/thrift/conflict/conflict.go":        "package conflict\n",
		"src/checkout/call.go":                   "package checkout\n",
		"src/shared/call.go":                     "package shared\n",
		"src/empty/call.go":                      "package empty\n",
		"src/unmapped/call.go":                   "package unmapped\n",
		"src-check/other.go":                     "package srccheck\n",
		"docs/unmatched.txt":                     "still inventoried\n",
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
