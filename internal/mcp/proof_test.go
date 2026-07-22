package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

type proofToolStore struct {
	store.Store
	store.EvidenceStore
	store.ProofBundleStore

	repos       []store.Repo
	runs        map[string]store.ExtractionRun
	assertions  map[string][]store.Assertion
	resolutions map[string]store.EvidenceResolution
	bundles     map[string]store.ProofBundleRecord
	calls       []string
}

func proofToolScope(repo, domain string) string { return repo + "\x00" + domain }

func proofToolEvidenceScope(repo, runID, atomID string) string {
	return repo + "\x00" + runID + "\x00" + atomID
}

func (s *proofToolStore) ListRepos(context.Context) ([]store.Repo, error) {
	s.calls = append(s.calls, "list-repos")
	return append([]store.Repo(nil), s.repos...), nil
}

func (s *proofToolStore) LatestPublishedRun(_ context.Context, repo, domain string) (*store.ExtractionRun, error) {
	s.calls = append(s.calls, "run:"+proofToolScope(repo, domain))
	run, ok := s.runs[proofToolScope(repo, domain)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copyOfRun := run
	return &copyOfRun, nil
}

func (s *proofToolStore) LatestExtractionAttempt(_ context.Context, repo, domain string) (*store.ExtractionAttempt, error) {
	s.calls = append(s.calls, "attempt:"+proofToolScope(repo, domain))
	run, ok := s.runs[proofToolScope(repo, domain)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &store.ExtractionAttempt{
		RunID: run.ID, Repo: run.Repo, Commit: run.Commit, Domain: run.Domain,
		Extractor: run.Extractor, Status: "published",
	}, nil
}

func (s *proofToolStore) ListAssertions(_ context.Context, query store.AssertionQuery) ([]store.Assertion, error) {
	s.calls = append(s.calls, "assertions:"+proofToolScope(query.Repo, query.RunID))
	var result []store.Assertion
	for _, assertion := range s.assertions[query.Repo] {
		if query.RunID != "" && assertion.RunID != query.RunID ||
			query.Predicate != "" && assertion.Predicate != query.Predicate ||
			query.Object != "" && assertion.Object != query.Object ||
			query.Lineage != "" && assertion.Lineage != query.Lineage {
			continue
		}
		result = append(result, assertion)
	}
	return result, nil
}

func (s *proofToolStore) ResolveEvidence(_ context.Context, repo, runID, atomID string) (*store.EvidenceResolution, error) {
	s.calls = append(s.calls, "resolve:"+proofToolEvidenceScope(repo, runID, atomID))
	resolution, ok := s.resolutions[proofToolEvidenceScope(repo, runID, atomID)]
	if !ok {
		return nil, store.ErrNotFound
	}
	copyOfResolution := resolution
	copyOfResolution.Occurrences = append([]store.SnapshotEvidence(nil), resolution.Occurrences...)
	return &copyOfResolution, nil
}

func (s *proofToolStore) PutProofBundle(_ context.Context, bundle store.ProofBundleRecord) error {
	if s.bundles == nil {
		s.bundles = make(map[string]store.ProofBundleRecord)
	}
	if existing, ok := s.bundles[bundle.ID]; ok && existing.Content != bundle.Content {
		return store.ErrConflict
	}
	s.bundles[bundle.ID] = bundle
	return nil
}

func (s *proofToolStore) GetProofBundle(_ context.Context, id string) (*store.ProofBundleRecord, error) {
	bundle, ok := s.bundles[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &bundle, nil
}

func proofToolRun(repo, domain string) store.ExtractionRun {
	protocol := "grpc"
	if domain == "scip-proto-field" {
		protocol = "scip"
	}
	return store.ExtractionRun{
		ID:   "run-" + strings.ReplaceAll(repo+"-"+domain, "/", "-"),
		Repo: repo, Commit: strings.Repeat("a", 40), Domain: domain,
		Extractor: domain + "@1", Status: "published",
		Coverage: store.CoverageManifest{
			Protocols:       []string{protocol},
			CorpusFileCount: 2, CandidateFileCount: 1, ReadFileCount: 1, ReadBytes: 100,
			SourceScopeDigest: "sha256:" + strings.Repeat("b", 64), AssertionCount: 1, AtomCount: 1,
		},
	}
}

func proofToolAssertion(repo string, run store.ExtractionRun, id, predicate, object, lineage, path string) (store.Assertion, store.EvidenceResolution) {
	atomID := "ea-" + id
	assertion := store.Assertion{
		ID: id, Predicate: predicate, Subject: path + ":20-30", Object: object,
		Lineage: lineage, Tier: store.TierExact, CodeRole: "production",
		Repo: repo, RunID: run.ID, Supporting: []string{atomID},
	}
	observed := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	resolution := store.EvidenceResolution{
		Atom: store.EvidenceAtom{
			ID: atomID, SchemaVersion: "t14.2-test", BlobDigest: "sha256:" + strings.Repeat("c", 64),
			StartByte: 20, EndByte: 30, RuleID: predicate, ExtractorVersion: run.Extractor,
		},
		Occurrences: []store.SnapshotEvidence{{
			ID: "occ-" + id, AtomID: atomID, Repo: repo, Commit: run.Commit,
			Path: path, StartLine: 7, EndLine: 7, VisibilityScope: "repo:" + repo,
			RunID: run.ID, ObservedAt: observed,
		}},
	}
	return assertion, resolution
}

func proofToolFixture(t *testing.T) (*sdk.Server, *proofToolStore, string, string) {
	t.Helper()
	const (
		visibleRepo = "example.com/visible/client"
		hiddenRepo  = "example.com/hidden/client"
		operation   = "/shop.Cart/Get"
	)
	lineage := "contract_scip_package_v1_" + strings.Repeat("d", 64)
	grpcRun := proofToolRun(visibleRepo, "grpc-consumer")
	fieldRun := proofToolRun(visibleRepo, "scip-proto-field")
	hiddenRun := proofToolRun(hiddenRepo, "grpc-consumer")
	grpcAssertion, grpcResolution := proofToolAssertion(visibleRepo, grpcRun, "grpc", "CALLS_OPERATION", operation, "", "client/cart.go")
	fieldAssertion, fieldResolution := proofToolAssertion(visibleRepo, fieldRun, "field", "REFERENCES_PROTO_FIELD", "shop.Cart#1", lineage, "client/model.go")
	hiddenAssertion, hiddenResolution := proofToolAssertion(hiddenRepo, hiddenRun, "secret", "CALLS_OPERATION", operation, "", "secret/client.go")
	st := &proofToolStore{
		repos: []store.Repo{
			{Name: hiddenRepo, IndexedCommitHash: hiddenRun.Commit},
			{Name: visibleRepo, IndexedCommitHash: grpcRun.Commit},
		},
		runs: map[string]store.ExtractionRun{
			proofToolScope(visibleRepo, grpcRun.Domain):  grpcRun,
			proofToolScope(visibleRepo, fieldRun.Domain): fieldRun,
			proofToolScope(hiddenRepo, hiddenRun.Domain): hiddenRun,
		},
		assertions: map[string][]store.Assertion{
			visibleRepo: {grpcAssertion, fieldAssertion}, hiddenRepo: {hiddenAssertion},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofToolEvidenceScope(visibleRepo, grpcRun.ID, grpcAssertion.Supporting[0]):    grpcResolution,
			proofToolEvidenceScope(visibleRepo, fieldRun.ID, fieldAssertion.Supporting[0]):  fieldResolution,
			proofToolEvidenceScope(hiddenRepo, hiddenRun.ID, hiddenAssertion.Supporting[0]): hiddenResolution,
		},
	}
	proofs := api.NewProofService(api.Options{
		Store: st, Evidence: st, ProofBundles: st,
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repo store.Repo) bool { return repo.Name == visibleRepo }
		},
		Principal:             func(context.Context) string { return "user:agent" },
		AuthorizationProvider: "test-permissions-v1",
	})
	if proofs == nil {
		t.Fatal("proof service unavailable")
	}
	return NewServer(Options{Version: "test", Store: st, Proofs: proofs}), st, operation, lineage
}

// T14.2 AC: one live stateless Streamable HTTP session through the official
// MCP SDK receives complete proof bundles with exact citations and coverage
// for both operation and field questions.
func TestProofToolsProtocolSession(t *testing.T) {
	s, st, operation, lineage := proofToolFixture(t)
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s },
		&sdk.StreamableHTTPOptions{Stateless: true})
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := sdk.NewClient(&sdk.Implementation{Name: "t14.2-agent", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	operationBundle, result := callProofSessionTool(t, session, "find_operation_consumers", map[string]any{
		"operation": operation,
	})
	if result.IsError {
		t.Fatalf("operation tool error: %v", result.Content)
	}
	assertProofToolBundle(t, operationBundle, "find_operation_consumers", "client/cart.go")
	if operationBundle.Bundle.Query.Operation != operation || operationBundle.Bundle.Assertions[0].Predicate != "CALLS_OPERATION" {
		t.Fatalf("operation answer = %+v", operationBundle.Bundle)
	}

	fieldBundle, result := callProofSessionTool(t, session, "find_proto_field_references", map[string]any{
		"lineage": lineage, "message": "shop.Cart", "field_number": 1,
	})
	if result.IsError {
		t.Fatalf("field tool error: %v", result.Content)
	}
	assertProofToolBundle(t, fieldBundle, "find_proto_field_references", "client/model.go")
	if fieldBundle.Bundle.Query.Lineage != lineage || fieldBundle.Bundle.Query.FieldNumber != 1 ||
		fieldBundle.Bundle.Assertions[0].Predicate != "REFERENCES_PROTO_FIELD" {
		t.Fatalf("field answer = %+v", fieldBundle.Bundle)
	}

	coverage, result := callProofSessionTool(t, session, "get_extraction_coverage", map[string]any{
		"domains": []string{"scip-proto-field", "grpc-consumer"},
	})
	if result.IsError || coverage.Bundle.Query.Kind != "get_extraction_coverage" ||
		len(coverage.Bundle.Assertions) != 0 || len(coverage.Bundle.Evidence) != 0 ||
		!sort.StringsAreSorted(coverage.Bundle.Query.Domains) || coverage.Bundle.Coverage.RepositoryCount != 1 {
		t.Fatalf("coverage answer = %+v, error=%v content=%v", coverage, result.IsError, result.Content)
	}

	for _, call := range st.calls {
		if strings.Contains(call, "hidden") || strings.Contains(call, "secret") {
			t.Fatalf("proof tool touched hidden evidence: %v", st.calls)
		}
	}
	for _, bundle := range []api.ProofBundleEnvelope{operationBundle, fieldBundle, coverage} {
		encoded, err := json.Marshal(bundle)
		if err != nil || strings.Contains(string(encoded), "hidden") || strings.Contains(string(encoded), "secret") {
			t.Fatalf("proof tool leaked hidden scope: %s, %v", encoded, err)
		}
	}
}

func callProofSessionTool(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) (api.ProofBundleEnvelope, *sdk.CallToolResult) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	var envelope api.ProofBundleEnvelope
	if result.StructuredContent != nil && !result.IsError {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("decode %s output: %v", name, err)
		}
	}
	return envelope, result
}

