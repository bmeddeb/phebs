package t421

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

// RecoveryPreparationResult records a private, one-shot native preparation.
// Operational schedules are not immutable evidence authority. In v2 they live
// here, not in ExtractionRootResult's retained-v1 schedule field.
type RecoveryPreparationResult struct {
	Schema                      string `json:"schema"`
	Phase                       string `json:"phase"`
	PrepareEventOrdinal         uint64 `json:"prepare_event_ordinal"`
	AuthoritySHA256             string `json:"authority_sha256"`
	PreservedRootsSHA256        string `json:"preserved_roots_sha256"`
	TargetGenerationSHA256      string `json:"target_generation_sha256"`
	PriorScheduleSHA256         string `json:"prior_schedule_sha256"`
	RecoveryGenerationSHA256    string `json:"recovery_generation_sha256"`
	RecoveryScheduleSHA256      string `json:"recovery_schedule_sha256"`
	ScheduleWrites              uint64 `json:"schedule_writes"`
	Chunks                      uint64 `json:"chunks"`
	Starts                      uint64 `json:"starts"`
	Successes                   uint64 `json:"successes"`
	Requeues                    uint64 `json:"requeues"`
	PreparationCompletionWrites uint64 `json:"preparation_completion_writes"`
	PreparationDeletes          uint64 `json:"preparation_deletes"`
	RecoveryCompletionWrites    uint64 `json:"recovery_completion_writes"`
	RecoveryRootInstalls        uint64 `json:"recovery_root_installs"`
	PublicationCalls            uint64 `json:"publication_calls"`
	ControlFileReads            uint64 `json:"control_file_reads"`
	StoreReadAttempts           uint64 `json:"store_read_attempts"`
	CandidateColdOpens          uint64 `json:"candidate_cold_opens"`
	MemberReads                 uint64 `json:"member_reads"`
	StoreWriteAttempts          uint64 `json:"store_write_attempts"`
	OtherPhaseControlReads      uint64 `json:"other_phase_control_reads"`
	OldJobsReset                uint64 `json:"old_jobs_reset"`
	ResultFilesRemoved          uint64 `json:"result_files_removed"`
	EvidenceRowsRemoved         uint64 `json:"evidence_rows_removed"`
	StoreAuthorityUnchanged     bool   `json:"store_authority_unchanged"`
	PreparedBeforeArm           bool   `json:"prepared_before_arm"`
	DirectoriesSynced           bool   `json:"directories_synced"`
	LocksReleasedBeforeWait     bool   `json:"locks_released_before_wait"`
}

func validateRecoveryPreparation(value InjectionTransition, authority AuthorityPhaseResult, metrics ReceiptMetrics, start uint64, plan Plan) error {
	index := -1
	if correctedPlanSemantics(plan.Schema) && plan.Correction != nil {
		index = slices.IndexFunc(plan.Correction.RecoveryPreparations, func(row RecoveryPreparation) bool { return row.Phase == value.Target.Phase })
	}
	if index < 0 {
		if value.Preparation != nil {
			return errors.New("unplanned recovery preparation")
		}
		return nil
	}
	if value.Preparation == nil {
		return errors.New("recovery lacks frozen preparation")
	}
	p := value.Preparation
	want := plan.Correction.RecoveryPreparations[index]
	generation := SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + value.Target.GenerationSHA256 + "\x00" + p.PriorScheduleSHA256))
	schedule, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: t401.RepositoryName, Stage: extractionpublication.ScheduleStage,
		Generation: generation, ResourceClass: store.GenerationResourceExtraction,
		TotalItems: int64(want.Chunks), ChunkItems: extractionpublication.ScheduleChunkItems,
		MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
	})
	if err != nil || p.Schema != "t422-native-recovery-preparation-v1" || p.Phase != want.Phase ||
		p.PrepareEventOrdinal <= start || p.PrepareEventOrdinal >= value.ArmEventOrdinal ||
		p.AuthoritySHA256 != value.AuthorityBeforeSHA256 || p.AuthoritySHA256 != value.AuthorityAfterSHA256 ||
		p.PreservedRootsSHA256 != authority.ExtractionRootsSHA256 || p.TargetGenerationSHA256 != value.Target.GenerationSHA256 ||
		!validDigest(p.PriorScheduleSHA256) || p.PriorScheduleSHA256 == schedule ||
		p.RecoveryGenerationSHA256 != generation || p.RecoveryScheduleSHA256 != schedule || value.Target.ScheduleSHA256 != schedule ||
		p.ScheduleWrites != want.ScheduleWrites || p.Chunks != want.Chunks || p.Starts != want.Starts ||
		p.Successes != want.Successes || p.Requeues != want.Requeues ||
		p.PreparationCompletionWrites != want.PreparationCompletionWrites || p.PreparationDeletes != want.PreparationDeletes ||
		p.RecoveryCompletionWrites != want.RecoveryCompletionWrites || p.RecoveryRootInstalls != want.RecoveryRootInstalls ||
		p.PublicationCalls < want.PublicationCalls.Minimum || p.PublicationCalls > want.PublicationCalls.Maximum ||
		uint64(metrics.PublicationWrites) < p.PublicationCalls || uint64(metrics.JobAttempts) < p.Starts || uint64(metrics.StoreTransactions) < p.ScheduleWrites ||
		p.OldJobsReset != 0 || p.ResultFilesRemoved != 0 || p.EvidenceRowsRemoved != 0 ||
		!p.StoreAuthorityUnchanged || !p.PreparedBeforeArm || !p.DirectoriesSynced || !p.LocksReleasedBeforeWait {
		return errors.New("recovery preparation differs from the frozen native transition")
	}
	return validateRecoveryPreparationReads(*p, want, metrics, plan)
}

