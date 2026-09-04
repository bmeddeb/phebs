package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/t421extractionprojection"
	"github.com/bmeddeb/phebs/internal/t421relationshipprojection"
	"github.com/bmeddeb/phebs/internal/t421sourceprojection"
)

const (
	t421FinalAuthoritySchema  = "t421-final-authority-source-free-v1"
	t421FinalProjectionSchema = "t421-final-state-projection-source-free-v1"
)

// Tests may install a process-local barrier before starting the real server.
// Production binaries leave it nil.
var t421FinalAuthorityBeforeConfirm func(context.Context) error

type t421FinalAuthorityState struct {
	PhysicalCommit                  string               `json:"physical_commit"`
	PhysicalTree                    string               `json:"physical_tree"`
	SourceGenerationSHA256          string               `json:"source_generation_sha256"`
	SearchGenerationSHA256          string               `json:"search_generation_sha256"`
	ObservationGenerationSHA256     string               `json:"observation_generation_sha256"`
	CandidateGenerationSHA256       string               `json:"candidate_generation_sha256"`
	CatalogRootSHA256               string               `json:"catalog_root_sha256"`
	CatalogActivationPlanSHA256     string               `json:"catalog_activation_plan_sha256"`
	CatalogActivationScheduleSHA256 string               `json:"catalog_activation_schedule_sha256"`
	CatalogActivationUnitSHA256     string               `json:"catalog_activation_unit_sha256"`
	ResolverCatalogGenerationSHA256 string               `json:"resolver_catalog_generation_sha256"`
	ResolverCatalogRootSHA256       string               `json:"resolver_catalog_root_sha256"`
	CallerGenerationSHA256          string               `json:"caller_generation_sha256"`
	CallerRootSHA256                string               `json:"caller_root_sha256"`
	RelationshipGenerationSHA256    string               `json:"relationship_generation_sha256"`
	RelationshipRootSHA256          string               `json:"relationship_root_sha256"`
	RelationshipProvenanceSHA256    string               `json:"relationship_provenance_sha256"`
	SearchInventory                 t421FinalSetIdentity `json:"search_inventory"`
	ObservationInputInventory       t421FinalSetIdentity `json:"observation_input_inventory"`
	ExtractionRootsSHA256           string               `json:"extraction_roots_sha256"`
	Current                         bool                 `json:"current"`
}

type t421FinalStateProjection struct {
	Schema                    string                                     `json:"schema"`
	CatalogLogicalSHA256      string                                     `json:"catalog_logical_sha256"`
	SemanticSHA256            string                                     `json:"semantic_sha256"`
	CatalogSource             t421FinalCatalogSource                     `json:"catalog_source"`
	Catalog                   t421FinalSetIdentity                       `json:"catalog"`
	MembershipSet             t421FinalSetIdentity                       `json:"membership_set"`
	Placements                t421FinalSetIdentity                       `json:"placements"`
	UnownedPrefixes           t421FinalSetIdentity                       `json:"unowned_prefixes"`
	ServiceQueries            t421FinalSetIdentity                       `json:"service_queries"`
	SearchInventory           t421FinalSetIdentity                       `json:"search_inventory"`
	ObservationInputInventory t421FinalSetIdentity                       `json:"observation_input_inventory"`
	ExtractionRoots           []t421extractionprojection.PhaseProjection `json:"extraction_roots"`
	RelationshipResults       []t421relationshipprojection.FamilySummary `json:"relationship_results"`
	ProductRelationship       t421relationshipprojection.ProductSummary  `json:"product_relationship"`
}

type t421FinalAuthorityResponse struct {
	Schema          string                                `json:"schema"`
	Authority       t421FinalAuthorityState               `json:"authority"`
	Projection      t421FinalStateProjection              `json:"projection"`
	ExtractionRoots []t421extractionprojection.RootResult `json:"extraction_roots"`
}

type t421FinalAuthorityReader struct {
	repository string
	dataDir    string
	indexDir   string
	store      *store.Surreal
	pins       *focusedindex.SearchGenerationPins
	catalog    *t421FinalCatalogCache
	source     *t421FinalSourceCache
	policies   []candidate.Policy
	identities []candidate.PolicyIdentity
	policy     string

	candidateState func(context.Context) (candidate.State, error)
	openDomain     func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error)
	relationship   *relationshippublication.CacheV3
	caller         *callerexecute.PublicationReader
	visible        func(context.Context) func(store.Repo) bool
}

