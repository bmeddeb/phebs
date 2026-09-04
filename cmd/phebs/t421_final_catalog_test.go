package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type t421FinalCatalogTestReadSource struct {
	mu          sync.Mutex
	root        servicecatalogv3.Root
	members     map[string][]byte
	memberReads int
}

func newT421FinalCatalogTestReadSource(
	generation servicecatalogv3.Generation,
) *t421FinalCatalogTestReadSource {
	members := make(map[string][]byte, len(generation.Members))
	descriptors := append(
		slices.Clone(generation.Root.ServiceMembers),
		generation.Root.PlacementMembers...,
	)
	for index, descriptor := range descriptors {
		members[descriptor.Digest] = slices.Clone(generation.Members[index].Content)
	}
	return &t421FinalCatalogTestReadSource{
		root: generation.Root, members: members,
	}
}

func (source *t421FinalCatalogTestReadSource) ReadServiceCatalogV3Root(
	ctx context.Context,
	repository, digest string,
) (servicecatalogv3.Root, error) {
	if err := ctx.Err(); err != nil {
		return servicecatalogv3.Root{}, err
	}
	if repository != source.root.Binding.Repository || digest != source.root.Digest {
		return servicecatalogv3.Root{}, errors.New("missing test root")
	}
	return cloneT421FinalCatalogRoot(source.root), nil
}

func (source *t421FinalCatalogTestReadSource) ReadServiceCatalogV3Member(
	ctx context.Context,
	descriptor servicecatalogv3.MemberDescriptor,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	source.memberReads++
	raw, ok := source.members[descriptor.Digest]
	if !ok {
		return nil, errors.New("missing test member")
	}
	return slices.Clone(raw), nil
}

func (source *t421FinalCatalogTestReadSource) reads() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.memberReads
}

func TestT421FinalCatalogCacheColdCommitWarmAndOwnership(t *testing.T) {
	generation, selector, visits := t421FinalCatalogTestFixture(t)
	source := newT421FinalCatalogTestReadSource(generation)
	cache := &t421FinalCatalogCache{}

	coldContext, coldLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: visits},
	)
	if err != nil {
		t.Fatal(err)
	}
	cold, pending, err := cache.prepare(
		coldContext, source, selector, generation.Root,
	)
	if err != nil || pending == nil ||
		cold.Projection.CatalogLogicalSHA256 != generation.Root.LogicalDigest ||
		cold.Projection.CatalogSource.Records != 5 {
		t.Fatalf("cold snapshot = %+v, pending=%t, error=%v", cold, pending != nil, err)
	}
	counts, err := coldLedger.Finish()
	if err != nil || counts.MemberVisits != visits ||
		source.reads() != len(generation.Members) {
		t.Fatalf("cold accounting = %+v, reads=%d, error=%v", counts, source.reads(), err)
	}
	canceledCommit, cancelCommit := context.WithCancel(t.Context())
	cancelCommit()
	if err := pending.commitAfterFinalFence(canceledCommit); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commit = %v", err)
	}

	zeroContext, zeroLedger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	beforeCommit, beforeCommitPending, err := cache.prepare(
		zeroContext, source, selector, generation.Root,
	)
	if !errors.Is(err, readaccounting.ErrLimit) ||
		!reflect.DeepEqual(beforeCommit, t421FinalCatalogSnapshot{}) || beforeCommitPending != nil {
		t.Fatalf("uncommitted retry = %+v, pending=%t, error=%v", beforeCommit, beforeCommitPending != nil, err)
	}
	if _, finishErr := zeroLedger.Finish(); !errors.Is(finishErr, readaccounting.ErrLimit) {
		t.Fatalf("uncommitted accounting = %v", finishErr)
	}
	readsBeforeWarm := source.reads()
	if err := pending.commitAfterFinalFence(t.Context()); err != nil {
		t.Fatal(err)
	}

	warmContext, warmLedger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	warm, warmPending, err := cache.prepare(
		warmContext, source, selector, generation.Root,
	)
	if err != nil || warmPending != nil || !reflect.DeepEqual(warm, cold) {
		t.Fatalf("warm snapshot = %+v, pending=%t, error=%v", warm, warmPending != nil, err)
	}
	counts, err = warmLedger.Finish()
	if err != nil || counts != (readaccounting.Counts{}) || source.reads() != readsBeforeWarm {
		t.Fatalf("warm accounting = %+v, reads=%d, error=%v", counts, source.reads(), err)
	}

	warm.Selector.Digest = "mutated"
	warm.Projection.SemanticSHA256 = "mutated"
	warm.Root.ServiceMembers[0].Digest = "mutated"
	warm.Root.Binding.Override.ID = "mutated"
	owned, _, err := cache.prepare(t.Context(), source, selector, generation.Root)
	if err != nil || !reflect.DeepEqual(owned, cold) {
		t.Fatalf("cache returned caller-owned mutation: %+v, %v", owned, err)
	}
	malformed := generation.Root
	malformed.Services++
	if snapshot, pending, err := cache.prepare(
		t.Context(), source, selector, malformed,
	); !errors.Is(err, servicecatalogv3.ErrInvalid) ||
		!reflect.DeepEqual(snapshot, t421FinalCatalogSnapshot{}) || pending != nil {
		t.Fatalf("malformed warm root = %+v, pending=%t, error=%v", snapshot, pending != nil, err)
	}
}

