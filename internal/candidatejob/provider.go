package candidatejob

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

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
	open       func(context.Context, string, candidate.Expected) (*candidate.Publication, error)
	openCaller func(context.Context, string, candidate.Expected) (*candidate.CallerPlan, error)

	current currentPublicationCache
}

// currentPublicationCache retains at most one strictly validated immutable
// generation per repository. A replacement drops the cache's only reference
// to the old generation; callers already using that value remain protected by
// OpenCurrentPublication's post-open pointer fence.
type currentPublicationCache struct {
	mu      sync.Mutex
	entries map[string]*currentPublicationOpen
}

type currentPublicationOpen struct {
	state       candidate.State
	done        chan struct{}
	publication *candidate.Publication
	err         error
}

func (cache *currentPublicationCache) open(
	ctx context.Context,
	state candidate.State,
	opener func(context.Context) (*candidate.Publication, error),
) (*candidate.Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*currentPublicationOpen)
	}
	entry := cache.entries[state.Repository]
	if entry != nil && entry.state == state {
		if entry.publication != nil {
			publication := entry.publication
			cache.mu.Unlock()
			return publication, nil
		}
		done := entry.done
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			return entry.publication, entry.err
		}
	}
	entry = &currentPublicationOpen{state: state, done: make(chan struct{})}
	cache.entries[state.Repository] = entry
	cache.mu.Unlock()

	publication, err := opener(ctx)
	if err == nil && publication == nil {
		err = errors.New("candidate publication opener returned nil")
	}
	cache.mu.Lock()
	entry.publication, entry.err = publication, err
	if err != nil && cache.entries[state.Repository] == entry {
		delete(cache.entries, state.Repository)
	}
	close(entry.done)
	cache.mu.Unlock()
	return publication, err
}

func (cache *currentPublicationCache) evict(
	state candidate.State,
	publication *candidate.Publication,
) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.entries[state.Repository]
	if entry != nil && entry.state == state && entry.publication == publication {
		delete(cache.entries, state.Repository)
	}
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
		open:       candidate.OpenContext,
		openCaller: candidate.OpenCallerPlanContext,
	}, nil
}

// PolicyDigest exposes the exact policy identity expected by this adapter.
func (provider *Provider) PolicyDigest() string {
	if provider == nil || provider.policies == nil {
		return ""
	}
	return provider.policies.digest
}

// CandidateManifestIdentity validates only the committed pointer identity and
// stable publication marker. It deliberately does not open the manifest or
// any member bytes; extraction uses this boundary to decide an exact no-op
// before taking the repository mirror lock.
func (provider *Provider) CandidateManifestIdentity(
	ctx context.Context,
	request extract.CandidateManifestRequest,
) (string, error) {
	state, _, _, err := provider.resolve(ctx, request)
	if err != nil {
		return "", err
	}
	return state.ManifestDigest, nil
}

// CandidateManifestGeneration returns the complete persisted preflight
// identity without touching publication bytes.
func (provider *Provider) CandidateManifestGeneration(
	ctx context.Context,
	request extract.CandidateManifestRequest,
) (extract.CandidateManifestPointerIdentity, error) {
	state, _, revision, err := provider.resolve(ctx, request)
	if err != nil {
		return extract.CandidateManifestPointerIdentity{}, err
	}
	return extract.CandidateManifestPointerIdentity{
		ManifestDigest:  state.ManifestDigest,
		PolicyDigest:    state.PolicyDigest,
		ControlRevision: revision,
	}, nil
}

func (provider *Provider) OpenCandidateManifest(
	ctx context.Context,
	request extract.CandidateManifestRequest,
) (extract.CandidateManifest, error) {
	state, expected, revision, err := provider.resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	if provider.open == nil {
		return nil, errors.New("candidate provider opener is not initialized")
	}
	if err := ensureCandidateRoot(provider.root, false); err != nil {
		return nil, err
	}
	publication, err := provider.open(ctx, provider.root, expected)
	if err != nil {
		return nil, fmt.Errorf("open candidate publication: %w", err)
	}
	if publication == nil {
		return nil, errors.New("candidate publication opener returned nil")
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
		publication:     publication,
		allowed:         cloneSet(provider.domainKeys),
		controlRevision: revision,
	}, nil
}

