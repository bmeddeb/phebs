package extractionpublication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	mathbits "math/bits"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/store"
)

// Runtime owns only partition execution controls. Candidate/source/
// observation content stays behind Source, and evidence authority stays behind
// Publisher.
type Runtime struct {
	Root          string
	Store         store.GenerationSchedulerStore
	Source        Source
	Executor      Executor
	Publisher     Publisher
	Fence         Fence
	OnSettled     func(context.Context, string) error
	Diagnostics   bool
	TimingReports func([]byte) error

	assembly [64]sync.Mutex
}

const (
	PartitionTimingSchemaV1 = "phebs-extraction-partition-timing-v1"
	PartitionTimingSchemaV2 = "phebs-extraction-partition-timing-v2"
	PartitionTimingSchema   = PartitionTimingSchemaV2
)

// PartitionTimingReport is a source-free diagnostic for one scheduler
// attempt. It contains bounded phase durations and digest identities only;
// source paths, repository names, content, and errors are deliberately absent.
type PartitionTimingReport struct {
	Schema                string                         `json:"schema"`
	Identity              string                         `json:"identity"`
	Generation            string                         `json:"generation"`
	Attempt               int                            `json:"attempt"`
	Outcome               string                         `json:"outcome"`
	Reused                bool                           `json:"reused"`
	SourceAcquireMS       int64                          `json:"source_acquire_ms"`
	ExecutorMS            int64                          `json:"executor_ms"`
	ResultMS              int64                          `json:"result_ms"`
	AssemblyMS            int64                          `json:"assembly_ms"`
	TotalMS               int64                          `json:"total_ms"`
	RefusalStage          pipelinerefusal.Stage          `json:"refusal_stage,omitempty"`
	RefusalGenerationKind pipelinerefusal.GenerationKind `json:"refusal_generation_kind,omitempty"`
	RefusalClassification pipelinerefusal.Classification `json:"refusal_classification,omitempty"`
	RefusalDimension      pipelinerefusal.Dimension      `json:"refusal_dimension,omitempty"`
	RefusalObserved       int64                          `json:"refusal_observed,omitempty"`
	RefusalLimit          int64                          `json:"refusal_limit,omitempty"`
}

func ValidatePartitionTimingReport(report PartitionTimingReport) error {
	if (report.Schema != PartitionTimingSchemaV1 && report.Schema != PartitionTimingSchemaV2) ||
		!validDigest(report.Identity) ||
		!validDigest(report.Generation) || report.Attempt < 0 ||
		(report.Outcome != "completed" && report.Outcome != "failed" &&
			report.Outcome != "terminal_refusal") ||
		report.SourceAcquireMS < 0 || report.ExecutorMS < 0 || report.ResultMS < 0 ||
		report.AssemblyMS < 0 || report.TotalMS < 0 ||
		report.SourceAcquireMS+report.ExecutorMS+report.ResultMS+report.AssemblyMS > report.TotalMS {
		return invalid("partition timing report")
	}
	hasRefusal := report.RefusalStage != "" || report.RefusalGenerationKind != "" ||
		report.RefusalClassification != "" || report.RefusalDimension != "" ||
		report.RefusalObserved != 0 || report.RefusalLimit != 0
	if report.Schema == PartitionTimingSchemaV1 {
		if hasRefusal {
			return invalid("v1 partition timing refusal")
		}
		return nil
	}
	if report.Outcome != "terminal_refusal" {
		if hasRefusal {
			return invalid("non-terminal partition timing refusal")
		}
		return nil
	}
	receipt := report.refusalReceipt()
	if pipelinerefusal.Validate(receipt) != nil {
		return invalid("terminal partition timing refusal")
	}
	return nil
}

func (report PartitionTimingReport) refusalReceipt() pipelinerefusal.Receipt {
	return pipelinerefusal.Receipt{
		Schema: pipelinerefusal.Schema, Stage: report.RefusalStage,
		GenerationKind: report.RefusalGenerationKind,
		Classification: report.RefusalClassification,
		Dimension:      report.RefusalDimension,
		Observed:       report.RefusalObserved,
		Limit:          report.RefusalLimit,
	}
}

func (report *PartitionTimingReport) setRefusal(err error) {
	if report == nil {
		return
	}
	receipt, ok := pipelinerefusal.From(err)
	if !ok || pipelinerefusal.Validate(receipt) != nil {
		receipt = pipelinerefusal.Receipt{
			Schema: pipelinerefusal.Schema, Stage: pipelinerefusal.StageUnknown,
			GenerationKind: pipelinerefusal.GenerationUnknown,
			Classification: pipelinerefusal.ClassificationUnknown,
			Dimension:      pipelinerefusal.DimensionUnknown,
		}
	}
	report.RefusalStage = receipt.Stage
	report.RefusalGenerationKind = receipt.GenerationKind
	report.RefusalClassification = receipt.Classification
	report.RefusalDimension = receipt.Dimension
	report.RefusalObserved = receipt.Observed
	report.RefusalLimit = receipt.Limit
}

func validatePlanningAuthority(authority PlanningAuthority) error {
	if reponame.Validate(authority.Repository) != nil ||
		!validDigest(authority.CandidateManifestDigest) ||
		!validDigest(authority.CandidateGenerationDigest) ||
		!validDigest(authority.CandidatePolicyDigest) ||
		!validDigest(authority.SourceGenerationDigest) ||
		!validDigest(authority.ObservationGenerationDigest) {
		return invalid("planning authority")
	}
	return nil
}

func authorityForPlans(repository string, domains []DomainPlan) (PlanningAuthority, error) {
	if len(domains) == 0 {
		return PlanningAuthority{}, invalid("empty authority binding")
	}
	first := domains[0].Plan
	authority := PlanningAuthority{
		Repository:                  repository,
		CandidateManifestDigest:     first.CandidateManifestDigest,
		CandidateGenerationDigest:   first.CandidateGenerationDigest,
		CandidatePolicyDigest:       first.CandidatePolicyDigest,
		SourceGenerationDigest:      first.SourceGenerationDigest,
		ObservationGenerationDigest: first.ObservationGenerationDigest,
	}
	if validatePlanningAuthority(authority) != nil {
		return PlanningAuthority{}, invalid("plan authority binding")
	}
	for _, domain := range domains[1:] {
		plan := domain.Plan
		if plan.Repository != repository ||
			plan.CandidateManifestDigest != authority.CandidateManifestDigest ||
			plan.CandidateGenerationDigest != authority.CandidateGenerationDigest ||
			plan.CandidatePolicyDigest != authority.CandidatePolicyDigest ||
			plan.SourceGenerationDigest != authority.SourceGenerationDigest ||
			plan.ObservationGenerationDigest != authority.ObservationGenerationDigest {
			return PlanningAuthority{}, invalid("mixed plan authority binding")
		}
	}
	return authority, nil
}

