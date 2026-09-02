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

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	// ScheduleStageV3 is deliberately separate from the selected v2 worker
	// stage. Building the shadow publication cannot consume or relabel v2 work.
	ScheduleStageV3 = store.ServiceRelationshipV3ScheduleStage

	runtimeBindingSchemaV3 = "phebs-relationship-v3-shadow-schedule-binding-v1"
)

type runtimeV3Store interface {
	GetServiceCatalogV3CandidatePointer(
		context.Context,
		string,
	) (store.ServiceCatalogV3Pointer, error)
	GetServiceCatalogV3Candidate(
		context.Context,
		string,
	) (*store.ServiceCatalogV3Candidate, error)
	GetServiceStateV3SummaryPoint(
		context.Context,
		string,
	) (servicecatalog.RepositoryState, error)
	ListAcceptedServiceStateV3Rows(
		context.Context,
		string,
		int,
	) ([]servicecatalog.ServiceState, error)
	ConfirmServiceStateV3Snapshot(
		context.Context,
		store.ServiceCatalogV3Pointer,
		servicecatalog.RepositoryState,
	) error
	PublishPinStoreV3
}

type runtimeBindingV3 struct {
	Schema              string `json:"schema"`
	Repository          string `json:"repository"`
	ScheduleGeneration  string `json:"schedule_generation"`
	TargetGeneration    string `json:"target_generation"`
	PriorScheduleDigest string `json:"prior_schedule_digest,omitempty"`
	SourceGeneration    string `json:"source_generation"`
	SourceRoot          string `json:"source_root"`
	CatalogRoot         string `json:"catalog_root"`
	CatalogRevision     uint64 `json:"catalog_revision"`
	StateSummary        string `json:"state_summary"`
	StateRevision       uint64 `json:"state_revision"`
	PolicyDigest        string `json:"policy_digest"`
	Digest              string `json:"digest"`
}

type runtimeAuthorityV3 struct {
	pointer store.ServiceCatalogV3Pointer
	summary servicecatalog.RepositoryState
}

type runtimeBuildSnapshotV3 struct {
	authority runtimeAuthorityV3
	catalog   servicecatalogv3.Generation
	states    []servicecatalog.ServiceState
}

// ReconcileV3 binds the selected v2 relationship's immutable component roots
// and the strict-current v3 catalog/state authority to one shadow build chunk.
// It does not rebuild or move any v2 component or relationship pointer. The
// result is true only when the exact target is already current; false with no
// error means a durable schedule or terminal outcome owns the continuation.
func (runtime *Runtime) ReconcileV3(ctx context.Context, repository string) (bool, error) {
	state, err := runtime.validateV3(repository)
	if err != nil {
		return false, err
	}
	source, sourceRoot, err := runtime.openCurrentV2Source(ctx, repository)
	if err != nil {
		return false, err
	}
	authority, err := loadRuntimeAuthorityV3(ctx, state, repository)
	if err != nil {
		return false, err
	}
	if err := source.ConfirmCurrent(); err != nil {
		return false, fmt.Errorf("%w: relationship v2 source changed", ErrPublishing)
	}
	if current, openErr := OpenCurrentV3(ctx, runtime.relationshipRoot(), repository); openErr == nil {
		if matchesRuntimeAuthorityV3(current.Root(), sourceRoot, authority) {
			if err := current.ConfirmCurrent(); err != nil {
				return false, fmt.Errorf("%w: relationship v3 current changed", ErrPublishing)
			}
			return true, nil
		}
	} else if !errors.Is(openErr, ErrNotFound) {
		return false, fmt.Errorf("open current relationship v3: %w", openErr)
	}
	if runtime.Admit != nil {
		if err := runtime.Admit(ctx); err != nil {
			return false, err
		}
	}
	policyDigest, err := digestValue(FrozenPolicyV3())
	if err != nil {
		return false, err
	}
	target, err := runtimeTargetShadowV3(
		repository, sourceRoot.GenerationDigest, sourceRoot.Digest,
		authority.pointer.RootDigest, authority.pointer.ControlRevision,
		authority.summary.SummaryDigest, authority.summary.ControlRevision,
		policyDigest,
	)
	if err != nil {
		return false, err
	}
	scheduleGeneration, priorDigest, terminal, err := runtime.scheduleIdentityShadowV3(
		ctx, repository, target,
	)
	if err != nil || terminal {
		return false, err
	}
	for collision := 0; ; collision++ {
		binding := runtimeBindingV3{
			Schema: runtimeBindingSchemaV3, Repository: repository,
			ScheduleGeneration: scheduleGeneration, TargetGeneration: target,
			PriorScheduleDigest: priorDigest,
			SourceGeneration:    sourceRoot.GenerationDigest, SourceRoot: sourceRoot.Digest,
			CatalogRoot: authority.pointer.RootDigest, CatalogRevision: authority.pointer.ControlRevision,
			StateSummary: authority.summary.SummaryDigest, StateRevision: authority.summary.ControlRevision,
			PolicyDigest: policyDigest,
		}
		if err := setRuntimeBindingDigestV3(&binding); err != nil {
			return false, err
		}
		if err := runtime.writeRuntimeBindingV3(binding); err != nil {
			return false, err
		}
		spec := store.GenerationScheduleSpec{
			Repository: repository, Stage: ScheduleStageV3, Generation: scheduleGeneration,
			ResourceClass: store.GenerationResourceMemory, TotalItems: 1, ChunkItems: 1,
			MaxAttempts: ScheduleMaxAttempts, RepositoryTokens: ScheduleRepositoryTokens,
		}
		_, err = runtime.Store.EnqueueGenerationSchedule(ctx, spec)
		if !errors.Is(err, store.ErrGenerationStale) {
			if err != nil {
				return false, fmt.Errorf("enqueue relationship v3 schedule: %w", err)
			}
			return false, nil
		}
		if collision >= store.MaxGenerationAttempts {
			return false, fmt.Errorf(
				"enqueue relationship v3 schedule: %w", store.ErrGenerationExhausted,
			)
		}
		priorDigest, err = store.GenerationScheduleDigest(spec)
		if err != nil {
			return false, err
		}
		scheduleGeneration, err = recoveryScheduleGenerationShadowV3(
			target, priorDigest,
		)
		if err != nil {
			return false, err
		}
	}
}

