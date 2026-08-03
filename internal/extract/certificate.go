package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	candidatepkg "github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

// certificateSchemaVersion names the T13.3 per-answer coverage certificate.
const certificateSchemaVersion = "coverage-certificate-v3"

// certificateMaxCorpusCensus bounds a receipt's corpus census. A
// candidate-manifest run records the manifest's corpus census, which the
// writer bounds by the candidate corpus ceiling rather than the walked
// per-run corpus limit frozen into the receipt's own limits block, so the
// receipt-side sanity bound must admit the larger writer contract.
const certificateMaxCorpusCensus = candidatepkg.MaxCorpusEntries

const certificateDomainSnapshotAttempts = 3

var errCertificateDomainTransition = errors.New(
	"extraction domain state changed while building coverage",
)

// RunSource is the narrow read surface the certificate builder consumes. It is
// deliberately not the full EvidenceStore: the builder can look up published
// evidence, the durable latest-attempt marker, and the bounded latest domain
// outcome, and nothing else.
type RunSource interface {
	LatestPublishedRun(ctx context.Context, scope store.ExtractionScope) (*store.ExtractionRun, error)
	LatestExtractionAttempt(ctx context.Context, scope store.ExtractionScope) (*store.ExtractionAttempt, error)
	LatestExtractionDomainOutcome(ctx context.Context, scope store.ExtractionScope) (*store.ExtractionDomainOutcome, error)
}

// CoverageCertificate is the per-answer honesty record: the caller's entire
// visible repository universe and the exact published evidence state an answer
// was computed from. Equal store state yields byte-equal certificates — there
// is no wall-clock field — so Digest changes exactly when covered state does.
type CoverageCertificate struct {
	SchemaVersion   string                  `json:"schema_version"`
	Domains         []string                `json:"domains"`
	RepositoryCount int                     `json:"repository_count"`
	Repositories    []CertificateRepository `json:"repositories"`
	Digest          string                  `json:"digest"` // sha256 over the canonical JSON with Digest empty
}

// CertificateRepository is one caller-visible repository: every visible
// repository appears, including ones with no published evidence at all.
type CertificateRepository struct {
	Repository    string                   `json:"repository"`
	IndexedCommit string                   `json:"indexed_commit,omitempty"`
	ScopePosture  string                   `json:"scope_posture"` // whole-repository | focused
	AnalysisUnit  *CertificateAnalysisUnit `json:"analysis_unit,omitempty"`
	SCIPIndex     string                   `json:"scip_index"` // present | absent | unknown
	Runs          []CertificateRun         `json:"runs"`

	// legacyScopeShape is set only while decoding a retained v1 certificate
	// whose repository entries predate scope_posture (added at T30.5 with the
	// v2 bump). Keeping that wire shape lets immutable v1 proof bundles
	// round-trip byte-for-byte; newly built v3 certificates always emit the
	// posture.
	legacyScopeShape bool
}

// MarshalJSON preserves the shipped v1 repository shape when a retained proof
// bundle is decoded, while keeping scope_posture explicit for newly built
// certificates.
func (repository CertificateRepository) MarshalJSON() ([]byte, error) {
	type current CertificateRepository
	if !repository.legacyScopeShape {
		return json.Marshal(current(repository))
	}
	return json.Marshal(struct {
		Repository    string           `json:"repository"`
		IndexedCommit string           `json:"indexed_commit,omitempty"`
		SCIPIndex     string           `json:"scip_index"`
		Runs          []CertificateRun `json:"runs"`
	}{
		Repository:    repository.Repository,
		IndexedCommit: repository.IndexedCommit,
		SCIPIndex:     repository.SCIPIndex,
		Runs:          repository.Runs,
	})
}

