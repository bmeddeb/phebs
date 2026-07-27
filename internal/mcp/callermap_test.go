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

	"github.com/bmeddeb/phebs/internal/api"
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

func TestCallerMapToolSchemasAndDarkRegistration(t *testing.T) {
	schemaDigests := map[string]string{
		"search_contract_operations": "sha256:e2b2b80c7ebb5eeece8c6179b0e21a1b5676dee1ec3a481487f1984c93fbefc2",
		"get_contract_operation":     "sha256:3a8bfc0a42ac27ffbfbd3e546892924a6cd8ec4ef6ab1fe7bb44a95ae4881af9",
		"list_operation_callers":     "sha256:ea91fc93492c15723db645b08c38d9e28f191ebc3e7669b481e04730c5963098",
	}
	for _, test := range []struct {
		name          string
		catalog       ContractCatalogQueries
		callerMap     CallerMapQueries
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
			name:      "caller pack without compatibility",
			catalog:   schemaContractCatalogQueries{},
			callerMap: schemaCallerMapQueries{}, proofs: schemaProofQueries{},
			wantCount: 17,
		},
		{
			name: "all experimental tools", catalog: schemaContractCatalogQueries{},
			callerMap: schemaCallerMapQueries{}, proofs: schemaProofQueries{},
			compatibility: schemaCompatibilityQueries{}, wantCount: 18,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(Options{
				Version: "test", ContractCatalog: test.catalog,
				CallerMap: test.callerMap, Proofs: test.proofs,
				Compatibility: test.compatibility,
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
			} {
				tool, ok := found[name]
				if ok != enabled {
					t.Fatalf("%s discovery = %v, enabled=%v", name, ok, enabled)
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
						`"attribution_digest"`, `"caveat"`,
					} {
						if !strings.Contains(string(output), field) {
							t.Fatalf("%s output omitted %s: %s", name, field, output)
						}
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

func (s *callerToolStore) LatestPublishedRun(
	_ context.Context,
	repo, domain string,
) (*store.ExtractionRun, error) {
	s.calls = append(s.calls, "run:"+callerToolScope(repo, domain))
	run, ok := s.runs[callerToolScope(repo, domain)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copyOfRun := run
	return &copyOfRun, nil
}

func (s *callerToolStore) LatestExtractionAttempt(
	_ context.Context,
	repo, domain string,
) (*store.ExtractionAttempt, error) {
	s.calls = append(s.calls, "attempt:"+callerToolScope(repo, domain))
	run, ok := s.runs[callerToolScope(repo, domain)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &store.ExtractionAttempt{
		RunID: run.ID, Repo: repo, Commit: run.Commit, Domain: domain,
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
	string,
	string,
	string,
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
) (*api.ContractCatalogService, *api.CallerMapService, *callerToolStore) {
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
			"1.2.0",
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
	callerMap := api.NewCallerMapService(opts)
	if catalog == nil || callerMap == nil {
		t.Fatal("T20.11 services unavailable")
	}
	return catalog, callerMap, st
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
	catalog, callerMap, st := callerToolFixture(t)
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
	catalog, callerMap, st := callerToolFixture(t)
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

func TestCallerMapToolsPreserveSharedRefusals(t *testing.T) {
	catalog, callerMap, st := callerToolFixture(t)
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
	callerMap *api.CallerMapService,
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
	if total != 4 || firstPage.TotalMatchingRows != 4 {
		t.Fatalf("caller page totals = %d / %d", total, firstPage.TotalMatchingRows)
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
