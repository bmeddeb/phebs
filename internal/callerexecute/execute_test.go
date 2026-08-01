package callerexecute

import (
	"context"
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
			LeafAdapterVersion: callerleaf.LeafAdapterV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestExecutePairReadsOnlySelectedBaseGoBlobs(t *testing.T) {
	generation := executeTestGeneration(t)
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
		receipt.ExcludedGoTestRecords != 1 || receipt.SourceBlobReads != 2 ||
		receipt.OutOfLeafReads != 0 {
		t.Fatalf("receipt = %+v", receipt)
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
