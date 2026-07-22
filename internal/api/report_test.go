package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/store"
)

func getImpactReport(t *testing.T, handler http.Handler, target string) (int, string, api.ContractImpactReport) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	var report api.ContractImpactReport
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
	}
	return recorder.Code, recorder.Body.String(), report
}

func postImpactReport(t *testing.T, handler http.Handler, request compat.Request) (int, string, api.ContractImpactReport) {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/contract_impact_report", strings.NewReader(string(encoded)))
	httpRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, httpRequest)
	var report api.ContractImpactReport
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
	}
	return recorder.Code, recorder.Body.String(), report
}

func TestContractImpactOperationReportIsPinnedPermissionSafeAndComplete(t *testing.T) {
	const (
		visibleRepo = "github.com/allowed/cart-client"
		unsupported = "github.com/allowed/no-go-client"
		hiddenRepo  = "github.com/secret/cart-client"
		operation   = "/shop.Cart/Get"
		domain      = "grpc-consumer"
	)
	visibleRun, hiddenRun := proofRun(visibleRepo, domain, "run-visible-impact"), proofRun(hiddenRepo, domain, "run-hidden-impact")
	visibleRun.Coverage.UnresolvedCount = 1
	visibleRun.Coverage.AssertionCount = 2
	visibleRun.Coverage.AtomCount = 2
	known, knownResolution := proofAssertion(visibleRepo, visibleRun.ID, "known-impact", "CALLS_OPERATION", operation, "lineage-visible", `{"schema":"grpcgo-call-detail-v1","method":"Get"}`)
	unresolved, unresolvedResolution := proofAssertion(visibleRepo, visibleRun.ID, "unresolved-impact", "UNRESOLVED_GRPC_CALL", "Get", "", `{"schema":"grpcgo-call-ambiguity-v1","method":"Get","candidate_count":2}`)
	unresolved.Tier = store.TierUnresolved
	hidden, hiddenResolution := proofAssertion(hiddenRepo, hiddenRun.ID, "hidden-impact", "CALLS_OPERATION", operation, "lineage-hidden", "secret detail")
	st := &proofAPIStore{
		repos: []store.Repo{
			{Name: hiddenRepo, IndexedCommitHash: hiddenRun.Commit},
			{Name: unsupported, IndexedCommitHash: strings.Repeat("d", 40)},
			{Name: visibleRepo, IndexedCommitHash: visibleRun.Commit},
		},
		runs: map[string]store.ExtractionRun{
			proofScope(visibleRepo, domain): visibleRun,
			proofScope(hiddenRepo, domain):  hiddenRun,
		},
		assertions: map[string][]store.Assertion{
			visibleRepo: {unresolved, known}, hiddenRepo: {hidden},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(visibleRepo, visibleRun.ID, known.Supporting[0]):      knownResolution,
			proofEvidenceScope(visibleRepo, visibleRun.ID, unresolved.Supporting[0]): unresolvedResolution,
			proofEvidenceScope(hiddenRepo, hiddenRun.ID, hidden.Supporting[0]):       hiddenResolution,
		},
	}
	visible := map[string]bool{visibleRepo: true, unsupported: true}
	handler := proofHandler(st, "user:member", &visible)

	code, body, report := getImpactReport(t, handler, "/api/contract_impact_report?operation=%2Fshop.Cart%2FGet")
	if code != http.StatusOK {
		t.Fatalf("operation report = %d %s", code, body)
	}
	if report.SchemaVersion != "contract-impact-report-v1" || report.BundleID == "" || report.Query.Operation != operation {
		t.Fatalf("report identity = %+v", report)
	}
	if len(report.KnownConsumers) != 1 || len(report.UnresolvedCandidates) != 1 {
		t.Fatalf("report evidence = known=%+v unresolved=%+v", report.KnownConsumers, report.UnresolvedCandidates)
	}
	consumer := report.KnownConsumers[0]
	if consumer.Repository != visibleRepo || consumer.Path != "consumer/known-impact.go" ||
		consumer.StartByte != 0 || consumer.EndByte != 4 || consumer.StartLine != 7 ||
		consumer.Commit != visibleRun.Commit || !consumer.Fresh ||
		consumer.Classification != "operation call" || consumer.Tier != store.TierExact {
		t.Fatalf("known consumer citation = %+v", consumer)
	}
	candidate := report.UnresolvedCandidates[0]
	if candidate.Repository != visibleRepo || candidate.Tier != store.TierUnresolved ||
		candidate.Reason != "method Get matches 2 generated services" {
		t.Fatalf("unresolved candidate = %+v", candidate)
	}
	coverageByRepo := make(map[string]api.ImpactCoverageRow, len(report.CoverageRows))
	for _, row := range report.CoverageRows {
		coverageByRepo[row.Repository] = row
	}
	if len(report.CoverageRows) != 2 || coverageByRepo[unsupported].State != "unsupported" ||
		coverageByRepo[visibleRepo].State != "covered" || coverageByRepo[visibleRepo].UnresolvedCount != 1 {
		t.Fatalf("coverage rows = %+v", report.CoverageRows)
	}
	if !strings.Contains(report.Conclusion.Text, "within the stated evidence scope") ||
		report.Conclusion.CoverageDigest != report.Coverage.Digest ||
		strings.Contains(body, hiddenRepo) || strings.Contains(body, "secret detail") {
		t.Fatalf("report qualification or visibility failed: %s", body)
	}

	readCode, readBody, read := getImpactReport(t, handler, "/api/contract_impact_reports/"+report.BundleID)
	if readCode != http.StatusOK || readBody != body || read.BundleID != report.BundleID {
		t.Fatalf("pinned report read differs: create=%d %s read=%d %s", code, body, readCode, readBody)
	}
	repeatCode, repeatBody, _ := getImpactReport(t, handler, "/api/contract_impact_report?operation=%2Fshop.Cart%2FGet")
	if repeatCode != http.StatusOK || repeatBody != body {
		t.Fatalf("repeated report is not byte-identical: %d %s", repeatCode, repeatBody)
	}

	visible = map[string]bool{}
	if revokedCode, revokedBody, _ := getImpactReport(t, handler, "/api/contract_impact_reports/"+report.BundleID); revokedCode != http.StatusNotFound || strings.Contains(revokedBody, visibleRepo) {
		t.Fatalf("revoked report read = %d %s", revokedCode, revokedBody)
	}
}

