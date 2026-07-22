package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	proofBundleSchemaVersion = "proof-bundle-v1"
	proofQueryAssertionLimit = 5000
	proofQueryEvidenceLimit  = 20_000
	proofBuildAttempts       = 3
	proofCaveat              = "Provisional evidence only; no absence, compatibility, migration-complete, or decommissioning conclusion."
)

var errProofQueryLimit = errors.New("proof query result exceeds its bounded limit")

// VisibilityContext records the authorization inputs under which the bundle
// was computed. Digests are deterministic and disclose no hidden repo names.
type VisibilityContext struct {
	Principal                  string `json:"principal"`
	AuthorizationProvider      string `json:"authorization_provider"`
	PermissionSnapshot         string `json:"permission_snapshot"`
	VisibleRepositorySetDigest string `json:"visible_repository_set_digest"`
}

// ProofQuery is the canonical question embedded in a proof bundle.
type ProofQuery struct {
	Kind        string   `json:"kind"`
	Operation   string   `json:"operation,omitempty"`
	Lineage     string   `json:"lineage,omitempty"`
	Message     string   `json:"message,omitempty"`
	FieldNumber int      `json:"field_number,omitempty"`
	Domains     []string `json:"domains"`
}

// BundleExtractor explicitly binds extractor versions to exact published
// runs rather than requiring consumers to infer them from coverage fields.
type BundleExtractor struct {
	Repository string `json:"repository"`
	Domain     string `json:"domain"`
	RunID      string `json:"run_id"`
	Extractor  string `json:"extractor"`
}

// BundleEvidence makes assertion atom references self-contained.
type BundleEvidence struct {
	Repository  string                   `json:"repository"`
	RunID       string                   `json:"run_id"`
	Atom        store.EvidenceAtom       `json:"atom"`
	Occurrences []store.SnapshotEvidence `json:"occurrences"`
}

// ProofBundle is canonical content. Its ID is sha256 over the JSON encoding
// of this value and therefore lives only in ProofBundleEnvelope.
type ProofBundle struct {
	SchemaVersion     string                      `json:"schema_version"`
	Query             ProofQuery                  `json:"query"`
	Assertions        []store.Assertion           `json:"assertions"`
	Evidence          []BundleEvidence            `json:"evidence"`
	Coverage          extract.CoverageCertificate `json:"coverage"`
	ExtractorVersions []BundleExtractor           `json:"extractor_versions"`
	VisibilityContext VisibilityContext           `json:"visibility_context"`
	Caveat            string                      `json:"caveat"`
}

// ProofBundleEnvelope is returned by both query creation and immutable reads.
type ProofBundleEnvelope struct {
	ID     string      `json:"id"`
	Bundle ProofBundle `json:"bundle"`
}

type assertionFilter struct {
	Predicate string
	Object    string
	Lineage   string
}

// ProofService is the shared T14 query core used by both Huma and MCP. A nil
// result from NewProofService keeps every proof surface undiscoverable while
// the provisional extraction feature is disabled.
type ProofService struct {
	opts Options
}

// NewProofService constructs the shared query service only when every store
// boundary needed to build and persist a proof bundle is available.
func NewProofService(opts Options) *ProofService {
	if opts.Store == nil || opts.Evidence == nil || opts.ProofBundles == nil {
		return nil
	}
	return &ProofService{opts: opts}
}

// FindOperationConsumers builds the same immutable answer returned by the
// Huma endpoint. Operation is the canonical /service/method identity.
func (s *ProofService) FindOperationConsumers(ctx context.Context, operation string) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	if err := validateOperation(operation); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query := ProofQuery{Kind: "find_operation_consumers", Operation: operation, Domains: []string{"grpc-consumer"}}
	return createProofBundle(ctx, s.opts, query, assertionFilter{
		Predicate: "CALLS_OPERATION", Object: operation,
	})
}

// FindProtoFieldReferences builds the immutable answer for one canonical
// (lineage, message, field number) identity.
func (s *ProofService) FindProtoFieldReferences(ctx context.Context, lineage, message string, fieldNumber int) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	if err := validateFieldIdentity(lineage, message, fieldNumber); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query := ProofQuery{
		Kind: "find_proto_field_references", Lineage: lineage,
		Message: message, FieldNumber: fieldNumber, Domains: []string{"scip-proto-field"},
	}
	return createProofBundle(ctx, s.opts, query, assertionFilter{
		Predicate: "REFERENCES_PROTO_FIELD",
		Object:    message + "#" + strconv.Itoa(fieldNumber), Lineage: lineage,
	})
}