// HandleV3 reuses the exact resolver/RPC/Kafka generations already selected
// by the current v2 relationship and publishes only the separate v3 shadow
// root. The existing mutation lock covers every authority read through commit.
func (runtime *Runtime) HandleV3(
	ctx context.Context,
	chunk store.GenerationChunk,
) (retErr error) {
	state, err := runtime.validateV3(chunk.Repository)
	if err != nil || chunk.Stage != ScheduleStageV3 || chunk.Offset != 0 ||
		chunk.Length != 1 || !validDigest(chunk.Generation) {
		return fmt.Errorf("%w: relationship v3 runtime chunk", ErrInvalid)
	}
	binding, err := runtime.readRuntimeBindingV3(chunk.Repository, chunk.Generation)
	if err != nil {
		return err
	}
	if err := runtime.ensureBuildRoots(); err != nil {
		return err
	}
	release, err := runtime.acquireMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := runtime.fenceScheduleV3(ctx, binding); err != nil {
		return err
	}
	source, sourceRoot, err := runtime.openCurrentV2Source(ctx, chunk.Repository)
	if err != nil {
		return err
	}
	if sourceRoot.GenerationDigest != binding.SourceGeneration ||
		sourceRoot.Digest != binding.SourceRoot {
		return fmt.Errorf("%w: relationship v2 source changed", ErrPublishing)
	}
	resolver, err := runtime.openBoundResolverV3(ctx, sourceRoot)
	if err != nil {
		return err
	}
	authority := sourceRoot.Authority
	rpc, err := rpccallerposting.OpenGeneration(
		ctx, runtime.rpcRoot(), chunk.Repository,
		authority.RPCGenerationDigest, authority.RPCRootDigest,
	)
	if err != nil {
		return fmt.Errorf("open relationship v3 RPC source: %w", err)
	}
	kafka, err := kafkatopicposting.OpenGeneration(
		ctx, runtime.kafkaRoot(), chunk.Repository,
		authority.KafkaGenerationDigest, authority.KafkaRootDigest,
	)
	if err != nil {
		return fmt.Errorf("open relationship v3 Kafka source: %w", err)
	}
	snapshot, err := loadRuntimeBuildSnapshotV3(ctx, state, chunk.Repository)
	if err != nil {
		return err
	}
	if !bindingMatchesRuntimeAuthorityV3(binding, snapshot.authority) {
		return fmt.Errorf("%w: relationship v3 catalog/state changed", ErrPublishing)
	}
	var prior *PublicationV3
	prior, err = OpenCurrentV3(ctx, runtime.relationshipRoot(), chunk.Repository)
	if errors.Is(err, ErrNotFound) {
		prior, err = nil, nil
	}
	if err != nil {
		return fmt.Errorf("open prior relationship v3: %w", err)
	}
	prepared, err := BuildV3(ctx, BuildRequestV3{
		Root: runtime.relationshipRoot(), Catalog: snapshot.catalog,
		States: snapshot.states, ServiceSummary: snapshot.authority.summary,
		Resolver: resolver, RPC: rpc, Kafka: kafka,
		Upstream: *sourceRoot.Authority.Upstream, Prior: prior,
	})
	if err != nil {
		return fmt.Errorf("build relationship v3 root: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, prepared.abort()) }()

	runtime.transition.Lock()
	defer runtime.transition.Unlock()
	if err := source.ConfirmCurrent(); err != nil {
		return fmt.Errorf("%w: relationship v2 source fence", ErrPublishing)
	}
	if err := runtime.confirmBoundResolverV3(ctx, sourceRoot); err != nil {
		return err
	}
	if err := state.ConfirmServiceStateV3Snapshot(
		ctx, snapshot.authority.pointer, snapshot.authority.summary,
	); err != nil {
		return runtimeStateFenceErrorV3("relationship v3 catalog/state fence", err)
	}
	if err := runtime.fenceScheduleV3(ctx, binding); err != nil {
		return err
	}
	if _, err := PublishV3(ctx, prepared, state); err != nil {
		return fmt.Errorf("publish relationship v3 root: %w", err)
	}
	return nil
}

func (runtime *Runtime) validateV3(repository string) (runtimeV3Store, error) {
	if runtime == nil || !filepath.IsAbs(runtime.DataDir) || runtime.Store == nil ||
		runtime.Acquire == nil || reponame.Validate(repository) != nil {
		return nil, fmt.Errorf("%w: relationship v3 runtime configuration", ErrInvalid)
	}
	state, ok := runtime.Store.(runtimeV3Store)
	if !ok {
		return nil, fmt.Errorf("%w: relationship v3 store", ErrInvalid)
	}
	return state, nil
}

func (runtime *Runtime) openCurrentV2Source(
	ctx context.Context,
	repository string,
) (*Publication, Root, error) {
	publication, err := OpenCurrent(ctx, runtime.relationshipRoot(), repository)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, Root{}, fmt.Errorf("%w: relationship v2 source", ErrNotFound)
		}
		return nil, Root{}, fmt.Errorf("open relationship v2 source: %w", err)
	}
	root := publication.Root()
	if root.Schema != RootSchemaV2 || root.Authority.Upstream == nil ||
		downstreamauthority.RequireUsable(*root.Authority.Upstream) != nil {
		return nil, Root{}, fmt.Errorf("%w: relationship v2 source authority", ErrInvalid)
	}
	return publication, root, nil
}

