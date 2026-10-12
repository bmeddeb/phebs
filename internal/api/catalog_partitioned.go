package api

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

// ContractCatalogAuthority names one exact partitioned declaration root a
// Contract Atlas response read (T49.3). The coverage certificate describes
// legacy published runs only; a repository/domain served from a current
// partitioned root appears here instead, never as a published legacy run.
type ContractCatalogAuthority struct {
	Repository string `json:"repository"`
	Domain     string `json:"domain"`
	RunID      string `json:"run_id"`
	Commit     string `json:"commit"`
	PlanDigest string `json:"plan_digest"`
	RootDigest string `json:"root_digest"`
}

// partitionedCatalogStore is the native capability set an exact partitioned
// declaration read needs; a store without it serves legacy runs only.
type partitionedCatalogStore interface {
	GetPartitionedExtractionDomainReference(ctx context.Context, repository, domain string) (store.PartitionedExtractionDomainReference, error)
	GetCandidateManifestPublication(ctx context.Context, repository string) (*store.CandidateManifestPublication, error)
	ListPartitionedAssertions(ctx context.Context, query store.AssertionQuery, authority store.PartitionedAssertionAuthority) ([]store.Assertion, error)
	ResolvePartitionedEvidence(ctx context.Context, authority store.PartitionedAssertionAuthority, atomID string) (*store.EvidenceResolution, error)
}

// The production evidence view is the store itself; a wrapper that drops a
// native method must fail to compile rather than silently serve legacy only.
var _ partitionedCatalogStore = (*store.Surreal)(nil)

type catalogSourceKey struct{ repository, domain string }

// catalogSource is the one exact evidence source for a repository/domain: a
// legacy published run, or the current partitioned root behind its fence.
type catalogSource struct {
	run       extract.CertificateRun
	evidence  store.EvidenceStore
	authority *ContractCatalogAuthority
	nonEmpty  bool
}

type catalogSources map[catalogSourceKey]catalogSource

func (sources catalogSources) lookup(repository, domain string) (catalogSource, bool) {
	source, ok := sources[catalogSourceKey{repository, domain}]
	return source, ok
}