// GetExtractionCoverage builds an assertion-free proof bundle over the
// requested domains. An empty slice selects every provisional domain.
func (s *ProofService) GetExtractionCoverage(ctx context.Context, domains []string) (*ProofBundleEnvelope, error) {
	if s == nil {
		return nil, huma.Error503ServiceUnavailable("proof queries unavailable")
	}
	domains, err := canonicalProofDomains(domains)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query := ProofQuery{Kind: "get_extraction_coverage", Domains: domains}
	return createProofBundle(ctx, s.opts, query, assertionFilter{})
}

func registerProofAPI(api huma.API, opts Options) {
	service := NewProofService(opts)
	if service == nil {
		return
	}

	type operationIn struct {
		Operation string `query:"operation" required:"true" minLength:"1" maxLength:"1024" example:"/shop.Cart/Get"`
	}
	type proofOut struct{ Body ProofBundleEnvelope }
	huma.Get(api, "/api/find_operation_consumers", func(ctx context.Context, in *operationIn) (*proofOut, error) {
		envelope, err := service.FindOperationConsumers(ctx, in.Operation)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})

	type fieldIn struct {
		Lineage     string `query:"lineage" required:"true" minLength:"1" maxLength:"1024"`
		Message     string `query:"message" required:"true" minLength:"1" maxLength:"1024" example:"shop.Cart"`
		FieldNumber int    `query:"field_number" required:"true" minimum:"1" maximum:"536870911" example:"1"`
	}
	huma.Get(api, "/api/find_proto_field_references", func(ctx context.Context, in *fieldIn) (*proofOut, error) {
		envelope, err := service.FindProtoFieldReferences(ctx, in.Lineage, in.Message, in.FieldNumber)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})

	type coverageIn struct {
		Domains string `query:"domains" doc:"comma-separated extractor domains; defaults to all provisional domains" example:"grpc-consumer,scip-proto-field"`
	}
	huma.Get(api, "/api/get_extraction_coverage", func(ctx context.Context, in *coverageIn) (*proofOut, error) {
		domains, err := proofDomains(in.Domains)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		envelope, err := service.GetExtractionCoverage(ctx, domains)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})

	type bundleIn struct {
		ID string `path:"id" minLength:"67" maxLength:"67"`
	}
	huma.Get(api, "/api/proof_bundles/{id}", func(ctx context.Context, in *bundleIn) (*proofOut, error) {
		envelope, err := readProofBundle(ctx, opts, in.ID)
		if err != nil {
			return nil, err
		}
		return &proofOut{Body: *envelope}, nil
	})
}

func createProofBundle(ctx context.Context, opts Options, query ProofQuery, filter assertionFilter) (*ProofBundleEnvelope, error) {
	visible, err := visibleRepositories(ctx, opts)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < proofBuildAttempts; attempt++ {
		certificate, err := extract.BuildCoverageCertificate(ctx, opts.Evidence, visible, query.Domains)
		if err != nil {
			return nil, huma.Error500InternalServerError("build extraction coverage", err)
		}
		assertions, evidence, err := collectProofEvidence(ctx, opts.Evidence, certificate, filter)
		if err != nil {
			if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
				continue
			}
			if errors.Is(err, store.ErrResultLimit) || errors.Is(err, errProofQueryLimit) {
				return nil, huma.Error422UnprocessableEntity("proof query exceeds bounded result limits; narrow the query")
			}
			return nil, huma.Error500InternalServerError("build proof evidence", err)
		}
		confirmed, err := extract.BuildCoverageCertificate(ctx, opts.Evidence, visible, query.Domains)
		if err != nil {
			return nil, huma.Error500InternalServerError("confirm extraction coverage", err)
		}
		if confirmed.Digest != certificate.Digest {
			continue
		}
		bundle := ProofBundle{
			SchemaVersion: proofBundleSchemaVersion, Query: query,
			Assertions: assertions, Evidence: evidence, Coverage: *certificate,
			ExtractorVersions: certificateExtractors(certificate),
			VisibilityContext: visibilityContext(ctx, opts, certificate), Caveat: proofCaveat,
		}
		content, err := json.Marshal(bundle)
		if err != nil {
			return nil, huma.Error500InternalServerError("canonicalize proof bundle", err)
		}
		repositories, runIDs := certificateScopes(certificate)
		record := store.ProofBundleRecord{
			ID: store.ComputeProofBundleID(string(content)), Content: string(content),
			Repositories: repositories, RunIDs: runIDs,
		}
		if err := opts.ProofBundles.PutProofBundle(ctx, record); err != nil {
			if errors.Is(err, store.ErrConflict) {
				continue
			}
			return nil, huma.Error500InternalServerError("persist proof bundle", err)
		}
		return &ProofBundleEnvelope{ID: record.ID, Bundle: bundle}, nil
	}
	return nil, huma.Error409Conflict("evidence changed while building the proof bundle; retry")
}