// UnmarshalJSON recognizes only the complete v1 omission. A repository entry
// carrying any post-v1 field without scope_posture remains a non-canonical
// current shape and is rejected by the caller's byte-exact round-trip check,
// because the legacy marshal drops those fields.
func (repository *CertificateRepository) UnmarshalJSON(data []byte) error {
	type current CertificateRepository
	var decoded current
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var presence struct {
		ScopePosture *json.RawMessage `json:"scope_posture"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	*repository = CertificateRepository(decoded)
	repository.legacyScopeShape = presence.ScopePosture == nil
	return nil
}

// CertificateAnalysisUnit is the canonical public scope projection. It binds
// every answer to the exact selected roots and typed-input designation without
// exposing any additional repository state.
type CertificateAnalysisUnit struct {
	Name              string   `json:"name"`
	Digest            string   `json:"digest"`
	PrimaryPaths      []string `json:"primary_paths"`
	SupportingPaths   []string `json:"supporting_paths"`
	TypedIndexPosture string   `json:"typed_index_posture"`
	TypedIndexKind    string   `json:"typed_index_kind,omitempty"`
	TypedIndexPath    string   `json:"typed_index_path,omitempty"`
}

// CertificateRun binds one (repository, domain) pair to both its latest
// published evidence and its latest extraction attempt. Failed replacements
// remain visible here even though they never replace published evidence.
type CertificateRun struct {
	Domain             string   `json:"domain"`
	Status             string   `json:"status"` // published | unpublished
	RunID              string   `json:"run_id,omitempty"`
	Extractor          string   `json:"extractor,omitempty"`
	Commit             string   `json:"commit,omitempty"`
	UnitDigest         string   `json:"unit_digest,omitempty"`
	Fresh              bool     `json:"fresh"` // run commit + unit == repository's indexed scope
	Protocols          []string `json:"protocols,omitempty"`
	Failures           []string `json:"failures,omitempty"`
	CorpusFileCount    int      `json:"corpus_file_count"`
	CandidateFileCount int      `json:"candidate_file_count"`
	ReadFileCount      int      `json:"read_file_count"`
	ReadBytes          int64    `json:"read_bytes"`
	SourceScopeDigest  string   `json:"source_scope_digest,omitempty"`
	UnresolvedCount    int      `json:"unresolved_count"`
	AssertionCount     int      `json:"assertion_count"`
	AtomCount          int      `json:"atom_count"`
	// InventoryPolicy and the gitlink fields surface the run's submodule
	// boundaries (T19.8). An empty policy is a legacy run whose boundary
	// status is unknown — consumers must never read the absent count as
	// zero. All five are omitempty, so legacy certificates keep their
	// exact prior bytes and digests.
	InventoryPolicy        string                     `json:"inventory_policy,omitempty"`
	GitlinkCount           int                        `json:"gitlink_count,omitempty"`
	GitlinkDigest          string                     `json:"gitlink_digest,omitempty"`
	GitlinkSamplePaths     []string                   `json:"gitlink_sample_paths,omitempty"`
	GitlinkSampleTruncated bool                       `json:"gitlink_sample_truncated,omitempty"`
	EvidenceScopePosture   string                     `json:"evidence_scope_posture,omitempty"`
	CandidateScope         *CertificateCandidateScope `json:"candidate_scope,omitempty"`
	LatestAttempt          *CertificateAttempt        `json:"latest_attempt,omitempty"`
	Outcome                *CertificateDomainOutcome  `json:"outcome,omitempty"`
}

// CertificateDomainOutcome is the time-free public projection of one durable
// latest domain outcome. GenerationDigest lets a consumer compare the record
// with other generation authority without treating an exact-scope but retired
// generation as current. The full receipt is decoded and cross-checked before
// it enters a certificate; the encoder's defensive schema-only fallback stays
// explicit rather than rendering zero-valued work as an exact receipt.
type CertificateDomainOutcome struct {
	Disposition             store.DomainOutcomeDisposition `json:"disposition"`
	GenerationDigest        string                         `json:"generation_digest"`
	Extractor               string                         `json:"extractor"`
	CandidateControlFailure bool                           `json:"candidate_control_failure,omitempty"`
	ReceiptSchema           string                         `json:"receipt_schema"`
	// ReceiptState is full | schema_only | legacy_exclusion_shape. The legacy
	// state names a retained pre-T30.6e receipt whose excluded-source/SCIP
	// counters are absent from the wire shape; its zero-filled Go values are
	// a decode artifact, never a certificate claim.
	ReceiptState string `json:"receipt_state"`
	// Receipt carries the validated stored receipt bytes verbatim through a
	// typed wrapper, so retained legacy bytes survive while HTTP OpenAPI keeps
	// the concrete receipt object schema. For a current-shape receipt the raw
	// bytes equal its canonical typed re-marshal.
	Receipt *CertificateOutcomeReceipt `json:"receipt,omitempty"`
}

// CertificateOutcomeReceipt preserves an immutable receipt's original bytes
// while retaining the exported typed fields used by generated HTTP schemas.
// The private raw mirror is populated only by a validated projection or JSON
// decode and is never an independent source of receipt semantics.
type CertificateOutcomeReceipt struct {
	ExtractionDomainOutcomeReceipt
	raw json.RawMessage
}

func (receipt CertificateOutcomeReceipt) MarshalJSON() ([]byte, error) {
	return receipt.canonicalBytes()
}

func (receipt *CertificateOutcomeReceipt) UnmarshalJSON(data []byte) error {
	if receipt == nil {
		return errors.New("certificate outcome receipt is nil")
	}
	var decoded ExtractionDomainOutcomeReceipt
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*receipt = CertificateOutcomeReceipt{
		ExtractionDomainOutcomeReceipt: decoded,
		raw:                            append(json.RawMessage(nil), data...),
	}
	return nil
}

func (receipt CertificateOutcomeReceipt) canonicalBytes() ([]byte, error) {
	if len(receipt.raw) != 0 {
		if !json.Valid(receipt.raw) {
			return nil, errors.New("certificate outcome receipt raw JSON is invalid")
		}
		return append([]byte(nil), receipt.raw...), nil
	}
	return json.Marshal(receipt.ExtractionDomainOutcomeReceipt)
}

// CertificateCandidateScope discloses the exact candidate-manifest projection
// used by one domain. Zero counts remain explicit inside this optional object;
// absence means a readable legacy/direct-corpus run.
type CertificateCandidateScope struct {
	ManifestDigest              string                 `json:"manifest_digest"`
	Plane                       string                 `json:"plane"`
	CorpusFileCount             int                    `json:"corpus_file_count"`
	CorpusDeclaredBytes         int64                  `json:"corpus_declared_bytes"`
	CorpusDigest                string                 `json:"corpus_digest"`
	PlannedFileCount            int                    `json:"planned_file_count"`
	PlannedRequiredCount        int                    `json:"planned_required_file_count"`
	PlannedDeclaredBytes        int64                  `json:"planned_declared_bytes"`
	PlannedDigest               string                 `json:"planned_digest"`
	BaseSourceFileCount         *int                   `json:"base_source_file_count,omitempty"`
	ExcludedGoTestCount         *int                   `json:"excluded_go_test_file_count,omitempty"`
	ExcludedSourceFileCount     int                    `json:"excluded_source_file_count"`
	ExcludedSourceRequiredCount int                    `json:"excluded_source_required_count"`
	ExcludedSourceDeclaredBytes int64                  `json:"excluded_source_declared_bytes"`
	ExcludedSCIPDocumentCount   int                    `json:"excluded_scip_document_count"`
	ExcludedSCIPDefinitionCount int                    `json:"excluded_scip_definition_count"`
	ExcludedSCIPOccurrenceCount int                    `json:"excluded_scip_occurrence_count"`
	TypedInput                  *CertificateTypedInput `json:"typed_input,omitempty"`

	// legacyExclusionShape is set only while decoding a retained v1/v2
	// certificate whose candidate scope predates the six explicit exclusion
	// counters above. Keeping that wire shape lets immutable proof bundles
	// round-trip byte-for-byte. Newly built v3 certificates always emit those
	// six counters, including zero; focused-local/local scopes additionally
	// emit both source-lane counters.
	legacyExclusionShape bool
}

// MarshalJSON preserves the shipped v1/v2 candidate-scope shape when a
// retained proof bundle is decoded, while keeping all six universal v3
// exclusion counters explicit for newly built certificates. The two optional
// source-lane counters remain explicit only for focused-local/local scopes.
func (scope CertificateCandidateScope) MarshalJSON() ([]byte, error) {
	type current CertificateCandidateScope
	if !scope.legacyExclusionShape {
		return json.Marshal(current(scope))
	}
	return json.Marshal(struct {
		ManifestDigest       string                 `json:"manifest_digest"`
		Plane                string                 `json:"plane"`
		CorpusFileCount      int                    `json:"corpus_file_count"`
		CorpusDeclaredBytes  int64                  `json:"corpus_declared_bytes"`
		CorpusDigest         string                 `json:"corpus_digest"`
		PlannedFileCount     int                    `json:"planned_file_count"`
		PlannedRequiredCount int                    `json:"planned_required_file_count"`
		PlannedDeclaredBytes int64                  `json:"planned_declared_bytes"`
		PlannedDigest        string                 `json:"planned_digest"`
		TypedInput           *CertificateTypedInput `json:"typed_input,omitempty"`
	}{
		ManifestDigest:       scope.ManifestDigest,
		Plane:                scope.Plane,
		CorpusFileCount:      scope.CorpusFileCount,
		CorpusDeclaredBytes:  scope.CorpusDeclaredBytes,
		CorpusDigest:         scope.CorpusDigest,
		PlannedFileCount:     scope.PlannedFileCount,
		PlannedRequiredCount: scope.PlannedRequiredCount,
		PlannedDeclaredBytes: scope.PlannedDeclaredBytes,
		PlannedDigest:        scope.PlannedDigest,
		TypedInput:           scope.TypedInput,
	})
}

// UnmarshalJSON recognizes only the complete legacy omission. A partial
// omission remains a non-canonical current shape and is rejected by the
// caller's byte-exact round-trip check.
func (scope *CertificateCandidateScope) UnmarshalJSON(data []byte) error {
	type current CertificateCandidateScope
	var decoded current
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var presence struct {
		ExcludedSourceFileCount     *json.RawMessage `json:"excluded_source_file_count"`
		ExcludedSourceRequiredCount *json.RawMessage `json:"excluded_source_required_count"`
		ExcludedSourceDeclaredBytes *json.RawMessage `json:"excluded_source_declared_bytes"`
		ExcludedSCIPDocumentCount   *json.RawMessage `json:"excluded_scip_document_count"`
		ExcludedSCIPDefinitionCount *json.RawMessage `json:"excluded_scip_definition_count"`
		ExcludedSCIPOccurrenceCount *json.RawMessage `json:"excluded_scip_occurrence_count"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	*scope = CertificateCandidateScope(decoded)
	scope.legacyExclusionShape =
		presence.ExcludedSourceFileCount == nil &&
			presence.ExcludedSourceRequiredCount == nil &&
			presence.ExcludedSourceDeclaredBytes == nil &&
			presence.ExcludedSCIPDocumentCount == nil &&
			presence.ExcludedSCIPDefinitionCount == nil &&
			presence.ExcludedSCIPOccurrenceCount == nil
	return nil
}

// ValidateCanonicalShape applies schema-version rules that cannot be
// expressed by Go's JSON tags alone. V1 predates repository scope and
// candidate projections. V2 requires repository scope but retains the old
// candidate-scope shape. V3 must disclose every universal exclusion counter,
// and its two source-lane counters are required exactly for a
// focused-local/local candidate projection.
func (certificate *CoverageCertificate) ValidateCanonicalShape() error {
	if certificate == nil {
		return errors.New("coverage certificate is nil")
	}
	switch certificate.SchemaVersion {
	case "coverage-certificate-v1":
		for _, repository := range certificate.Repositories {
			if !repository.legacyScopeShape {
				return fmt.Errorf(
					"coverage certificate v1 repository %q carries scope posture",
					repository.Repository,
				)
			}
			if repository.AnalysisUnit != nil {
				return fmt.Errorf(
					"coverage certificate v1 repository %q carries analysis-unit scope",
					repository.Repository,
				)
			}
			for _, run := range repository.Runs {
				if run.UnitDigest != "" || run.EvidenceScopePosture != "" ||
					run.CandidateScope != nil || run.Outcome != nil ||
					run.LatestAttempt != nil && run.LatestAttempt.UnitDigest != "" {
					return fmt.Errorf(
						"coverage certificate v1 run %q/%q carries post-v1 scope or outcome state",
						repository.Repository, run.Domain,
					)
				}
			}
		}
		return nil
	case "coverage-certificate-v2":
		for _, repository := range certificate.Repositories {
			if repository.legacyScopeShape {
				return fmt.Errorf(
					"coverage certificate v2 repository %q omits scope posture",
					repository.Repository,
				)
			}
			if !validCertificateScopePosture(repository.ScopePosture) {
				return fmt.Errorf(
					"coverage certificate v2 repository %q has invalid scope posture %q",
					repository.Repository, repository.ScopePosture,
				)
			}
			for _, run := range repository.Runs {
				if run.Outcome != nil {
					return fmt.Errorf(
						"coverage certificate v2 run %q/%q carries a v3 outcome",
						repository.Repository, run.Domain,
					)
				}
				if run.CandidateScope != nil &&
					!run.CandidateScope.legacyExclusionShape {
					return fmt.Errorf(
						"coverage certificate v2 candidate scope for %q/%q carries v3 counters",
						repository.Repository, run.Domain,
					)
				}
			}
		}
		return nil
	case certificateSchemaVersion:
		// Checked below.
	default:
		return fmt.Errorf(
			"unsupported coverage certificate schema %q",
			certificate.SchemaVersion,
		)
	}
	for _, repository := range certificate.Repositories {
		if repository.legacyScopeShape {
			return fmt.Errorf(
				"coverage certificate v3 repository %q omits scope posture",
				repository.Repository,
			)
		}
		if !validCertificateScopePosture(repository.ScopePosture) {
			return fmt.Errorf(
				"coverage certificate v3 repository %q has invalid scope posture %q",
				repository.Repository, repository.ScopePosture,
			)
		}
		for _, run := range repository.Runs {
			if err := validateCertificateDomainOutcome(
				repository.Repository, run,
			); err != nil {
				return err
			}
			scope := run.CandidateScope
			if scope == nil {
				continue
			}
			if scope.legacyExclusionShape {
				return fmt.Errorf(
					"coverage certificate v3 candidate scope for %q/%q omits exclusion counters",
					repository.Repository, run.Domain,
				)
			}
			requiresSourceLanes :=
				run.EvidenceScopePosture == "focused-local" &&
					scope.Plane == "local"
			hasBase := scope.BaseSourceFileCount != nil
			hasExcludedGoTest := scope.ExcludedGoTestCount != nil
			switch {
			case requiresSourceLanes && (!hasBase || !hasExcludedGoTest):
				return fmt.Errorf(
					"coverage certificate v3 focused-local/local candidate scope for %q/%q omits source-lane counters",
					repository.Repository, run.Domain,
				)
			case !requiresSourceLanes && (hasBase || hasExcludedGoTest):
				return fmt.Errorf(
					"coverage certificate v3 candidate scope for %q/%q carries source-lane counters outside focused-local/local",
					repository.Repository, run.Domain,
				)
			}
		}
	}
	return nil
}

func validCertificateScopePosture(posture string) bool {
	return posture == "whole-repository" || posture == "focused"
}

func validateCertificateDomainOutcome(
	repository string,
	run CertificateRun,
) error {
	outcome := run.Outcome
	if outcome == nil {
		return nil
	}
	fail := func(reason string) error {
		return fmt.Errorf(
			"coverage certificate v3 outcome for %q/%q %s",
			repository, run.Domain, reason,
		)
	}
	if !store.ValidDomainOutcomeDisposition(outcome.Disposition) {
		return fail("has an unknown disposition")
	}
	if !validExtractionGenerationDigest(outcome.GenerationDigest) ||
		strings.TrimSpace(outcome.Extractor) == "" ||
		strings.TrimSpace(outcome.Extractor) != outcome.Extractor ||
		len(outcome.Extractor) > 128 ||
		outcome.ReceiptSchema != store.ExtractionOutcomeReceiptSchema {
		return fail("has invalid generation or receipt identity")
	}
	if outcome.CandidateControlFailure &&
		outcome.Disposition != store.DomainOutcomeTerminalGenerationRefusal {
		return fail("marks a non-terminal candidate-control failure")
	}
	if outcome.Disposition == store.DomainOutcomePublished &&
		(run.Status != "published" || run.Extractor != outcome.Extractor) {
		return fail("does not match its published run")
	}

	switch outcome.ReceiptState {
	case "schema_only":
		if outcome.Receipt != nil {
			return fail("carries receipt content in schema-only state")
		}
		return nil
	case "full", "legacy_exclusion_shape":
		if outcome.Receipt == nil {
			return fail("omits receipt content")
		}
	default:
		return fail("has an unknown receipt state")
	}

	receiptBytes, err := outcome.Receipt.canonicalBytes()
	if err != nil {
		return fail("has invalid receipt JSON")
	}
	receipt := outcome.Receipt.ExtractionDomainOutcomeReceipt
	var canonical []byte
	if outcome.ReceiptState == "full" {
		canonical, err = json.Marshal(receipt)
	} else {
		canonical, err = marshalLegacyExclusionReceipt(receipt)
	}
	if err != nil || string(canonical) != string(receiptBytes) {
		return fail("has a non-canonical receipt shape")
	}
	if receipt.Schema != store.ExtractionOutcomeReceiptSchema ||
		receipt.Schema != outcome.ReceiptSchema ||
		receipt.Domain != run.Domain ||
		receipt.ExtractorVersion != outcome.Extractor ||
		receipt.Disposition != outcome.Disposition ||
		!validOperationReason(receipt.Reason) ||
		!validCertificateOutcomeReceipt(receipt) {
		return fail("has inconsistent receipt content")
	}
	return nil
}

func validExtractionGenerationDigest(value string) bool {
	const prefix = "extraction_generation_v1_"
	digest, ok := strings.CutPrefix(value, prefix)
	if !ok || len(digest) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && prefix+hex.EncodeToString(decoded) == value
}

// CertificateTypedInput is the real-path typed parser receipt. Present=false
// is an explicit gap and carries no content identity.
type CertificateTypedInput struct {
	Kind          string `json:"kind"`
	Path          string `json:"path,omitempty"`
	ObjectID      string `json:"object_id,omitempty"`
	DeclaredBytes int64  `json:"declared_bytes"`
	Digest        string `json:"digest,omitempty"`
	Present       bool   `json:"present"`
}

// CertificateAttempt is deliberately time-free: identity, input revision,
// extractor, and state describe the attempt without making an unchanged
// evidence state differ merely because it was processed later.
type CertificateAttempt struct {
	RunID      string `json:"run_id"`
	Commit     string `json:"commit"`
	UnitDigest string `json:"unit_digest,omitempty"`
	Extractor  string `json:"extractor"`
	Status     string `json:"status"` // staged | published | aborted
	Failure    string `json:"failure,omitempty"`
}

// BuildCoverageCertificate compiles the certificate for an already-authorized
// visible repository slice. Visibility filtering is the caller's authorization
// boundary; the builder never queries, names, or counts any repository outside
// the slice, so an invisible repository cannot influence the output.
func BuildCoverageCertificate(
	ctx context.Context,
	source RunSource,
	visible []store.Repo,
	domains []string,
) (*CoverageCertificate, error) {
	if source == nil {
		return nil, errors.New("coverage certificate requires a run source")
	}
	domainList, err := certificateDomains(domains)
	if err != nil {
		return nil, err
	}
	repos := append([]store.Repo(nil), visible...)
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	for i, repo := range repos {
		if repo.Name == "" || repo.Deleting || i > 0 && repos[i-1].Name == repo.Name {
			return nil, fmt.Errorf("visible repository set is inconsistent at %q", repo.Name)
		}
	}

	certificate := &CoverageCertificate{
		SchemaVersion:   certificateSchemaVersion,
		Domains:         domainList,
		RepositoryCount: len(repos),
		Repositories:    make([]CertificateRepository, 0, len(repos)),
	}
	for _, repo := range repos {
		entry := CertificateRepository{
			Repository:    repo.Name,
			IndexedCommit: repo.IndexedCommitHash,
			ScopePosture:  "whole-repository",
			SCIPIndex:     "unknown",
			Runs:          make([]CertificateRun, 0, len(domainList)),
		}
		if repo.IndexedAnalysisUnit != nil {
			if err := repo.IndexedAnalysisUnit.Validate(repo.Name); err != nil {
				return nil, fmt.Errorf(
					"visible repository %q has invalid analysis-unit state: %w",
					repo.Name, err,
				)
			}
			entry.ScopePosture = repo.IndexedAnalysisUnit.SearchIndexPosture
			entry.AnalysisUnit = &CertificateAnalysisUnit{
				Name:              repo.IndexedAnalysisUnit.Name,
				Digest:            repo.IndexedAnalysisUnit.Digest,
				PrimaryPaths:      append([]string(nil), repo.IndexedAnalysisUnit.PrimaryPaths...),
				SupportingPaths:   append([]string(nil), repo.IndexedAnalysisUnit.SupportingPaths...),
				TypedIndexPosture: repo.IndexedAnalysisUnit.TypedIndexPosture,
			}
			if repo.IndexedAnalysisUnit.TypedIndex != nil {
				entry.AnalysisUnit.TypedIndexKind = repo.IndexedAnalysisUnit.TypedIndex.Kind
				entry.AnalysisUnit.TypedIndexPath = repo.IndexedAnalysisUnit.TypedIndex.Path
			}
		}
		for _, domain := range domainList {
			scope := certificateExtractionScope(repo, domain)
			var (
				run                *store.ExtractionRun
				published          bool
				certificateAttempt *CertificateAttempt
				certificateOutcome *CertificateDomainOutcome
			)
			for snapshotAttempt := 0; ; snapshotAttempt++ {
				var err error
				run, err = source.LatestPublishedRun(ctx, scope)
				published = err == nil
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					return nil, fmt.Errorf(
						"latest published run for %q/%q: %w",
						repo.Name, domain, err,
					)
				}
				if published && (run == nil || run.ID == "" ||
					run.Repo != repo.Name || run.Domain != domain ||
					run.Status != "published" || run.Commit != scope.Commit ||
					run.UnitDigest != scope.UnitDigest || run.Extractor == "") {
					return nil, fmt.Errorf(
						"published run for %q/%q is inconsistent",
						repo.Name, domain,
					)
				}
				outcome, outcomeErr := source.LatestExtractionDomainOutcome(ctx, scope)
				if errors.Is(outcomeErr, store.ErrNotFound) {
					outcome = nil
				} else if outcomeErr != nil {
					return nil, fmt.Errorf(
						"latest extraction domain outcome for %q/%q: %w",
						repo.Name, domain, outcomeErr,
					)
				}
				attempt, attemptErr := source.LatestExtractionAttempt(ctx, scope)
				if errors.Is(attemptErr, store.ErrNotFound) {
					attempt = nil
				} else if attemptErr != nil {
					return nil, fmt.Errorf(
						"latest extraction attempt for %q/%q: %w",
						repo.Name, domain, attemptErr,
					)
				}
				if attempt == nil && published {
					attempt = &store.ExtractionAttempt{
						RunID: run.ID, Repo: run.Repo, Commit: run.Commit,
						Domain: run.Domain, UnitDigest: run.UnitDigest,
						Extractor: run.Extractor, Status: "published",
					}
				}
				// Publication commits the run, published outcome, and latest
				// attempt atomically, while failure first aborts the attempt and
				// then records its non-published outcome. Reading run, outcome,
				// then attempt makes either transition produce a real pre/post
				// state or an identity mismatch that retries. Reading the attempt
				// first could pair a pre-abort staged attempt with the later
				// failure outcome.
				certificateAttempt, err = validateCertificateAttempt(
					scope, attempt, run, published,
				)
				if err == nil {
					certificateOutcome, err = certificateDomainOutcome(
						scope, outcome, run, published,
					)
				}
				if !errors.Is(err, errCertificateDomainTransition) {
					if err != nil {
						return nil, err
					}
					break
				}
				if snapshotAttempt+1 >= certificateDomainSnapshotAttempts {
					return nil, fmt.Errorf(
						"coverage for %q/%q remained in transition after %d reads: %w",
						repo.Name, domain, certificateDomainSnapshotAttempts,
						store.ErrConflict,
					)
				}
			}
			if !published {
				entry.Runs = append(entry.Runs, CertificateRun{
					Domain: domain, Status: "unpublished", UnitDigest: scope.UnitDigest,
					LatestAttempt: certificateAttempt, Outcome: certificateOutcome,
				})
				continue
			}
			protocols := append([]string(nil), run.Coverage.Protocols...)
			failures := append([]string(nil), run.Coverage.Failures...)
			sort.Strings(protocols)
			sort.Strings(failures)
			fresh := run.Commit == repo.IndexedCommitHash &&
				run.UnitDigest == scope.UnitDigest
			candidateScope, err := certificateCandidateScope(run.Coverage)
			if err != nil {
				return nil, fmt.Errorf(
					"published run for %q/%q has invalid candidate scope: %w",
					repo.Name, domain, err,
				)
			}
			entry.Runs = append(entry.Runs, CertificateRun{
				Domain: domain, Status: "published", RunID: run.ID,
				Extractor: run.Extractor, Commit: run.Commit,
				UnitDigest: run.UnitDigest, Fresh: fresh,
				Protocols: protocols, Failures: failures,
				CorpusFileCount:        run.Coverage.CorpusFileCount,
				CandidateFileCount:     run.Coverage.CandidateFileCount,
				ReadFileCount:          run.Coverage.ReadFileCount,
				ReadBytes:              run.Coverage.ReadBytes,
				SourceScopeDigest:      run.Coverage.SourceScopeDigest,
				UnresolvedCount:        run.Coverage.UnresolvedCount,
				AssertionCount:         run.Coverage.AssertionCount,
				AtomCount:              run.Coverage.AtomCount,
				InventoryPolicy:        run.Coverage.InventoryPolicy,
				GitlinkCount:           run.Coverage.GitlinkCount,
				GitlinkDigest:          run.Coverage.GitlinkDigest,
				GitlinkSamplePaths:     append([]string(nil), run.Coverage.GitlinkSamplePaths...),
				GitlinkSampleTruncated: run.Coverage.GitlinkSampleTruncated,
				EvidenceScopePosture:   run.Coverage.ScopePosture,
				CandidateScope:         candidateScope,
				LatestAttempt:          certificateAttempt,
				Outcome:                certificateOutcome,
			})
			for _, protocol := range protocols {
				if !fresh {
					break
				}
				switch {
				case protocol == "scip":
					entry.SCIPIndex = "present"
				case protocol == "scip-index-absent" && entry.SCIPIndex != "present":
					entry.SCIPIndex = "absent"
				}
			}
		}
		certificate.Repositories = append(certificate.Repositories, entry)
	}

	digest, err := certificateDigest(certificate)
	if err != nil {
		return nil, err
	}
	certificate.Digest = digest
	return certificate, nil
}

