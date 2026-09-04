package extractionpublication

import (
	"bytes"
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

type DownstreamDomainStore interface {
	GetPartitionedExtractionDomain(
		context.Context, string, string,
	) (*store.PartitionedExtractionDomain, error)
}

// DomainSnapshot is the exact current plan/root pair and the authority derived
// from that same atomic store binding.
type DomainSnapshot struct {
	Plan      candidate.DomainResultPlan
	Root      candidate.DomainResultRoot
	Authority candidate.DownstreamDomainAuthority
}

// CurrentDomainAuthority validates the store's exact canonical plan/root pair
// without opening candidate, source, observation, Git, or evidence content.
func CurrentDomainAuthority(
	ctx context.Context,
	state DownstreamDomainStore,
	repository, domain string,
) (candidate.DownstreamDomainAuthority, error) {
	snapshot, err := CurrentSnapshot(ctx, state, repository, domain)
	return snapshot.Authority, err
}

// CurrentSnapshot validates and returns the store's exact canonical plan/root
// pair without opening candidate, source, observation, Git, or evidence content.
func CurrentSnapshot(
	ctx context.Context,
	state DownstreamDomainStore,
	repository, domain string,
) (DomainSnapshot, error) {
	if state == nil {
		return DomainSnapshot{}, invalid("downstream domain store")
	}
	publication, err := state.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil {
		return DomainSnapshot{}, err
	}
	if publication == nil {
		return DomainSnapshot{}, invalid("downstream domain publication")
	}
	plan, err := candidate.DecodeDomainResultPlanControl(bytes.NewReader([]byte(publication.Plan)))
	if err != nil {
		return DomainSnapshot{}, errors.Join(err, invalid("stored downstream plan"))
	}
	root, err := candidate.DecodeDomainResultRoot(bytes.NewReader([]byte(publication.Root)), plan)
	if err != nil {
		return DomainSnapshot{}, errors.Join(err, invalid("stored downstream root"))
	}
	if plan.Repository != publication.Repository || plan.Domain != publication.Domain ||
		plan.Digest != publication.PlanDigest || root.Digest != publication.RootDigest ||
		plan.CandidateManifestDigest != publication.CandidateDigest ||
		plan.SourceGenerationDigest != publication.SourceDigest ||
		plan.ObservationGenerationDigest != publication.ObservationDigest ||
		root.Totals.Facts != publication.Facts || root.Totals.Rows != publication.Rows ||
		root.Totals.References != publication.References {
		return DomainSnapshot{}, invalid("stored downstream authority mismatch")
	}
	authority, err := candidate.NewDownstreamDomainAuthority(plan, root, publication.RunID)
	if err != nil {
		return DomainSnapshot{}, err
	}
	return DomainSnapshot{Plan: plan, Root: root, Authority: authority}, nil
}
