package relationshippublication

import (
	"bytes"
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
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	ScheduleStage            = "service-relationship"
	ScheduleMaxAttempts      = 5
	ScheduleRepositoryTokens = 1
	BindingSchema            = "phebs-relationship-schedule-binding-v1"
	BindingSchemaV2          = "phebs-relationship-schedule-binding-v2"
	BindingSchemaV3          = "phebs-relationship-schedule-binding-v3"
	ResolverResidentLimit    = 128 << 20
	RPCResidentLimit         = 192 << 20
	KafkaResidentLimit       = 160 << 20

	mutationAcquireTimeout = 5 * time.Second
	mutationProbeTimeout   = 25 * time.Millisecond
	mutationRetryDelay     = 10 * time.Millisecond
)

// RuntimeStore is the exact durable control surface used by the relationship
// worker. It deliberately excludes evidence and historical-generation scans.
type RuntimeStore interface {
	store.GenerationSchedulerStore
	GetGenerationScheduleFailure(
		context.Context, string, string, string,
	) (*store.GenerationScheduleFailure, error)
	store.ServiceCatalogPublicationStore
	store.ServiceStateStore
	store.ResolverCatalogPublicationStore
	store.PartitionedEvidenceStore
}

type Runtime struct {
	DataDir        string
	Store          RuntimeStore
	Cache          *observationpublication.Cache
	InventoryCache *observationpublication.InventoryCacheV2
	Domains        []downstreamauthority.DomainIdentity
	Admit          func(context.Context) error
	Acquire        func(context.Context) (func(), error)

	transition sync.Mutex
}

type mutationAcquirePolicy struct {
	timeout    time.Duration
	probe      time.Duration
	retryDelay time.Duration
}

func defaultMutationAcquirePolicy() mutationAcquirePolicy {
	return mutationAcquirePolicy{
		timeout: mutationAcquireTimeout, probe: mutationProbeTimeout,
		retryDelay: mutationRetryDelay,
	}
}

type scheduleBinding struct {
	Schema                string                         `json:"schema"`
	Repository            string                         `json:"repository"`
	ScheduleGeneration    string                         `json:"schedule_generation"`
	TargetGeneration      string                         `json:"target_generation"`
	PriorScheduleDigest   string                         `json:"prior_schedule_digest,omitempty"`
	ObservationGeneration string                         `json:"observation_generation"`
	CatalogGeneration     string                         `json:"catalog_generation"`
	ServiceStateSet       string                         `json:"service_state_set"`
	ServiceStateSummary   string                         `json:"service_state_summary,omitempty"`
	ServiceStateRevision  uint64                         `json:"service_state_revision,omitempty"`
	ResolverGeneration    string                         `json:"resolver_generation"`
	BuildPolicyDigest     string                         `json:"build_policy_digest,omitempty"`
	Digest                string                         `json:"digest"`
	Upstream              *downstreamauthority.Authority `json:"upstream,omitempty"`
}

type runtimeSnapshot struct {
	catalog servicecatalog.Publication
	summary servicecatalog.RepositoryState
	states  []servicecatalog.ServiceState
}

// Reconcile binds current observation, catalog, and resolver controls to one
// durable one-chunk build. No derived member or index is opened on the no-op
// path; a worker performs the admitted full joins.
func (runtime *Runtime) Reconcile(ctx context.Context, repository string) error {
	if err := runtime.validate(repository); err != nil {
		return err
	}
	if runtime.v2Enabled() {
		return runtime.reconcileV2(ctx, repository)
	}
	observationGeneration, err := observationpublication.CurrentGeneration(
		filepath.Join(runtime.DataDir, "observations"), repository,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: observation authority", ErrNotFound)
		}
		return fmt.Errorf("relationship observation authority: %w", err)
	}
	snapshot, err := runtime.loadServiceSnapshot(ctx, repository)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: catalog authority", ErrNotFound)
		}
		return fmt.Errorf("relationship catalog authority: %w", err)
	}
	catalog := &snapshot.catalog
	stateSet, err := publicationServiceStateSetDigest(snapshot.catalog, snapshot.states)
	if err != nil {
		return fmt.Errorf("relationship service-state authority: %w", err)
	}
	resolver, err := runtime.Store.GetResolverCatalogPublication(ctx, repository)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: resolver authority", ErrNotFound)
		}
		return fmt.Errorf("relationship resolver authority: %w", err)
	}
	if catalog == nil || resolver == nil {
		return fmt.Errorf("%w: relationship authority is incomplete", ErrNotFound)
	}
	resolverCurrent, err := runtime.Store.ResolverCatalogPublicationCurrent(ctx, *resolver)
	if err != nil || !resolverCurrent {
		return fmt.Errorf("%w: relationship resolver authority changed", ErrPublishing)
	}
	target, err := runtimeTarget(
		repository, observationGeneration, catalog.GenerationDigest,
		stateSet, resolver.GenerationDigest,
	)
	if err != nil {
		return err
	}
	if current, openErr := OpenCurrent(ctx, runtime.relationshipRoot(), repository); openErr == nil {
		authority := current.Root().Authority
		if authority.ObservationGenerationDigest == observationGeneration &&
			authority.CatalogGenerationDigest == catalog.GenerationDigest &&
			authority.ServiceStateSetDigest == stateSet &&
			authority.ResolverGenerationDigest == resolver.GenerationDigest {
			return nil
		}
	}
	if runtime.Admit != nil {
		if err := runtime.Admit(ctx); err != nil {
			return err
		}
	}
	scheduleGeneration, priorDigest, terminal, err := runtime.scheduleIdentity(ctx, repository, target, "")
	if err != nil {
		return err
	}
	if terminal {
		return nil
	}
	binding := scheduleBinding{
		Schema: BindingSchema, Repository: repository,
		ScheduleGeneration: scheduleGeneration, TargetGeneration: target,
		PriorScheduleDigest: priorDigest, ObservationGeneration: observationGeneration,
		CatalogGeneration: catalog.GenerationDigest, ServiceStateSet: stateSet,
		ResolverGeneration: resolver.GenerationDigest,
	}
	if err := setBindingDigest(&binding); err != nil {
		return err
	}
	if err := runtime.writeBinding(binding); err != nil {
		return err
	}
	_, err = runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: repository, Stage: ScheduleStage, Generation: scheduleGeneration,
		ResourceClass: store.GenerationResourceMemory, TotalItems: 1, ChunkItems: 1,
		MaxAttempts: ScheduleMaxAttempts, RepositoryTokens: ScheduleRepositoryTokens,
	})
	return err
}

