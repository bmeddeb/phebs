package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestProofUnindexedRepositoryHTTPAndMCP(t *testing.T) {
	const repository = "evaluation.local/discovered"
	lineage := "contract_scip_package_v1_" + strings.Repeat("d", 64)
	for _, test := range []struct {
		name, target string
		field, kafka bool
	}{
		{"stable protobuf field", "/api/find_proto_field_references?lineage=" + lineage + "&message=p.Message&field_number=1", true, false},
		{"stable neutral field", "/api/find_field_references?lineage=" + lineage + "&message=p.Message&field_number=1", true, false},
		{"Kafka usage", "/api/find_kafka_topic_usage?topic=events", false, true},
		{"extraction coverage", "/api/get_extraction_coverage?domains=scip-proto-field,kafka-producer", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := &proofAPIStore{repos: []store.Repo{{Name: repository}, {Name: "hidden.example/private"}}}
			st.onLatestRun = func(scope store.ExtractionScope) { t.Fatalf("unindexed/hidden scope queried: %+v", scope) }
			opts := api.Options{Version: "test", APIKey: "discovery-test-key", Store: st, Evidence: st, ProofBundles: st,
				ProtobufFieldReferencesReleased: test.field,
				Visible: func(context.Context) func(store.Repo) bool {
					return func(repo store.Repo) bool { return repo.Name == repository }
				},
			}
			if test.field {
				opts.EvidenceExtractorVersions = map[string]string{"scip-proto-field": scipfield.CommittedOpenAPIVersion}
			}
			if test.kafka {
				opts.KafkaReleasedPlanes = []string{"consumer", "producer"}
			}
			handler := api.New(opts)
			anonymous := httptest.NewRecorder()
			handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, test.target, nil))
			if anonymous.Code != http.StatusUnauthorized || len(st.calls) != 0 {
				t.Fatalf("authentication crossed storage: status=%d calls=%v", anonymous.Code, st.calls)
			}
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.Header.Set("Authorization", "Bearer discovery-test-key")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("unindexed proof=%d %s", response.Code, response.Body)
			}
			var envelope api.ProofBundleEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			bundle := envelope.Bundle
			if len(bundle.Assertions) != 0 || len(bundle.Evidence) != 0 || len(bundle.ExactAuthorities) != 0 || len(bundle.ExtractorVersions) != 0 || bundle.Coverage.RepositoryCount != 1 || len(bundle.Coverage.Repositories) != 1 {
				t.Fatalf("unindexed proof claimed evidence: %+v", bundle)
			}
			for _, run := range bundle.Coverage.Repositories[0].Runs {
				if run.Status != "unpublished" || run.RunID != "" || run.Fresh || run.LatestAttempt != nil || run.Outcome != nil {
					t.Fatalf("unindexed proof claimed authority: %+v", run)
				}
			}
			if strings.Contains(response.Body.String(), "hidden.example") {
				t.Fatal("hidden repository entered proof")
			}
			if test.field {
				projection, err := api.NewProofService(opts).FindProtoFieldReferencesMCP(t.Context(), lineage, "p.Message", 1)
				if err != nil || projection.Outcome != "partial" || projection.AbsenceEligibility.Eligible || projection.ResultWindow.ResultSetComplete || projection.ResultWindow.Returned != 0 {
					t.Fatalf("MCP claimed absence/completeness: projection=%+v err=%v", projection, err)
				}
			}
			for _, call := range st.calls {
				if strings.HasPrefix(call, "run:") || strings.HasPrefix(call, "attempt:") || strings.HasPrefix(call, "assertions:") || strings.HasPrefix(call, "resolve:") {
					t.Fatalf("unindexed proof read extraction state: %v", st.calls)
				}
			}
		})
	}
}

func TestProofUnindexedToIndexedTransitionConflicts(t *testing.T) {
	st := &proofAPIStore{repos: []store.Repo{{Name: "evaluation.local/discovered"}}}
	lists := 0
	st.onListRepos = func() {
		lists++
		if lists == 2 {
			st.repos[0].IndexedCommitHash = strings.Repeat("a", 40)
		}
	}
	code, body, _ := getProof(t, proofHandler(st, "member", nil), "/api/get_extraction_coverage?domains=scip-proto-field")
	if code != http.StatusConflict || len(st.bundles) != 0 {
		t.Fatalf("source transition accepted stale coverage: status=%d body=%s bundles=%v", code, body, st.bundles)
	}
}