func certificateExtractionScope(repo store.Repo, domain string) store.ExtractionScope {
	scope := store.ExtractionScope{
		Repository: repo.Name,
		Commit:     repo.IndexedCommitHash,
		Domain:     domain,
	}
	if repo.IndexedAnalysisUnit != nil {
		scope.UnitDigest = repo.IndexedAnalysisUnit.Digest
	}
	return scope
}

func certificateCandidateScope(
	coverage store.CoverageManifest,
) (*CertificateCandidateScope, error) {
	if coverage.CandidateManifestDigest == "" {
		return nil, nil
	}
	if coverage.PlannedFileCount < 0 ||
		coverage.ExcludedSourceFileCount < 0 ||
		coverage.ExcludedSourceFileCount > coverage.PlannedFileCount ||
		coverage.ExcludedSourceRequiredCount < 0 ||
		coverage.ExcludedSourceRequiredCount > coverage.ExcludedSourceFileCount ||
		coverage.ExcludedSourceDeclaredBytes < 0 ||
		coverage.ExcludedSCIPDocumentCount < 0 ||
		coverage.ExcludedSCIPDefinitionCount < 0 ||
		coverage.ExcludedSCIPOccurrenceCount < 0 {
		return nil, errors.New("candidate exclusion counts are inconsistent")
	}
	scope := &CertificateCandidateScope{
		ManifestDigest:              coverage.CandidateManifestDigest,
		Plane:                       coverage.CandidatePlane,
		CorpusFileCount:             coverage.ScopeCorpusFileCount,
		CorpusDeclaredBytes:         coverage.ScopeCorpusDeclaredBytes,
		CorpusDigest:                coverage.ScopeCorpusDigest,
		PlannedFileCount:            coverage.PlannedFileCount,
		PlannedRequiredCount:        coverage.PlannedRequiredFileCount,
		PlannedDeclaredBytes:        coverage.PlannedDeclaredBytes,
		PlannedDigest:               coverage.PlannedScopeDigest,
		ExcludedSourceFileCount:     coverage.ExcludedSourceFileCount,
		ExcludedSourceRequiredCount: coverage.ExcludedSourceRequiredCount,
		ExcludedSourceDeclaredBytes: coverage.ExcludedSourceDeclaredBytes,
		ExcludedSCIPDocumentCount:   coverage.ExcludedSCIPDocumentCount,
		ExcludedSCIPDefinitionCount: coverage.ExcludedSCIPDefinitionCount,
		ExcludedSCIPOccurrenceCount: coverage.ExcludedSCIPOccurrenceCount,
	}
	if coverage.ScopePosture == "focused-local" && coverage.CandidatePlane == "local" {
		base := coverage.PlannedFileCount - coverage.ExcludedSourceFileCount
		excludedGoTest := coverage.ExcludedSourceFileCount
		scope.BaseSourceFileCount = &base
		scope.ExcludedGoTestCount = &excludedGoTest
	}
	if coverage.TypedInputKind != "" {
		scope.TypedInput = &CertificateTypedInput{
			Kind:          coverage.TypedInputKind,
			Path:          coverage.TypedInputPath,
			ObjectID:      coverage.TypedInputObjectID,
			DeclaredBytes: coverage.TypedInputDeclaredBytes,
			Digest:        coverage.TypedInputDigest,
			Present:       coverage.TypedInputPresent,
		}
	}
	return scope, nil
}