func (runtime *Runtime) v2Enabled() bool {
	return runtime != nil && runtime.InventoryCache != nil && runtime.Domains != nil
}

func (runtime *Runtime) reconcileV2(ctx context.Context, repository string) error {
	observation, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, filepath.Join(runtime.DataDir, "observations"), repository,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: observation v2 authority", ErrNotFound)
		}
		return fmt.Errorf("relationship observation v2 authority: %w", err)
	}
	domains := make([]candidate.DownstreamDomainAuthority, 0, len(runtime.Domains))
	for _, required := range runtime.Domains {
		domain, domainErr := extractionpublication.CurrentDomainAuthority(
			ctx, runtime.Store, repository, required.Domain,
		)
		if errors.Is(domainErr, store.ErrNotFound) {
			continue
		}
		if domainErr != nil {
			return fmt.Errorf("relationship extraction authority %s: %w", required.Domain, domainErr)
		}
		if !domainMatchesObservation(required, domain, observation) {
			// A root from the previous observation generation is absent from the
			// new required aggregate. BuildRequired will make that absence explicit
			// and retire any stale relationship root below.
			continue
		}
		domains = append(domains, domain)
	}
	upstream, err := downstreamauthority.BuildRequired(observation, runtime.Domains, domains)
	if err != nil {
		return err
	}
	if err := downstreamauthority.RequireUsable(upstream); err != nil {
		release, acquireErr := runtime.acquireMutation(ctx)
		if acquireErr != nil {
			return acquireErr
		}
		defer release()
		// Re-read every small upstream pointer under the mutation boundary so a
		// late successful root cannot be hidden by an older unavailable marker.
		confirmed, confirmErr := runtime.currentUpstreamAuthority(ctx, repository)
		if confirmErr != nil || confirmed.Digest != upstream.Digest {
			return errors.Join(confirmErr, ErrPublishing)
		}
		_, markErr := MarkUnavailable(ctx, runtime.relationshipRoot(), repository, upstream)
		return markErr
	}
	summary, err := runtime.Store.GetServiceStateSummary(ctx, repository)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: catalog authority", ErrNotFound)
		}
		return err
	}
	if summary == nil || servicecatalog.ValidateRepositoryState(*summary, true) != nil ||
		summary.Repository != repository {
		return fmt.Errorf("%w: service-state summary", ErrInvalid)
	}
	resolver, err := runtime.Store.GetResolverCatalogPublication(ctx, repository)
	if err != nil || resolver == nil {
		return fmt.Errorf("%w: resolver authority", ErrNotFound)
	}
	resolverCurrent, err := runtime.Store.ResolverCatalogPublicationCurrent(ctx, *resolver)
	if err != nil || !resolverCurrent {
		return fmt.Errorf("%w: resolver authority changed", ErrPublishing)
	}
	if current, openErr := OpenCurrent(ctx, runtime.relationshipRoot(), repository); openErr == nil {
		if matchesCurrentV2Authority(current.Root(), upstream, *summary, resolver.GenerationDigest) {
			return nil
		}
	}
	snapshot, err := runtime.loadServiceSnapshot(ctx, repository)
	if err != nil {
		return err
	}
	if !sameSummaryFence(snapshot.summary, *summary) {
		return fmt.Errorf("%w: service-state summary changed", ErrPublishing)
	}
	stateSet, err := publicationServiceStateSetDigest(snapshot.catalog, snapshot.states)
	if err != nil {
		return err
	}
	buildPolicy, err := runtimeBuildPolicyDigest()
	if err != nil {
		return err
	}
	target, err := runtimeTargetV3(
		repository, upstream.Digest, snapshot.catalog.GenerationDigest,
		stateSet, resolver.GenerationDigest, summary.SummaryDigest, summary.ControlRevision,
		buildPolicy,
	)
	if err != nil {
		return err
	}
	legacyTarget, err := runtimeTargetV2(
		repository, upstream.Digest, snapshot.catalog.GenerationDigest,
		stateSet, resolver.GenerationDigest, summary.SummaryDigest, summary.ControlRevision,
	)
	if err != nil {
		return err
	}
	if runtime.Admit != nil {
		if err := runtime.Admit(ctx); err != nil {
			return err
		}
	}
	scheduleGeneration, priorDigest, terminal, err := runtime.scheduleIdentity(ctx, repository, target, legacyTarget)
	if err != nil {
		return err
	}
	if terminal {
		return nil
	}
	binding := scheduleBinding{
		Schema: BindingSchemaV3, Repository: repository,
		ScheduleGeneration: scheduleGeneration, TargetGeneration: target,
		PriorScheduleDigest:   priorDigest,
		ObservationGeneration: observation.ObservationGenerationDigest,
		CatalogGeneration:     snapshot.catalog.GenerationDigest, ServiceStateSet: stateSet,
		ServiceStateSummary: summary.SummaryDigest, ServiceStateRevision: summary.ControlRevision,
		ResolverGeneration: resolver.GenerationDigest, Upstream: &upstream,
		BuildPolicyDigest: buildPolicy,
	}
	if err := setBindingDigest(&binding); err != nil {
		return err
	}
	if err := runtime.writeBinding(binding); err != nil {
		return err
	}
	_, err = runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: repository, Stage: ScheduleStage, Generation: scheduleGeneration,
		ResourceClass: store.GenerationResourceMemory, TotalItems: 1, ChunkItems: 1,
		MaxAttempts: ScheduleMaxAttempts, RepositoryTokens: ScheduleRepositoryTokens,
	})
	return err
}