func TestT421FinalCatalogCacheUsesFullSelectorAndOneEpochSlot(t *testing.T) {
	generation, selector, visits := t421FinalCatalogTestFixture(t)
	source := newT421FinalCatalogTestReadSource(generation)
	cache := &t421FinalCatalogCache{}

	contextA, ledgerA, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: visits},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, pendingA, err := cache.prepare(contextA, source, selector, generation.Root)
	if err != nil || pendingA == nil {
		t.Fatalf("first fill = pending %t, %v", pendingA != nil, err)
	}
	if _, err := ledgerA.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := pendingA.commitAfterFinalFence(t.Context()); err != nil {
		t.Fatal(err)
	}

	next := selector
	next.CatalogControlRevision++
	next.StateControlRevision++
	next.StateSummaryDigest = testRuntimeDigest("7")
	next.SearchGenerationDigest = testRuntimeDigest("8")
	next.RelationshipGenerationDigest = testRuntimeDigest("9")
	next.RelationshipRootDigest = testRuntimeDigest("a")
	next.ControlRevision++
	next.Digest = testRuntimeDigest("b")
	next.ChangedAt = next.ChangedAt.Add(time.Second)
	missContext, missLedger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.prepare(missContext, source, next, generation.Root); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("changed full selector reused root-only cache: %v", err)
	}
	if _, err := missLedger.Finish(); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("changed selector accounting = %v", err)
	}

	nextContext, nextLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: visits},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, pendingNext, err := cache.prepare(nextContext, source, next, generation.Root)
	if err != nil || pendingNext == nil {
		t.Fatalf("replacement fill = pending %t, %v", pendingNext != nil, err)
	}
	if _, err := nextLedger.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := pendingNext.commitAfterFinalFence(t.Context()); err != nil {
		t.Fatal(err)
	}

	for name, candidate := range map[string]struct {
		cache    *t421FinalCatalogCache
		selector store.ServiceRuntimeSelector
	}{
		"replaced prior slot": {cache: cache, selector: selector},
		"new process epoch":   {cache: &t421FinalCatalogCache{}, selector: next},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, ledger, startErr := readaccounting.Start(t.Context(), readaccounting.Counts{})
			if startErr != nil {
				t.Fatal(startErr)
			}
			if _, _, prepareErr := candidate.cache.prepare(
				ctx, source, candidate.selector, generation.Root,
			); !errors.Is(prepareErr, readaccounting.ErrLimit) {
				t.Fatalf("unexpected cache hit: %v", prepareErr)
			}
			if _, finishErr := ledger.Finish(); !errors.Is(finishErr, readaccounting.ErrLimit) {
				t.Fatalf("cold accounting = %v", finishErr)
			}
		})
	}
}