// The native store's internal transaction/query conflict loop is independent
// of generation-job retries. This pins internal/store.maxQueueRetries, not the
// five-attempt generation budget. Preparation itself is never retried.
const recoveryPreparationStoreAttempts = uint64(64)

// Bounds are for one warm native call; a cold candidate open adds one manifest
// control. The optional extra file read is an existing successor-binding check.
func recoveryPreparationReadBounds(plan Plan, row RecoveryPreparation) (CounterBound, CounterBound, error) {
	domains := plan.Profile.Pipeline.ExtractionDomains
	if len(domains) == 0 || len(domains) > extractionpublication.MaxDomains || row.PreparationCompletionWrites > 1 {
		return CounterBound{}, CounterBound{}, errors.New("recovery preparation read inventory is invalid")
	}
	var partitions uint64
	for _, domain := range domains {
		if domain.ApplicablePartitions > uint64(candidate.MaxDomainResultPartitions) {
			return CounterBound{}, CounterBound{}, errors.New("recovery preparation partition read bound is invalid")
		}
		partitions += domain.ApplicablePartitions
	}
	if partitions != row.Chunks {
		return CounterBound{}, CounterBound{}, errors.New("recovery preparation read inventory differs from its schedule")
	}
	domainCount := uint64(len(domains))
	// Core: generation/selected-plan/six bindings + four controls per domain
	// + every result + optional checkpoint reread. The main callback adds four
	// latest/latest/source/latest authority confirmations (sixteen controls).
	files := 24 + 4*domainCount + partitions + row.PreparationCompletionWrites
	return CounterBound{Minimum: files, Maximum: files + 1}, CounterBound{
		Minimum: domainCount + 10 + 4, Maximum: domainCount + 10 + 4*recoveryPreparationStoreAttempts,
	}, nil
}

func validateRecoveryPreparationReads(p RecoveryPreparationResult, row RecoveryPreparation, metrics ReceiptMetrics, plan Plan) error {
	files, queries, err := recoveryPreparationReadBounds(plan, row)
	if err != nil || p.CandidateColdOpens > 1 {
		return errors.New("recovery preparation read inventory is invalid")
	}
	if p.ControlFileReads < files.Minimum+p.CandidateColdOpens || p.ControlFileReads > files.Maximum+p.CandidateColdOpens ||
		p.StoreReadAttempts < queries.Minimum || p.StoreReadAttempts > queries.Maximum ||
		p.StoreWriteAttempts == 0 || p.StoreWriteAttempts > recoveryPreparationStoreAttempts ||
		uint64(metrics.StoreTransactions) < p.StoreWriteAttempts {
		return errors.New("recovery preparation read/write attempts differ from the native call graph")
	}
	if p.CandidateColdOpens == 0 {
		if p.MemberReads != 0 {
			return errors.New("warm preparation retained candidate member reads")
		}
	} else {
		minimum, err := recoveryPreparationColdMemberMinimum(plan.Profile.Pipeline.ExtractionDomains)
		if err != nil || p.MemberReads < minimum {
			return errors.New("cold preparation omitted candidate artifact or projection reads")
		}
	}
	if uint64(metrics.MemberReads) < p.MemberReads ||
		p.StoreReadAttempts > math.MaxUint64-p.ControlFileReads {
		return errors.New("recovery preparation read subtotal is unavailable or overflows")
	}
	controls := p.ControlFileReads + p.StoreReadAttempts
	if p.OtherPhaseControlReads > math.MaxUint64-controls || uint64(metrics.ControlReads) != controls+p.OtherPhaseControlReads {
		return errors.New("recovery preparation and other scoped controls differ from the phase total")
	}
	return nil
}

