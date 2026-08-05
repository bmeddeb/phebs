package store_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestServiceCatalogPublicationLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/mono"
	commitA := "1111111111111111111111111111111111111111"
	commitB := "2222222222222222222222222222222222222222"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/mono.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commitA, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	first := testCatalogPublication(t, repository, commitA, "catalog", "orders")
	if err := s.PublishServiceCatalog(ctx, first); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	stored, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GenerationDigest != first.GenerationDigest ||
		stored.ControlRevision != 1 || stored.PublishedAt.IsZero() {
		t.Fatalf("first stored publication = %+v", stored)
	}
	firstPublishedAt := stored.PublishedAt
	if err := s.PublishServiceCatalog(ctx, first); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	stored, err = s.GetServiceCatalog(ctx, repository)
	if err != nil || stored.ControlRevision != 1 ||
		!stored.PublishedAt.Equal(firstPublishedAt) {
		t.Fatalf("exact retry changed pointer = %+v, %v", stored, err)
	}

	// One selected authority version is immutable even if a replacement
	// presents other well-formed canonical bytes.
	reusedVersion := testCatalogPublication(t, repository, commitA, "catalog", "Orders API")
	if err := s.PublishServiceCatalog(ctx, reusedVersion); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reused version error = %v, want ErrConflict", err)
	}
	stored, err = s.GetServiceCatalog(ctx, repository)
	if err != nil || stored.GenerationDigest != first.GenerationDigest {
		t.Fatalf("failed replacement changed prior authority = %+v, %v", stored, err)
	}

	second := testCatalogPublication(t, repository, commitB, "catalog", "Orders API")
	if err := s.PublishServiceCatalog(ctx, second); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale HEAD error = %v, want ErrConflict", err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commitB, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishServiceCatalog(ctx, second); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	stored, err = s.GetServiceCatalog(ctx, repository)
	if err != nil || stored.GenerationDigest != second.GenerationDigest ||
		stored.ControlRevision != 2 {
		t.Fatalf("second stored publication = %+v, %v", stored, err)
	}
	historical, err := s.GetServiceCatalogGeneration(
		ctx, repository, first.GenerationDigest,
	)
	if err != nil || historical.GenerationDigest != first.GenerationDigest ||
		historical.ControlRevision != 0 || !historical.PublishedAt.IsZero() ||
		string(historical.Canonical) != string(first.Canonical) {
		t.Fatalf("historical generation = %+v, %v", historical, err)
	}
}