// OpenCurrentPublication resolves the current strict store pointer into the
// complete validated candidate publication for T40.10 planning. It is not a
// product reader: callers still have to bind a derived plan to the returned
// immutable state and re-fence the pointer before publication.
func (provider *Provider) OpenCurrentPublication(
	ctx context.Context,
	repository string,
	unit *analysisunit.State,
) (*candidate.Publication, error) {
	if provider == nil || provider.open == nil {
		return nil, errors.New("candidate provider is not initialized")
	}
	state, err := provider.CurrentPublicationState(ctx, repository, unit)
	if err != nil {
		return nil, err
	}
	expected := candidate.Expected{
		Repository: state.Repository, Commit: state.Commit,
		Unit:         analysisunit.CloneState(unit),
		Policies:     slices.Clone(provider.policies.identities),
		PolicyDigest: state.PolicyDigest, GenerationDigest: state.GenerationDigest,
		ManifestDigest: state.ManifestDigest,
	}
	publication, err := provider.current.open(ctx, state, func(
		openCtx context.Context,
	) (*candidate.Publication, error) {
		return provider.open(openCtx, provider.root, expected)
	})
	if err != nil {
		return nil, fmt.Errorf("open current candidate publication: %w", err)
	}
	confirmed, confirmErr := provider.CurrentPublicationState(ctx, repository, unit)
	if publication == nil || confirmErr != nil || publication.State() != state || confirmed != state {
		provider.current.evict(state, publication)
		return nil, errors.Join(confirmErr, errors.New("current candidate publication changed during strict open"))
	}
	return publication, nil
}

