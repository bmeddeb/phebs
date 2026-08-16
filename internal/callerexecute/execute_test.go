package callerexecute

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
)

type executeTestPlan struct {
	digest  string
	domains []extract.CandidateManifestDomain
	leaf    extract.CandidateCallerLeaf
	files   []extract.CandidateManifestFile
}

func (plan *executeTestPlan) Identity() string            { return plan.digest }
func (*executeTestPlan) CandidateControlRevision() uint64 { return 1 }
func (plan *executeTestPlan) CallerDomains() []extract.CandidateManifestDomain {
	return slices.Clone(plan.domains)
}
func (plan *executeTestPlan) CallerLeaves() []extract.CandidateCallerLeaf {
	return []extract.CandidateCallerLeaf{plan.leaf}
}
func (plan *executeTestPlan) ForEachCallerLeafFile(
	ctx context.Context,
	domain, version string,
	leaf extract.CandidateCallerLeaf,
	visit func(extract.CandidateManifestFile) error,
) error {
	if domain != plan.domains[0].Domain || version != plan.domains[0].Version ||
		leaf != plan.leaf {
		panic("unexpected pair")
	}
	for _, file := range plan.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(file); err != nil {
			return err
		}
	}
	return nil
}

func executeTestGeneration(t *testing.T) callerleaf.GenerationIdentity {
	return executeTestGenerationWithAdapter(t, callerleaf.LeafAdapterV1)
}