func matchesCurrentV2Authority(
	root Root,
	upstream downstreamauthority.Authority,
	summary servicecatalog.RepositoryState,
	resolverGeneration string,
) bool {
	authority := root.Authority
	return root.Schema == RootSchemaV2 && authority.Upstream != nil &&
		authority.Upstream.Digest == upstream.Digest &&
		authority.CatalogGenerationDigest == summary.CatalogGeneration &&
		authority.ServiceStateSummaryDigest == summary.SummaryDigest &&
		authority.ServiceStateControlRevision == summary.ControlRevision &&
		authority.ResolverGenerationDigest == resolverGeneration
}

// Handle builds all repository-shared components and publishes the one atomic
// relationship root only after rechecking every precious authority fence.
func (runtime *Runtime) Handle(ctx context.Context, chunk store.GenerationChunk) error {
	if err := runtime.validate(chunk.Repository); err != nil || chunk.Stage != ScheduleStage ||
		chunk.Offset != 0 || chunk.Length != 1 || !validDigest(chunk.Generation) {
		return fmt.Errorf("%w: relationship runtime chunk", ErrInvalid)
	}
	binding, err := runtime.readBinding(chunk.Repository, chunk.Generation)
	if err != nil {
		return err
	}
	if err := runtime.ensureBuildRoots(); err != nil {
		return err
	}
	if runtime.Acquire == nil {
		return fmt.Errorf("%w: relationship mutation lock", ErrInvalid)
	}
	release, err := runtime.acquireMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	var observationsV1 *observationpublication.Publication
	var observationsV2 observationpublication.DownstreamSource
	if isPartitionedBinding(binding.Schema) {
		if binding.Upstream == nil || runtime.InventoryCache == nil {
			return fmt.Errorf("%w: relationship v2 observation configuration", ErrInvalid)
		}
		lease, acquireErr := runtime.InventoryCache.AcquireCurrent(
			ctx, filepath.Join(runtime.DataDir, "observations"), chunk.Repository,
			binding.Upstream.Observation,
		)
		if acquireErr != nil {
			return fmt.Errorf("open relationship observations v2: %w", acquireErr)
		}
		defer lease.Release()
		observationsV2 = lease.Publication()
	} else {
		lease, acquireErr := runtime.Cache.Acquire(
			ctx, filepath.Join(runtime.DataDir, "observations"), chunk.Repository,
		)
		if acquireErr != nil {
			return fmt.Errorf("open relationship observations: %w", acquireErr)
		}
		defer lease.Release()
		observationsV1 = lease.Publication()
		if observationsV1 == nil || observationsV1.Manifest().GenerationDigest != binding.ObservationGeneration {
			return fmt.Errorf("%w: observation generation changed", ErrPublishing)
		}
	}

	resolverPointer, err := runtime.Store.GetResolverCatalogPublication(ctx, chunk.Repository)
	if err != nil || resolverPointer == nil || resolverPointer.GenerationDigest != binding.ResolverGeneration {
		return fmt.Errorf("%w: resolver generation changed", ErrPublishing)
	}
	resolverCurrent, err := runtime.Store.ResolverCatalogPublicationCurrent(ctx, *resolverPointer)
	if err != nil || !resolverCurrent {
		return fmt.Errorf("%w: resolver generation changed", ErrPublishing)
	}
	resolverState := resolvermaterialize.StateFromStore(*resolverPointer)
	protocols := resolverProtocols(resolverState)
	var descriptors []gocaller.DirectDescriptor
	if len(protocols) != 0 {
		_, views, openErr := resolvermaterialize.OpenCallerResolvers(
			ctx, filepath.Join(runtime.DataDir, "resolver-catalogs"), resolverState, protocols,
		)
		if openErr != nil {
			return fmt.Errorf("open relationship resolver catalog: %w", openErr)
		}
		for _, protocol := range protocols {
			descriptors = append(descriptors, views[protocol].Descriptors()...)
		}
	}
	var prior *resolvernamespace.Publication
	var priorRPC *rpccallerposting.Publication
	var priorKafka *kafkatopicposting.Publication
	var priorRelationship *Publication
	if current, openErr := OpenCurrent(ctx, runtime.relationshipRoot(), chunk.Repository); openErr == nil {
		priorRelationship = current
		authority := current.Root().Authority
		priorRPC, _ = rpccallerposting.OpenGeneration(
			ctx, runtime.rpcRoot(), chunk.Repository,
			authority.RPCGenerationDigest, authority.RPCRootDigest,
		)
		priorKafka, _ = kafkatopicposting.OpenGeneration(
			ctx, runtime.kafkaRoot(), chunk.Repository,
			authority.KafkaGenerationDigest, authority.KafkaRootDigest,
		)
	}
	if current, openErr := resolvernamespace.OpenCurrent(
		ctx, runtime.resolverNamespaceRoot(), chunk.Repository,
	); openErr == nil {
		prior = current
	}
	resolverRequest := resolvernamespace.BuildRequest{
		Root: runtime.resolverNamespaceRoot(), Repository: chunk.Repository,
		Commit: resolverState.Commit, ResolverGenerationDigest: resolverState.GenerationDigest,
		ResolverManifestDigest: resolverState.ManifestDigest, Descriptors: descriptors, Prior: prior,
		ResidentLimitBytes: ResolverResidentLimit,
	}
	var resolverStage *resolvernamespace.Prepared
	if isPartitionedBinding(binding.Schema) {
		resolverStage, err = resolvernamespace.BuildV2(ctx, resolvernamespace.BuildRequestV2{
			BuildRequest: resolverRequest, Upstream: *binding.Upstream,
		})
	} else {
		resolverStage, err = resolvernamespace.Build(ctx, resolverRequest)
	}
	if err != nil {
		return relationshipBuildFailure(
			err, "build relationship resolver namespaces",
			resolvernamespace.ErrLimit,
			pipelinerefusal.StageRelationshipResolverNamespaces,
		)
	}
	resolverPublication, err := resolverStage.Publish(ctx)
	if err != nil {
		return fmt.Errorf("publish relationship resolver namespaces: %w", err)
	}
	var rpcStage *rpccallerposting.Prepared
	if isPartitionedBinding(binding.Schema) {
		rpcStage, err = rpccallerposting.BuildV2(ctx, rpccallerposting.BuildRequestV2{
			Root: runtime.rpcRoot(), Observations: observationsV2, Resolver: resolverPublication,
			Upstream: *binding.Upstream, Prior: priorRPC, ResidentLimitBytes: RPCResidentLimit,
		})
	} else {
		rpcStage, err = rpccallerposting.Build(ctx, rpccallerposting.BuildRequest{
			Root: runtime.rpcRoot(), Observations: observationsV1, Resolver: resolverPublication,
			ResidentLimitBytes: RPCResidentLimit,
		})
	}
	if err != nil {
		return relationshipBuildFailure(
			err, "build relationship RPC postings",
			rpccallerposting.ErrLimit,
			pipelinerefusal.StageRelationshipRPCPostings,
		)
	}
	rpcPublication, err := rpcStage.Publish(ctx)
	if err != nil {
		return fmt.Errorf("publish relationship RPC postings: %w", err)
	}
	var kafkaStage *kafkatopicposting.Prepared
	if isPartitionedBinding(binding.Schema) {
		kafkaStage, err = kafkatopicposting.BuildV2(ctx, kafkatopicposting.BuildRequestV2{
			Root: runtime.kafkaRoot(), Observations: observationsV2,
			Upstream: *binding.Upstream, Prior: priorKafka, ResidentLimitBytes: KafkaResidentLimit,
		})
	} else {
		kafkaStage, err = kafkatopicposting.Build(ctx, kafkatopicposting.BuildRequest{
			Root: runtime.kafkaRoot(), Observations: observationsV1,
			ResidentLimitBytes: KafkaResidentLimit,
		})
	}
	if err != nil {
		return relationshipBuildFailure(
			err, "build relationship Kafka postings",
			kafkatopicposting.ErrLimit,
			pipelinerefusal.StageRelationshipKafkaProjection,
		)
	}
	kafkaPublication, err := kafkaStage.Publish(ctx)
	if err != nil {
		return fmt.Errorf("publish relationship Kafka postings: %w", err)
	}

	snapshot, err := runtime.loadServiceSnapshot(ctx, chunk.Repository)
	if err != nil {
		return err
	}
	if snapshot.catalog.GenerationDigest != binding.CatalogGeneration {
		return fmt.Errorf("%w: catalog generation changed", ErrPublishing)
	}
	stateSet, err := publicationServiceStateSetDigest(snapshot.catalog, snapshot.states)
	if err != nil || stateSet != binding.ServiceStateSet {
		return fmt.Errorf("%w: service-state set changed", ErrPublishing)
	}
	if isPartitionedBinding(binding.Schema) &&
		(snapshot.summary.SummaryDigest != binding.ServiceStateSummary ||
			snapshot.summary.ControlRevision != binding.ServiceStateRevision) {
		return fmt.Errorf("%w: service-state summary changed", ErrPublishing)
	}
	buildRequest := BuildRequest{
		Root: runtime.relationshipRoot(), Catalog: snapshot.catalog, States: snapshot.states,
		Resolver: resolverPublication, RPC: rpcPublication, Kafka: kafkaPublication,
	}
	var stage *Prepared
	if isPartitionedBinding(binding.Schema) {
		stage, err = BuildV2(ctx, BuildRequestV2{
			BuildRequest: buildRequest, Upstream: *binding.Upstream,
			ServiceSummary: snapshot.summary, Prior: priorRelationship,
		})
	} else {
		stage, err = Build(ctx, buildRequest)
	}
	if err != nil {
		return fmt.Errorf("build relationship root: %w", err)
	}

	runtime.transition.Lock()
	defer runtime.transition.Unlock()
	if err := runtime.fence(ctx, binding, snapshot.summary); err != nil {
		return err
	}
	if isPartitionedBinding(binding.Schema) {
		owner := "relationship:" + stage.Root().GenerationDigest
		pinned := make([]string, 0, len(binding.Upstream.Domains))
		for _, domain := range binding.Upstream.Domains {
			if err := runtime.Store.PinPartitionedExtractionRun(ctx, domain.RunID, owner); err != nil {
				for _, runID := range pinned {
					_ = runtime.Store.UnpinPartitionedExtractionRun(ctx, runID, owner)
				}
				return fmt.Errorf("pin relationship extraction root: %w", err)
			}
			pinned = append(pinned, domain.RunID)
		}
		if _, err := stage.Publish(ctx); err != nil {
			for _, runID := range pinned {
				_ = runtime.Store.UnpinPartitionedExtractionRun(ctx, runID, owner)
			}
			return fmt.Errorf("publish relationship root: %w", err)
		}
		return nil
	}
	if _, err := stage.Publish(ctx); err != nil {
		return fmt.Errorf("publish relationship root: %w", err)
	}
	return nil
}