func loadRuntimeAuthorityV3(
	ctx context.Context,
	state runtimeV3Store,
	repository string,
) (runtimeAuthorityV3, error) {
	pointer, err := state.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		return runtimeAuthorityV3{}, fmt.Errorf("load relationship v3 catalog pointer: %w", err)
	}
	summary, err := state.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		return runtimeAuthorityV3{}, fmt.Errorf("load relationship v3 state summary: %w", err)
	}
	if servicecatalogv3.ValidateRepositoryState(summary, true) != nil ||
		pointer.Repository != repository || pointer.RootDigest != summary.CatalogGeneration ||
		pointer.ControlRevision == 0 || pointer.ControlRevision != summary.CatalogControlRevision ||
		summary.Repository != repository {
		return runtimeAuthorityV3{}, fmt.Errorf("%w: relationship v3 catalog/state authority", ErrInvalid)
	}
	if err := state.ConfirmServiceStateV3Snapshot(ctx, pointer, summary); err != nil {
		return runtimeAuthorityV3{}, runtimeStateFenceErrorV3(
			"relationship v3 catalog/state changed", err,
		)
	}
	return runtimeAuthorityV3{pointer: pointer, summary: summary}, nil
}

func loadRuntimeBuildSnapshotV3(
	ctx context.Context,
	state runtimeV3Store,
	repository string,
) (runtimeBuildSnapshotV3, error) {
	candidate, err := state.GetServiceCatalogV3Candidate(ctx, repository)
	if err != nil {
		return runtimeBuildSnapshotV3{}, fmt.Errorf("load relationship v3 catalog: %w", err)
	}
	if candidate == nil || servicecatalogv3.ValidateGeneration(candidate.Generation) != nil ||
		candidate.Generation.Root.Binding.Repository != repository || candidate.ControlRevision == 0 {
		return runtimeBuildSnapshotV3{}, fmt.Errorf("%w: relationship v3 catalog", ErrInvalid)
	}
	pointer := store.ServiceCatalogV3Pointer{
		Repository: repository, RootDigest: candidate.Generation.Root.Digest,
		ControlRevision: candidate.ControlRevision, PublishedAt: candidate.PublishedAt,
	}
	summary, err := state.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		return runtimeBuildSnapshotV3{}, fmt.Errorf("load relationship v3 state summary: %w", err)
	}
	states, err := state.ListAcceptedServiceStateV3Rows(ctx, repository, MaxServicesV3+1)
	if err != nil {
		return runtimeBuildSnapshotV3{}, fmt.Errorf("load relationship v3 accepted states: %w", err)
	}
	if len(states) > MaxServicesV3 {
		return runtimeBuildSnapshotV3{}, fmt.Errorf("%w: relationship v3 accepted states", ErrLimit)
	}
	if err := validateRuntimeStatesV3(repository, candidate.Generation.Root, summary, states); err != nil {
		return runtimeBuildSnapshotV3{}, err
	}
	if err := state.ConfirmServiceStateV3Snapshot(ctx, pointer, summary); err != nil {
		return runtimeBuildSnapshotV3{}, runtimeStateFenceErrorV3(
			"relationship v3 catalog/state changed", err,
		)
	}
	return runtimeBuildSnapshotV3{
		authority: runtimeAuthorityV3{pointer: pointer, summary: summary},
		catalog:   candidate.Generation, states: cloneRuntimeStatesV3(states),
	}, nil
}