func collectProofEvidence(ctx context.Context, source store.EvidenceStore, certificate *extract.CoverageCertificate, filter assertionFilter) ([]store.Assertion, []BundleEvidence, error) {
	assertions := make([]store.Assertion, 0)
	if filter.Predicate != "" {
		if len(certificate.Domains) != 1 {
			return nil, nil, errors.New("filtered proof query requires exactly one coverage domain")
		}
		domain := certificate.Domains[0]
		for _, repository := range certificate.Repositories {
			run, ok := certificateRun(repository, domain)
			if !ok || run.Status != "published" {
				continue
			}
			rows, err := source.ListAssertions(ctx, store.AssertionQuery{
				Predicate: filter.Predicate, Object: filter.Object, Lineage: filter.Lineage,
				Repo: repository.Repository, RunID: run.RunID, Limit: proofQueryAssertionLimit,
			})
			if err != nil {
				return nil, nil, err
			}
			for _, assertion := range rows {
				if assertion.Repo != repository.Repository || assertion.RunID != run.RunID ||
					assertion.Predicate != filter.Predicate || assertion.Object != filter.Object ||
					filter.Lineage != "" && assertion.Lineage != filter.Lineage {
					return nil, nil, errors.New("proof query returned an inconsistent assertion")
				}
			}
			assertions = append(assertions, rows...)
			if len(assertions) > proofQueryAssertionLimit {
				return nil, nil, fmt.Errorf("%w: more than %d assertions", errProofQueryLimit, proofQueryAssertionLimit)
			}
		}
	}
	assertions = deterministicAssertions(assertions)

	type evidenceKey struct{ repo, runID, atomID string }
	keys := make(map[evidenceKey]struct{})
	for _, assertion := range assertions {
		for _, atomID := range append(append([]string(nil), assertion.Supporting...), assertion.Contradicting...) {
			keys[evidenceKey{repo: assertion.Repo, runID: assertion.RunID, atomID: atomID}] = struct{}{}
			if len(keys) > proofQueryEvidenceLimit {
				return nil, nil, fmt.Errorf("%w: more than %d evidence references", errProofQueryLimit, proofQueryEvidenceLimit)
			}
		}
	}
	ordered := make([]evidenceKey, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].repo != ordered[j].repo {
			return ordered[i].repo < ordered[j].repo
		}
		if ordered[i].runID != ordered[j].runID {
			return ordered[i].runID < ordered[j].runID
		}
		return ordered[i].atomID < ordered[j].atomID
	})
	evidence := make([]BundleEvidence, 0, len(ordered))
	for _, key := range ordered {
		resolved, err := source.ResolveEvidence(ctx, key.repo, key.runID, key.atomID)
		if err != nil {
			return nil, nil, err
		}
		if resolved == nil || resolved.Atom.ID != key.atomID {
			return nil, nil, errors.New("proof evidence resolution is inconsistent")
		}
		occurrences := append([]store.SnapshotEvidence(nil), resolved.Occurrences...)
		sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].ID < occurrences[j].ID })
		for _, occurrence := range occurrences {
			if occurrence.Repo != key.repo || occurrence.RunID != key.runID || occurrence.AtomID != key.atomID {
				return nil, nil, errors.New("proof evidence occurrence scope is inconsistent")
			}
		}
		evidence = append(evidence, BundleEvidence{
			Repository: key.repo, RunID: key.runID, Atom: resolved.Atom, Occurrences: occurrences,
		})
	}
	return assertions, evidence, nil
}

func certificateRun(repository extract.CertificateRepository, domain string) (extract.CertificateRun, bool) {
	for _, run := range repository.Runs {
		if run.Domain == domain {
			return run, true
		}
	}
	return extract.CertificateRun{}, false
}

func certificateExtractors(certificate *extract.CoverageCertificate) []BundleExtractor {
	result := make([]BundleExtractor, 0)
	for _, repository := range certificate.Repositories {
		for _, run := range repository.Runs {
			if run.Status == "published" {
				result = append(result, BundleExtractor{
					Repository: repository.Repository, Domain: run.Domain,
					RunID: run.RunID, Extractor: run.Extractor,
				})
			}
		}
	}
	return result
}