func TestContractImpactFieldReportUsesStableIdentityAndExactSpan(t *testing.T) {
	const (
		repo        = "github.com/allowed/cart-reader"
		domain      = "scip-proto-field"
		message     = "shop.Cart"
		fieldNumber = 7
	)
	lineage := "contract_scip_package_v1_" + strings.Repeat("e", 64)
	run := proofRun(repo, domain, "run-field-report")
	run.Coverage.Protocols = []string{"scip"}
	field, resolution := proofAssertion(repo, run.ID, "field-report", "REFERENCES_PROTO_FIELD", message+"#7", lineage,
		`{"schema":"proto-field-reference-detail-v1","classification":"write"}`)
	st := &proofAPIStore{
		repos:      []store.Repo{{Name: repo, IndexedCommitHash: run.Commit}},
		runs:       map[string]store.ExtractionRun{proofScope(repo, domain): run},
		assertions: map[string][]store.Assertion{repo: {field}},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, run.ID, field.Supporting[0]): resolution,
		},
	}
	target := "/api/contract_impact_report?lineage=" + lineage + "&message=" + message + "&field_number=7"
	code, body, report := getImpactReport(t, proofHandler(st, "user:member", nil), target)
	if code != http.StatusOK {
		t.Fatalf("field report = %d %s", code, body)
	}
	if report.Query.Kind != "contract_impact_field" || report.Query.Lineage != lineage ||
		report.Query.Message != message || report.Query.FieldNumber != fieldNumber || len(report.KnownConsumers) != 1 {
		t.Fatalf("field report identity = %+v", report)
	}
	consumer := report.KnownConsumers[0]
	if consumer.Kind != "field_reference" || consumer.Classification != "write" ||
		consumer.StartByte != 0 || consumer.EndByte != 4 || consumer.StartLine != 7 || !consumer.Fresh {
		t.Fatalf("field report evidence = %+v", consumer)
	}
}

