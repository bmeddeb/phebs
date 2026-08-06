package relationshippublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

type BuildRequest struct {
	Root     string
	Catalog  servicecatalog.Publication
	States   []servicecatalog.ServiceState
	Resolver *resolvernamespace.Publication
	RPC      *rpccallerposting.Publication
	Kafka    *kafkatopicposting.Publication
}

type resolverSource interface {
	Root() resolvernamespace.Root
}

type rpcSource interface {
	Root() rpccallerposting.Root
	WalkPostings(context.Context, func(rpccallerposting.Posting) error) error
}

type kafkaSource interface {
	Root() kafkatopicposting.Root
	WalkPostings(context.Context, func(kafkatopicposting.Posting) error) error
}

type Prepared struct {
	root       string
	repository string
	directory  string
	rootValue  Root
	closed     bool
}

type placementIndex struct {
	values map[string]servicecatalog.PlacementAuthority
}

type serviceAccumulator struct {
	state  servicecatalog.ServiceState
	refs   []ServiceReference
	charge int64
	failed bool
	reason string
}

type buildAccumulator struct {
	placements       placementIndex
	repository       map[int][]Projection
	services         map[string]*serviceAccumulator
	seen             map[string]struct{}
	projectionCount  int
	totalServiceRefs int
	residentCharge   int64
	serviceRefLimit  int
	totalRefLimit    int
	residentLimit    int64
}

func Build(ctx context.Context, request BuildRequest) (*Prepared, error) {
	return buildSources(
		ctx, request.Root, request.Catalog, request.States,
		request.Resolver, request.RPC, request.Kafka,
	)
}

func buildSources(
	ctx context.Context,
	root string,
	catalog servicecatalog.Publication,
	states []servicecatalog.ServiceState,
	resolver resolverSource,
	rpc rpcSource,
	kafka kafkaSource,
) (*Prepared, error) {
	if root == "" || resolver == nil || rpc == nil || kafka == nil {
		return nil, errors.New("relationship publication inputs are incomplete")
	}
	verified, err := servicecatalog.VerifyPublication(catalog, true)
	if err != nil {
		return nil, fmt.Errorf("verify service catalog: %w", err)
	}
	serviceProjections, err := verified.ProjectServices()
	if err != nil {
		return nil, err
	}
	placements, err := verified.ProjectPlacements()
	if err != nil {
		return nil, err
	}
	resolverRoot := resolver.Root()
	rpcRoot := rpc.Root()
	kafkaRoot := kafka.Root()
	if err := resolvernamespace.ValidateRoot(resolverRoot); err != nil {
		return nil, fmt.Errorf("verify resolver root: %w", err)
	}
	if err := rpccallerposting.ValidateRoot(rpcRoot); err != nil {
		return nil, fmt.Errorf("verify RPC root: %w", err)
	}
	if err := kafkatopicposting.ValidateRoot(kafkaRoot); err != nil {
		return nil, fmt.Errorf("verify Kafka root: %w", err)
	}
	repository := catalog.Repository
	if resolverRoot.Authority.Repository != repository || rpcRoot.Authority.Repository != repository ||
		kafkaRoot.Authority.Repository != repository ||
		rpcRoot.Authority.ResolverGenerationDigest != resolverRoot.GenerationDigest ||
		rpcRoot.Authority.ResolverRootDigest != resolverRoot.Digest ||
		rpcRoot.Authority.ObservationGenerationDigest != kafkaRoot.Authority.ObservationGenerationDigest ||
		rpcRoot.Authority.ObservationManifestDigest != kafkaRoot.Authority.ObservationManifestDigest ||
		rpcRoot.Authority.ObservationSourceDigest != kafkaRoot.Authority.ObservationSourceDigest {
		return nil, fmt.Errorf("%w: upstream authorities disagree", ErrInvalid)
	}
	catalogSource, err := servicecatalog.SourceGenerationDigest(catalog)
	if err != nil {
		return nil, err
	}
	policyDigest, err := digestValue(FrozenPolicy())
	if err != nil {
		return nil, err
	}
	authority := Authority{
		Repository: repository, CatalogGenerationDigest: catalog.GenerationDigest,
		CatalogDigest: catalog.CatalogDigest, CatalogSourceGeneration: catalogSource,
		ObservationGenerationDigest: rpcRoot.Authority.ObservationGenerationDigest,
		ObservationManifestDigest:   rpcRoot.Authority.ObservationManifestDigest,
		ObservationSourceDigest:     rpcRoot.Authority.ObservationSourceDigest,
		ResolverGenerationDigest:    resolverRoot.GenerationDigest, ResolverRootDigest: resolverRoot.Digest,
		RPCGenerationDigest: rpcRoot.GenerationDigest, RPCRootDigest: rpcRoot.Digest,
		KafkaGenerationDigest: kafkaRoot.GenerationDigest, KafkaRootDigest: kafkaRoot.Digest,
		PolicyDigest: policyDigest,
	}
	services, err := bindServiceStates(serviceProjections, states, repository)
	if err != nil {
		return nil, err
	}
	authority.ServiceStateSetDigest, err = serviceStateSetDigest(services)
	if err != nil {
		return nil, err
	}
	if err := validateAuthority(authority); err != nil {
		return nil, err
	}
	authorityDigest, err := digestValue(authority)
	if err != nil {
		return nil, err
	}
	indexValues := make(map[string]servicecatalog.PlacementAuthority, len(placements))
	for _, placement := range placements {
		indexValues[placement.Path] = placement
	}
	accumulator := &buildAccumulator{
		placements: placementIndex{values: indexValues}, repository: make(map[int][]Projection),
		services: services, seen: make(map[string]struct{}),
		serviceRefLimit: MaxServiceReferences, totalRefLimit: MaxTotalServiceReferences,
		residentLimit: MaxResidentChargeBytes,
	}
	if err := rpc.WalkPostings(ctx, func(posting rpccallerposting.Posting) error {
		projection, err := accumulator.projectRPC(posting)
		if err != nil {
			return err
		}
		return accumulator.add(projection)
	}); err != nil {
		return nil, fmt.Errorf("walk RPC postings: %w", err)
	}
	if err := kafka.WalkPostings(ctx, func(posting kafkatopicposting.Posting) error {
		projection, err := accumulator.projectKafka(posting)
		if err != nil {
			return err
		}
		return accumulator.add(projection)
	}); err != nil {
		return nil, fmt.Errorf("walk Kafka postings: %w", err)
	}
	return writeStage(ctx, root, authority, authorityDigest, accumulator)
}

