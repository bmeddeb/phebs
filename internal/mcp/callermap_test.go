package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/store"
)

type schemaContractCatalogQueries struct{}

func (schemaContractCatalogQueries) List(
	context.Context,
	api.ContractCatalogQuery,
	int,
	string,
) (*api.ContractCatalogList, error) {
	return nil, errors.New("not called")
}

func (schemaContractCatalogQueries) OperationForProtocol(
	context.Context,
	string,
	string,
	string,
	string,
) (*api.ContractCatalogOperation, error) {
	return nil, errors.New("not called")
}

type schemaCallerMapQueries struct{}

func (schemaCallerMapQueries) List(
	context.Context,
	api.CallerMapQuery,
	int,
	string,
) (*api.CallerMapPage, error) {
	return nil, errors.New("not called")
}

// exactEnvelopeCallerMapQueries keeps the older evidence fixture useful for
// transport pagination tests while pinning T30.6j's mandatory exact envelope.
// Production registration supplies the exact publication-backed service.
type exactEnvelopeCallerMapQueries struct {
	legacy *api.CallerMapService
}

func (service exactEnvelopeCallerMapQueries) List(
	ctx context.Context,
	query api.CallerMapQuery,
	pageSize int,
	cursor string,
) (*api.CallerMapPage, error) {
	page, err := service.legacy.List(ctx, query, pageSize, cursor)
	if err != nil {
		return nil, err
	}
	page.Generation = &api.CallerMapGeneration{
		State: "current", Plane: "repository-overlay",
		Repository: query.Endpoint.Repository, Commit: callerToolCommit,
		RecordCounts: &api.CallerMapRecordCounts{
			CandidateRecords: 6, BaseRecords: 5, ExcludedGoTestRecords: 1,
		},
		PartitionProgress: &api.CallerMapPartitionProgress{
			State: "complete", SettledPairCount: 1,
			SucceededPairCount: 1, TotalPairCount: callerMapInt(1),
		},
	}
	page.Generation.ExcludedGoTestRecords = 1
	page.Scope = &api.AnalysisScopeProjection{
		Repository: query.Endpoint.Repository, Commit: callerToolCommit,
		ScopePosture: "whole-repository",
	}
	page.MatchingRowsState = "exact"
	return page, nil
}

func callerMapInt(value int) *int { return &value }

type schemaExactCallerMapQueries struct {
	schemaCallerMapQueries
	wantToken string
	result    api.CallerMapCitation
	called    bool
}

func (service *schemaExactCallerMapQueries) CitationAvailable() bool { return true }

func (service *schemaExactCallerMapQueries) ReadCitation(
	_ context.Context,
	token string,
) (*api.CallerMapCitation, error) {
	if token != service.wantToken {
		return nil, errors.New("citation token differs")
	}
	service.called = true
	result := service.result
	return &result, nil
}

type schemaCallerComparisonQueries struct{}

func (schemaCallerComparisonQueries) Compare(
	context.Context,
	api.CallerComparisonQuery,
	int,
	string,
) (*api.CallerComparisonPage, error) {
	return nil, errors.New("not called")
}

// exactEnvelopeCallerComparisonQueries keeps the older evidence fixture useful
// for transport pagination tests while pinning T30.6k's mandatory exact
// envelope. Production registration supplies the exact publication-backed
// service; exact publication and citation behavior is covered in the API tests.
type exactEnvelopeCallerComparisonQueries struct {
	legacy *api.CallerComparisonService
}

func (service exactEnvelopeCallerComparisonQueries) Compare(
	ctx context.Context,
	query api.CallerComparisonQuery,
	pageSize int,
	cursor string,
) (*api.CallerComparisonPage, error) {
	page, err := service.legacy.Compare(ctx, query, pageSize, cursor)
	if err != nil {
		return nil, err
	}
	page.SchemaVersion = "caller-comparison-v2"
	page.MatchingRowsState = "exact"
	page.Coverage = nil
	for _, snapshot := range []*api.CallerComparisonSnapshot{
		&page.Old,
		&page.Replacement,
	} {
		snapshot.Generation = &api.CallerMapGeneration{
			State: "current", Plane: "repository-overlay",
			Repository: snapshot.Endpoint.Repository, Commit: callerToolCommit,
		}
		snapshot.MatchingRowsState = "exact"
		snapshot.CoverageDigest = ""
		snapshot.AttributionDigest = ""
	}
	return page, nil
}

