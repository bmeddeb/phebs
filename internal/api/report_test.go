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

// T19.5: thrift consumer evidence flows through the protocol-blind operation
// impact report with the same kinds and labels grpc evidence receives.
func TestContractImpactOperationReportCoversThriftConsumers(t *testing.T) {
	const (
		repo      = "github.com/allowed/thrift-client"
		operation = "/agent.Agent/emitBatch"
		domain    = "thrift-consumer"
	)
	run := proofRun(repo, domain, "run-thrift-impact")
	run.Coverage.UnresolvedCount = 1
	run.Coverage.AssertionCount = 2
	run.Coverage.AtomCount = 2
	known, knownResolution := proofAssertion(
		repo, run.ID, "thrift-call", "CALLS_OPERATION", operation, "lineage-thrift",
		`{"schema":"thriftgo-call-detail-v1","method":"emitBatch","go_method":"EmitBatch"}`,
	)
	known.Tier = store.TierHeuristic
	ambiguous, ambiguousResolution := proofAssertion(
		repo, run.ID, "thrift-ambiguous", "UNRESOLVED_THRIFT_CALL", operation, "",
		`{"schema":"thriftgo-call-ambiguity-v1","method":"EmitBatch","candidate_count":2,"candidate_operation":"/agent.Agent/emitBatch"}`,
	)
	ambiguous.Tier = store.TierUnresolved
	st := &proofAPIStore{
		repos: []store.Repo{{Name: repo, IndexedCommitHash: run.Commit}},
		runs:  map[string]store.ExtractionRun{proofScope(repo, domain): run},
		assertions: map[string][]store.Assertion{
			repo: {ambiguous, known},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, run.ID, known.Supporting[0]):     knownResolution,
			proofEvidenceScope(repo, run.ID, ambiguous.Supporting[0]): ambiguousResolution,
		},
	}
	visible := map[string]bool{repo: true}
	handler := proofHandler(st, "user:member", &visible)

	code, body, report := getImpactReport(
		t, handler, "/api/contract_impact_report?operation=%2Fagent.Agent%2FemitBatch",
	)
	if code != http.StatusOK {
		t.Fatalf("thrift operation report = %d %s", code, body)
	}
	if len(report.MatchingCallEvidence) != 1 || len(report.ExtractorAbstentions) != 1 {
		t.Fatalf("thrift evidence = matching=%+v abstentions=%+v",
			report.MatchingCallEvidence, report.ExtractorAbstentions)
	}
	consumer := report.MatchingCallEvidence[0]
	if consumer.Kind != "operation_call" || consumer.Classification != "matching_call_evidence" ||
		consumer.Tier != store.TierHeuristic ||
		consumer.Domain != domain || consumer.Protocol != "thrift" {
		t.Fatalf("thrift consumer row = %+v", consumer)
	}
	candidate := report.ExtractorAbstentions[0]
	if candidate.Kind != "unresolved_candidate" ||
		candidate.Classification != "extractor_abstention" ||
		candidate.Domain != domain || candidate.Protocol != "thrift" ||
		candidate.Reason != "method EmitBatch matches 2 generated services" {
		t.Fatalf("thrift candidate row = %+v", candidate)
	}
}