func (sources catalogSources) authorities() []ContractCatalogAuthority {
	out := make([]ContractCatalogAuthority, 0)
	for _, source := range sources {
		if source.authority != nil {
			out = append(out, *source.authority)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repository != out[j].Repository {
			return out[i].Repository < out[j].Repository
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}

// stateDigest binds cursors and the confirm-after-build check to both the
// legacy certificate and the exact partitioned roots. Without partitioned
// roots it is the certificate digest, so existing cursors are unchanged.
func (sources catalogSources) stateDigest(certificateDigest string) string {
	authorities := sources.authorities()
	if len(authorities) == 0 {
		return certificateDigest
	}
	return digestJSON(struct {
		Certificate string                     `json:"certificate"`
		Partitioned []ContractCatalogAuthority `json:"partitioned"`
	}{certificateDigest, authorities})
}

// selectCatalogSources prefers a legacy published run and otherwise admits the
// current partitioned declaration root only when the store's own fence (the
// repository's indexed commit and analysis unit, the candidate manifest and
// policy, and the sealed staged run) proves it current right now. A stale or
// absent root leaves that repository/domain unavailable, never partial.
func selectCatalogSources(
	ctx context.Context,
	evidence store.EvidenceStore,
	certificate *extract.CoverageCertificate,
	visible []store.Repo,
) (catalogSources, error) {
	sources := make(catalogSources)
	native, partitioned := evidence.(partitionedCatalogStore)
	repos := make(map[string]store.Repo, len(visible))
	for _, repo := range visible {
		repos[repo.Name] = repo
	}
	for _, repository := range certificate.Repositories {
		for _, pack := range protocolPacks {
			key := catalogSourceKey{repository.Repository, pack.declarationDomain}
			if run, ok := certificateRun(repository, pack.declarationDomain); ok && run.Status == "published" {
				sources[key] = catalogSource{run: run, evidence: evidence, nonEmpty: run.AssertionCount > 0}
				continue
			}
			repo, ok := repos[repository.Repository]
			if !partitioned || !ok || repo.IndexedCommitHash == "" {
				continue
			}
			source, err := partitionedCatalogSource(ctx, evidence, native, repo, pack.declarationDomain)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			sources[key] = source
		}
	}
	return sources, nil
}

func partitionedCatalogSource(
	ctx context.Context,
	evidence store.EvidenceStore,
	native partitionedCatalogStore,
	repo store.Repo,
	domain string,
) (catalogSource, error) {
	// Bounded point reads: the root reference never transfers the canonical
	// plan/root bytes, and the candidate pointer supplies the policy digest.
	root, err := native.GetPartitionedExtractionDomainReference(ctx, repo.Name, domain)
	if err != nil {
		return catalogSource{}, err
	}
	unit := ""
	if repo.IndexedAnalysisUnit != nil {
		unit = repo.IndexedAnalysisUnit.Digest
	}
	manifest, err := native.GetCandidateManifestPublication(ctx, repo.Name)
	if err != nil {
		return catalogSource{}, err
	}
	if manifest.ManifestDigest != root.CandidateDigest || manifest.HeadCommit != repo.IndexedCommitHash ||
		manifest.UnitDigest != unit {
		// The root belongs to another candidate generation: not current here.
		return catalogSource{}, fmt.Errorf("partitioned declaration root is not current: %w", store.ErrNotFound)
	}
	authority := store.PartitionedAssertionAuthority{
		Repository: repo.Name, Domain: domain, RunID: root.RunID,
		PlanDigest: root.PlanDigest, RootDigest: root.RootDigest,
		Commit: repo.IndexedCommitHash, UnitDigest: unit,
		CandidateManifestDigest: root.CandidateDigest, CandidatePolicyDigest: manifest.PolicyDigest,
	}
	view := catalogPartitionedEvidence{EvidenceStore: evidence, native: native, authority: authority}
	// One bounded probe proves the root current under the store fence before
	// it is offered; later pages re-check it and report a transition as conflict.
	probe, err := view.ListAssertions(ctx, store.AssertionQuery{
		Repo: repo.Name, RunID: root.RunID, Limit: 1, AllowTruncate: true,
	})
	if err != nil {
		return catalogSource{}, err
	}
	return catalogSource{
		run: extract.CertificateRun{
			Domain: domain, RunID: root.RunID, Commit: repo.IndexedCommitHash, Fresh: true,
		},
		evidence: view,
		authority: &ContractCatalogAuthority{
			Repository: repo.Name, Domain: domain, RunID: root.RunID, Commit: repo.IndexedCommitHash,
			PlanDigest: root.PlanDigest, RootDigest: root.RootDigest,
		},
		nonEmpty: len(probe) > 0,
	}, nil
}

// catalogPartitionedEvidence routes the Atlas's assertion pages and source
// locators through the fenced native reads of exactly one partitioned root.
type catalogPartitionedEvidence struct {
	store.EvidenceStore
	native    partitionedCatalogStore
	authority store.PartitionedAssertionAuthority
}

func (source catalogPartitionedEvidence) ListAssertions(ctx context.Context, query store.AssertionQuery) ([]store.Assertion, error) {
	if query.Repo != source.authority.Repository || query.RunID != source.authority.RunID {
		return nil, store.ErrConflict
	}
	return source.native.ListPartitionedAssertions(ctx, query, source.authority)
}

func (source catalogPartitionedEvidence) ResolveEvidence(ctx context.Context, repository, runID, atomID string) (*store.EvidenceResolution, error) {
	if repository != source.authority.Repository || runID != source.authority.RunID {
		return nil, store.ErrConflict
	}
	return source.native.ResolvePartitionedEvidence(ctx, source.authority, atomID)
}
