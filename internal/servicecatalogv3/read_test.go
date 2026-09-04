package servicecatalogv3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

type readTestSource struct {
	mu sync.Mutex

	roots       map[readRootKey]Root
	members     map[string][]byte
	rootReads   int
	memberReads int
}

func newReadTestSource(generations ...Generation) *readTestSource {
	source := &readTestSource{
		roots: make(map[readRootKey]Root), members: make(map[string][]byte),
	}
	for _, generation := range generations {
		root := generation.Root
		source.roots[readRootKey{
			repository: root.Binding.Repository, digest: root.Digest,
		}] = root
		descriptors := append(
			append([]MemberDescriptor{}, root.ServiceMembers...),
			root.PlacementMembers...,
		)
		for index, descriptor := range descriptors {
			source.members[descriptor.Digest] = append(
				[]byte(nil), generation.Members[index].Content...,
			)
		}
	}
	return source
}

func (source *readTestSource) ReadServiceCatalogV3Root(
	_ context.Context,
	repository, digest string,
) (Root, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.rootReads++
	root, ok := source.roots[readRootKey{repository: repository, digest: digest}]
	if !ok {
		return Root{}, errors.New("missing root")
	}
	if err := ValidateRoot(root); err != nil {
		return Root{}, err
	}
	return cloneReadRoot(root), nil
}

func (source *readTestSource) ReadServiceCatalogV3Member(
	_ context.Context,
	descriptor MemberDescriptor,
) ([]byte, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.memberReads++
	raw, ok := source.members[descriptor.Digest]
	if !ok {
		return nil, errors.New("missing member")
	}
	return append([]byte(nil), raw...), nil
}

