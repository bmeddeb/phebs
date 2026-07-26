package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	contractImpactReportSchemaVersion = "contract-impact-report-v1"
	impactScopePhrase                 = "within the stated evidence scope"
)

var errUnsupportedImpactBundle = errors.New("proof bundle does not describe a contract impact question")

func proofCapabilities(opts Options) []string {
	service := NewProofService(opts)
	if service == nil {
		return nil
	}
	capabilities := []string{"contract-impact-report", "kafka-topic-usage"}
	if service.CompatibilityAvailable() {
		capabilities = append(capabilities, "contract-compatibility")
	}
	return capabilities
}

// ContractImpactReport is a deterministic presentation of one immutable,
// reauthorized proof bundle. It is not persisted separately: BundleID remains
// the content address and every citation resolves through the pinned commit in
// the bundle's evidence occurrence.
type ContractImpactReport struct {
	SchemaVersion        string                      `json:"schema_version"`
	BundleID             string                      `json:"bundle_id"`
	Query                ProofQuery                  `json:"query"`
	Conclusion           ImpactConclusion            `json:"conclusion"`
	KnownConsumers       []ImpactEvidenceRow         `json:"known_consumers"`
	UnresolvedCandidates []ImpactEvidenceRow         `json:"unresolved_candidates"`
	Compatibility        *compat.CompatibilityResult `json:"compatibility,omitempty"`
	Coverage             extract.CoverageCertificate `json:"coverage"`
	CoverageRows         []ImpactCoverageRow         `json:"coverage_rows"`
	Caveat               string                      `json:"caveat"`
}

// ImpactConclusion always binds its bounded statement to the exact coverage
// certificate rendered alongside it.
type ImpactConclusion struct {
	Text           string `json:"text"`
	CoverageDigest string `json:"coverage_digest"`
}

// ImpactEvidenceRow is one clickable source occurrence. Commit and line are
// immutable citation coordinates, not a mutable repository HEAD link.
type ImpactEvidenceRow struct {
	Kind           string `json:"kind"` // operation_call | field_reference | unresolved_candidate
	Domain         string `json:"domain"`
	Protocol       string `json:"protocol,omitempty"`
	AssertionID    string `json:"assertion_id"`
	EvidenceAtomID string `json:"evidence_atom_id"`
	Predicate      string `json:"predicate"`
	Object         string `json:"object"`
	Lineage        string `json:"lineage,omitempty"`
	Repository     string `json:"repository"`
	Commit         string `json:"commit"`
	Path           string `json:"path"`
	StartByte      int    `json:"start_byte"`
	EndByte        int    `json:"end_byte"`
	StartLine      int    `json:"start_line"`
	EndLine        int    `json:"end_line"`
	Tier           string `json:"tier"`
	CodeRole       string `json:"code_role,omitempty"`
	Classification string `json:"classification"`
	Reason         string `json:"reason,omitempty"`
	Fresh          bool   `json:"fresh"`
}

// ImpactCoverageRow makes unsupported, stale, failed, and unresolved scope
// visible without asking the UI to infer qualification from raw manifests.
type ImpactCoverageRow struct {
	Repository        string   `json:"repository"`
	Domain            string   `json:"domain"`
	State             string   `json:"state"` // covered | stale | failed | processing | unsupported | covered_with_failed_replacement
	Reason            string   `json:"reason,omitempty"`
	IndexedCommit     string   `json:"indexed_commit,omitempty"`
	EvidenceCommit    string   `json:"evidence_commit,omitempty"`
	RunID             string   `json:"run_id,omitempty"`
	Extractor         string   `json:"extractor,omitempty"`
	Protocols         []string `json:"protocols,omitempty"`
	Failures          []string `json:"failures,omitempty"`
	AssertionCount    int      `json:"assertion_count"`
	UnresolvedCount   int      `json:"unresolved_count"`
	CandidateFiles    int      `json:"candidate_file_count"`
	ReadFiles         int      `json:"read_file_count"`
	SourceScopeDigest string   `json:"source_scope_digest,omitempty"`
}

