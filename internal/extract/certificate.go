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
const certificateSchemaVersion = "coverage-certificate-v1"

// RunSource is the narrow read surface the certificate builder consumes. It is
// deliberately not the full EvidenceStore: the builder can look up published
// evidence and the durable latest-attempt marker, and nothing else.
type RunSource interface {
	LatestPublishedRun(ctx context.Context, repo, domain string) (*store.ExtractionRun, error)
	LatestExtractionAttempt(ctx context.Context, repo, domain string) (*store.ExtractionAttempt, error)
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
	Repository    string           `json:"repository"`
	IndexedCommit string           `json:"indexed_commit,omitempty"`
	SCIPIndex     string           `json:"scip_index"` // present | absent | unknown
	Runs          []CertificateRun `json:"runs"`
}

// CertificateRun binds one (repository, domain) pair to both its latest
// published evidence and its latest extraction attempt. Failed replacements
// remain visible here even though they never replace published evidence.
type CertificateRun struct {
	Domain             string              `json:"domain"`
	Status             string              `json:"status"` // published | unpublished
	RunID              string              `json:"run_id,omitempty"`
	Extractor          string              `json:"extractor,omitempty"`
	Commit             string              `json:"commit,omitempty"`
	Fresh              bool                `json:"fresh"` // run commit == repository's indexed commit
	Protocols          []string            `json:"protocols,omitempty"`
	Failures           []string            `json:"failures,omitempty"`
	CorpusFileCount    int                 `json:"corpus_file_count"`
	CandidateFileCount int                 `json:"candidate_file_count"`
	ReadFileCount      int                 `json:"read_file_count"`
	ReadBytes          int64               `json:"read_bytes"`
	SourceScopeDigest  string              `json:"source_scope_digest,omitempty"`
	UnresolvedCount    int                 `json:"unresolved_count"`
	AssertionCount     int                 `json:"assertion_count"`
	AtomCount          int                 `json:"atom_count"`
	LatestAttempt      *CertificateAttempt `json:"latest_attempt,omitempty"`
}

// CertificateAttempt is deliberately time-free: identity, input revision,
// extractor, and state describe the attempt without making an unchanged
// evidence state differ merely because it was processed later.
type CertificateAttempt struct {
	RunID     string `json:"run_id"`
	Commit    string `json:"commit"`
	Extractor string `json:"extractor"`
	Status    string `json:"status"` // staged | published | aborted
	Failure   string `json:"failure,omitempty"`
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
			SCIPIndex:     "unknown",
			Runs:          make([]CertificateRun, 0, len(domainList)),
		}
		for _, domain := range domainList {
			run, err := source.LatestPublishedRun(ctx, repo.Name, domain)
			published := err == nil
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("latest published run for %q/%q: %w", repo.Name, domain, err)
			}
			if published && (run == nil || run.ID == "" || run.Repo != repo.Name || run.Domain != domain ||
				run.Status != "published" || run.Commit == "" || run.Extractor == "") {
				return nil, fmt.Errorf("published run for %q/%q is inconsistent", repo.Name, domain)
			}
			attempt, attemptErr := source.LatestExtractionAttempt(ctx, repo.Name, domain)
			if errors.Is(attemptErr, store.ErrNotFound) {
				attempt = nil
			} else if attemptErr != nil {
				return nil, fmt.Errorf("latest extraction attempt for %q/%q: %w", repo.Name, domain, attemptErr)
			}
			if attempt == nil && published {
				attempt = &store.ExtractionAttempt{
					RunID: run.ID, Repo: run.Repo, Commit: run.Commit, Domain: run.Domain,
					Extractor: run.Extractor, Status: "published",
				}
			}
			certificateAttempt, err := validateCertificateAttempt(repo.Name, domain, attempt, run, published)
			if err != nil {
				return nil, err
			}
			if !published {
				entry.Runs = append(entry.Runs, CertificateRun{
					Domain: domain, Status: "unpublished", LatestAttempt: certificateAttempt,
				})
				continue
			}
			protocols := append([]string(nil), run.Coverage.Protocols...)
			failures := append([]string(nil), run.Coverage.Failures...)
			sort.Strings(protocols)
			sort.Strings(failures)
			fresh := run.Commit == repo.IndexedCommitHash
			entry.Runs = append(entry.Runs, CertificateRun{
				Domain: domain, Status: "published", RunID: run.ID,
				Extractor: run.Extractor, Commit: run.Commit, Fresh: fresh,
				Protocols: protocols, Failures: failures,
				CorpusFileCount:    run.Coverage.CorpusFileCount,
				CandidateFileCount: run.Coverage.CandidateFileCount,
				ReadFileCount:      run.Coverage.ReadFileCount,
				ReadBytes:          run.Coverage.ReadBytes,
				SourceScopeDigest:  run.Coverage.SourceScopeDigest,
				UnresolvedCount:    run.Coverage.UnresolvedCount,
				AssertionCount:     run.Coverage.AssertionCount,
				AtomCount:          run.Coverage.AtomCount,
				LatestAttempt:      certificateAttempt,
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

func validateCertificateAttempt(repo, domain string, attempt *store.ExtractionAttempt, publishedRun *store.ExtractionRun, published bool) (*CertificateAttempt, error) {
	if attempt == nil {
		return nil, nil
	}
	if attempt.RunID == "" || attempt.Repo != repo || attempt.Domain != domain ||
		attempt.Commit == "" || attempt.Extractor == "" ||
		attempt.Status != "staged" && attempt.Status != "published" && attempt.Status != "aborted" {
		return nil, fmt.Errorf("latest extraction attempt for %q/%q is inconsistent", repo, domain)
	}
	if attempt.Status == "published" && (!published || publishedRun == nil ||
		publishedRun.ID != attempt.RunID || publishedRun.Commit != attempt.Commit ||
		publishedRun.Extractor != attempt.Extractor) {
		return nil, fmt.Errorf("published attempt for %q/%q does not match published evidence", repo, domain)
	}
	result := &CertificateAttempt{
		RunID: attempt.RunID, Commit: attempt.Commit,
		Extractor: attempt.Extractor, Status: attempt.Status,
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