// relationshipBuildFailure closes a deterministic component bound overrun as a
// terminal typed refusal so it cannot burn retry attempts or chain recovery
// schedules. A measured limit (pipelinerefusal.Measurement in the chain)
// retains its exact scalars through At; a bare limit closes as unknown.
func relationshipBuildFailure(
	err error,
	action string,
	limit error,
	stage pipelinerefusal.Stage,
) error {
	if err == nil {
		return nil
	}
	cause := fmt.Errorf("%s: %w", action, err)
	if errors.Is(err, limit) {
		return store.WithTerminal(pipelinerefusal.At(
			cause, stage, pipelinerefusal.GenerationRelationship,
		))
	}
	return cause
}

func (runtime *Runtime) validate(repository string) error {
	if runtime == nil || !filepath.IsAbs(runtime.DataDir) || runtime.Store == nil ||
		runtime.Cache == nil || runtime.Acquire == nil || reponame.Validate(repository) != nil {
		return fmt.Errorf("%w: relationship runtime configuration", ErrInvalid)
	}
	return nil
}

func (runtime *Runtime) acquireMutation(ctx context.Context) (func(), error) {
	return acquireMutationWithPolicy(ctx, runtime.Acquire, defaultMutationAcquirePolicy())
}

func acquireMutationWithPolicy(
	ctx context.Context,
	acquire func(context.Context) (func(), error),
	policy mutationAcquirePolicy,
) (func(), error) {
	if ctx == nil || acquire == nil || policy.timeout <= 0 || policy.probe <= 0 ||
		policy.probe > policy.timeout || policy.retryDelay <= 0 {
		return nil, fmt.Errorf("%w: relationship mutation acquisition", ErrInvalid)
	}
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, policy.timeout)
	defer cancelAcquire()
	for {
		probeCtx, cancelProbe := context.WithTimeout(acquireCtx, policy.probe)
		release, err := acquire(probeCtx)
		probeErr := probeCtx.Err()
		cancelProbe()
		if err == nil {
			if release == nil {
				return nil, fmt.Errorf("%w: relationship mutation lock returned no release", ErrInvalid)
			}
			return release, nil
		}
		if release != nil {
			release()
			return nil, fmt.Errorf("%w: relationship mutation lock returned release with error", ErrInvalid)
		}
		if acquireCtx.Err() != nil {
			return nil, fmt.Errorf("acquire relationship mutation lock: %w", acquireCtx.Err())
		}
		if probeErr == nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("acquire relationship mutation lock: %w", err)
		}
		retry := time.NewTimer(policy.retryDelay)
		select {
		case <-acquireCtx.Done():
			retry.Stop()
			return nil, fmt.Errorf("acquire relationship mutation lock: %w", acquireCtx.Err())
		case <-retry.C:
		}
	}
}

