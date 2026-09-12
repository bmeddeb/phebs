//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

// Real collecting-residue filesystem drains, selected controller/cursor SDK,
// authenticated FD6/quiescent workspace sampling and joined LC/WB output. The
// other fourteen owners are explicit no-ops. Shard/object contents are inert;
// this does not build a corpus or establish current/pin publication semantics.
func TestT422WorkspaceCleanupNativeComposition(t *testing.T) {
	started := time.Now()
	testT422ArchiveRetiredNativeEndpoint(t, false, true, false, true)
	t.Logf("actual cleanup: objects=%d shards=%d deleted_units=%d turns=%d cursor_writes=%d workspace_samples=%d elapsed=%s",
		t422CleanupObservationFiles, t422CleanupSearchFiles, t422CleanupExpectedDeleted,
		t422CleanupExpectedTurns, 2*t422CleanupExpectedTurns, t422CleanupExpectedTurns+2, time.Since(started))
}

// Same native admission/checkpoint path with the production ten concrete
// owners and six explicit closed owners. Files are inert residue, not built
// publications; the catalog is built and published through the real store.
func TestT422WorkspaceAllOwnersNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpoint(t, false, true, false, true, true)
}

func seedT422AllNativeOwners(t *testing.T, st *store.Surreal, root string, turns *atomic.Uint64) ([]lifecycle.Owner, func()) {
	t.Helper()
	seeded, verifyFiles := seedT422CleanupNativeOwners(t, root, turns)
	data := filepath.Join(root, "data")
	stageRepository := filepath.Join(relationshippublication.ShadowBase(filepath.Join(data, "relationships")), strings.Repeat("4", 64))
	stage := filepath.Join(stageRepository, ".stage-all-owner-cleanup")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := range t422AllOwnersRelationshipFiles {
		if err := os.WriteFile(filepath.Join(stage, fmt.Sprintf("member-%05d.json", index)), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Exercise the real regular/sparse census even with no active partial stage.
	if err := os.MkdirAll(filepath.Join(data, "extraction-publications"), 0o700); err != nil {
		t.Fatal(err)
	}
	repository, current := seedT422AllOwnerCatalog(t, st)
	// A valid resumed job census finishes on odd owner visits while the real
	// regular/sparse stage census finishes on even visits. Persist that skew in
	// the actual store so this composition cannot pass by cursor alignment.
	if err := st.CompareAndSwapLifecycleCursor(t.Context(), "owner:"+lifecycle.JobOwner, 0, `{"kind":7,"phase":"count"}`); err != nil {
		t.Fatal("seed resumed job census", err)
	}
	acquire := func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireMutationLock(ctx, filepath.Join(data, "index"))
	}
	exclusive := func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireExclusiveMutationLock(ctx, filepath.Join(data, "index"))
	}
	owners := []lifecycle.Owner{
		lifecycle.CatalogGenerationOwner{Store: st, Acquire: acquire},
		lifecycle.CatalogV3GenerationOwner{Store: st, Acquire: acquire},
		lifecycle.GenerationOwner{Store: st, Acquire: acquire},
		lifecycle.JobOwnerImpl{Store: st, Acquire: acquire},
		seeded[0].(t422CleanupCountOwner).Owner,
		seeded[1].(t422CleanupCountOwner).Owner,
		lifecycle.ObservationGenerationOwner{Root: filepath.Join(data, "observations"), Pins: &observationpublication.Cache{}, Acquire: acquire},
		lifecycle.RelationshipGenerationOwner{DataDir: data, Pins: &relationshippublication.Cache{}, AcquireExclusive: exclusive, Store: st},
		lifecycle.RelationshipGenerationOwnerV3{DataDir: data, Pins: &relationshippublication.CacheV3{}, AcquireExclusive: exclusive, Store: st},
		lifecycle.ExtractionStageOwner{Root: filepath.Join(data, "extraction-publications"), Acquire: acquire},
	}
	owners = append(owners, lifecycle.ClosedOwners()...)
	for index, owner := range owners {
		owners[index] = t422CleanupCountOwner{Owner: owner, turns: turns}
	}
	return owners, func() {
		verifyFiles()
		if _, err := os.Lstat(stageRepository); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("relationship residue survived", err)
		}
		precious, err := st.ValidateServiceCatalogV3Precious(t.Context())
		if err != nil || precious.HistoricalRoots != 3 || precious.CollectingRoots != 0 {
			t.Fatal("catalog retained/current/collecting boundary", precious, err)
		}
		candidate, err := st.GetServiceCatalogV3Candidate(t.Context(), repository)
		if err != nil || candidate.Generation.Root.Digest != current || candidate.ControlRevision != 4 {
			t.Fatal("current catalog changed", err)
		}
	}
}