func certificateScopes(certificate *extract.CoverageCertificate) ([]string, []string) {
	repositories := make([]string, 0, len(certificate.Repositories))
	runSet := make(map[string]struct{})
	for _, repository := range certificate.Repositories {
		repositories = append(repositories, repository.Repository)
		for _, run := range repository.Runs {
			if run.Status == "published" {
				runSet[run.RunID] = struct{}{}
			}
		}
	}
	runIDs := make([]string, 0, len(runSet))
	for runID := range runSet {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	return repositories, runIDs
}

func visibilityContext(ctx context.Context, opts Options, certificate *extract.CoverageCertificate) VisibilityContext {
	repositories, _ := certificateScopes(certificate)
	visibleDigest := digestJSON(repositories)
	principal := "anonymous"
	if opts.Principal != nil {
		if resolved := strings.TrimSpace(opts.Principal(ctx)); resolved != "" {
			principal = resolved
		}
	}
	provider := opts.AuthorizationProvider
	if provider == "" {
		provider = "unfiltered-v1"
	}
	snapshot := digestJSON(struct {
		Principal string `json:"principal"`
		Provider  string `json:"provider"`
		Visible   string `json:"visible"`
	}{principal, provider, visibleDigest})
	return VisibilityContext{
		Principal: principal, AuthorizationProvider: provider,
		PermissionSnapshot: snapshot, VisibleRepositorySetDigest: visibleDigest,
	}
}

func digestJSON(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readProofBundle(ctx context.Context, opts Options, id string) (*ProofBundleEnvelope, error) {
	record, err := opts.ProofBundles.GetProofBundle(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, huma.Error404NotFound("proof bundle not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("read proof bundle", err)
	}
	if record == nil || record.ID != id || store.ComputeProofBundleID(record.Content) != id {
		return nil, huma.Error500InternalServerError("stored proof bundle identity is inconsistent")
	}
	visible, err := visibleRepositories(ctx, opts)
	if err != nil {
		return nil, err
	}
	visibleNames := make(map[string]struct{}, len(visible))
	for _, repo := range visible {
		visibleNames[repo.Name] = struct{}{}
	}
	for _, name := range record.Repositories {
		if _, ok := visibleNames[name]; !ok {
			return nil, huma.Error404NotFound("proof bundle not found")
		}
	}
	var bundle ProofBundle
	if err := json.Unmarshal([]byte(record.Content), &bundle); err != nil {
		return nil, huma.Error500InternalServerError("decode proof bundle", err)
	}
	canonical, err := json.Marshal(bundle)
	if err != nil || string(canonical) != record.Content || bundle.SchemaVersion != proofBundleSchemaVersion {
		return nil, huma.Error500InternalServerError("stored proof bundle is inconsistent")
	}
	repositories, runIDs := certificateScopes(&bundle.Coverage)
	if !equalStrings(repositories, record.Repositories) || !equalStrings(runIDs, record.RunIDs) {
		return nil, huma.Error500InternalServerError("stored proof bundle scope is inconsistent")
	}
	return &ProofBundleEnvelope{ID: record.ID, Bundle: bundle}, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validateOperation(operation string) error {
	if !utf8.ValidString(operation) || len(operation) > 1024 || strings.TrimSpace(operation) != operation ||
		!strings.HasPrefix(operation, "/") || strings.Count(operation, "/") != 2 {
		return errors.New("operation must be canonical /fully.qualified.Service/Method")
	}
	parts := strings.Split(strings.TrimPrefix(operation, "/"), "/")
	if len(parts) != 2 || !validQueryIdentity(parts[0]) || !validQueryIdentity(parts[1]) {
		return errors.New("operation must be canonical /fully.qualified.Service/Method")
	}
	return nil
}

func validateFieldIdentity(lineage, message string, number int) error {
	if !validQueryIdentity(lineage) || !validQueryIdentity(message) || number < 1 || number > 536_870_911 ||
		number >= 19_000 && number <= 19_999 {
		return errors.New("field identity requires bounded lineage, message, and a valid protobuf field number")
	}
	return nil
}

func validQueryIdentity(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == '/' || r == '#' {
			return false
		}
	}
	return true
}

func proofDomains(raw string) ([]string, error) {
	if raw == "" {
		return canonicalProofDomains(nil)
	}
	return canonicalProofDomains(strings.Split(raw, ","))
}

func canonicalProofDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return []string{"grpc-consumer", "proto-contract", "scip-proto-field"}, nil
	}
	parts := append([]string(nil), domains...)
	if len(parts) == 0 || len(parts) > 64 {
		return nil, errors.New("domains must contain from 1 through 64 values")
	}
	seen := make(map[string]struct{}, len(parts))
	for _, domain := range parts {
		if domain == "" || len(domain) > 128 || strings.TrimSpace(domain) != domain || !validDomain(domain) {
			return nil, errors.New("domains must be comma-separated extractor tokens")
		}
		if _, duplicate := seen[domain]; duplicate {
			return nil, errors.New("domains must not contain duplicates")
		}
		seen[domain] = struct{}{}
	}
	sort.Strings(parts)
	return parts, nil
}

func validDomain(value string) bool {
	for _, r := range value {
		if r == '-' || r == '.' || r == '_' || r >= '0' && r <= '9' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			continue
		}
		return false
	}
	return value != ""
}