func serviceStateSetDigest(services map[string]*serviceAccumulator) (string, error) {
	values := make([]serviceSetIdentity, 0, len(services))
	for _, key := range sortedServiceKeys(services) {
		state := services[key].state
		values = append(values, serviceSetIdentity{
			ServiceKey: key, Incarnation: state.Incarnation,
			DesiredGeneration: state.DesiredGeneration,
		})
	}
	return digestServiceSet(values)
}

func publicationServiceStateSetDigest(
	publication servicecatalog.Publication,
	states []servicecatalog.ServiceState,
) (string, error) {
	verified, err := servicecatalog.VerifyPublication(publication, true)
	if err != nil {
		return "", err
	}
	projections, err := verified.ProjectServices()
	if err != nil {
		return "", err
	}
	services, err := bindServiceStates(projections, states, publication.Repository)
	if err != nil {
		return "", err
	}
	return serviceStateSetDigest(services)
}

func bindServiceStates(
	projections []servicecatalog.ServiceProjection,
	states []servicecatalog.ServiceState,
	repository string,
) (map[string]*serviceAccumulator, error) {
	accepted := make(map[string]servicecatalog.ServiceProjection)
	for _, projection := range projections {
		if projection.Service.Disposition == servicecatalog.DispositionAccepted && !projection.Removed {
			accepted[projection.Service.Key] = projection
		}
	}
	if len(accepted) > MaxServices || len(states) != len(accepted) {
		return nil, fmt.Errorf("%w: accepted service state coverage", ErrInvalid)
	}
	result := make(map[string]*serviceAccumulator, len(states))
	for _, state := range states {
		if err := servicecatalog.ValidateServiceState(state, true); err != nil ||
			state.Repository != repository || state.Removed ||
			state.Disposition != servicecatalog.DispositionAccepted {
			return nil, fmt.Errorf("%w: service state", ErrInvalid)
		}
		projection, present := accepted[state.ServiceKey]
		if !present {
			return nil, fmt.Errorf("%w: service state is not accepted", ErrInvalid)
		}
		desired, err := servicecatalog.ServiceDesiredGeneration(projection, state.Incarnation)
		if err != nil || desired != state.DesiredGeneration ||
			state.DesiredCatalogGeneration != projection.CatalogGeneration ||
			state.DesiredSourceGeneration != projection.SourceGeneration {
			return nil, fmt.Errorf("%w: service desired authority", ErrInvalid)
		}
		if _, duplicate := result[state.ServiceKey]; duplicate {
			return nil, fmt.Errorf("%w: duplicate service state", ErrInvalid)
		}
		result[state.ServiceKey] = &serviceAccumulator{state: state, refs: []ServiceReference{}}
	}
	return result, nil
}