func certificateDomainOutcome(
	scope store.ExtractionScope,
	outcome *store.ExtractionDomainOutcome,
	publishedRun *store.ExtractionRun,
	published bool,
) (*CertificateDomainOutcome, error) {
	if outcome == nil {
		return nil, nil
	}
	if err := outcome.Validate(); err != nil {
		return nil, fmt.Errorf(
			"latest extraction domain outcome for %q/%q is invalid: %w",
			scope.Repository, scope.Domain, err,
		)
	}
	if outcome.Scope != scope {
		return nil, fmt.Errorf(
			"latest extraction domain outcome for %q/%q has inconsistent scope",
			scope.Repository, scope.Domain,
		)
	}
	generationDigest := store.ComputeExtractionGenerationDigest(outcome.Generation)
	if outcome.Generation.Digest != generationDigest {
		return nil, fmt.Errorf(
			"latest extraction domain outcome for %q/%q has inconsistent generation digest",
			scope.Repository, scope.Domain,
		)
	}
	if outcome.Disposition == store.DomainOutcomePublished &&
		(!published || publishedRun == nil ||
			outcome.RunID != publishedRun.ID ||
			outcome.Generation.Extractor != publishedRun.Extractor) {
		return nil, fmt.Errorf(
			"%w: published extraction domain outcome for %q/%q does not match published evidence",
			errCertificateDomainTransition, scope.Repository, scope.Domain,
		)
	}

	projected := &CertificateDomainOutcome{
		Disposition:             outcome.Disposition,
		GenerationDigest:        generationDigest,
		Extractor:               outcome.Generation.Extractor,
		CandidateControlFailure: outcome.CandidateControlFailure,
		ReceiptSchema:           outcome.ReceiptSchema,
	}
	schemaOnly := `{"schema":"` + store.ExtractionOutcomeReceiptSchema + `"}`
	if outcome.Receipt == schemaOnly {
		projected.ReceiptState = "schema_only"
		return projected, nil
	}

	var receipt ExtractionDomainOutcomeReceipt
	if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
		return nil, fmt.Errorf(
			"latest extraction domain outcome for %q/%q has an invalid receipt: %w",
			scope.Repository, scope.Domain, err,
		)
	}
	receiptState := "full"
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf(
			"latest extraction domain outcome for %q/%q has a non-canonical or unknown receipt shape",
			scope.Repository, scope.Domain,
		)
	}
	if string(canonical) != outcome.Receipt {
		// The excluded-source/SCIP counters were added at T30.6e under the
		// unchanged v1 receipt schema, so a retained receipt that reproduces
		// the exact pre-T30.6e wire shape is legitimate, not unknown. Byte
		// equality against the legacy shape also proves every absent counter
		// decoded as zero.
		legacy, legacyErr := marshalLegacyExclusionReceipt(receipt)
		if legacyErr != nil || string(legacy) != outcome.Receipt {
			return nil, fmt.Errorf(
				"latest extraction domain outcome for %q/%q has a non-canonical or unknown receipt shape",
				scope.Repository, scope.Domain,
			)
		}
		receiptState = "legacy_exclusion_shape"
	}
	if receipt.Schema != store.ExtractionOutcomeReceiptSchema ||
		receipt.Schema != outcome.ReceiptSchema ||
		receipt.Domain != scope.Domain ||
		receipt.ExtractorVersion != outcome.Generation.Extractor ||
		receipt.Disposition != outcome.Disposition ||
		!validOperationReason(receipt.Reason) ||
		!validCertificateOutcomeReceipt(receipt) {
		return nil, fmt.Errorf(
			"latest extraction domain outcome for %q/%q has an inconsistent receipt",
			scope.Repository, scope.Domain,
		)
	}
	projected.ReceiptState = receiptState
	projected.Receipt = &CertificateOutcomeReceipt{
		ExtractionDomainOutcomeReceipt: receipt,
		raw: json.RawMessage(
			append([]byte(nil), outcome.Receipt...),
		),
	}
	return projected, nil
}