func validateRuntimeStatesV3(
	repository string,
	root servicecatalogv3.Root,
	summary servicecatalog.RepositoryState,
	states []servicecatalog.ServiceState,
) error {
	if err := validateServiceSummaryV3(summary, root, len(states)); err != nil {
		return err
	}
	prior := ""
	for _, state := range states {
		if servicecatalogv3.ValidateServiceState(state, true) != nil ||
			state.Repository != repository || state.ServiceKey <= prior || state.Removed ||
			state.Disposition != servicecatalog.DispositionAccepted {
			return fmt.Errorf("%w: relationship v3 accepted state", ErrInvalid)
		}
		prior = state.ServiceKey
	}
	return nil
}

func cloneRuntimeStatesV3(values []servicecatalog.ServiceState) []servicecatalog.ServiceState {
	result := make([]servicecatalog.ServiceState, len(values))
	for index, value := range values {
		value.Successors = slices.Clone(value.Successors)
		result[index] = value
	}
	return result
}

func matchesRuntimeAuthorityV3(
	root RootV3,
	source Root,
	authority runtimeAuthorityV3,
) bool {
	value := root.Authority
	upstream := source.Authority.Upstream
	return root.Schema == RootSchemaV3 && upstream != nil &&
		value.Repository == source.Authority.Repository &&
		value.CatalogRootDigest == authority.pointer.RootDigest &&
		value.CatalogControlRevision == authority.pointer.ControlRevision &&
		value.ServiceStateSummaryDigest == authority.summary.SummaryDigest &&
		value.ServiceStateControlRevision == authority.summary.ControlRevision &&
		value.UpstreamDigest == upstream.Digest &&
		value.ResolverGenerationDigest == source.Authority.ResolverGenerationDigest &&
		value.ResolverRootDigest == source.Authority.ResolverRootDigest &&
		value.RPCGenerationDigest == source.Authority.RPCGenerationDigest &&
		value.RPCRootDigest == source.Authority.RPCRootDigest &&
		value.KafkaGenerationDigest == source.Authority.KafkaGenerationDigest &&
		value.KafkaRootDigest == source.Authority.KafkaRootDigest
}