// BuildOperationImpactReport includes exact operation matches and same-method
// syntactic candidates the extractor deliberately could not disambiguate.
func (s *ProofService) BuildOperationImpactReport(ctx context.Context, operation string) (*ContractImpactReport, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("contract impact reports unavailable")
	}
	if err := validateOperation(operation); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	method := operation[strings.LastIndex(operation, "/")+1:]
	// Protocol-blind entry point: both registered consumer domains are
	// queried; a domain without published runs adds only honest no-run
	// coverage rows (T19.5).
	query := ProofQuery{
		Kind: "contract_impact_operation", Operation: operation,
		Domains: []string{"grpc-consumer", "thrift-consumer"},
	}
	thriftPack, _ := packForProtocol("thrift")
	service := strings.TrimPrefix(operation[:strings.LastIndex(operation, "/")], "/")
	envelope, err := buildProofBundle(ctx, s.opts, query, []assertionFilter{
		{Domain: "grpc-consumer", Predicate: "CALLS_OPERATION", Object: operation},
		{Domain: "grpc-consumer", Predicate: "UNRESOLVED_GRPC_CALL", Object: method},
		{Domain: "thrift-consumer", Predicate: "CALLS_OPERATION", Object: operation},
		// Thrift call abstentions carry canonical candidate operations; the
		// extractor never asks this layer to reproduce Go publicizing rules.
		{Domain: "thrift-consumer", Predicate: "UNRESOLVED_THRIFT_CALL",
			Object: packUnresolvedCallObject(thriftPack, service, method)},
	}, nil)
	if err != nil {
		return nil, err
	}
	return projectContractImpactReport(envelope)
}

// BuildFieldImpactReport reports references to one stable field identity.
func (s *ProofService) BuildFieldImpactReport(ctx context.Context, lineage, message string, fieldNumber int) (*ContractImpactReport, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("contract impact reports unavailable")
	}
	if err := validateFieldIdentity(lineage, message, fieldNumber); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query := ProofQuery{
		Kind: "contract_impact_field", Lineage: lineage, Message: message,
		FieldNumber: fieldNumber, Domains: []string{"scip-proto-field"},
	}
	envelope, err := createProofBundle(ctx, s.opts, query, assertionFilter{
		Domain: "scip-proto-field", Predicate: "REFERENCES_PROTO_FIELD",
		Object: message + "#" + strconv.Itoa(fieldNumber), Lineage: lineage,
	})
	if err != nil {
		return nil, err
	}
	return projectContractImpactReport(envelope)
}

// BuildChangeImpactReport reuses the pinned Buf proof engine, then projects
// its affected-field consumer evidence into the report shape.
func (s *ProofService) BuildChangeImpactReport(ctx context.Context, request compat.Request) (*ContractImpactReport, error) {
	envelope, err := s.CheckContractCompatibility(ctx, request)
	if err != nil {
		return nil, err
	}
	return projectContractImpactReport(envelope)
}

// ReadContractImpactReport reauthorizes the immutable bundle before deriving
// its report. A report URL is therefore no more a bearer credential than the
// underlying bundle ID.
func (s *ProofService) ReadContractImpactReport(ctx context.Context, id string) (*ContractImpactReport, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("contract impact reports unavailable")
	}
	envelope, err := readProofBundle(ctx, s.opts, id)
	if err != nil {
		return nil, err
	}
	report, err := projectContractImpactReport(envelope)
	if errors.Is(err, errUnsupportedImpactBundle) {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	return report, err
}