// marshalLegacyExclusionReceipt reproduces the exact T30.6c-through-T30.6d
// (pre-T30.6e) receipt wire shape: identical field order with the four
// excluded-source/SCIP counters and the excluded-source declared bytes absent.
// Limits never changed across the T30.6e boundary. The one-commit T30.6b era
// additionally lacked nine limits fields and fails this shape too. Supporting
// It would also require relaxing the positive-limits checks and remains
// explicitly out of scope unless such vintage rows are actually retained.
func marshalLegacyExclusionReceipt(
	receipt ExtractionDomainOutcomeReceipt,
) ([]byte, error) {
	type legacyCounts struct {
		CorpusFiles          int `json:"corpus_files"`
		CandidateFiles       int `json:"candidate_files"`
		OpenedSourceAttempts int `json:"opened_source_attempts"`
		OpenedSourceFiles    int `json:"opened_source_files"`
		Facts                int `json:"facts"`
		Atoms                int `json:"atoms"`
		Assertions           int `json:"assertions"`
		Unresolved           int `json:"unresolved"`
		StagedChunks         int `json:"staged_chunks"`
		StagedRows           int `json:"staged_rows"`
	}
	type legacyBytes struct {
		PlannedDeclared int64 `json:"planned_declared"`
		OpenedSource    int64 `json:"opened_source"`
	}
	return json.Marshal(struct {
		Schema           string                          `json:"schema"`
		Domain           string                          `json:"domain"`
		ExtractorVersion string                          `json:"extractor_version"`
		Disposition      store.DomainOutcomeDisposition  `json:"disposition"`
		Reason           string                          `json:"reason"`
		InventoryMS      int64                           `json:"inventory_ms"`
		OpenedSourceMS   int64                           `json:"opened_source_ms"`
		ExtractorMS      int64                           `json:"extractor_ms"`
		StagingMS        int64                           `json:"staging_ms"`
		Counts           legacyCounts                    `json:"counts"`
		Bytes            legacyBytes                     `json:"bytes"`
		Limits           ExtractionOperationDomainLimits `json:"limits"`
	}{
		Schema:           receipt.Schema,
		Domain:           receipt.Domain,
		ExtractorVersion: receipt.ExtractorVersion,
		Disposition:      receipt.Disposition,
		Reason:           receipt.Reason,
		InventoryMS:      receipt.InventoryMS,
		OpenedSourceMS:   receipt.OpenedSourceMS,
		ExtractorMS:      receipt.ExtractorMS,
		StagingMS:        receipt.StagingMS,
		Counts: legacyCounts{
			CorpusFiles:          receipt.Counts.CorpusFiles,
			CandidateFiles:       receipt.Counts.CandidateFiles,
			OpenedSourceAttempts: receipt.Counts.OpenedSourceAttempts,
			OpenedSourceFiles:    receipt.Counts.OpenedSourceFiles,
			Facts:                receipt.Counts.Facts,
			Atoms:                receipt.Counts.Atoms,
			Assertions:           receipt.Counts.Assertions,
			Unresolved:           receipt.Counts.Unresolved,
			StagedChunks:         receipt.Counts.StagedChunks,
			StagedRows:           receipt.Counts.StagedRows,
		},
		Bytes: legacyBytes{
			PlannedDeclared: receipt.Bytes.PlannedDeclared,
			OpenedSource:    receipt.Bytes.OpenedSource,
		},
		Limits: receipt.Limits,
	})
}

