package extractionpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	RecoveryPreparationSchema       = "phebs-extraction-recovery-preparation-v1"
	RecoveryPreparationScheduleOnly = "schedule_only"
	RecoveryPreparationCheckpoint   = "checkpoint"
)

var ErrRecoveryPreparationDisabled = errors.New("extraction recovery preparation is disabled")

type RecoveryPreparationRoot struct {
	Domain     string `json:"domain"`
	PlanDigest string `json:"plan_digest"`
	RootDigest string `json:"root_digest"`
}

// RecoveryPreparationRequest must come from separately admitted ceremony
// controls. Roots are the complete, ordered generation inventory; a selected
// plan and ordinal identify the target without accepting any filesystem path.
type RecoveryPreparationRequest struct {
	Schema              string                    `json:"schema"`
	Authority           PlanningAuthority         `json:"authority"`
	GenerationDigest    string                    `json:"generation_digest"`
	PriorScheduleDigest string                    `json:"prior_schedule_digest"`
	Roots               []RecoveryPreparationRoot `json:"roots"`
	Mode                string                    `json:"mode"`
	TargetDomain        string                    `json:"target_domain"`
	TargetOrdinal       int                       `json:"target_ordinal"`
}

// PrepareRecovery creates one native predecessor-bound operational schedule
// for an actually completed generation. It never resets old jobs, publishes
// evidence, or changes immutable results. Call only on the live Reconciler;
// another instance does not share its locks. There is deliberately no ordinary
// caller or HTTP, CLI, environment, or background control transport.
//
// A non-nil error is a terminal preparation refusal, not permission to retry:
// filesystem mutations or enqueue may already have committed. No rollback is
// attempted. Locks are released before return and must never span recovery.
func (reconciler *Reconciler) PrepareRecovery(
	ctx context.Context,
	request RecoveryPreparationRequest,
) (*store.GenerationSchedule, error) {
	if reconciler == nil || !reconciler.RecoveryPreparationEnabled {
		return nil, ErrRecoveryPreparationDisabled
	}
	if ctx == nil || reconciler.Runtime == nil || reconciler.Runtime.validate() != nil ||
		reconciler.Root != reconciler.Runtime.Root || reconciler.Evidence == nil ||
		reconciler.CandidateReference == nil || reconciler.AuthorityReference == nil ||
		request.Schema != RecoveryPreparationSchema || validatePlanningAuthority(request.Authority) != nil ||
		!validDigest(request.GenerationDigest) || !validDigest(request.PriorScheduleDigest) ||
		len(request.Roots) == 0 || len(request.Roots) > MaxDomains ||
		(request.Mode != RecoveryPreparationScheduleOnly && request.Mode != RecoveryPreparationCheckpoint) ||
		!boundedIdentity(request.TargetDomain, 128) || request.TargetOrdinal < 0 {
		return nil, invalid("recovery preparation request")
	}
	request.Roots = slices.Clone(request.Roots)
	for _, root := range request.Roots {
		if !boundedIdentity(root.Domain, 128) || !validDigest(root.PlanDigest) || !validDigest(root.RootDigest) {
			return nil, invalid("recovery preparation root")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock := &reconciler.mu[reconcileShard(request.Authority.Repository)]
	if err := lockRecoveryPreparation(ctx, lock); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime := reconciler.Runtime
	repository := request.Authority.Repository
	for _, path := range []string{runtime.Root, runtime.repositoryDirectory(repository)} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.Join(err, invalid("recovery preparation namespace"))
		}
	}
	directory := runtime.generationDirectory(repository, request.GenerationDigest)
	generation, err := runtime.openGeneration(directory, repository, request.GenerationDigest)
	if err != nil {
		return nil, err
	}
	if len(generation.Domains) != len(request.Roots) || generation.WorkItems == 0 {
		return nil, ErrStale
	}
	// Re-derive the immutable v1 producer identity, not merely agreement among
	// caller-supplied names and mutable bindings/pointers. Keep the field order
	// and domain separator identical to Runtime.Reconcile's frozen v1 recipe.
	semantic := struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Plans      []string `json:"plans"`
	}{Schema: GenerationSchema, Repository: repository, Plans: make([]string, 0, len(generation.Domains))}
	for _, descriptor := range generation.Domains {
		semantic.Plans = append(semantic.Plans, descriptor.PlanDigest)
	}
	semanticRaw, _ := canonical(semantic)
	if digest(GenerationSchema, semanticRaw) != generation.Digest {
		return nil, ErrStale
	}
	target := -1
	for index, descriptor := range generation.Domains {
		if descriptor.Domain != request.Roots[index].Domain || descriptor.PlanDigest != request.Roots[index].PlanDigest {
			return nil, ErrStale
		}
		if descriptor.Domain == request.TargetDomain {
			target = index
		}
	}
	if target < 0 {
		return nil, ErrStale
	}
	targetDomain, err := runtime.openDomainPlan(directory, generation.Domains[target])
	if err != nil {
		return nil, err
	}
	if targetDomain.Plan.Repository != repository {
		return nil, ErrStale
	}
	authority, err := authorityForPlans(repository, []DomainPlan{targetDomain})
	if err != nil || authority != request.Authority || request.TargetOrdinal >= len(targetDomain.Plan.Expected) {
		return nil, errors.Join(err, ErrStale)
	}
	assembly := runtime.assemblyLock(targetDomain.Plan.Digest)
	if err := lockRecoveryPreparation(ctx, assembly); err != nil {
		return nil, err
	}
	defer assembly.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release, err := runtime.Fence.FenceDomain(ctx, FenceRequest{Plan: targetDomain.Plan})
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, invalid("recovery preparation fence release")
	}
	defer release()
	if err := reconciler.confirmRecoveryAuthority(ctx, request, generation); err != nil {
		return nil, err
	}
	for index, descriptor := range generation.Domains {
		domain, err := runtime.openDomainPlan(directory, descriptor)
		if err != nil {
			return nil, err
		}
		if domain.Plan.Repository != repository {
			return nil, ErrStale
		}
		authority, err := authorityForPlans(repository, []DomainPlan{domain})
		if err != nil || authority != request.Authority {
			return nil, errors.Join(err, ErrStale)
		}
		if err := reconciler.validateRecoveryDomain(ctx, directory, generation, domain, request.Roots[index]); err != nil {
			return nil, err
		}
	}
	// The same live shard excludes reconciliation; the exclusive publication
	// fence excludes lifecycle control/schedule collection. Enqueue is NOT a
	// store-level predecessor CAS, so re-prove the predecessor before mutation.
	if err := reconciler.confirmRecoveryAuthority(ctx, request, generation); err != nil {
		return nil, err
	}
	if request.Mode == RecoveryPreparationCheckpoint {
		if err := runtime.prepareRecoveryCheckpoint(ctx, directory, targetDomain, request.TargetOrdinal); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := runtime.enqueue(ctx, generation); err != nil {
		return nil, err
	}
	current, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if err != nil {
		return nil, err
	}
	want := recoveryGeneration(generation.Digest, request.PriorScheduleDigest)
	if current == nil || !recoveryScheduleMatches(*current, generation) || current.Generation != want {
		return nil, ErrStale
	}
	binding, err := runtime.readBinding(repository, want)
	if err != nil || binding.TargetGeneration != generation.Digest || binding.PriorSchedule != request.PriorScheduleDigest {
		return nil, errors.Join(err, ErrStale)
	}
	// Candidate publication has its own repository-work lock, not this
	// publication fence. Close that race after enqueue without rolling back a
	// possibly active successor if the selected upstream authority moved.
	if err := reconciler.confirmRecoveryReferences(ctx, request.Authority); err != nil {
		return nil, err
	}
	// Workers may already have claimed the successor. Its counters need not be
	// pristine; the exact operational identity and native binding are the result.
	return current, nil
}