func seedT422AllOwnerCatalog(t *testing.T, st *store.Surreal) (string, string) {
	t.Helper()
	repository := "example.com/native/all-owner-catalog"
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "native-cleanup", Version: "shared"},
		Services: make([]servicecatalog.Service, 10_000), Memberships: make([]servicecatalog.Membership, 31_500), Unowned: make([]servicecatalog.UnownedPlacement, 105),
	}
	for index := range catalog.Services {
		catalog.Services[index] = servicecatalog.Service{Key: fmt.Sprintf("service-%05d", index), DisplayName: "Service", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase}
	}
	for index := range catalog.Memberships {
		catalog.Memberships[index] = servicecatalog.Membership{ServiceKey: catalog.Services[index%len(catalog.Services)].Key, Path: fmt.Sprintf("owned/path-%05d", index), Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase}
	}
	for index := range catalog.Unowned {
		catalog.Unowned[index] = servicecatalog.UnownedPlacement{Path: fmt.Sprintf("unowned/path-%03d", index), Origin: servicecatalog.OriginBase}
	}
	if err := st.UpsertRepo(t.Context(), store.Repo{Name: repository, CloneURL: "https://" + repository + ".git"}); err != nil {
		t.Fatal(err)
	}
	var current string
	for index := range 4 {
		commit := fmt.Sprintf("%040x", index+1)
		generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{Repository: repository, Authority: catalog.Authority,
			Source: servicecatalogv3.Source{Kind: servicecatalog.SourceOperator, Path: "/catalog.json", Commit: commit, CensusDigest: "sha256:" + strings.Repeat("b", 64), FileCount: 31_605, AcceptedFileCount: 31_500, UnownedFileCount: 105}}, catalog)
		if err != nil || len(generation.Root.ServiceMembers) != 20 || len(generation.Root.PlacementMembers) != 16 {
			t.Fatal("real catalog member geometry", err)
		}
		if err := st.SetRepoIndexed(t.Context(), repository, commit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := st.PublishServiceCatalogV3Candidate(t.Context(), generation); err != nil {
			t.Fatal(err)
		}
		current = generation.Root.Digest
	}
	return repository, current
}

type t422CleanupCountOwner struct {
	lifecycle.Owner
	turns *atomic.Uint64
}

func (owner t422CleanupCountOwner) Sweep(ctx context.Context, now time.Time, cursor string, limits lifecycle.Limits) lifecycle.OwnerResult {
	owner.turns.Add(1)
	return owner.Owner.Sweep(ctx, now, cursor, limits)
}

func seedT422CleanupNativeOwners(t *testing.T, root string, turns *atomic.Uint64) ([]lifecycle.Owner, func()) {
	t.Helper()
	observationRoot := filepath.Join(root, "data", "observations")
	collecting := filepath.Join(observationRoot, strings.Repeat("1", 64), observationpublication.InventoryPublicationDirectoryV2,
		"collecting-stage-"+strings.Repeat("2", 32))
	objects := filepath.Join(collecting, observationpublication.InventoryPublicationInventoryNameV2, "segment-00000", "objects")
	indexRoot := filepath.Join(root, "data", "index")
	searchStage := filepath.Join(focusedindex.SearchGenerationRootDirectory(indexRoot), strings.Repeat("3", 64), ".stage-prior-owner-cleanup")
	for _, directory := range []string{objects, searchStage} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for index := range t422CleanupObservationFiles {
		path := filepath.Join(objects, fmt.Sprintf("%016x-%064x.json", index, index))
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for index := range t422CleanupSearchFiles {
		path := filepath.Join(searchStage, fmt.Sprintf("phebs-whole-%04d.zoekt", index))
		if err := os.WriteFile(path, []byte("inert cleanup fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	acquire := func(ctx context.Context) (func(), error) {
		return focusedindex.AcquireMutationLock(ctx, indexRoot)
	}
	owners := []lifecycle.Owner{
		lifecycle.ObservationInventoryOwnerV2{Root: observationRoot, Acquire: acquire, Pins: observationpublication.InventoryPinsV2{Cache: &observationpublication.InventoryCacheV2{}}},
		lifecycle.SearchGenerationOwnerImpl{IndexDir: indexRoot, Acquire: acquire, Pins: &focusedindex.SearchGenerationPins{}},
	}
	for index := range 14 {
		owners = append(owners, lifecycle.StaticOwner{OwnerName: fmt.Sprintf("test-noop-%02d", index), Completeness: lifecycle.Exact})
	}
	for index, owner := range owners {
		owners[index] = t422CleanupCountOwner{Owner: owner, turns: turns}
	}
	return owners, func() {
		for _, path := range []string{collecting, searchStage} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("collecting residue survived: %s: %v", path, err)
			}
		}
	}
}