func validCertificateOutcomeReceipt(
	receipt ExtractionDomainOutcomeReceipt,
) bool {
	if receipt.InventoryMS < 0 || receipt.OpenedSourceMS < 0 ||
		receipt.ExtractorMS < 0 || receipt.StagingMS < 0 {
		return false
	}
	counts := receipt.Counts
	if counts.CorpusFiles < 0 || counts.CandidateFiles < 0 ||
		counts.ExcludedSourceFiles < 0 || counts.ExcludedSCIPDocuments < 0 ||
		counts.ExcludedSCIPDefinitions < 0 || counts.ExcludedSCIPOccurrences < 0 ||
		counts.OpenedSourceAttempts < 0 || counts.OpenedSourceFiles < 0 ||
		counts.Facts < 0 || counts.Atoms < 0 || counts.Assertions < 0 ||
		counts.Unresolved < 0 || counts.StagedChunks < 0 || counts.StagedRows < 0 ||
		counts.CandidateFiles > counts.CorpusFiles ||
		counts.ExcludedSourceFiles > counts.CorpusFiles ||
		counts.ExcludedSourceFiles >
			counts.CorpusFiles-counts.CandidateFiles ||
		counts.OpenedSourceFiles > counts.OpenedSourceAttempts ||
		counts.ExcludedSCIPDefinitions > counts.ExcludedSCIPOccurrences ||
		counts.ExcludedSCIPDocuments == 0 &&
			(counts.ExcludedSCIPDefinitions != 0 ||
				counts.ExcludedSCIPOccurrences != 0) {
		return false
	}
	bytes := receipt.Bytes
	if bytes.PlannedDeclared < 0 || bytes.ExcludedSourceDeclared < 0 ||
		bytes.OpenedSource < 0 ||
		bytes.ExcludedSourceDeclared > bytes.PlannedDeclared ||
		counts.ExcludedSourceFiles == 0 && bytes.ExcludedSourceDeclared != 0 {
		return false
	}
	limits := receipt.Limits
	if limits.CorpusFiles <= 0 || limits.OpenedSourceAttempts <= 0 ||
		limits.OpenedSourceFiles <= 0 || limits.OpenedSourceBytes <= 0 ||
		limits.Facts <= 0 || limits.SourceBlobBytes <= 0 ||
		limits.TypedInputBytes <= 0 || limits.AggregateWallMS <= 0 ||
		limits.MirrorLockMS <= 0 || limits.DomainWallMS <= 0 ||
		limits.AbortWallMS <= 0 || limits.OutcomeWallMS <= 0 ||
		limits.MaxSerialDomains <= 0 || limits.SchedulerIdentityBytes <= 0 ||
		limits.AggregateStagedRows <= 0 || limits.DomainStagedRows <= 0 ||
		counts.CorpusFiles > certificateMaxCorpusCensus ||
		counts.CandidateFiles > limits.CorpusFiles ||
		counts.ExcludedSourceFiles >
			limits.CorpusFiles-counts.CandidateFiles ||
		counts.OpenedSourceAttempts > limits.OpenedSourceAttempts ||
		counts.OpenedSourceFiles > limits.OpenedSourceFiles ||
		bytes.OpenedSource > limits.OpenedSourceBytes ||
		counts.Facts > limits.Facts ||
		counts.StagedRows > limits.DomainStagedRows ||
		counts.StagedRows > limits.AggregateStagedRows {
		return false
	}
	return validCertificateOutcomeReason(
		receipt.Disposition, receipt.Reason,
	)
}

