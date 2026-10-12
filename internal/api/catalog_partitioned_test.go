package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	atlasRepo      = "github.com/acme/contracts"
	atlasLineage   = "contract_lineage_demo"
	atlasOperation = "demo.Catalog/Get"
	atlasNativeRun = "native-proto-run"
)

// partitionedAtlasStore serves one partitioned proto-contract root behind a
// fence the test controls, and counts which read path each request used.
type partitionedAtlasStore struct {
	*proofAPIStore
	root                     store.PartitionedExtractionDomainReference
	manifest                 store.CandidateManifestPublication
	current                  bool
	onReference              func()
	nativePages, nativeReads int
}

func (s *partitionedAtlasStore) authority() store.PartitionedAssertionAuthority {
	return store.PartitionedAssertionAuthority{
		Repository: atlasRepo, Domain: catalogProtoDomain, RunID: s.root.RunID,
		PlanDigest: s.root.PlanDigest, RootDigest: s.root.RootDigest, Commit: catalogCommit,
		CandidateManifestDigest: s.manifest.ManifestDigest, CandidatePolicyDigest: s.manifest.PolicyDigest,
	}
}

func (s *partitionedAtlasStore) GetPartitionedExtractionDomainReference(_ context.Context, repository, domain string) (store.PartitionedExtractionDomainReference, error) {
	if s.onReference != nil {
		s.onReference()
	}
	if repository != atlasRepo || domain != catalogProtoDomain || s.root.RunID == "" {
		return store.PartitionedExtractionDomainReference{}, store.ErrNotFound
	}
	return s.root, nil
}

func (s *partitionedAtlasStore) GetCandidateManifestPublication(_ context.Context, repository string) (*store.CandidateManifestPublication, error) {
	if repository != atlasRepo {
		return nil, store.ErrNotFound
	}
	manifest := s.manifest
	return &manifest, nil
}

func (s *partitionedAtlasStore) ListPartitionedAssertions(ctx context.Context, query store.AssertionQuery, authority store.PartitionedAssertionAuthority) ([]store.Assertion, error) {
	s.nativePages++
	if !s.current || authority != s.authority() {
		if query.After != nil {
			return nil, store.ErrConflict
		}
		return nil, store.ErrNotFound
	}
	return s.ListAssertions(ctx, query)
}