func (runtime *Runtime) relationshipRoot() string {
	return filepath.Join(runtime.DataDir, "relationships")
}

func (runtime *Runtime) resolverNamespaceRoot() string {
	return filepath.Join(runtime.DataDir, "relationship-resolver-namespaces")
}

func (runtime *Runtime) rpcRoot() string {
	return filepath.Join(runtime.DataDir, "relationship-rpc-postings")
}

func (runtime *Runtime) kafkaRoot() string {
	return filepath.Join(runtime.DataDir, "relationship-kafka-postings")
}

func (runtime *Runtime) ensureBuildRoots() error {
	for _, root := range []string{
		runtime.relationshipRoot(), runtime.resolverNamespaceRoot(),
		runtime.rpcRoot(), runtime.kafkaRoot(),
	} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		if err := validateDirectory(root); err != nil {
			return err
		}
	}
	return nil
}

func resolverProtocols(state resolvercatalog.State) []resolvermaterialize.Protocol {
	values := make([]resolvermaterialize.Protocol, 0, 2)
	for _, candidate := range []struct {
		protocol resolvermaterialize.Protocol
		pack     string
	}{
		{resolvermaterialize.ProtocolGRPC, resolvermaterialize.GRPCGeneratedPackName},
		{resolvermaterialize.ProtocolThrift, resolvermaterialize.ThriftGeneratedPackName},
	} {
		if slices.Contains(state.ResolverPacks, resolvercatalog.ResolverPack{
			Name: candidate.pack, Version: resolvermaterialize.PackVersion,
		}) {
			values = append(values, candidate.protocol)
		}
	}
	return values
}

func (runtime *Runtime) loadServiceSnapshot(
	ctx context.Context, repository string,
) (runtimeSnapshot, error) {
	var result runtimeSnapshot
	position := store.ServiceStatePosition{}
	for {
		page, err := runtime.Store.ListServiceStates(
			ctx, repository, store.ServiceStateFilter{}, position,
			store.MaxServiceStateReadPage,
		)
		if err != nil {
			return result, fmt.Errorf("load relationship service states: %w", err)
		}
		if page == nil {
			return result, fmt.Errorf("%w: nil service state page", ErrInvalid)
		}
		if result.catalog.GenerationDigest == "" {
			result.catalog = page.Publication
			result.summary = page.Summary
		} else if !sameCatalogFence(result.catalog, page.Publication) ||
			!sameSummaryFence(result.summary, page.Summary) {
			return result, fmt.Errorf("%w: service state snapshot changed", ErrPublishing)
		}
		for _, entry := range page.Entries {
			if entry.State.Disposition == servicecatalog.DispositionAccepted && !entry.State.Removed {
				if len(result.states) >= MaxServices {
					return result, ErrLimit
				}
				result.states = append(result.states, entry.State)
			}
		}
		if page.Continuation == nil {
			break
		}
		if page.Continuation.ServiceKey <= position.ServiceKey {
			return result, fmt.Errorf("%w: service state cursor did not advance", ErrInvalid)
		}
		position = *page.Continuation
	}
	if result.catalog.GenerationDigest == "" ||
		servicecatalog.ValidatePublication(result.catalog, true) != nil ||
		servicecatalog.ValidateRepositoryState(result.summary, true) != nil {
		return result, fmt.Errorf("%w: service state snapshot", ErrInvalid)
	}
	if err := runtime.Store.ConfirmServiceStateSnapshot(ctx, repository, result.summary); err != nil {
		return result, fmt.Errorf("confirm relationship service states: %w", err)
	}
	return result, nil
}