func registerContractImpactAPI(api huma.API, opts Options, service *ProofService) {
	if service == nil {
		return
	}
	type reportOut struct{ Body ContractImpactReport }
	type reportIn struct {
		Operation   string `query:"operation" maxLength:"1024" example:"/shop.Cart/Get"`
		Lineage     string `query:"lineage" maxLength:"1024"`
		Message     string `query:"message" maxLength:"1024" example:"shop.Cart"`
		FieldNumber int    `query:"field_number" minimum:"0" maximum:"536870911"`
	}
	huma.Get(api, "/api/contract_impact_report", func(ctx context.Context, in *reportIn) (*reportOut, error) {
		var report *ContractImpactReport
		var err error
		switch {
		case in.Operation != "" && in.Lineage == "" && in.Message == "" && in.FieldNumber == 0:
			report, err = service.BuildOperationImpactReport(ctx, in.Operation)
		case in.Operation == "" && in.Lineage != "" && in.Message != "" && in.FieldNumber > 0:
			report, err = service.BuildFieldImpactReport(ctx, in.Lineage, in.Message, in.FieldNumber)
		default:
			return nil, huma.Error400BadRequest("provide exactly one operation or one complete lineage/message/field_number identity")
		}
		if err != nil {
			return nil, err
		}
		return &reportOut{Body: *report}, nil
	})

	if service.CompatibilityAvailable() {
		type changeIn struct{ Body compat.Request }
		huma.Register(api, huma.Operation{
			OperationID: "build-contract-change-impact-report",
			Method:      "POST", Path: "/api/contract_impact_report",
			Summary:      "Build a bounded contract-change impact report",
			MaxBodyBytes: compatibilityBodyLimit,
		}, func(ctx context.Context, in *changeIn) (*reportOut, error) {
			report, err := service.BuildChangeImpactReport(ctx, in.Body)
			if err != nil {
				return nil, err
			}
			return &reportOut{Body: *report}, nil
		})
	}

	type savedIn struct {
		ID string `path:"id" minLength:"67" maxLength:"67"`
	}
	huma.Get(api, "/api/contract_impact_reports/{id}", func(ctx context.Context, in *savedIn) (*reportOut, error) {
		report, err := service.ReadContractImpactReport(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &reportOut{Body: *report}, nil
	})
}

func projectContractImpactReport(envelope *ProofBundleEnvelope) (*ContractImpactReport, error) {
	if envelope == nil || envelope.ID == "" || envelope.Bundle.SchemaVersion != proofBundleSchemaVersion {
		return nil, errors.New("project contract impact report: proof bundle is inconsistent")
	}
	switch envelope.Bundle.Query.Kind {
	case "contract_impact_operation", "contract_impact_field", "find_operation_consumers", "find_proto_field_references", "check_contract_compatibility":
	default:
		return nil, errUnsupportedImpactBundle
	}

	known, unresolved, err := projectImpactEvidence(envelope.Bundle)
	if err != nil {
		return nil, fmt.Errorf("project contract impact report: %w", err)
	}
	conclusion := impactConclusion(envelope.Bundle, len(known))
	return &ContractImpactReport{
		SchemaVersion: contractImpactReportSchemaVersion, BundleID: envelope.ID,
		Query:          envelope.Bundle.Query,
		Conclusion:     ImpactConclusion{Text: conclusion, CoverageDigest: envelope.Bundle.Coverage.Digest},
		KnownConsumers: known, UnresolvedCandidates: unresolved,
		Compatibility: envelope.Bundle.Compatibility,
		Coverage:      envelope.Bundle.Coverage, CoverageRows: impactCoverageRows(envelope.Bundle.Coverage),
		Caveat: envelope.Bundle.Caveat,
	}, nil
}

func impactConclusion(bundle ProofBundle, knownCount int) string {
	if bundle.Compatibility != nil {
		if bundle.Compatibility.Compatible {
			return "No wire-compatibility blockers were found " + impactScopePhrase + "."
		}
		return "Wire-compatibility blockers were found " + impactScopePhrase + "."
	}
	if knownCount > 0 {
		return "Known consumer evidence was found " + impactScopePhrase + "."
	}
	return "No matching consumer evidence was found " + impactScopePhrase + "; this does not establish absence."
}

type impactEvidenceKey struct {
	repo, runID, atomID string
}

type impactRunFreshness struct {
	domain   string
	protocol string
	commit   string
	fresh    bool
}

func projectImpactEvidence(bundle ProofBundle) ([]ImpactEvidenceRow, []ImpactEvidenceRow, error) {
	evidence := make(map[impactEvidenceKey]BundleEvidence, len(bundle.Evidence))
	for _, item := range bundle.Evidence {
		key := impactEvidenceKey{repo: item.Repository, runID: item.RunID, atomID: item.Atom.ID}
		if key.repo == "" || key.runID == "" || key.atomID == "" {
			return nil, nil, errors.New("proof evidence identity is incomplete")
		}
		if _, exists := evidence[key]; exists {
			return nil, nil, errors.New("proof evidence identity is duplicated")
		}
		evidence[key] = item
	}
	freshness := make(map[string]impactRunFreshness)
	for _, repo := range bundle.Coverage.Repositories {
		for _, run := range repo.Runs {
			if run.RunID != "" {
				freshness[repo.Repository+"\x00"+run.RunID] = impactRunFreshness{
					domain: run.Domain, protocol: impactProtocol(run.Domain),
					commit: run.Commit, fresh: run.Fresh,
				}
			}
		}
	}

	known := make([]ImpactEvidenceRow, 0)
	unresolved := make([]ImpactEvidenceRow, 0)
	seen := make(map[string]struct{})
	for _, assertion := range bundle.Assertions {
		kind, isUnresolved := impactAssertionKind(assertion.Predicate, assertion.Tier)
		if kind == "" {
			continue
		}
		classification, reason := impactEvidenceLabels(assertion)
		for _, atomID := range assertion.Supporting {
			item, ok := evidence[impactEvidenceKey{repo: assertion.Repo, runID: assertion.RunID, atomID: atomID}]
			if !ok {
				return nil, nil, fmt.Errorf("assertion %s support %s is missing", assertion.ID, atomID)
			}
			if item.Atom.StartByte < 0 || item.Atom.EndByte <= item.Atom.StartByte {
				return nil, nil, fmt.Errorf("assertion %s support %s atom span is inconsistent", assertion.ID, atomID)
			}
			if len(item.Occurrences) == 0 {
				return nil, nil, fmt.Errorf("assertion %s support %s has no occurrence", assertion.ID, atomID)
			}
			for _, occurrence := range item.Occurrences {
				if occurrence.AtomID != atomID || occurrence.Repo != assertion.Repo || occurrence.RunID != assertion.RunID ||
					occurrence.Commit == "" || occurrence.Path == "" || occurrence.StartLine <= 0 || occurrence.EndLine < occurrence.StartLine {
					return nil, nil, fmt.Errorf("assertion %s support %s occurrence is inconsistent", assertion.ID, atomID)
				}
				dedupe := assertion.ID + "\x00" + atomID + "\x00" + occurrence.ID
				if _, exists := seen[dedupe]; exists {
					continue
				}
				seen[dedupe] = struct{}{}
				run, ok := freshness[assertion.Repo+"\x00"+assertion.RunID]
				if !ok || run.domain == "" {
					return nil, nil, fmt.Errorf("assertion %s run is absent from coverage", assertion.ID)
				}
				row := ImpactEvidenceRow{
					Kind: kind, AssertionID: assertion.ID, EvidenceAtomID: atomID,
					Domain: run.domain, Protocol: run.protocol,
					Predicate: assertion.Predicate, Object: assertion.Object, Lineage: assertion.Lineage,
					Repository: occurrence.Repo, Commit: occurrence.Commit, Path: occurrence.Path,
					StartByte: item.Atom.StartByte, EndByte: item.Atom.EndByte,
					StartLine: occurrence.StartLine, EndLine: occurrence.EndLine,
					Tier: assertion.Tier, CodeRole: assertion.CodeRole,
					Classification: classification, Reason: reason,
					Fresh: run.fresh && run.commit == occurrence.Commit,
				}
				if isUnresolved {
					unresolved = append(unresolved, row)
				} else {
					known = append(known, row)
				}
			}
		}
	}
	sortImpactEvidence(known)
	sortImpactEvidence(unresolved)
	return known, unresolved, nil
}

func impactProtocol(domain string) string {
	for _, pack := range protocolPacks {
		if domain == pack.declarationDomain || domain == pack.consumerDomain {
			return pack.protocol
		}
	}
	if domain == "scip-proto-field" {
		return "protobuf"
	}
	return ""
}

func impactAssertionKind(predicate, tier string) (string, bool) {
	switch predicate {
	case "CALLS_OPERATION":
		return "operation_call", false
	case "REFERENCES_PROTO_FIELD":
		return "field_reference", false
	case "UNRESOLVED_GRPC_CALL", "UNRESOLVED_THRIFT_CALL":
		return "unresolved_candidate", true
	default:
		if tier == store.TierUnresolved {
			return "unresolved_candidate", true
		}
		return "", false
	}
}

func impactEvidenceLabels(assertion store.Assertion) (classification, reason string) {
	var detail struct {
		Classification string `json:"classification"`
		Method         string `json:"method"`
		CandidateCount int    `json:"candidate_count"`
	}
	_ = json.Unmarshal([]byte(assertion.Detail), &detail)
	switch assertion.Predicate {
	case "CALLS_OPERATION":
		return "operation call", ""
	case "REFERENCES_PROTO_FIELD":
		if detail.Classification == "" {
			detail.Classification = "unknown access"
		}
		return detail.Classification, ""
	case "UNRESOLVED_GRPC_CALL", "UNRESOLVED_THRIFT_CALL":
		if detail.Method != "" && detail.CandidateCount > 0 {
			return "ambiguous operation call", fmt.Sprintf("method %s matches %d generated services", detail.Method, detail.CandidateCount)
		}
		return "ambiguous operation call", "extractor abstained from service resolution"
	default:
		return "unresolved", "extractor abstained"
	}
}

func sortImpactEvidence(rows []ImpactEvidenceRow) {
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if left.Repository != right.Repository {
			return left.Repository < right.Repository
		}
		if left.Domain != right.Domain {
			return left.Domain < right.Domain
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.StartLine != right.StartLine {
			return left.StartLine < right.StartLine
		}
		if left.AssertionID != right.AssertionID {
			return left.AssertionID < right.AssertionID
		}
		return left.EvidenceAtomID < right.EvidenceAtomID
	})
}