func TestCallerMapToolSchemasAndDarkRegistration(t *testing.T) {
	schemaDigests := map[string]string{
		"search_contract_operations": "sha256:e2b2b80c7ebb5eeece8c6179b0e21a1b5676dee1ec3a481487f1984c93fbefc2",
		"get_contract_operation":     "sha256:3a8bfc0a42ac27ffbfbd3e546892924a6cd8ec4ef6ab1fe7bb44a95ae4881af9",
		"list_operation_callers":     "sha256:12032f56828d67e4e7546b4302c38e6f793a4e2f2b839f03450de51b6c2d5931",
		"compare_operation_callers":  "sha256:a6df9c83577b74080b819b88ee6c271fa7a3277cd16324cd8e025542a6edc22d",
	}
	for _, test := range []struct {
		name          string
		catalog       ContractCatalogQueries
		callerMap     CallerMapQueries
		comparison    CallerComparisonQueries
		proofs        ProofQueries
		compatibility CompatibilityQueries
		wantCount     int
	}{
		{name: "dark", wantCount: 10},
		{
			name: "catalog alone stays dark", catalog: schemaContractCatalogQueries{},
			wantCount: 10,
		},
		{
			name: "caller map alone stays dark", callerMap: schemaCallerMapQueries{},
			wantCount: 10,
		},
		{
			name: "caller map annex", catalog: schemaContractCatalogQueries{},
			callerMap: schemaCallerMapQueries{}, wantCount: 13,
		},
		{
			name:       "comparison requires the complete annex",
			catalog:    schemaContractCatalogQueries{},
			callerMap:  schemaCallerMapQueries{},
			comparison: schemaCallerComparisonQueries{}, wantCount: 14,
		},
		{
			name:      "caller pack without compatibility",
			catalog:   schemaContractCatalogQueries{},
			callerMap: schemaCallerMapQueries{}, proofs: schemaProofQueries{},
			comparison: schemaCallerComparisonQueries{}, wantCount: 19,
		},
		{
			name: "all experimental tools", catalog: schemaContractCatalogQueries{},
			callerMap: schemaCallerMapQueries{}, proofs: schemaProofQueries{},
			comparison:    schemaCallerComparisonQueries{},
			compatibility: schemaCompatibilityQueries{}, wantCount: 20,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(Options{
				Version: "test", ContractCatalog: test.catalog,
				CallerMap: test.callerMap, Proofs: test.proofs,
				CallerComparison: test.comparison,
				Compatibility:    test.compatibility,
			})
			serverTransport, clientTransport := sdk.NewInMemoryTransports()
			go func() {
				_, _ = server.Connect(t.Context(), serverTransport, nil)
			}()
			client := sdk.NewClient(
				&sdk.Implementation{Name: "t20.11-schema", Version: "1"},
				nil,
			)
			session, err := client.Connect(t.Context(), clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			listed, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			found := make(map[string]*sdk.Tool, len(listed.Tools))
			for _, tool := range listed.Tools {
				found[tool.Name] = tool
			}
			if len(found) != test.wantCount {
				t.Fatalf(
					"tool count = %d, want %d: %v",
					len(found),
					test.wantCount,
					found,
				)
			}
			enabled := test.catalog != nil && test.callerMap != nil
			for _, name := range []string{
				"search_contract_operations",
				"get_contract_operation",
				"list_operation_callers",
				"compare_operation_callers",
			} {
				tool, ok := found[name]
				wantEnabled := enabled
				if name == "compare_operation_callers" {
					wantEnabled = enabled && test.comparison != nil
				}
				if ok != wantEnabled {
					t.Fatalf("%s discovery = %v, enabled=%v", name, ok, wantEnabled)
				}
				if !ok {
					continue
				}
				input, _ := json.Marshal(tool.InputSchema)
				output, _ := json.Marshal(tool.OutputSchema)
				schemaBytes := append(append(input, '\n'), output...)
				digest := sha256.Sum256(schemaBytes)
				gotDigest := "sha256:" + hex.EncodeToString(digest[:])
				if gotDigest != schemaDigests[name] {
					t.Fatalf(
						"%s input/output schema digest = %s, want %s",
						name,
						gotDigest,
						schemaDigests[name],
					)
				}
				switch name {
				case "search_contract_operations":
					if !strings.Contains(string(input), `"page_size"`) ||
						!strings.Contains(string(input), `"cursor"`) ||
						!strings.Contains(string(output), `"items"`) ||
						!strings.Contains(string(output), `"coverage_digest"`) {
						t.Fatalf("%s schemas: input=%s output=%s", name, input, output)
					}
				case "get_contract_operation":
					for _, field := range []string{
						`"protocol"`, `"repository"`,
						`"declaration_lineage"`, `"operation"`,
					} {
						if !strings.Contains(string(input), field) {
							t.Fatalf("%s input omitted %s: %s", name, field, input)
						}
					}
					if !strings.Contains(string(output), `"endpoint"`) ||
						!strings.Contains(string(output), `"detail"`) {
						t.Fatalf("%s output schema: %s", name, output)
					}
				case "list_operation_callers":
					for _, field := range []string{
						`"protocol"`, `"repository"`, `"declaration_lineage"`,
						`"operation"`, `"unit"`, `"owner"`, `"path_prefix"`,
						`"code_role"`, `"tier"`, `"freshness"`,
						`"resolution"`, `"ordering"`, `"page_size"`, `"cursor"`,
					} {
						if !strings.Contains(string(input), field) {
							t.Fatalf("%s input omitted %s: %s", name, field, input)
						}
					}
					for _, field := range []string{
						`"rows"`, `"pagination"`, `"coverage_digest"`,
						`"attribution_digest"`, `"generation"`,
						`"record_counts"`, `"partition_progress"`, `"scope"`,
						`"matching_rows_state"`, `"caveat"`,
					} {
						if !strings.Contains(string(output), field) {
							t.Fatalf("%s output omitted %s: %s", name, field, output)
						}
					}
				case "compare_operation_callers":
					for _, field := range []string{
						`"old_protocol"`, `"old_repository"`,
						`"old_declaration_lineage"`, `"old_operation"`,
						`"replacement_protocol"`, `"replacement_repository"`,
						`"replacement_declaration_lineage"`,
						`"replacement_operation"`, `"classification"`,
						`"level"`, `"page_size"`, `"cursor"`,
					} {
						if !strings.Contains(string(input), field) {
							t.Fatalf("%s input omitted %s: %s", name, field, input)
						}
					}
					for _, field := range []string{
						`"old"`, `"replacement"`, `"rows"`, `"pagination"`,
						`"total_rows"`, `"matching_rows_state"`, `"caveat"`,
					} {
						if !strings.Contains(string(output), field) {
							t.Fatalf("%s output omitted %s: %s", name, field, output)
						}
					}
					if strings.Contains(string(output), `"coverage"`) {
						t.Fatalf("%s output retained legacy coverage: %s", name, output)
					}
				}
			}
			if enabled {
				if _, ok := found["find_operation_consumers"]; ok != (test.proofs != nil) {
					t.Fatal("Caller Map annex changed the proof tool gate")
				}
				if _, ok := found["get_extraction_coverage"]; ok != (test.proofs != nil) {
					t.Fatal("Caller Map annex duplicated or changed the coverage tool gate")
				}
			}
		})
	}
}

