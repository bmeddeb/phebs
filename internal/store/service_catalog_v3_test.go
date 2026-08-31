package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestServiceCatalogV3CandidateLifecycleAndV2Isolation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/catalog-v3"
	commitA := strings.Repeat("1", 40)
	commitB := strings.Repeat("2", 40)
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/catalog-v3.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commitA, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	v2 := testCatalogPublication(t, repository, commitA, "catalog", "Orders")
	if err := s.PublishServiceCatalog(ctx, v2); err != nil {
		t.Fatal(err)
	}
	v2Before, err := s.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}

	first := testServiceCatalogV3Generation(t, repository, commitA, "Orders")
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatalf("publish first v3 candidate: %v", err)
	}
	opened, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || opened.Generation.Root.Digest != first.Root.Digest ||
		opened.ControlRevision != 1 || opened.PublishedAt.IsZero() {
		t.Fatalf("first v3 candidate = %+v, %v", opened, err)
	}
	firstPublished := opened.PublishedAt
	if err := s.PublishServiceCatalogV3Candidate(ctx, first); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	opened, err = s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || opened.ControlRevision != 1 ||
		!opened.PublishedAt.Equal(firstPublished) {
		t.Fatalf("exact retry changed pointer = %+v, %v", opened, err)
	}

	reused := testServiceCatalogV3Generation(t, repository, commitA, "Orders API")
	if err := s.PublishServiceCatalogV3Candidate(ctx, reused); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same authority version replacement = %v", err)
	}
	opened, err = s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || opened.Generation.Root.Digest != first.Root.Digest ||
		opened.ControlRevision != 1 {
		t.Fatalf("refusal changed pointer = %+v, %v", opened, err)
	}

	if err := s.SetRepoIndexed(ctx, repository, commitB, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	second := testServiceCatalogV3Generation(t, repository, commitB, "Orders API")
	if err := s.PublishServiceCatalogV3Candidate(ctx, second); err != nil {
		t.Fatalf("publish second v3 candidate: %v", err)
	}
	opened, err = s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || opened.Generation.Root.Digest != second.Root.Digest ||
		opened.ControlRevision != 2 {
		t.Fatalf("second v3 candidate = %+v, %v", opened, err)
	}

	v2After, err := s.GetServiceCatalog(ctx, repository)
	if err != nil || !reflect.DeepEqual(v2After, v2Before) {
		t.Fatalf("v3 changed v2 authority = %+v, %v; want %+v", v2After, err, v2Before)
	}
	v2Historical, err := s.GetServiceCatalogGeneration(ctx, repository, v2.GenerationDigest)
	if err != nil || !reflect.DeepEqual(v2Historical.Canonical, v2.Canonical) ||
		v2Historical.GenerationDigest != v2.GenerationDigest {
		t.Fatalf("v3 changed v2 history = %+v, %v", v2Historical, err)
	}
}

func TestServiceCatalogV3ConcurrentExactPublication(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/catalog-v3-concurrent"
	commit := strings.Repeat("3", 40)
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/catalog-v3-concurrent.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	generation := testServiceCatalogV3Generation(t, repository, commit, "Orders")
	start := make(chan struct{})
	errorsByWorker := make([]error, 2)
	var wait sync.WaitGroup
	for worker := range errorsByWorker {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errorsByWorker[worker] = s.PublishServiceCatalogV3Candidate(ctx, generation)
		}()
	}
	close(start)
	wait.Wait()
	for worker, err := range errorsByWorker {
		if err != nil {
			t.Fatalf("worker %d: %v", worker, err)
		}
	}
	opened, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || opened.ControlRevision != 1 ||
		opened.Generation.Root.Digest != generation.Root.Digest {
		t.Fatalf("concurrent exact candidate = %+v, %v", opened, err)
	}
}