func impactCoverageRows(certificate extract.CoverageCertificate) []ImpactCoverageRow {
	rows := make([]ImpactCoverageRow, 0, certificate.RepositoryCount*len(certificate.Domains))
	for _, repo := range certificate.Repositories {
		for _, run := range repo.Runs {
			state, reason := impactCoverageState(repo, run)
			rows = append(rows, ImpactCoverageRow{
				Repository: repo.Repository, Domain: run.Domain, State: state, Reason: reason,
				IndexedCommit: repo.IndexedCommit, EvidenceCommit: run.Commit,
				RunID: run.RunID, Extractor: run.Extractor,
				Protocols: append([]string(nil), run.Protocols...), Failures: append([]string(nil), run.Failures...),
				AssertionCount: run.AssertionCount, UnresolvedCount: run.UnresolvedCount,
				CandidateFiles: run.CandidateFileCount, ReadFiles: run.ReadFileCount,
				SourceScopeDigest: run.SourceScopeDigest,
			})
		}
	}
	return rows
}

func impactCoverageState(repo extract.CertificateRepository, run extract.CertificateRun) (state, reason string) {
	if run.Status != "published" {
		if run.LatestAttempt != nil {
			switch run.LatestAttempt.Status {
			case "staged":
				return "processing", "the latest extraction attempt has not published"
			case "aborted":
				return "failed", run.LatestAttempt.Failure
			}
		}
		return "unsupported", "no published evidence for this domain"
	}
	if !run.Fresh {
		return "stale", "evidence commit does not match the indexed revision"
	}
	if run.Domain == "scip-proto-field" && repo.SCIPIndex == "absent" {
		return "unsupported", "repository has no committed index.scip"
	}
	if len(run.Failures) > 0 {
		return "failed", strings.Join(run.Failures, "; ")
	}
	if run.LatestAttempt != nil && run.LatestAttempt.Status == "aborted" && run.LatestAttempt.RunID != run.RunID {
		return "covered_with_failed_replacement", run.LatestAttempt.Failure
	}
	return "covered", ""
}