func sameCatalogFence(left, right servicecatalog.Publication) bool {
	return left.Repository == right.Repository &&
		left.GenerationDigest == right.GenerationDigest &&
		left.CatalogDigest == right.CatalogDigest &&
		left.ControlRevision == right.ControlRevision
}

func sameSummaryFence(left, right servicecatalog.RepositoryState) bool {
	return left.Repository == right.Repository &&
		left.CatalogGeneration == right.CatalogGeneration &&
		left.SummaryDigest == right.SummaryDigest &&
		left.ControlRevision == right.ControlRevision
}

func (runtime *Runtime) fence(
	ctx context.Context, binding scheduleBinding, summary servicecatalog.RepositoryState,
) error {
	if isPartitionedBinding(binding.Schema) {
		if summary.SummaryDigest != binding.ServiceStateSummary ||
			summary.ControlRevision != binding.ServiceStateRevision {
			return fmt.Errorf("%w: service-state summary fence", ErrPublishing)
		}
		current, err := runtime.currentUpstreamAuthority(ctx, binding.Repository)
		if err != nil || binding.Upstream == nil || current.Digest != binding.Upstream.Digest ||
			downstreamauthority.RequireUsable(current) != nil {
			return fmt.Errorf("%w: upstream v2 fence", ErrPublishing)
		}
	} else {
		currentObservation, err := observationpublication.CurrentGeneration(
			filepath.Join(runtime.DataDir, "observations"), binding.Repository,
		)
		if err != nil || currentObservation != binding.ObservationGeneration {
			return fmt.Errorf("%w: observation fence", ErrPublishing)
		}
	}
	resolver, err := runtime.Store.GetResolverCatalogPublication(ctx, binding.Repository)
	if err != nil || resolver == nil || resolver.GenerationDigest != binding.ResolverGeneration {
		return fmt.Errorf("%w: resolver fence", ErrPublishing)
	}
	currentResolver, err := runtime.Store.ResolverCatalogPublicationCurrent(ctx, *resolver)
	if err != nil || !currentResolver {
		return fmt.Errorf("%w: resolver fence", ErrPublishing)
	}
	if err := runtime.Store.ConfirmServiceStateSnapshot(ctx, binding.Repository, summary); err != nil {
		return fmt.Errorf("%w: service-state fence: %v", ErrPublishing, err)
	}
	schedule, err := runtime.Store.GetGenerationSchedule(ctx, binding.Repository, ScheduleStage)
	if err != nil || schedule == nil || schedule.Generation != binding.ScheduleGeneration ||
		schedule.Status != store.GenerationScheduleActive {
		return fmt.Errorf("%w: schedule fence", ErrPublishing)
	}
	return nil
}

func (runtime *Runtime) currentUpstreamAuthority(
	ctx context.Context, repository string,
) (downstreamauthority.Authority, error) {
	observation, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		ctx, filepath.Join(runtime.DataDir, "observations"), repository,
	)
	if err != nil {
		return downstreamauthority.Authority{}, err
	}
	domains := make([]candidate.DownstreamDomainAuthority, 0, len(runtime.Domains))
	for _, required := range runtime.Domains {
		domain, domainErr := extractionpublication.CurrentDomainAuthority(
			ctx, runtime.Store, repository, required.Domain,
		)
		if errors.Is(domainErr, store.ErrNotFound) {
			continue
		}
		if domainErr != nil {
			return downstreamauthority.Authority{}, domainErr
		}
		if !domainMatchesObservation(required, domain, observation) {
			continue
		}
		domains = append(domains, domain)
	}
	return downstreamauthority.BuildRequired(observation, runtime.Domains, domains)
}

func domainMatchesObservation(
	required downstreamauthority.DomainIdentity,
	domain candidate.DownstreamDomainAuthority,
	observation observationpublication.DownstreamAuthority,
) bool {
	return domain.Domain == required.Domain && domain.Version == required.Version &&
		domain.SourceGenerationDigest == observation.SourceGenerationDigest &&
		domain.ObservationGenerationDigest == observation.ObservationGenerationDigest
}

func runtimeTarget(
	repository, observation, catalog, serviceStateSet, resolver string,
) (string, error) {
	if reponame.Validate(repository) != nil || !validDigest(observation) ||
		!validDigest(catalog) || !validDigest(serviceStateSet) || !validDigest(resolver) {
		return "", fmt.Errorf("%w: relationship schedule authority", ErrInvalid)
	}
	return digestValue(struct {
		Domain      string `json:"domain"`
		Repository  string `json:"repository"`
		Observation string `json:"observation"`
		Catalog     string `json:"catalog"`
		ServiceSet  string `json:"service_state_set"`
		Resolver    string `json:"resolver"`
	}{
		Domain: "phebs-relationship-schedule-target-v1", Repository: repository,
		Observation: observation, Catalog: catalog,
		ServiceSet: serviceStateSet, Resolver: resolver,
	})
}

