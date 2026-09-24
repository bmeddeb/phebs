package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	t422ArchiveTailHeader = "X-Phebs-T422-Archive-Tail"
	t422ArchiveTailValue  = "settled-v1"
)

type t422ArchiveTailKey struct{}

func t422ArchiveTailReadinessLimits() readaccounting.Counts {
	return readaccounting.Counts{
		ControlFileReads:  7,
		StoreReadAttempts: 4 + 9 + 2 + 4 + 4*store.MaxGenerationScheduleReadAttempts,
	}
}

func t422ArchiveTailRoute(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	values := request.Header.Values(t422ArchiveTailHeader)
	return len(values) == 1 && values[0] == t422ArchiveTailValue &&
		request.Method == http.MethodGet && request.URL.Path == t421ExactTailReadinessPath &&
		request.URL.Path == request.URL.EscapedPath() && request.URL.RawQuery == "" &&
		!request.URL.ForceQuery && request.URL.Fragment == ""
}

func (state *t421ExactReadAccountingState) archiveTailRequest(request *http.Request) bool {
	if state == nil || state.semantic == nil || !dispatchadmission.ProductionWorkSelected() ||
		!t422ArchiveTailRoute(request) || state.semantic.request.ServerEpoch != 5 {
		return false
	}
	admitted, present := request.Context().Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	return present && admitted.Phase == 12 && state.semantic.matches(admitted)
}

type t422ArchiveTailScheduleSource interface {
	GetGenerationScheduleForArchiveTail(context.Context, string, string) (*store.GenerationSchedule, error)
}

func t422ArchiveTailSchedule(ctx context.Context, state t422ArchiveTailScheduleSource, repository string) (*store.GenerationSchedule, bool, error) {
	schedule, err := state.GetGenerationScheduleForArchiveTail(ctx, repository, relationshippublication.ScheduleStageV3)
	if errors.Is(err, store.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil || schedule == nil {
		return nil, false, errors.Join(err, errors.New("archive relationship schedule unavailable"))
	}
	switch schedule.Status {
	case store.GenerationScheduleActive:
		return schedule, false, nil
	case store.GenerationScheduleSettled:
		if schedule.Failed == 0 {
			return schedule, true, nil
		}
	}
	return nil, false, errors.New("archive relationship schedule is terminal or superseded")
}

func t422ArchiveTailSameSchedule(before, after *store.GenerationSchedule) bool {
	return reflect.DeepEqual(before, after)
}

func t422ArchiveTailDomainCurrent(
	bound candidate.DownstreamDomainAuthority,
	current store.PartitionedExtractionDomainReference,
) bool {
	return bound.Domain == current.Domain &&
		bound.PlanDigest == current.PlanDigest && bound.RootDigest == current.RootDigest &&
		bound.RunID == current.RunID &&
		bound.CandidateManifestDigest == current.CandidateDigest &&
		bound.SourceGenerationDigest == current.SourceDigest &&
		bound.ObservationGenerationDigest == current.ObservationDigest
}

func t422ArchiveTailReady(
	before, after *store.GenerationSchedule,
	current, afterIdle bool,
) bool {
	return current && afterIdle && t422ArchiveTailSameSchedule(before, after)
}

// The namespace's manifest field binds the catalog authority digest.
func t422ArchiveTailResolverMatches(publication store.ResolverCatalogPublication, resolver resolvernamespace.Root) bool {
	return publication.GenerationDigest == resolver.Authority.ResolverGenerationDigest &&
		publication.AuthorityDigest == resolver.Authority.ResolverManifestDigest
}

type t422ArchiveTailCatalogStateSource interface {
	GetServiceCatalogV3CandidatePointer(context.Context, string) (store.ServiceCatalogV3Pointer, error)
	GetServiceStateV3SummaryPoint(context.Context, string) (servicecatalog.RepositoryState, error)
}

// These dark point getters have no intrinsic exact-read charge. The two
// snapshots cost four store attempts, even when values remain unchanged.
func t422ArchiveTailCatalogStateCurrent(
	ctx context.Context, source t422ArchiveTailCatalogStateSource,
	repository string, authority relationshippublication.AuthorityV3,
) (bool, error) {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return false, err
	}
	pointer, err := source.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return false, err
	}
	summary, err := source.GetServiceStateV3SummaryPoint(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if servicecatalogv3.ValidateRepositoryState(summary, true) != nil ||
		pointer.Repository != repository || summary.Repository != repository ||
		pointer.RootDigest != summary.CatalogGeneration ||
		pointer.ControlRevision != summary.CatalogControlRevision ||
		authority.CatalogRootDigest != pointer.RootDigest ||
		authority.CatalogControlRevision != pointer.ControlRevision ||
		authority.ServiceStateSummaryDigest != summary.SummaryDigest ||
		authority.ServiceStateControlRevision != summary.ControlRevision {
		return false, nil
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return false, err
	}
	confirmedPointer, err := source.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		return false, err
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return false, err
	}
	confirmedSummary, err := source.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		return false, err
	}
	return confirmedPointer == pointer && confirmedSummary == summary, nil
}

// t422ArchiveTailCurrent checks only current scalar inputs used by ReconcileV3.
// It does not transfer the nine potentially large stored plans and roots.
func (reader *t421FinalAuthorityReader) t422ArchiveTailCurrent(
	ctx context.Context, relationship relationshippublication.RootV3, resolver resolvernamespace.Root,
) (bool, error) {
	authority := relationship.Authority
	upstream := authority.Upstream
	observation, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, filepath.Join(reader.dataDir, "observations"), reader.repository,
	)
	if err != nil {
		return false, err
	}
	if upstream.Observation != observation || len(upstream.Required) != len(reader.identities) ||
		len(upstream.Domains) != len(reader.identities) || len(reader.identities) != 9 {
		return false, nil
	}
	for index, identity := range reader.identities {
		domain := upstream.Domains[index]
		if upstream.Required[index].Domain != identity.Domain ||
			upstream.Required[index].Version != identity.Version ||
			domain.Domain != identity.Domain || domain.Version != identity.Version {
			return false, nil
		}
		current, readErr := reader.store.GetPartitionedExtractionDomainReference(
			ctx, reader.repository, identity.Domain,
		)
		if errors.Is(readErr, store.ErrNotFound) {
			return false, nil
		}
		if readErr != nil {
			return false, readErr
		}
		if !t422ArchiveTailDomainCurrent(domain, current) {
			return false, nil
		}
	}
	publication, err := reader.store.GetResolverCatalogPublication(ctx, reader.repository)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil || publication == nil {
		return false, errors.Join(err, errors.New("archive resolver publication unavailable"))
	}
	current, err := reader.store.ResolverCatalogPublicationCurrent(ctx, *publication)
	if err != nil {
		return false, err
	}
	if !current || !t422ArchiveTailResolverMatches(*publication, resolver) {
		return false, nil
	}
	return t422ArchiveTailCatalogStateCurrent(ctx, reader.store, reader.repository, authority)
}