func TestServiceCatalogV3LiveMaximumShape(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repository := "example.com/acme/catalog-v3-maximum"
	commit := strings.Repeat("4", 40)
	if err := s.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://example.com/acme/catalog-v3-maximum.git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: commit,
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services:    make([]servicecatalog.Service, 0, servicecatalogv3.MaxTotalServices),
		Memberships: make([]servicecatalog.Membership, 0, servicecatalogv3.MaxMemberships),
	}
	roles := [6]struct {
		path int
		role string
	}{
		{0, servicecatalog.RolePrimary}, {0, servicecatalog.RoleShared},
		{1, servicecatalog.RoleSupporting}, {1, servicecatalog.RoleShared},
		{2, servicecatalog.RoleSupporting}, {2, servicecatalog.RoleTyped},
	}
	for index := range servicecatalogv3.MaxTotalServices {
		key := fmt.Sprintf("service-%05d", index)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		})
		for _, item := range roles {
			catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
				ServiceKey: key, Path: fmt.Sprintf("service/%05d/%d", index, item.path),
				Role: item.role, Origin: servicecatalog.OriginBase,
			})
		}
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceCommitted, Path: "/catalog.json", Commit: commit,
			CensusDigest: "sha256:" + strings.Repeat("a", 64),
			FileCount:    37_500, AcceptedFileCount: 37_500,
		},
		Authority: authority,
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if generation.Root.Services != servicecatalogv3.MaxTotalServices ||
		generation.Root.Memberships != servicecatalogv3.MaxMemberships ||
		generation.Root.Paths != 37_500 {
		t.Fatalf("maximum root = %+v", generation.Root)
	}
	if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatalf("publish maximum candidate: %v", err)
	}
	opened, err := s.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil || opened.Generation.Root.Digest != generation.Root.Digest ||
		len(opened.Generation.Members) != len(generation.Members) {
		t.Fatalf("maximum candidate open = %+v, %v", opened, err)
	}
	if len(generation.Members) < 2 || len(generation.Members) > servicecatalogv3.MaxMembers {
		t.Fatalf("maximum member inventory = %d", len(generation.Members))
	}
	nextCommit := strings.Repeat("5", 40)
	if err := s.SetRepoIndexed(ctx, repository, nextCommit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	next := testServiceCatalogV3Generation(t, repository, nextCommit, "Next")
	if err := s.PublishServiceCatalogV3Candidate(ctx, next); err != nil {
		t.Fatal(err)
	}
	var retiredLogical, deletedRoot, deletedMembers int64
	cursor := ""
	for turn := range 256 {
		sweep, sweepErr := s.SweepServiceCatalogV3Lifecycle(ctx, cursor, 11, 16, 1)
		if sweepErr != nil {
			t.Fatalf("maximum lifecycle turn %d: %+v, %v", turn, sweep, sweepErr)
		}
		cursor = sweep.Cursor
		retiredLogical += sweep.RetiredLogicalBytes
		deletedRoot += sweep.DeletedRootBytes
		deletedMembers += sweep.DeletedMemberBytes
		if deletedRoot == int64(generation.Root.RootBytes) {
			break
		}
	}
	if retiredLogical != int64(generation.Root.LogicalBytes) ||
		deletedRoot != int64(generation.Root.RootBytes) ||
		deletedMembers != int64(generation.Root.EncodedMemberBytes) {
		t.Fatalf(
			"maximum lifecycle bytes logical/root/member = %d/%d/%d; want %d/%d/%d",
			retiredLogical, deletedRoot, deletedMembers,
			generation.Root.LogicalBytes, generation.Root.RootBytes,
			generation.Root.EncodedMemberBytes,
		)
	}
	report, err := s.ValidateServiceCatalogV3Precious(ctx)
	if err != nil || report.HistoricalRoots != 1 || report.CollectingRoots != 0 {
		t.Fatalf("maximum lifecycle final = %+v, %v", report, err)
	}
}

func testServiceCatalogV3Generation(
	t *testing.T,
	repository, commit, displayName string,
) servicecatalogv3.Generation {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: commit,
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: displayName,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: repository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceCommitted, Path: "/catalog.json", Commit: commit,
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