func newT421FinalAuthorityReader(
	repository, dataDir, indexDir string,
	state *store.Surreal,
	pins *focusedindex.SearchGenerationPins,
	policies []candidate.Policy,
	candidateState func(context.Context) (candidate.State, error),
	openDomain func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error),
	relationship *relationshippublication.CacheV3,
	caller *callerexecute.PublicationReader,
	visible func(context.Context) func(store.Repo) bool,
) (*t421FinalAuthorityReader, error) {
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		return nil, err
	}
	policy, err := candidate.PolicyDigest(identities)
	if err != nil {
		return nil, err
	}
	if repository == "" || !filepath.IsAbs(dataDir) || !filepath.IsAbs(indexDir) ||
		state == nil || pins == nil || len(policies) == 0 || candidateState == nil ||
		openDomain == nil || relationship == nil || caller == nil {
		return nil, errors.New("T42.1 final authority reader is incomplete")
	}
	return &t421FinalAuthorityReader{
		repository: repository, dataDir: dataDir, indexDir: indexDir,
		store: state, pins: pins, catalog: &t421FinalCatalogCache{},
		source: &t421FinalSourceCache{}, policies: slices.Clone(policies),
		identities: identities, policy: policy, candidateState: candidateState,
		openDomain: openDomain, relationship: relationship, caller: caller,
		visible: visible,
	}, nil
}

func t421FinalAuthorityRepository(
	selections map[string]config.ServiceCatalog,
) (string, error) {
	if len(selections) != 1 {
		return "", errors.New("T42.1 exact mode requires one service catalog")
	}
	for repository, selection := range selections {
		if selection.RuntimeVersion() != config.ServiceCatalogRuntimeV3 {
			return "", errors.New("T42.1 exact mode requires catalog runtime v3")
		}
		return repository, nil
	}
	return "", errors.New("T42.1 exact mode has no service catalog")
}

func t421FinalAuthorityReadLimits() readaccounting.Counts {
	limits, ok := t421FinalAuthorityMaximumReadLimits()
	if !ok {
		panic("T42.1 final authority read limits overflow")
	}
	return limits
}

