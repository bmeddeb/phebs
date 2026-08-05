package servicequery

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const testRepository = "example.invalid/servicequery"

func TestCompileBindsCurrentServiceInsideZoektQuery(t *testing.T) {
	t.Parallel()

	publication := testPublication(
		t, strings.Repeat("1", 40), "v1",
		[]string{"services/orders", "shared/trace.go"}, 4,
	)
	state, summary := testState(t, publication, publication, servicecatalog.StatusCurrent)
	search := testSearchManifest(t, publication.SourceCommit)
	compiled, err := Compile(Request{
		Expression: "T342_NEEDLE", RevisionSelector: "HEAD",
		Scopes: []PreparedScope{mustPrepare(t, Scope{
			CurrentCatalog: publication, ActiveCatalog: publication,
			Summary: summary, State: state, Search: search,
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	repositories, branches, predicates := queryAtoms(compiled.Query)
	if !slices.Equal(repositories, []string{testRepository}) {
		t.Fatalf("repository predicates = %v", repositories)
	}
	if !slices.Equal(branches, []string{"HEAD"}) || len(predicates) != 2 {
		t.Fatalf("branches = %v, path predicates = %v", branches, predicates)
	}
	for _, path := range []string{"services/orders", "shared/trace.go"} {
		found := false
		for _, pattern := range predicates {
			if regexp.MustCompile(pattern).MatchString(path) &&
				regexp.MustCompile(pattern).MatchString(path+"/child.go") &&
				!regexp.MustCompile(pattern).MatchString("prefix/"+path) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no exact descendant predicate for %q in %v", path, predicates)
		}
	}
	if compiled.Authority.PathCount != 2 ||
		compiled.Authority.PredicateAtoms != 2 ||
		compiled.Authority.CurrentCatalogGeneration != publication.GenerationDigest ||
		compiled.Authority.ActiveCatalogGeneration != publication.GenerationDigest ||
		compiled.Authority.RepositorySourceGeneration != search.SourceGenerationDigest ||
		compiled.Authority.RepositorySearchGeneration != search.Digest {
		t.Fatalf("authority = %+v", compiled.Authority)
	}
	canonical, err := compiled.Authority.Canonical()
	if err != nil || !json.Valid(canonical) {
		t.Fatalf("canonical authority = %q, error %v", canonical, err)
	}
	decoded, err := DecodeAuthority(canonical)
	if err != nil || decoded != compiled.Authority {
		t.Fatalf("decoded authority = %+v, error %v", decoded, err)
	}
	withUnknown := slices.Clone(canonical[:len(canonical)-1])
	withUnknown = append(withUnknown, []byte(`,"unknown":true}`)...)
	if _, err := DecodeAuthority(withUnknown); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("unknown-field authority error = %v", err)
	}
	withDuplicate := slices.Clone(canonical[:len(canonical)-1])
	withDuplicate = append(withDuplicate, []byte(`,"schema":"phebs-service-query-authority-v1"}`)...)
	if _, err := DecodeAuthority(withDuplicate); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("duplicate-field authority error = %v", err)
	}

	tampered := compiled.Authority
	tampered.StateSummaryRevision++
	if err := ValidateAuthority(tampered); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("tampered authority error = %v", err)
	}
}

func TestCompileUsesExactStaleActiveGeneration(t *testing.T) {
	t.Parallel()

	active := testPublication(
		t, strings.Repeat("2", 40), "v2",
		[]string{"services/payments"}, 8,
	)
	current := testPublication(
		t, strings.Repeat("3", 40), "v3",
		[]string{"services/billing-next"}, 9,
	)
	state, summary := testState(t, current, active, servicecatalog.StatusStale)
	compiled, err := Compile(Request{
		Expression: "T342_STALE", RevisionSelector: "release",
		Scopes: []PreparedScope{mustPrepare(t, Scope{
			CurrentCatalog: current, ActiveCatalog: active,
			Summary: summary, State: state,
			Search: testSearchManifest(t, active.SourceCommit),
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, branches, predicates := queryAtoms(compiled.Query)
	if !slices.Equal(branches, []string{"refs/tags/v1^{commit}"}) ||
		len(predicates) != 1 ||
		!regexp.MustCompile(predicates[0]).MatchString("services/payments/client.go") ||
		regexp.MustCompile(predicates[0]).MatchString("services/billing-next/client.go") {
		t.Fatalf("stale branch/predicates = %v / %v", branches, predicates)
	}
	if compiled.Authority.Status != servicecatalog.StatusStale ||
		compiled.Authority.CurrentCatalogGeneration != current.GenerationDigest ||
		compiled.Authority.ActiveCatalogGeneration != active.GenerationDigest ||
		compiled.Authority.RevisionCommit != strings.Repeat("4", 40) {
		t.Fatalf("stale authority = %+v", compiled.Authority)
	}

	mismatched := testSearchManifest(t, current.SourceCommit)
	_, err = Prepare(Scope{
		CurrentCatalog: current, ActiveCatalog: active,
		Summary: summary, State: state, Search: mismatched,
	})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("mismatched active reader error = %v", err)
	}
}

func TestCompileRefusesUnavailableAndCrossRepositoryScopes(t *testing.T) {
	t.Parallel()

	publication := testPublication(
		t, strings.Repeat("5", 40), "v5", []string{"service"}, 1,
	)
	state, summary := testState(t, publication, publication, servicecatalog.StatusCurrent)
	state.Status = servicecatalog.StatusUnavailable
	state.ActiveDesiredGeneration = ""
	state.ActiveSourceGeneration = ""
	state.ActiveCatalogGeneration = ""
	if err := servicecatalog.SetServiceStateDigest(&state); err != nil {
		t.Fatal(err)
	}
	scope := Scope{
		CurrentCatalog: publication, ActiveCatalog: publication,
		Summary: summary, State: state,
		Search: testSearchManifest(t, publication.SourceCommit),
	}
	if _, err := Prepare(scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable state error = %v", err)
	}

	state, summary = testState(t, publication, publication, servicecatalog.StatusCurrent)
	scope.State, scope.Summary = state, summary
	prepared := mustPrepare(t, scope)
	if _, err := Compile(Request{
		Expression: "needle", RevisionSelector: "HEAD",
		Scopes: []PreparedScope{prepared, prepared},
	}); !errors.Is(err, ErrUnsupportedComposition) {
		t.Fatalf("cross-repository composition error = %v", err)
	}
	if _, err := Compile(Request{
		Expression: "needle", RevisionSelector: "HEAD",
	}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("empty composition error = %v", err)
	}
}

func TestCompileDeduplicatesRolesAndBoundsPredicateComplexity(t *testing.T) {
	t.Parallel()

	paths := make([]string, servicecatalog.MaxServicePaths)
	for index := range paths {
		paths[index] = fmt.Sprintf("service/path-%03d", index)
	}
	publication := testPublication(
		t, strings.Repeat("6", 40), "maximum", paths, 12,
	)
	state, summary := testState(t, publication, publication, servicecatalog.StatusCurrent)
	compiled, err := Compile(Request{
		Expression:       strings.Repeat("x", MaxExpressionBytes),
		RevisionSelector: "HEAD",
		Scopes: []PreparedScope{mustPrepare(t, Scope{
			CurrentCatalog: publication, ActiveCatalog: publication,
			Summary: summary, State: state,
			Search: testSearchManifest(t, publication.SourceCommit),
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Authority.PathCount != servicecatalog.MaxServicePaths ||
		compiled.Authority.PredicateAtoms != servicecatalog.MaxServicePaths ||
		compiled.Authority.PredicateBytes > MaxPredicateBytes {
		t.Fatalf("maximum predicate authority = %+v", compiled.Authority)
	}

	request := Request{
		Expression:       strings.Repeat("x", MaxExpressionBytes+1),
		RevisionSelector: "HEAD", Scopes: []PreparedScope{{}},
	}
	if _, err := Compile(request); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("oversized expression error = %v", err)
	}

	roles := testPublicationWithRoles(t, strings.Repeat("7", 40))
	state, summary = testState(t, roles, roles, servicecatalog.StatusCurrent)
	compiled, err = Compile(Request{
		Expression: "needle", RevisionSelector: "HEAD",
		Scopes: []PreparedScope{mustPrepare(t, Scope{
			CurrentCatalog: roles, ActiveCatalog: roles,
			Summary: summary, State: state,
			Search: testSearchManifest(t, roles.SourceCommit),
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Authority.PathCount != 2 || compiled.Authority.PredicateAtoms != 2 {
		t.Fatalf("role-deduplicated authority = %+v", compiled.Authority)
	}
}

func queryAtoms(compiled query.Q) ([]string, []string, []string) {
	var repositories, branches, predicates []string
	query.VisitAtoms(compiled, func(atom query.Q) {
		switch value := atom.(type) {
		case *query.RepoSet:
			for repository := range value.Set {
				repositories = append(repositories, repository)
			}
		case *query.Branch:
			if value.Exact {
				branches = append(branches, value.Pattern)
			}
		case *query.Regexp:
			if value.FileName && value.CaseSensitive {
				predicates = append(predicates, value.Regexp.String())
			}
		}
	})
	slices.Sort(repositories)
	slices.Sort(branches)
	slices.Sort(predicates)
	return repositories, branches, predicates
}

func mustPrepare(t *testing.T, scope Scope) PreparedScope {
	t.Helper()
	prepared, err := Prepare(scope)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func testPublication(
	t *testing.T,
	commit, version string,
	paths []string,
	revision uint64,
) servicecatalog.Publication {
	t.Helper()
	memberships := make([]servicecatalog.Membership, 0, len(paths))
	for _, path := range paths {
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: "svc.orders", Path: path,
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	}
	return testPublicationFromCatalog(t, commit, version, servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "test", Version: version,
		},
		Services: []servicecatalog.Service{{
			Key: "svc.orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: memberships,
		Unowned:     []servicecatalog.UnownedPlacement{},
	}, revision)
}

func testPublicationWithRoles(t *testing.T, commit string) servicecatalog.Publication {
	t.Helper()
	return testPublicationFromCatalog(t, commit, "roles", servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "test", Version: "roles",
		},
		Services: []servicecatalog.Service{{
			Key: "svc.orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{
			{ServiceKey: "svc.orders", Path: "service", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
			{ServiceKey: "svc.orders", Path: "typed/index.scip", Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase},
			{ServiceKey: "svc.orders", Path: "typed/index.scip", Role: servicecatalog.RoleTyped, Origin: servicecatalog.OriginBase},
		},
		Unowned: []servicecatalog.UnownedPlacement{},
	}, 2)
}

func testPublicationFromCatalog(
	t *testing.T,
	commit, version string,
	catalog servicecatalog.Catalog,
	revision uint64,
) servicecatalog.Publication {
	t.Helper()
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema:             servicecatalog.PublicationSchema,
		Repository:         testRepository,
		SourceKind:         servicecatalog.SourceOperator,
		SourcePath:         "/catalog/" + version + ".json",
		SourceCommit:       commit,
		SourceCensusDigest: digest("test-census\x00", []byte(commit)),
		SourceFileCount:    len(catalog.Memberships),
		AcceptedFileCount:  len(catalog.Memberships),
		Authority:          catalog.Authority,
		CatalogDigest:      catalogDigest,
		Canonical:          canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	publication.ControlRevision = revision
	publication.PublishedAt = time.Date(2026, 8, 5, 12, 0, int(revision), 0, time.UTC)
	if err := servicecatalog.ValidatePublication(publication, true); err != nil {
		t.Fatal(err)
	}
	return publication
}

func testState(
	t *testing.T,
	current, active servicecatalog.Publication,
	status string,
) (servicecatalog.ServiceState, servicecatalog.RepositoryState) {
	t.Helper()
	desired, found, err := servicecatalog.ProjectService(current, "svc.orders")
	if err != nil || !found {
		t.Fatalf("desired projection: found %v, error %v", found, err)
	}
	activeProjection, found, err := servicecatalog.ProjectService(active, "svc.orders")
	if err != nil || !found {
		t.Fatalf("active projection: found %v, error %v", found, err)
	}
	desiredGeneration, err := servicecatalog.ServiceDesiredGeneration(desired, 1)
	if err != nil {
		t.Fatal(err)
	}
	activeGeneration, err := servicecatalog.ServiceDesiredGeneration(activeProjection, 1)
	if err != nil {
		t.Fatal(err)
	}
	state := servicecatalog.ServiceState{
		Schema:     servicecatalog.ServiceStateSchema,
		Repository: testRepository, ServiceKey: desired.Service.Key,
		DisplayName: desired.Service.DisplayName,
		Disposition: desired.Service.Disposition, Origin: desired.Service.Origin,
		Successors: []string{}, Incarnation: 1,
		DesiredGeneration:        desiredGeneration,
		DesiredSourceGeneration:  desired.SourceGeneration,
		DesiredCatalogGeneration: desired.CatalogGeneration,
		ActiveDesiredGeneration:  activeGeneration,
		ActiveSourceGeneration:   activeProjection.SourceGeneration,
		ActiveCatalogGeneration:  activeProjection.CatalogGeneration,
		Status:                   status, StateDigest: "", ControlRevision: 3,
		ChangedAt: time.Date(2026, 8, 5, 13, 0, 0, 0, time.UTC),
	}
	if err := servicecatalog.SetServiceStateDigest(&state); err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidateServiceState(state, true); err != nil {
		t.Fatal(err)
	}
	summary := servicecatalog.RepositoryState{
		Schema:                 servicecatalog.RepositoryStateSchema,
		Repository:             testRepository,
		CatalogGeneration:      current.GenerationDigest,
		CatalogControlRevision: current.ControlRevision,
		CatalogServiceCount:    1, LiveServiceCount: 1,
		ControlRevision: 4,
		UpdatedAt:       time.Date(2026, 8, 5, 13, 1, 0, 0, time.UTC),
	}
	if status == servicecatalog.StatusCurrent {
		summary.CurrentCount = 1
	} else {
		summary.StaleCount = 1
	}
	if err := servicecatalog.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidateRepositoryState(summary, true); err != nil {
		t.Fatal(err)
	}
	return state, summary
}

func testSearchManifest(t *testing.T, head string) repositoryindex.SearchManifest {
	t.Helper()
	manifest := repositoryindex.SearchManifest{
		Schema:     repositoryindex.SearchManifestSchema,
		Repository: testRepository,
		Revisions: []store.IndexedRevision{
			{Selector: "HEAD", Branch: "HEAD", Commit: head},
			{Selector: "release", Branch: "refs/tags/v1^{commit}", Commit: strings.Repeat("4", 40)},
		},
		SourceManifestName:     repositoryindex.SourceManifestName(testRepository),
		SourceGenerationDigest: digest("test-source\x00", []byte(head)),
		TopologyPolicy:         repositoryindex.DirectTopologyPolicy,
		PhysicalRoot: repositoryindex.PhysicalRoot{
			Schema:         "phebs-whole-shard-set-v1",
			ManifestName:   "whole.manifest.json",
			ManifestDigest: digest("test-whole\x00", []byte(head)),
			Members: []repositoryindex.PhysicalMember{{
				Ordinal: 0, Count: 1, Name: "test.zoekt",
				ContentDigest:  digest("test-shard\x00", []byte(head)),
				ContentBytes:   1,
				MetadataDigest: digest("test-metadata\x00", []byte(head)),
			}},
		},
	}
	var err error
	manifest.Digest, err = repositoryindex.SearchManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositoryindex.ValidateSearchManifest(manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}