func bindingMatchesRuntimeAuthorityV3(
	binding runtimeBindingV3,
	authority runtimeAuthorityV3,
) bool {
	return binding.CatalogRoot == authority.pointer.RootDigest &&
		binding.CatalogRevision == authority.pointer.ControlRevision &&
		binding.StateSummary == authority.summary.SummaryDigest &&
		binding.StateRevision == authority.summary.ControlRevision
}

func (runtime *Runtime) openBoundResolverV3(
	ctx context.Context,
	source Root,
) (*resolvernamespace.Publication, error) {
	resolver, err := resolvernamespace.OpenCurrent(
		ctx, runtime.resolverNamespaceRoot(), source.Authority.Repository,
	)
	if err != nil {
		return nil, fmt.Errorf("open relationship v3 resolver source: %w", err)
	}
	root := resolver.Root()
	if root.GenerationDigest != source.Authority.ResolverGenerationDigest ||
		root.Digest != source.Authority.ResolverRootDigest {
		return nil, fmt.Errorf("%w: relationship v3 resolver source changed", ErrPublishing)
	}
	return resolver, nil
}

func (runtime *Runtime) confirmBoundResolverV3(ctx context.Context, source Root) error {
	_, err := runtime.openBoundResolverV3(ctx, source)
	return err
}

func (runtime *Runtime) fenceScheduleV3(
	ctx context.Context,
	binding runtimeBindingV3,
) error {
	schedule, err := runtime.Store.GetGenerationSchedule(
		ctx, binding.Repository, ScheduleStageV3,
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: relationship v3 schedule fence", ErrPublishing)
		}
		return fmt.Errorf("read relationship v3 schedule fence: %w", err)
	}
	if schedule == nil || schedule.Generation != binding.ScheduleGeneration ||
		schedule.Status != store.GenerationScheduleActive {
		return fmt.Errorf("%w: relationship v3 schedule fence", ErrPublishing)
	}
	return nil
}

func runtimeStateFenceErrorV3(action string, err error) error {
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %s: %v", ErrPublishing, action, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func runtimeTargetShadowV3(
	repository, sourceGeneration, sourceRoot, catalogRoot string,
	catalogRevision uint64,
	stateSummary string,
	stateRevision uint64,
	policyDigest string,
) (string, error) {
	if reponame.Validate(repository) != nil || !validDigest(sourceGeneration) ||
		!validDigest(sourceRoot) || !validDigest(catalogRoot) || catalogRevision == 0 ||
		!validDigest(stateSummary) || stateRevision == 0 || !validDigest(policyDigest) {
		return "", fmt.Errorf("%w: relationship v3 runtime target", ErrInvalid)
	}
	return digestValue(struct {
		Domain           string `json:"domain"`
		Repository       string `json:"repository"`
		SourceGeneration string `json:"source_generation"`
		SourceRoot       string `json:"source_root"`
		CatalogRoot      string `json:"catalog_root"`
		CatalogRevision  uint64 `json:"catalog_revision"`
		StateSummary     string `json:"state_summary"`
		StateRevision    uint64 `json:"state_revision"`
		PolicyDigest     string `json:"policy_digest"`
	}{
		Domain:     "phebs-relationship-v3-shadow-schedule-target-v1",
		Repository: repository, SourceGeneration: sourceGeneration, SourceRoot: sourceRoot,
		CatalogRoot: catalogRoot, CatalogRevision: catalogRevision,
		StateSummary: stateSummary, StateRevision: stateRevision,
		PolicyDigest: policyDigest,
	})
}

func (runtime *Runtime) scheduleIdentityShadowV3(
	ctx context.Context,
	repository, target string,
) (string, string, bool, error) {
	current, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStageV3)
	if errors.Is(err, store.ErrNotFound) {
		return target, "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if current == nil {
		return "", "", false, fmt.Errorf("%w: nil relationship v3 schedule", ErrInvalid)
	}
	if binding, bindingErr := runtime.readRuntimeBindingV3(
		repository, current.Generation,
	); bindingErr == nil && binding.TargetGeneration == target {
		if current.Status == store.GenerationScheduleActive {
			return current.Generation, binding.PriorScheduleDigest, false, nil
		}
		if current.Status == store.GenerationScheduleSettled && current.Failed > 0 {
			failure, failureErr := runtime.Store.GetGenerationScheduleFailure(
				ctx, repository, ScheduleStageV3, current.Digest,
			)
			if failureErr != nil {
				return "", "", false, failureErr
			}
			if closedRelationshipFailure(failure) {
				return current.Generation, binding.PriorScheduleDigest, true, nil
			}
		}
	}
	recovery, err := recoveryScheduleGenerationShadowV3(target, current.Digest)
	return recovery, current.Digest, false, err
}