func assertProofToolBundle(t *testing.T, envelope api.ProofBundleEnvelope, kind, path string) {
	t.Helper()
	if !strings.HasPrefix(envelope.ID, "pb_") || envelope.Bundle.Query.Kind != kind ||
		envelope.Bundle.Coverage.RepositoryCount != 1 || len(envelope.Bundle.Assertions) != 1 ||
		len(envelope.Bundle.Evidence) != 1 || len(envelope.Bundle.Evidence[0].Occurrences) != 1 ||
		envelope.Bundle.Evidence[0].Occurrences[0].Path != path ||
		envelope.Bundle.Evidence[0].Occurrences[0].StartLine != 7 ||
		envelope.Bundle.VisibilityContext.Principal != "user:agent" ||
		!strings.Contains(envelope.Bundle.Caveat, "Provisional evidence") {
		t.Fatalf("incomplete proof bundle = %+v", envelope)
	}
}

type schemaProofQueries struct{}

func (schemaProofQueries) FindOperationConsumers(context.Context, string) (*api.ProofBundleEnvelope, error) {
	return nil, errors.New("not called")
}

func (schemaProofQueries) FindProtoFieldReferences(context.Context, string, string, int) (*api.ProofBundleEnvelope, error) {
	return nil, errors.New("not called")
}