func TestT421FinalCatalogCacheFailuresDoNotInsert(t *testing.T) {
	generation, selector, visits := t421FinalCatalogTestFixture(t)
	source := newT421FinalCatalogTestReadSource(generation)
	cache := &t421FinalCatalogCache{}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if snapshot, pending, err := cache.prepare(
		canceled, source, selector, generation.Root,
	); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(snapshot, t421FinalCatalogSnapshot{}) || pending != nil || source.reads() != 0 {
		t.Fatalf("canceled fill = %+v, pending=%t, reads=%d, error=%v", snapshot, pending != nil, source.reads(), err)
	}

	limitedContext, limitedLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: visits - 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, pending, err := cache.prepare(
		limitedContext, source, selector, generation.Root,
	)
	if !errors.Is(err, readaccounting.ErrLimit) ||
		!reflect.DeepEqual(snapshot, t421FinalCatalogSnapshot{}) || pending != nil {
		t.Fatalf("limited fill = %+v, pending=%t, error=%v", snapshot, pending != nil, err)
	}
	if _, err := limitedLedger.Finish(); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("limited accounting = %v", err)
	}

	zeroContext, zeroLedger, err := readaccounting.Start(t.Context(), readaccounting.Counts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cache.prepare(zeroContext, source, selector, generation.Root); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("failed fill populated cache: %v", err)
	}
	if _, err := zeroLedger.Finish(); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("failed-fill retry accounting = %v", err)
	}

	retryContext, retryLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: visits},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, retryPending, err := cache.prepare(
		retryContext, source, selector, generation.Root,
	); err != nil || retryPending == nil {
		t.Fatalf("exact retry = pending %t, %v", retryPending != nil, err)
	}
	if counts, err := retryLedger.Finish(); err != nil || counts.MemberVisits != visits {
		t.Fatalf("exact retry accounting = %+v, %v", counts, err)
	}
}

func t421FinalCatalogTestFixture(
	t *testing.T,
) (servicecatalogv3.Generation, store.ServiceRuntimeSelector, uint64) {
	t.Helper()
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "catalog", Version: "v1",
	}
	override := &servicecatalog.OperatorOverride{ID: "operator", Version: "v1"}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority, Override: override,
		Services: []servicecatalog.Service{
			{Key: "billing", DisplayName: "Billing", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
			{Key: "orders", DisplayName: "Orders", Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase},
		},
		Memberships: []servicecatalog.Membership{
			{ServiceKey: "billing", Path: "services/billing", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
			{ServiceKey: "orders", Path: "services/orders", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
		},
		Unowned: []servicecatalog.UnownedPlacement{{Path: "docs", Origin: servicecatalog.OriginOverride}},
	}
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{
		Repository: "example/catalog",
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator, Path: "/tmp/catalog.json",
			Commit: strings.Repeat("c", 40), CensusDigest: testRuntimeDigest("c"),
			FileCount: 3, AcceptedFileCount: 2, UnownedFileCount: 1,
		},
		Authority: authority, Override: override,
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	selector := store.ServiceRuntimeSelector{
		Schema: store.ServiceRuntimeSelectorSchema, Repository: generation.Root.Binding.Repository,
		Backend: store.ServiceRuntimeV3, CatalogRootDigest: generation.Root.Digest,
		CatalogControlRevision: 1, StateControlRevision: 2,
		StateSummaryDigest: testRuntimeDigest("1"), SearchGenerationDigest: testRuntimeDigest("2"),
		RelationshipGenerationDigest: testRuntimeDigest("3"), RelationshipRootDigest: testRuntimeDigest("4"),
		ControlRevision: 3, Digest: testRuntimeDigest("5"), ChangedAt: time.Unix(42, 0).UTC(),
	}
	return generation, selector, t421FinalCatalogTestMemberVisits(t, generation)
}

func t421FinalCatalogTestMemberVisits(
	t *testing.T,
	generation servicecatalogv3.Generation,
) uint64 {
	t.Helper()
	var visits uint64
	for _, encoded := range generation.Members {
		switch encoded.Kind {
		case "service":
			var member servicecatalogv3.ServiceMember
			if err := json.Unmarshal(encoded.Content, &member); err != nil {
				t.Fatal(err)
			}
			visits += uint64(len(member.Services) + len(member.Memberships))
		case "placement":
			var member servicecatalogv3.PlacementMember
			if err := json.Unmarshal(encoded.Content, &member); err != nil {
				t.Fatal(err)
			}
			visits += uint64(len(member.Inherited) + len(member.Placements))
		default:
			t.Fatalf("unexpected member kind %q", encoded.Kind)
		}
	}
	if visits == 0 {
		t.Fatal("empty member-visit fixture")
	}
	return visits
}