func validCertificateOutcomeReason(
	disposition store.DomainOutcomeDisposition,
	reason string,
) bool {
	switch disposition {
	case store.DomainOutcomePublished:
		return reason == OperationReasonNoCandidates ||
			reason == OperationReasonTypedInputAbsent ||
			reason == OperationReasonPublishedEmpty ||
			reason == OperationReasonPublishedNonempty
	case store.DomainOutcomeUnavailablePrerequisite:
		return reason == OperationReasonTypedInputAbsent
	case store.DomainOutcomeTerminalGenerationRefusal:
		// A mid-run budget refusal joined with an abort failure retains the
		// budget reason the domain recorder had already completed with, so
		// both budget reasons are durably recordable as terminal refusals.
		return reason == OperationReasonLimitRefusal ||
			reason == OperationReasonFailed ||
			reason == OperationReasonAggregateBudget ||
			reason == OperationReasonDomainBudget
	case store.DomainOutcomeRetryableFailure:
		return reason == OperationReasonNotReady ||
			reason == OperationReasonNoCandidates ||
			reason == OperationReasonTypedInputAbsent ||
			reason == OperationReasonPublishedEmpty ||
			reason == OperationReasonPublishedNonempty ||
			reason == OperationReasonAggregateBudget ||
			reason == OperationReasonDomainBudget ||
			reason == OperationReasonCanceled ||
			reason == OperationReasonFailed
	default:
		return false
	}
}

