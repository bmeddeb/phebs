package t421

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/t421catalogprojection"
	"github.com/bmeddeb/phebs/spike/t401"
)

// Supplied typed response fixtures exercise actual decoding, provenance and
// observed hash construction, not a native endpoint or the whole corpus.
func epochQueryProjectionFixture(t *testing.T) (*epochQueryProjectionContext, servicecatalog.Catalog) {
	t.Helper()
	catalog := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityCommitted, ID: "catalog", Version: strings.Repeat("a", 40)}}
	for i := range 101 {
		catalog.Services = append(catalog.Services, servicecatalog.Service{Key: serviceKey(i), DisplayName: serviceKey(i), Disposition: "accepted", Origin: servicecatalog.OriginBase})
		catalog.Memberships = append(catalog.Memberships, independentMemberships(i)...)
	}
	catalog.Unowned = []servicecatalog.UnownedPlacement{{Path: "tools/unowned-0000.go", Origin: "base"}}
	actual, err := t421catalogprojection.Derive(t.Context(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	f := epochFinalResponse{Authority: epochFinalAuthority{Current: true, PhysicalCommit: strings.Repeat("a", 40), SourceGenerationSHA256: testDigest("source"), SearchGenerationSHA256: testDigest("search"), CatalogRootSHA256: testDigest("catalog"), RelationshipGenerationSHA256: testDigest("relationship"), RelationshipRootSHA256: testDigest("relationship-root"), ResolverCatalogGenerationSHA256: testDigest("resolver"), ResolverCatalogRootSHA256: testDigest("resolver-root"), ObservationGenerationSHA256: testDigest("observation")}, Projection: epochFinalProjection{CatalogLogicalSHA256: testDigest("logical"), Catalog: SetIdentity(actual.Catalog), MembershipSet: SetIdentity(actual.Memberships), Placements: SetIdentity(actual.Placements), UnownedPrefixes: SetIdentity(actual.UnownedPrefixes), ServiceQueries: SetIdentity(actual.ServiceQueries)}}
	f.Schema, f.Projection.Schema = "t421-final-authority-source-free-v1", "t421-final-state-projection-source-free-v1"
	// Derive the catalog source namespace through its actual native constructor,
	// independently of the repository source identity used by search/observation.
	generation, err := servicecatalogv3.Build(servicecatalogv3.Binding{Repository: "github.com/t421/query", Authority: catalog.Authority,
		Source: servicecatalogv3.Source{Kind: servicecatalog.SourceCommitted, Path: "/tmp/catalog.json", Commit: catalog.Authority.Version, CensusDigest: testDigest("fixture-census")}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	source, err := servicecatalogv3.SourceGenerationDigest(generation.Root)
	if err != nil || generation.Root.Schema != servicecatalogv3.RootSchemaV2 || source == f.Authority.SourceGenerationSHA256 {
		t.Fatal("catalog/repository source namespaces not distinct")
	}
	f.Authority.CatalogRootSHA256 = generation.Root.Digest
	f.Projection.CatalogLogicalSHA256 = generation.Root.LogicalDigest
	// Namespace publication IDs are distinct from their upstream resolver
	// catalog IDs in F. These namespace IDs are supplied, not native evidence.
	f.QueryAuthority = &epochQueryAuthority{CatalogSourceGenerationSHA256: source,
		ResolverNamespaceGenerationSHA256: testDigest("resolver-namespace-generation"),
		ResolverNamespaceRootSHA256:       testDigest("resolver-namespace-root")}
	bound, err := newEpochQueryProjectionContext(t.Context(), "github.com/t421/query", f, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return bound, catalog
}

func epochQueryMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func epochQuerySignFixture(t *testing.T, value any) string {
	return base64.RawURLEncoding.EncodeToString(epochQueryMarshal(t, value)) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

func epochQuerySearchFixture(t *testing.T, bound *epochQueryProjectionContext, query QueryCase, ordinal uint64) search.Result {
	t.Helper()
	projection, err := bound.queryProjection(query)
	if err != nil {
		t.Fatal(err)
	}
	p := projection.parameters
	f := bound.final.Authority
	value := search.Result{Files: []search.FileResult{}}
	if query.ExpectedRecords != 0 {
		path, content, line := "shared/group-0000/library.go", "FixturePath", 1
		if query.Surface == "all_code_search" {
			profile, err := frozenStructuralProfile()
			if err != nil {
				t.Fatal(err)
			}
			var raw []byte
			path, raw, err = t401.FrozenStructuralGoFixture(profile, ordinal)
			if err != nil {
				t.Fatal(err)
			}
			for i, part := range strings.Split(string(raw), "\n") {
				if strings.Contains(part, "T401Fixture") {
					content, line = part, i+1
					break
				}
			}
		}
		needle := "FixturePath"
		if query.Surface == "all_code_search" {
			needle = "T401Fixture"
		}
		start := strings.Index(content, needle) + 1
		value.Files = []search.FileResult{{Repo: bound.repository, Ref: f.PhysicalCommit, Path: path, Chunks: []search.Chunk{{Content: content, StartLine: line, Ranges: []search.Range{{StartLine: line, EndLine: line, StartCol: start, EndCol: start + len(needle)}}}}}}
		value.Stats.MatchCount, value.Stats.FileCount = 1, 1
	}
	scope := search.ScopeReceipt{Schema: search.ScopeReceiptSchema, Kind: p["scope"], MembershipPolicy: "visible-indexed-repositories-v1", ExpressionDigest: SHA256([]byte("phebs-search-expression-v1\x00" + p["query"])), ResultFiles: len(value.Files), ResultMatches: value.Stats.MatchCount, Revisions: []search.ScopeRevision{}}
	if scope.Kind == "service" {
		scope.Repository, scope.ServiceKey, scope.ServiceStatus, scope.MembershipPolicy = bound.repository, p["service_key"], "current", "accepted-roles-union-shared-included-unowned-excluded-v1"
		a := servicequery.Authority{Schema: servicequery.AuthoritySchema, PredicatePolicy: servicequery.PredicatePolicy, TopologyPolicy: repositoryindex.DirectTopologyPolicy, Repository: bound.repository, ServiceKey: scope.ServiceKey, Status: "current", Incarnation: 1, RevisionSelector: "HEAD", RevisionBranch: "main", RevisionCommit: f.PhysicalCommit, ExpressionDigest: testDigest("expression"), CurrentCatalogGeneration: f.CatalogRootSHA256, ActiveCatalogGeneration: f.CatalogRootSHA256, CatalogControlRevision: 1, ActiveSourceGeneration: bound.final.QueryAuthority.CatalogSourceGenerationSHA256, ActiveDesiredGeneration: testDigest("desired"), ServiceStateDigest: testDigest("state"), ServiceStateRevision: 1, StateSummaryDigest: testDigest("summary"), StateSummaryRevision: 1, RepositorySourceGeneration: f.SourceGenerationSHA256, RepositorySearchGeneration: f.SearchGenerationSHA256, PathDigest: testDigest("paths"), PathCount: 1, PathBytes: 1, PredicateAtoms: 1, PredicateBytes: 20}
		a.Digest = SHA256(append([]byte("phebs-service-query-authority-v1\x00"), epochQueryMarshal(t, a)...))
		if err := servicequery.ValidateAuthority(a); err != nil {
			t.Fatal(err)
		}
		scope.Authority = &a
	}
	type citation struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
		Path       string `json:"path"`
	}
	citations := []citation{}
	for _, file := range value.Files {
		citations = append(citations, citation{file.Repo, file.Ref, file.Path})
		scope.Revisions = append(scope.Revisions, search.ScopeRevision{Repository: file.Repo, Commit: file.Ref})
	}
	scope.ResultSetDigest = SHA256(append([]byte("phebs-search-result-citations-v1\x00"), epochQueryMarshal(t, citations)...))
	scope.Digest = SHA256(append([]byte("phebs-search-scope-v1\x00"), epochQueryMarshal(t, scope)...))
	value.Scope = &scope
	return value
}

func epochQueryServiceFixture(bound *epochQueryProjectionContext) apiresponse.ServiceDetail {
	f := bound.final.Authority
	result := apiresponse.ServiceDetail{SchemaVersion: "phebs-service-detail-v1", Repository: apiresponse.ServiceRepository{Repository: bound.repository, SourceCommit: f.PhysicalCommit, CatalogGeneration: f.CatalogRootSHA256, CatalogDigest: bound.final.Projection.CatalogLogicalSHA256}, Service: apiresponse.Service{Repository: bound.repository, Key: serviceKey(0), Disposition: "accepted", Status: "current", ActiveCatalogGeneration: f.CatalogRootSHA256, ActiveSourceGeneration: bound.final.QueryAuthority.CatalogSourceGenerationSHA256, Incarnation: 1, ControlRevision: 1, MembershipCount: 6, DistinctPathCount: 5}}
	for _, member := range independentMemberships(0) {
		result.Memberships = append(result.Memberships, apiresponse.ServiceMembership{Path: member.Path, Role: member.Role, Origin: member.Origin})
	}
	return result
}

func epochQueryRelationshipFixture(t *testing.T, bound *epochQueryProjectionContext, query QueryCase) []apiresponse.RelationshipPage {
	t.Helper()
	projection, err := bound.queryProjection(query)
	if err != nil {
		t.Fatal(err)
	}
	p, f := projection.parameters, bound.final.Authority
	q := apiresponse.RelationshipQuery{Repositories: []string{bound.repository}, ServiceKey: p["service_key"], View: p["view"], Kind: p["kind"], Plane: p["plane"], LookupKey: p["lookup_key"]}
	root := apiresponse.RelationshipRootReceipt{Repository: bound.repository, State: "complete", RootSchema: relationshippublication.RootSchemaV3, Generation: f.RelationshipGenerationSHA256, RootDigest: f.RelationshipRootSHA256, AuthorityDigest: testDigest("authority"), ServiceKey: q.ServiceKey, ServiceIncarnation: 1, ServiceGeneration: testDigest("desired"), RepositoryComplete: true, AllServicesComplete: true, Authority: &apiresponse.RelationshipAuthority{Repository: bound.repository, CatalogGenerationDigest: f.CatalogRootSHA256, CatalogDigest: bound.final.Projection.CatalogLogicalSHA256, CatalogSourceGeneration: bound.final.QueryAuthority.CatalogSourceGenerationSHA256, ResolverGenerationDigest: bound.final.QueryAuthority.ResolverNamespaceGenerationSHA256, ResolverRootDigest: bound.final.QueryAuthority.ResolverNamespaceRootSHA256, Upstream: &apiresponse.RelationshipUpstreamAuthority{Repository: bound.repository, Observation: apiresponse.RelationshipObservationAuthority{SourceGenerationDigest: f.SourceGenerationSHA256, ObservationGenerationDigest: f.ObservationGenerationSHA256}}}}
	var rows []apiresponse.RelationshipRow
	for index := range int(query.ExpectedRecords) {
		consumer := 1
		switch query.Name {
		case "layered_dag_dependency":
			consumer = 100
		case "bounded_fanout_dependency":
			consumer = 2
		case "chain_callers":
			consumer = index + 1
		case "kafka_producer_topic":
			consumer = 0
		}
		participation := "source"
		if q.View == "callers" {
			participation = "target"
		}
		placement := func(index int, path string) apiresponse.RelationshipPlacement {
			roles := []apiresponse.RelationshipRoleClaim{{Role: "primary", Origin: "base"}}
			if strings.HasPrefix(path, "contracts/") {
				roles = []apiresponse.RelationshipRoleClaim{{Role: "supporting", Origin: "base"}, {Role: "typed", Origin: "base"}}
			}
			return apiresponse.RelationshipPlacement{Path: path, Claims: []apiresponse.RelationshipServiceClaim{{ServiceKey: serviceKey(index), Disposition: "accepted", Roles: roles}}}
		}
		source := placement(consumer, fmt.Sprintf("services/service-%05d/main.go", consumer))
		row := apiresponse.RelationshipRow{Repository: bound.repository, ServiceKey: q.ServiceKey, ServiceIncarnation: 1, ServiceGeneration: root.ServiceGeneration, Kind: q.Kind, Plane: q.Plane, LookupKey: q.LookupKey, Participation: []string{participation}, Source: source, PostingDigest: testDigest(fmt.Sprint("posting", index)), Class: "resolved"}
		row.Evidence = apiresponse.RelationshipEvidence{Kind: q.Kind, Plane: q.Plane, Class: row.Class, Path: source.Path, ObjectID: strings.Repeat("a", 40), ContentDigest: testDigest("content"), Span: apiresponse.RelationshipSpan{StartByte: 1, EndByte: 2, StartLine: 1, EndLine: 1}, SourceRole: "production", PostingDigest: row.PostingDigest}
		if q.Kind == "rpc" {
			target := placement(0, "contracts/service-00000/api.proto")
			row.Target = &target
			row.Evidence.Operation = q.LookupKey
			row.Evidence.DeclarationPath = target.Path
			row.CounterpartServices = []string{serviceKey(0)}
			if participation == "target" {
				row.CounterpartServices = []string{serviceKey(consumer)}
			}
		} else {
			row.Class = "literal"
			row.Evidence.Class = "literal"
			row.Evidence.TopicSpelling = q.LookupKey
		}
		native := relationshippublication.Projection{Schema: relationshippublication.ProjectionSchema, Kind: row.Kind, PostingDigest: row.PostingDigest, Class: row.Class, Plane: row.Plane, LookupKey: row.LookupKey}
		if err := json.Unmarshal(epochQueryMarshal(t, row.Source), &native.Source); err != nil {
			t.Fatal(err)
		}
		if row.Target != nil {
			if err := json.Unmarshal(epochQueryMarshal(t, row.Target), &native.Target); err != nil {
				t.Fatal(err)
			}
		}
		row.ProjectionDigest = SHA256(epochQueryMarshal(t, native))
		row.Citation = epochQuerySignFixture(t, map[string]any{"schema": "phebs-service-relationship-citation-v1", "binding": "actual-fixture-binding", "repository": bound.repository, "source": 0, "projection": row.ProjectionDigest})
		rows = append(rows, row)
	}
	reference := func(row apiresponse.RelationshipRow) string {
		return SHA256(epochQueryMarshal(t, relationshippublication.ServiceReference{Schema: relationshippublication.ServiceReferenceSchema, ProjectionDigest: row.ProjectionDigest, PostingDigest: row.PostingDigest, Kind: row.Kind, Plane: row.Plane, LookupKey: row.LookupKey, Participation: row.Participation}))
	}
	slices.SortFunc(rows, func(a, b apiresponse.RelationshipRow) int { return strings.Compare(reference(a), reference(b)) })
	var pages []apiresponse.RelationshipPage
	for i, row := range rows {
		page := apiresponse.RelationshipPage{SchemaVersion: "phebs-service-relationship-page-v1", Query: q, RowsState: "nonempty", Roots: []apiresponse.RelationshipRootReceipt{root}, Rows: []apiresponse.RelationshipRow{row}, Coverage: apiresponse.RelationshipCoverage{AuthorizedRepositories: 1, CompleteRoots: 1, ScannedReferences: len(rows), ReturnedRows: 1}, Pagination: apiresponse.RelationshipPagination{Order: "repository,reference_digest:asc", PageSize: 1, Returned: 1}}
		if i+1 < len(rows) {
			queryRaw := epochQueryMarshal(t, struct {
				Schema   string                        `json:"schema"`
				Query    apiresponse.RelationshipQuery `json:"query"`
				PageSize int                           `json:"page_size"`
			}{"phebs-service-relationship-page-v1", q, 1})
			page.Pagination.NextCursor = epochQuerySignFixture(t, map[string]any{"schema": "phebs-service-relationship-cursor-v1", "binding": "actual-fixture-binding", "query_digest": SHA256(queryRaw), "offset": i + 1})
		}
		pages = append(pages, page)
	}
	return pages
}

func TestEpochQueryProjectionAllCases(t *testing.T) {
	bound, _ := epochQueryProjectionFixture(t)
	for _, query := range correctedQueryCases() {
		t.Run(query.Name, func(t *testing.T) {
			var bodies [][]byte
			switch {
			case query.ExpectedStatus == 404:
				bodies = [][]byte{[]byte(`{"status":404,"title":"Not Found","detail":"search scope not found"}`)}
			case query.Surface == "service_detail":
				bodies = [][]byte{epochQueryMarshal(t, epochQueryServiceFixture(bound))}
			case query.Surface == "service_relationships":
				for _, page := range epochQueryRelationshipFixture(t, bound, query) {
					bodies = append(bodies, epochQueryMarshal(t, page))
				}
			default:
				bodies = [][]byte{epochQueryMarshal(t, epochQuerySearchFixture(t, bound, query, 0))}
			}
			var results []epochQueryProjectionResult
			for _, transport := range []string{"http", "mcp"} {
				p, err := bound.queryProjection(query)
				if err != nil {
					t.Fatal(err)
				}
				for _, body := range bodies {
					if transport == "http" {
						_, err = p.addHTTP(int(query.ExpectedStatus), body)
					} else {
						if query.ExpectedStatus == 404 {
							body = nil
						}
						_, err = p.addMCP(query.ExpectedMCPCode, body)
					}
					if err != nil {
						t.Fatal(transport, err, string(body))
					}
				}
				got, err := p.finish()
				if err != nil || got.SHA256 != query.ProjectionSHA256 {
					t.Fatal(transport, got, string(got.Projection), query.ProjectionSHA256, err)
				}
				results = append(results, got)
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatal(results)
			}
		})
	}
}

func TestEpochQueryProjectionObservedNotExpected(t *testing.T) {
	bound, _ := epochQueryProjectionFixture(t)
	query := correctedQueryCases()[0]
	p, _ := bound.queryProjection(query)
	body := epochQueryMarshal(t, epochQuerySearchFixture(t, bound, query, 1))
	if _, err := p.addHTTP(200, body); err != nil {
		t.Fatal(err)
	}
	got, err := p.finish()
	if err != nil || got.SHA256 == query.ProjectionSHA256 || !strings.Contains(string(got.Projection), `"returned_ordinals":[1]`) {
		t.Fatal(got, err)
	}
}

func TestEpochQueryProjectionSourceNamespaces(t *testing.T) {
	bound, catalog := epochQueryProjectionFixture(t)
	for _, name := range []string{"first_service", "shared_placement_service_scope", "chain_dependency"} {
		for _, mode := range []string{"native_catalog_source", "physical_source", "changed_catalog_source", "physical_field_changed"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				var query QueryCase
				for _, candidate := range correctedQueryCases() {
					if candidate.Name == name {
						query = candidate
					}
				}
				source := bound.final.QueryAuthority.CatalogSourceGenerationSHA256
				switch mode {
				case "physical_source":
					source = bound.final.Authority.SourceGenerationSHA256
				case "changed_catalog_source":
					source = testDigest("wrong-catalog-source")
				}
				var body []byte
				switch query.Surface {
				case "service_detail":
					value := epochQueryServiceFixture(bound)
					value.Service.ActiveSourceGeneration = source
					if mode == "physical_field_changed" {
						value.Repository.SourceCommit = strings.Repeat("b", 40)
					}
					body = epochQueryMarshal(t, value)
				case "service_search":
					value := epochQuerySearchFixture(t, bound, query, 0)
					value.Scope.Authority.ActiveSourceGeneration = source
					if mode == "physical_field_changed" {
						value.Scope.Authority.RepositorySourceGeneration = source
					}
					value.Scope.Authority.Digest = ""
					value.Scope.Authority.Digest = SHA256(append([]byte("phebs-service-query-authority-v1\x00"), epochQueryMarshal(t, value.Scope.Authority)...))
					value.Scope.Digest = ""
					value.Scope.Digest = SHA256(append([]byte("phebs-search-scope-v1\x00"), epochQueryMarshal(t, value.Scope)...))
					body = epochQueryMarshal(t, value)
				case "service_relationships":
					value := epochQueryRelationshipFixture(t, bound, query)[0]
					value.Roots[0].Authority.CatalogSourceGeneration = source
					if mode == "physical_field_changed" {
						value.Roots[0].Authority.Upstream.Observation.SourceGenerationDigest = source
					}
					body = epochQueryMarshal(t, value)
				}
				projection, err := bound.queryProjection(query)
				if err != nil {
					t.Fatal(err)
				}
				_, err = projection.addHTTP(200, body)
				if (err == nil) != (mode == "native_catalog_source") {
					t.Fatal("catalog/repository source namespaces conflated", err)
				}
			})
		}
	}
	for _, authority := range []*epochQueryAuthority{nil, {}, {CatalogSourceGenerationSHA256: "malformed"}} {
		final := bound.final
		final.QueryAuthority = authority
		if _, err := newEpochQueryProjectionContext(t.Context(), bound.repository, final, catalog); err == nil {
			t.Fatal("missing/malformed actual F query authority accepted")
		}
	}
	final := bound.final
	copyAuthority := *final.QueryAuthority
	final.QueryAuthority = &copyAuthority
	retained, err := newEpochQueryProjectionContext(t.Context(), bound.repository, final, catalog)
	if err != nil {
		t.Fatal(err)
	}
	copyAuthority.CatalogSourceGenerationSHA256 = testDigest("later-mutation")
	if retained.final.QueryAuthority.CatalogSourceGenerationSHA256 != bound.final.QueryAuthority.CatalogSourceGenerationSHA256 {
		t.Fatal("returned caller alias changed retained F authority")
	}
}

func TestEpochQueryProjectionPrivateRefusal(t *testing.T) {
	bound, _ := epochQueryProjectionFixture(t)
	var query QueryCase
	for _, candidate := range correctedQueryCases() {
		if candidate.Name == "chain_dependency" {
			query = candidate
		}
	}
	for _, test := range []struct {
		clause string
		mutate func(*apiresponse.RelationshipPage)
	}{
		{"page_envelope", func(p *apiresponse.RelationshipPage) { p.Coverage.ScannedReferences++ }},
		{"root_authority", func(p *apiresponse.RelationshipPage) { p.Roots[0].RootDigest = testDigest("changed") }},
		{"row_evidence", func(p *apiresponse.RelationshipPage) { p.Rows[0].Evidence.Span.EndByte = 0 }},
		{"placement", func(p *apiresponse.RelationshipPage) { p.Rows[0].Source.Claims[0].Roles[0].Origin = "override" }},
		{"projection_digest", func(p *apiresponse.RelationshipPage) { p.Rows[0].ProjectionDigest = testDigest("changed") }},
		{"citation", func(p *apiresponse.RelationshipPage) { p.Rows[0].Citation = "private-input-must-not-escape" }},
		{"rpc_semantic", func(p *apiresponse.RelationshipPage) { p.Rows[0].Evidence.Operation = "private-input-must-not-escape" }},
	} {
		t.Run(test.clause, func(t *testing.T) {
			page := epochQueryRelationshipFixture(t, bound, query)[0]
			test.mutate(&page)
			projection, err := bound.queryProjection(query)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := projection.addHTTP(200, epochQueryMarshal(t, page)); err != ErrExecutionEpochOne || projection.refusal != test.clause {
				t.Fatal("private refusal missing or public error changed", projection.refusal, err)
			}
			if _, err := projection.addHTTP(200, []byte("private-input-must-not-escape")); err != ErrExecutionEpochOne || projection.refusal != test.clause {
				t.Fatal("first refusal not retained", projection.refusal, err)
			}
		})
	}
	for _, clause := range []string{"transport_code", "transport_status", "page_sequence", "page_count_cursor"} {
		t.Run(clause, func(t *testing.T) {
			for _, candidate := range correctedQueryCases() {
				if candidate.Name == "chain_callers" {
					query = candidate
				}
			}
			projection, err := bound.queryProjection(query)
			if err != nil {
				t.Fatal(err)
			}
			switch clause {
			case "transport_code":
				_, err = projection.addMCP("wrong", nil)
			case "transport_status":
				_, err = projection.addHTTP(500, nil)
			case "page_sequence":
				projection.pages = query.ExpectedRecords
				_, err = projection.addHTTP(200, nil)
			case "page_count_cursor":
				page := epochQueryRelationshipFixture(t, bound, query)[0]
				page.Pagination.NextCursor = ""
				_, err = projection.addHTTP(200, epochQueryMarshal(t, page))
			}
			if err != ErrExecutionEpochOne || projection.refusal != clause {
				t.Fatal("boundary refusal missing", projection.refusal, err)
			}
		})
	}
}

func TestEpochQueryProjectionResolverNamespaces(t *testing.T) {
	bound, catalog := epochQueryProjectionFixture(t)
	for _, query := range correctedQueryCases() {
		if query.Surface != "service_relationships" {
			continue
		}
		for _, mode := range []string{"namespace", "catalog_generation", "catalog_manifest", "changed_generation", "changed_root", "missing_generation", "missing_root"} {
			t.Run(query.Name+"/"+mode, func(t *testing.T) {
				page := epochQueryRelationshipFixture(t, bound, query)[0]
				final := bound.final
				proof := *final.QueryAuthority
				final.QueryAuthority = &proof
				switch mode {
				case "catalog_generation":
					page.Roots[0].Authority.ResolverGenerationDigest = final.Authority.ResolverCatalogGenerationSHA256
				case "catalog_manifest":
					page.Roots[0].Authority.ResolverRootDigest = final.Authority.ResolverCatalogRootSHA256
				case "changed_generation":
					proof.ResolverNamespaceGenerationSHA256 = testDigest("different-namespace")
				case "changed_root":
					proof.ResolverNamespaceRootSHA256 = testDigest("different-namespace")
				case "missing_generation":
					proof.ResolverNamespaceGenerationSHA256 = ""
				case "missing_root":
					proof.ResolverNamespaceRootSHA256 = ""
				}
				context, err := newEpochQueryProjectionContext(t.Context(), bound.repository, final, catalog)
				if strings.HasPrefix(mode, "missing_") {
					if err == nil {
						t.Fatal("missing namespace proof accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				projection, err := context.queryProjection(query)
				if err != nil {
					t.Fatal(err)
				}
				_, err = projection.addHTTP(200, epochQueryMarshal(t, page))
				if (err == nil) != (mode == "namespace") {
					t.Fatal("resolver namespace and upstream catalog identities conflated", err)
				}
			})
		}
	}
}

func TestEpochQueryProjectionCatalogBinding(t *testing.T) {
	bound, catalog := epochQueryProjectionFixture(t)
	catalog.Memberships[0].Role = "shared"
	if _, err := newEpochQueryProjectionContext(t.Context(), bound.repository, bound.final, catalog); err == nil {
		t.Fatal("changed actual catalog accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := newEpochQueryProjectionContext(ctx, bound.repository, bound.final, catalog); err == nil {
		t.Fatal("canceled context")
	}
}

func TestEpochQueryProjectionRefusals(t *testing.T) {
	bound, _ := epochQueryProjectionFixture(t)
	queries := correctedQueryCases()
	for _, body := range []string{`{"status":404,"title":"Not Found","detail":"different missing resource"}`, `{"status":404,"title":"Not Found"}`} {
		p, _ := bound.queryProjection(queries[1])
		if _, err := p.addHTTP(404, []byte(body)); err == nil {
			t.Fatal("unclassified denial projected")
		}
	}
	searchQuery := queries[0]
	for _, test := range []struct {
		name   string
		mutate func(*search.Result)
	}{
		{"wrong_repository", func(v *search.Result) { v.Files[0].Repo = "github.com/other/repo" }},
		{"wrong_commit", func(v *search.Result) { v.Files[0].Ref = strings.Repeat("b", 40) }},
		{"wrong_scope_digest", func(v *search.Result) { v.Scope.Digest = testDigest("changed") }},
		{"bad_count", func(v *search.Result) { v.Stats.MatchCount = 2 }},
		{"bad_span", func(v *search.Result) { v.Files[0].Chunks[0].Ranges[0].EndCol = 9999 }},
		{"changed_content", func(v *search.Result) { v.Files[0].Chunks[0].Content = "unrelated" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := epochQuerySearchFixture(t, bound, searchQuery, 0)
			test.mutate(&v)
			p, _ := bound.queryProjection(searchQuery)
			if _, err := p.addHTTP(200, epochQueryMarshal(t, v)); err == nil {
				t.Fatal("accepted")
			}
			if _, err := p.finish(); err == nil {
				t.Fatal("failed page projected")
			}
			if _, err := p.addHTTP(200, epochQueryMarshal(t, epochQuerySearchFixture(t, bound, searchQuery, 0))); err == nil {
				t.Fatal("failure not sticky")
			}
		})
	}
	chain := queries[slices.IndexFunc(queries, func(q QueryCase) bool { return q.Name == "chain_callers" })]
	for _, test := range []struct {
		name   string
		mutate func(*apiresponse.RelationshipPage)
	}{
		{"root_generation", func(p *apiresponse.RelationshipPage) { p.Roots[0].Generation = testDigest("changed") }},
		{"root_schema", func(p *apiresponse.RelationshipPage) { p.Roots[0].RootSchema = relationshippublication.RootSchema }},
		{"root_catalog", func(p *apiresponse.RelationshipPage) {
			p.Roots[0].Authority.CatalogGenerationDigest = testDigest("changed")
		}},
		{"row_incarnation", func(p *apiresponse.RelationshipPage) { p.Rows[0].ServiceIncarnation++ }},
		{"projection_digest", func(p *apiresponse.RelationshipPage) { p.Rows[0].ProjectionDigest = testDigest("changed") }},
		{"posting_digest", func(p *apiresponse.RelationshipPage) { p.Rows[0].Evidence.PostingDigest = testDigest("changed") }},
		{"bad_claim", func(p *apiresponse.RelationshipPage) { p.Rows[0].Source.Claims[0].Roles[0].Origin = "override" }},
		{"citation_projection", func(p *apiresponse.RelationshipPage) {
			p.Rows[0].Citation = epochQuerySignFixture(t, map[string]any{"schema": "phebs-service-relationship-citation-v1", "binding": "actual-fixture-binding", "repository": bound.repository, "source": 0, "projection": testDigest("changed")})
		}},
		{"cursor_query", func(p *apiresponse.RelationshipPage) {
			p.Pagination.NextCursor = epochQuerySignFixture(t, map[string]any{"schema": "phebs-service-relationship-cursor-v1", "binding": "actual-fixture-binding", "query_digest": testDigest("changed"), "offset": 1})
		}},
		{"early_empty_cursor", func(p *apiresponse.RelationshipPage) { p.Pagination.NextCursor = "" }},
		{"wrong_query", func(p *apiresponse.RelationshipPage) { p.Query.View = "dependencies" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := epochQueryRelationshipFixture(t, bound, chain)[0]
			test.mutate(&v)
			p, _ := bound.queryProjection(chain)
			if _, err := p.addMCP("ok", epochQueryMarshal(t, v)); err == nil {
				t.Fatal("accepted")
			}
			if _, err := p.finish(); err == nil {
				t.Fatal("failed page projected")
			}
		})
	}
	for _, name := range []string{"duplicate_reference", "changed_later_root", "late_missing_page"} {
		t.Run(name, func(t *testing.T) {
			pages := epochQueryRelationshipFixture(t, bound, chain)
			p, _ := bound.queryProjection(chain)
			if _, err := p.addHTTP(200, epochQueryMarshal(t, pages[0])); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "duplicate_reference":
				pages[1].Rows = pages[0].Rows
			case "changed_later_root":
				pages[1].Roots[0].AuthorityDigest = testDigest("changed")
			case "late_missing_page":
				if _, err := p.finish(); err == nil {
					t.Fatal("incomplete projected")
				}
				return
			}
			if _, err := p.addHTTP(200, epochQueryMarshal(t, pages[1])); err == nil {
				t.Fatal("bad later page")
			}
			if p.records != 1 {
				t.Fatal("accepted prefix lost", p.records)
			}
		})
	}
}

func TestEpochQueryProjectionHumaAndDetachedResults(t *testing.T) {
	bound, _ := epochQueryProjectionFixture(t)
	query := correctedQueryCases()[2]
	body := epochQueryMarshal(t, epochQueryServiceFixture(bound))
	body = append([]byte(`{"$schema":"http://127.0.0.1/schemas/ServiceDetail.json",`), body[1:]...)
	p, _ := bound.queryProjection(query)
	if _, err := p.addHTTP(200, body); err != nil {
		t.Fatal(err)
	}
	first, err := p.finish()
	if err != nil {
		t.Fatal(err)
	}
	first.Projection[0] = '!'
	second, err := p.finish()
	if err != nil || second.Projection[0] != '{' || second.SHA256 != query.ProjectionSHA256 {
		t.Fatal(second, err)
	}
	for _, suffix := range []string{`{}`, ` null`} {
		q, _ := bound.queryProjection(query)
		if _, err := q.addHTTP(200, append(slices.Clone(body), []byte(suffix)...)); err == nil {
			t.Fatal("trailing JSON")
		}
	}
	unknown := append([]byte(`{"unexpected":true,`), body[1:]...)
	q, _ := bound.queryProjection(query)
	if _, err := q.addHTTP(200, unknown); err == nil {
		t.Fatal("unknown field")
	}
}
