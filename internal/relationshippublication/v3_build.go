package relationshippublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

type BuildRequestV3 struct {
	Root           string
	Catalog        servicecatalogv3.Generation
	States         []servicecatalog.ServiceState
	ServiceSummary servicecatalog.RepositoryState
	Resolver       resolverSource
	RPC            rpcSource
	Kafka          kafkaSource
	Upstream       downstreamauthority.Authority
	Prior          *PublicationV3
}

type PreparedV3 struct {
	root       string
	repository string
	directory  string
	rootValue  RootV3
	closed     bool
}

func BuildV3(ctx context.Context, request BuildRequestV3) (*PreparedV3, error) {
	if request.Root == "" || request.Resolver == nil || request.RPC == nil || request.Kafka == nil {
		return nil, errors.New("relationship v3 publication inputs are incomplete")
	}
	upstream := request.Upstream
	upstream.Required = slices.Clone(request.Upstream.Required)
	upstream.Domains = slices.Clone(request.Upstream.Domains)
	if downstreamauthority.RequireUsable(upstream) != nil {
		return nil, fmt.Errorf("%w: relationship v3 upstream", ErrInvalid)
	}
	// Catalog performs the one complete member decode/validation pass; all
	// service-state and placement derivation below reuses that normalized view.
	catalog, err := request.Catalog.Catalog()
	if err != nil {
		return nil, fmt.Errorf("verify service catalog v3: %w", err)
	}
	catalogRoot := request.Catalog.Root
	services, err := bindCatalogServiceStatesV3(catalog, request.States, catalogRoot)
	if err != nil {
		return nil, err
	}
	placements, err := catalogPlacementIndexV3(catalog)
	if err != nil {
		return nil, err
	}
	if err := validateServiceSummaryV3(request.ServiceSummary, catalogRoot, len(services)); err != nil {
		return nil, err
	}

	resolverRoot := request.Resolver.Root()
	rpcRoot := request.RPC.Root()
	kafkaRoot := request.Kafka.Root()
	if err := validateComponentRootsV3(
		catalogRoot.Binding.Repository, upstream, resolverRoot, rpcRoot, kafkaRoot,
	); err != nil {
		return nil, err
	}
	policyDigest, err := digestValue(FrozenPolicyV3())
	if err != nil {
		return nil, err
	}
	serviceSetDigest, err := serviceStateSetDigestV3(services)
	if err != nil {
		return nil, err
	}
	catalogSource, err := servicecatalogv3.SourceGenerationDigest(catalogRoot)
	if err != nil {
		return nil, err
	}
	authority := AuthorityV3{
		Repository:        catalogRoot.Binding.Repository,
		CatalogRootDigest: catalogRoot.Digest, CatalogLogicalDigest: catalogRoot.LogicalDigest,
		CatalogSourceGeneration:     catalogSource,
		CatalogControlRevision:      request.ServiceSummary.CatalogControlRevision,
		ServiceStateSetDigest:       serviceSetDigest,
		ServiceStateSummaryDigest:   request.ServiceSummary.SummaryDigest,
		ServiceStateControlRevision: request.ServiceSummary.ControlRevision,
		ObservationGenerationDigest: rpcRoot.Authority.ObservationGenerationDigest,
		ObservationManifestDigest:   rpcRoot.Authority.ObservationManifestDigest,
		ObservationSourceDigest:     rpcRoot.Authority.ObservationSourceDigest,
		ResolverGenerationDigest:    resolverRoot.GenerationDigest,
		ResolverRootDigest:          resolverRoot.Digest,
		RPCGenerationDigest:         rpcRoot.GenerationDigest, RPCRootDigest: rpcRoot.Digest,
		KafkaGenerationDigest: kafkaRoot.GenerationDigest, KafkaRootDigest: kafkaRoot.Digest,
		Upstream: upstream, UpstreamDigest: upstream.Digest,
		PolicyDigest: policyDigest,
	}
	if err := validateAuthorityV3(authority); err != nil {
		return nil, err
	}
	authorityDigest, err := digestValue(authority)
	if err != nil {
		return nil, err
	}
	accumulator := &buildAccumulator{
		placements: placementIndex{values: placements}, repository: make(map[int][]Projection),
		services: services, seen: make(map[string]struct{}),
		serviceRefLimit: MaxServiceReferences, totalRefLimit: MaxTotalServiceReferences,
		residentLimit: MaxResidentChargeBytes,
	}
	if err := request.RPC.WalkPostings(ctx, func(posting rpccallerposting.Posting) error {
		projection, projectErr := accumulator.projectRPCV3(posting)
		if projectErr != nil {
			return projectErr
		}
		return accumulator.add(projection)
	}); err != nil {
		return nil, fmt.Errorf("walk RPC postings for v3: %w", err)
	}
	if err := request.Kafka.WalkPostings(ctx, func(posting kafkatopicposting.Posting) error {
		projection, projectErr := accumulator.projectKafkaV3(posting)
		if projectErr != nil {
			return projectErr
		}
		return accumulator.add(projection)
	}); err != nil {
		return nil, fmt.Errorf("walk Kafka postings for v3: %w", err)
	}
	return writePublicationStageV3(
		ctx, request.Root, authority, authorityDigest, request.Prior, accumulator,
	)
}