func recoveryPreparationColdMemberMinimum(domains []ExtractionDomainProfile) (uint64, error) {
	var repository, caller uint64
	for _, domain := range domains {
		if domain.CandidateRecords > uint64(candidate.MaxCorpusEntries) {
			return 0, errors.New("candidate read population exceeds native admission")
		}
		if strings.HasSuffix(domain.Domain, "-caller") {
			caller = max(caller, domain.CandidateRecords)
		} else {
			repository = max(repository, domain.CandidateRecords)
		}
	}
	// Domains overlap within each plane, but the two physical planes are read
	// separately. Their largest admitted domain supplies a conservative record
	// floor without charging the same plane once for every extractor.
	records := repository + caller
	if records == 0 {
		return 0, errors.New("cold candidate read inventory is empty")
	}
	// Native candidate.projectionSorter uses 512-record runs, binary carries,
	// then a low-to-high final merge. Count only run lengths, never source data:
	// one artifact traversal, each merge input, and the final projection scan.
	reads := 2 * records
	var levels [64]uint64
	for remaining := records; remaining > 0; {
		run := min(remaining, uint64(512))
		remaining -= run
		for level := range levels {
			if levels[level] == 0 {
				levels[level] = run
				break
			}
			run += levels[level]
			reads += run
			levels[level] = 0
		}
	}
	var result uint64
	for _, run := range levels {
		if run == 0 {
			continue
		}
		if result != 0 {
			reads += result + run
		}
		result += run
	}
	return reads, nil
}

func injectionTargetMatchesPreparedExtraction(value InjectionTransition, root ExtractionRootResult) bool {
	if value.Preparation == nil {
		return injectionTargetMatchesExtraction(value.Target, root)
	}
	if root.ScheduleSHA256 != "" || value.Target.ScheduleSHA256 != value.Preparation.RecoveryScheduleSHA256 {
		return false
	}
	root.ScheduleSHA256 = value.Target.ScheduleSHA256
	return injectionTargetMatchesExtraction(value.Target, root)
}

func validateRecoveryPreparationLineage(values []TransitionResult, authority map[string]AuthorityPhaseResult, plan Plan) error {
	if plan.Correction == nil {
		return nil
	}
	var prior, target string
	var chunks uint64
	for _, row := range plan.Correction.RecoveryPreparations {
		transition, ok := namedTransition(values, row.Phase)
		if !ok || transition.Outcome != "passed" {
			break
		}
		if len(transition.Injections) != 1 || transition.Injections[0].Preparation == nil {
			return errors.New("recovery preparation lineage is incomplete")
		}
		p := transition.Injections[0].Preparation
		if prior == "" {
			var err error
			target, prior, chunks, err = ordinaryReturnASchedule(authority)
			if err != nil {
				return err
			}
		}
		if p.TargetGenerationSHA256 != target || p.PriorScheduleSHA256 != prior ||
			p.Chunks != chunks || row.Chunks != chunks {
			return errors.New("recovery preparation did not continue the exact prior native schedule")
		}
		prior = p.RecoveryScheduleSHA256
	}
	return nil
}

// ordinaryReturnASchedule follows Runtime.enqueue's predecessor-bound identity
// rule. Only cold has no predecessor; physical B and return A each inherit the
// preceding operational schedule. Logical-only replacement creates none.
// The authority map has already been resolved and validated by the receipt.
func ordinaryReturnASchedule(authority map[string]AuthorityPhaseResult) (string, string, uint64, error) {
	var target, prior string
	var chunks uint64
	for _, phase := range []string{"cold", "physical_delta_b", "return_a"} {
		value, ok := authority[phase]
		if !ok || value.Outcome != "passed" || len(value.ExtractionRoots) == 0 {
			return "", "", 0, fmt.Errorf("ordinary extraction schedule authority is absent for %q", phase)
		}
		target, chunks = value.ExtractionRoots[0].GenerationSHA256, 0
		for _, root := range value.ExtractionRoots {
			if !validDigest(root.GenerationSHA256) || root.GenerationSHA256 != target ||
				root.ApplicablePartitions > math.MaxInt64-chunks {
				return "", "", 0, fmt.Errorf("ordinary extraction schedule authority is invalid for %q", phase)
			}
			chunks += root.ApplicablePartitions
		}
		generation := target
		if prior != "" {
			generation = SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + target + "\x00" + prior))
		}
		var err error
		prior, err = store.GenerationScheduleDigest(store.GenerationScheduleSpec{
			Repository: t401.RepositoryName, Stage: extractionpublication.ScheduleStage,
			Generation: generation, ResourceClass: store.GenerationResourceExtraction,
			TotalItems: int64(chunks), ChunkItems: extractionpublication.ScheduleChunkItems,
			MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
		})
		if err != nil {
			return "", "", 0, fmt.Errorf("derive ordinary extraction schedule for %q: %w", phase, err)
		}
	}
	return target, prior, chunks, nil
}