func TestContractImpactOperationReportSeparatesResolvedCallerAndAbstention(t *testing.T) {
	const (
		repo      = "github.com/allowed/typed-client"
		operation = "/shop.Cart/Get"
		domain    = "grpc-caller"
		lineage   = "provisional_repo_path_v1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	run := proofRun(repo, domain, "run-typed-impact")
	run.Coverage.AssertionCount = 2
	run.Coverage.UnresolvedCount = 1
	run.Coverage.AtomCount = 2
	resolved, resolvedEvidence := proofAssertion(
		repo, run.ID, "typed-impact", "CALLS_OPERATION", operation, lineage,
		`{"schema":"go-caller-detail-v1","resolution":"syntax","protocol":"grpc","unit_state":"unavailable"}`,
	)
	resolved.Tier = store.TierHeuristic
	abstention, abstentionEvidence := proofAssertion(
		repo, run.ID, "typed-abstention", "UNRESOLVED_CALLER", operation, "",
		`{"schema":"go-caller-detail-v1","resolution":"syntax","protocol":"grpc","unit_state":"unavailable","unresolved_reason":"unsupported_receiver_flow"}`,
	)
	abstention.Tier = store.TierUnresolved
	st := &proofAPIStore{
		repos: []store.Repo{{Name: repo, IndexedCommitHash: run.Commit}},
		runs:  map[string]store.ExtractionRun{proofScope(repo, domain): run},
		assertions: map[string][]store.Assertion{
			repo: {abstention, resolved},
		},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, run.ID, resolved.Supporting[0]):   resolvedEvidence,
			proofEvidenceScope(repo, run.ID, abstention.Supporting[0]): abstentionEvidence,
		},
	}
	code, body, report := getImpactReport(
		t,
		proofHandler(st, "user:member", nil),
		"/api/contract_impact_report?operation=%2Fshop.Cart%2FGet",
	)
	if code != http.StatusOK {
		t.Fatalf("typed caller report = %d %s", code, body)
	}
	if len(report.ResolvedEvidence) != 1 ||
		report.ResolvedEvidence[0].Classification != "resolved_caller" ||
		report.ResolvedEvidence[0].Lineage != lineage ||
		len(report.MatchingCallEvidence) != 0 ||
		len(report.ExtractorAbstentions) != 1 ||
		report.ExtractorAbstentions[0].Classification != "extractor_abstention" ||
		report.ExtractorAbstentions[0].Reason != "unsupported_receiver_flow" {
		t.Fatalf("typed caller vocabulary = %+v", report)
	}
}

