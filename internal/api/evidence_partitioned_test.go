package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func rawPartitionedEvidence(t *testing.T, st *partitionedAtlasStore, visible func(context.Context) func(store.Repo) bool) *httptest.ResponseRecorder {
	t.Helper()
	handler := api.New(api.Options{Version: "test", Store: st, Evidence: st, Visible: visible})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
	return response
}

func TestEvidencePartitionedCurrentAndEmptyRoot(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(strconv.FormatBool(empty), func(t *testing.T) {
			st, _ := newPartitionedAtlas(t)
			if empty {
				st.assertions[atlasRepo] = nil
			}
			response := rawPartitionedEvidence(t, st, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("view=%d %s", response.Code, response.Body)
			}
			var view api.EvidenceView
			if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			want := 2
			if empty {
				want = 0
			}
			if view.PublishedRunCount != 0 || view.PartitionedRootCount != 1 || view.AssertionCount != want || len(view.Repositories) != 1 {
				t.Fatalf("view=%+v", view)
			}
			row := view.Repositories[0]
			if row.Run != nil || row.ExactAuthority == nil || row.ExactAuthority.RunID != atlasNativeRun || row.ExactAuthority.RootDigest != st.root.RootDigest || row.AssertionCount != want {
				t.Fatalf("row=%+v", row)
			}
			if strings.Contains(response.Body.String(), `"run":`) || strings.Contains(response.Body.String(), `"status":"published"`) {
				t.Fatalf("native root invented a legacy run: %s", response.Body)
			}
			if st.nativePages != 3 || st.nativeReads != 0 {
				t.Fatalf("pages=%d resolutions=%d", st.nativePages, st.nativeReads)
			}
		})
	}
}

func TestEvidencePartitionedUnavailableSources(t *testing.T) {
	for name, change := range map[string]func(*partitionedAtlasStore){
		"absent":          func(st *partitionedAtlasStore) { st.root.RunID = "" },
		"stale":           func(st *partitionedAtlasStore) { st.current = false },
		"other candidate": func(st *partitionedAtlasStore) { st.manifest.ManifestDigest = "sha256:" + strings.Repeat("9", 64) },
		"other commit":    func(st *partitionedAtlasStore) { st.manifest.HeadCommit = strings.Repeat("b", 40) },
		"other unit":      func(st *partitionedAtlasStore) { st.manifest.UnitDigest = "sha256:" + strings.Repeat("9", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			st, _ := newPartitionedAtlas(t)
			change(st)
			response := rawPartitionedEvidence(t, st, nil)
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), atlasNativeRun) || strings.Contains(response.Body.String(), atlasOperation) {
				t.Fatalf("ineligible source=%d %s", response.Code, response.Body)
			}
		})
	}
}

func TestEvidencePartitionedPreservesLegacyBytesAndPreference(t *testing.T) {
	st, _ := newPartitionedAtlas(t)
	legacy := catalogRun(atlasRepo, catalogProtoDomain, "legacy-run", 1)
	st.runs[proofScope(atlasRepo, catalogProtoDomain)] = legacy
	assertion, resolution := catalogAssertion(atlasRepo, legacy.ID, "legacy-service", "DECLARES_SERVICE", "proto/catalog.proto", "demo.Legacy", atlasLineage, `{"schema":"proto-service-detail-v1","name":"Legacy"}`)
	putCatalogAssertion(st.proofAPIStore, assertion, resolution)
	legacyHandler := api.New(api.Options{Version: "test", Store: st.proofAPIStore, Evidence: st.proofAPIStore})
	before := httptest.NewRecorder()
	legacyHandler.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/api/evidence?domain=proto-contract", nil))
	after := rawPartitionedEvidence(t, st, nil)
	if before.Code != http.StatusOK || after.Code != http.StatusOK || before.Body.String() != after.Body.String() || st.nativePages != 0 {
		t.Fatalf("legacy changed: before=%d %s after=%d %s nativePages=%d", before.Code, before.Body, after.Code, after.Body, st.nativePages)
	}
	if strings.Contains(after.Body.String(), "exact_authority") || strings.Contains(after.Body.String(), "partitioned_root_count") {
		t.Fatalf("new fields changed legacy bytes: %s", after.Body)
	}
}