func TestCallerMapMCPAcceptsProductionExactGapEnvelope(t *testing.T) {
	const repository = callerToolContractA
	unit, err := (analysisunit.Scope{
		Repository: repository, Name: "orders-service",
		Primary: []string{"idl/orders.proto"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	// A committed empty list and a decoded JSON null have the same semantic
	// identity. Exercise the latter so MCP's schema cannot reject a state the
	// product reader accepts.
	unit.SupportingPaths = nil
	evidence := &callerToolStore{
		repos: []store.Repo{{
			Name: repository, IndexedCommitHash: callerToolCommit,
			IndexedAnalysisUnit: unit,
		}},
		runs:        make(map[string]store.ExtractionRun),
		assertions:  make(map[string][]store.Assertion),
		resolutions: make(map[string]store.EvidenceResolution),
	}
	registry, err := callerexecute.NewRegistry(
		[]extract.Extractor{gocaller.NewGRPC()},
	)
	if err != nil {
		t.Fatal(err)
	}
	publications := callerpublication.NewRegistry(t.TempDir())
	t.Cleanup(func() { _ = publications.Close() })
	reader, err := callerexecute.NewPublicationReader(
		t.TempDir(),
		&exactGapCallerReadStore{callerToolStore: evidence},
		registry,
		publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	service := api.NewCallerMapService(api.Options{
		Store: evidence, Evidence: evidence, CallerMapEnabled: true,
		CallerReader: reader, DataDir: t.TempDir(),
		Principal:             func(context.Context) string { return "user:agent" },
		AuthorizationProvider: "t30.7-mcp-parity-v1",
	})
	if service == nil {
		t.Fatal("exact Caller Map service is unavailable")
	}

	server := NewServer(Options{
		Version: "test", ContractCatalog: schemaContractCatalogQueries{},
		CallerMap: service,
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _, _ = server.Connect(t.Context(), serverTransport, nil) }()
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t30.7-mcp-parity", Version: "1"}, nil,
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	page, result := callToolSession[api.CallerMapPage](
		t,
		session,
		"list_operation_callers",
		map[string]any{
			"protocol": "protobuf", "repository": repository,
			"declaration_lineage": callerToolLineageA,
			"operation":           callerToolOperation,
		},
	)
	if result.IsError {
		t.Fatalf("exact Caller Map gap failed MCP output validation: %s", textContent(t, result))
	}
	if page.Scope == nil || page.Scope.Repository != repository ||
		page.Scope.ScopePosture != analysisunit.SearchIndexFocused ||
		page.Scope.AnalysisUnit == nil ||
		page.Scope.AnalysisUnit.SupportingPaths != nil ||
		page.Generation == nil || page.Generation.Repository != repository ||
		page.Generation.PartitionProgress == nil ||
		page.Generation.PartitionProgress.State != "unavailable" ||
		page.MatchingRowsState != "unavailable" {
		t.Fatalf("exact Caller Map gap lost production scope/progress: %+v", page)
	}
}

func TestExactCallerCitationToolRegistrationAndParity(t *testing.T) {
	want := api.CallerMapCitation{
		SchemaVersion: "caller-map-citation-v1",
		Generation: api.CallerMapGeneration{
			State: "current", Plane: "repository-overlay",
			Repository: "github.com/acme/orders", Commit: strings.Repeat("a", 40),
			GenerationDigest:    "sha256:" + strings.Repeat("b", 64),
			PublicationRevision: 7,
		},
		Source: api.CallerMapSource{
			Repository: "github.com/acme/orders", Commit: strings.Repeat("a", 40),
			Path: "src/client.go", ObjectID: strings.Repeat("c", 40),
			BlobDigest: "sha256:" + strings.Repeat("d", 64),
			Plane:      "repository-overlay", StartByte: 10, EndByte: 16,
			StartLine: 2, EndLine: 2, AssertionID: "record-1",
			RunID: "generation-1", AtomID: "atom-1",
		},
		Content: "Call()",
	}
	callers := &schemaExactCallerMapQueries{
		wantToken: "opaque-exact-citation", result: want,
	}
	server := NewServer(Options{
		Version: "test", ContractCatalog: schemaContractCatalogQueries{},
		CallerMap: callers,
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _, _ = server.Connect(t.Context(), serverTransport, nil) }()
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t30.6j-citation", Version: "1"}, nil,
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name == "read_operation_caller_citation" {
			found = true
			input, _ := json.Marshal(tool.InputSchema)
			encoded, _ := json.Marshal(tool.OutputSchema)
			schemaBytes := append(append(input, '\n'), encoded...)
			digest := sha256.Sum256(schemaBytes)
			gotDigest := "sha256:" + hex.EncodeToString(digest[:])
			const wantDigest = "sha256:347a381058e52c07c1e95c2558a4b4435cd577048f03e8e2c4dd74f273ee3f87"
			if gotDigest != wantDigest {
				t.Fatalf("citation input/output schema digest = %s, want %s", gotDigest, wantDigest)
			}
			if !strings.Contains(string(encoded), `"content"`) ||
				!strings.Contains(string(encoded), `"generation"`) ||
				!strings.Contains(string(encoded), `"source"`) {
				t.Fatalf("citation output schema = %s", encoded)
			}
		}
	}
	if !found {
		t.Fatal("exact citation tool is not registered")
	}
	got, result := callToolSession[api.CallerMapCitation](
		t, session, "read_operation_caller_citation",
		map[string]any{"citation": callers.wantToken},
	)
	if result.IsError || !callers.called {
		t.Fatalf("citation call = %+v, called=%v", result, callers.called)
	}
	assertSameJSON(t, got, want)
}

const (
	callerToolContractA = "example.com/contracts/orders"
	callerToolContractB = "example.com/contracts/archive"
	callerToolSource    = "example.com/services/checkout"
	callerToolHidden    = "example.com/services/secret"
	callerToolOperation = "/acme.orders.v1.Orders/Get"
	callerToolLineageA  = "provisional_repo_path_v1_" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	callerToolLineageB = "provisional_repo_path_v1_" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	callerToolAttribution = "cccccccccccccccccccccccccccccccc" +
		"cccccccccccccccccccccccccccccccc"
	callerToolCommit = "dddddddddddddddddddddddddddddddddddddddd"
)

type callerToolStore struct {
	store.Store
	store.EvidenceStore

	repos       []store.Repo
	runs        map[string]store.ExtractionRun
	assertions  map[string][]store.Assertion
	resolutions map[string]store.EvidenceResolution
	calls       []string
	writes      int
}

// exactGapCallerReadStore is the smallest real PublicationReader authority
// that can prove the production Caller Map gap envelope through MCP. The
// product reader, rather than a transport fixture, supplies scope and bounded
// partition progress on this path.
type exactGapCallerReadStore struct {
	*callerToolStore
}

func (*exactGapCallerReadStore) GetCandidateManifestPublication(
	context.Context,
	string,
) (*store.CandidateManifestPublication, error) {
	return nil, store.ErrNotFound
}

func (*exactGapCallerReadStore) GetResolverCatalogPublication(
	context.Context,
	string,
) (*store.ResolverCatalogPublication, error) {
	return nil, store.ErrNotFound
}

func (*exactGapCallerReadStore) ResolverCatalogPublicationCurrent(
	context.Context,
	store.ResolverCatalogPublication,
) (bool, error) {
	return false, nil
}

func (*exactGapCallerReadStore) GetCallerGenerationPublication(
	context.Context,
	string,
) (*store.CallerGenerationPublication, error) {
	return nil, store.ErrNotFound
}

func (*exactGapCallerReadStore) GetCallerGenerationPublicationSummary(
	context.Context,
	string,
) (*store.CallerGenerationPublicationSummary, error) {
	return nil, store.ErrNotFound
}

func (*exactGapCallerReadStore) CallerGenerationPublicationSummaryCurrent(
	context.Context,
	store.CallerGenerationPublicationSummary,
) (bool, error) {
	return false, nil
}

func (*exactGapCallerReadStore) CallerGenerationPublicationSummaryAuthorityCurrent(
	context.Context,
	store.CallerGenerationPublicationSummary,
) (bool, error) {
	return false, nil
}

func (*exactGapCallerReadStore) CallerGenerationPublicationSummariesAuthorityCurrent(
	context.Context,
	[]store.CallerGenerationPublicationSummary,
) (bool, error) {
	return false, nil
}

func (*exactGapCallerReadStore) GetCallerGenerationAdmission(
	context.Context,
	store.CallerGenerationIdentity,
) (*store.CallerGenerationAdmission, error) {
	return nil, store.ErrNotFound
}

func (*exactGapCallerReadStore) GetCallerLeafOutcomeProgress(
	context.Context,
	store.CallerGenerationIdentity,
) (store.CallerLeafOutcomeProgress, error) {
	return store.CallerLeafOutcomeProgress{}, nil
}

func (s *callerToolStore) LatestExtractionDomainOutcome(
	context.Context,
	store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	return nil, store.ErrNotFound
}

func callerToolScope(repo, domain string) string {
	return repo + "\x00" + domain
}

func callerToolEvidenceScope(repo, runID, atomID string) string {
	return repo + "\x00" + runID + "\x00" + atomID
}

func (s *callerToolStore) ListRepos(context.Context) ([]store.Repo, error) {
	s.calls = append(s.calls, "list-repos")
	return append([]store.Repo(nil), s.repos...), nil
}

func (s *callerToolStore) GetRepo(
	_ context.Context,
	repository string,
) (*store.Repo, error) {
	for _, repo := range s.repos {
		if repo.Name == repository {
			copyOfRepo := repo
			return &copyOfRepo, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *callerToolStore) LatestPublishedRun(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionRun, error) {
	s.calls = append(s.calls, "run:"+callerToolScope(scope.Repository, scope.Domain))
	run, ok := s.runs[callerToolScope(scope.Repository, scope.Domain)]
	if !ok || run.Commit != scope.Commit || run.UnitDigest != scope.UnitDigest {
		return nil, store.ErrNotFound
	}
	copyOfRun := run
	return &copyOfRun, nil
}

func (s *callerToolStore) LatestExtractionAttempt(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionAttempt, error) {
	s.calls = append(s.calls, "attempt:"+callerToolScope(scope.Repository, scope.Domain))
	run, ok := s.runs[callerToolScope(scope.Repository, scope.Domain)]
	if !ok || run.Commit != scope.Commit || run.UnitDigest != scope.UnitDigest {
		return nil, store.ErrNotFound
	}
	return &store.ExtractionAttempt{
		RunID: run.ID, Repo: scope.Repository, Commit: run.Commit,
		UnitDigest: run.UnitDigest, Domain: scope.Domain,
		Extractor: run.Extractor, Status: "published",
	}, nil
}

func (s *callerToolStore) ListAssertions(
	_ context.Context,
	query store.AssertionQuery,
) ([]store.Assertion, error) {
	s.calls = append(
		s.calls,
		"assertions:"+callerToolScope(query.Repo, query.RunID),
	)
	rows := make([]store.Assertion, 0)
	for _, assertion := range s.assertions[query.Repo] {
		if query.RunID != "" && assertion.RunID != query.RunID ||
			query.Predicate != "" && assertion.Predicate != query.Predicate ||
			query.Subject != "" && assertion.Subject != query.Subject ||
			query.Object != "" && assertion.Object != query.Object ||
			query.ObjectPrefix != "" &&
				!strings.HasPrefix(assertion.Object, query.ObjectPrefix) ||
			query.Lineage != "" && assertion.Lineage != query.Lineage {
			continue
		}
		rows = append(rows, assertion)
	}
	sort.Slice(rows, func(i, j int) bool {
		return callerToolAssertionKey(rows[i]) < callerToolAssertionKey(rows[j])
	})
	if query.After != nil {
		after := strings.Join([]string{
			query.After.Predicate, query.After.Subject, query.After.Object,
			query.After.ID, query.After.RunID,
		}, "\x00")
		rows = slicesAfter(rows, after)
	}
	if query.Limit > 0 && len(rows) > query.Limit {
		if !query.AllowTruncate {
			return nil, store.ErrResultLimit
		}
		rows = rows[:min(query.Limit+1, len(rows))]
	}
	return rows, nil
}

func slicesAfter(rows []store.Assertion, after string) []store.Assertion {
	index := sort.Search(len(rows), func(index int) bool {
		return callerToolAssertionKey(rows[index]) > after
	})
	return rows[index:]
}

func callerToolAssertionKey(assertion store.Assertion) string {
	return strings.Join([]string{
		assertion.Predicate, assertion.Subject, assertion.Object,
		assertion.ID, assertion.RunID,
	}, "\x00")
}

func (s *callerToolStore) ListReverseAssertions(
	_ context.Context,
	query store.ReverseAssertionQuery,
) (*store.ReverseAssertionPage, error) {
	s.calls = append(
		s.calls,
		"reverse:"+callerToolScope(query.Repo, query.RunID),
	)
	rows := make([]store.Assertion, 0)
	for _, assertion := range s.assertions[query.Repo] {
		if assertion.RunID != query.RunID ||
			assertion.Predicate != query.Predicate ||
			assertion.Object != query.Object ||
			query.Lineage != "" && assertion.Lineage != query.Lineage {
			continue
		}
		if query.After != nil &&
			callerToolReverseKey(assertion) <= strings.Join([]string{
				query.After.RowLineage,
				query.After.Subject,
				query.After.AssertionID,
			}, "\x00") {
			continue
		}
		rows = append(rows, assertion)
	}
	sort.Slice(rows, func(i, j int) bool {
		return callerToolReverseKey(rows[i]) < callerToolReverseKey(rows[j])
	})
	limit := query.Limit
	if limit == 0 {
		limit = 50
	}
	page := &store.ReverseAssertionPage{Assertions: rows}
	if len(rows) <= limit {
		return page, nil
	}
	page.Assertions = rows[:limit]
	last := page.Assertions[len(page.Assertions)-1]
	page.Next = &store.ReverseAssertionCursor{
		Repo: query.Repo, RunID: query.RunID,
		Predicate: query.Predicate, Object: query.Object,
		QueryLineage: query.Lineage, RowLineage: last.Lineage,
		Subject: last.Subject, AssertionID: last.ID,
	}
	return page, nil
}

func callerToolReverseKey(assertion store.Assertion) string {
	return strings.Join(
		[]string{assertion.Lineage, assertion.Subject, assertion.ID},
		"\x00",
	)
}

func (s *callerToolStore) ResolveEvidence(
	_ context.Context,
	repo, runID, atomID string,
) (*store.EvidenceResolution, error) {
	s.calls = append(
		s.calls,
		"resolve:"+callerToolEvidenceScope(repo, runID, atomID),
	)
	resolution, ok := s.resolutions[callerToolEvidenceScope(
		repo,
		runID,
		atomID,
	)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copyOfResolution := resolution
	copyOfResolution.Occurrences = append(
		[]store.SnapshotEvidence(nil),
		resolution.Occurrences...,
	)
	return &copyOfResolution, nil
}

func (s *callerToolStore) BeginExtractionRun(
	context.Context,
	store.ExtractionScope,
	string,
) (*store.ExtractionRun, error) {
	s.writes++
	return nil, errors.New("unexpected evidence write")
}

func (s *callerToolStore) AddEvidence(
	context.Context,
	string,
	[]store.EvidenceAtom,
	[]store.SnapshotEvidence,
	[]store.Assertion,
) error {
	s.writes++
	return errors.New("unexpected evidence write")
}

func (s *callerToolStore) AddEvidenceChunk(
	context.Context,
	string,
	string,
	int,
	[]store.EvidenceAtom,
	[]store.SnapshotEvidence,
	[]store.Assertion,
) error {
	s.writes++
	return errors.New("unexpected evidence write")
}

func (s *callerToolStore) PublishExtractionRun(
	context.Context,
	string,
	store.CoverageManifest,
) error {
	s.writes++
	return errors.New("unexpected evidence write")
}

func (s *callerToolStore) AbortExtractionRun(context.Context, string) error {
	s.writes++
	return errors.New("unexpected evidence write")
}

func (s *callerToolStore) PinRun(context.Context, string, string) error {
	s.writes++
	return errors.New("unexpected evidence write")
}

func (s *callerToolStore) SweepEvidence(
	context.Context,
	time.Time,
	time.Duration,
) (store.EvidenceSweepProgress, error) {
	s.writes++
	return store.EvidenceSweepProgress{}, errors.New("unexpected evidence write")
}

func callerToolRun(
	repo, domain, id, extractor string,
	protocols []string,
	assertions int,
) store.ExtractionRun {
	return store.ExtractionRun{
		ID: id, Repo: repo, Commit: callerToolCommit, Domain: domain,
		Extractor: extractor, Status: "published",
		Coverage: store.CoverageManifest{
			Protocols: protocols, CorpusFileCount: assertions,
			CandidateFileCount: assertions, ReadFileCount: assertions,
			ReadBytes:         int64(assertions * 100),
			SourceScopeDigest: "sha256:" + strings.Repeat("e", 64),
			AssertionCount:    assertions, AtomCount: assertions,
		},
	}
}

func callerToolFixture(
	t *testing.T,
) (
	*api.ContractCatalogService,
	CallerMapQueries,
	CallerComparisonQueries,
	*callerToolStore,
) {
	t.Helper()
	st := &callerToolStore{
		repos: []store.Repo{
			{Name: callerToolContractA, IndexedCommitHash: callerToolCommit},
			{Name: callerToolContractB, IndexedCommitHash: callerToolCommit},
			{Name: callerToolHidden, IndexedCommitHash: callerToolCommit},
			{Name: callerToolSource, IndexedCommitHash: callerToolCommit},
		},
		runs:        make(map[string]store.ExtractionRun),
		assertions:  make(map[string][]store.Assertion),
		resolutions: make(map[string]store.EvidenceResolution),
	}
	for index, item := range []struct {
		repository string
		lineage    string
	}{
		{callerToolContractA, callerToolLineageA},
		{callerToolContractB, callerToolLineageB},
	} {
		run := callerToolRun(
			item.repository,
			"proto-contract",
			"run-declaration-"+strconv.Itoa(index),
			"proto-contract@1.0.0",
			[]string{"protobuf", "protobuf-shapes"},
			1,
		)
		st.runs[callerToolScope(item.repository, run.Domain)] = run
		callerToolPutAssertion(
			st,
			store.Assertion{
				ID:        "declaration-" + strconv.Itoa(index),
				Predicate: "DECLARES_OPERATION",
				Subject:   "idl/orders.proto",
				Object:    strings.TrimPrefix(callerToolOperation, "/"),
				Lineage:   item.lineage,
				Tier:      store.TierExact,
				Repo:      item.repository,
				RunID:     run.ID,
				Supporting: []string{
					"atom-declaration-" + strconv.Itoa(index),
				},
				Detail: `{"schema":"proto-operation-detail-v1",` +
					`"request":{"raw":"GetRequest","resolution":"unresolved"},` +
					`"response":{"raw":"GetResponse","resolution":"unresolved"},` +
					`"client_streaming":false,"server_streaming":false}`,
			},
			"idl/orders.proto",
			10,
			20,
		)
	}
	for _, repository := range []string{callerToolSource, callerToolHidden} {
		run := callerToolRun(
			repository,
			"grpc-caller",
			"run-caller-"+strings.ReplaceAll(repository, "/", "-"),
			"1.3.0",
			[]string{
				"attribution-" + callerToolAttribution,
				"grpc",
				"resolution-scip-v1",
			},
			4,
		)
		st.runs[callerToolScope(repository, run.Domain)] = run
	}
	for index := 0; index < 3; index++ {
		callerToolPutCaller(
			t,
			st,
			callerToolSource,
			"caller-"+strconv.Itoa(index),
			"src/caller_"+strconv.Itoa(index)+".go",
			100+index*20,
			"CALLS_OPERATION",
			callerToolLineageA,
			"scip",
			"resolved",
			"team-orders",
		)
	}
	callerToolPutCaller(
		t, st, callerToolSource, "unresolved", "src/dynamic.go", 200,
		"UNRESOLVED_CALLER", "", "syntax", "unavailable", "",
	)
	callerToolPutCaller(
		t, st, callerToolHidden, "hidden", "src/secret.go", 300,
		"CALLS_OPERATION", callerToolLineageA, "scip", "resolved",
		"team-secret",
	)
	callerToolPutCaller(
		t, st, callerToolSource, "replacement-both", "src/caller_0.go", 100,
		"CALLS_OPERATION", callerToolLineageB, "scip", "resolved",
		"team-orders",
	)
	callerToolPutCaller(
		t, st, callerToolSource, "replacement-new", "src/replacement.go", 400,
		"CALLS_OPERATION", callerToolLineageB, "scip", "resolved",
		"team-replacement",
	)
	sourceRunKey := callerToolScope(callerToolSource, "grpc-caller")
	sourceRun := st.runs[sourceRunKey]
	sourceRun.Coverage.AssertionCount = 6
	sourceRun.Coverage.AtomCount = 6
	st.runs[sourceRunKey] = sourceRun
	opts := api.Options{
		Store: st, Evidence: st, CallerMapEnabled: true,
		Principal: func(context.Context) string { return "user:agent" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repo store.Repo) bool {
				return repo.Name != callerToolHidden
			}
		},
		AuthorizationProvider: "t20.11-test-v1",
	}
	catalog := api.NewContractCatalogService(opts)
	legacyCallerMap := api.NewLegacyCallerMapService(opts)
	callerMap := exactEnvelopeCallerMapQueries{legacy: legacyCallerMap}
	legacyComparison := api.NewLegacyCallerComparisonService(opts)
	if catalog == nil || legacyCallerMap == nil || legacyComparison == nil {
		t.Fatal("T20.11 services unavailable")
	}
	comparison := exactEnvelopeCallerComparisonQueries{legacy: legacyComparison}
	return catalog, callerMap, comparison, st
}

func callerToolPutCaller(
	t *testing.T,
	st *callerToolStore,
	repository, id, path string,
	start int,
	predicate, lineage, resolution, unitState, owner string,
) {
	t.Helper()
	run := st.runs[callerToolScope(repository, "grpc-caller")]
	detail := map[string]any{
		"schema": "go-caller-detail-v1", "resolution": resolution,
		"protocol":           "grpc",
		"attribution_digest": "sha256:" + callerToolAttribution,
		"unit_state":         unitState,
	}
	if predicate == "UNRESOLVED_CALLER" {
		detail["unresolved_reason"] = "unsupported_receiver_flow"
	}
	if owner != "" {
		detail["unit_candidates"] = []map[string]any{{
			"id": "unit-" + id, "logical_services": []string{"orders"},
			"owners": []string{owner},
		}}
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	tier := store.TierDerived
	if predicate == "UNRESOLVED_CALLER" {
		tier = store.TierUnresolved
	}
	callerToolPutAssertion(
		st,
		store.Assertion{
			ID: id, Predicate: predicate,
			Subject: path + ":" + strconv.Itoa(start) + "-" +
				strconv.Itoa(start+10),
			Object: callerToolOperation, Lineage: lineage, Tier: tier,
			CodeRole: "production", Repo: repository, RunID: run.ID,
			Supporting: []string{"atom-" + id}, Detail: string(encoded),
		},
		path,
		start,
		start+10,
	)
}

func callerToolPutAssertion(
	st *callerToolStore,
	assertion store.Assertion,
	path string,
	start, end int,
) {
	st.assertions[assertion.Repo] = append(
		st.assertions[assertion.Repo],
		assertion,
	)
	atomID := assertion.Supporting[0]
	st.resolutions[callerToolEvidenceScope(
		assertion.Repo,
		assertion.RunID,
		atomID,
	)] = store.EvidenceResolution{
		Atom: store.EvidenceAtom{
			ID: atomID, SchemaVersion: "t20.11-test",
			BlobDigest: "sha256:" + strings.Repeat("f", 64),
			StartByte:  start, EndByte: end,
		},
		Occurrences: []store.SnapshotEvidence{{
			ID: "occ-" + assertion.ID, AtomID: atomID,
			Repo: assertion.Repo, Commit: callerToolCommit,
			Path: path, StartLine: start/10 + 1, EndLine: start/10 + 1,
			VisibilityScope: "repo:" + assertion.Repo,
			RunID:           assertion.RunID,
		}},
	}
}

// T20.11 AC: an official-SDK stateless session discovers one of two
// duplicate-named operations, uses that returned full identity for exact
// detail, and exhausts a multi-page Caller Map with byte-equivalent shared
// service responses. Hidden state cannot affect bytes or work shape.
func TestCallerMapToolsProtocolSession(t *testing.T) {
	catalog, callerMap, _, st := callerToolFixture(t)
	server := NewServer(Options{
		Version: "test", Store: st,
		ContractCatalog: catalog, CallerMap: callerMap,
	})
	handler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true},
	)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t20.11-agent", Version: "1"},
		nil,
	)
	session, err := client.Connect(
		t.Context(),
		&sdk.StreamableClientTransport{Endpoint: httpServer.URL},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	assertCallerMapToolsSession(t, session, catalog, callerMap, st)
}

func TestCallerMapToolsInMemoryParity(t *testing.T) {
	catalog, callerMap, _, st := callerToolFixture(t)
	server := NewServer(Options{
		Version: "test", Store: st,
		ContractCatalog: catalog, CallerMap: callerMap,
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() {
		_, _ = server.Connect(t.Context(), serverTransport, nil)
	}()
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t20.11-in-memory", Version: "1"},
		nil,
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	assertCallerMapToolsSession(t, session, catalog, callerMap, st)
}

func TestCallerComparisonToolProtocolSession(t *testing.T) {
	catalog, callerMap, comparison, st := callerToolFixture(t)
	server := NewServer(Options{
		Version: "test", Store: st,
		ContractCatalog: catalog, CallerMap: callerMap,
		CallerComparison: comparison,
	})
	handler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true},
	)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t20.13-agent", Version: "1"},
		nil,
	)
	session, err := client.Connect(
		t.Context(),
		&sdk.StreamableClientTransport{Endpoint: httpServer.URL},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	assertCallerComparisonToolSession(t, session, comparison, st)
}

func TestCallerComparisonToolInMemoryParity(t *testing.T) {
	catalog, callerMap, comparison, st := callerToolFixture(t)
	server := NewServer(Options{
		Version: "test", Store: st,
		ContractCatalog: catalog, CallerMap: callerMap,
		CallerComparison: comparison,
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() {
		_, _ = server.Connect(t.Context(), serverTransport, nil)
	}()
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t20.13-in-memory", Version: "1"},
		nil,
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	assertCallerComparisonToolSession(t, session, comparison, st)
}

func TestCallerMapToolsPreserveSharedRefusals(t *testing.T) {
	catalog, callerMap, _, st := callerToolFixture(t)
	server := NewServer(Options{
		Version: "test", Store: st,
		ContractCatalog: catalog, CallerMap: callerMap,
	})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() {
		_, _ = server.Connect(t.Context(), serverTransport, nil)
	}()
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t20.11-refusals", Version: "1"},
		nil,
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	query := api.CallerMapQuery{
		Endpoint: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: callerToolContractA,
			Lineage: callerToolLineageA, Operation: callerToolOperation,
		},
		Ordering: "source",
	}
	args := map[string]any{
		"protocol": "protobuf", "repository": callerToolContractA,
		"declaration_lineage": callerToolLineageA,
		"operation":           callerToolOperation,
		"ordering":            "source",
	}

	_, directErr := callerMap.List(t.Context(), query, 101, "")
	requireMCPSharedStatus(t, directErr, http.StatusBadRequest)
	args["page_size"] = 101
	_, result := callToolSession[api.CallerMapPage](
		t,
		session,
		"list_operation_callers",
		args,
	)
	if !result.IsError ||
		!strings.Contains(textContent(t, result), directErr.Error()) {
		t.Fatalf(
			"oversized MCP refusal = error:%v %q, direct=%v",
			result.IsError,
			textContent(t, result),
			directErr,
		)
	}

	first, err := callerMap.List(t.Context(), query, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Pagination.NextCursor == "" {
		t.Fatal("stale-cursor test requires a continuation")
	}
	runKey := callerToolScope(callerToolSource, "grpc-caller")
	changed := st.runs[runKey]
	changed.Coverage.ReadBytes++
	st.runs[runKey] = changed

	_, directErr = callerMap.List(
		t.Context(),
		query,
		1,
		first.Pagination.NextCursor,
	)
	requireMCPSharedStatus(t, directErr, http.StatusConflict)
	args["page_size"] = 1
	args["cursor"] = first.Pagination.NextCursor
	_, result = callToolSession[api.CallerMapPage](
		t,
		session,
		"list_operation_callers",
		args,
	)
	if !result.IsError ||
		!strings.Contains(textContent(t, result), directErr.Error()) {
		t.Fatalf(
			"stale MCP refusal = error:%v %q, direct=%v",
			result.IsError,
			textContent(t, result),
			directErr,
		)
	}
	if st.writes != 0 {
		t.Fatalf("refusal paths performed %d evidence writes", st.writes)
	}
}

func assertCallerComparisonToolSession(
	t *testing.T,
	session *sdk.ClientSession,
	comparison CallerComparisonQueries,
	st *callerToolStore,
) {
	t.Helper()
	args := map[string]any{
		"old_protocol": "protobuf", "old_repository": callerToolContractA,
		"old_declaration_lineage":         callerToolLineageA,
		"old_operation":                   callerToolOperation,
		"replacement_protocol":            "protobuf",
		"replacement_repository":          callerToolContractB,
		"replacement_declaration_lineage": callerToolLineageB,
		"replacement_operation":           callerToolOperation,
		"level":                           "occurrence", "ordering": "source", "page_size": 1,
	}
	query := api.CallerComparisonQuery{
		Old: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: callerToolContractA,
			Lineage: callerToolLineageA, Operation: callerToolOperation,
		},
		Replacement: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: callerToolContractB,
			Lineage: callerToolLineageB, Operation: callerToolOperation,
		},
		Freshness: "any", Resolution: "any", Ordering: "source",
		Level: "occurrence",
	}
	cursor := ""
	total := 0
	classes := make(map[string]int)
	for pageNumber := 0; ; pageNumber++ {
		args["cursor"] = cursor
		page, result := callToolSession[api.CallerComparisonPage](
			t, session, "compare_operation_callers", args,
		)
		if result.IsError {
			t.Fatalf(
				"comparison page %d error: %s",
				pageNumber,
				textContent(t, result),
			)
		}
		direct, err := comparison.Compare(t.Context(), query, 1, cursor)
		if err != nil {
			t.Fatal(err)
		}
		assertSameJSON(t, page, *direct)
		for _, row := range page.Rows {
			classes[row.Classification]++
			for _, side := range []api.CallerComparisonSide{
				row.Old, row.Replacement,
			} {
				for _, evidence := range side.Rows {
					if evidence.Source.Repository == callerToolHidden ||
						evidence.Source.Path == "src/secret.go" {
						t.Fatalf("hidden comparison citation leaked: %+v", evidence)
					}
				}
			}
		}
		total += len(page.Rows)
		if page.Pagination.Complete {
			if pageNumber == 0 {
				t.Fatal("comparison did not exercise continuation")
			}
			break
		}
		if page.Pagination.NextCursor == "" ||
			page.Pagination.NextCursor == cursor {
			t.Fatalf("invalid comparison continuation: %+v", page.Pagination)
		}
		cursor = page.Pagination.NextCursor
	}
	if total != 5 || classes["both_evidence"] != 1 ||
		classes["old_only_evidence"] != 2 ||
		classes["new_only_evidence"] != 1 ||
		classes["unresolved"] != 1 {
		t.Fatalf("comparison total/classes = %d / %v", total, classes)
	}
	if st.writes != 0 {
		t.Fatalf("comparison tool performed %d evidence writes", st.writes)
	}
}

func requireMCPSharedStatus(t *testing.T, err error, status int) {
	t.Helper()
	if err == nil {
		t.Fatalf("shared service accepted request, want HTTP %d", status)
	}
	statusErr, ok := err.(huma.StatusError)
	if !ok || statusErr.GetStatus() != status {
		t.Fatalf("shared error = %T %v, want HTTP %d", err, err, status)
	}
}

func assertCallerMapToolsSession(
	t *testing.T,
	session *sdk.ClientSession,
	catalog *api.ContractCatalogService,
	callerMap CallerMapQueries,
	st *callerToolStore,
) {
	t.Helper()
	if _, err := catalog.List(
		t.Context(),
		api.ContractCatalogQuery{Protocol: "protobuf"},
		100,
		"",
	); err != nil {
		encoded, _ := json.Marshal(err)
		t.Fatalf(
			"direct discovery preflight: %T %#v; cause=%v; json=%s",
			err,
			err,
			errors.Unwrap(err),
			encoded,
		)
	}
	searchArgs := map[string]any{"protocol": "protobuf", "page_size": 100}
	search, searchResult := callToolSession[api.ContractCatalogList](
		t,
		session,
		"search_contract_operations",
		searchArgs,
	)
	if searchResult.IsError {
		encoded, _ := json.Marshal(searchResult)
		t.Fatalf("search tool error: %s", encoded)
	}
	operations := make([]api.ContractCatalogItem, 0)
	for _, item := range search.Items {
		if item.Kind == "operation" && item.Operation == callerToolOperation {
			operations = append(operations, item)
		}
	}
	if len(operations) != 2 ||
		operations[0].Repository == operations[1].Repository ||
		operations[0].Lineage == operations[1].Lineage {
		t.Fatalf("duplicate operation discovery = %+v", operations)
	}
	selected := operations[0]
	for _, operation := range operations {
		if operation.Repository == callerToolContractA {
			selected = operation
		}
	}
	directSearch, err := catalog.List(
		t.Context(),
		api.ContractCatalogQuery{Protocol: "protobuf"},
		100,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSameJSON(t, search, *directSearch)

	identityArgs := map[string]any{
		"protocol": selected.Protocol, "repository": selected.Repository,
		"declaration_lineage": selected.Lineage,
		"operation":           selected.Operation,
	}
	detail, detailResult := callToolSession[contractOperationResult](
		t,
		session,
		"get_contract_operation",
		identityArgs,
	)
	if detailResult.IsError {
		t.Fatalf("detail tool error: %s", textContent(t, detailResult))
	}
	directDetail, err := catalog.OperationForProtocol(
		t.Context(),
		selected.Protocol,
		selected.Repository,
		selected.Lineage,
		selected.Operation,
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Endpoint.Protocol != selected.Protocol ||
		detail.Endpoint.Repository != selected.Repository ||
		detail.Endpoint.Lineage != selected.Lineage ||
		detail.Endpoint.Operation != selected.Operation {
		t.Fatalf("detail endpoint = %+v, selected=%+v", detail.Endpoint, selected)
	}
	assertSameJSON(t, detail.Detail, *directDetail)

	callerArgs := map[string]any{
		"protocol": selected.Protocol, "repository": selected.Repository,
		"declaration_lineage": selected.Lineage,
		"operation":           selected.Operation,
		"ordering":            "source",
		"page_size":           2,
	}
	cursor := ""
	total := 0
	var firstPage api.CallerMapPage
	for pageNumber := 0; ; pageNumber++ {
		callerArgs["cursor"] = cursor
		page, result := callToolSession[api.CallerMapPage](
			t,
			session,
			"list_operation_callers",
			callerArgs,
		)
		if result.IsError {
			t.Fatalf(
				"caller page %d error: %s",
				pageNumber,
				textContent(t, result),
			)
		}
		direct, err := callerMap.List(
			t.Context(),
			api.CallerMapQuery{
				Endpoint: api.CallerMapEndpoint{
					Protocol:   selected.Protocol,
					Repository: selected.Repository,
					Lineage:    selected.Lineage,
					Operation:  selected.Operation,
				},
				Freshness: "any", Resolution: "any", Ordering: "source",
			},
			2,
			cursor,
		)
		if err != nil {
			t.Fatal(err)
		}
		assertSameJSON(t, page, *direct)
		if pageNumber == 0 {
			firstPage = page
		}
		for _, row := range page.Rows {
			if row.Source.Repository == callerToolHidden ||
				row.Source.Path == "src/secret.go" {
				t.Fatalf("hidden caller leaked: %+v", row)
			}
		}
		total += len(page.Rows)
		if page.Pagination.Complete {
			if pageNumber == 0 {
				t.Fatal("caller inventory did not require continuation")
			}
			break
		}
		if page.Pagination.NextCursor == "" ||
			page.Pagination.NextCursor == cursor {
			t.Fatalf("invalid continuation on page %d: %+v", pageNumber, page)
		}
		cursor = page.Pagination.NextCursor
	}
	if total != 4 || firstPage.TotalMatchingRows == nil || *firstPage.TotalMatchingRows != 4 {
		t.Fatalf("caller page totals = %d / %v", total, firstPage.TotalMatchingRows)
	}

	beforeMutation, err := json.Marshal(firstPage)
	if err != nil {
		t.Fatal(err)
	}
	hiddenRun := st.runs[callerToolScope(callerToolHidden, "grpc-caller")]
	hiddenRun.Commit = strings.Repeat("9", 40)
	hiddenRun.Coverage.AssertionCount++
	st.runs[callerToolScope(callerToolHidden, "grpc-caller")] = hiddenRun
	st.repos[2].IndexedCommitHash = hiddenRun.Commit
	st.assertions[callerToolHidden] = nil
	afterHiddenSearch, result := callToolSession[api.ContractCatalogList](
		t,
		session,
		"search_contract_operations",
		searchArgs,
	)
	if result.IsError {
		t.Fatalf(
			"catalog after hidden mutation: %s",
			textContent(t, result),
		)
	}
	assertSameJSON(t, afterHiddenSearch, search)
	afterHiddenDetail, result := callToolSession[contractOperationResult](
		t,
		session,
		"get_contract_operation",
		identityArgs,
	)
	if result.IsError {
		t.Fatalf(
			"operation detail after hidden mutation: %s",
			textContent(t, result),
		)
	}
	assertSameJSON(t, afterHiddenDetail, detail)
	afterHiddenMutation, result := callToolSession[api.CallerMapPage](
		t,
		session,
		"list_operation_callers",
		map[string]any{
			"protocol":            selected.Protocol,
			"repository":          selected.Repository,
			"declaration_lineage": selected.Lineage,
			"operation":           selected.Operation,
			"ordering":            "source",
			"page_size":           2,
		},
	)
	if result.IsError {
		t.Fatalf(
			"caller map after hidden mutation: %s",
			textContent(t, result),
		)
	}
	afterMutation, err := json.Marshal(afterHiddenMutation)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeMutation) != string(afterMutation) {
		t.Fatalf(
			"hidden mutation changed bytes:\n%s\n%s",
			beforeMutation,
			afterMutation,
		)
	}
	for _, call := range st.calls {
		if strings.Contains(call, callerToolHidden) {
			t.Fatalf("shared services queried invisible repository: %v", st.calls)
		}
	}
	if st.writes != 0 {
		t.Fatalf("read-only MCP session performed %d evidence writes", st.writes)
	}
}

func callToolSession[T any](
	t *testing.T,
	session *sdk.ClientSession,
	name string,
	arguments map[string]any,
) (T, *sdk.CallToolResult) {
	t.Helper()
	result, err := session.CallTool(
		t.Context(),
		&sdk.CallToolParams{Name: name, Arguments: arguments},
	)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	var output T
	if result.StructuredContent != nil && !result.IsError {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &output); err != nil {
			t.Fatalf("decode %s output: %v", name, err)
		}
	}
	return output, result
}

func assertSameJSON(t *testing.T, left, right any) {
	t.Helper()
	leftJSON, err := canonicalToolJSON(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := canonicalToolJSON(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("shared-service projection mismatch:\n%s\n%s", leftJSON, rightJSON)
	}
}

func canonicalToolJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var wire any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}