// CurrentPublicationState reads only the committed pointer and publication
// marker. T40.10 uses it to recognize an exact durable execution generation
// before opening any manifest/member controls.
func (provider *Provider) CurrentPublicationState(
	ctx context.Context,
	repository string,
	unit *analysisunit.State,
) (candidate.State, error) {
	if provider == nil || provider.store == nil || provider.policies == nil {
		return candidate.State{}, errors.New("candidate provider is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return candidate.State{}, err
	}
	if candidate.IsPublishing(provider.root, repository) {
		return candidate.State{}, candidate.ErrPublishing
	}
	pointer, err := provider.store.GetCandidateManifestPublication(ctx, repository)
	if err != nil {
		return candidate.State{}, fmt.Errorf("load candidate publication pointer: %w", err)
	}
	if pointer == nil {
		return candidate.State{}, errors.New("candidate publication store returned nil")
	}
	state, err := pointerState(*pointer)
	unitDigest, unitErr := checkedUnitDigest(repository, unit)
	if err != nil || unitErr != nil || state.PolicyDigest != provider.policies.digest ||
		state.UnitDigest != unitDigest {
		return candidate.State{}, fmt.Errorf("candidate publication pointer is stale: %w", extract.ErrCandidateManifestStale)
	}
	if candidate.IsPublishing(provider.root, repository) {
		return candidate.State{}, candidate.ErrPublishing
	}
	confirmed, err := provider.store.GetCandidateManifestPublication(ctx, repository)
	if err != nil || confirmed == nil {
		return candidate.State{}, errors.Join(err, errors.New("candidate publication pointer changed during state read"))
	}
	confirmedState, err := pointerState(*confirmed)
	if err != nil || confirmedState != state {
		return candidate.State{}, errors.Join(err, errors.New("candidate publication pointer changed during state read"))
	}
	return state, nil
}

// OpenCandidateCallerPlan resolves the same exact store-bound generation as
// OpenCandidateManifest, but opens only the canonical manifest and caller leaf
// envelopes. Individual leaf contents are descriptor-checked and streamed on
// demand by the returned plan.
func (provider *Provider) OpenCandidateCallerPlan(
	ctx context.Context,
	request extract.CandidateManifestRequest,
) (extract.CandidateCallerPlan, error) {
	state, expected, revision, err := provider.resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	if provider.openCaller == nil {
		return nil, errors.New("candidate caller-plan opener is not initialized")
	}
	if err := ensureCandidateRoot(provider.root, false); err != nil {
		return nil, err
	}
	plan, err := provider.openCaller(ctx, provider.root, expected)
	if err != nil {
		return nil, fmt.Errorf("open candidate caller plan: %w", err)
	}
	if plan == nil {
		return nil, errors.New("candidate caller-plan opener returned nil")
	}
	if plan.State() != state {
		return nil, errors.New(
			"candidate caller-plan bytes do not match the persisted pointer",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &callerPlanAdapter{
		plan: plan, allowed: cloneSet(provider.domainKeys),
		controlRevision: revision,
	}, nil
}

func (provider *Provider) resolve(
	ctx context.Context,
	request extract.CandidateManifestRequest,
) (candidate.State, candidate.Expected, uint64, error) {
	if provider == nil || provider.store == nil || provider.policies == nil {
		return candidate.State{}, candidate.Expected{},
			0,
			errors.New("candidate provider is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return candidate.State{}, candidate.Expected{}, 0, err
	}
	if !slices.Equal(request.Domains, provider.domains) {
		return candidate.State{}, candidate.Expected{}, 0, errors.New(
			"candidate manifest request domain set is partial, reordered, or stale",
		)
	}
	unitDigest, err := checkedUnitDigest(
		request.Repository, request.AnalysisUnit,
	)
	if err != nil {
		return candidate.State{}, candidate.Expected{}, 0, err
	}
	generation, err := candidate.GenerationDigest(
		request.Repository, request.Commit, request.AnalysisUnit,
		provider.policies.identities,
	)
	if err != nil {
		return candidate.State{}, candidate.Expected{},
			0,
			fmt.Errorf("candidate request identity: %w", err)
	}
	if candidate.IsPublishing(provider.root, request.Repository) {
		return candidate.State{}, candidate.Expected{}, 0, candidate.ErrPublishing
	}
	pointer, err := provider.store.GetCandidateManifestPublication(
		ctx, request.Repository,
	)
	if err != nil {
		return candidate.State{}, candidate.Expected{},
			0,
			fmt.Errorf("load candidate publication pointer: %w", err)
	}
	if pointer == nil {
		return candidate.State{}, candidate.Expected{},
			0,
			errors.New("candidate publication store returned nil")
	}
	state, err := pointerState(*pointer)
	if err != nil {
		return candidate.State{}, candidate.Expected{},
			0,
			fmt.Errorf("candidate publication pointer: %w", err)
	}
	if state.Repository != request.Repository ||
		state.Commit != request.Commit ||
		state.UnitDigest != unitDigest ||
		state.PolicyDigest != provider.policies.digest ||
		state.GenerationDigest != generation ||
		state.Manifest != candidate.ManifestName(request.Repository) {
		return candidate.State{}, candidate.Expected{}, 0, fmt.Errorf(
			"%w: candidate publication pointer does not match requested indexed generation",
			extract.ErrCandidateManifestStale,
		)
	}
	if candidate.IsPublishing(provider.root, request.Repository) {
		return candidate.State{}, candidate.Expected{}, 0, candidate.ErrPublishing
	}
	return state, candidate.Expected{
		Repository: request.Repository, Commit: request.Commit,
		Unit:             analysisunit.CloneState(request.AnalysisUnit),
		Policies:         slices.Clone(provider.policies.identities),
		PolicyDigest:     provider.policies.digest,
		GenerationDigest: generation,
		ManifestDigest:   state.ManifestDigest,
	}, pointer.ControlRevision, nil
}

type manifestAdapter struct {
	publication     *candidate.Publication
	allowed         map[string]struct{}
	controlRevision uint64
}

type callerPlanAdapter struct {
	plan            *candidate.CallerPlan
	allowed         map[string]struct{}
	controlRevision uint64
}

func (plan *callerPlanAdapter) CandidateControlRevision() uint64 {
	if plan == nil {
		return 0
	}
	return plan.controlRevision
}

func (plan *callerPlanAdapter) Identity() string {
	if plan == nil || plan.plan == nil {
		return ""
	}
	return plan.plan.State().ManifestDigest
}

func (plan *callerPlanAdapter) CallerDomains() []extract.CandidateManifestDomain {
	if plan == nil || plan.plan == nil {
		return nil
	}
	identities := plan.plan.Domains()
	result := make([]extract.CandidateManifestDomain, 0, len(identities))
	for _, identity := range identities {
		if _, ok := plan.allowed[domainKey(identity.Domain, identity.Version)]; !ok {
			continue
		}
		result = append(result, extract.CandidateManifestDomain{
			Domain: identity.Domain, Version: identity.Version,
		})
	}
	return result
}

func (plan *callerPlanAdapter) CallerLeaves() []extract.CandidateCallerLeaf {
	if plan == nil || plan.plan == nil {
		return nil
	}
	leaves := plan.plan.Leaves()
	result := make([]extract.CandidateCallerLeaf, 0, len(leaves))
	for _, leaf := range leaves {
		result = append(result, callerLeafDescriptor(leaf))
	}
	return result
}

func (plan *callerPlanAdapter) ForEachCallerLeafFile(
	ctx context.Context,
	domain, version string,
	leaf extract.CandidateCallerLeaf,
	visit func(extract.CandidateManifestFile) error,
) error {
	if plan == nil || plan.plan == nil || visit == nil {
		return errors.New("candidate caller-plan replay is invalid")
	}
	if _, ok := plan.allowed[domainKey(domain, version)]; !ok {
		return fmt.Errorf("candidate domain %s/%s is outside the request", domain, version)
	}
	selected := candidate.CallerLeaf{
		Artifact: candidate.Artifact{
			Name: leaf.Name, Ordinal: leaf.Ordinal,
			RecordCount: leaf.RecordCount, DeclaredBytes: leaf.DeclaredBytes,
			ContentBytes: leaf.ContentBytes, ContentDigest: leaf.ContentDigest,
		},
		Prefix: leaf.Prefix, PrefixBits: leaf.PrefixBits,
	}
	return plan.plan.ReplayLeaf(
		ctx, domain, version, selected,
		func(record candidate.Record) error {
			return visit(extract.CandidateManifestFile{
				Path: record.Path, ObjectID: record.OID,
				DeclaredBytes: record.DeclaredBytes,
				SourceLane:    record.SourceLane, Required: record.Required,
				InUnit: record.InUnit,
			})
		},
	)
}

func callerLeafDescriptor(leaf candidate.CallerLeaf) extract.CandidateCallerLeaf {
	return extract.CandidateCallerLeaf{
		Name: leaf.Name, Ordinal: leaf.Ordinal,
		Prefix: leaf.Prefix, PrefixBits: leaf.PrefixBits,
		RecordCount: leaf.RecordCount, DeclaredBytes: leaf.DeclaredBytes,
		ContentBytes: leaf.ContentBytes, ContentDigest: leaf.ContentDigest,
	}
}

func (manifest *manifestAdapter) CandidateControlRevision() uint64 {
	if manifest == nil {
		return 0
	}
	return manifest.controlRevision
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

func (manifest *manifestAdapter) CandidateManifestDiagnostics() extract.CandidateManifestDiagnostics {
	if manifest == nil || manifest.publication == nil {
		return extract.CandidateManifestDiagnostics{}
	}
	source := manifest.publication.Diagnostics()
	convert := func(
		input candidate.PublicationPlaneDiagnostics,
	) extract.CandidateManifestPlaneDiagnostics {
		return extract.CandidateManifestPlaneDiagnostics{
			Records: input.Records, Members: input.Members,
			CanonicalBytes: input.CanonicalBytes,
			DeclaredBytes:  input.DeclaredBytes,
		}
	}
	return extract.CandidateManifestDiagnostics{
		Repository: convert(source.Repository),
		Local:      convert(source.Local), Caller: convert(source.Caller),
		TypedConfigured:    source.TypedConfigured,
		TypedPresent:       source.TypedPresent,
		TypedDeclaredBytes: source.TypedDeclaredBytes,
	}
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

func (manifest *manifestAdapter) DomainScope(
	domain, version string,
) (extract.CandidateManifestScope, error) {
	if manifest == nil || manifest.publication == nil {
		return extract.CandidateManifestScope{},
			errors.New("candidate manifest scope is invalid")
	}
	if _, ok := manifest.allowed[domainKey(domain, version)]; !ok {
		return extract.CandidateManifestScope{},
			fmt.Errorf("candidate domain %s/%s is outside the request", domain, version)
	}
	view, err := manifest.publication.Domain(domain, version)
	if err != nil {
		return extract.CandidateManifestScope{}, err
	}
	scope := view.Scope()
	domainSummary := scope.Domain
	candidateCount := domainSummary.RepositoryCandidateCount
	requiredCount := domainSummary.RepositoryRequiredCount
	declaredBytes := domainSummary.RepositoryDeclaredBytes
	digest := domainSummary.RepositoryDigest
	if domainSummary.Plane == candidate.PlaneLocal && scope.UnitDigest != "" {
		candidateCount = domainSummary.UnitCandidateCount
		requiredCount = domainSummary.UnitRequiredCount
		declaredBytes = domainSummary.UnitDeclaredBytes
		digest = domainSummary.UnitDigest
	}
	return extract.CandidateManifestScope{
		ManifestDigest:         scope.ManifestDigest,
		UnitDigest:             scope.UnitDigest,
		Plane:                  domainSummary.Plane,
		CorpusFileCount:        scope.Corpus.RegularCount,
		CorpusDeclaredBytes:    scope.Corpus.RegularDeclaredBytes,
		CorpusDigest:           scope.Corpus.RegularDigest,
		CandidateFileCount:     candidateCount,
		RequiredFileCount:      requiredCount,
		CandidateDeclaredBytes: declaredBytes,
		CandidateDigest:        digest,
	}, nil
}

func (manifest *manifestAdapter) TypedInput(
	domain, version, kind string,
) (extract.CandidateManifestTypedInput, error) {
	if manifest == nil || manifest.publication == nil {
		return extract.CandidateManifestTypedInput{},
			errors.New("candidate manifest typed input is invalid")
	}
	if _, ok := manifest.allowed[domainKey(domain, version)]; !ok {
		return extract.CandidateManifestTypedInput{},
			fmt.Errorf("candidate domain %s/%s is outside the request", domain, version)
	}
	view, err := manifest.publication.Domain(domain, version)
	if err != nil {
		return extract.CandidateManifestTypedInput{}, err
	}
	input, err := view.TypedInput(kind)
	if err != nil {
		return extract.CandidateManifestTypedInput{}, err
	}
	if input == nil {
		return extract.CandidateManifestTypedInput{}, nil
	}
	return extract.CandidateManifestTypedInput{
		Kind: input.Kind, Path: input.Path, ObjectID: input.OID,
		DeclaredBytes: input.DeclaredBytes, Present: input.Present,
	}, nil
}

func (manifest *manifestAdapter) PathInScope(filePath string) bool {
	return manifest != nil && manifest.publication != nil &&
		manifest.publication.PathInScope(filePath)
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
			SourceLane:    record.SourceLane,
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
var _ extract.CandidateCallerPlanProvider = (*Provider)(nil)
var _ extract.CandidateManifestIdentityProvider = (*Provider)(nil)
var _ extract.CandidateManifestGenerationProvider = (*Provider)(nil)
var _ extract.CandidateManifest = (*manifestAdapter)(nil)
var _ extract.CandidateManifestControl = (*manifestAdapter)(nil)
var _ extract.CandidateManifestDiagnosticsProvider = (*manifestAdapter)(nil)
var _ extract.CandidateCallerPlan = (*callerPlanAdapter)(nil)
var _ extract.CandidateManifestControl = (*callerPlanAdapter)(nil)
