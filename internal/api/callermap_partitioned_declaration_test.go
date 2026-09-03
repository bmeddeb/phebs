package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/store"
)

type partitionedCallerDeclarationStore struct {
	store.EvidenceStore
	authority                                store.PartitionedAssertionAuthority
	assertion                                store.Assertion
	resolution                               store.EvidenceResolution
	status                                   string
	sealed, current, empty, publishedKey     bool
	pageErr, locatorErr                      error
	legacyPages, nativePages, legacyLocators int
	nativeLocators                           int
}

func (state *partitionedCallerDeclarationStore) ListAssertions(_ context.Context, query store.AssertionQuery) ([]store.Assertion, error) {
	state.legacyPages++
	if state.status != "published" || !state.publishedKey || state.empty {
		return nil, nil
	}
	return state.page(query)
}

func (state *partitionedCallerDeclarationStore) ListPartitionedAssertions(_ context.Context, query store.AssertionQuery, authority store.PartitionedAssertionAuthority) ([]store.Assertion, error) {
	state.nativePages++
	if state.pageErr != nil {
		return nil, state.pageErr
	}
	if state.status != "staged" || state.publishedKey || !state.sealed || !state.current || authority != state.authority {
		return nil, store.ErrNotFound
	}
	if state.empty {
		return nil, nil
	}
	return state.page(query)
}

func (state *partitionedCallerDeclarationStore) page(query store.AssertionQuery) ([]store.Assertion, error) {
	if query.Repo != state.assertion.Repo || query.RunID != state.assertion.RunID ||
		query.Predicate != state.assertion.Predicate || query.Object != state.assertion.Object ||
		query.Lineage != state.assertion.Lineage || query.Limit != 1 || query.AllowTruncate || query.After != nil {
		return nil, errors.New("declaration query lost its exact scope")
	}
	return []store.Assertion{state.assertion}, nil
}

func (state *partitionedCallerDeclarationStore) ResolveEvidence(_ context.Context, repository, runID, atomID string) (*store.EvidenceResolution, error) {
	state.legacyLocators++
	if state.status != "published" || !state.publishedKey || repository != state.authority.Repository || runID != state.authority.RunID || atomID != state.resolution.Atom.ID {
		return nil, store.ErrNotFound
	}
	return &state.resolution, nil
}

func (state *partitionedCallerDeclarationStore) ResolvePartitionedEvidence(_ context.Context, authority store.PartitionedAssertionAuthority, atomID string) (*store.EvidenceResolution, error) {
	state.nativeLocators++
	if state.locatorErr != nil {
		return nil, state.locatorErr
	}
	if state.status != "staged" || state.publishedKey || !state.sealed || !state.current || authority != state.authority || atomID != state.resolution.Atom.ID {
		return nil, store.ErrNotFound
	}
	return &state.resolution, nil
}

func newPartitionedCallerDeclaration() (*partitionedCallerDeclarationStore, *callerexecute.PublicationRead, CallerMapQuery) {
	authority := store.PartitionedAssertionAuthority{
		Repository: "example.invalid/neutral", Domain: "proto-contract", RunID: "native-run",
		Commit: strings.Repeat("a", 40), PlanDigest: exactAuthorityDigest("1"), RootDigest: exactAuthorityDigest("2"),
		CandidateManifestDigest: exactAuthorityDigest("3"), CandidatePolicyDigest: exactAuthorityDigest("4"),
	}
	state := &partitionedCallerDeclarationStore{
		authority: authority, status: "staged", sealed: true, current: true,
		assertion: store.Assertion{
			ID: "declaration", Repo: authority.Repository, RunID: authority.RunID,
			Predicate: "DECLARES_OPERATION", Subject: "api.proto", Object: "orders.Orders/Get",
			Lineage: exactAuthorityTestLineage, Tier: store.TierExact, Supporting: []string{"atom"},
		},
		resolution: store.EvidenceResolution{
			Atom: store.EvidenceAtom{ID: "atom", StartByte: 0, EndByte: 5},
			Occurrences: []store.SnapshotEvidence{{
				ID: "occurrence", AtomID: "atom", Repo: authority.Repository, RunID: authority.RunID,
				Commit: authority.Commit, Path: "api.proto", StartLine: 1, EndLine: 1,
				VisibilityScope: "repo:" + authority.Repository,
			}},
		},
	}
	read := &callerexecute.PublicationRead{
		Availability: callerexecute.PublicationCurrent,
		Resolver: &store.ResolverCatalogPublication{Declarations: []store.ResolverCatalogDeclarationPublication{{
			Domain: authority.Domain, RunID: authority.RunID, AuthoritySchema: store.PartitionedExtractionDomainSchema,
			GenerationDigest: authority.RootDigest, PlanDigest: authority.PlanDigest, RootDigest: authority.RootDigest,
		}}},
		Summary: &store.CallerGenerationPublicationSummary{Generation: store.CallerGenerationIdentity{
			Repository: authority.Repository, HeadCommit: authority.Commit,
			CandidateManifestDigest: authority.CandidateManifestDigest, CandidatePolicyDigest: authority.CandidatePolicyDigest,
		}},
	}
	query := CallerMapQuery{Endpoint: CallerMapEndpoint{
		Protocol: "protobuf", Repository: authority.Repository, Lineage: state.assertion.Lineage,
		Operation: "/" + state.assertion.Object,
	}}
	return state, read, query
}

