package t421

import (
	"errors"
	"fmt"
	"math"
	"slices"

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
	if plan.Correction != nil {
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
	return nil
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
