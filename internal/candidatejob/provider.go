package candidatejob

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

// Provider strictly resolves database pointers into immutable filesystem
// publications for the extraction harness.
type Provider struct {
	root       string
	store      store.CandidateManifestPublicationStore
	policies   *PolicySet
	domains    []extract.CandidateManifestDomain
	domainKeys map[string]struct{}
}

// NewProvider constructs an adapter from the same PolicySet used by its
// planner.
func NewProvider(
	dataDir string,
	state store.CandidateManifestPublicationStore,
	policies *PolicySet,
) (*Provider, error) {
	if state == nil {
		return nil, errors.New("candidate provider store is required")
	}
	if err := validateDataDir(dataDir); err != nil {
		return nil, err
	}
	if err := policies.validate(); err != nil {
		return nil, fmt.Errorf("candidate provider policies: %w", err)
	}
	domains := make([]extract.CandidateManifestDomain, len(policies.identities))
	keys := make(map[string]struct{}, len(policies.identities))
	for index, identity := range policies.identities {
		domains[index] = extract.CandidateManifestDomain{
			Domain: identity.Domain, Version: identity.Version,
		}
		keys[domainKey(identity.Domain, identity.Version)] = struct{}{}
	}
	return &Provider{
		root:       CandidateRoot(dataDir),
		store:      state,
		policies:   policies,
		domains:    domains,
		domainKeys: keys,
	}, nil
}

// PolicyDigest exposes the exact policy identity expected by this adapter.
func (provider *Provider) PolicyDigest() string {
	if provider == nil || provider.policies == nil {
		return ""
	}
	return provider.policies.digest
}

func (provider *Provider) OpenCandidateManifest(
	ctx context.Context,
	request extract.CandidateManifestRequest,
) (extract.CandidateManifest, error) {
	if provider == nil || provider.store == nil || provider.policies == nil {
		return nil, errors.New("candidate provider is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !slices.Equal(request.Domains, provider.domains) {
		return nil, errors.New(
			"candidate manifest request domain set is partial, reordered, or stale",
		)
	}
	unitDigest, err := checkedUnitDigest(
		request.Repository, request.AnalysisUnit,
	)
	if err != nil {
		return nil, err
	}
	generation, err := candidate.GenerationDigest(
		request.Repository, request.Commit, unitDigest,
		provider.policies.identities,
	)
	if err != nil {
		return nil, fmt.Errorf("candidate request identity: %w", err)
	}
	pointer, err := provider.store.GetCandidateManifestPublication(
		ctx, request.Repository,
	)
	if err != nil {
		return nil, fmt.Errorf("load candidate publication pointer: %w", err)
	}
	if pointer == nil {
		return nil, errors.New("candidate publication store returned nil")
	}
	state, err := pointerState(*pointer)
	if err != nil {
		return nil, fmt.Errorf("candidate publication pointer: %w", err)
	}
	if state.Repository != request.Repository ||
		state.Commit != request.Commit ||
		state.UnitDigest != unitDigest ||
		state.PolicyDigest != provider.policies.digest ||
		state.GenerationDigest != generation ||
		state.Manifest != candidate.ManifestName(request.Repository) {
		return nil, errors.New(
			"candidate publication pointer does not match requested indexed generation",
		)
	}
	if err := ensureCandidateRoot(provider.root, false); err != nil {
		return nil, err
	}
	publication, err := candidate.OpenContext(ctx, provider.root, candidate.Expected{
		Repository: request.Repository, Commit: request.Commit,
		Unit:             analysisunit.CloneState(request.AnalysisUnit),
		Policies:         slices.Clone(provider.policies.identities),
		PolicyDigest:     provider.policies.digest,
		GenerationDigest: generation,
		ManifestDigest:   state.ManifestDigest,
	})
	if err != nil {
		return nil, fmt.Errorf("open candidate publication: %w", err)
	}
	if publication.State() != state {
		return nil, errors.New(
			"candidate publication bytes do not match the persisted pointer",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &manifestAdapter{
		publication: publication,
		allowed:     cloneSet(provider.domainKeys),
	}, nil
}

type manifestAdapter struct {
	publication *candidate.Publication
	allowed     map[string]struct{}
}

func (manifest *manifestAdapter) Identity() string {
	if manifest == nil || manifest.publication == nil {
		return ""
	}
	return manifest.publication.State().ManifestDigest
}

func (manifest *manifestAdapter) CorpusFileCount() int {
	if manifest == nil || manifest.publication == nil {
		return -1
	}
	return manifest.publication.Corpus().RegularCount
}

func (manifest *manifestAdapter) GitlinkBoundaries() extract.CandidateManifestGitlinks {
	if manifest == nil || manifest.publication == nil {
		return extract.CandidateManifestGitlinks{Count: -1}
	}
	corpus := manifest.publication.Corpus()
	return extract.CandidateManifestGitlinks{
		Count: corpus.GitlinkCount, Digest: corpus.GitlinkDigest,
		SampleTruncated: corpus.GitlinkCount > 0,
	}
}

func (manifest *manifestAdapter) ForEachRepositoryFile(
	ctx context.Context,
	domain, version string,
	visit func(extract.CandidateManifestFile) error,
) error {
	if manifest == nil || manifest.publication == nil || visit == nil {
		return errors.New("candidate manifest replay is invalid")
	}
	if _, ok := manifest.allowed[domainKey(domain, version)]; !ok {
		return fmt.Errorf("candidate domain %s/%s is outside the request", domain, version)
	}
	view, err := manifest.publication.Domain(domain, version)
	if err != nil {
		return err
	}
	return view.ForEachRepositoryRecord(ctx, func(record candidate.Record) error {
		return visit(extract.CandidateManifestFile{
			Path: record.Path, ObjectID: record.OID,
			DeclaredBytes: record.DeclaredBytes,
			Required:      record.Required,
			InUnit:        record.InUnit,
		})
	})
}

func domainKey(domain, version string) string {
	return domain + "\x00" + version
}

func cloneSet(input map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(input))
	for key := range input {
		result[key] = struct{}{}
	}
	return result
}

var _ extract.CandidateManifestProvider = (*Provider)(nil)
var _ extract.CandidateManifest = (*manifestAdapter)(nil)