func TestExactCallerPartitionedDeclarationDispatchAndFences(t *testing.T) {
	for _, test := range []struct {
		name          string
		mutate        func(*partitionedCallerDeclarationStore, *callerexecute.PublicationRead)
		status        int
		legacyPages   int
		nativePages   int
		legacySources int
		nativeSources int
	}{
		{name: "native staged sealed root", nativePages: 1, nativeSources: 1},
		{name: "legacy published", mutate: func(state *partitionedCallerDeclarationStore, read *callerexecute.PublicationRead) {
			state.status = "published"
			state.publishedKey = true
			read.Resolver.Declarations[0] = store.ResolverCatalogDeclarationPublication{Domain: state.authority.Domain, RunID: state.authority.RunID}
		}, legacyPages: 1, legacySources: 1},
		{name: "legacy status without publication key", mutate: func(state *partitionedCallerDeclarationStore, read *callerexecute.PublicationRead) {
			state.status = "published"
			read.Resolver.Declarations[0] = store.ResolverCatalogDeclarationPublication{Domain: state.authority.Domain, RunID: state.authority.RunID}
		}, status: http.StatusNotFound, legacyPages: 1},
		{name: "legacy empty never falls back", mutate: func(state *partitionedCallerDeclarationStore, read *callerexecute.PublicationRead) {
			read.Resolver.Declarations[0] = store.ResolverCatalogDeclarationPublication{Domain: state.authority.Domain, RunID: state.authority.RunID}
		}, status: http.StatusNotFound, legacyPages: 1},
		{name: "native empty never falls back", mutate: func(state *partitionedCallerDeclarationStore, _ *callerexecute.PublicationRead) {
			state.empty = true
		}, status: http.StatusNotFound, nativePages: 1},
		{name: "unsealed native rejected", mutate: func(state *partitionedCallerDeclarationStore, _ *callerexecute.PublicationRead) {
			state.sealed = false
		}, status: http.StatusNotFound, nativePages: 1},
		{name: "superseded root rejected", mutate: func(state *partitionedCallerDeclarationStore, _ *callerexecute.PublicationRead) {
			state.current = false
		}, status: http.StatusNotFound, nativePages: 1},
		{name: "page authority transition", mutate: func(state *partitionedCallerDeclarationStore, _ *callerexecute.PublicationRead) {
			state.pageErr = store.ErrConflict
		}, status: http.StatusConflict, nativePages: 1},
		{name: "locator authority transition", mutate: func(state *partitionedCallerDeclarationStore, _ *callerexecute.PublicationRead) {
			state.locatorErr = store.ErrConflict
		}, status: http.StatusConflict, nativePages: 1, nativeSources: 1},
		{name: "locator absent", mutate: func(state *partitionedCallerDeclarationStore, _ *callerexecute.PublicationRead) {
			state.locatorErr = store.ErrNotFound
		}, status: http.StatusNotFound, nativePages: 1, nativeSources: 1},
		{name: "locator operational failure", mutate: func(state *partitionedCallerDeclarationStore, _ *callerexecute.PublicationRead) {
			state.locatorErr = errors.New("private failure")
		}, status: http.StatusInternalServerError, nativePages: 1, nativeSources: 1},
		{name: "historical native not admitted", mutate: func(_ *partitionedCallerDeclarationStore, read *callerexecute.PublicationRead) {
			read.Availability = callerexecute.PublicationStale
		}, status: http.StatusConflict},
		{name: "partial native authority never legacy", mutate: func(_ *partitionedCallerDeclarationStore, read *callerexecute.PublicationRead) {
			read.Resolver.Declarations[0].AuthoritySchema = ""
		}, status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, read, query := newPartitionedCallerDeclaration()
			if test.mutate != nil {
				test.mutate(state, read)
			}
			claim, err := exactCallerDeclaration(t.Context(), state, read, protocolPacks[0], query)
			if test.status == 0 {
				if err != nil || claim.RunID != state.authority.RunID || len(claim.Sources) != 1 || claim.Sources[0].Path != "api.proto" {
					t.Fatalf("exact declaration = %+v, %v", claim, err)
				}
			} else if status, ok := err.(huma.StatusError); !ok || status.GetStatus() != test.status || !reflect.DeepEqual(claim, ContractCatalogClaim{}) {
				t.Fatalf("refused declaration = %+v, %v; want status %d", claim, err, test.status)
			}
			if got, want := [4]int{state.legacyPages, state.nativePages, state.legacyLocators, state.nativeLocators}, [4]int{test.legacyPages, test.nativePages, test.legacySources, test.nativeSources}; got != want {
				t.Fatalf("legacy/native page and locator calls = %v, want %v", got, want)
			}
		})
	}
}

func TestExactCallerPartitionedDeclarationRequiresNativeCapabilities(t *testing.T) {
	state, read, query := newPartitionedCallerDeclaration()
	legacyOnly := struct{ store.EvidenceStore }{EvidenceStore: state}
	_, err := exactCallerDeclaration(t.Context(), legacyOnly, read, protocolPacks[0], query)
	if status, ok := err.(huma.StatusError); !ok || status.GetStatus() != http.StatusConflict {
		t.Fatalf("missing native reader = %v, want conflict", err)
	}
	if state.legacyPages+state.nativePages+state.legacyLocators+state.nativeLocators != 0 {
		t.Fatal("missing capability fell back to a legacy or partial read")
	}
}

func TestExactCallerPartitionedLocatorCannotEscapeSelectedRun(t *testing.T) {
	state, _, _ := newPartitionedCallerDeclaration()
	source := exactPartitionedDeclarationEvidence{EvidenceStore: state, native: state, authority: state.authority}
	if _, err := source.ResolveEvidence(t.Context(), state.authority.Repository, "foreign-run", "atom"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("foreign run = %v, want conflict", err)
	}
	if state.legacyLocators+state.nativeLocators != 0 {
		t.Fatal("foreign run reached an evidence store")
	}
}