func runtimeTargetV2(
	repository, upstream, catalog, serviceStateSet, resolver, serviceSummary string,
	serviceRevision uint64,
) (string, error) {
	if reponame.Validate(repository) != nil || !validDigest(upstream) ||
		!validDigest(catalog) || !validDigest(serviceStateSet) || !validDigest(resolver) ||
		!validDigest(serviceSummary) || serviceRevision == 0 {
		return "", fmt.Errorf("%w: relationship v2 schedule authority", ErrInvalid)
	}
	return digestValue(struct {
		Domain     string `json:"domain"`
		Repository string `json:"repository"`
		Upstream   string `json:"upstream"`
		Catalog    string `json:"catalog"`
		ServiceSet string `json:"service_state_set"`
		Summary    string `json:"service_state_summary"`
		Revision   uint64 `json:"service_state_revision"`
		Resolver   string `json:"resolver"`
	}{
		Domain: "phebs-relationship-schedule-target-v2", Repository: repository,
		Upstream: upstream, Catalog: catalog, ServiceSet: serviceStateSet,
		Summary: serviceSummary, Revision: serviceRevision, Resolver: resolver,
	})
}

// runtimeBuildPolicyDigest binds the schedule target to every frozen builder
// policy and to the three operational resident fences applied by Handle. A
// policy-only change therefore supersedes a settled terminal schedule without
// pretending that unchanged source authority is a new generation. Every input
// is a process constant, so the digest is computed once.
var runtimeBuildPolicyDigest = sync.OnceValues(func() (string, error) {
	return digestValue(struct {
		Domain           string                   `json:"domain"`
		Resolver         resolvernamespace.Policy `json:"resolver"`
		RPC              rpccallerposting.Policy  `json:"rpc"`
		Kafka            kafkatopicposting.Policy `json:"kafka"`
		Relationship     Policy                   `json:"relationship"`
		ResolverResident int64                    `json:"resolver_resident_bytes"`
		RPCResident      int64                    `json:"rpc_resident_bytes"`
		KafkaResident    int64                    `json:"kafka_resident_bytes"`
	}{
		Domain:   "phebs-relationship-build-policy-v1",
		Resolver: resolvernamespace.FrozenPolicy(), RPC: rpccallerposting.FrozenPolicy(),
		Kafka: kafkatopicposting.FrozenPolicy(), Relationship: FrozenPolicy(),
		ResolverResident: ResolverResidentLimit, RPCResident: RPCResidentLimit,
		KafkaResident: KafkaResidentLimit,
	})
})

func runtimeTargetV3(
	repository, upstream, catalog, serviceStateSet, resolver, serviceSummary string,
	serviceRevision uint64,
	buildPolicy string,
) (string, error) {
	if reponame.Validate(repository) != nil || !validDigest(upstream) ||
		!validDigest(catalog) || !validDigest(serviceStateSet) || !validDigest(resolver) ||
		!validDigest(serviceSummary) || serviceRevision == 0 || !validDigest(buildPolicy) {
		return "", fmt.Errorf("%w: relationship v3 schedule authority", ErrInvalid)
	}
	return digestValue(struct {
		Domain      string `json:"domain"`
		Repository  string `json:"repository"`
		Upstream    string `json:"upstream"`
		Catalog     string `json:"catalog"`
		ServiceSet  string `json:"service_state_set"`
		Summary     string `json:"service_state_summary"`
		Revision    uint64 `json:"service_state_revision"`
		Resolver    string `json:"resolver"`
		BuildPolicy string `json:"build_policy_digest"`
	}{
		Domain: "phebs-relationship-schedule-target-v3", Repository: repository,
		Upstream: upstream, Catalog: catalog, ServiceSet: serviceStateSet,
		Summary: serviceSummary, Revision: serviceRevision, Resolver: resolver,
		BuildPolicy: buildPolicy,
	})
}

func (runtime *Runtime) scheduleIdentity(
	ctx context.Context, repository, target, legacyTarget string,
) (string, string, bool, error) {
	current, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if errors.Is(err, store.ErrNotFound) {
		return target, "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if current == nil {
		return "", "", false, fmt.Errorf("%w: nil relationship schedule", ErrInvalid)
	}
	if binding, bindingErr := runtime.readBinding(repository, current.Generation); bindingErr == nil {
		if current.Status == store.GenerationScheduleActive &&
			binding.TargetGeneration == target {
			return current.Generation, binding.PriorScheduleDigest, false, nil
		}
		// An active pre-upgrade schedule whose binding matches the legacy
		// (V2) target is allowed to finish; superseding it would restart an
		// in-flight build whose authority inputs did not change. It reports
		// no-work (like a retained terminal) so the caller does not rewrite
		// the existing binding under the new schema mid-flight.
		if current.Status == store.GenerationScheduleActive &&
			legacyTarget != "" && binding.TargetGeneration == legacyTarget {
			return current.Generation, binding.PriorScheduleDigest, true, nil
		}
		// Terminal retention requires a V3 binding: only the V3 target embeds
		// the build-policy digest, so only there does a policy or bound raise
		// mint a distinct recovery target. Older bindings keep the recovery
		// path so a raised bound can still rebuild them.
		if binding.TargetGeneration == target && binding.Schema == BindingSchemaV3 &&
			current.Status == store.GenerationScheduleSettled && current.Failed > 0 {
			failure, failureErr := runtime.Store.GetGenerationScheduleFailure(
				ctx, repository, ScheduleStage, current.Digest,
			)
			if failureErr != nil {
				return "", "", false, failureErr
			}
			if closedRelationshipFailure(failure) {
				return current.Generation, binding.PriorScheduleDigest, true, nil
			}
		}
	}
	recovery, err := digestValue(struct {
		Domain string `json:"domain"`
		Target string `json:"target"`
		Prior  string `json:"prior"`
	}{
		Domain: "phebs-relationship-recovery-schedule-v1",
		Target: target, Prior: current.Digest,
	})
	return recovery, current.Digest, false, err
}

