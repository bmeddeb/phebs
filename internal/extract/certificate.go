package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

// certificateSchemaVersion names the T13.3 per-answer coverage certificate.
const certificateSchemaVersion = "coverage-certificate-v1"

// RunSource is the narrow read surface the certificate builder consumes. It is
// deliberately not the full EvidenceStore: the builder can look up published
// runs and nothing else.
type RunSource interface {
	LatestPublishedRun(ctx context.Context, repo, domain string) (*store.ExtractionRun, error)
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

// CertificateRun is the latest published evidence state for one
// (repository, domain) pair. A failed extraction never publishes, so failure
// surfaces here as an `unpublished` entry or a stale (`fresh: false`) run —
// the certificate records what the evidence is, not what was attempted.
type CertificateRun struct {
	Domain          string     `json:"domain"`
	Status          string     `json:"status"` // published | unpublished
	Extractor       string     `json:"extractor,omitempty"`
	Commit          string     `json:"commit,omitempty"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	Fresh           bool       `json:"fresh"` // run commit == repository's indexed commit
	Protocols       []string   `json:"protocols,omitempty"`
	Failures        []string   `json:"failures,omitempty"`
	UnresolvedCount int        `json:"unresolved_count"`
	AssertionCount  int        `json:"assertion_count"`
	AtomCount       int        `json:"atom_count"`
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
			if errors.Is(err, store.ErrNotFound) {
				entry.Runs = append(entry.Runs, CertificateRun{Domain: domain, Status: "unpublished"})
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("latest published run for %q/%q: %w", repo.Name, domain, err)
			}
			if run == nil || run.ID == "" || run.Repo != repo.Name || run.Domain != domain ||
				run.Status != "published" || run.Commit == "" {
				return nil, fmt.Errorf("published run for %q/%q is inconsistent", repo.Name, domain)
			}
			protocols := append([]string(nil), run.Coverage.Protocols...)
			failures := append([]string(nil), run.Coverage.Failures...)
			sort.Strings(protocols)
			sort.Strings(failures)
			var publishedAt *time.Time
			if run.PublishedAt != nil {
				utc := run.PublishedAt.UTC()
				publishedAt = &utc
			}
			entry.Runs = append(entry.Runs, CertificateRun{
				Domain:          domain,
				Status:          "published",
				Extractor:       run.Extractor,
				Commit:          run.Commit,
				PublishedAt:     publishedAt,
				Fresh:           run.Commit == repo.IndexedCommitHash,
				Protocols:       protocols,
				Failures:        failures,
				UnresolvedCount: run.Coverage.UnresolvedCount,
				AssertionCount:  run.Coverage.AssertionCount,
				AtomCount:       run.Coverage.AtomCount,
			})
			for _, protocol := range protocols {
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

func certificateDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, errors.New("coverage certificate requires at least one domain")
	}
	list := append([]string(nil), domains...)
	sort.Strings(list)
	for i, domain := range list {
		if domain == "" || i > 0 && list[i-1] == domain {
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
