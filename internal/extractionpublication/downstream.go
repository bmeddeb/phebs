package extractionpublication

import (
	"bytes"
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

// CurrentDomainAuthority validates the store's exact canonical plan/root pair
// without opening candidate, source, observation, Git, or evidence content.
func CurrentDomainAuthority(
	ctx context.Context,
	state store.PartitionedEvidenceStore,
	repository, domain string,
) (candidate.DownstreamDomainAuthority, error) {
	if state == nil {
		return candidate.DownstreamDomainAuthority{}, invalid("downstream domain store")
	}
	publication, err := state.GetPartitionedExtractionDomain(ctx, repository, domain)
	if err != nil || publication == nil {
		return candidate.DownstreamDomainAuthority{}, err
	}
	plan, err := candidate.DecodeDomainResultPlanControl(bytes.NewReader([]byte(publication.Plan)))
	if err != nil {
		return candidate.DownstreamDomainAuthority{}, errors.Join(err, invalid("stored downstream plan"))
	}
	root, err := candidate.DecodeDomainResultRoot(bytes.NewReader([]byte(publication.Root)), plan)
	if err != nil {
		return candidate.DownstreamDomainAuthority{}, errors.Join(err, invalid("stored downstream root"))
	}
	if plan.Repository != publication.Repository || plan.Domain != publication.Domain ||
		plan.Digest != publication.PlanDigest || root.Digest != publication.RootDigest ||
		plan.CandidateManifestDigest != publication.CandidateDigest ||
		plan.SourceGenerationDigest != publication.SourceDigest ||
		plan.ObservationGenerationDigest != publication.ObservationDigest ||
		root.Totals.Facts != publication.Facts || root.Totals.Rows != publication.Rows ||
		root.Totals.References != publication.References {
		return candidate.DownstreamDomainAuthority{}, invalid("stored downstream authority mismatch")
	}
	return candidate.NewDownstreamDomainAuthority(plan, root, publication.RunID)
}