func authorityBindingDigest(authority PlanningAuthority) string {
	raw, _ := canonical(authority)
	return digest("phebs-extraction-authority-binding-v1", raw)
}

func (runtime *Runtime) authorityBindingPath(authority PlanningAuthority) string {
	return filepath.Join(runtime.repositoryDirectory(authority.Repository),
		"authority-"+strings.TrimPrefix(authorityBindingDigest(authority), "sha256:")+".json")
}

func (runtime *Runtime) writeAuthorityBinding(authority PlanningAuthority, target string) error {
	if validatePlanningAuthority(authority) != nil || !validDigest(target) {
		return invalid("authority binding")
	}
	binding := authorityBinding{Schema: AuthorityBindingSchema, Authority: authority, Target: target}
	path := runtime.authorityBindingPath(authority)
	if err := writeExclusiveCanonical(path, binding); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		raw, readErr := readBounded(path, MaxPointerBytes)
		var existing authorityBinding
		if readErr != nil || decodeExact(raw, &existing) != nil || existing != binding {
			return errors.Join(readErr, invalid("authority binding collision"))
		}
	}
	return syncDirectory(filepath.Dir(path))
}

// ReuseAuthority recognizes an exact current or actively scheduled execution
// generation from pointer-sized controls. It performs no candidate member,
// sparse index, source segment, inventory segment, or plan-file read.
func (runtime *Runtime) ReuseAuthority(
	ctx context.Context,
	authority PlanningAuthority,
) (string, bool, error) {
	if err := runtime.validate(); err != nil || validatePlanningAuthority(authority) != nil {
		return "", false, invalid("reuse authority")
	}
	raw, err := readBounded(runtime.authorityBindingPath(authority), MaxPointerBytes)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var binding authorityBinding
	if decodeExact(raw, &binding) != nil || binding.Schema != AuthorityBindingSchema ||
		binding.Authority != authority || !validDigest(binding.Target) {
		return "", false, invalid("authority binding control")
	}
	directory := runtime.generationDirectory(authority.Repository, binding.Target)
	generation, err := runtime.openGeneration(directory, authority.Repository, binding.Target)
	if err != nil {
		return "", false, err
	}
	if schedule, scheduleErr := runtime.Store.GetGenerationSchedule(
		ctx, authority.Repository, ScheduleStage,
	); scheduleErr == nil && schedule.Status == store.GenerationScheduleActive {
		target, targetErr := runtime.scheduleTarget(authority.Repository, schedule.Generation)
		if targetErr == nil && target == binding.Target {
			return binding.Target, true, nil
		}
	} else if scheduleErr != nil && !errors.Is(scheduleErr, store.ErrNotFound) {
		return "", false, scheduleErr
	}
	for _, descriptor := range generation.Domains {
		pointer, pointerErr := runtime.readCurrentPointer(authority.Repository, descriptor.Domain)
		if pointerErr != nil || pointer.GenerationDigest != binding.Target ||
			pointer.PlanDigest != descriptor.PlanDigest {
			return "", false, nil
		}
	}
	return binding.Target, true, nil
}

// ExistingPlans returns the durable run bindings for an exact already-created
// generation. Planning calls this before minting new invisible evidence runs,
// so restart and A→B→A reuse do not leak replacement staging runs.
func (runtime *Runtime) ExistingPlans(
	ctx context.Context,
	repository string,
	plans []candidate.DomainResultPlan,
) ([]DomainPlan, bool, error) {
	if err := runtime.validate(); err != nil || reponame.Validate(repository) != nil ||
		plans == nil || len(plans) > MaxDomains {
		return nil, false, invalid("existing generation inventory")
	}
	semantic := struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Plans      []string `json:"plans"`
	}{Schema: GenerationSchema, Repository: repository, Plans: make([]string, 0, len(plans))}
	for _, plan := range plans {
		if plan.Repository != repository || candidate.ValidateDomainResultPlanControl(plan) != nil {
			return nil, false, invalid("existing generation plan")
		}
		semantic.Plans = append(semantic.Plans, plan.Digest)
	}
	raw, _ := canonical(semantic)
	target := digest("phebs-extraction-partition-generation-v1", raw)
	directory := runtime.generationDirectory(repository, target)
	generation, err := runtime.openGeneration(directory, repository, target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(generation.Domains) != len(plans) {
		return nil, false, invalid("existing generation plan count")
	}
	result := make([]DomainPlan, len(plans))
	for index, descriptor := range generation.Domains {
		domain, err := runtime.openDomainPlan(directory, descriptor)
		if err != nil {
			return nil, false, err
		}
		if domain.Plan.Digest != plans[index].Digest {
			return nil, false, invalid("existing generation plan order")
		}
		result[index] = domain
	}
	return result, true, nil
}