func executeTestGenerationWithAdapter(
	t *testing.T,
	adapter string,
) callerleaf.GenerationIdentity {
	t.Helper()
	a := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	identity, err := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository:           "github.com/acme/service",
		HeadCommit:           "0123456789012345678901234567890123456789",
		DeclarationSetDigest: a, CandidateManifestDigest: b,
		CandidatePolicyDigest: a, ResolverGenerationDigest: b,
		ResolverManifestDigest: a,
		Extractors: []callerleaf.ExtractorIdentity{{
			Domain: "grpc-caller", Version: "1.5.0",
			LeafAdapterVersion: adapter,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestExecutePairCompactsExactEmptyResolverCoverage(t *testing.T) {
	generation := executeTestGenerationWithAdapter(t, callerleaf.LeafAdapterV2)
	leaf := extract.CandidateCallerLeaf{
		Name: "caller.ndjson", Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount: 5, DeclaredBytes: 640, ContentBytes: 500,
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	files := []extract.CandidateManifestFile{
		{Path: "consumer/a.go", ObjectID: "1111111111111111111111111111111111111111", DeclaredBytes: 100, SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/b.go", ObjectID: "2222222222222222222222222222222222222222", DeclaredBytes: 100, SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/a_test.go", ObjectID: "3333333333333333333333333333333333333333", DeclaredBytes: 100, SourceLane: candidate.SourceLaneGoTest},
		{Path: "go.mod", ObjectID: "4444444444444444444444444444444444444444", DeclaredBytes: 40, SourceLane: candidate.SourceLaneBase},
		{Path: "gen/other.pb.go", ObjectID: "5555555555555555555555555555555555555555", DeclaredBytes: 100, SourceLane: candidate.SourceLaneBase},
	}
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: "grpc-caller", Version: "1.5.0"}},
		leaf:    leaf, files: files,
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc"}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "caller-leaves")
	stage, err := callerleaf.NewStage(root, generation, pairs[0].Identity)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := gocaller.NewDirectResolverWithGeneratedSources(
		t.Context(), "grpc", nil, []gocaller.GeneratedSourceIdentity{{
			Path: files[4].Path, ObjectID: files[4].ObjectID,
		}},
	)
	if err != nil || resolver.DescriptorCount() != 0 {
		t.Fatalf("empty resolver = %v, descriptors %d", err, resolver.DescriptorCount())
	}
	if err := ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0], Protocol: "grpc",
		ResolverCatalogDigest: generation.ResolverManifestDigest, Resolver: resolver,
		ReadBlob: func(context.Context, string, string, int64) ([]byte, error) {
			t.Fatal("compact empty-resolver coverage read a source blob")
			return nil, nil
		}, Stage: stage,
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	receipt := prepared.Receipt()
	if receipt.RecordCount != 1 || receipt.ResultCount != 0 ||
		receipt.AbstentionCount != 0 || receipt.CoverageRecordCount != 1 ||
		receipt.CoveredCandidateCount != 5 || receipt.ExcludedGoTestRecords != 1 ||
		receipt.SourceBlobReads != 0 || receipt.SourceBlobBytes != 0 {
		t.Fatalf("compact receipt = %+v", receipt)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var records []callerleaf.Record
	if err := publication.ScanRecords(
		t.Context(), generation, pairs[0].Identity,
		func(_ callerleaf.RecordReference, record callerleaf.Record) error {
			records = append(records, record)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Coverage == nil ||
		records[0].Coverage.NoDirectCandidateCount != 2 ||
		!slices.Equal(records[0].Coverage.Gaps, []callerleaf.CoverageGap{
			{Reason: callerleaf.CoverageGapCatalogOwnedInput, Count: 1},
			{Reason: callerleaf.CoverageGapExcludedGoTest, Count: 1},
			{Reason: callerleaf.CoverageGapResolverGeneratedInput, Count: 1},
		}) {
		t.Fatalf("compact records = %+v", records)
	}
}

func TestExecutePairCompactsDescriptorPresentZeroFactCoverage(t *testing.T) {
	generation := executeTestGenerationWithAdapter(t, callerleaf.LeafAdapterV3)
	valid := []byte("package consumer\nfunc noop() {}\n")
	invalid := []byte{0xff, 0xfe}
	files := []extract.CandidateManifestFile{
		{Path: "consumer/noop.go", ObjectID: "1111111111111111111111111111111111111111", DeclaredBytes: int64(len(valid)), SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/invalid.go", ObjectID: "2222222222222222222222222222222222222222", DeclaredBytes: int64(len(invalid)), SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/large.go", ObjectID: "3333333333333333333333333333333333333333", DeclaredBytes: callerleaf.MaxDirectSourceBytes + 1, SourceLane: candidate.SourceLaneBase},
		{Path: "go.mod", ObjectID: "4444444444444444444444444444444444444444", DeclaredBytes: 20, SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/noop_test.go", ObjectID: "5555555555555555555555555555555555555555", DeclaredBytes: 10, SourceLane: candidate.SourceLaneGoTest},
		{Path: "gen/orders_grpc.pb.go", ObjectID: "6666666666666666666666666666666666666666", DeclaredBytes: 100, SourceLane: candidate.SourceLaneBase},
	}
	declaredBytes := int64(0)
	for _, file := range files {
		declaredBytes += file.DeclaredBytes
	}
	leaf := extract.CandidateCallerLeaf{
		Name: "caller.ndjson", Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount: 7, DeclaredBytes: declaredBytes, ContentBytes: 500,
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: "grpc-caller", Version: "1.5.0"}},
		leaf:    leaf, files: files,
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc"}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := gocaller.NewDirectResolver(t.Context(), "grpc", []gocaller.DirectDescriptor{{
		State: "resolved", Protocol: "grpc", ImportPath: "example.com/gen/orders",
		Package: "orders", ClientType: "OrdersClient", Method: "Get",
		Operation: "/orders.Orders/Get", Constructors: []string{"NewOrdersClient"},
		GeneratedPath: files[5].Path, GeneratedObjectID: files[5].ObjectID,
		GeneratedDigest:       "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		GeneratorRelativePath: "orders.proto", DeclarationPath: "idl/orders.proto",
		DeclarationLineage: "lineage",
	}})
	if err != nil || resolver.DescriptorCount() != 1 {
		t.Fatalf("resolver = %v, descriptors %d", err, resolver.DescriptorCount())
	}
	stage, err := callerleaf.NewStage(
		filepath.Join(t.TempDir(), "caller-leaves"), generation, pairs[0].Identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{files[0].ObjectID: valid, files[1].ObjectID: invalid}
	if err := ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0], Protocol: "grpc",
		ResolverCatalogDigest: generation.ResolverManifestDigest, Resolver: resolver,
		ReadBlob: func(_ context.Context, _ string, oid string, _ int64) ([]byte, error) {
			return slices.Clone(contents[oid]), nil
		}, Stage: stage,
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	receipt := prepared.Receipt()
	if receipt.RecordCount != 1 || receipt.ResultCount != 0 || receipt.AbstentionCount != 0 ||
		receipt.CoverageRecordCount != 1 || receipt.CoveredCandidateCount != 7 ||
		receipt.ExcludedGoTestRecords != 1 || receipt.SourceBlobReads != 2 ||
		receipt.SourceBlobBytes != int64(len(valid)+len(invalid)) {
		t.Fatalf("zero-fact receipt = %+v", receipt)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var record callerleaf.Record
	if err := publication.ScanRecords(
		t.Context(), generation, pairs[0].Identity,
		func(_ callerleaf.RecordReference, current callerleaf.Record) error {
			record = current
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if record.Schema != callerleaf.CoverageRecordSchemaV2 || record.Coverage == nil ||
		record.Coverage.Schema != callerleaf.CoverageSchemaV2 ||
		record.Coverage.Reason != callerleaf.CoverageReasonZeroCallerFacts ||
		record.Coverage.NoDirectCandidateCount != 1 ||
		!slices.Equal(record.Coverage.Gaps, []callerleaf.CoverageGap{
			{Reason: callerleaf.CoverageGapCatalogOwnedInput, Count: 1},
			{Reason: callerleaf.CoverageGapDomainUnselected, Count: 1},
			{Reason: callerleaf.CoverageGapExcludedGoTest, Count: 1},
			{Reason: callerleaf.CoverageGapInvalidUTF8, Count: 1},
			{Reason: callerleaf.CoverageGapResolverGeneratedInput, Count: 1},
			{Reason: callerleaf.CoverageGapSourceTooLarge, Count: 1},
		}) {
		t.Fatalf("zero-fact coverage = %+v", record)
	}
}

func TestExecutePairCompactsMaximumDescriptorPresentZeroFactShape(t *testing.T) {
	generation := executeTestGenerationWithAdapter(t, callerleaf.LeafAdapterV3)
	content := []byte("package neutral\nfunc noop() {}\n")
	files := make([]extract.CandidateManifestFile, candidate.MaxRecordsPerArtifact)
	for index := range files {
		files[index] = extract.CandidateManifestFile{
			Path:     fmt.Sprintf("neutral/f%04d.go", index),
			ObjectID: fmt.Sprintf("%040x", index+1), DeclaredBytes: int64(len(content)),
			SourceLane: candidate.SourceLaneBase,
		}
	}
	leaf := extract.CandidateCallerLeaf{
		Name: "caller.ndjson", Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount: len(files), DeclaredBytes: int64(len(files) * len(content)),
		ContentBytes:  1 << 20,
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: "grpc-caller", Version: "1.5.0"}},
		leaf:    leaf, files: files,
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc"}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := gocaller.NewDirectResolver(t.Context(), "grpc", []gocaller.DirectDescriptor{{
		State: "resolved", Protocol: "grpc", ImportPath: "example.com/gen/orders",
		Package: "orders", ClientType: "OrdersClient", Method: "Get",
		Operation: "/orders.Orders/Get", Constructors: []string{"NewOrdersClient"},
		GeneratedPath:         "gen/orders_grpc.pb.go",
		GeneratedObjectID:     "ffffffffffffffffffffffffffffffffffffffff",
		GeneratedDigest:       "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		GeneratorRelativePath: "orders.proto", DeclarationPath: "idl/orders.proto",
		DeclarationLineage: "lineage",
	}})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(
		filepath.Join(t.TempDir(), "caller-leaves"), generation, pairs[0].Identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0], Protocol: "grpc",
		ResolverCatalogDigest: generation.ResolverManifestDigest, Resolver: resolver,
		ReadBlob: func(_ context.Context, _ string, _ string, limit int64) ([]byte, error) {
			if limit != int64(len(content)) {
				t.Fatalf("read limit = %d", limit)
			}
			return slices.Clone(content), nil
		}, Stage: stage,
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	receipt := prepared.Receipt()
	if receipt.RecordCount != 1 || receipt.CoverageRecordCount != 1 ||
		receipt.CoveredCandidateCount != candidate.MaxRecordsPerArtifact ||
		receipt.ResultCount != 0 || receipt.AbstentionCount != 0 ||
		receipt.SourceBlobReads != candidate.MaxRecordsPerArtifact ||
		receipt.SourceBlobBytes != leaf.DeclaredBytes ||
		receipt.ContentBytes*int64(callerleaf.MaxExpectedPairs) > callerleaf.MaxAggregateCanonicalBytes {
		t.Fatalf("maximum zero-fact receipt = %+v", receipt)
	}
}