func bindCatalogServiceStatesV3(
	catalog servicecatalog.Catalog,
	states []servicecatalog.ServiceState,
	root servicecatalogv3.Root,
) (map[string]*serviceAccumulator, error) {
	if len(states) > MaxServicesV3 {
		return nil, ErrLimit
	}
	byKey := make(map[string]servicecatalog.ServiceState, len(states))
	for _, state := range states {
		if err := servicecatalogv3.ValidateServiceState(state, true); err != nil ||
			state.Repository != root.Binding.Repository || state.Removed ||
			state.Disposition != servicecatalog.DispositionAccepted {
			return nil, fmt.Errorf("%w: v3 service state", ErrInvalid)
		}
		if _, duplicate := byKey[state.ServiceKey]; duplicate {
			return nil, fmt.Errorf("%w: duplicate v3 service state", ErrInvalid)
		}
		byKey[state.ServiceKey] = state
	}
	result := make(map[string]*serviceAccumulator, len(states))
	sourceGeneration, err := servicecatalogv3.SourceGenerationDigest(root)
	if err != nil {
		return nil, err
	}
	for _, service := range catalog.Services {
		if service.Disposition != servicecatalog.DispositionAccepted {
			continue
		}
		projection, projectionErr := projectCatalogServiceV3(
			root, sourceGeneration, service, membershipsForCatalogServiceV3(catalog.Memberships, service.Key),
		)
		if projectionErr != nil {
			return nil, projectionErr
		}
		state, present := byKey[service.Key]
		if !present || servicecatalogv3.ValidateStateProjection(state, projection, false) != nil {
			return nil, fmt.Errorf("%w: v3 service desired authority", ErrInvalid)
		}
		result[state.ServiceKey] = &serviceAccumulator{state: state, refs: []ServiceReference{}}
		delete(byKey, state.ServiceKey)
	}
	if len(byKey) != 0 || len(result) != len(states) {
		return nil, fmt.Errorf("%w: v3 accepted service state coverage", ErrInvalid)
	}
	return result, nil
}

func projectCatalogServiceV3(
	root servicecatalogv3.Root,
	sourceGeneration string,
	service servicecatalog.Service,
	memberships []servicecatalog.Membership,
) (servicecatalog.ServiceProjection, error) {
	service.Successors = slices.Clone(service.Successors)
	binding := struct {
		Schema           string                      `json:"schema"`
		Repository       string                      `json:"repository"`
		SourceGeneration string                      `json:"source_generation"`
		Service          servicecatalog.Service      `json:"service"`
		Memberships      []servicecatalog.Membership `json:"memberships"`
	}{
		Schema: "phebs-service-desired-v3-shadow", Repository: root.Binding.Repository,
		SourceGeneration: sourceGeneration, Service: service, Memberships: memberships,
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return servicecatalog.ServiceProjection{}, err
	}
	raw = append(raw, '\n')
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-desired-v3-shadow\x00"))
	_, _ = hash.Write(raw)
	return servicecatalog.ServiceProjection{
		Repository: root.Binding.Repository, Service: service, Memberships: memberships,
		SourceGeneration: sourceGeneration, CatalogGeneration: root.Digest,
		GenerationDigest: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		Removed:          service.Disposition == servicecatalog.DispositionRejected,
	}, nil
}

