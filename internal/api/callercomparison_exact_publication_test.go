package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

type exactComparisonMultiStore struct {
	*proofAPIStore
	states map[string]*exactCallerAPIStore
}

func (state *exactComparisonMultiStore) exactState(
	repository string,
) *exactCallerAPIStore {
	return state.states[repository]
}

func (state *exactComparisonMultiStore) GetCandidateManifestPublication(
	ctx context.Context,
	repository string,
) (*store.CandidateManifestPublication, error) {
	if source := state.exactState(repository); source != nil {
		return source.GetCandidateManifestPublication(ctx, repository)
	}
	return nil, store.ErrNotFound
}

func (state *exactComparisonMultiStore) GetResolverCatalogPublication(
	ctx context.Context,
	repository string,
) (*store.ResolverCatalogPublication, error) {
	if source := state.exactState(repository); source != nil {
		return source.GetResolverCatalogPublication(ctx, repository)
	}
	return nil, store.ErrNotFound
}

func (state *exactComparisonMultiStore) ResolverCatalogPublicationCurrent(
	ctx context.Context,
	pointer store.ResolverCatalogPublication,
) (bool, error) {
	if source := state.exactState(pointer.Repository); source != nil {
		return source.ResolverCatalogPublicationCurrent(ctx, pointer)
	}
	return false, nil
}

func (state *exactComparisonMultiStore) GetCallerGenerationPublication(
	ctx context.Context,
	repository string,
) (*store.CallerGenerationPublication, error) {
	if source := state.exactState(repository); source != nil {
		return source.GetCallerGenerationPublication(ctx, repository)
	}
	return nil, store.ErrNotFound
}

func (state *exactComparisonMultiStore) GetCallerGenerationPublicationSummary(
	ctx context.Context,
	repository string,
) (*store.CallerGenerationPublicationSummary, error) {
	if source := state.exactState(repository); source != nil {
		return source.GetCallerGenerationPublicationSummary(ctx, repository)
	}
	return nil, store.ErrNotFound
}

func (state *exactComparisonMultiStore) CallerGenerationPublicationSummaryCurrent(
	ctx context.Context,
	summary store.CallerGenerationPublicationSummary,
) (bool, error) {
	if source := state.exactState(summary.Generation.Repository); source != nil {
		return source.CallerGenerationPublicationSummaryCurrent(ctx, summary)
	}
	return false, nil
}

func (state *exactComparisonMultiStore) CallerGenerationPublicationSummaryAuthorityCurrent(
	ctx context.Context,
	summary store.CallerGenerationPublicationSummary,
) (bool, error) {
	if source := state.exactState(summary.Generation.Repository); source != nil {
		return source.CallerGenerationPublicationSummaryAuthorityCurrent(ctx, summary)
	}
	return false, nil
}

func (state *exactComparisonMultiStore) CallerGenerationPublicationSummariesAuthorityCurrent(
	ctx context.Context,
	summaries []store.CallerGenerationPublicationSummary,
) (bool, error) {
	if len(summaries) < 1 || len(summaries) > 2 {
		return false, store.ErrResultLimit
	}
	for _, summary := range summaries {
		current, err := state.CallerGenerationPublicationSummaryAuthorityCurrent(
			ctx, summary,
		)
		if err != nil || !current {
			return current, err
		}
	}
	return true, nil
}

func (state *exactComparisonMultiStore) GetCallerGenerationAdmission(
	ctx context.Context,
	generation store.CallerGenerationIdentity,
) (*store.CallerGenerationAdmission, error) {
	if source := state.exactState(generation.Repository); source != nil {
		return source.GetCallerGenerationAdmission(ctx, generation)
	}
	return nil, store.ErrNotFound
}

