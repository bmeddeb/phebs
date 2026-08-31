package store_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestServiceCatalogV3LifecycleRetainsCandidateAndTwoPrior(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	repository := "example.com/acme/catalog-v3-lifecycle"
	commit := strings.Repeat("a", 40)
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/catalog-v3-lifecycle.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for index := range 5 {
		generation := lifecycleServiceCatalogV3Generation(
			t, repository, commit, fmt.Sprintf("v%d", index),
		)
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatalf("publish generation %d: %v", index, err)
		}
		time.Sleep(time.Millisecond)
	}
	cursor := ""
	for turn := range 256 {
		sweep, err := s.SweepServiceCatalogV3Lifecycle(
			ctx, cursor, 11, 16, store.ServiceCatalogV3Retained,
		)
		if err != nil {
			t.Fatalf("lifecycle turn %d: %+v, %v", turn, sweep, err)
		}
		cursor = sweep.Cursor
		report, validateErr := s.ValidateServiceCatalogV3Precious(ctx)
		if validateErr != nil {
			t.Fatalf("validate turn %d: %+v, %v", turn, report, validateErr)
		}
		if report.HistoricalRoots == store.ServiceCatalogV3Retained &&
			report.CollectingRoots == 0 {
			candidate, openErr := s.GetServiceCatalogV3Candidate(ctx, repository)
			if openErr != nil || candidate.ControlRevision != 5 {
				t.Fatalf("retained candidate = %+v, %v", candidate, openErr)
			}
			return
		}
	}
	t.Fatal("catalog v3 lifecycle did not converge")
}

func lifecycleServiceCatalogV3Generation(
	t *testing.T,
	repository, commit, version string,
) servicecatalogv3.Generation {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "catalog-owner", Version: version,
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders " + version,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
		Unowned: []servicecatalog.UnownedPlacement{},
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator, Path: "/catalog.json", Commit: commit,
			CensusDigest: "sha256:" + strings.Repeat("b", 64),
			FileCount:    1, AcceptedFileCount: 1,
		},
		Authority: authority,
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return generation
}