func (reader *t421FinalAuthorityReader) Read(
	ctx context.Context,
) ([]byte, func() error, error) {
	if reader == nil || ctx == nil {
		return nil, nil, errors.New("T42.1 final authority reader is nil")
	}
	repository, err := reader.authorizedRepository(ctx)
	if err != nil {
		return nil, nil, err
	}
	selector, err := reader.store.GetServiceRuntimeSelector(ctx, reader.repository)
	if err != nil {
		return nil, nil, err
	}
	stateSummary, err := reader.selectedState(ctx, selector)
	if err != nil {
		return nil, nil, err
	}
	activation, err := reader.store.ReadServiceStateV3ActivationAuthority(ctx, selector)
	if err != nil {
		return nil, nil, err
	}
	catalogRoot, err := reader.store.ReadServiceCatalogV3Root(
		ctx, reader.repository, selector.CatalogRootDigest,
	)
	if err != nil || !matchesT421FinalCatalogSelection(selector, catalogRoot) {
		return nil, nil, errors.Join(err, errors.New("selected catalog root changed"))
	}
	catalog, catalogPending, err := reader.catalog.prepare(
		ctx, reader.store, selector, catalogRoot,
	)
	if err != nil {
		return nil, nil, err
	}

	searchLease, err := reader.pins.Acquire(reader.repository, selector.SearchGenerationDigest)
	if err != nil {
		return nil, nil, err
	}
	defer searchLease.Release()
	search, err := focusedindex.ReadSearchGenerationControls(
		ctx, reader.indexDir, reader.repository, selector.SearchGenerationDigest,
	)
	if err != nil {
		return nil, nil, err
	}
	source, sourcePending, err := reader.source.prepare(
		ctx, reader.repository, search, reader.policies, reader.policy,
	)
	if err != nil || source.Commit != repository.IndexedCommitHash ||
		catalogRoot.Binding.Source.Commit != source.Commit {
		return nil, nil, errors.Join(err, errors.New("physical source authority changed"))
	}

	observation, err := observationpublication.CurrentInventoryAuthorityReferenceV2(
		ctx, filepath.Join(reader.dataDir, "observations"), reader.repository,
	)
	if err != nil || observation.SourceGenerationDigest != search.Source.Digest {
		return nil, nil, errors.Join(err, errors.New("observation source authority changed"))
	}
	candidateState, err := reader.candidateState(ctx)
	if err != nil || candidateState.Repository != reader.repository ||
		candidateState.Commit != source.Commit || candidateState.PolicyDigest != reader.policy {
		return nil, nil, errors.Join(err, errors.New("candidate authority changed"))
	}

	extractionProjections, extractionRoots, extractionAuthorities, planDigests, err :=
		reader.readExtraction(ctx, source.Projection, observation, candidateState)
	if err != nil {
		return nil, nil, err
	}
	extractionGeneration, err := extractionpublication.GenerationDigest(
		reader.repository, planDigests,
	)
	if err != nil {
		return nil, nil, err
	}
	for index := range extractionRoots {
		extractionRoots[index].GenerationSHA256 = extractionGeneration
	}

	relationship, err := reader.relationship.ReadCurrentSemanticSnapshot(
		ctx, filepath.Join(reader.dataDir, "relationships"), reader.repository,
	)
	if err != nil || !reader.matchesRelationship(
		selector, stateSummary, catalogRoot, observation, extractionAuthorities,
		candidateState, relationship.Root,
	) {
		return nil, nil, errors.Join(err, errors.New("relationship authority changed"))
	}
	semantic, err := t421relationshipprojection.Derive(ctx, relationship.Projections)
	if err != nil {
		return nil, nil, err
	}
	resolver, err := reader.openRelationshipComponents(ctx, relationship.Root, relationship.Projections)
	if err != nil {
		return nil, nil, err
	}

	caller, err := reader.caller.Open(ctx, reader.repository)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = caller.Release() }()
	if !reader.matchesCaller(caller, source.Commit, candidateState, relationship.Root, resolver) {
		return nil, nil, errors.New("caller authority changed")
	}

	rootsSHA256, err := t421FinalReceiptSHA256(extractionRoots)
	if err != nil {
		return nil, nil, err
	}
	searchInventory := t421FinalSetIdentity(source.Projection.TreeInventory)
	observationInventory := t421FinalSetIdentity(source.Projection.ObservationInputInventory)
	response := t421FinalAuthorityResponse{
		Schema: t421FinalAuthoritySchema,
		Authority: t421FinalAuthorityState{
			PhysicalCommit: source.Commit, PhysicalTree: source.Projection.TreeOID,
			SourceGenerationSHA256:          search.Source.Digest,
			SearchGenerationSHA256:          search.Search.Digest,
			ObservationGenerationSHA256:     observation.ObservationGenerationDigest,
			CandidateGenerationSHA256:       candidateState.GenerationDigest,
			CatalogRootSHA256:               catalogRoot.Digest,
			CatalogActivationPlanSHA256:     activation.PlanDigest,
			CatalogActivationScheduleSHA256: activation.ScheduleDigest,
			CatalogActivationUnitSHA256:     activation.UnitDigest,
			ResolverCatalogGenerationSHA256: resolver.Authority.ResolverGenerationDigest,
			ResolverCatalogRootSHA256:       resolver.Authority.ResolverManifestDigest,
			CallerGenerationSHA256:          caller.ExpectedGeneration.Digest,
			CallerRootSHA256:                caller.State.ManifestDigest,
			RelationshipGenerationSHA256:    relationship.Root.GenerationDigest,
			RelationshipRootSHA256:          relationship.Root.Digest,
			RelationshipProvenanceSHA256:    relationship.Root.Authority.Upstream.ProvenanceDigest,
			SearchInventory:                 searchInventory, ObservationInputInventory: observationInventory,
			ExtractionRootsSHA256: rootsSHA256, Current: true,
		},
		Projection: t421FinalStateProjection{
			Schema:               t421FinalProjectionSchema,
			CatalogLogicalSHA256: catalog.Projection.CatalogLogicalSHA256,
			SemanticSHA256:       catalog.Projection.SemanticSHA256,
			CatalogSource:        catalog.Projection.CatalogSource,
			Catalog:              catalog.Projection.Catalog, MembershipSet: catalog.Projection.MembershipSet,
			Placements: catalog.Projection.Placements, UnownedPrefixes: catalog.Projection.UnownedPrefixes,
			ServiceQueries:  catalog.Projection.ServiceQueries,
			SearchInventory: searchInventory, ObservationInputInventory: observationInventory,
			ExtractionRoots:     extractionProjections,
			RelationshipResults: semantic.Families, ProductRelationship: semantic.Product,
		},
		ExtractionRoots: extractionRoots,
	}
	raw, err := t421FinalMarshal(response)
	if err != nil {
		return nil, nil, err
	}
	if t421FinalAuthorityBeforeConfirm != nil {
		if err := t421FinalAuthorityBeforeConfirm(ctx); err != nil {
			return nil, nil, err
		}
	}

	if err := reader.confirm(
		ctx, repository, selector, stateSummary, activation, catalogRoot, observation,
		candidateState, relationship, caller, extractionAuthorities,
	); err != nil {
		return nil, nil, err
	}
	if err := caller.Release(); err != nil {
		return nil, nil, errors.Join(err, errors.New("release caller authority"))
	}
	commit := func() error {
		return t421FinalCommitCaches(ctx, sourcePending, catalogPending)
	}
	return raw, commit, nil
}