func newExactCallerComparisonFixture(
	t *testing.T,
) (*exactCallerAPIFixture, *api.CallerComparisonService) {
	t.Helper()
	fixture := newExactCallerAPIFixtureWithMutators(
		t, 5, nil,
		func(record *callerleaf.Record) {
			index, err := strconv.Atoi(record.Fact.Atom.FactFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			var detail map[string]any
			if err := json.Unmarshal(
				[]byte(record.Fact.Assertion.Detail), &detail,
			); err != nil {
				t.Fatal(err)
			}
			candidateID := byte('b' + index)
			if index == 2 || index == 3 {
				candidateID = 'd'
			}
			detail["unit_candidates"] = []map[string]any{{
				"id":               "au_" + strings.Repeat(string(candidateID), 64),
				"logical_services": []string{"orders"},
				"owners":           []string{"team-orders"},
			}}
			switch index {
			case 1:
				record.Fact.Assertion.Lineage = replacementLineage
			case 3:
				record.Fact.Assertion.Lineage = replacementLineage
				// Pair this replacement row with old row 2 under one immutable
				// occurrence tuple while retaining a distinct exact record ID.
				record.Fact.Atom.StartByte = 24
				record.Fact.Atom.EndByte = 35
				record.Fact.StartLine = 3
				record.Fact.EndLine = 3
				record.Fact.Assertion.Subject = "src/calls.go:24-35"
			case 4:
				record.Kind = callerleaf.RecordAbstention
				record.Reason = "unresolved_caller"
				record.Fact.Assertion.Predicate = "UNRESOLVED_CALLER"
				record.Fact.Assertion.Lineage = ""
				record.Fact.Assertion.Tier = store.TierUnresolved
				detail["unit_state"] = "unavailable"
				delete(detail, "unit_candidates")
				detail["unresolved_reason"] = "unsupported_receiver_flow"
			}
			encoded, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			record.Fact.Assertion.Detail = string(encoded)
		},
	)
	return finishExactCallerComparisonFixture(t, fixture)
}

func finishExactCallerComparisonFixture(
	t *testing.T,
	fixture *exactCallerAPIFixture,
) (*exactCallerAPIFixture, *api.CallerComparisonService) {
	t.Helper()
	declaration, resolution := catalogAssertion(
		fixture.repository, exactDeclarationRun, "exact-replacement-declaration",
		"DECLARES_OPERATION", "idl/orders.proto",
		strings.TrimPrefix(exactCallerOperation, "/"), replacementLineage,
		`{"schema":"proto-operation-detail-v1"}`,
	)
	resolution.Occurrences[0].Commit = fixture.commit
	putCatalogAssertion(fixture.store.proofAPIStore, declaration, resolution)

	opts := fixture.serviceOptions()
	opts.CallerReader = fixture.reader
	opts.CallerMap = fixture.service
	comparison := api.NewCallerComparisonService(opts)
	if comparison == nil {
		t.Fatal("exact caller comparison service is unavailable")
	}
	return fixture, comparison
}

func exactComparisonQuery(fixture *exactCallerAPIFixture) api.CallerComparisonQuery {
	return api.CallerComparisonQuery{
		Old: fixture.query.Endpoint,
		Replacement: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: fixture.repository,
			Lineage: replacementLineage, Operation: exactCallerOperation,
		},
		Freshness: "any", Resolution: "any", Ordering: "source",
		Level: "occurrence",
	}
}

