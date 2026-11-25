package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/compat"
)

// PHEBS-41: the released change-impact claim composes the released Buf WIRE
// verdict with released field references. These tests pin its HTTP boundary.

func changeImpactChecker(lineage string) *fixedCompatibility {
	field := compat.FieldIdentity{Lineage: lineage, Message: "shop.Cart", Number: 1}
	return &fixedCompatibility{result: compat.CompatibilityResult{
		Compatible: false,
		Before:     compat.InputSnapshot{Digest: "sha256:" + strings.Repeat("1", 64), Files: []compat.InputFile{}},
		After:      compat.InputSnapshot{Digest: "sha256:" + strings.Repeat("3", 64), Files: []compat.InputFile{}},
		Violations: []compat.Violation{{
			Snapshot: "after", Path: "cart.proto", StartLine: 4, StartColumn: 3, EndLine: 4, EndColumn: 18,
			Rule: "FIELD_SAME_TYPE", Message: "field type changed", Field: &field,
		}},
		AffectedFields: []compat.FieldIdentity{field},
		Run: compat.Run{Engine: "buf", Version: compat.Version, Policy: compat.Policy,
			Arguments: []string{"breaking", "after", "--against", "before"}, ExitCode: 100, Result: "breaking"},
	}}
}

func changeImpactCapabilities(t *testing.T, handler http.Handler) []string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	var version struct {
		Capabilities []string `json:"capabilities"`
	}
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &version) != nil {
		t.Fatalf("version = %d %s", recorder.Code, recorder.Body)
	}
	return version.Capabilities
}

func TestReleasedChangeImpactServesOnlyComposedReleasedPlanes(t *testing.T) {
	st, opts, lineage := fieldAdmissionFixture()
	checker := changeImpactChecker(lineage)
	opts.Version = "test"
	opts.Compatibility = checker
	opts.ChangeImpactReleased = true
	handler := api.New(opts)

	capabilities := changeImpactCapabilities(t, handler)
	for _, want := range []string{"contract-compatibility", "contract-impact-change", "protobuf-field-references"} {
		if !slices.Contains(capabilities, want) {
			t.Fatalf("capabilities %v lack %s", capabilities, want)
		}
	}
	if slices.Contains(capabilities, "contract-impact-report") {
		t.Fatalf("released claim advertised the experimental report: %v", capabilities)
	}

	request := compat.Request{
		Lineage: lineage,
		Before:  []compat.File{{Path: "cart.proto", Content: "syntax = \"proto3\";"}},
		After:   []compat.File{{Path: "cart.proto", Content: "syntax = \"proto3\";"}},
	}
	code, body, envelope := postCompatibility(t, handler, request)
	if code != http.StatusOK {
		t.Fatalf("released compatibility = %d %s", code, body)
	}
	if envelope.Bundle.Query.Kind != "check_contract_compatibility" || !slices.Equal(envelope.Bundle.Query.Domains, []string{"scip-proto-field"}) ||
		len(envelope.Bundle.Assertions) != 1 || envelope.Bundle.Compatibility == nil || envelope.Bundle.Compatibility.Compatible {
		t.Fatalf("released compatibility bundle = %+v", envelope.Bundle)
	}

	code, body, report := postImpactReport(t, handler, request)
	if code != http.StatusOK || report.Compatibility == nil || len(report.ResolvedEvidence) != 1 ||
		report.ResolvedEvidence[0].Domain != "scip-proto-field" {
		t.Fatalf("released change report = %d %s", code, body)
	}
	saved := "/api/contract_impact_reports/" + report.BundleID
	if code, body, reread := getImpactReport(t, handler, saved); code != http.StatusOK || reread.BundleID != report.BundleID {
		t.Fatalf("saved released change report = %d %s", code, body)
	}

	code, body, field := getImpactReport(t, handler, "/api/contract_impact_report?lineage="+lineage+"&message=shop.Cart&field_number=1")
	if code != http.StatusOK || len(field.ResolvedEvidence) != 1 {
		t.Fatalf("released field report = %d %s", code, body)
	}

	code, body, _ = getImpactReport(t, handler, "/api/contract_impact_report?operation=%2Fshop.Cart%2FGet")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "operation_impact_report") {
		t.Fatalf("released operation entry = %d %s, want 503 naming the operation report", code, body)
	}
	for _, call := range st.calls {
		if strings.Contains(call, "consumer") || strings.Contains(call, "caller") {
			t.Fatalf("released change impact read an unreleased domain: %s", call)
		}
	}

	declaration := request
	declaration.Lineage = "contract_declaration_v1_shop"
	if code, body, _ := postCompatibility(t, handler, declaration); code != http.StatusBadRequest {
		t.Fatalf("declaration lineage = %d %s, want 400", code, body)
	}
}

func TestReleasedChangeImpactStaysDarkWithoutEveryDependency(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.Options)
	}{
		{name: "released field without the claim", change: func(opts *api.Options) { opts.ChangeImpactReleased = false }},
		{name: "claim without a started checker", change: func(opts *api.Options) { opts.Compatibility = nil }},
		{name: "claim with only Thrift fields", change: func(opts *api.Options) {
			opts.ProtobufFieldReferencesReleased = false
			opts.ThriftFieldReferencesReleased = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, opts, lineage := fieldAdmissionFixture()
			opts.Version = "test"
			opts.Compatibility = changeImpactChecker(lineage)
			opts.ChangeImpactReleased = true
			test.change(&opts)
			handler := api.New(opts)
			capabilities := changeImpactCapabilities(t, handler)
			if slices.Contains(capabilities, "contract-impact-change") || slices.Contains(capabilities, "contract-compatibility") {
				t.Fatalf("capabilities %v advertise an unadmitted claim", capabilities)
			}
			request := compat.Request{Lineage: lineage, Before: []compat.File{{Path: "cart.proto", Content: "syntax = \"proto3\";"}},
				After: []compat.File{{Path: "cart.proto", Content: "syntax = \"proto3\";"}}}
			if code, _, _ := postCompatibility(t, handler, request); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
				t.Fatalf("compatibility route = %d, want absent", code)
			}
			if code, _, _ := getImpactReport(t, handler, "/api/contract_impact_report?lineage="+lineage+"&message=shop.Cart&field_number=1"); code != http.StatusNotFound {
				t.Fatalf("report route = %d, want absent", code)
			}
			if proof := api.NewProofService(opts); proof.CompatibilityAvailable() {
				t.Fatal("compatibility available without every dependency")
			}
		})
	}
	// The experimental surface is unaffected by the released flag.
	_, opts, _ := fieldAdmissionFixture()
	opts.ProtobufFieldReferencesReleased = false
	opts.Compatibility = &fixedCompatibility{}
	if proof := api.NewProofService(opts); !proof.CompatibilityAvailable() {
		t.Fatal("experimental compatibility lost")
	}
}