func (schemaProofQueries) GetExtractionCoverage(context.Context, []string) (*api.ProofBundleEnvelope, error) {
	return nil, errors.New("not called")
}

func TestProofToolSchemasAndDarkRegistration(t *testing.T) {
	for _, test := range []struct {
		name      string
		proofs    ProofQueries
		wantCount int
	}{
		{name: "dark", wantCount: 10},
		{name: "enabled", proofs: schemaProofQueries{}, wantCount: 13},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			s := NewServer(Options{Version: "test", Proofs: test.proofs})
			serverTransport, clientTransport := sdk.NewInMemoryTransports()
			go func() { _, _ = s.Connect(ctx, serverTransport, nil) }()
			client := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
			session, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			listed, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := make(map[string]*sdk.Tool, len(listed.Tools))
			for _, tool := range listed.Tools {
				found[tool.Name] = tool
			}
			if len(found) != test.wantCount {
				t.Fatalf("tool count = %d, want %d: %v", len(found), test.wantCount, found)
			}
			if _, premature := found["check_contract_compatibility"]; premature {
				t.Fatal("compatibility tool registered before the T14.3 engine")
			}
			for _, name := range []string{"find_operation_consumers", "find_proto_field_references", "get_extraction_coverage"} {
				tool, ok := found[name]
				if test.proofs == nil {
					if ok {
						t.Fatalf("dark server advertised %s", name)
					}
					continue
				}
				if !ok {
					t.Fatalf("enabled server omitted %s", name)
				}
				input, _ := json.Marshal(tool.InputSchema)
				output, _ := json.Marshal(tool.OutputSchema)
				if !strings.Contains(string(input), `"type":"object"`) ||
					!strings.Contains(string(output), `"visibility_context"`) || !strings.Contains(string(output), `"coverage"`) {
					t.Errorf("%s schemas: input=%s output=%s", name, input, output)
				}
			}
		})
	}
}

func TestProofToolInvalidInputIsToolError(t *testing.T) {
	s, _, _, _ := proofToolFixture(t)
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{name: "operation", args: map[string]any{"operation": "shop.Cart/Get"}},
		{name: "field", args: map[string]any{"lineage": "x", "message": "shop.Cart", "field_number": 19_000}},
		{name: "coverage", args: map[string]any{"domains": []string{"grpc-consumer", "grpc-consumer"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := map[string]string{
				"operation": "find_operation_consumers", "field": "find_proto_field_references",
				"coverage": "get_extraction_coverage",
			}[test.name]
			_, result := callTool[api.ProofBundleEnvelope](t, s, tool, test.args)
			if !result.IsError {
				t.Fatalf("invalid %s input succeeded: %+v", test.name, result)
			}
		})
	}
}
