// Package callerexecute schedules and executes direct caller-domain candidate
// leaves against one exact immutable resolver catalog.
package callerexecute

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

type Adapter struct {
	Domain   string
	Version  string
	Protocol string
}

type Registry struct {
	adapters         []Adapter
	candidateDomains []extract.CandidateManifestDomain
}

func NewRegistry(extractors []extract.Extractor) (*Registry, error) {
	registry := &Registry{}
	seen := make(map[string]struct{}, len(extractors))
	for index, extractor := range extractors {
		if extractor == nil {
			return nil, fmt.Errorf("caller registry extractor %d is nil", index)
		}
		domain, version := extractor.Domain(), extractor.Version()
		if domain == "" || version == "" || strings.TrimSpace(domain) != domain ||
			strings.TrimSpace(version) != version {
			return nil, fmt.Errorf("caller registry extractor %d has invalid identity", index)
		}
		if _, duplicate := seen[domain]; duplicate {
			return nil, fmt.Errorf("caller registry domain %q is duplicated", domain)
		}
		seen[domain] = struct{}{}
		registry.candidateDomains = append(
			registry.candidateDomains,
			extract.CandidateManifestDomain{Domain: domain, Version: version},
		)
		protocol := ""
		switch domain {
		case "grpc-caller":
			protocol = "grpc"
		case "thrift-caller":
			protocol = "thrift"
		}
		if protocol != "" {
			registry.adapters = append(registry.adapters, Adapter{
				Domain: domain, Version: version, Protocol: protocol,
			})
		}
	}
	sort.Slice(registry.candidateDomains, func(i, j int) bool {
		if registry.candidateDomains[i].Domain != registry.candidateDomains[j].Domain {
			return registry.candidateDomains[i].Domain < registry.candidateDomains[j].Domain
		}
		return registry.candidateDomains[i].Version < registry.candidateDomains[j].Version
	})
	sort.Slice(registry.adapters, func(i, j int) bool {
		return registry.adapters[i].Domain < registry.adapters[j].Domain
	})
	if len(registry.adapters) > callerleaf.MaxCallerDomains {
		return nil, callerleaf.ErrLimit
	}
	return registry, nil
}

func (registry *Registry) Enabled() bool {
	return registry != nil && len(registry.adapters) > 0
}

func (registry *Registry) Adapters() []Adapter {
	if registry == nil {
		return nil
	}
	return slices.Clone(registry.adapters)
}

func (registry *Registry) CandidateDomains() []extract.CandidateManifestDomain {
	if registry == nil {
		return nil
	}
	return slices.Clone(registry.candidateDomains)
}

func (registry *Registry) validate() error {
	if registry == nil || len(registry.adapters) == 0 || len(registry.candidateDomains) == 0 {
		return errors.New("caller registry is empty")
	}
	for index, adapter := range registry.adapters {
		if adapter.Domain == "" || adapter.Version == "" ||
			adapter.Protocol != "grpc" && adapter.Protocol != "thrift" ||
			index > 0 && registry.adapters[index-1].Domain >= adapter.Domain {
			return errors.New("caller registry is invalid or unordered")
		}
	}
	return nil
}

type GenerationAuthority struct {
	Repository *store.Repo
	Candidate  *store.CandidateManifestPublication
	Resolver   *store.ResolverCatalogPublication
}

func GenerationIdentity(
	authority GenerationAuthority,
	registry *Registry,
) (callerleaf.GenerationIdentity, error) {
	if err := registry.validate(); err != nil {
		return callerleaf.GenerationIdentity{}, err
	}
	if authority.Repository == nil || authority.Candidate == nil || authority.Resolver == nil {
		return callerleaf.GenerationIdentity{}, errors.New("caller generation authority is incomplete")
	}
	repository, candidate, resolver :=
		authority.Repository, authority.Candidate, authority.Resolver
	unitDigest := ""
	if repository.IndexedAnalysisUnit != nil {
		unitDigest = repository.IndexedAnalysisUnit.Digest
	}
	if repository.Name != candidate.Repository || repository.Name != resolver.Repository ||
		repository.IndexedCommitHash != candidate.HeadCommit ||
		repository.IndexedCommitHash != resolver.HeadCommit ||
		unitDigest != candidate.UnitDigest || unitDigest != resolver.UnitDigest ||
		candidate.ManifestDigest != resolver.CandidateManifestDigest {
		return callerleaf.GenerationIdentity{}, errors.New("caller generation authorities disagree")
	}
	extractors := make([]callerleaf.ExtractorIdentity, len(registry.adapters))
	for index, adapter := range registry.adapters {
		extractors[index] = callerleaf.ExtractorIdentity{
			Domain: adapter.Domain, Version: adapter.Version,
			LeafAdapterVersion: callerleaf.LeafAdapterV1,
		}
	}
	return callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository: repository.Name, HeadCommit: repository.IndexedCommitHash,
		UnitDigest:               unitDigest,
		DeclarationSetDigest:     resolver.DeclarationSetDigest,
		CandidateManifestDigest:  candidate.ManifestDigest,
		CandidatePolicyDigest:    candidate.PolicyDigest,
		ResolverGenerationDigest: resolver.GenerationDigest,
		ResolverManifestDigest:   resolver.ManifestDigest,
		Extractors:               extractors,
	})
}

type ExpectedPair struct {
	Adapter   Adapter
	Candidate extract.CandidateCallerLeaf
	Identity  callerleaf.PairIdentity
}

func ExpectedPairs(
	plan extract.CandidateCallerPlan,
	generation callerleaf.GenerationIdentity,
	registry *Registry,
) ([]ExpectedPair, string, error) {
	if plan == nil || plan.Identity() != generation.CandidateManifestDigest {
		return nil, "", errors.New("caller plan does not match generation")
	}
	planDomains := plan.CallerDomains()
	if len(planDomains) != len(registry.adapters) {
		return nil, "", errors.New("candidate caller domain set is partial or stale")
	}
	for index, adapter := range registry.adapters {
		if planDomains[index].Domain != adapter.Domain ||
			planDomains[index].Version != adapter.Version {
			return nil, "", errors.New("candidate caller domain set is reordered or stale")
		}
	}
	leaves := plan.CallerLeaves()
	if len(registry.adapters) > 0 && len(leaves) > callerleaf.MaxExpectedPairs/len(registry.adapters) {
		return nil, "", callerleaf.ErrLimit
	}
	result := make([]ExpectedPair, 0, len(registry.adapters)*len(leaves))
	for _, adapter := range registry.adapters {
		for _, leaf := range leaves {
			descriptor := callerleaf.LeafDescriptor{
				Name: leaf.Name, Ordinal: leaf.Ordinal,
				Prefix: leaf.Prefix, PrefixBits: leaf.PrefixBits,
				RecordCount: leaf.RecordCount, DeclaredBytes: leaf.DeclaredBytes,
				ContentBytes: leaf.ContentBytes, ContentDigest: leaf.ContentDigest,
			}
			identity, err := callerleaf.NewPairIdentity(
				generation, adapter.Domain, adapter.Version, descriptor,
			)
			if err != nil {
				return nil, "", err
			}
			result = append(result, ExpectedPair{
				Adapter: adapter, Candidate: leaf, Identity: identity,
			})
		}
	}
	identities := make([]callerleaf.PairIdentity, len(result))
	for index := range result {
		identities[index] = result[index].Identity
	}
	digest, err := callerleaf.PairSetDigest(generation, identities)
	return result, digest, err
}
