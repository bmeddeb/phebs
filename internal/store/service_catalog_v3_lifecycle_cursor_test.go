package store_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

// This is a real store/publication/shared-member drain, not a modeled owner.
// It deliberately places the oldest root last in digest order: advancing past
// an unfinished root used to add an empty EOF/reset visit after every edge.
func TestServiceCatalogV3LifecycleResumesSharedMemberDrain(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	repository := "example.com/acme/catalog-v3-cursor"
	if err := s.UpsertRepo(ctx, store.Repo{Name: repository, CloneURL: "https://" + repository + ".git"}); err != nil {
		t.Fatal(err)
	}
	generations := lifecycleSharedCatalogGenerations(t, repository)
	sort.Slice(generations, func(i, j int) bool { return generations[i].Root.Digest > generations[j].Root.Digest })
	for _, generation := range generations {
		if err := s.SetRepoIndexed(ctx, repository, generation.Root.Binding.Source.Commit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	oldest := generations[0].Root
	retired, err := s.SweepServiceCatalogV3Lifecycle(ctx, "", 11, 16, store.ServiceCatalogV3Retained)
	if err != nil || !retired.More || retired.Deleted != 0 || retired.RetiredLogicalBytes != int64(oldest.LogicalBytes) ||
		retired.Cursor != generations[1].Root.Digest {
		t.Fatalf("retirement must retain the unfinished root's predecessor: %+v / %v", retired, err)
	}
	cursor := retired.Cursor
	memberCount := len(oldest.ServiceMembers) + len(oldest.PlacementMembers)
	for member := range memberCount {
		// An already-canceled read must not advance the durable member cursor.
		if member == memberCount/2 {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := s.SweepServiceCatalogV3Lifecycle(canceled, cursor, 11, 16, store.ServiceCatalogV3Retained); err == nil {
				t.Fatal("canceled collecting sweep succeeded")
			}
		}
		sweep, err := s.SweepServiceCatalogV3Lifecycle(ctx, cursor, 11, 16, store.ServiceCatalogV3Retained)
		if err != nil || sweep.Scanned != 1 || !sweep.More || sweep.Cursor != cursor || sweep.Deleted != 0 ||
			sweep.RetiredLogicalBytes != 0 || sweep.DeletedRootBytes != 0 || sweep.DeletedMemberBytes != 0 {
			t.Fatalf("shared member %d must advance without an EOF/reset turn: %+v / %v", member, sweep, err)
		}
	}
	finalized, err := s.SweepServiceCatalogV3Lifecycle(ctx, cursor, 11, 16, store.ServiceCatalogV3Retained)
	if err != nil || finalized.Scanned != 1 || !finalized.More || finalized.Cursor != oldest.Digest ||
		finalized.Deleted != 1 || finalized.DeletedRootBytes != int64(oldest.RootBytes) || finalized.DeletedMemberBytes != 0 {
		t.Fatalf("finalization must advance beyond the removed root: %+v / %v", finalized, err)
	}
	wrapped, err := s.SweepServiceCatalogV3Lifecycle(ctx, finalized.Cursor, 11, 16, store.ServiceCatalogV3Retained)
	if err != nil || wrapped.Scanned != 0 || !wrapped.More || wrapped.Cursor != "" {
		t.Fatalf("one completed-pass wrap: %+v / %v", wrapped, err)
	}
	drained, err := s.SweepServiceCatalogV3Lifecycle(ctx, wrapped.Cursor, 11, 16, store.ServiceCatalogV3Retained)
	if err != nil || drained.Scanned != 3 || drained.More || drained.Cursor != "" || drained.Deleted != 0 {
		t.Fatalf("retained roots must close an exact drained pass: %+v / %v", drained, err)
	}
	precious, err := s.ValidateServiceCatalogV3Precious(ctx)
	if err != nil || precious.HistoricalRoots != 3 || precious.CollectingRoots != 0 {
		t.Fatalf("retained candidate/prior roots or shared members changed: %+v / %v", precious, err)
	}
	current, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || current.Generation.Root.Digest != generations[3].Root.Digest || current.ControlRevision != 4 ||
		!reflect.DeepEqual(current.Generation.Members, generations[3].Members) {
		t.Fatalf("current shared generation changed: %v", err)
	}
	t.Logf("real shared catalog drain: members=%d progressing_visits=%d terminal_visits=3, candidate and two priors intact", memberCount, memberCount)
}

func lifecycleSharedCatalogGenerations(t *testing.T, repository string) []servicecatalogv3.Generation {
	t.Helper()
	// Match the frozen corpus's 10,000-service / 31,605-selector member
	// geometry without running its source/index/extraction constructors.
	catalog := servicecatalog.Catalog{
		Schema:      servicecatalog.Schema,
		Authority:   servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "catalog-owner", Version: "shared"},
		Services:    make([]servicecatalog.Service, 10_000),
		Memberships: make([]servicecatalog.Membership, 31_500),
		Unowned:     make([]servicecatalog.UnownedPlacement, 105),
	}
	for index := range catalog.Services {
		catalog.Services[index] = servicecatalog.Service{Key: fmt.Sprintf("service-%05d", index), DisplayName: "Service",
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase}
	}
	for index := range catalog.Memberships {
		catalog.Memberships[index] = servicecatalog.Membership{ServiceKey: catalog.Services[index%len(catalog.Services)].Key,
			Path: fmt.Sprintf("owned/path-%05d", index), Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase}
	}
	for index := range catalog.Unowned {
		catalog.Unowned[index] = servicecatalog.UnownedPlacement{Path: fmt.Sprintf("unowned/path-%03d", index), Origin: servicecatalog.OriginBase}
	}
	result := make([]servicecatalogv3.Generation, 4)
	for index := range result {
		generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{Repository: repository,
			Source: servicecatalogv3.Source{Kind: servicecatalog.SourceOperator, Path: "/catalog.json", Commit: fmt.Sprintf("%040x", index+1),
				CensusDigest: "sha256:" + strings.Repeat("b", 64), FileCount: 31_605, AcceptedFileCount: 31_500, UnownedFileCount: 105},
			Authority: catalog.Authority}, catalog)
		if err != nil {
			t.Fatal(err)
		}
		if len(generation.Root.ServiceMembers) != 20 || len(generation.Root.PlacementMembers) != 16 {
			t.Fatalf("fixture member geometry changed: service=%d placement=%d", len(generation.Root.ServiceMembers), len(generation.Root.PlacementMembers))
		}
		if index > 0 && !reflect.DeepEqual(generation.Members, result[0].Members) {
			t.Fatal("fixture roots do not share their actual member bytes")
		}
		result[index] = generation
	}
	return result
}