func closedRelationshipFailure(failure *store.GenerationScheduleFailure) bool {
	if failure == nil || failure.Refusal == nil ||
		pipelinerefusal.Validate(*failure.Refusal) != nil {
		return false
	}
	// Validate already pins every relationship-kind refusal to a relationship
	// stage, so the generation kind alone identifies a closed build refusal.
	return failure.Refusal.GenerationKind == pipelinerefusal.GenerationRelationship
}

func setBindingDigest(value *scheduleBinding) error {
	if value == nil {
		return fmt.Errorf("%w: nil schedule binding", ErrInvalid)
	}
	copyValue := *value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil {
		return err
	}
	value.Digest = digest
	return validateBinding(*value)
}

func isPartitionedBinding(schema string) bool {
	return schema == BindingSchemaV2 || schema == BindingSchemaV3
}

func validateBinding(value scheduleBinding) error {
	if (value.Schema != BindingSchema && value.Schema != BindingSchemaV2 &&
		value.Schema != BindingSchemaV3) ||
		reponame.Validate(value.Repository) != nil ||
		!validDigest(value.ScheduleGeneration) || !validDigest(value.TargetGeneration) ||
		!validDigest(value.ObservationGeneration) || !validDigest(value.CatalogGeneration) ||
		!validDigest(value.ServiceStateSet) ||
		!validDigest(value.ResolverGeneration) ||
		(value.Schema == BindingSchemaV3 && !validDigest(value.BuildPolicyDigest)) ||
		(value.Schema != BindingSchemaV3 && value.BuildPolicyDigest != "") ||
		(value.PriorScheduleDigest != "" && !validDigest(value.PriorScheduleDigest)) {
		return fmt.Errorf("%w: schedule binding", ErrInvalid)
	}
	var want string
	var err error
	if isPartitionedBinding(value.Schema) {
		if value.Upstream == nil || downstreamauthority.RequireUsable(*value.Upstream) != nil ||
			value.Upstream.Repository != value.Repository ||
			value.Upstream.Observation.ObservationGenerationDigest != value.ObservationGeneration {
			return fmt.Errorf("%w: schedule v2 upstream", ErrInvalid)
		}
		if value.Schema == BindingSchemaV3 {
			// The binding validates against its own recorded policy digest
			// (the target recomputation below fences tampering); pinning to
			// the current process policy would make every in-flight binding
			// unreadable across a policy-changing deploy, burning attempts on
			// ErrInvalid instead of superseding cleanly by target mismatch.
			want, err = runtimeTargetV3(
				value.Repository, value.Upstream.Digest,
				value.CatalogGeneration, value.ServiceStateSet, value.ResolverGeneration,
				value.ServiceStateSummary, value.ServiceStateRevision,
				value.BuildPolicyDigest,
			)
		} else {
			want, err = runtimeTargetV2(
				value.Repository, value.Upstream.Digest,
				value.CatalogGeneration, value.ServiceStateSet, value.ResolverGeneration,
				value.ServiceStateSummary, value.ServiceStateRevision,
			)
		}
	} else {
		if value.Upstream != nil || value.ServiceStateSummary != "" || value.ServiceStateRevision != 0 {
			return fmt.Errorf("%w: schedule v1 upstream", ErrInvalid)
		}
		want, err = runtimeTarget(
			value.Repository, value.ObservationGeneration,
			value.CatalogGeneration, value.ServiceStateSet, value.ResolverGeneration,
		)
	}
	if err != nil || want != value.TargetGeneration {
		return fmt.Errorf("%w: schedule target", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: schedule binding digest", ErrInvalid)
	}
	return nil
}

func (runtime *Runtime) bindingDirectory(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return filepath.Join(runtime.DataDir, "relationship-schedules", hex.EncodeToString(sum[:]))
}

func (runtime *Runtime) bindingPath(repository, generation string) string {
	return filepath.Join(
		runtime.bindingDirectory(repository), strings.TrimPrefix(generation, "sha256:")+".json",
	)
}

func (runtime *Runtime) writeBinding(value scheduleBinding) error {
	if err := validateBinding(value); err != nil {
		return err
	}
	directory := runtime.bindingDirectory(value.Repository)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := validateDirectory(directory); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	path := runtime.bindingPath(value.Repository, value.ScheduleGeneration)
	if err := writeExclusive(path, raw); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := readRegular(path, MaxRootBytes)
		if readErr != nil || !bytes.Equal(existing, raw) {
			return fmt.Errorf("%w: schedule binding collision", ErrInvalid)
		}
	}
	return syncDirectory(directory)
}

func (runtime *Runtime) readBinding(repository, generation string) (scheduleBinding, error) {
	var value scheduleBinding
	if reponame.Validate(repository) != nil || !validDigest(generation) {
		return value, fmt.Errorf("%w: schedule binding key", ErrInvalid)
	}
	raw, err := readRegular(runtime.bindingPath(repository, generation), MaxRootBytes)
	if err != nil {
		return value, err
	}
	if err := decodeExact(raw, MaxRootBytes, &value); err != nil ||
		validateBinding(value) != nil || value.Repository != repository ||
		value.ScheduleGeneration != generation {
		return value, fmt.Errorf("%w: schedule binding", ErrInvalid)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return value, fmt.Errorf("%w: noncanonical schedule binding", ErrInvalid)
	}
	return value, nil
}