func TestContractImpactOperationReportPreservesProtocolIdentityForEqualObjects(t *testing.T) {
	const (
		repo      = "github.com/allowed/mixed-client"
		operation = "/agent.Agent/Get"
	)
	grpcRun := proofRun(repo, "grpc-consumer", "run-grpc-equal-object")
	thriftRun := proofRun(repo, "thrift-consumer", "run-thrift-equal-object")
	grpc, grpcResolution := proofAssertion(
		repo, grpcRun.ID, "grpc-equal-object", "CALLS_OPERATION", operation, "grpc-lineage",
		`{"schema":"grpcgo-call-detail-v1","method":"Get"}`,
	)
	thrift, thriftResolution := proofAssertion(
		repo, thriftRun.ID, "thrift-equal-object", "CALLS_OPERATION", operation, "thrift-lineage",
		`{"schema":"thriftgo-call-detail-v1","method":"Get","go_method":"Get"}`,
	)
	thrift.Tier = store.TierHeuristic
	st := &proofAPIStore{
		repos: []store.Repo{{Name: repo, IndexedCommitHash: grpcRun.Commit}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, grpcRun.Domain):   grpcRun,
			proofScope(repo, thriftRun.Domain): thriftRun,
		},
		assertions: map[string][]store.Assertion{repo: {grpc, thrift}},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, grpcRun.ID, grpc.Supporting[0]):     grpcResolution,
			proofEvidenceScope(repo, thriftRun.ID, thrift.Supporting[0]): thriftResolution,
		},
	}
	visible := map[string]bool{repo: true}

	code, body, report := getImpactReport(
		t,
		proofHandler(st, "user:member", &visible),
		"/api/contract_impact_report?operation=%2Fagent.Agent%2FGet",
	)
	if code != http.StatusOK {
		t.Fatalf("mixed-protocol operation report = %d %s", code, body)
	}
	if len(report.MatchingCallEvidence) != 2 {
		t.Fatalf("matching call evidence = %+v", report.MatchingCallEvidence)
	}
	got := make(map[string]string, len(report.MatchingCallEvidence))
	for _, row := range report.MatchingCallEvidence {
		if row.Object != operation {
			t.Fatalf("operation object = %q", row.Object)
		}
		got[row.Domain] = row.Protocol
	}
	want := map[string]string{
		"grpc-consumer":   "protobuf",
		"thrift-consumer": "thrift",
	}
	if len(got) != len(want) {
		t.Fatalf("protocol identities = %+v", got)
	}
	for domain, protocol := range want {
		if got[domain] != protocol {
			t.Fatalf("protocol identity for %s = %q, want %q", domain, got[domain], protocol)
		}
	}
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
	if report.SchemaVersion != "contract-impact-report-v2" || report.BundleID == "" || report.Query.Operation != operation {
		t.Fatalf("report identity = %+v", report)
	}
	if len(report.MatchingCallEvidence) != 1 || len(report.ExtractorAbstentions) != 1 {
		t.Fatalf("report evidence = matching=%+v abstentions=%+v",
			report.MatchingCallEvidence, report.ExtractorAbstentions)
	}
	consumer := report.MatchingCallEvidence[0]
	if consumer.Repository != visibleRepo || consumer.Path != "consumer/known-impact.go" ||
		consumer.StartByte != 0 || consumer.EndByte != 4 || consumer.StartLine != 7 ||
		consumer.Commit != visibleRun.Commit || !consumer.Fresh ||
		consumer.Domain != domain || consumer.Protocol != "protobuf" ||
		consumer.Classification != "matching_call_evidence" || consumer.Tier != store.TierExact {
		t.Fatalf("matching call citation = %+v", consumer)
	}
	candidate := report.ExtractorAbstentions[0]
	if candidate.Repository != visibleRepo || candidate.Tier != store.TierUnresolved ||
		candidate.Domain != domain || candidate.Protocol != "protobuf" ||
		candidate.Reason != "method Get matches 2 generated services" {
		t.Fatalf("unresolved candidate = %+v", candidate)
	}
	// T20.10: the protocol-blind impact query covers both legacy consumer and
	// declaration-proven caller domains. Dark domains appear only as honest
	// per-repo no-run rows.
	coverageByScope := make(map[string]api.ImpactCoverageRow, len(report.CoverageRows))
	for _, row := range report.CoverageRows {
		coverageByScope[row.Repository+"/"+row.Domain] = row
	}
	if len(report.CoverageRows) != 8 ||
		coverageByScope[unsupported+"/grpc-caller"].State != "unsupported" ||
		coverageByScope[unsupported+"/grpc-consumer"].State != "unsupported" ||
		coverageByScope[visibleRepo+"/grpc-caller"].State != "unsupported" ||
		coverageByScope[visibleRepo+"/grpc-consumer"].State != "covered" ||
		coverageByScope[visibleRepo+"/grpc-consumer"].UnresolvedCount != 1 ||
		coverageByScope[visibleRepo+"/thrift-caller"].State != "unsupported" ||
		coverageByScope[visibleRepo+"/thrift-consumer"].State != "unsupported" ||
		coverageByScope[unsupported+"/thrift-caller"].State != "unsupported" ||
		coverageByScope[unsupported+"/thrift-consumer"].State != "unsupported" {
		t.Fatalf("coverage rows = %+v", report.CoverageRows)
	}
	if !strings.Contains(report.Conclusion.Text, "within the stated evidence scope") ||
		report.Conclusion.CoverageDigest != report.Coverage.Digest ||
		strings.Contains(body, `"known_consumers"`) ||
		strings.Contains(body, `"unresolved_candidates"`) ||
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
		report.Query.Message != message || report.Query.FieldNumber == nil ||
		*report.Query.FieldNumber != fieldNumber ||
		len(report.ResolvedEvidence) != 1 {
		t.Fatalf("field report identity = %+v", report)
	}
	consumer := report.ResolvedEvidence[0]
	if consumer.Kind != "field_reference" || consumer.Classification != "resolved_field_reference" ||
		consumer.Reason != "write" ||
		consumer.Domain != domain || consumer.Protocol != "protobuf" ||
		consumer.StartByte != 0 || consumer.EndByte != 4 || consumer.StartLine != 7 || !consumer.Fresh {
		t.Fatalf("field report evidence = %+v", consumer)
	}
}