func TestReadCacheColdWarmConcurrentAndLeaseRetirement(t *testing.T) {
	if _, err := NewReadCache(0, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid cache limits = %v", err)
	}
	first := readTestGeneration(t, "a", MaxServicesPerMember+1)
	second := readTestGeneration(t, "b", 1)
	source := newReadTestSource(first, second)
	cache, err := NewReadCache(2, 2)
	if err != nil {
		t.Fatal(err)
	}

	lease, err := cache.Open(
		t.Context(), source, first.Root.Binding.Repository, first.Root.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		projection, readErr := lease.Service(t.Context(), source, "service-00000")
		if readErr != nil || projection.Service.Key != "service-00000" {
			t.Fatalf("warm service = %+v, %v", projection, readErr)
		}
	}
	stats := cache.Stats()
	if stats.RootReads != 1 || stats.RootValidations != 1 ||
		stats.MemberReads != 1 || stats.MemberValidations != 1 ||
		stats.RootLeases != 1 || stats.MemberLeases != 1 {
		t.Fatalf("cold/warm stats = %+v", stats)
	}

	const readers = 32
	errorsByReader := make(chan error, readers)
	var group sync.WaitGroup
	for range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			current, openErr := cache.Open(
				context.Background(), source,
				first.Root.Binding.Repository, first.Root.Digest,
			)
			if openErr != nil {
				errorsByReader <- openErr
				return
			}
			defer current.Close()
			_, openErr = current.Service(context.Background(), source, "service-00000")
			errorsByReader <- openErr
		}()
	}
	group.Wait()
	close(errorsByReader)
	for readErr := range errorsByReader {
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	stats = cache.Stats()
	if stats.RootReads != 1 || stats.MemberReads != 1 {
		t.Fatalf("concurrent cache fill repeated source reads = %+v", stats)
	}
	lease.Close()
	if cache.Stats().RootLeases != 0 || cache.Stats().MemberLeases != 0 {
		t.Fatalf("released lease stats = %+v", cache.Stats())
	}

	bounded, err := NewReadCache(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := bounded.Open(
		t.Context(), source, first.Root.Binding.Repository, first.Root.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pinned.Service(t.Context(), source, "service-00000"); err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Open(
		t.Context(), source, second.Root.Binding.Repository, second.Root.Digest,
	); !errors.Is(err, ErrReadCacheFull) {
		t.Fatalf("leased root retirement = %v, want cache full", err)
	}
	if _, err := pinned.Service(
		t.Context(), source, fmt.Sprintf("service-%05d", MaxServicesPerMember),
	); !errors.Is(err, ErrReadCacheFull) {
		t.Fatalf("leased member retirement = %v, want cache full", err)
	}
	pinned.Close()
	replacement, err := bounded.Open(
		t.Context(), source, second.Root.Binding.Repository, second.Root.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	replacement.Close()
	if bounded.Stats().RootEntries != 1 {
		t.Fatalf("bounded root entries = %+v", bounded.Stats())
	}
}

func TestReadCacheMemberAccountingColdWarmAndLimitRefusal(t *testing.T) {
	generation := readTestGeneration(t, "accounting", 2)
	descriptor := generation.Root.ServiceMembers[0]
	wantVisits := uint64(descriptor.Records + descriptor.Memberships)
	if wantVisits < 2 {
		t.Fatalf("member visits = %d, want at least two", wantVisits)
	}

	source := newReadTestSource(generation)
	cache := NewDefaultReadCache()
	lease, err := cache.Open(
		t.Context(), source, generation.Root.Binding.Repository, generation.Root.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	scoped, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		projection, readErr := lease.Service(scoped, source, "service-00000")
		if readErr != nil || projection.Service.Key != "service-00000" {
			t.Fatalf("cold/warm projection = %+v, %v", projection, readErr)
		}
	}
	counts, err := ledger.Finish()
	if err != nil || counts.MemberVisits != wantVisits {
		t.Fatalf("cold/warm member visits = %+v, %v", counts, err)
	}
	if stats := cache.Stats(); stats.MemberReads != 1 || stats.MemberEntries != 1 {
		t.Fatalf("cold/warm cache stats = %+v", stats)
	}

	refusalSource := newReadTestSource(generation)
	refusalCache := NewDefaultReadCache()
	refusalLease, err := refusalCache.Open(
		t.Context(), refusalSource,
		generation.Root.Binding.Repository, generation.Root.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer refusalLease.Close()
	limited, limitedLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits - 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := refusalLease.Service(limited, refusalSource, "service-00000")
	if !errors.Is(err, readaccounting.ErrLimit) || projection.Service.Key != "" {
		t.Fatalf("limited projection = %+v, %v", projection, err)
	}
	counts, err = limitedLedger.Finish()
	if !errors.Is(err, readaccounting.ErrLimit) || counts.MemberVisits != wantVisits {
		t.Fatalf("limited member visits = %+v, %v", counts, err)
	}
	if stats := refusalCache.Stats(); stats.MemberReads != 1 || stats.MemberEntries != 0 {
		t.Fatalf("refused fill cache stats = %+v", stats)
	}

	retry, retryLedger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	projection, err = refusalLease.Service(retry, refusalSource, "service-00000")
	if err != nil || projection.Service.Key != "service-00000" {
		t.Fatalf("retry projection = %+v, %v", projection, err)
	}
	counts, err = retryLedger.Finish()
	if err != nil || counts.MemberVisits != wantVisits {
		t.Fatalf("retry member visits = %+v, %v", counts, err)
	}
	if stats := refusalCache.Stats(); stats.MemberReads != 2 || stats.MemberEntries != 1 {
		t.Fatalf("retry cache stats = %+v", stats)
	}
}

func TestReadCatalogContextReadsCompleteGenerationAndAccountsMembers(t *testing.T) {
	catalog := acceptedCatalog(MaxPathsPerMember+1, true)
	catalog.Services = append(catalog.Services, servicecatalog.Service{
		Key: "tree-owner", DisplayName: "tree owner",
		Disposition: servicecatalog.DispositionAccepted,
		Origin:      servicecatalog.OriginBase,
	})
	catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
		ServiceKey: "tree-owner", Path: "tree",
		Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
	})
	generation, err := Build(testBinding(catalog.Authority), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(generation.Root.ServiceMembers) < 2 || len(generation.Root.PlacementMembers) < 2 {
		t.Fatalf("incomplete fixture: service=%d placement=%d", len(generation.Root.ServiceMembers), len(generation.Root.PlacementMembers))
	}
	var wantVisits uint64
	for _, encoded := range generation.Members {
		switch encoded.Kind {
		case "service":
			var member ServiceMember
			if err := decodeCanonical(encoded.Content, &member); err != nil {
				t.Fatal(err)
			}
			wantVisits += uint64(len(member.Services) + len(member.Memberships))
		case "placement":
			var member PlacementMember
			if err := decodeCanonical(encoded.Content, &member); err != nil {
				t.Fatal(err)
			}
			wantVisits += uint64(len(member.Inherited) + len(member.Placements))
		default:
			t.Fatalf("unexpected member kind %q", encoded.Kind)
		}
	}
	source := newReadTestSource(generation)
	scoped, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := ReadCatalogContext(scoped, source, generation.Root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := servicecatalog.Normalize(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opened, want) {
		t.Fatal("complete catalog differs")
	}
	logicalDigest, err := NormalizedCatalogLogicalDigest(t.Context(), opened)
	if err != nil || logicalDigest != generation.Root.LogicalDigest {
		t.Fatalf("logical digest = %q, %v", logicalDigest, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NormalizedCatalogLogicalDigest(canceled, opened); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled logical digest = %v", err)
	}
	counts, err := ledger.Finish()
	if err != nil || counts.MemberVisits != wantVisits {
		t.Fatalf("member visits = %+v, %v", counts, err)
	}
	if source.rootReads != 0 || source.memberReads != len(generation.Members) {
		t.Fatalf("source reads: root=%d member=%d, want root=0 member=%d", source.rootReads, source.memberReads, len(generation.Members))
	}

	source.mu.Lock()
	for digest := range source.members {
		source.members[digest][0] ^= 0xff
		break
	}
	source.mu.Unlock()
	if !reflect.DeepEqual(opened, want) {
		t.Fatal("returned catalog aliases source member bytes")
	}
}

func TestProjectServiceMemberAccountsDecodedRecordsBeforeSemanticRefusal(t *testing.T) {
	generation := readTestGeneration(t, "semantic-refusal", 2)
	descriptor := generation.Root.ServiceMembers[0]
	raw := generation.Members[0].Content
	var member ServiceMember
	if err := decodeCanonical(raw, &member); err != nil {
		t.Fatal(err)
	}
	wantVisits := uint64(len(member.Services) + len(member.Memberships))
	member.PolicyDigest = rawDigest([]byte("wrong policy"))
	raw, err := canonical(member)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.ContentBytes = len(raw)
	descriptor.Digest = rawDigest(raw)
	sourceGeneration, err := SourceGenerationDigest(generation.Root)
	if err != nil {
		t.Fatal(err)
	}
	scoped, ledger, err := readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	projections, err := projectServiceMember(
		scoped, generation.Root, descriptor, raw, sourceGeneration,
	)
	if !errors.Is(err, ErrInvalid) || projections != nil {
		t.Fatalf("semantic refusal = %+v, %v", projections, err)
	}
	counts, accountingErr := ledger.Finish()
	if accountingErr != nil || counts.MemberVisits != wantVisits {
		t.Fatalf("semantic-refusal visits = %+v, %v", counts, accountingErr)
	}

	raw = append([]byte(" "), generation.Members[0].Content...)
	descriptor = generation.Root.ServiceMembers[0]
	descriptor.ContentBytes = len(raw)
	descriptor.Digest = rawDigest(raw)
	scoped, ledger, err = readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	projections, err = projectServiceMember(
		scoped, generation.Root, descriptor, raw, sourceGeneration,
	)
	if !errors.Is(err, ErrInvalid) || projections != nil {
		t.Fatalf("canonical refusal = %+v, %v", projections, err)
	}
	counts, accountingErr = ledger.Finish()
	if accountingErr != nil || counts.MemberVisits != wantVisits {
		t.Fatalf("canonical-refusal visits = %+v, %v", counts, accountingErr)
	}

	raw = append(bytes.Clone(generation.Members[0].Content), []byte("{}")...)
	descriptor = generation.Root.ServiceMembers[0]
	descriptor.ContentBytes = len(raw)
	descriptor.Digest = rawDigest(raw)
	scoped, ledger, err = readaccounting.Start(
		t.Context(), readaccounting.Counts{MemberVisits: wantVisits},
	)
	if err != nil {
		t.Fatal(err)
	}
	projections, err = projectServiceMember(
		scoped, generation.Root, descriptor, raw, sourceGeneration,
	)
	if !errors.Is(err, ErrInvalid) || projections != nil {
		t.Fatalf("trailing-value refusal = %+v, %v", projections, err)
	}
	counts, accountingErr = ledger.Finish()
	if accountingErr != nil || counts.MemberVisits != wantVisits {
		t.Fatalf("trailing-value visits = %+v, %v", counts, accountingErr)
	}
}

func TestReadCacheRejectsMalformedRootWithoutFallback(t *testing.T) {
	generation := readTestGeneration(t, "malformed", 1)
	source := newReadTestSource(generation)
	key := readRootKey{
		repository: generation.Root.Binding.Repository,
		digest:     generation.Root.Digest,
	}
	root := source.roots[key]
	root.Services++
	source.roots[key] = root
	cache := NewDefaultReadCache()
	if _, err := cache.Open(
		t.Context(), source, key.repository, key.digest,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed root = %v, want invalid", err)
	}
	if stats := cache.Stats(); stats.RootEntries != 0 || stats.RootReads != 1 ||
		stats.RootValidations != 1 {
		t.Fatalf("malformed-root stats = %+v", stats)
	}
}

func TestReadCacheReturnsNotExistForServiceRangeHole(t *testing.T) {
	catalog := acceptedCatalog(3, false)
	catalog.Services = append(catalog.Services[:1], catalog.Services[2:]...)
	catalog.Memberships = append(catalog.Memberships[:1], catalog.Memberships[2:]...)
	generation, err := Build(testBinding(catalog.Authority), catalog)
	if err != nil {
		t.Fatal(err)
	}
	source := newReadTestSource(generation)
	cache := NewDefaultReadCache()
	lease, err := cache.Open(
		t.Context(), source, generation.Root.Binding.Repository, generation.Root.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := lease.Service(t.Context(), source, "service-00001"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("routed service-range hole = %v, want not exist", err)
	}
	if stats := cache.Stats(); stats.MemberReads != 1 || stats.MemberValidations != 1 {
		t.Fatalf("range-hole member was not strict-opened: %+v", stats)
	}
}

func readTestGeneration(t *testing.T, version string, count int) Generation {
	t.Helper()
	catalog := acceptedCatalog(count, false)
	catalog.Authority = servicecatalog.Authority{
		Kind:    servicecatalog.AuthorityOperator,
		ID:      "read-cache",
		Version: version,
	}
	generation, err := Build(Binding{
		Repository: "example.com/acme/read-cache",
		Source: Source{
			Kind: servicecatalog.SourceOperator, Path: "/catalog.json",
			Commit:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			CensusDigest: rawDigest([]byte("read-cache-" + version)),
		},
		Authority: catalog.Authority,
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return generation
}