// Ordinary mutexes stay unchanged. Only an explicitly admitted preparation
// bounds its wait; a canceled waiter never leaves a goroutine acquiring later.
func lockRecoveryPreparation(ctx context.Context, lock *sync.Mutex) error {
	wait, cancel := context.WithTimeout(ctx, authorityFenceAcquireTimeout)
	defer cancel()
	ticker := time.NewTicker(authorityFenceRetryDelay)
	defer ticker.Stop()
	for {
		if err := wait.Err(); err != nil {
			return err
		}
		if lock.TryLock() {
			return nil
		}
		select {
		case <-wait.Done():
		case <-ticker.C:
		}
	}
}

func recoveryScheduleMatches(schedule store.GenerationSchedule, generation Generation) bool {
	return store.ValidateGenerationSchedule(schedule) == nil &&
		schedule.Repository == generation.Repository && schedule.Stage == ScheduleStage &&
		schedule.ResourceClass == store.GenerationResourceExtraction &&
		schedule.TotalItems == int64(generation.WorkItems) && schedule.ChunkItems == ScheduleChunkItems &&
		schedule.MaxAttempts == ScheduleMaxAttempts && schedule.RepositoryTokens == ScheduleRepositoryTokens
}

func (reconciler *Reconciler) confirmRecoveryAuthority(
	ctx context.Context,
	request RecoveryPreparationRequest,
	generation Generation,
) error {
	authority := request.Authority
	if err := reconciler.confirmRecoveryReferences(ctx, authority); err != nil {
		return err
	}
	runtime := reconciler.Runtime
	raw, err := readBounded(runtime.authorityBindingPath(authority), MaxPointerBytes)
	var authorityBinding authorityBinding
	if err != nil || decodeExact(raw, &authorityBinding) != nil ||
		authorityBinding.Schema != AuthorityBindingSchema || authorityBinding.Authority != authority ||
		authorityBinding.Target != generation.Digest {
		return errors.Join(err, ErrStale)
	}
	current, err := runtime.Store.GetGenerationSchedule(ctx, authority.Repository, ScheduleStage)
	if err != nil {
		return err
	}
	if current == nil || !recoveryScheduleMatches(*current, generation) || current.Digest != request.PriorScheduleDigest ||
		current.Status != store.GenerationScheduleSettled || current.Failed != 0 || current.Succeeded != current.TotalChunks {
		return ErrStale
	}
	binding, err := runtime.readBinding(authority.Repository, current.Generation)
	if err != nil || binding.TargetGeneration != generation.Digest {
		return errors.Join(err, ErrStale)
	}
	return nil
}