func TestContractImpactThriftFieldZeroReportAndNeutralBundleProjection(
	t *testing.T,
) {
	const (
		repo    = "github.com/allowed/thrift-zero-report"
		domain  = "scip-thrift-field"
		message = "agent.Result"
	)
	lineage := "contract_scip_package_v1_" + strings.Repeat("f", 64)
	run := proofRun(repo, domain, "run-thrift-zero-report")
	run.Coverage.Protocols = []string{"scip", "thrift"}
	field, resolution := proofAssertion(
		repo,
		run.ID,
		"thrift-zero-report",
		"REFERENCES_THRIFT_FIELD",
		message+"#0",
		lineage,
		`{"schema":"thrift-field-reference-detail-v1","name":"success","classification":"read","generator":"thriftrw","source_binding":"module_digest"}`,
	)
	st := &proofAPIStore{
		repos: []store.Repo{{
			Name: repo, IndexedCommitHash: run.Commit,
		}},
		runs: map[string]store.ExtractionRun{
			proofScope(repo, domain): run,
		},
		assertions: map[string][]store.Assertion{repo: {field}},
		resolutions: map[string]store.EvidenceResolution{
			proofEvidenceScope(repo, run.ID, field.Supporting[0]): resolution,
		},
	}
	handler := proofHandler(st, "user:thrift-zero-report", nil)
	target := "/api/contract_impact_report?lineage=" + lineage +
		"&message=" + message + "&field_number=0"
	code, body, report := getImpactReport(t, handler, target)
	if code != http.StatusOK {
		t.Fatalf("Thrift field-zero report = %d %s", code, body)
	}
	if report.Query.Kind != "contract_impact_field" ||
		report.Query.FieldNumber == nil ||
		*report.Query.FieldNumber != 0 ||
		strings.Join(report.Query.Domains, ",") != domain ||
		len(report.ResolvedEvidence) != 1 {
		t.Fatalf("Thrift field-zero report identity = %+v", report)
	}
	row := report.ResolvedEvidence[0]
	if row.Kind != "field_reference" ||
		row.Protocol != "thrift" ||
		row.Domain != domain ||
		row.Predicate != "REFERENCES_THRIFT_FIELD" ||
		row.Classification != "resolved_field_reference" ||
		row.Reason != "read" ||
		row.Path != "consumer/thrift-zero-report.go" ||
		row.StartLine != 7 {
		t.Fatalf("Thrift field-zero report row = %+v", row)
	}
	repeatCode, repeatBody, _ := getImpactReport(t, handler, target)
	if repeatCode != http.StatusOK || repeatBody != body {
		t.Fatalf(
			"Thrift field-zero report is not deterministic: %d %s",
			repeatCode,
			repeatBody,
		)
	}

	proofCode, _, proof := getProof(
		t,
		handler,
		"/api/find_field_references?lineage="+lineage+
			"&message="+message+"&field_number=0",
	)
	if proofCode != http.StatusOK {
		t.Fatalf("neutral field-zero proof = %d", proofCode)
	}
	savedCode, _, saved := getImpactReport(
		t,
		handler,
		"/api/contract_impact_reports/"+proof.ID,
	)
	if savedCode != http.StatusOK ||
		saved.Query.Kind != "find_field_references" ||
		len(saved.ResolvedEvidence) != 1 ||
		saved.ResolvedEvidence[0].Protocol != "thrift" {
		t.Fatalf(
			"neutral bundle report projection = %d %+v",
			savedCode,
			saved,
		)
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
		len(report.ResolvedEvidence) != 1 ||
		report.ResolvedEvidence[0].Classification != "resolved_field_reference" ||
		report.ResolvedEvidence[0].Reason != "read" ||
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
	if strings.Contains(version.Body.String(), "contract-impact-report") ||
		strings.Contains(version.Body.String(), "kafka-topic-usage") {
		t.Fatalf("dark version advertised proof capabilities: %s", version.Body)
	}

	anonymous := proofHandler(st, "", nil)
	version = httptest.NewRecorder()
	anonymous.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if strings.Contains(version.Body.String(), "contract-impact-report") ||
		strings.Contains(version.Body.String(), "kafka-topic-usage") {
		t.Fatalf("anonymous version advertised proof capabilities: %s", version.Body)
	}

	enabled := proofHandler(st, "user:member", nil)
	version = httptest.NewRecorder()
	enabled.ServeHTTP(version, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if !strings.Contains(version.Body.String(), "contract-impact-report") ||
		!strings.Contains(version.Body.String(), "kafka-topic-usage") ||
		strings.Contains(version.Body.String(), "contract-compatibility") {
		t.Fatalf("enabled version capabilities = %s", version.Body)
	}
}