func TestExactCallerComparisonClassifiesNeutralUnionAndReadsCitations(
	t *testing.T,
) {
	fixture, service := newExactCallerComparisonFixture(t)
	query := exactComparisonQuery(fixture)
	page, err := service.Compare(t.Context(), query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.SchemaVersion != "caller-comparison-v2" ||
		page.MatchingRowsState != "exact" || page.TotalRows == nil ||
		*page.TotalRows != 4 || len(page.Rows) != 4 || page.Coverage != nil ||
		page.Old.Generation == nil || page.Replacement.Generation == nil ||
		page.Old.Declaration == nil || page.Replacement.Declaration == nil {
		t.Fatalf("exact caller comparison page = %+v", page)
	}
	classes := make(map[string]int)
	var citation string
	for _, row := range page.Rows {
		classes[row.Classification]++
		for _, side := range []api.CallerComparisonSide{row.Old, row.Replacement} {
			if len(side.Rows) > 4 || side.OccurrenceCount < len(side.Rows) {
				t.Fatalf("unbounded comparison side = %+v", side)
			}
			if citation == "" && len(side.Rows) > 0 {
				citation = side.Rows[0].Source.Citation
			}
		}
	}
	want := map[string]int{
		"old_only_evidence": 1, "both_evidence": 1,
		"new_only_evidence": 1, "unresolved": 1,
	}
	if !equalStringIntMap(classes, want) {
		t.Fatalf("exact occurrence classes = %v, want %v", classes, want)
	}
	if citation == "" {
		t.Fatal("comparison emitted no exact citation")
	}
	resolved, err := fixture.service.ReadCitation(t.Context(), citation)
	if err != nil || resolved.Content == "" ||
		resolved.Generation.Repository != fixture.repository ||
		resolved.Source.Plane != "repository-overlay" {
		t.Fatalf("comparison citation = %+v, %v", resolved, err)
	}

	query.Level = "unit"
	units, err := service.Compare(t.Context(), query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	unitClasses := make(map[string]int)
	promotedUnit := false
	for _, row := range units.Rows {
		unitClasses[row.Classification]++
		if row.Unit == nil {
			continue
		}
		promotedUnit = true
		for _, side := range []api.CallerComparisonSide{row.Old, row.Replacement} {
			for _, sample := range side.Rows {
				if len(sample.Unit.Candidates) != 0 || sample.Unit.CandidateTotal != 0 {
					t.Fatalf("unit citation sample duplicated promoted attribution: %+v", sample.Unit)
				}
			}
		}
	}
	if units.TotalRows == nil || *units.TotalRows != 4 ||
		!equalStringIntMap(unitClasses, want) || !promotedUnit {
		t.Fatalf("exact unit comparison = %+v / %v", units, unitClasses)
	}
}

func TestExactCallerComparisonHTTPSharesCallerCitationEngine(t *testing.T) {
	fixture, _ := newExactCallerComparisonFixture(t)
	opts := fixture.serviceOptions()
	opts.CallerReader = fixture.reader
	handler := api.New(opts)
	values := url.Values{
		"old_protocol":           {"protobuf"},
		"old_repository":         {fixture.repository},
		"old_lineage":            {exactCallerLineage},
		"old_operation":          {exactCallerOperation},
		"replacement_protocol":   {"protobuf"},
		"replacement_repository": {fixture.repository},
		"replacement_lineage":    {replacementLineage},
		"replacement_operation":  {exactCallerOperation},
		"level":                  {"occurrence"},
		"page_size":              {"100"},
	}
	var page api.CallerComparisonPage
	code, body := catalogHTTP(
		t, handler, "/api/compare_operation_callers?"+values.Encode(), &page,
	)
	if code != http.StatusOK || page.MatchingRowsState != "exact" ||
		len(page.Rows) == 0 {
		t.Fatalf("HTTP exact comparison = %d %s / %+v", code, body, page)
	}
	citation := ""
	for _, row := range page.Rows {
		for _, side := range []api.CallerComparisonSide{row.Old, row.Replacement} {
			if len(side.Rows) > 0 {
				citation = side.Rows[0].Source.Citation
				break
			}
		}
		if citation != "" {
			break
		}
	}
	var resolved api.CallerMapCitation
	code, body = catalogHTTP(
		t, handler, "/api/contract_callers/citation?citation="+
			url.QueryEscape(citation), &resolved,
	)
	if code != http.StatusOK || resolved.Content == "" {
		t.Fatalf("HTTP comparison citation = %d %s / %+v", code, body, resolved)
	}
	_, version := catalogHTTP(t, handler, "/api/version", nil)
	if !strings.Contains(version, `"contract-caller-comparison"`) {
		t.Fatalf("exact comparison capability absent: %s", version)
	}
	code, openAPI := catalogHTTP(t, handler, "/api/openapi.json", nil)
	if code != http.StatusOK {
		t.Fatalf("exact comparison OpenAPI = %d %s", code, openAPI)
	}
	requireExactComparisonOpenAPI(t, openAPI)
}

func requireExactComparisonOpenAPI(t *testing.T, raw string) {
	t.Helper()
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	require := func(name string, fields ...string) {
		t.Helper()
		schema, ok := document.Components.Schemas[name]
		if !ok {
			t.Fatalf("OpenAPI omitted %s", name)
		}
		for _, field := range fields {
			if !slices.Contains(schema.Required, field) {
				t.Fatalf(
					"OpenAPI %s does not require %s: %+v",
					name, field, schema,
				)
			}
		}
		if name == "CallerComparisonExactPage" {
			if _, legacy := schema.Properties["coverage"]; legacy {
				t.Fatalf("OpenAPI exact page retained legacy coverage: %+v", schema)
			}
		}
	}
	require(
		"CallerComparisonExactPage", "schema_version", "query", "old",
		"replacement", "rows", "matching_rows_state", "pagination", "caveat",
	)
	require(
		"CallerComparisonExactSnapshot", "endpoint", "generation",
		"matching_rows_state",
	)
}

func TestExactCallerComparisonBoundsPageWideCitationHydration(t *testing.T) {
	const entries = 100
	fixture := newExactCallerAPIFixtureWithMutators(
		t, entries*2, nil,
		func(record *callerleaf.Record) {
			index, err := strconv.Atoi(record.Fact.Atom.FactFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			pair := index
			if index >= entries {
				pair = index - entries
				record.Fact.Assertion.Lineage = replacementLineage
				start := pair * 12
				record.Fact.Atom.StartByte = start
				record.Fact.Atom.EndByte = start + 11
				record.Fact.StartLine = pair + 1
				record.Fact.EndLine = pair + 1
				record.Fact.Assertion.Subject = fmt.Sprintf(
					"src/calls.go:%d-%d", start, start+11,
				)
			}
			var detail map[string]any
			if err := json.Unmarshal(
				[]byte(record.Fact.Assertion.Detail), &detail,
			); err != nil {
				t.Fatal(err)
			}
			detail["unit_candidates"] = []map[string]any{{
				"id": "au_" + fmt.Sprintf("%064x", pair+1),
			}}
			encoded, err := json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			record.Fact.Assertion.Detail = string(encoded)
		},
	)
	fixture, service := finishExactCallerComparisonFixture(t, fixture)
	page, err := service.Compare(
		t.Context(), exactComparisonQuery(fixture), entries, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalRows == nil || *page.TotalRows != entries ||
		len(page.Rows) != entries {
		t.Fatalf("maximum comparison page = %+v", page)
	}
	hydrated := 0
	truncated := false
	for _, row := range page.Rows {
		for _, side := range []api.CallerComparisonSide{row.Old, row.Replacement} {
			hydrated += len(side.Rows)
			truncated = truncated || side.RowsTruncated
		}
	}
	if hydrated != entries || !truncated {
		t.Fatalf(
			"page-wide citation hydration = %d, truncated=%t; want %d/true",
			hydrated, truncated, entries,
		)
	}
}

func TestExactCallerComparisonGapAndCursorTransitions(t *testing.T) {
	fixture, service := newExactCallerComparisonFixture(t)
	query := exactComparisonQuery(fixture)
	first, err := service.Compare(t.Context(), query, 1, "")
	if err != nil || first.Pagination.NextCursor == "" {
		t.Fatalf("exact first page = %+v, %v", first, err)
	}
	tamperByte := "A"
	if strings.HasSuffix(first.Pagination.NextCursor, tamperByte) {
		tamperByte = "B"
	}
	tampered := first.Pagination.NextCursor[:len(first.Pagination.NextCursor)-1] + tamperByte
	_, err = service.Compare(t.Context(), query, 1, tampered)
	requireCatalogStatus(t, err, http.StatusBadRequest)
	differentQuery := query
	differentQuery.Owner = "team-other"
	_, err = service.Compare(
		t.Context(), differentQuery, 1, first.Pagination.NextCursor,
	)
	requireCatalogStatus(t, err, http.StatusBadRequest)
	baseRevision := fixture.store.currentRevision
	fixture.store.currentRevision++
	_, err = service.Compare(
		t.Context(), query, 1, first.Pagination.NextCursor,
	)
	requireCatalogStatus(t, err, http.StatusConflict)
	fixture.store.currentRevision = baseRevision

	pointer := fixture.store.publication
	fixture.store.publication = nil
	gap, err := service.Compare(t.Context(), query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if gap.MatchingRowsState != "unavailable" || gap.TotalRows != nil ||
		len(gap.Rows) != 0 || !gap.Pagination.Complete ||
		gap.Old.MatchingRowsState != "unavailable" ||
		gap.Replacement.MatchingRowsState != "unavailable" ||
		!strings.Contains(gap.Caveat, "absence are unavailable") {
		t.Fatalf("exact comparison gap = %+v", gap)
	}
	fixture.store.publication = pointer

	first, err = service.Compare(t.Context(), query, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	publicationReads := fixture.store.publicationReads
	fixture.visible[fixture.repository] = false
	_, err = service.Compare(
		t.Context(), query, 1, first.Pagination.NextCursor,
	)
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.store.publicationReads != publicationReads {
		t.Fatalf(
			"revoked continuation reached publication I/O: %d -> %d",
			publicationReads, fixture.store.publicationReads,
		)
	}
}

func TestExactCallerComparisonFencesIndependentSidesAndAuthorizesBothFirst(
	t *testing.T,
) {
	dataDir := t.TempDir()
	oldRepository := "github.com/acme/old-callers"
	replacementRepository := "github.com/acme/replacement-callers"
	oldFixture := newExactCallerAPIFixtureConfigured(
		t, 2, oldRepository, dataDir, nil, nil,
	)
	replacementFixture := newExactCallerAPIFixtureConfigured(
		t, 2, replacementRepository, dataDir, nil, nil,
	)
	state := mergeExactComparisonStores(oldFixture.store, replacementFixture.store)
	adapters, err := callerexecute.NewRegistry([]sdk.Extractor{gocaller.NewGRPC()})
	if err != nil {
		t.Fatal(err)
	}
	publications := callerpublication.NewRegistry(callerexecute.Root(dataDir))
	t.Cleanup(func() { _ = publications.Close() })
	reader, err := callerexecute.NewPublicationReader(state, adapters, publications)
	if err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{oldRepository: true, replacementRepository: true}
	opts := api.Options{
		Version: "test", Store: state, Evidence: state, DataDir: dataDir,
		CallerMapEnabled: true, CallerReader: reader,
		Principal: func(context.Context) string { return "user:comparison-member" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool { return visible[repository.Name] }
		},
		AuthorizationProvider: "exact-comparison-test-permissions-v1",
	}
	opts.CallerMap = api.NewCallerMapService(opts)
	opts.CallerComparison = api.NewCallerComparisonService(opts)
	if opts.CallerMap == nil || opts.CallerComparison == nil {
		t.Fatal("two-repository exact caller services are unavailable")
	}
	query := api.CallerComparisonQuery{
		Old: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: oldRepository,
			Lineage: exactCallerLineage, Operation: exactCallerOperation,
		},
		Replacement: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: replacementRepository,
			Lineage: exactCallerLineage, Operation: exactCallerOperation,
		},
		Freshness: "any", Resolution: "any", Ordering: "source",
		Level: "occurrence",
	}
	first, err := opts.CallerComparison.Compare(t.Context(), query, 1, "")
	if err != nil || first.TotalRows == nil || *first.TotalRows != 4 ||
		first.Pagination.NextCursor == "" {
		t.Fatalf("two-repository first page = %+v, %v", first, err)
	}
	replacementFixture.store.currentRevision++
	_, err = opts.CallerComparison.Compare(
		t.Context(), query, 1, first.Pagination.NextCursor,
	)
	requireCatalogStatus(t, err, http.StatusConflict)
	replacementFixture.store.currentRevision--

	basePublication := replacementFixture.store.publication
	baseAdmission := replacementFixture.store.admission
	baseRevision := replacementFixture.store.currentRevision
	for _, test := range []struct {
		name      string
		state     string
		configure func()
	}{
		{
			name: "missing", state: "missing",
			configure: func() { replacementFixture.store.publication = nil },
		},
		{
			name: "failed", state: "failed",
			configure: func() {
				replacementFixture.store.publication = nil
				replacementFixture.store.admission = &store.CallerGenerationAdmission{
					Generation:  basePublication.Generation,
					Disposition: store.CallerGenerationTerminalGenerationRefusal,
				}
			},
		},
		{
			name: "stale", state: "stale",
			configure: func() { replacementFixture.store.currentRevision++ },
		},
	} {
		t.Run("one-sided "+test.name, func(t *testing.T) {
			replacementFixture.store.publication = basePublication
			replacementFixture.store.admission = baseAdmission
			replacementFixture.store.currentRevision = baseRevision
			test.configure()
			gap, err := opts.CallerComparison.Compare(t.Context(), query, 100, "")
			if err != nil {
				t.Fatal(err)
			}
			if gap.MatchingRowsState != "unavailable" || gap.TotalRows != nil ||
				len(gap.Rows) != 0 || !gap.Pagination.Complete ||
				gap.Pagination.NextCursor != "" ||
				gap.Old.MatchingRowsState != "exact" ||
				gap.Replacement.MatchingRowsState != "unavailable" ||
				gap.Replacement.Generation == nil ||
				gap.Replacement.Generation.State != test.state {
				t.Fatalf("one-sided %s gap = %+v", test.name, gap)
			}
		})
	}
	replacementFixture.store.publication = basePublication
	replacementFixture.store.admission = baseAdmission
	replacementFixture.store.currentRevision = baseRevision

	// Permission loss after both derived reads but before serialization
	// suppresses the whole result. The callback runs inside the final joint
	// authority fence, immediately before authorization's last-result check.
	finalAuthorityCheck := replacementFixture.store.authorityCurrentChecks + 2
	replacementFixture.store.afterAuthorityCurrent = func(check int) {
		if check == finalAuthorityCheck {
			visible[replacementRepository] = false
		}
	}
	page, err := opts.CallerComparison.Compare(t.Context(), query, 1, "")
	if page != nil {
		t.Fatalf("result-time permission loss returned a page: %+v", page)
	}
	requireCatalogStatus(t, err, http.StatusNotFound)
	replacementFixture.store.afterAuthorityCurrent = nil
	visible[replacementRepository] = true

	oldReads := oldFixture.store.publicationReads
	replacementReads := replacementFixture.store.publicationReads
	visible[replacementRepository] = false
	_, err = opts.CallerComparison.Compare(t.Context(), query, 1, "")
	requireCatalogStatus(t, err, http.StatusNotFound)
	if oldFixture.store.publicationReads != oldReads ||
		replacementFixture.store.publicationReads != replacementReads {
		t.Fatalf(
			"hidden side reached caller publication I/O: old=%d/%d replacement=%d/%d",
			oldReads, oldFixture.store.publicationReads,
			replacementReads, replacementFixture.store.publicationReads,
		)
	}
}

func mergeExactComparisonStores(
	states ...*exactCallerAPIStore,
) *exactComparisonMultiStore {
	merged := &proofAPIStore{
		assertions:  make(map[string][]store.Assertion),
		resolutions: make(map[string]store.EvidenceResolution),
		runs:        make(map[string]store.ExtractionRun),
		attempts:    make(map[string]store.ExtractionAttempt),
	}
	result := &exactComparisonMultiStore{
		proofAPIStore: merged, states: make(map[string]*exactCallerAPIStore),
	}
	for _, state := range states {
		for _, repository := range state.repos {
			merged.repos = append(merged.repos, repository)
			result.states[repository.Name] = state
		}
		for repository, assertions := range state.assertions {
			merged.assertions[repository] = append(
				merged.assertions[repository], assertions...,
			)
		}
		for key, resolution := range state.resolutions {
			merged.resolutions[key] = resolution
		}
		for key, run := range state.runs {
			merged.runs[key] = run
		}
		for key, attempt := range state.attempts {
			merged.attempts[key] = attempt
		}
	}
	return result
}

var _ callerexecute.PublicationReadStore = (*exactComparisonMultiStore)(nil)