func (reconciler *Reconciler) confirmRecoveryReferences(ctx context.Context, authority PlanningAuthority) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := reconciler.CandidateReference(ctx, authority.Repository)
	if err != nil || state.Repository != authority.Repository ||
		state.ManifestDigest != authority.CandidateManifestDigest || state.GenerationDigest != authority.CandidateGenerationDigest ||
		state.PolicyDigest != authority.CandidatePolicyDigest {
		return errors.Join(err, ErrStale)
	}
	source, observation, err := reconciler.AuthorityReference(ctx, authority.Repository)
	if err != nil || source != authority.SourceGenerationDigest || observation != authority.ObservationGenerationDigest {
		return errors.Join(err, ErrStale)
	}
	return nil
}

func (reconciler *Reconciler) validateRecoveryDomain(
	ctx context.Context,
	directory string,
	generation Generation,
	domain DomainPlan,
	expected RecoveryPreparationRoot,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	plan := domain.Plan
	resultDirectory := filepath.Join(directory, domainKey(plan.Domain))
	root, present, err := readDomainRoot(filepath.Join(resultDirectory, rootName()), plan)
	if err != nil || !present || root.Digest != expected.RootDigest ||
		root.Disposition == candidate.PartitionResultTerminalRefusal || root.Disposition == candidate.PartitionResultRetryable {
		return errors.Join(err, ErrStale)
	}
	pointer, err := reconciler.Runtime.readCurrentPointer(plan.Repository, plan.Domain)
	if err != nil || pointer.GenerationDigest != generation.Digest || pointer.PlanDigest != plan.Digest || pointer.RootDigest != root.Digest {
		return errors.Join(err, ErrStale)
	}
	completion, err := readCompletionControl(filepath.Join(resultDirectory, completionName()), plan)
	if err != nil || completion.Count != completion.Expected || len(root.Results) != len(plan.Expected) {
		return errors.Join(err, ErrStale)
	}
	for ordinal := range plan.Expected {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, present, err := readPartitionResult(filepath.Join(resultDirectory, resultName(ordinal)), plan, ordinal)
		if err != nil || !present || result != root.Results[ordinal] {
			return errors.Join(err, ErrStale)
		}
	}
	publication, err := reconciler.Evidence.GetPartitionedExtractionDomain(ctx, plan.Repository, plan.Domain)
	if err != nil {
		return err
	}
	planRaw, _ := canonical(plan)
	rootRaw, _ := canonical(root)
	if publication == nil || publication.Validate() != nil || publication.Repository != plan.Repository ||
		publication.Domain != plan.Domain || publication.RunID != domain.RunID || publication.PlanDigest != plan.Digest ||
		publication.RootDigest != root.Digest || publication.CandidateDigest != plan.CandidateManifestDigest ||
		publication.SourceDigest != plan.SourceGenerationDigest || publication.ObservationDigest != plan.ObservationGenerationDigest ||
		publication.Facts != root.Totals.Facts || publication.Rows != root.Totals.Rows || publication.References != root.Totals.References ||
		publication.Plan != string(planRaw) || publication.Root != string(rootRaw) {
		return ErrStale
	}
	return nil
}

func (runtime *Runtime) prepareRecoveryCheckpoint(ctx context.Context, directory string, domain DomainPlan, ordinal int) error {
	resultDirectory := filepath.Join(directory, domainKey(domain.Plan.Domain))
	completionPath := filepath.Join(resultDirectory, completionName())
	completion, err := readCompletionControl(completionPath, domain.Plan)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	completion.Bits[ordinal/8] &^= byte(1 << (ordinal % 8))
	completion.Count--
	if err := writeAtomicCanonical(completionPath, completion); err != nil {
		return err
	}
	// Remove the pointer before its root: even an interrupted prefix must not
	// leave a dangling pointer that the ordinary pointer-only reuse path accepts.
	for _, path := range []string{runtime.currentPath(domain.Plan.Repository, domain.Plan.Domain), filepath.Join(resultDirectory, rootName())} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	return nil
}