func t421FinalCommitCaches(
	ctx context.Context,
	source *t421FinalSourcePending,
	catalog *t421FinalCatalogPending,
) error {
	if ctx == nil || source != nil && source.cache == nil ||
		catalog != nil && catalog.cache == nil {
		return errors.New("invalid T42.1 final cache commit")
	}
	if source != nil {
		source.cache.mu.Lock()
		defer source.cache.mu.Unlock()
	}
	if catalog != nil {
		catalog.cache.mu.Lock()
		defer catalog.cache.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if source != nil {
		source.cache.valid = true
		source.cache.key = source.key
		source.cache.projection = cloneT421FinalSourceProjection(source.projection)
	}
	if catalog != nil {
		catalog.cache.valid = true
		catalog.cache.key = catalog.key
		catalog.cache.projection = catalog.projection
	}
	return nil
}

func (reader *t421FinalAuthorityReader) readExtraction(
	ctx context.Context,
	sourceProjection t421sourceprojection.Projection,
	observation observationpublication.InventoryAuthorityV2,
	candidateState candidate.State,
) ([]t421extractionprojection.PhaseProjection, []t421extractionprojection.RootResult,
	[]candidate.DownstreamDomainAuthority, []string, error) {
	if len(sourceProjection.CandidateInventories) != len(reader.identities) {
		return nil, nil, nil, nil, errors.New("candidate inventory is incomplete")
	}
	projections := make([]t421extractionprojection.PhaseProjection, 0, len(reader.identities))
	roots := make([]t421extractionprojection.RootResult, 0, len(reader.identities))
	authorities := make([]candidate.DownstreamDomainAuthority, 0, len(reader.identities))
	plans := make([]string, 0, len(reader.identities))
	for index, identity := range reader.identities {
		inventory := sourceProjection.CandidateInventories[index]
		if inventory.Domain != identity.Domain {
			return nil, nil, nil, nil, errors.New("candidate inventory order changed")
		}
		snapshot, err := extractionpublication.CurrentSnapshot(
			ctx, reader.store, reader.repository, identity.Domain,
		)
		if err != nil || !reader.matchesExtraction(
			identity, snapshot, observation, candidateState,
		) {
			return nil, nil, nil, nil, errors.Join(
				err, errors.New("extraction authority changed"),
			)
		}
		domain, err := reader.openDomain(ctx, snapshot.Plan)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		result, err := t421extractionprojection.Derive(
			ctx, snapshot, domain,
			t421extractionprojection.SetIdentity(inventory.Candidates),
			t421extractionprojection.SetIdentity(inventory.Proof),
		)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		projections = append(projections, result.Projection)
		roots = append(roots, result.Root)
		authorities = append(authorities, snapshot.Authority)
		plans = append(plans, snapshot.Plan.Digest)
	}
	return projections, roots, authorities, plans, nil
}

func (reader *t421FinalAuthorityReader) matchesExtraction(
	identity candidate.PolicyIdentity,
	snapshot extractionpublication.DomainSnapshot,
	observation observationpublication.InventoryAuthorityV2,
	candidateState candidate.State,
) bool {
	plan := snapshot.Plan
	return plan.Repository == reader.repository &&
		plan.Domain == identity.Domain && plan.Version == identity.Version &&
		plan.CandidateManifestDigest == candidateState.ManifestDigest &&
		plan.CandidateGenerationDigest == candidateState.GenerationDigest &&
		plan.CandidatePolicyDigest == candidateState.PolicyDigest &&
		plan.SourceGenerationDigest == observation.SourceGenerationDigest &&
		plan.ObservationGenerationDigest == observation.ObservationGenerationDigest
}

func (reader *t421FinalAuthorityReader) authorizedRepository(
	ctx context.Context,
) (store.Repo, error) {
	repository, err := reader.store.GetRepo(ctx, reader.repository)
	if err != nil || repository == nil || repository.Name != reader.repository ||
		repository.Deleting || repository.IndexedCommitHash == "" {
		return store.Repo{}, errors.Join(err, errors.New("repository is unavailable"))
	}
	if reader.visible != nil {
		if visible := reader.visible(ctx); visible != nil && !visible(*repository) {
			return store.Repo{}, errors.New("repository is unavailable")
		}
	}
	return *repository, nil
}

func (reader *t421FinalAuthorityReader) selectedState(
	ctx context.Context,
	selector store.ServiceRuntimeSelector,
) (servicecatalog.RepositoryState, error) {
	if selector.Repository != reader.repository || selector.Backend != store.ServiceRuntimeV3 {
		return servicecatalog.RepositoryState{}, errors.New("selected runtime is not v3")
	}
	value, err := reader.store.GetServiceStateV3SummarySnapshot(
		ctx, reader.repository, selector.StateControlRevision, selector.StateSummaryDigest,
	)
	if err != nil || !t421FinalSelectedStateMatches(selector, value) {
		return servicecatalog.RepositoryState{}, errors.Join(err, errors.New("selected state changed"))
	}
	return value, nil
}

func t421FinalSelectedStateMatches(
	selector store.ServiceRuntimeSelector,
	state servicecatalog.RepositoryState,
) bool {
	return selector.Repository == state.Repository &&
		selector.CatalogRootDigest == state.CatalogGeneration &&
		selector.CatalogControlRevision == state.CatalogControlRevision &&
		selector.StateControlRevision == state.ControlRevision &&
		selector.StateSummaryDigest == state.SummaryDigest
}

func (reader *t421FinalAuthorityReader) matchesRelationship(
	selector store.ServiceRuntimeSelector,
	state servicecatalog.RepositoryState,
	catalog servicecatalogv3.Root,
	observation observationpublication.InventoryAuthorityV2,
	domains []candidate.DownstreamDomainAuthority,
	candidateState candidate.State,
	root relationshippublication.RootV3,
) bool {
	sourceGeneration, err := servicecatalogv3.SourceGenerationDigest(catalog)
	if err != nil || relationshippublication.ValidateRootV3(root) != nil ||
		root.GenerationDigest != selector.RelationshipGenerationDigest ||
		root.Digest != selector.RelationshipRootDigest {
		return false
	}
	authority := root.Authority
	if authority.Repository != reader.repository ||
		authority.CatalogRootDigest != catalog.Digest ||
		authority.CatalogLogicalDigest != catalog.LogicalDigest ||
		authority.CatalogSourceGeneration != sourceGeneration ||
		authority.CatalogControlRevision != state.CatalogControlRevision ||
		authority.ServiceStateSummaryDigest != state.SummaryDigest ||
		authority.ServiceStateControlRevision != state.ControlRevision ||
		authority.ObservationGenerationDigest != observation.ObservationGenerationDigest ||
		authority.ObservationManifestDigest != observation.InventoryRootDigest ||
		authority.ObservationSourceDigest != observation.SourceGenerationDigest ||
		authority.Upstream.Observation.SourceRootDigest != observation.SourceRootDigest ||
		authority.Upstream.Observation.ObservationRootDigest != observation.InventoryRootDigest ||
		authority.UpstreamDigest != authority.Upstream.Digest ||
		authority.Upstream.Repository != reader.repository ||
		len(authority.Upstream.Required) != len(reader.identities) ||
		len(authority.Upstream.Domains) != len(domains) {
		return false
	}
	for index, identity := range reader.identities {
		if authority.Upstream.Required[index].Domain != identity.Domain ||
			authority.Upstream.Required[index].Version != identity.Version ||
			authority.Upstream.Domains[index] != domains[index] ||
			domains[index].CandidateManifestDigest != candidateState.ManifestDigest ||
			domains[index].CandidatePolicyDigest != candidateState.PolicyDigest {
			return false
		}
	}
	return true
}

func (reader *t421FinalAuthorityReader) openRelationshipComponents(
	ctx context.Context,
	root relationshippublication.RootV3,
	projections []relationshippublication.Projection,
) (resolvernamespace.Root, error) {
	authority := root.Authority
	resolver, err := resolvernamespace.OpenGeneration(
		ctx, filepath.Join(reader.dataDir, "relationship-resolver-namespaces"), reader.repository,
		authority.ResolverGenerationDigest, authority.ResolverRootDigest,
	)
	if err != nil {
		return resolvernamespace.Root{}, err
	}
	if err := resolver.ValidateComplete(ctx); err != nil {
		return resolvernamespace.Root{}, err
	}
	resolverRoot := resolver.Root()
	expected, err := t421FinalNewComponentInventory(projections)
	if err != nil {
		return resolvernamespace.Root{}, err
	}
	rpc, err := rpccallerposting.OpenGeneration(
		ctx, filepath.Join(reader.dataDir, "relationship-rpc-postings"), reader.repository,
		authority.RPCGenerationDigest, authority.RPCRootDigest,
	)
	if err != nil {
		return resolvernamespace.Root{}, err
	}
	if err := rpc.WalkPostings(ctx, func(posting rpccallerposting.Posting) error {
		return expected.take(t421FinalRPCComponentIdentity(posting))
	}); err != nil {
		return resolvernamespace.Root{}, err
	}
	kafka, err := kafkatopicposting.OpenGeneration(
		ctx, filepath.Join(reader.dataDir, "relationship-kafka-postings"), reader.repository,
		authority.KafkaGenerationDigest, authority.KafkaRootDigest,
	)
	if err != nil {
		return resolvernamespace.Root{}, err
	}
	if err := kafka.WalkPostings(ctx, func(posting kafkatopicposting.Posting) error {
		return expected.take(t421FinalKafkaComponentIdentity(posting))
	}); err != nil {
		return resolvernamespace.Root{}, err
	}
	return resolverRoot, expected.complete()
}

type t421FinalComponentIdentity struct {
	kind          string
	plane         string
	class         string
	lookupKey     string
	postingDigest string
}

type t421FinalComponentInventory map[t421FinalComponentIdentity]int

func t421FinalNewComponentInventory(
	projections []relationshippublication.Projection,
) (t421FinalComponentInventory, error) {
	result := make(t421FinalComponentInventory, len(projections))
	for _, projection := range projections {
		key := t421FinalComponentIdentity{
			kind: projection.Kind, plane: projection.Plane, class: projection.Class,
			lookupKey: projection.LookupKey, postingDigest: projection.PostingDigest,
		}
		if (key.kind != "rpc" && key.kind != "kafka") || key.plane == "" || key.class == "" || key.postingDigest == "" {
			return nil, errors.New("relationship component identity is invalid")
		}
		result[key]++
	}
	return result, nil
}

func (inventory t421FinalComponentInventory) take(key t421FinalComponentIdentity) error {
	remaining := inventory[key]
	if remaining == 0 {
		return errors.New("relationship component composition changed")
	}
	if remaining == 1 {
		delete(inventory, key)
	} else {
		inventory[key] = remaining - 1
	}
	return nil
}

func (inventory t421FinalComponentInventory) complete() error {
	if len(inventory) != 0 {
		return errors.New("relationship component composition changed")
	}
	return nil
}

func t421FinalRPCComponentIdentity(posting rpccallerposting.Posting) t421FinalComponentIdentity {
	return t421FinalComponentIdentity{
		kind: "rpc", plane: posting.Protocol, class: posting.Class,
		lookupKey: posting.LookupOperation, postingDigest: posting.Digest,
	}
}

func t421FinalKafkaComponentIdentity(posting kafkatopicposting.Posting) t421FinalComponentIdentity {
	lookupKey := ""
	if posting.Class == "literal" {
		lookupKey = posting.TopicSpelling
	}
	return t421FinalComponentIdentity{
		kind: "kafka", plane: posting.Plane, class: posting.Class,
		lookupKey: lookupKey, postingDigest: posting.Digest,
	}
}

func (reader *t421FinalAuthorityReader) matchesCaller(
	read *callerexecute.PublicationRead,
	commit string,
	candidateState candidate.State,
	root relationshippublication.RootV3,
	resolver resolvernamespace.Root,
) bool {
	if read == nil || read.Availability != callerexecute.PublicationCurrent ||
		read.State == nil || read.Resolver == nil || read.ExpectedGeneration.Digest == "" {
		return false
	}
	return read.ExpectedGeneration.Repository == reader.repository &&
		read.ExpectedGeneration.HeadCommit == commit &&
		read.ExpectedGeneration.CandidateManifestDigest == candidateState.ManifestDigest &&
		read.ExpectedGeneration.CandidatePolicyDigest == candidateState.PolicyDigest &&
		t421CallerResolverMatches(read.ExpectedGeneration, root, resolver) &&
		read.Resolver.GenerationDigest == resolver.Authority.ResolverGenerationDigest &&
		read.Resolver.AuthorityDigest == resolver.Authority.ResolverManifestDigest &&
		read.State.Generation.Digest == read.ExpectedGeneration.Digest &&
		read.State.ManifestDigest != ""
}

func (reader *t421FinalAuthorityReader) confirm(
	ctx context.Context,
	repository store.Repo,
	selector store.ServiceRuntimeSelector,
	state servicecatalog.RepositoryState,
	activation store.ServiceStateV3ActivationAuthority,
	catalog servicecatalogv3.Root,
	observation observationpublication.InventoryAuthorityV2,
	candidateState candidate.State,
	relationship relationshippublication.SemanticSnapshotV3,
	caller *callerexecute.PublicationRead,
	domains []candidate.DownstreamDomainAuthority,
) error {
	for index, identity := range reader.identities {
		current, err := extractionpublication.CurrentSnapshot(
			ctx, reader.store, reader.repository, identity.Domain,
		)
		if err != nil || current.Authority != domains[index] {
			return errors.Join(err, errors.New("extraction authority changed"))
		}
	}
	confirmedCandidate, err := reader.candidateState(ctx)
	if err != nil || confirmedCandidate != candidateState {
		return errors.Join(err, errors.New("candidate authority changed"))
	}
	confirmedObservation, err := observationpublication.CurrentInventoryAuthorityReferenceV2(
		ctx, filepath.Join(reader.dataDir, "observations"), reader.repository,
	)
	if err != nil || confirmedObservation != observation {
		return errors.Join(err, errors.New("observation authority changed"))
	}
	confirmedCatalog, err := reader.store.ReadServiceCatalogV3Root(
		ctx, reader.repository, selector.CatalogRootDigest,
	)
	if err != nil || !reflect.DeepEqual(confirmedCatalog, catalog) {
		return errors.Join(err, errors.New("catalog authority changed"))
	}
	confirmedState, err := reader.selectedState(ctx, selector)
	if err != nil || confirmedState != state {
		return errors.Join(err, errors.New("state authority changed"))
	}
	confirmedActivation, err := reader.store.ReadServiceStateV3ActivationAuthority(ctx, selector)
	if err != nil || confirmedActivation != activation {
		return errors.Join(err, errors.New("activation authority changed"))
	}
	currentCaller, err := reader.caller.Current(ctx, caller)
	if err != nil || !currentCaller {
		return errors.Join(err, errors.New("caller authority changed"))
	}
	if err := reader.relationship.ConfirmCurrentSemanticSnapshot(
		ctx, filepath.Join(reader.dataDir, "relationships"), reader.repository, relationship,
	); err != nil {
		return err
	}
	if err := reader.store.ConfirmServiceRuntimeSelector(ctx, selector); err != nil {
		return err
	}
	confirmedRepository, err := reader.authorizedRepository(ctx)
	if err != nil || confirmedRepository.IndexedCommitHash != repository.IndexedCommitHash ||
		confirmedRepository.Name != repository.Name {
		return errors.Join(err, errors.New("repository authority changed"))
	}
	return nil
}

func t421FinalMarshal(value t421FinalAuthorityResponse) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, errors.Join(err, errors.New("T42.1 final authority response is invalid"))
	}
	return append(raw, '\n'), nil
}

func t421FinalReceiptSHA256(value any) (string, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return t421FinalSHA256(append(raw, '\n')), nil
}