func TestContractChangeImpactReportUsesCompatibilityProof(t *testing.T) {
	const (
		repo    = "github.com/allowed/cart-client"
		domain  = "scip-proto-field"
		lineage = "contract_scip_package_v1_cart"
		message = "shop.Cart"
	)
	run := proofRun(repo, domain, "run-field-impact")
	field, resolution := proofAssertion(repo, run.ID, "field-impact", "REFERENCES_PROTO_FIELD", message+"#1", lineage,
		`{"schema":"proto-field-reference-detail-v1","name":"id","classification":"read","dependency_version":"v1.0.0"}`)
	checker := &fixedCompatibility{result: compat.CompatibilityResult{
		Compatible: false,
		Before:     compat.InputSnapshot{Digest: "sha256:before", Files: []compat.InputFile{}},
		After:      compat.InputSnapshot{Digest: "sha256:after", Files: []compat.InputFile{}},
		Violations: []compat.Violation{{
			Snapshot: "after", Path: "cart.proto", StartLine: 4, StartColumn: 3, EndLine: 4, EndColumn: 18,
			Rule: "FIELD_SAME_TYPE", Message: "field type changed",
			Field: &compat.FieldIdentity{Lineage: lineage, Message: message, Number: 1},
		}},
		AffectedFields: []compat.FieldIdentity{{Lineage: lineage, Message: message, Number: 1}},
		Run:            compat.Run{Engine: "buf", Version: compat.Version, Policy: compat.Policy, Arguments: []string{"breaking", "after", "--against", "before"}, ExitCode: 100, Result: "breaking"},
	}}
	st := &proofAPIStore{
		repos:      []store.Repo{{Name: repo, IndexedCommitHash: run.Commit}},
		runs:       map[string]store.ExtractionRun{proofScope(repo, domain): run},
		assertions: map[string][]store.Assertion{repo: {field}},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, run.ID, field.Supporting[0]): resolution,
		},
	}
	handler := proofHandler(st, "user:member", nil, checker)
	request := compat.Request{
		Lineage: lineage,
		Before:  []compat.File{{Path: "cart.proto", Content: "syntax = \"proto3\";"}},
		After:   []compat.File{{Path: "cart.proto", Content: "syntax = \"proto3\";"}},
	}
	code, body, report := postImpactReport(t, handler, request)
	if code != http.StatusOK {
		t.Fatalf("change report = %d %s", code, body)
	}
	if report.Compatibility == nil || report.Compatibility.Compatible || len(report.Compatibility.Violations) != 1 ||
		len(report.KnownConsumers) != 1 || report.KnownConsumers[0].Classification != "read" ||
		!strings.Contains(report.Conclusion.Text, "blockers were found within the stated evidence scope") {
		t.Fatalf("change report = %+v", report)
	}
	if checker.request.Lineage != lineage || len(checker.request.Before) != 1 || len(checker.request.After) != 1 {
		t.Fatalf("compatibility request = %+v", checker.request)
	}
}

func TestContractImpactCapabilityAndRoutesRemainDark(t *testing.T) {
	st := &proofAPIStore{}
	dark := api.New(api.Options{Version: "test", Store: st})
	for _, target := range []string{
		"/api/contract_impact_report?operation=%2Fshop.Cart%2FGet",
		"/api/contract_impact_reports/pb_" + strings.Repeat("0", 64),
	} {
		if code, _, _ := getImpactReport(t, dark, target); code != http.StatusNotFound {
			t.Fatalf("dark report route %s = %d, want 404", target, code)
		}
	}
	version := httptest.NewRecorder()
	dark.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if strings.Contains(version.Body.String(), "contract-impact-report") {
		t.Fatalf("dark version advertised impact capability: %s", version.Body)
	}

	anonymous := proofHandler(st, "", nil)
	version = httptest.NewRecorder()
	anonymous.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if strings.Contains(version.Body.String(), "contract-impact-report") {
		t.Fatalf("anonymous version advertised impact capability: %s", version.Body)
	}

	enabled := proofHandler(st, "user:member", nil)
	version = httptest.NewRecorder()
	enabled.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if !strings.Contains(version.Body.String(), "contract-impact-report") || strings.Contains(version.Body.String(), "contract-compatibility") {
		t.Fatalf("enabled version capabilities = %s", version.Body)
	}
}
