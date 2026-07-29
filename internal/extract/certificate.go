package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/bmeddeb/phebs/internal/store"
)

// certificateSchemaVersion names the T13.3 per-answer coverage certificate.
const certificateSchemaVersion = "coverage-certificate-v2"

// RunSource is the narrow read surface the certificate builder consumes. It is
// deliberately not the full EvidenceStore: the builder can look up published
// evidence and the durable latest-attempt marker, and nothing else.
type RunSource interface {
	LatestPublishedRun(ctx context.Context, scope store.ExtractionScope) (*store.ExtractionRun, error)
	LatestExtractionAttempt(ctx context.Context, scope store.ExtractionScope) (*store.ExtractionAttempt, error)
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
}

// CertificateCandidateScope discloses the exact candidate-manifest projection
// used by one domain. Zero counts remain explicit inside this optional object;
// absence means a readable legacy/direct-corpus run.
type CertificateCandidateScope struct {
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
			entry.ScopePosture = "focused"
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
			run, err := source.LatestPublishedRun(ctx, scope)
			published := err == nil
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("latest published run for %q/%q: %w", repo.Name, domain, err)
			}
			if published && (run == nil || run.ID == "" || run.Repo != repo.Name || run.Domain != domain ||
				run.Status != "published" || run.Commit != scope.Commit ||
				run.UnitDigest != scope.UnitDigest || run.Extractor == "") {
				return nil, fmt.Errorf("published run for %q/%q is inconsistent", repo.Name, domain)
			}
			attempt, attemptErr := source.LatestExtractionAttempt(ctx, scope)
			if errors.Is(attemptErr, store.ErrNotFound) {
				attempt = nil
			} else if attemptErr != nil {
				return nil, fmt.Errorf("latest extraction attempt for %q/%q: %w", repo.Name, domain, attemptErr)
			}
			if attempt == nil && published {
				attempt = &store.ExtractionAttempt{
					RunID: run.ID, Repo: run.Repo, Commit: run.Commit, Domain: run.Domain,
					UnitDigest: run.UnitDigest, Extractor: run.Extractor, Status: "published",
				}
			}
			certificateAttempt, err := validateCertificateAttempt(scope, attempt, run, published)
			if err != nil {
				return nil, err
			}
			if !published {
				entry.Runs = append(entry.Runs, CertificateRun{
					Domain: domain, Status: "unpublished", UnitDigest: scope.UnitDigest,
					LatestAttempt: certificateAttempt,
				})
				continue
			}
			protocols := append([]string(nil), run.Coverage.Protocols...)
			failures := append([]string(nil), run.Coverage.Failures...)
			sort.Strings(protocols)
			sort.Strings(failures)
			fresh := run.Commit == repo.IndexedCommitHash &&
				run.UnitDigest == scope.UnitDigest
			candidateScope := certificateCandidateScope(run.Coverage)
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
) *CertificateCandidateScope {
	if coverage.CandidateManifestDigest == "" {
		return nil
	}
	scope := &CertificateCandidateScope{
		ManifestDigest:       coverage.CandidateManifestDigest,
		Plane:                coverage.CandidatePlane,
		CorpusFileCount:      coverage.ScopeCorpusFileCount,
		CorpusDeclaredBytes:  coverage.ScopeCorpusDeclaredBytes,
		CorpusDigest:         coverage.ScopeCorpusDigest,
		PlannedFileCount:     coverage.PlannedFileCount,
		PlannedRequiredCount: coverage.PlannedRequiredFileCount,
		PlannedDeclaredBytes: coverage.PlannedDeclaredBytes,
		PlannedDigest:        coverage.PlannedScopeDigest,
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
	return scope
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
			"published attempt for %q/%q does not match published evidence",
			scope.Repository, scope.Domain,
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