func TestCatalogLifecycleRetainsCurrentAndTwoRollbackGenerations(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/catalog-lifecycle"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/catalog-lifecycle.git",
	}); err != nil {
		t.Fatal(err)
	}
	publications := make([]servicecatalog.Publication, 0, 5)
	for index := range 5 {
		commit := fmt.Sprintf("%040d", index+1)
		if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		publication := testCatalogPublication(
			t, repository, commit, "catalog", fmt.Sprintf("Orders %d", index),
		)
		if err := s.PublishServiceCatalog(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publications = append(publications, publication)
		time.Sleep(20 * time.Millisecond)
	}
	sweep, err := s.SweepCatalogLifecycle(
		ctx, "", 11, 16, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	deleted := sweep.Deleted
	for attempts := 0; sweep.More && attempts < 10; attempts++ {
		sweep, err = s.SweepCatalogLifecycle(
			ctx, sweep.Cursor, 11, 16, 3,
		)
		if err != nil {
			t.Fatal(err)
		}
		deleted += sweep.Deleted
	}
	var missing []int
	for index, publication := range publications {
		_, err := s.GetServiceCatalogGeneration(ctx, repository, publication.GenerationDigest)
		if err != nil && strings.Contains(err.Error(), "current generation is absent") {
			missing = append(missing, index)
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if deleted != 2 || !reflect.DeepEqual(missing, []int{0, 1}) {
		t.Fatalf("catalog lifecycle deleted=%d final=%+v, missing=%v; want oldest two", deleted, sweep, missing)
	}
}

func TestCatalogLifecycleProtectsActiveServiceCatalog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/catalog-active-root"
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/catalog-active-root.git",
	}); err != nil {
		t.Fatal(err)
	}
	publications := make([]servicecatalog.Publication, 0, 5)
	for index := range 5 {
		commit := fmt.Sprintf("%040d", index+20)
		if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		publication := testCatalogPublication(
			t, repository, commit, "catalog", fmt.Sprintf("Active %d", index),
		)
		if err := s.PublishServiceCatalog(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publications = append(publications, publication)
		if index == 0 {
			persisted, err := s.GetServiceCatalog(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ReconcileServiceStates(ctx, *persisted); err != nil {
				t.Fatal(err)
			}
			state, err := s.GetServiceState(ctx, repository, "orders")
			if err != nil {
				t.Fatal(err)
			}
			summary, err := s.GetServiceStateSummary(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ActivateService(ctx, servicecatalog.ServiceActivation{
				Repository: repository, ServiceKey: "orders",
				Incarnation: state.Incarnation, DesiredGeneration: state.DesiredGeneration,
				StateControlRevision:      state.ControlRevision,
				RepositoryControlRevision: summary.ControlRevision,
			}); err != nil {
				t.Fatal(err)
			}
			// Catalog timestamps use the store's second-precision wire format.
			// Put the active generation strictly before the later rollback set
			// so this test proves the service root, rather than a timestamp tie,
			// preserves it.
			nextSecond := time.Now().UTC().Truncate(time.Second).Add(time.Second)
			time.Sleep(time.Until(nextSecond) + 20*time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cursor := ""
	deleted := 0
	for range 30 {
		sweep, err := s.SweepCatalogLifecycle(
			ctx, cursor, 11, 16, 3,
		)
		if err != nil {
			t.Fatal(err)
		}
		deleted += sweep.Deleted
		cursor = sweep.Cursor
	}
	if _, err := s.GetServiceCatalogGeneration(
		ctx, repository, publications[0].GenerationDigest,
	); err != nil {
		t.Fatalf("active historical catalog was collected: %v", err)
	}
	var missing []int
	for index, publication := range publications {
		_, err := s.GetServiceCatalogGeneration(ctx, repository, publication.GenerationDigest)
		if err != nil && strings.Contains(err.Error(), "current generation is absent") {
			missing = append(missing, index)
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if deleted != 1 || len(missing) != 1 || missing[0] == 0 || missing[0] == 4 {
		t.Fatalf("catalog lifecycle deleted=%d cursor=%q missing=%v; want one unrooted historical generation", deleted, cursor, missing)
	}
}

func TestServiceCatalogAnalysisUnitV1Fence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/legacy"
	commit := "3333333333333333333333333333333333333333"
	unit, err := (analysisunit.Scope{
		Repository: repository, Name: "orders", Primary: []string{"svc"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/legacy.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexedState(ctx, repository, commit, nil, unit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	publication := testLegacyCatalogPublication(t, repository, commit, unit.Digest)
	if err := s.PublishServiceCatalog(ctx, publication); err != nil {
		t.Fatalf("publish exact legacy authority: %v", err)
	}

	otherDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mismatch := testLegacyCatalogPublication(t, repository, commit, otherDigest)
	if err := s.PublishServiceCatalog(ctx, mismatch); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mismatched v1 digest error = %v, want ErrConflict", err)
	}
	stored, err := s.GetServiceCatalog(ctx, repository)
	if err != nil || stored.LegacyAnalysisUnitDigest != unit.Digest {
		t.Fatalf("legacy authority after refusal = %+v, %v", stored, err)
	}
}

func TestServiceCatalogSurvivesReopen(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	directory := t.TempDir()
	repository := "example.com/acme/reopen-catalog"
	commit := "4444444444444444444444444444444444444444"
	current, err := store.OpenLocal(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/reopen-catalog.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := current.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	want := testCatalogPublication(t, repository, commit, "catalog", "orders")
	if err := current.PublishServiceCatalog(ctx, want); err != nil {
		t.Fatal(err)
	}
	before, err := current.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenLocal(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	after, err := reopened.GetServiceCatalog(ctx, repository)
	if err != nil || after.GenerationDigest != before.GenerationDigest ||
		after.ControlRevision != before.ControlRevision ||
		!after.PublishedAt.Equal(before.PublishedAt) ||
		string(after.Canonical) != string(before.Canonical) {
		t.Fatalf("reopened publication = %+v, %v; want %+v", after, err, before)
	}
}

func testCatalogPublication(
	t *testing.T,
	repository, commit, authorityID, displayName string,
) servicecatalog.Publication {
	t.Helper()
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityCommitted, ID: authorityID, Version: commit,
		},
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: displayName,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
		Unowned: []servicecatalog.UnownedPlacement{},
	}
	return finishTestCatalogPublication(
		t, repository, commit, servicecatalog.SourceCommitted, "/catalog.json", "", catalog,
	)
}

func testLegacyCatalogPublication(
	t *testing.T,
	repository, commit, digest string,
) servicecatalog.Publication {
	t.Helper()
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator,
			ID:   servicecatalog.AnalysisUnitV1AuthorityID, Version: digest,
		},
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
		Unowned: []servicecatalog.UnownedPlacement{},
	}
	return finishTestCatalogPublication(
		t, repository, commit, servicecatalog.SourceAnalysisUnitV1, "", digest, catalog,
	)
}

func finishTestCatalogPublication(
	t *testing.T,
	repository, commit, sourceKind, sourcePath, legacyDigest string,
	catalog servicecatalog.Catalog,
) servicecatalog.Publication {
	t.Helper()
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind: sourceKind, SourcePath: sourcePath, SourceCommit: commit,
		SourceCensusDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		SourceFileCount:    1, AcceptedFileCount: 1, UnownedFileCount: 0,
		Authority: catalog.Authority, Override: catalog.Override,
		CatalogDigest: digest, LegacyAnalysisUnitDigest: legacyDigest,
		Canonical: canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidatePublication(publication, false); err != nil {
		t.Fatal(err)
	}
	return publication
}