func (s *partitionedAtlasStore) ResolvePartitionedEvidence(_ context.Context, authority store.PartitionedAssertionAuthority, atomID string) (*store.EvidenceResolution, error) {
	s.nativeReads++
	if !s.current || authority != s.authority() {
		return nil, store.ErrNotFound
	}
	resolution, ok := s.resolutions[proofEvidenceScope(authority.Repository, authority.RunID, atomID)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &resolution, nil
}

func newPartitionedAtlas(t *testing.T) (*partitionedAtlasStore, *api.ContractCatalogService) {
	t.Helper()
	base := &proofAPIStore{
		repos:       []store.Repo{{Name: atlasRepo, IndexedCommitHash: catalogCommit}},
		runs:        map[string]store.ExtractionRun{},
		assertions:  map[string][]store.Assertion{},
		resolutions: map[string]store.EvidenceResolution{},
		bundles:     map[string]store.ProofBundleRecord{},
	}
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	st := &partitionedAtlasStore{
		proofAPIStore: base, current: true,
		root: store.PartitionedExtractionDomainReference{
			Schema: store.PartitionedExtractionDomainSchema, Repository: atlasRepo, Domain: catalogProtoDomain,
			RunID: atlasNativeRun, PlanDigest: digest("1"), RootDigest: digest("2"), CandidateDigest: digest("3"),
			SourceDigest: digest("4"), ObservationDigest: digest("5"),
		},
		manifest: store.CandidateManifestPublication{
			Repository: atlasRepo, HeadCommit: catalogCommit, ManifestDigest: digest("3"), PolicyDigest: digest("6"),
		},
	}
	service, serviceResolution := catalogAssertion(atlasRepo, atlasNativeRun, "service", "DECLARES_SERVICE",
		"proto/catalog.proto", "demo.Catalog", atlasLineage, `{"schema":"proto-service-detail-v1","name":"Catalog"}`)
	putCatalogAssertion(base, service, serviceResolution)
	operation, operationResolution := catalogAssertion(atlasRepo, atlasNativeRun, "operation", "DECLARES_OPERATION",
		"proto/catalog.proto", atlasOperation, atlasLineage,
		`{"schema":"proto-operation-detail-v1","request":{"raw":"Request","resolution":"unresolved"},"response":{"raw":"Response","resolution":"unresolved"},"client_streaming":false,"server_streaming":false}`)
	putCatalogAssertion(base, operation, operationResolution)
	options := catalogOptions(base, "user:member", nil)
	options.Store, options.Evidence, options.ProofBundles = st, st, st
	return st, api.NewContractCatalogService(options)
}

func TestContractCatalogServesTheCurrentPartitionedDeclarationRoot(t *testing.T) {
	st, service := newPartitionedAtlas(t)
	list, err := service.List(context.Background(), api.ContractCatalogQuery{}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || len(list.ExactAuthorities) != 1 || st.nativePages == 0 {
		t.Fatalf("items=%d authorities=%+v nativePages=%d", len(list.Items), list.ExactAuthorities, st.nativePages)
	}
	authority := list.ExactAuthorities[0]
	if authority.RunID != atlasNativeRun || authority.RootDigest != st.root.RootDigest || authority.Commit != catalogCommit {
		t.Fatalf("authority = %+v", authority)
	}
	for _, run := range list.Coverage.Repositories[0].Runs {
		if run.Domain == catalogProtoDomain && run.Status == "published" {
			t.Fatal("the legacy certificate must not claim the partitioned run is published")
		}
	}
	detail, err := service.Operation(context.Background(), atlasRepo, atlasLineage, "/"+atlasOperation)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ExactAuthority == nil || detail.ExactAuthority.RootDigest != st.root.RootDigest || st.nativeReads == 0 {
		t.Fatalf("detail authority = %+v nativeReads=%d", detail.ExactAuthority, st.nativeReads)
	}
	for _, call := range st.calls {
		if strings.HasPrefix(call, "resolve:") {
			t.Fatalf("a partitioned locator used the legacy resolver: %s", call)
		}
	}
}

func TestContractCatalogLeavesAStaleOrForeignRootUnavailable(t *testing.T) {
	for name, mutate := range map[string]func(*partitionedAtlasStore){
		"fence not current":        func(s *partitionedAtlasStore) { s.current = false },
		"other candidate":          func(s *partitionedAtlasStore) { s.manifest.ManifestDigest = "sha256:" + strings.Repeat("9", 64) },
		"repository moved commits": func(s *partitionedAtlasStore) { s.manifest.HeadCommit = strings.Repeat("b", 40) },
		"no root":                  func(s *partitionedAtlasStore) { s.root.RunID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			st, service := newPartitionedAtlas(t)
			mutate(st)
			list, err := service.List(context.Background(), api.ContractCatalogQuery{}, 10, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Items) != 0 || len(list.ExactAuthorities) != 0 {
				t.Fatalf("stale root served: items=%d authorities=%+v", len(list.Items), list.ExactAuthorities)
			}
			_, err = service.Operation(context.Background(), atlasRepo, atlasLineage, "/"+atlasOperation)
			requireCatalogStatus(t, err, http.StatusNotFound)
		})
	}
}

func TestContractCatalogPrefersALegacyPublishedRun(t *testing.T) {
	st, service := newPartitionedAtlas(t)
	legacy := catalogRun(atlasRepo, catalogProtoDomain, "legacy-run", 1)
	st.runs[proofScope(atlasRepo, catalogProtoDomain)] = legacy
	assertion, resolution := catalogAssertion(atlasRepo, legacy.ID, "legacy-service", "DECLARES_SERVICE",
		"proto/catalog.proto", "demo.Legacy", atlasLineage, `{"schema":"proto-service-detail-v1","name":"Legacy"}`)
	putCatalogAssertion(st.proofAPIStore, assertion, resolution)
	list, err := service.List(context.Background(), api.ContractCatalogQuery{}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || len(list.ExactAuthorities) != 0 || st.nativePages != 0 {
		t.Fatalf("items=%d authorities=%d nativePages=%d", len(list.Items), len(list.ExactAuthorities), st.nativePages)
	}
}

func TestContractCatalogBindsCursorsAndRetriesToThePartitionedRoot(t *testing.T) {
	st, service := newPartitionedAtlas(t)
	first, err := service.List(context.Background(), api.ContractCatalogQuery{}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Pagination.NextCursor == "" {
		t.Fatal("expected a continuation cursor")
	}
	st.root.RootDigest = "sha256:" + strings.Repeat("7", 64)
	_, err = service.List(context.Background(), api.ContractCatalogQuery{}, 1, first.Pagination.NextCursor)
	requireCatalogStatus(t, err, http.StatusConflict)

	// A root that changes between every build and its confirmation never yields a page.
	st, service = newPartitionedAtlas(t)
	flips := 0
	st.onReference = func() {
		flips++
		st.root.RootDigest = "sha256:" + strings.Repeat(string(rune('a'+flips%6)), 64)
	}
	_, err = service.List(context.Background(), api.ContractCatalogQuery{}, 10, "")
	requireCatalogStatus(t, err, http.StatusConflict)
}