func membershipsForCatalogServiceV3(
	memberships []servicecatalog.Membership,
	serviceKey string,
) []servicecatalog.Membership {
	start := sort.Search(len(memberships), func(index int) bool {
		return memberships[index].ServiceKey >= serviceKey
	})
	if start == len(memberships) || memberships[start].ServiceKey != serviceKey {
		return nil
	}
	end := start + sort.Search(len(memberships)-start, func(index int) bool {
		return memberships[start+index].ServiceKey > serviceKey
	})
	return slices.Clone(memberships[start:end])
}

func catalogPlacementIndexV3(
	catalog servicecatalog.Catalog,
) (map[string]servicecatalog.PlacementAuthority, error) {
	services := make(map[string]servicecatalog.Service, len(catalog.Services))
	for _, service := range catalog.Services {
		services[service.Key] = service
	}
	result := make(map[string]servicecatalog.PlacementAuthority)
	claimsByPath := make(map[string]map[string]*servicecatalog.PlacementClaim)
	for _, membership := range catalog.Memberships {
		claims := claimsByPath[membership.Path]
		if claims == nil {
			claims = make(map[string]*servicecatalog.PlacementClaim)
			claimsByPath[membership.Path] = claims
		}
		claim := claims[membership.ServiceKey]
		if claim == nil {
			service := services[membership.ServiceKey]
			claim = &servicecatalog.PlacementClaim{
				ServiceKey: service.Key, Disposition: service.Disposition,
			}
			claims[membership.ServiceKey] = claim
		}
		claim.Roles = append(claim.Roles, servicecatalog.PlacementRole{
			Role: membership.Role, Origin: membership.Origin,
		})
	}
	for _, unowned := range catalog.Unowned {
		claims := claimsByPath[unowned.Path]
		if claims == nil {
			claims = make(map[string]*servicecatalog.PlacementClaim)
			claimsByPath[unowned.Path] = claims
		}
		result[unowned.Path] = servicecatalog.PlacementAuthority{Path: unowned.Path, Unowned: true}
	}
	for path, claims := range claimsByPath {
		value := result[path]
		value.Path = path
		keys := make([]string, 0, len(claims))
		for key := range claims {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		value.Claims = make([]servicecatalog.PlacementClaim, 0, len(keys))
		for _, key := range keys {
			claim := *claims[key]
			claim.Roles = slices.Clone(claim.Roles)
			value.Claims = append(value.Claims, claim)
		}
		result[path] = value
	}
	return result, nil
}

func validateServiceSummaryV3(
	value servicecatalog.RepositoryState,
	root servicecatalogv3.Root,
	accepted int,
) error {
	if servicecatalogv3.ValidateRepositoryState(value, true) != nil ||
		value.Repository != root.Binding.Repository || value.CatalogGeneration != root.Digest ||
		value.CatalogControlRevision == 0 || value.CatalogServiceCount != root.Services ||
		accepted != root.Dispositions.Accepted {
		return fmt.Errorf("%w: v3 service summary", ErrInvalid)
	}
	return nil
}

func validateComponentRootsV3(
	repository string,
	upstream downstreamauthority.Authority,
	resolverRoot resolvernamespace.Root,
	rpcRoot rpccallerposting.Root,
	kafkaRoot kafkatopicposting.Root,
) error {
	if resolvernamespace.ValidateRoot(resolverRoot) != nil ||
		rpccallerposting.ValidateRoot(rpcRoot) != nil || kafkatopicposting.ValidateRoot(kafkaRoot) != nil {
		return fmt.Errorf("%w: v3 component root", ErrInvalid)
	}
	if resolverRoot.Schema != resolvernamespace.RootSchemaV2 ||
		rpcRoot.Schema != rpccallerposting.RootSchemaV2 || kafkaRoot.Schema != kafkatopicposting.RootSchemaV2 ||
		resolverRoot.Authority.Repository != repository || rpcRoot.Authority.Repository != repository ||
		kafkaRoot.Authority.Repository != repository ||
		resolverRoot.Authority.Upstream == nil || resolverRoot.Authority.Upstream.Digest != upstream.Digest ||
		rpcRoot.Authority.Upstream == nil || rpcRoot.Authority.Upstream.Digest != upstream.Digest ||
		kafkaRoot.Authority.Upstream == nil || kafkaRoot.Authority.Upstream.Digest != upstream.Digest ||
		rpcRoot.Authority.ResolverGenerationDigest != resolverRoot.GenerationDigest ||
		rpcRoot.Authority.ResolverRootDigest != resolverRoot.Digest ||
		rpcRoot.Authority.ObservationGenerationDigest != kafkaRoot.Authority.ObservationGenerationDigest ||
		rpcRoot.Authority.ObservationManifestDigest != kafkaRoot.Authority.ObservationManifestDigest ||
		rpcRoot.Authority.ObservationSourceDigest != kafkaRoot.Authority.ObservationSourceDigest ||
		rpcRoot.Authority.ObservationV2 == nil || kafkaRoot.Authority.ObservationV2 == nil ||
		*rpcRoot.Authority.ObservationV2 != upstream.Observation ||
		*kafkaRoot.Authority.ObservationV2 != upstream.Observation {
		return fmt.Errorf("%w: v3 component authorities disagree", ErrInvalid)
	}
	return nil
}

func serviceStateSetDigestV3(services map[string]*serviceAccumulator) (string, error) {
	values := make([]serviceSetIdentityV3, 0, len(services))
	for _, key := range sortedServiceKeys(services) {
		state := services[key].state
		values = append(values, serviceSetIdentityV3{
			ServiceKey: key, Incarnation: state.Incarnation,
			DesiredGeneration: state.DesiredGeneration,
		})
	}
	return digestServiceSetV3(values)
}

func (accumulator *buildAccumulator) projectRPCV3(
	posting rpccallerposting.Posting,
) (Projection, error) {
	source, err := accumulator.placements.lookup(posting.Path)
	if err != nil {
		return Projection{}, err
	}
	value := Projection{
		Schema: ProjectionSchema, Kind: "rpc", PostingDigest: posting.Digest,
		Class: posting.Class, Plane: posting.Protocol, LookupKey: posting.LookupOperation,
		Source: source,
	}
	if posting.DeclarationPath != "" {
		target, lookupErr := accumulator.placements.lookup(posting.DeclarationPath)
		if lookupErr != nil {
			return Projection{}, lookupErr
		}
		value.Target = &target
	}
	return finalizeProjectionV3(value)
}

func (accumulator *buildAccumulator) projectKafkaV3(
	posting kafkatopicposting.Posting,
) (Projection, error) {
	source, err := accumulator.placements.lookup(posting.Path)
	if err != nil {
		return Projection{}, err
	}
	value := Projection{
		Schema: ProjectionSchema, Kind: "kafka", PostingDigest: posting.Digest,
		Class: posting.Class, Plane: posting.Plane, Source: source,
	}
	if posting.Class == "literal" {
		value.LookupKey = posting.TopicSpelling
	}
	return finalizeProjectionV3(value)
}

func finalizeProjectionV3(value Projection) (Projection, error) {
	value.Digest = ""
	digest, err := digestValue(value)
	if err != nil {
		return Projection{}, err
	}
	value.Digest = digest
	if err := validateProjection(value); err != nil {
		return Projection{}, err
	}
	return value, nil
}
