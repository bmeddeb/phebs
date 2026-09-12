//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

const t422EvidenceSearch = "/api/search?q=T401Fixture&scope=all_code&max_matches=1&context_lines=0"

func TestT422QueryEvidenceClosedRequests(t *testing.T) {
	final := t421ExactFinalAuthorityRead{Read: func(context.Context) ([]byte, func() error, error) { return nil, nil, nil }}
	for _, test := range []struct {
		name, path, value string
		duplicate, want   bool
	}{
		{"search", t422EvidenceSearch, t422QueryEvidenceValue, false, true},
		{"final", t421ExactFinalAuthorityPath, t422QueryEvidenceValue, false, true},
		{"tail", t421ExactTailReadinessPath, t422QueryEvidenceValue, false, false},
		{"foreign", "/api/lifecycle-status", t422QueryEvidenceValue, false, false},
		{"wrong_value", t422EvidenceSearch, "v2", false, false},
		{"duplicate", t422EvidenceSearch, t422QueryEvidenceValue, true, false},
		{"wrong_query", strings.Replace(t422EvidenceSearch, "T401Fixture", "other", 1), t422QueryEvidenceValue, false, false},
		{"wrong_scope", strings.Replace(t422EvidenceSearch, "all_code", "service", 1), t422QueryEvidenceValue, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, test.path, nil)
			r.Header.Set(t422QueryEvidenceHeader, test.value)
			if test.duplicate {
				r.Header.Add(t422QueryEvidenceHeader, test.value)
			}
			if _, ok := t421ExactReadLimits(r, final, final); ok != test.want {
				t.Fatalf("admission = %v", ok)
			}
		})
	}
	for _, query := range []string{"T401Fixture", "other"} {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_code","arguments":{"query":"` + query + `","scope":"all_code","max_matches":1,"context_lines":0}}}`
		r := httptest.NewRequest(http.MethodPost, t421ExactMCPPath, strings.NewReader(body))
		r.Header.Set(t422QueryEvidenceHeader, t422QueryEvidenceValue)
		if _, ok := t421ExactReadLimits(r); ok != (query == "T401Fixture") {
			t.Fatalf("MCP %q admission = %v", query, ok)
		}
	}
}

// Actual authentication and exact handler; the downstream observations are
// explicitly supplied values, not a native index or selected bootstrap proof.
func TestT422QueryEvidenceReportPresence(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	service, err := auth.New(ctx, auth.Options{Store: &t422LifecycleAuthFixture{}, Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); service.WaitCleanup() }()
	for _, name := range []string{"omitted", "zero", "two", "missing", "duplicate", "final"} {
		t.Run(name, func(t *testing.T) {
			capture := &t421ExactReadTestCapture{}
			count := uint64(2)
			if name == "zero" {
				count = 0
			}
			final := t421ExactFinalAuthorityRead{Read: func(ctx context.Context) ([]byte, func() error, error) {
				if selected, _ := ctx.Value(t422QueryEvidenceKey{}).(bool); !selected {
					t.Error("F lost admitted opt-in")
				}
				return []byte(`{}`), nil, nil
			}}
			handler := service.Require(t421ExactReadHandler(true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name != "missing" {
					_ = readaccounting.ObserveSearchRepositories(r.Context(), count)
				}
				if name == "duplicate" {
					_ = readaccounting.ObserveSearchRepositories(r.Context(), 9)
				}
				w.WriteHeader(http.StatusOK)
			}), capture.report, capture.fail, final))
			path := t422EvidenceSearch
			if name == "final" {
				path = t421ExactFinalAuthorityPath
			}
			r := exactT421ReadRequest(http.MethodGet, path, 1)
			if name != "omitted" {
				r.Header.Set(t422QueryEvidenceHeader, t422QueryEvidenceValue)
			}
			response := serveT421ExactReadRequest(t, handler, r)
			var report t421ExactReadReport
			if err := json.Unmarshal([]byte(response.report), &report); err != nil {
				t.Fatal(err)
			}
			wantFailure := name == "missing" || name == "duplicate"
			if (report.Status != "complete") != wantFailure {
				t.Fatalf("report = %s", response.report)
			}
			wantPresent := name == "zero" || name == "two" || name == "duplicate"
			if (report.VisibleRepositories != nil) != wantPresent {
				t.Fatalf("report = %s", response.report)
			}
			if wantPresent && *report.VisibleRepositories != count {
				t.Fatal("changed observed value")
			}
			if name == "omitted" && strings.Contains(response.report, "visible_repositories") {
				t.Fatal("legacy bytes changed")
			}
		})
	}
}

func TestT422QueryEvidenceFinalOmission(t *testing.T) {
	value := t421FinalAuthorityResponse{}
	emitted, err := t421FinalMarshal(value)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "query_authority") {
		t.Fatal("omission changed old F wire")
	}
	generation, _, _ := t421FinalCatalogTestFixture(t)
	value.QueryAuthority, err = t422FinalQueryAuthority(context.WithValue(t.Context(), t422QueryEvidenceKey{}, true), generation.Root)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := `,"query_authority":{"catalog_source_generation_sha256":"` + value.QueryAuthority.CatalogSourceGenerationSHA256 + `"}}`
	if !strings.HasSuffix(string(selected), wantSuffix) || !strings.HasPrefix(string(selected), string(raw[:len(raw)-1])) {
		t.Fatal("extension was not appended canonically")
	}
	selectedEmitted, err := t421FinalMarshal(value)
	if err != nil {
		t.Fatal(err)
	}
	emittedSuffix := ",\n  \"query_authority\": {\n    \"catalog_source_generation_sha256\": \"" + value.QueryAuthority.CatalogSourceGenerationSHA256 + "\"\n  }\n}\n"
	if len(selectedEmitted)-len(emitted) != 142 || strings.Contains(string(emitted), "query_authority") ||
		!strings.HasPrefix(string(selectedEmitted), string(emitted[:len(emitted)-3])) || !strings.HasSuffix(string(selectedEmitted), emittedSuffix) {
		t.Fatal("actual indented F extension changed shape or 142-byte delta")
	}
}

func TestT422QueryEvidenceCatalogSource(t *testing.T) {
	generation, _, _ := t421FinalCatalogTestFixture(t)
	want, err := servicecatalogv3.SourceGenerationDigest(generation.Root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), t422QueryEvidenceKey{}, true)
	proof, err := t422FinalQueryAuthority(ctx, generation.Root)
	if err != nil || proof == nil || proof.CatalogSourceGenerationSHA256 != want || want == generation.Root.Digest {
		t.Fatalf("actual source proof = %+v, %v", proof, err)
	}
	invalid := generation.Root
	invalid.Binding.Source.Commit = strings.Repeat("f", 40)
	if _, err := t422FinalQueryAuthority(ctx, invalid); err == nil {
		t.Fatal("accepted changed unvalidated source")
	}
	if value, err := t422FinalQueryAuthority(t.Context(), invalid); value != nil || err != nil {
		t.Fatal("omission performed root validation")
	}
}