func validateCertificateAttempt(
	scope store.ExtractionScope,
	attempt *store.ExtractionAttempt,
	publishedRun *store.ExtractionRun,
	published bool,
) (*CertificateAttempt, error) {
	if attempt == nil {
		return nil, nil
	}
	if attempt.RunID == "" ||
		attempt.Repo != scope.Repository ||
		attempt.Commit != scope.Commit ||
		attempt.UnitDigest != scope.UnitDigest ||
		attempt.Domain != scope.Domain ||
		attempt.Extractor == "" ||
		attempt.Status != "staged" && attempt.Status != "published" && attempt.Status != "aborted" {
		return nil, fmt.Errorf(
			"latest extraction attempt for %q/%q is inconsistent",
			scope.Repository, scope.Domain,
		)
	}
	if attempt.Status == "published" && (!published || publishedRun == nil ||
		publishedRun.ID != attempt.RunID || publishedRun.Commit != attempt.Commit ||
		publishedRun.UnitDigest != attempt.UnitDigest ||
		publishedRun.Extractor != attempt.Extractor) {
		return nil, fmt.Errorf(
			"%w: published attempt for %q/%q does not match published evidence",
			errCertificateDomainTransition, scope.Repository, scope.Domain,
		)
	}
	result := &CertificateAttempt{
		RunID: attempt.RunID, Commit: attempt.Commit,
		UnitDigest: attempt.UnitDigest,
		Extractor:  attempt.Extractor, Status: attempt.Status,
	}
	if attempt.Status == "aborted" {
		result.Failure = "extraction aborted before publication"
	}
	return result, nil
}

func certificateDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, errors.New("coverage certificate requires at least one domain")
	}
	if len(domains) > 64 {
		return nil, errors.New("coverage certificate accepts at most 64 domains")
	}
	list := append([]string(nil), domains...)
	sort.Strings(list)
	for i, domain := range list {
		if !validToken(domain) || i > 0 && list[i-1] == domain {
			return nil, fmt.Errorf("certificate domain set is inconsistent at %q", domain)
		}
	}
	return list, nil
}

func certificateDigest(certificate *CoverageCertificate) (string, error) {
	unsigned := *certificate
	unsigned.Digest = ""
	data, err := json.Marshal(unsigned)
	if err != nil {
		return "", fmt.Errorf("canonicalize coverage certificate: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