func recoveryScheduleGenerationShadowV3(target, prior string) (string, error) {
	return digestValue(struct {
		Domain string `json:"domain"`
		Target string `json:"target"`
		Prior  string `json:"prior"`
	}{
		Domain: "phebs-relationship-v3-shadow-recovery-schedule-v1",
		Target: target, Prior: prior,
	})
}

func setRuntimeBindingDigestV3(value *runtimeBindingV3) error {
	if value == nil {
		return fmt.Errorf("%w: nil relationship v3 binding", ErrInvalid)
	}
	copyValue := *value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil {
		return err
	}
	value.Digest = digest
	return validateRuntimeBindingV3(*value)
}

func validateRuntimeBindingV3(value runtimeBindingV3) error {
	if value.Schema != runtimeBindingSchemaV3 || reponame.Validate(value.Repository) != nil ||
		!validDigest(value.ScheduleGeneration) || !validDigest(value.TargetGeneration) ||
		(value.PriorScheduleDigest != "" && !validDigest(value.PriorScheduleDigest)) ||
		!validDigest(value.SourceGeneration) || !validDigest(value.SourceRoot) ||
		!validDigest(value.CatalogRoot) || value.CatalogRevision == 0 ||
		!validDigest(value.StateSummary) || value.StateRevision == 0 ||
		!validDigest(value.PolicyDigest) || !validDigest(value.Digest) {
		return fmt.Errorf("%w: relationship v3 schedule binding", ErrInvalid)
	}
	target, err := runtimeTargetShadowV3(
		value.Repository, value.SourceGeneration, value.SourceRoot,
		value.CatalogRoot, value.CatalogRevision,
		value.StateSummary, value.StateRevision, value.PolicyDigest,
	)
	if err != nil || target != value.TargetGeneration {
		return fmt.Errorf("%w: relationship v3 schedule target", ErrInvalid)
	}
	copyValue := value
	copyValue.Digest = ""
	digest, err := digestValue(copyValue)
	if err != nil || digest != value.Digest {
		return fmt.Errorf("%w: relationship v3 schedule binding digest", ErrInvalid)
	}
	return nil
}

func (runtime *Runtime) runtimeBindingDirectoryV3(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return filepath.Join(
		runtime.DataDir, "relationship-v3-schedules", hex.EncodeToString(sum[:]),
	)
}

func (runtime *Runtime) runtimeBindingPathV3(repository, generation string) string {
	return filepath.Join(
		runtime.runtimeBindingDirectoryV3(repository),
		strings.TrimPrefix(generation, "sha256:")+".json",
	)
}

func (runtime *Runtime) writeRuntimeBindingV3(value runtimeBindingV3) error {
	if err := validateRuntimeBindingV3(value); err != nil {
		return err
	}
	directory := runtime.runtimeBindingDirectoryV3(value.Repository)
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
	path := runtime.runtimeBindingPathV3(value.Repository, value.ScheduleGeneration)
	if err := writeExclusive(path, raw); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := readRegular(path, MaxRootBytesV3)
		if readErr != nil || !bytes.Equal(existing, raw) {
			return fmt.Errorf("%w: relationship v3 schedule binding collision", ErrInvalid)
		}
	}
	return syncDirectory(directory)
}

func (runtime *Runtime) readRuntimeBindingV3(
	repository, generation string,
) (runtimeBindingV3, error) {
	var value runtimeBindingV3
	if reponame.Validate(repository) != nil || !validDigest(generation) {
		return value, fmt.Errorf("%w: relationship v3 schedule binding key", ErrInvalid)
	}
	raw, err := readRegular(runtime.runtimeBindingPathV3(repository, generation), MaxRootBytesV3)
	if err != nil {
		return value, err
	}
	if err := decodeExact(raw, MaxRootBytesV3, &value); err != nil ||
		validateRuntimeBindingV3(value) != nil || value.Repository != repository ||
		value.ScheduleGeneration != generation {
		return value, fmt.Errorf("%w: relationship v3 schedule binding", ErrInvalid)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return value, fmt.Errorf("%w: noncanonical relationship v3 schedule binding", ErrInvalid)
	}
	return value, nil
}