func TestEvidencePartitionedAuthorizationPrecedesNativeReads(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(strconv.FormatBool(deleting), func(t *testing.T) {
			st, _ := newPartitionedAtlas(t)
			st.repos[0].Deleting = deleting
			rootReads := 0
			st.onReference = func() { rootReads++ }
			visible := func(context.Context) func(store.Repo) bool { return func(store.Repo) bool { return deleting } }
			response := rawPartitionedEvidence(t, st, visible)
			if response.Code != http.StatusOK || rootReads != 0 || st.nativePages != 0 || strings.Contains(response.Body.String(), atlasRepo) {
				t.Fatalf("filtered source=%d %s reads=%d pages=%d", response.Code, response.Body, rootReads, st.nativePages)
			}
			for _, call := range st.calls {
				if call != "list-repos" {
					t.Fatalf("filtered source touched evidence: %v", st.calls)
				}
			}
		})
	}
}

func TestEvidencePartitionedFinalFences(t *testing.T) {
	for name, mutate := range map[string]func(*partitionedAtlasStore){
		"root moved":          func(st *partitionedAtlasStore) { st.root.RootDigest = "sha256:" + strings.Repeat("7", 64) },
		"candidate moved":     func(st *partitionedAtlasStore) { st.manifest.ManifestDigest = "sha256:" + strings.Repeat("8", 64) },
		"repository moved":    func(st *partitionedAtlasStore) { st.repos[0].IndexedCommitHash = strings.Repeat("b", 40) },
		"repository deleting": func(st *partitionedAtlasStore) { st.repos[0].Deleting = true },
		"scope revision ABA":  func(st *partitionedAtlasStore) { st.repos[0].EvidenceRevision += 2 },
	} {
		t.Run(name, func(t *testing.T) {
			st, _ := newPartitionedAtlas(t)
			st.onAssertions = func(q store.AssertionQuery) {
				if q.Limit == 5000 {
					mutate(st)
				}
			}
			response := rawPartitionedEvidence(t, st, nil)
			if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), atlasNativeRun) || strings.Contains(response.Body.String(), atlasOperation) {
				t.Fatalf("moved source=%d %s", response.Code, response.Body)
			}
		})
	}
	t.Run("root appears after absence", func(t *testing.T) {
		st, _ := newPartitionedAtlas(t)
		original := st.root
		st.root.RunID = ""
		calls := 0
		st.onLatestRun = func(store.ExtractionScope) {
			calls++
			if calls == 2 {
				st.root = original
			}
		}
		response := rawPartitionedEvidence(t, st, nil)
		if response.Code != http.StatusConflict {
			t.Fatalf("new source=%d %s", response.Code, response.Body)
		}
	})
	t.Run("authorization changes", func(t *testing.T) {
		st, _ := newPartitionedAtlas(t)
		allowed := true
		st.onAssertions = func(q store.AssertionQuery) {
			if q.Limit == 5000 {
				allowed = false
			}
		}
		response := rawPartitionedEvidence(t, st, func(context.Context) func(store.Repo) bool { return func(store.Repo) bool { return allowed } })
		if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), atlasOperation) {
			t.Fatalf("changed permission=%d %s", response.Code, response.Body)
		}
	})
}

func TestEvidencePartitionedCompleteBoundAndScope(t *testing.T) {
	for _, count := range []int{5000, 5001} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			st, _ := newPartitionedAtlas(t)
			row := st.assertions[atlasRepo][0]
			st.assertions[atlasRepo] = make([]store.Assertion, count)
			for i := range count {
				st.assertions[atlasRepo][i] = row
				st.assertions[atlasRepo][i].ID = "bounded-" + strconv.Itoa(i)
			}
			response := rawPartitionedEvidence(t, st, nil)
			if count == 5000 {
				var view api.EvidenceView
				if response.Code != http.StatusOK {
					t.Fatalf("bounded=%d %s", response.Code, response.Body)
				}
				if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
					t.Fatal(err)
				}
				if view.AssertionCount != count {
					t.Fatalf("count=%d", view.AssertionCount)
				}
			} else if response.Code != http.StatusUnprocessableEntity || strings.Contains(response.Body.String(), "bounded-") {
				t.Fatalf("overflow=%d %s", response.Code, response.Body)
			}
		})
	}
	t.Run("foreign assertion", func(t *testing.T) {
		st, _ := newPartitionedAtlas(t)
		st.assertions[atlasRepo][0].Repo = "synthetic.invalid/foreign"
		response := rawPartitionedEvidence(t, st, nil)
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "synthetic.invalid/foreign") {
			t.Fatalf("foreign assertion=%d %s", response.Code, response.Body)
		}
	})
}

