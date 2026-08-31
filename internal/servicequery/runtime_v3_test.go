package servicequery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type runtimeV3Source struct {
	scope   V3Scope
	members map[string][]byte
	revoked bool
}

func (source *runtimeV3Source) GetServiceCatalogV3CandidatePointer(
	context.Context, string,
) (store.ServiceCatalogV3Pointer, error) {
	return store.ServiceCatalogV3Pointer{
		Repository:      source.scope.CurrentRoot.Binding.Repository,
		RootDigest:      source.scope.CurrentRoot.Digest,
		ControlRevision: source.scope.CurrentControlRevision,
		PublishedAt:     time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}, nil
}

func (source *runtimeV3Source) ReadServiceCatalogV3Root(
	_ context.Context, repository, digest string,
) (servicecatalogv3.Root, error) {
	if repository != source.scope.CurrentRoot.Binding.Repository ||
		digest != source.scope.CurrentRoot.Digest {
		return servicecatalogv3.Root{}, store.ErrNotFound
	}
	if err := servicecatalogv3.ValidateRoot(source.scope.CurrentRoot); err != nil {
		return servicecatalogv3.Root{}, err
	}
	return source.scope.CurrentRoot, nil
}

func (source *runtimeV3Source) ReadServiceCatalogV3Member(
	_ context.Context, descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	raw := source.members[descriptor.Digest]
	if raw == nil {
		return nil, store.ErrNotFound
	}
	return raw, nil
}

func (source *runtimeV3Source) GetServiceStateV3SummaryPoint(
	context.Context, string,
) (servicecatalog.RepositoryState, error) {
	return source.scope.Summary, nil
}

func (source *runtimeV3Source) GetServiceStateV3Point(
	context.Context, string, string,
) (servicecatalog.ServiceState, error) {
	return source.scope.State, nil
}

func (source *runtimeV3Source) ListServiceStateV3Rows(
	context.Context, string, string, int,
) ([]servicecatalog.ServiceState, error) {
	return nil, nil
}

func (source *runtimeV3Source) ListAcceptedServiceStateV3Rows(
	context.Context, string, int,
) ([]servicecatalog.ServiceState, error) {
	return nil, nil
}

func (source *runtimeV3Source) ConfirmServiceStateV3Snapshot(
	context.Context, store.ServiceCatalogV3Pointer, servicecatalog.RepositoryState,
) error {
	if source.revoked {
		return store.ErrConflict
	}
	return nil
}

type runtimeV3RepositoryStore struct{ repo store.Repo }

func (source runtimeV3RepositoryStore) GetRepo(
	context.Context, string,
) (*store.Repo, error) {
	repo := source.repo
	return &repo, nil
}

func TestOpenRuntimeScopeV3HoldsLeaseThroughRevocation(t *testing.T) {
	commit := strings.Repeat("c", 40)
	scope := testV3Scope(t, commit, 19)
	generation, err := servicecatalogv3.Build(scope.CurrentRoot.Binding, servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: scope.CurrentRoot.Binding.Authority,
		Services:    []servicecatalog.Service{scope.DesiredProjection.Service},
		Memberships: scope.DesiredProjection.Memberships,
	})
	if err != nil || generation.Root.Digest != scope.CurrentRoot.Digest {
		t.Fatalf("rebuild runtime v3 fixture = %v", err)
	}
	members := make(map[string][]byte, len(generation.Members))
	for _, member := range generation.Members {
		for _, descriptor := range append(
			generation.Root.ServiceMembers, generation.Root.PlacementMembers...,
		) {
			if descriptor.Kind == member.Kind && descriptor.Ordinal == member.Ordinal {
				members[descriptor.Digest] = member.Content
			}
		}
	}

	revisions := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}}
	indexDir := t.TempDir()
	whole := focusedindex.WholeManifest{
		Schema: focusedindex.WholeManifestSchema, Repository: testRepository,
		Revisions: revisions,
		Members: []focusedindex.WholeShardMember{{
			Ordinal: 0, Count: 1, Name: focusedindex.WholeShardName(testRepository, 1, 0),
			ContentDigest: digest("runtime-v3-shard\x00", []byte(commit)), ContentBytes: 1,
			MetadataDigest: digest("runtime-v3-metadata\x00", []byte(commit)),
		}},
	}
	whole.Digest, err = focusedindex.WholeManifestDigest(whole)
	if err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.WriteControlFile(
		indexDir+"/"+focusedindex.WholeManifestName(testRepository), whole,
	); err != nil {
		t.Fatal(err)
	}
	sourceManifest := repositoryindex.SourceManifest{
		Schema: repositoryindex.SourceManifestSchema, Repository: testRepository,
		CensusPolicy: repositoryindex.CensusPolicy, Revisions: revisions,
		RevisionMembers: []repositoryindex.RevisionMember{{
			Ordinal: 0, Selector: "HEAD", Branch: "HEAD", Commit: commit,
			Digest: digest("runtime-v3-revision\x00", []byte(commit)),
		}},
		Members: []repositoryindex.SourceMember{},
	}
	sourceManifest.Digest, err = repositoryindex.SourceManifestDigest(sourceManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.WriteControlFile(
		indexDir+"/"+repositoryindex.SourceManifestName(testRepository), sourceManifest,
	); err != nil {
		t.Fatal(err)
	}
	physical := repositoryindex.PhysicalRoot{
		Schema: whole.Schema, ManifestName: focusedindex.WholeManifestName(testRepository),
		ManifestDigest: whole.Digest,
		Members: []repositoryindex.PhysicalMember{{
			Ordinal: 0, Count: 1, Name: whole.Members[0].Name,
			ContentDigest:  whole.Members[0].ContentDigest,
			ContentBytes:   whole.Members[0].ContentBytes,
			MetadataDigest: whole.Members[0].MetadataDigest,
		}},
	}
	search, err := repositoryindex.WriteSearchManifest(
		indexDir, testRepository, revisions, sourceManifest, physical,
	)
	if err != nil {
		t.Fatal(err)
	}
	scope.Search = search
	scope.State.ActiveSearchGeneration = search.Digest
	if err := servicecatalogv3.SetServiceStateDigest(&scope.State); err != nil {
		t.Fatal(err)
	}
	source := &runtimeV3Source{scope: scope, members: members}
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := store.NewServiceStateV3Reader(source, cache)
	if err != nil {
		t.Fatal(err)
	}
	repositoryStore := runtimeV3RepositoryStore{repo: store.Repo{
		Name: testRepository, IndexedCommitHash: commit, IndexedRevisions: revisions,
	}}
	opened, err := OpenRuntimeScopeV3(
		t.Context(), indexDir, repositoryStore, reader, testRepository, "svc.orders",
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats := cache.Stats(); stats.RootLeases != 1 || stats.MemberLeases != 1 {
		t.Fatalf("open runtime v3 leases = %+v", stats)
	}
	if _, valid := opened.Prepared(); !valid {
		t.Fatal("open runtime v3 scope is invalid")
	}
	source.revoked = true
	if err := ConfirmRuntimeScopeV3(
		t.Context(), indexDir, repositoryStore, reader, opened,
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked runtime v3 confirmation = %v", err)
	}
	opened.Close()
	if stats := cache.Stats(); stats.RootLeases != 0 || stats.MemberLeases != 0 {
		t.Fatalf("closed runtime v3 leases = %+v", stats)
	}
}