func (accumulator *buildAccumulator) projectRPC(posting rpccallerposting.Posting) (Projection, error) {
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
		target, err := accumulator.placements.lookup(posting.DeclarationPath)
		if err != nil {
			return Projection{}, err
		}
		value.Target = &target
	}
	return finalizeProjection(value)
}

func (accumulator *buildAccumulator) projectKafka(posting kafkatopicposting.Posting) (Projection, error) {
	source, err := accumulator.placements.lookup(posting.Path)
	if err != nil {
		return Projection{}, err
	}
	value := Projection{
		Schema: ProjectionSchema, Kind: "kafka", PostingDigest: posting.Digest,
		Class: posting.Class, Plane: posting.Plane,
		Source: source,
	}
	if posting.Class == "literal" {
		value.LookupKey = posting.TopicSpelling
	}
	return finalizeProjection(value)
}

func finalizeProjection(value Projection) (Projection, error) {
	value.Digest = ""
	digest, err := digestValue(value)
	if err != nil {
		return Projection{}, err
	}
	value.Digest = digest
	if err := validateProjection(value); err != nil {
		return Projection{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxProjectionBytes {
		return Projection{}, ErrLimit
	}
	return value, nil
}

func (index placementIndex) lookup(path string) (Placement, error) {
	if repopath.Validate(path) != nil {
		return Placement{}, fmt.Errorf("%w: projection path", ErrInvalid)
	}
	claims := make(map[string]ServiceClaim)
	unowned := false
	candidate := path
	for {
		if authority, present := index.values[candidate]; present {
			if candidate == path && authority.Unowned {
				unowned = true
			}
			for _, claim := range authority.Claims {
				roles := make([]RoleClaim, len(claim.Roles))
				for roleIndex, role := range claim.Roles {
					roles[roleIndex] = RoleClaim{Role: role.Role, Origin: role.Origin}
				}
				claims[claim.ServiceKey] = ServiceClaim{
					ServiceKey: claim.ServiceKey, Disposition: claim.Disposition, Roles: roles,
				}
			}
		}
		separator := strings.LastIndexByte(candidate, '/')
		if separator < 0 {
			break
		}
		candidate = candidate[:separator]
	}
	if len(claims) == 0 && !unowned {
		return Placement{}, fmt.Errorf("%w: path is outside catalog complement", ErrInvalid)
	}
	if len(claims) > MaxClaimsPerPlacement {
		return Placement{}, ErrLimit
	}
	keys := make([]string, 0, len(claims))
	for key := range claims {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	value := Placement{Path: path, Unowned: unowned, Claims: make([]ServiceClaim, 0, len(keys))}
	for _, key := range keys {
		value.Claims = append(value.Claims, claims[key])
	}
	if err := validatePlacement(value); err != nil {
		return Placement{}, err
	}
	return value, nil
}

func (accumulator *buildAccumulator) add(projection Projection) error {
	if _, duplicate := accumulator.seen[projection.Digest]; duplicate {
		return nil
	}
	if accumulator.projectionCount >= MaxProjectionRecords {
		return ErrLimit
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	const projectionOverhead = 512
	if int64(len(raw)+projectionOverhead) > accumulator.residentLimit-accumulator.residentCharge {
		return ErrLimit
	}
	accumulator.seen[projection.Digest] = struct{}{}
	accumulator.projectionCount++
	accumulator.residentCharge += int64(len(raw) + projectionOverhead)
	bucket := projectionBucket(projection.Digest)
	accumulator.repository[bucket] = append(accumulator.repository[bucket], cloneProjection(projection))
	participation := make(map[string][]string)
	for _, claim := range projection.Source.Claims {
		if claim.Disposition == servicecatalog.DispositionAccepted {
			participation[claim.ServiceKey] = append(participation[claim.ServiceKey], "source")
		}
	}
	if projection.Target != nil {
		for _, claim := range projection.Target.Claims {
			if claim.Disposition == servicecatalog.DispositionAccepted {
				participation[claim.ServiceKey] = append(participation[claim.ServiceKey], "target")
			}
		}
	}
	serviceKeys := make([]string, 0, len(participation))
	for key := range participation {
		serviceKeys = append(serviceKeys, key)
	}
	slices.Sort(serviceKeys)
	for _, key := range serviceKeys {
		roles := participation[key]
		slices.Sort(roles)
		roles = slices.Compact(roles)
		reference := ServiceReference{
			Schema: ServiceReferenceSchema, ProjectionDigest: projection.Digest,
			PostingDigest: projection.PostingDigest, Kind: projection.Kind,
			Plane: projection.Plane, LookupKey: projection.LookupKey,
			Participation: roles,
		}
		digest, err := digestValue(reference)
		if err != nil {
			return err
		}
		reference.Digest = digest
		if err := validateServiceReference(reference); err != nil {
			return err
		}
		if err := accumulator.addServiceReference(key, reference); err != nil {
			return err
		}
	}
	return nil
}

func (accumulator *buildAccumulator) addServiceReference(key string, reference ServiceReference) error {
	service := accumulator.services[key]
	if service == nil {
		return fmt.Errorf("%w: accepted placement has no state", ErrInvalid)
	}
	if service.failed {
		return nil
	}
	if len(service.refs) >= accumulator.serviceRefLimit ||
		accumulator.totalServiceRefs >= accumulator.totalRefLimit {
		accumulator.totalServiceRefs -= len(service.refs)
		accumulator.residentCharge -= service.charge
		service.refs = nil
		service.charge = 0
		service.failed = true
		service.reason = "reference_limit"
		return nil
	}
	raw, err := json.Marshal(reference)
	if err != nil {
		return err
	}
	const referenceOverhead = 256
	charge := int64(len(raw) + referenceOverhead)
	if charge > accumulator.residentLimit-accumulator.residentCharge {
		accumulator.totalServiceRefs -= len(service.refs)
		accumulator.residentCharge -= service.charge
		service.refs = nil
		service.charge = 0
		service.failed = true
		service.reason = "resident_limit"
		return nil
	}
	service.refs = append(service.refs, reference)
	service.charge += charge
	accumulator.totalServiceRefs++
	accumulator.residentCharge += charge
	return nil
}

func projectionBucket(digest string) int {
	raw, _ := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return int(raw[0])
}

func writeStage(
	ctx context.Context,
	root string,
	authority Authority,
	authorityDigest string,
	accumulator *buildAccumulator,
) (*Prepared, error) {
	// Implemented in publication.go so staging and complete validation share
	// the same closed file-name and byte-accounting rules.
	return writePublicationStage(ctx, root, authority, authorityDigest, accumulator)
}

func sortedServiceKeys(values map[string]*serviceAccumulator) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func serviceMemberName(serviceKey string) string {
	sum := sha256.Sum256([]byte(serviceKey))
	return "service-" + hex.EncodeToString(sum[:]) + ".json"
}

func repositoryMemberName(bucket int) string { return fmt.Sprintf("repository-%03d.json", bucket) }

func stageDirectory(root, repository string) (string, error) {
	if err := ensureRoot(root); err != nil {
		return "", err
	}
	base := repositoryRoot(root, repository)
	if err := os.Mkdir(base, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err := validateDirectory(base); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, ".stage-")
}

func memberPath(directory, name string) string { return filepath.Join(directory, name) }