// Reconcile admits the complete reservation set before any Source lease,
// persists the immutable controls, reactivates an exact historical generation
// without re-execution, or enqueues one item per unsettled partition.
func (runtime *Runtime) Reconcile(
	ctx context.Context,
	repository string,
	domains []DomainPlan,
) (string, error) {
	if err := runtime.validate(); err != nil {
		return "", err
	}
	if err := reponame.Validate(repository); err != nil || domains == nil || len(domains) > MaxDomains {
		return "", invalid("reconcile inventory")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	semantic := struct {
		Schema     string   `json:"schema"`
		Repository string   `json:"repository"`
		Plans      []string `json:"plans"`
	}{Schema: GenerationSchema, Repository: repository, Plans: make([]string, 0, len(domains))}
	seen := make(map[string]struct{}, len(domains))
	for index := range domains {
		domain := domains[index]
		if domain.Schema != DomainSchema || domain.Plan.Repository != repository ||
			!boundedIdentity(domain.RunID, 256) ||
			candidate.ValidateDomainResultPlanControl(domain.Plan) != nil {
			return "", invalid("domain plan")
		}
		key := domain.Plan.Domain + "\x00" + domain.Plan.Version
		if _, duplicate := seen[key]; duplicate {
			return "", invalid("duplicate domain plan")
		}
		seen[key] = struct{}{}
		semantic.Plans = append(semantic.Plans, domain.Plan.Digest)
	}
	semanticRaw, _ := canonical(semantic)
	target := digest("phebs-extraction-partition-generation-v1", semanticRaw)
	var authority PlanningAuthority
	hasAuthorityBinding := false
	if len(domains) > 0 {
		if candidateAuthority, authorityErr := authorityForPlans(repository, domains); authorityErr == nil {
			authority = candidateAuthority
			hasAuthorityBinding = true
		}
	}
	directory := runtime.generationDirectory(repository, target)
	if generation, err := runtime.openGeneration(directory, repository, target); err == nil {
		if hasAuthorityBinding {
			if err := runtime.writeAuthorityBinding(authority, target); err != nil {
				return "", err
			}
		}
		if err := runtime.finalizeZeroDomains(ctx, generation, directory); err != nil {
			return "", err
		}
		if err := runtime.reactivateComplete(ctx, generation, directory); err != nil {
			return "", err
		}
		if runtime.generationSettled(generation, directory) {
			return target, nil
		}
		return target, runtime.enqueue(ctx, generation)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if err := ensurePrivateDirectory(runtime.repositoryDirectory(repository)); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(runtime.repositoryDirectory(repository), ".stage-generation-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(staging, 0o700); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	generation := Generation{
		Schema: GenerationSchema, Repository: repository, Digest: target,
		Domains: make([]DomainDescriptor, 0, len(domains)),
	}
	workOffset := 0
	for index, domain := range domains {
		planName := fmt.Sprintf("plan-%03d.json", index)
		descriptor := DomainDescriptor{
			Domain: domain.Plan.Domain, Version: domain.Plan.Version,
			PlanDigest: domain.Plan.Digest, PlanName: planName, RunID: domain.RunID,
			StartOrdinal: workOffset, EndOrdinal: workOffset + len(domain.Plan.Expected),
		}
		generation.Domains = append(generation.Domains, descriptor)
		workOffset = descriptor.EndOrdinal
		if err := writeExclusiveCanonical(filepath.Join(staging, planName), domain); err != nil {
			return "", err
		}
		if err := ensurePrivateDirectory(filepath.Join(staging, domainKey(domain.Plan.Domain))); err != nil {
			return "", err
		}
		if err := writeExclusiveCanonical(
			filepath.Join(staging, domainKey(domain.Plan.Domain), completionName()),
			newCompletionControl(domain.Plan),
		); err != nil {
			return "", err
		}
	}
	generation.WorkItems = workOffset
	if err := validateGeneration(generation); err != nil {
		return "", err
	}
	if err := writeExclusiveCanonical(filepath.Join(staging, generationName()), generation); err != nil {
		return "", err
	}
	if err := syncDirectory(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, directory); err != nil {
		opened, openErr := runtime.openGeneration(directory, repository, target)
		if openErr != nil {
			return "", errors.Join(err, openErr)
		}
		generation = opened
	}
	if err := syncDirectory(runtime.repositoryDirectory(repository)); err != nil {
		return "", err
	}
	if hasAuthorityBinding {
		if err := runtime.writeAuthorityBinding(authority, target); err != nil {
			return "", err
		}
	}
	if err := runtime.finalizeZeroDomains(ctx, generation, directory); err != nil {
		return "", err
	}
	if generation.WorkItems == 0 {
		return target, nil
	}
	return target, runtime.enqueue(ctx, generation)
}

func (runtime *Runtime) validate() error {
	if runtime == nil || !filepath.IsAbs(runtime.Root) || runtime.Root == string(filepath.Separator) ||
		runtime.Store == nil || runtime.Source == nil || runtime.Executor == nil ||
		runtime.Publisher == nil || runtime.Fence == nil {
		return invalid("runtime configuration")
	}
	return nil
}

func boundedIdentity(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value
}

func validateGeneration(value Generation) error {
	if value.Schema != GenerationSchema || reponame.Validate(value.Repository) != nil ||
		!validDigest(value.Digest) || value.Domains == nil || len(value.Domains) > MaxDomains ||
		value.WorkItems < 0 || value.WorkItems > MaxDomains*candidate.MaxDomainResultPartitions {
		return invalid("generation control")
	}
	end := 0
	seen := map[string]struct{}{}
	for index, domain := range value.Domains {
		if !boundedIdentity(domain.Domain, 128) || !boundedIdentity(domain.Version, 128) ||
			!validDigest(domain.PlanDigest) || domain.PlanName != fmt.Sprintf("plan-%03d.json", index) ||
			!boundedIdentity(domain.RunID, 256) || domain.StartOrdinal != end ||
			domain.EndOrdinal < domain.StartOrdinal ||
			domain.EndOrdinal-domain.StartOrdinal > candidate.MaxDomainResultPartitions {
			return invalid("generation domain descriptor")
		}
		key := domain.Domain + "\x00" + domain.Version
		if _, duplicate := seen[key]; duplicate {
			return invalid("duplicate generation domain")
		}
		seen[key] = struct{}{}
		end = domain.EndOrdinal
	}
	if end != value.WorkItems {
		return invalid("generation work range")
	}
	return nil
}

func (runtime *Runtime) repositoryDirectory(repository string) string {
	return filepath.Join(runtime.Root, repositoryKey(repository))
}

func (runtime *Runtime) generationDirectory(repository, generation string) string {
	return filepath.Join(runtime.repositoryDirectory(repository), strings.TrimPrefix(generation, "sha256:"))
}

func (runtime *Runtime) bindingPath(repository, schedule string) string {
	return filepath.Join(runtime.repositoryDirectory(repository),
		"schedule-"+strings.TrimPrefix(schedule, "sha256:")+".json")
}

func (runtime *Runtime) currentPath(repository, domain string) string {
	return filepath.Join(runtime.repositoryDirectory(repository), "current-"+domainKey(domain)+".json")
}

func (runtime *Runtime) openGeneration(directory, repository, target string) (Generation, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return Generation{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Generation{}, invalid("generation directory")
	}
	raw, err := readBounded(filepath.Join(directory, generationName()), MaxGenerationControlBytes)
	if err != nil {
		return Generation{}, err
	}
	var generation Generation
	if decodeExact(raw, &generation) != nil || validateGeneration(generation) != nil ||
		generation.Repository != repository || generation.Digest != target {
		return Generation{}, invalid("generation control")
	}
	for _, descriptor := range generation.Domains {
		resultInfo, statErr := os.Lstat(filepath.Join(directory, domainKey(descriptor.Domain)))
		if statErr != nil || !resultInfo.IsDir() || resultInfo.Mode()&os.ModeSymlink != 0 {
			return Generation{}, errors.Join(statErr, invalid("domain result directory"))
		}
	}
	return generation, nil
}

func (runtime *Runtime) openDomainPlan(directory string, descriptor DomainDescriptor) (DomainPlan, error) {
	raw, err := readBounded(filepath.Join(directory, descriptor.PlanName), candidate.MaxDomainResultPlanBytes+1024)
	if err != nil {
		return DomainPlan{}, err
	}
	var domain DomainPlan
	if decodeExact(raw, &domain) != nil || domain.Schema != DomainSchema ||
		domain.RunID != descriptor.RunID || domain.Plan.Digest != descriptor.PlanDigest ||
		domain.Plan.Domain != descriptor.Domain || domain.Plan.Version != descriptor.Version ||
		len(domain.Plan.Expected) != descriptor.EndOrdinal-descriptor.StartOrdinal ||
		candidate.ValidateDomainResultPlanControl(domain.Plan) != nil {
		return DomainPlan{}, invalid("domain plan control")
	}
	return domain, nil
}

func recoveryGeneration(target, prior string) string {
	return digest("phebs-extraction-recovery-schedule-v1", []byte(target+"\x00"+prior))
}

func (runtime *Runtime) enqueue(ctx context.Context, generation Generation) error {
	scheduleGeneration := generation.Digest
	prior := ""
	current, err := runtime.Store.GetGenerationSchedule(ctx, generation.Repository, ScheduleStage)
	if err == nil {
		bindingTarget, targetErr := runtime.scheduleTarget(generation.Repository, current.Generation)
		if targetErr == nil && bindingTarget == generation.Digest && current.Status == store.GenerationScheduleActive {
			return nil
		}
		if targetErr == nil && bindingTarget == generation.Digest || current.Generation == generation.Digest {
			scheduleGeneration = recoveryGeneration(generation.Digest, current.Digest)
			prior = current.Digest
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	binding := scheduleBinding{
		Schema: BindingSchema, Repository: generation.Repository,
		ScheduleGeneration: scheduleGeneration, TargetGeneration: generation.Digest,
		PriorSchedule: prior,
	}
	if err := runtime.writeBinding(binding); err != nil {
		return err
	}
	_, err = runtime.Store.EnqueueGenerationSchedule(ctx, store.GenerationScheduleSpec{
		Repository: generation.Repository, Stage: ScheduleStage,
		Generation: scheduleGeneration, ResourceClass: store.GenerationResourceExtraction,
		TotalItems: int64(generation.WorkItems), ChunkItems: ScheduleChunkItems,
		MaxAttempts: ScheduleMaxAttempts, RepositoryTokens: ScheduleRepositoryTokens,
	})
	return err
}

func (runtime *Runtime) writeBinding(binding scheduleBinding) error {
	if validateBinding(binding) != nil {
		return invalid("schedule binding")
	}
	path := runtime.bindingPath(binding.Repository, binding.ScheduleGeneration)
	if err := writeExclusiveCanonical(path, binding); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := runtime.readBinding(binding.Repository, binding.ScheduleGeneration)
		if readErr != nil || existing != binding {
			return errors.Join(readErr, invalid("schedule binding collision"))
		}
	}
	return syncDirectory(filepath.Dir(path))
}

func validateBinding(binding scheduleBinding) error {
	if binding.Schema != BindingSchema || reponame.Validate(binding.Repository) != nil ||
		!validDigest(binding.ScheduleGeneration) || !validDigest(binding.TargetGeneration) {
		return invalid("schedule binding")
	}
	if binding.ScheduleGeneration == binding.TargetGeneration {
		if binding.PriorSchedule != "" {
			return invalid("initial schedule binding")
		}
	} else if !validDigest(binding.PriorSchedule) ||
		binding.ScheduleGeneration != recoveryGeneration(binding.TargetGeneration, binding.PriorSchedule) {
		return invalid("recovery schedule binding")
	}
	return nil
}

func (runtime *Runtime) readBinding(repository, schedule string) (scheduleBinding, error) {
	raw, err := readBounded(runtime.bindingPath(repository, schedule), MaxPointerBytes)
	if err != nil {
		return scheduleBinding{}, err
	}
	var binding scheduleBinding
	if decodeExact(raw, &binding) != nil || validateBinding(binding) != nil ||
		binding.Repository != repository || binding.ScheduleGeneration != schedule {
		return scheduleBinding{}, invalid("schedule binding")
	}
	return binding, nil
}

func (runtime *Runtime) scheduleTarget(repository, schedule string) (string, error) {
	binding, err := runtime.readBinding(repository, schedule)
	if err == nil {
		return binding.TargetGeneration, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return schedule, nil
}

// SchedulePlanningAuthority opens one scheduler generation through the same
// binding, generation, and domain-plan validators used by Handle. Diagnostics
// may use the returned source-free authority to identify in-flight work, but
// it grants no content lease and performs no publication transition.
func (runtime *Runtime) SchedulePlanningAuthority(
	repository string,
	schedule string,
) (PlanningAuthority, error) {
	if runtime == nil || !filepath.IsAbs(runtime.Root) ||
		reponame.Validate(repository) != nil || !validDigest(schedule) {
		return PlanningAuthority{}, invalid("schedule planning authority")
	}
	target, err := runtime.scheduleTarget(repository, schedule)
	if err != nil {
		return PlanningAuthority{}, err
	}
	directory := runtime.generationDirectory(repository, target)
	generation, err := runtime.openGeneration(directory, repository, target)
	if err != nil {
		return PlanningAuthority{}, err
	}
	if len(generation.Domains) == 0 {
		return PlanningAuthority{}, invalid("empty schedule planning authority")
	}
	domains := make([]DomainPlan, 0, len(generation.Domains))
	for _, descriptor := range generation.Domains {
		domain, openErr := runtime.openDomainPlan(directory, descriptor)
		if openErr != nil {
			return PlanningAuthority{}, openErr
		}
		domains = append(domains, domain)
	}
	return authorityForPlans(repository, domains)
}

// Handle executes exactly one scheduler item. An exact settled retry performs
// no Source acquisition and proceeds directly to the idempotent assembler.
func (runtime *Runtime) Handle(ctx context.Context, chunk store.GenerationChunk) (retErr error) {
	if err := runtime.validate(); err != nil || chunk.Stage != ScheduleStage ||
		chunk.Length != 1 || chunk.Offset < 0 || !validDigest(chunk.Generation) {
		return invalid("runtime chunk")
	}
	timingEnabled := runtime.Diagnostics || runtime.TimingReports != nil
	var started time.Time
	var timing PartitionTimingReport
	var timingRefusal error
	if timingEnabled {
		started = time.Now()
		timing = PartitionTimingReport{
			Schema: PartitionTimingSchema, Identity: chunk.Identity,
			Generation: chunk.Generation, Attempt: chunk.Attempt, Outcome: "failed",
		}
	}
	defer func() {
		if !timingEnabled {
			return
		}
		timing.TotalMS = time.Since(started).Milliseconds()
		if retErr == nil {
			timing.Outcome = "completed"
		}
		if timing.Outcome == "terminal_refusal" {
			timing.setRefusal(timingRefusal)
		}
		runtime.emitPartitionTiming(timing)
	}()
	target, err := runtime.scheduleTarget(chunk.Repository, chunk.Generation)
	if err != nil {
		return err
	}
	directory := runtime.generationDirectory(chunk.Repository, target)
	generation, err := runtime.openGeneration(directory, chunk.Repository, target)
	if err != nil {
		return err
	}
	descriptor, localOrdinal, err := domainForOffset(generation, int(chunk.Offset))
	if err != nil {
		return err
	}
	domain, err := runtime.openDomainPlan(directory, descriptor)
	if err != nil {
		return err
	}
	resultPath := filepath.Join(directory, domainKey(descriptor.Domain), resultName(localOrdinal))
	result, present, err := readPartitionResult(resultPath, domain.Plan, localOrdinal)
	if err != nil {
		return err
	}
	if !present {
		// The source lease is the expensive/content-bearing boundary. Re-fence
		// the exact scheduler lease immediately before acquiring it; a
		// superseded or reaped worker must stop control-only.
		if err := runtime.Store.HeartbeatGenerationChunk(ctx, chunk); err != nil {
			return err
		}
		var phaseStarted time.Time
		if timingEnabled {
			phaseStarted = time.Now()
		}
		lease, acquireErr := runtime.Source.AcquirePartition(ctx, domain.Plan, localOrdinal)
		if timingEnabled {
			timing.SourceAcquireMS = time.Since(phaseStarted).Milliseconds()
		}
		if acquireErr != nil {
			return acquireErr
		}
		if lease == nil {
			return invalid("source returned no partition lease")
		}
		defer lease.Release()
		deadline := time.Duration(
			domain.Plan.Expected[localOrdinal].Quotas.DeadlineMilliseconds,
		) * time.Millisecond
		executionCtx, cancelExecution := context.WithTimeout(ctx, deadline)
		defer cancelExecution()
		if timingEnabled {
			phaseStarted = time.Now()
		}
		spec, executeErr := runtime.Executor.ExecutePartition(
			executionCtx, domain.Plan, localOrdinal, lease, domain.RunID,
		)
		if timingEnabled {
			timing.ExecutorMS = time.Since(phaseStarted).Milliseconds()
		}
		if executeErr != nil {
			if spec.Disposition != candidate.PartitionResultTerminalRefusal ||
				!store.IsTerminal(executeErr) {
				return executeErr
			}
			timingRefusal = executeErr
		} else if spec.Disposition == candidate.PartitionResultTerminalRefusal {
			return invalid("executor returned an unmarked terminal refusal")
		}
		if spec.Disposition == candidate.PartitionResultRetryable {
			return invalid("executor returned scheduler-owned retryable disposition")
		}
		if timingEnabled {
			phaseStarted = time.Now()
		}
		result, err = candidate.BuildPartitionResult(domain.Plan, localOrdinal, spec)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runtime.Store.HeartbeatGenerationChunk(ctx, chunk); err != nil {
			return err
		}
		if err := installPartitionResult(resultPath, result); err != nil {
			return err
		}
		if timingEnabled {
			timing.ResultMS = time.Since(phaseStarted).Milliseconds()
		}
	} else {
		timing.Reused = true
	}
	var phaseStarted time.Time
	if timingEnabled {
		phaseStarted = time.Now()
	}
	if err := runtime.tryAssemble(ctx, chunk, directory, descriptor, domain, localOrdinal); err != nil {
		return err
	}
	if timingEnabled {
		timing.AssemblyMS = time.Since(phaseStarted).Milliseconds()
	}
	if result.Disposition == candidate.PartitionResultTerminalRefusal {
		timing.Outcome = "terminal_refusal"
		return store.WithTerminal(errors.New("partition terminal refusal"))
	}
	return nil
}

func (runtime *Runtime) emitPartitionTiming(report PartitionTimingReport) {
	if runtime == nil || (!runtime.Diagnostics && runtime.TimingReports == nil) {
		return
	}
	if ValidatePartitionTimingReport(report) != nil {
		return
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > 4096 {
		return
	}
	sink := runtime.TimingReports
	if sink == nil {
		sink = func(value []byte) error {
			diagnostics.Logf("extraction partition timing: %s", value)
			return nil
		}
	}
	func() {
		defer func() { _ = recover() }()
		_ = sink(raw)
	}()
}

// OnExhausted is wired to generationscheduler.Class.OnExhausted. It records a
// zero-work retryable result only after the scheduler atomically consumes the
// final attempt.
func (runtime *Runtime) OnExhausted(
	ctx context.Context,
	chunk store.GenerationChunk,
	_ error,
) error {
	if err := runtime.validate(); err != nil || chunk.Stage != ScheduleStage || chunk.Length != 1 {
		return invalid("exhausted runtime chunk")
	}
	current, err := runtime.Store.GetGenerationSchedule(ctx, chunk.Repository, ScheduleStage)
	if err != nil {
		return err
	}
	if current.Generation != chunk.Generation || current.Digest != chunk.ScheduleDigest {
		return store.ErrGenerationStale
	}
	target, err := runtime.scheduleTarget(chunk.Repository, chunk.Generation)
	if err != nil {
		return err
	}
	directory := runtime.generationDirectory(chunk.Repository, target)
	generation, err := runtime.openGeneration(directory, chunk.Repository, target)
	if err != nil {
		return err
	}
	descriptor, localOrdinal, err := domainForOffset(generation, int(chunk.Offset))
	if err != nil {
		return err
	}
	domain, err := runtime.openDomainPlan(directory, descriptor)
	if err != nil {
		return err
	}
	result, err := candidate.BuildPartitionResult(domain.Plan, localOrdinal, candidate.PartitionResultSpec{
		Disposition: candidate.PartitionResultRetryable, Reason: RetryFailureReason,
	})
	if err != nil {
		return err
	}
	path := filepath.Join(directory, domainKey(descriptor.Domain), resultName(localOrdinal))
	if err := installPartitionResult(path, result); err != nil {
		return err
	}
	return runtime.tryAssemble(ctx, chunk, directory, descriptor, domain, localOrdinal)
}

func domainForOffset(generation Generation, offset int) (DomainDescriptor, int, error) {
	if offset < 0 || offset >= generation.WorkItems {
		return DomainDescriptor{}, 0, invalid("chunk offset")
	}
	for _, domain := range generation.Domains {
		if offset >= domain.StartOrdinal && offset < domain.EndOrdinal {
			return domain, offset - domain.StartOrdinal, nil
		}
	}
	return DomainDescriptor{}, 0, invalid("chunk offset is unmapped")
}

func readPartitionResult(path string, plan candidate.DomainResultPlan, ordinal int) (candidate.PartitionResult, bool, error) {
	raw, err := readBounded(path, candidate.MaxPartitionResultBytes)
	if errors.Is(err, os.ErrNotExist) {
		return candidate.PartitionResult{}, false, nil
	}
	if err != nil {
		return candidate.PartitionResult{}, false, err
	}
	var result candidate.PartitionResult
	if decodeExact(raw, &result) != nil {
		return candidate.PartitionResult{}, false, invalid("partition result control")
	}
	rebuilt, err := candidate.BuildPartitionResult(plan, ordinal, candidate.PartitionResultSpec{
		Disposition: result.Disposition, Reason: result.Reason,
		Totals: result.Totals, ContentDigest: result.ContentDigest,
	})
	if err != nil {
		return candidate.PartitionResult{}, false, err
	}
	want, _ := canonical(rebuilt)
	if !bytes.Equal(raw, want) {
		return candidate.PartitionResult{}, false, invalid("partition result differs from recomputation")
	}
	return result, true, nil
}

func installPartitionResult(path string, result candidate.PartitionResult) error {
	raw, err := canonical(result)
	if err != nil {
		return err
	}
	if err := writeExclusive(path, raw); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := readBounded(path, candidate.MaxPartitionResultBytes)
		if readErr != nil || !bytes.Equal(existing, raw) {
			return errors.Join(readErr, invalid("partition result collision"))
		}
	}
	return syncDirectory(filepath.Dir(path))
}

func newCompletionControl(plan candidate.DomainResultPlan) completionControl {
	return completionControl{
		Schema: CompletionSchema, PlanDigest: plan.Digest,
		Expected: len(plan.Expected), Bits: make([]byte, (len(plan.Expected)+7)/8),
	}
}

func readCompletionControl(path string, plan candidate.DomainResultPlan) (completionControl, error) {
	raw, err := readBounded(path, MaxCompletionBytes)
	if err != nil {
		return completionControl{}, err
	}
	var control completionControl
	if decodeExact(raw, &control) != nil || control.Schema != CompletionSchema ||
		control.PlanDigest != plan.Digest || control.Expected != len(plan.Expected) ||
		len(control.Bits) != (len(plan.Expected)+7)/8 || control.Count < 0 ||
		control.Count > control.Expected {
		return completionControl{}, invalid("completion control")
	}
	count := 0
	for _, value := range control.Bits {
		count += mathbits.OnesCount8(value)
	}
	if remainder := control.Expected % 8; remainder != 0 && len(control.Bits) > 0 {
		if control.Bits[len(control.Bits)-1]&^byte((1<<remainder)-1) != 0 {
			return completionControl{}, invalid("completion control tail")
		}
	}
	if count != control.Count {
		return completionControl{}, invalid("completion control count")
	}
	return control, nil
}

func markCompletion(control *completionControl, ordinal int) bool {
	if ordinal < 0 {
		return false
	}
	mask := byte(1 << (ordinal % 8))
	if control.Bits[ordinal/8]&mask != 0 {
		return false
	}
	control.Bits[ordinal/8] |= mask
	control.Count++
	return true
}

func (runtime *Runtime) assemblyLock(planDigest string) *sync.Mutex {
	shard := 0
	for index := range len(planDigest) {
		shard = (shard*33 + int(planDigest[index])) % len(runtime.assembly)
	}
	return &runtime.assembly[shard]
}

func (runtime *Runtime) tryAssemble(
	ctx context.Context,
	chunk store.GenerationChunk,
	directory string,
	descriptor DomainDescriptor,
	domain DomainPlan,
	settledOrdinal int,
) error {
	lock := runtime.assemblyLock(domain.Plan.Digest)
	lock.Lock()
	defer lock.Unlock()
	resultDirectory := filepath.Join(directory, domainKey(descriptor.Domain))
	rootPath := filepath.Join(resultDirectory, rootName())
	if root, present, err := readDomainRoot(rootPath, domain.Plan); err != nil {
		return err
	} else if present {
		return runtime.publishRoot(ctx, chunk, directory, domain, root)
	}
	completionPath := filepath.Join(resultDirectory, completionName())
	completion, err := readCompletionControl(completionPath, domain.Plan)
	if err != nil {
		return err
	}
	if settledOrdinal < -1 || settledOrdinal >= completion.Expected {
		return invalid("completion ordinal")
	}
	if markCompletion(&completion, settledOrdinal) {
		if err := writeAtomicCanonical(completionPath, completion); err != nil {
			return err
		}
	}
	if completion.Count != completion.Expected {
		return nil
	}
	results := make([]candidate.PartitionResult, 0, len(domain.Plan.Expected))
	for ordinal := range domain.Plan.Expected {
		result, present, err := readPartitionResult(
			filepath.Join(resultDirectory, resultName(ordinal)), domain.Plan, ordinal,
		)
		if err != nil {
			return err
		}
		if !present {
			return invalid("completion control names a missing result")
		}
		results = append(results, result)
	}
	root, err := candidate.BuildDomainResultRoot(domain.Plan, results)
	if err != nil {
		return err
	}
	rootRaw, _ := canonical(root)
	if err := writeExclusive(rootPath, rootRaw); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := readBounded(rootPath, candidate.MaxDomainResultRootBytes)
		if readErr != nil || !bytes.Equal(existing, rootRaw) {
			return errors.Join(readErr, invalid("domain root collision"))
		}
	}
	if err := syncDirectory(resultDirectory); err != nil {
		return err
	}
	return runtime.publishRoot(ctx, chunk, directory, domain, root)
}

func (runtime *Runtime) publishRoot(
	ctx context.Context,
	chunk store.GenerationChunk,
	directory string,
	domain DomainPlan,
	root candidate.DomainResultRoot,
) error {
	if root.Disposition == candidate.PartitionResultTerminalRefusal ||
		root.Disposition == candidate.PartitionResultRetryable {
		if runtime.OnSettled != nil {
			return runtime.OnSettled(ctx, domain.Plan.Repository)
		}
		return nil
	}
	releaseFence, err := runtime.Fence.FenceDomain(ctx, FenceRequest{Chunk: chunk, Plan: domain.Plan})
	if err != nil {
		return errors.Join(ErrStale, err)
	}
	if releaseFence == nil {
		return invalid("publication fence returned no release")
	}
	released := false
	defer func() {
		if !released {
			releaseFence()
		}
	}()
	if err := runtime.Publisher.PublishDomain(ctx, domain.Plan, root, domain.RunID); err != nil {
		return err
	}
	pointer := Pointer{
		Schema: PointerSchema, Repository: domain.Plan.Repository,
		Domain:           domain.Plan.Domain,
		GenerationDigest: "sha256:" + filepath.Base(directory),
		PlanDigest:       domain.Plan.Digest,
		RootDigest:       root.Digest,
		RootName:         filepath.Join(domainKey(domain.Plan.Domain), rootName()),
	}
	if err := writeAtomicCanonical(runtime.currentPath(domain.Plan.Repository, domain.Plan.Domain), pointer); err != nil {
		return err
	}
	// Downstream reconciliation acquires the shared side of the same mutation
	// fence. Release the exclusive publication fence before invoking it.
	releaseFence()
	released = true
	if runtime.OnSettled != nil {
		return runtime.OnSettled(ctx, domain.Plan.Repository)
	}
	return nil
}

func (runtime *Runtime) finalizeZeroDomains(ctx context.Context, generation Generation, directory string) error {
	for _, descriptor := range generation.Domains {
		if descriptor.StartOrdinal != descriptor.EndOrdinal {
			continue
		}
		domain, err := runtime.openDomainPlan(directory, descriptor)
		if err != nil {
			return err
		}
		if err := runtime.tryAssemble(ctx, store.GenerationChunk{
			Repository: generation.Repository, Stage: ScheduleStage,
		}, directory, descriptor, domain, -1); err != nil {
			return err
		}
	}
	return nil
}

func (runtime *Runtime) reactivateComplete(ctx context.Context, generation Generation, directory string) error {
	settled := false
	for _, descriptor := range generation.Domains {
		domain, err := runtime.openDomainPlan(directory, descriptor)
		if err != nil {
			return err
		}
		root, present, err := readDomainRoot(filepath.Join(directory, domainKey(descriptor.Domain), rootName()), domain.Plan)
		if err != nil || !present {
			continue
		}
		if root.Disposition == candidate.PartitionResultTerminalRefusal || root.Disposition == candidate.PartitionResultRetryable {
			continue
		}
		releaseFence, err := runtime.Fence.FenceDomain(ctx, FenceRequest{Plan: domain.Plan})
		if err != nil {
			return errors.Join(ErrStale, err)
		}
		if releaseFence == nil {
			return invalid("publication fence returned no release")
		}
		if err := runtime.Publisher.PublishDomain(ctx, domain.Plan, root, domain.RunID); err != nil {
			releaseFence()
			return err
		}
		pointer := Pointer{Schema: PointerSchema, Repository: domain.Plan.Repository,
			Domain: domain.Plan.Domain, GenerationDigest: generation.Digest,
			PlanDigest: domain.Plan.Digest, RootDigest: root.Digest,
			RootName: filepath.Join(domainKey(domain.Plan.Domain), rootName()),
		}
		if err := writeAtomicCanonical(runtime.currentPath(domain.Plan.Repository, domain.Plan.Domain), pointer); err != nil {
			releaseFence()
			return err
		}
		settled = true
		releaseFence()
	}
	if settled && runtime.OnSettled != nil {
		return runtime.OnSettled(ctx, generation.Repository)
	}
	return nil
}

func readDomainRoot(path string, plan candidate.DomainResultPlan) (candidate.DomainResultRoot, bool, error) {
	raw, err := readBounded(path, candidate.MaxDomainResultRootBytes)
	if errors.Is(err, os.ErrNotExist) {
		return candidate.DomainResultRoot{}, false, nil
	}
	if err != nil {
		return candidate.DomainResultRoot{}, false, err
	}
	root, err := candidate.DecodeDomainResultRoot(bytes.NewReader(raw), plan)
	return root, err == nil, err
}

func (runtime *Runtime) generationSettled(generation Generation, directory string) bool {
	for _, descriptor := range generation.Domains {
		domain, err := runtime.openDomainPlan(directory, descriptor)
		if err != nil {
			return false
		}
		_, present, err := readDomainRoot(filepath.Join(directory, domainKey(descriptor.Domain), rootName()), domain.Plan)
		if err != nil || !present {
			return false
		}
	}
	return true
}

func (runtime *Runtime) readCurrentPointer(repository, domainName string) (Pointer, error) {
	raw, err := readBounded(runtime.currentPath(repository, domainName), MaxPointerBytes)
	if err != nil {
		return Pointer{}, err
	}
	var pointer Pointer
	if decodeExact(raw, &pointer) != nil || pointer.Schema != PointerSchema ||
		pointer.Repository != repository || pointer.Domain != domainName ||
		!validDigest(pointer.GenerationDigest) || !validDigest(pointer.PlanDigest) ||
		!validDigest(pointer.RootDigest) ||
		pointer.RootName != filepath.Join(domainKey(domainName), rootName()) {
		return Pointer{}, invalid("current pointer")
	}
	return pointer, nil
}

// Current validates the pointer and exact root before returning it.
func (runtime *Runtime) Current(
	ctx context.Context,
	repository, domainName string,
) (candidate.DomainResultRoot, error) {
	if runtime == nil || ctx == nil || reponame.Validate(repository) != nil || !boundedIdentity(domainName, 128) {
		return candidate.DomainResultRoot{}, invalid("current scope")
	}
	pointer, err := runtime.readCurrentPointer(repository, domainName)
	if err != nil {
		return candidate.DomainResultRoot{}, err
	}
	directory := runtime.generationDirectory(repository, pointer.GenerationDigest)
	generation, err := runtime.openGeneration(directory, repository, pointer.GenerationDigest)
	if err != nil {
		return candidate.DomainResultRoot{}, err
	}
	index := slices.IndexFunc(generation.Domains, func(value DomainDescriptor) bool { return value.Domain == domainName })
	if index < 0 {
		return candidate.DomainResultRoot{}, invalid("current domain is absent")
	}
	domain, err := runtime.openDomainPlan(directory, generation.Domains[index])
	if err != nil {
		return candidate.DomainResultRoot{}, err
	}
	root, present, err := readDomainRoot(filepath.Join(directory, domainKey(domainName), rootName()), domain.Plan)
	if err != nil || !present || root.Digest != pointer.RootDigest {
		return candidate.DomainResultRoot{}, errors.Join(err, invalid("current root"))
	}
	return root, nil
}

// Status exposes only bounded result dispositions and counts. It never opens
// Source content or evidence rows.
func (runtime *Runtime) Status(
	ctx context.Context,
	repository, generationDigest string,
) (Status, error) {
	if runtime == nil || ctx == nil || reponame.Validate(repository) != nil || !validDigest(generationDigest) {
		return Status{}, invalid("status scope")
	}
	directory := runtime.generationDirectory(repository, generationDigest)
	generation, err := runtime.openGeneration(directory, repository, generationDigest)
	if err != nil {
		return Status{}, err
	}
	status := Status{Repository: repository, Generation: generationDigest, Domains: make([]DomainStatus, 0, len(generation.Domains))}
	for _, descriptor := range generation.Domains {
		domain, err := runtime.openDomainPlan(directory, descriptor)
		if err != nil {
			return Status{}, err
		}
		item := DomainStatus{Domain: descriptor.Domain, PlanDigest: descriptor.PlanDigest, Expected: len(domain.Plan.Expected)}
		for ordinal := range domain.Plan.Expected {
			result, present, err := readPartitionResult(filepath.Join(directory, domainKey(descriptor.Domain), resultName(ordinal)), domain.Plan, ordinal)
			if err != nil {
				return Status{}, err
			}
			if present {
				item.Settled++
				if result.Disposition == candidate.PartitionResultTerminalRefusal || result.Disposition == candidate.PartitionResultRetryable {
					item.Failed++
				}
				if result.Disposition == candidate.PartitionResultRetryable && result.Reason == RetryFailureReason {
					item.RetryExhausted++
				}
			}
		}
		root, present, err := readDomainRoot(filepath.Join(directory, domainKey(descriptor.Domain), rootName()), domain.Plan)
		if err != nil {
			return Status{}, err
		}
		if present {
			item.RootDigest, item.Disposition = root.Digest, root.Disposition
		}
		if current, err := runtime.Current(ctx, repository, descriptor.Domain); err == nil {
			item.Current = current.PlanDigest == descriptor.PlanDigest
		}
		status.Domains = append(status.Domains, item)
	}
	return status, nil
}

// Progress returns a weakly timed but generation-fenced operational snapshot.
// An absent schedule is an exact unavailable state for the current instant,
// not proof that no extraction job exists.
func (runtime *Runtime) Progress(ctx context.Context, repository string) (Progress, error) {
	if runtime == nil || ctx == nil || reponame.Validate(repository) != nil {
		return Progress{}, invalid("progress scope")
	}
	schedule, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if errors.Is(err, store.ErrNotFound) {
		return Progress{State: "unavailable"}, nil
	}
	if err != nil {
		return Progress{}, err
	}
	if store.ValidateGenerationSchedule(*schedule) != nil || schedule.Repository != repository ||
		schedule.Stage != ScheduleStage || schedule.ResourceClass != store.GenerationResourceExtraction {
		return Progress{}, invalid("progress schedule")
	}
	target, err := runtime.scheduleTarget(repository, schedule.Generation)
	if err != nil {
		return Progress{}, err
	}
	generation, err := runtime.openGeneration(
		runtime.generationDirectory(repository, target), repository, target,
	)
	if err != nil {
		return Progress{}, err
	}
	if schedule.TotalItems != int64(generation.WorkItems) {
		return Progress{}, invalid("progress generation schedule")
	}
	progress := Progress{
		State: string(schedule.Status), Total: schedule.TotalChunks,
		Materialized: schedule.Materialized, Pending: schedule.Pending,
		Running: schedule.Running, Succeeded: schedule.Succeeded,
		Failed: schedule.Failed, Domains: len(generation.Domains),
	}
	for _, descriptor := range generation.Domains {
		pointer, pointerErr := runtime.readCurrentPointer(repository, descriptor.Domain)
		if errors.Is(pointerErr, os.ErrNotExist) {
			continue
		}
		if pointerErr != nil {
			return Progress{}, pointerErr
		}
		if pointer.GenerationDigest == target && pointer.PlanDigest == descriptor.PlanDigest {
			progress.CurrentDomains++
		}
	}
	confirmed, err := runtime.Store.GetGenerationSchedule(ctx, repository, ScheduleStage)
	if err != nil || !equalGenerationSchedule(confirmed, schedule) {
		return Progress{}, errors.Join(err, ErrStale)
	}
	if progress.State == string(store.GenerationScheduleSettled) &&
		progress.Failed == 0 && progress.Succeeded == progress.Total &&
		progress.CurrentDomains == progress.Domains {
		progress.State = "current"
	}
	return progress, nil
}

func equalGenerationSchedule(left, right *store.GenerationSchedule) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Schema != right.Schema || left.Digest != right.Digest ||
		left.Repository != right.Repository || left.Stage != right.Stage ||
		left.Generation != right.Generation || left.ResourceClass != right.ResourceClass ||
		left.TotalItems != right.TotalItems || left.ChunkItems != right.ChunkItems ||
		left.TotalChunks != right.TotalChunks || left.MaxAttempts != right.MaxAttempts ||
		left.RepositoryTokens != right.RepositoryTokens || left.NextOffset != right.NextOffset ||
		left.Materialized != right.Materialized || left.Pending != right.Pending ||
		left.Running != right.Running || left.Succeeded != right.Succeeded ||
		left.Failed != right.Failed || left.Status != right.Status ||
		!left.CreatedAt.Equal(right.CreatedAt) || !left.UpdatedAt.Equal(right.UpdatedAt) {
		return false
	}
	if left.LastClaimedAt == nil || right.LastClaimedAt == nil {
		return left.LastClaimedAt == right.LastClaimedAt
	}
	return left.LastClaimedAt.Equal(*right.LastClaimedAt)
}