func TestExecutePairResultKeepsFullDescriptorPresentArtifact(t *testing.T) {
	generation := executeTestGenerationWithAdapter(t, callerleaf.LeafAdapterV3)
	leaf := extract.CandidateCallerLeaf{
		Name: "caller.ndjson", Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount: 5, DeclaredBytes: 640, ContentBytes: 500,
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	resolved := "package consumer\nimport pb \"example.com/gen/orders\"\nfunc call(c pb.OrderServiceClient) { c.GetOrder(nil, nil) }\n"
	unrelated := "package consumer\nimport other \"example.com/other\"\nfunc call(c other.Client) { c.GetOrder(nil, nil) }\n"
	files := []extract.CandidateManifestFile{
		{Path: "consumer/call.go", ObjectID: "1111111111111111111111111111111111111111", DeclaredBytes: int64(len(resolved)), SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/unrelated.go", ObjectID: "2222222222222222222222222222222222222222", DeclaredBytes: int64(len(unrelated)), SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/call_test.go", ObjectID: "3333333333333333333333333333333333333333", DeclaredBytes: 10, SourceLane: candidate.SourceLaneGoTest},
		{Path: "go.mod", ObjectID: "4444444444444444444444444444444444444444", DeclaredBytes: 20, SourceLane: candidate.SourceLaneBase},
		{Path: "gen/orders_grpc.pb.go", ObjectID: "5555555555555555555555555555555555555555", DeclaredBytes: 100, SourceLane: candidate.SourceLaneBase},
	}
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: "grpc-caller", Version: "1.5.0"}},
		leaf:    leaf, files: files,
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc"}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(
		filepath.Join(t.TempDir(), "caller-leaves"), generation, pairs[0].Identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	reads := []string{}
	content := map[string][]byte{
		files[0].ObjectID: []byte(resolved), files[1].ObjectID: []byte(unrelated),
	}
	resolver, err := gocaller.NewDirectResolver(t.Context(), "grpc", []gocaller.DirectDescriptor{{
		State: "resolved", Protocol: "grpc",
		ImportPath: "example.com/gen/orders", Package: "orders",
		ClientType: "OrderServiceClient", Method: "GetOrder",
		Operation:             "/demo.OrderService/GetOrder",
		Constructors:          []string{"NewOrderServiceClient"},
		GeneratedPath:         "gen/orders_grpc.pb.go",
		GeneratedObjectID:     "5555555555555555555555555555555555555555",
		GeneratedDigest:       "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		GeneratorRelativePath: "orders.proto",
		DeclarationPath:       "idl/orders.proto", DeclarationLineage: "lineage",
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0],
		Protocol: "grpc", ResolverCatalogDigest: generation.ResolverManifestDigest,
		Resolver: resolver,
		ReadBlob: func(_ context.Context, _ string, oid string, limit int64) ([]byte, error) {
			if limit != int64(len(content[oid])) {
				t.Fatalf("source read cap = %d, want declared %d", limit, len(content[oid]))
			}
			reads = append(reads, oid)
			return slices.Clone(content[oid]), nil
		},
		Stage: stage,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	receipt := prepared.Receipt()
	if !slices.Equal(reads, []string{files[0].ObjectID, files[1].ObjectID}) {
		t.Fatalf("source reads = %v", reads)
	}
	if receipt.ResultCount != 1 || receipt.AbstentionCount != 3 ||
		receipt.CoverageRecordCount != 0 ||
		receipt.ExcludedGoTestRecords != 1 || receipt.SourceBlobReads != 2 ||
		receipt.OutOfLeafReads != 0 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestExecutePairUnresolvedFactKeepsFullDescriptorPresentArtifact(t *testing.T) {
	generation := executeTestGenerationWithAdapter(t, callerleaf.LeafAdapterV3)
	unresolved := []byte(`package consumer
import api "example.com/repo/gen"
func invoke() { client := api.NewGreeterClient(); client.SayHello() }
`)
	unrelated := []byte("package consumer\nfunc noop() {}\n")
	files := []extract.CandidateManifestFile{
		{Path: "consumer/call.go", ObjectID: "1111111111111111111111111111111111111111", DeclaredBytes: int64(len(unresolved)), SourceLane: candidate.SourceLaneBase},
		{Path: "consumer/noop.go", ObjectID: "2222222222222222222222222222222222222222", DeclaredBytes: int64(len(unrelated)), SourceLane: candidate.SourceLaneBase},
	}
	leaf := extract.CandidateCallerLeaf{
		Name: "caller.ndjson", Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount: 2, DeclaredBytes: int64(len(unresolved) + len(unrelated)), ContentBytes: 500,
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: "grpc-caller", Version: "1.5.0"}},
		leaf:    leaf, files: files,
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc"}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := gocaller.NewDirectResolver(t.Context(), "grpc", []gocaller.DirectDescriptor{{
		State: "ambiguous", Reason: "multiple_declaration_paths", Protocol: "grpc",
		ImportPath: "example.com/repo/gen", Package: "api", ClientType: "GreeterClient",
		Method: "SayHello", Operation: "/demo.Greeter/SayHello",
		Constructors: []string{"NewGreeterClient"}, GeneratedPath: "gen/api_grpc.pb.go",
		GeneratedObjectID:     "3333333333333333333333333333333333333333",
		GeneratedDigest:       "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		GeneratorRelativePath: "api.proto",
	}})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(
		filepath.Join(t.TempDir(), "caller-leaves"), generation, pairs[0].Identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{files[0].ObjectID: unresolved, files[1].ObjectID: unrelated}
	if err := ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0], Protocol: "grpc",
		ResolverCatalogDigest: generation.ResolverManifestDigest, Resolver: resolver,
		ReadBlob: func(_ context.Context, _ string, oid string, _ int64) ([]byte, error) {
			return slices.Clone(contents[oid]), nil
		}, Stage: stage,
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	receipt := prepared.Receipt()
	if receipt.ResultCount != 0 || receipt.AbstentionCount != 2 ||
		receipt.CoverageRecordCount != 0 || receipt.SourceBlobReads != 2 {
		t.Fatalf("unresolved receipt = %+v", receipt)
	}
	publication, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unresolvedFacts, inputAbstentions := 0, 0
	if err := publication.ScanRecords(
		t.Context(), generation, pairs[0].Identity,
		func(_ callerleaf.RecordReference, record callerleaf.Record) error {
			if record.Fact != nil && record.Fact.Assertion.Predicate == "UNRESOLVED_CALLER" {
				unresolvedFacts++
			} else if record.Fact == nil && record.Reason == "no_direct_caller" {
				inputAbstentions++
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if unresolvedFacts != 1 || inputAbstentions != 1 {
		t.Fatalf("unresolved facts = %d, input abstentions = %d", unresolvedFacts, inputAbstentions)
	}
}

func TestExecuteExplicitEmptyDomainLeaf(t *testing.T) {
	generation := executeTestGeneration(t)
	leaf := extract.CandidateCallerLeaf{
		Name: "caller.ndjson", Ordinal: 0, Prefix: "00", PrefixBits: 2,
		RecordCount: 1, DeclaredBytes: 1, ContentBytes: 100,
		ContentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	plan := &executeTestPlan{
		digest:  generation.CandidateManifestDigest,
		domains: []extract.CandidateManifestDomain{{Domain: "grpc-caller", Version: "1.5.0"}},
		leaf:    leaf,
	}
	pairs, _, err := ExpectedPairs(plan, generation, &Registry{
		adapters:         []Adapter{{Domain: "grpc-caller", Version: "1.5.0", Protocol: "grpc"}},
		candidateDomains: plan.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(
		filepath.Join(t.TempDir(), "caller-leaves"), generation, pairs[0].Identity,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := gocaller.NewDirectResolver(t.Context(), "grpc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExecutePair(t.Context(), ExecuteRequest{
		RepositoryDir: "/unused", Plan: plan, Pair: pairs[0], Protocol: "grpc",
		ResolverCatalogDigest: generation.ResolverManifestDigest,
		Resolver:              resolver,
		ReadBlob: func(context.Context, string, string, int64) ([]byte, error) {
			t.Fatal("empty pair read a source blob")
			return nil, nil
		}, Stage: stage,
	}); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if receipt := prepared.Receipt(); receipt.RecordCount != 0 || receipt.ContentBytes != 0 {
		t.Fatalf("empty receipt = %+v", receipt)
	}
}