func TestEvidencePartitionedEnabledSchema(t *testing.T) {
	st, _ := newPartitionedAtlas(t)
	handler := api.New(api.Options{Version: "test", Store: st, Evidence: st})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("schema=%d %s", response.Code, response.Body)
	}
	var schema struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	row, ok := schema.Components.Schemas["EvidenceRepositoryView"]
	if !ok || row.Properties["exact_authority"] == nil {
		t.Fatalf("missing exact authority schema: %+v", row)
	}
	for _, required := range row.Required {
		if required == "run" {
			t.Fatal("native roots must not require a legacy run")
		}
	}
	if schema.Components.Schemas["EvidenceView"].Properties["partitioned_root_count"] == nil {
		t.Fatal("missing native root count schema")
	}
}

func TestEvidencePartitionedReleasedDomainAdmission(t *testing.T) {
	callerOnly := []string{"proto-contract", "grpc-caller"}
	for _, test := range []struct {
		name, domain string
		released     []string
		admitted     bool
	}{
		{"protobuf declaration", "proto-contract", []string{"proto-contract"}, true},
		{"caller declaration dependency", "proto-contract", callerOnly, true},
		{"caller implementation domain", "grpc-caller", callerOnly, true},
		{"provisional consumer", "grpc-consumer", nil, true},
		{"provisional fields", "scip-proto-field", nil, true},
		{"protobuf excludes consumer", "grpc-consumer", []string{"proto-contract"}, false},
		{"protobuf excludes fields", "scip-proto-field", []string{"proto-contract"}, false},
		{"caller excludes consumer", "grpc-consumer", callerOnly, false},
		{"caller excludes fields", "scip-proto-field", callerOnly, false},
		{"caller excludes kafka", "kafka-producer", callerOnly, false},
	} {
		for _, native := range []bool{false, true} {
			t.Run(test.name+"/native="+strconv.FormatBool(native), func(t *testing.T) {
				atlas, _ := newPartitionedAtlas(t)
				root := atlas.root
				root.Domain = test.domain
				st := &partitionedKafkaStore{proofAPIStore: atlas.proofAPIStore, root: root, manifest: atlas.manifest, current: true}
				if !native {
					run := catalogRun(atlasRepo, test.domain, "legacy-release-scope", 1)
					st.runs[proofScope(atlasRepo, test.domain)] = run
					st.assertions[atlasRepo] = []store.Assertion{{ID: "admission-sentinel", Predicate: "TEST_RAW", Repo: atlasRepo, RunID: run.ID}}
				}
				visibleCalls := 0
				handler := api.New(api.Options{Version: "test", Store: st, Evidence: st, CatalogReleasedDomains: test.released, Visible: func(context.Context) func(store.Repo) bool { visibleCalls++; return nil }})
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/evidence?domain="+test.domain, nil))
				if !test.admitted {
					if response.Code != http.StatusNotFound || visibleCalls != 0 || len(st.calls) != 0 || st.nativeReads != 0 {
						t.Fatalf("unreleased domain touched storage: status=%d body=%s visibility=%d calls=%v nativeReads=%d", response.Code, response.Body, visibleCalls, st.calls, st.nativeReads)
					}
					return
				}
				var view api.EvidenceView
				if response.Code != http.StatusOK {
					t.Fatalf("admitted domain=%d %s", response.Code, response.Body)
				}
				if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
					t.Fatal(err)
				}
				if len(view.Repositories) != 1 || view.AssertionCount == 0 || visibleCalls != 2 {
					t.Fatalf("admitted view=%+v visibility=%d", view, visibleCalls)
				}
				if native && (view.PartitionedRootCount != 1 || view.PublishedRunCount != 0) || !native && (view.PartitionedRootCount != 0 || view.PublishedRunCount != 1) {
					t.Fatalf("admitted publication kind=%+v", view)
				}
			})
		}
	}
}
