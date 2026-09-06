package t421

import (
	"math"
	"testing"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

// These small control-projection counterexamples do not model a completed
// corpus. The separate ordinary-pass test supplies native full-corpus IDs.
func TestRecoveryPreparationRejectsMissingWorkAndDestructiveRecipes(t *testing.T) {
	plan := Plan{Schema: PlanV2Schema, Correction: &ContractCorrection{RecoveryPreparations: correctedRecoveryPreparations()}}
	plan.Profile.Pipeline.ExtractionDomains = frozenExtractionDomains()
	for _, row := range plan.Correction.RecoveryPreparations {
		t.Run(row.Phase, func(t *testing.T) {
			files, queries, boundsErr := recoveryPreparationReadBounds(plan, row)
			if boundsErr != nil {
				t.Fatal(boundsErr)
			}
			target, prior := SHA256([]byte("test-target")), SHA256([]byte("test-prior"))
			generation := SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + target + "\x00" + prior))
			schedule, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
				Repository: t401.RepositoryName, Stage: extractionpublication.ScheduleStage, Generation: generation,
				ResourceClass: store.GenerationResourceExtraction, TotalItems: int64(row.Chunks), ChunkItems: extractionpublication.ScheduleChunkItems,
				MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
			})
			if err != nil {
				t.Fatal(err)
			}
			authority := AuthorityPhaseResult{AuthorityState: AuthorityState{ExtractionRootsSHA256: SHA256([]byte("test-result-set"))}}
			digest := SHA256([]byte("test-product-authority"))
			p := RecoveryPreparationResult{
				Schema: "t422-native-recovery-preparation-v1", Phase: row.Phase, PrepareEventOrdinal: 2,
				AuthoritySHA256: digest, PreservedRootsSHA256: authority.ExtractionRootsSHA256,
				TargetGenerationSHA256: target, PriorScheduleSHA256: prior, RecoveryGenerationSHA256: generation, RecoveryScheduleSHA256: schedule,
				ScheduleWrites: row.ScheduleWrites, Chunks: row.Chunks, Starts: row.Starts, Successes: row.Successes, Requeues: row.Requeues,
				PreparationCompletionWrites: row.PreparationCompletionWrites, PreparationDeletes: row.PreparationDeletes,
				RecoveryCompletionWrites: row.RecoveryCompletionWrites, RecoveryRootInstalls: row.RecoveryRootInstalls,
				PublicationCalls: row.PublicationCalls.Minimum, StoreAuthorityUnchanged: true, PreparedBeforeArm: true, DirectoriesSynced: true, LocksReleasedBeforeWait: true,
				ControlFileReads: files.Minimum, StoreReadAttempts: queries.Minimum, StoreWriteAttempts: 1,
			}
			value := InjectionTransition{Target: InjectionTargetProjection{Phase: row.Phase, GenerationSHA256: target, ScheduleSHA256: schedule},
				ArmEventOrdinal: 3, AuthorityBeforeSHA256: digest, AuthorityAfterSHA256: digest, Preparation: &p}
			metrics := ReceiptMetrics{PublicationWrites: CountMetric(p.PublicationCalls), JobAttempts: CountMetric(p.Starts), StoreTransactions: CountMetric(p.ScheduleWrites),
				ControlReads: CountMetric(p.ControlFileReads + p.StoreReadAttempts)}
			if err := validateRecoveryPreparation(value, authority, metrics, 1, plan); err != nil {
				t.Fatal(err)
			}
			for name, mutate := range map[string]func(*RecoveryPreparationResult){
				"no_schedule_write":         func(p *RecoveryPreparationResult) { p.ScheduleWrites = 0 },
				"missing_preparation_event": func(p *RecoveryPreparationResult) { p.PrepareEventOrdinal = 0 },
				"late_preparation":          func(p *RecoveryPreparationResult) { p.PrepareEventOrdinal = value.ArmEventOrdinal },
				"missing_chunks":            func(p *RecoveryPreparationResult) { p.Chunks-- },
				"missing_recovery_start":    func(p *RecoveryPreparationResult) { p.Starts-- },
				"job_reset":                 func(p *RecoveryPreparationResult) { p.OldJobsReset = 1 },
				"result_removal":            func(p *RecoveryPreparationResult) { p.ResultFilesRemoved = 1 },
				"sealed_evidence_removal":   func(p *RecoveryPreparationResult) { p.EvidenceRowsRemoved = 1 },
				"extra_control_deletion":    func(p *RecoveryPreparationResult) { p.PreparationDeletes++ },
				"changed_product_authority": func(p *RecoveryPreparationResult) { p.StoreAuthorityUnchanged = false },
				"locks_held_while_waiting":  func(p *RecoveryPreparationResult) { p.LocksReleasedBeforeWait = false },
				"invented_schedule":         func(p *RecoveryPreparationResult) { p.RecoveryScheduleSHA256 = prior },
				"unmetered_file_reads":      func(p *RecoveryPreparationResult) { p.ControlFileReads = 0 },
				"unmetered_store_reads":     func(p *RecoveryPreparationResult) { p.StoreReadAttempts = 0 },
				"unmetered_write_attempt":   func(p *RecoveryPreparationResult) { p.StoreWriteAttempts = 0 },
			} {
				t.Run(name, func(t *testing.T) {
					changed := p
					mutate(&changed)
					invalid := value
					invalid.Preparation = &changed
					if validateRecoveryPreparation(invalid, authority, metrics, 1, plan) == nil {
						t.Fatal("invalid preparation accepted")
					}
				})
			}
			missing := value
			missing.Preparation = nil
			if validateRecoveryPreparation(missing, authority, metrics, 1, plan) == nil {
				t.Fatal("missing preparation accepted")
			}
			if validateRecoveryPreparation(value, authority, ReceiptMetrics{}, 1, plan) == nil {
				t.Fatal("unmetered preparation accepted")
			}
			if validateRecoveryPreparation(value, authority, metrics, 1, Plan{Schema: PlanSchema}) == nil {
				t.Fatal("v1 accepted prospective preparation")
			}
		})
	}
}

func TestRecoveryPreparationReadAccounting(t *testing.T) {
	for _, version := range []struct {
		schema           string
		minimum, maximum uint64
	}{
		{PlanV2Schema, 23, 275},
		{PlanV3Schema, 24, 339},
	} {
		t.Run(version.schema, func(t *testing.T) {
			plan := Plan{Schema: version.schema}
			plan.Profile.Pipeline.ExtractionDomains = frozenExtractionDomains()
			for index, row := range correctedRecoveryPreparations() {
				t.Run(row.Phase, func(t *testing.T) {
					files, queries, err := recoveryPreparationReadBounds(plan, row)
					if err != nil || files.Minimum != 116+uint64(index) || files.Maximum != files.Minimum+1 || queries.Minimum != version.minimum || queries.Maximum != version.maximum {
						t.Fatalf("read bounds: files=%+v queries=%+v err=%v", files, queries, err)
					}
					warm := RecoveryPreparationResult{ControlFileReads: files.Minimum, StoreReadAttempts: queries.Minimum,
						StoreWriteAttempts: 1, OtherPhaseControlReads: 7}
					measured := func(p RecoveryPreparationResult) ReceiptMetrics {
						return ReceiptMetrics{ControlReads: CountMetric(p.ControlFileReads + p.StoreReadAttempts + p.OtherPhaseControlReads),
							MemberReads: CountMetric(p.MemberReads), StoreTransactions: CountMetric(p.StoreWriteAttempts)}
					}
					if err := validateRecoveryPreparationReads(warm, row, measured(warm), plan); err != nil {
						t.Fatal(err)
					}
					coldMinimum, err := recoveryPreparationColdMemberMinimum(plan.Profile.Pipeline.ExtractionDomains)
					if err != nil || coldMinimum != 470_732 {
						t.Fatalf("cold minimum=%d err=%v", coldMinimum, err)
					}
					cold := warm
					cold.CandidateColdOpens, cold.MemberReads = 1, coldMinimum
					cold.ControlFileReads++
					for name, p := range map[string]RecoveryPreparationResult{
						"cold_artifacts_and_all_spool_reads": cold,
						"existing_binding_reread": func() RecoveryPreparationResult {
							value := warm
							value.ControlFileReads++
							return value
						}(),
						"native_conflict_attempts": func() RecoveryPreparationResult {
							value := warm
							value.StoreReadAttempts, value.StoreWriteAttempts = queries.Maximum, recoveryPreparationStoreAttempts
							return value
						}(),
					} {
						t.Run(name, func(t *testing.T) {
							if err := validateRecoveryPreparationReads(p, row, measured(p), plan); err != nil {
								t.Fatal(err)
							}
						})
					}
					for name, mutate := range map[string]func(*RecoveryPreparationResult){
						"missing_file":        func(p *RecoveryPreparationResult) { p.ControlFileReads-- },
						"unplanned_file":      func(p *RecoveryPreparationResult) { p.ControlFileReads += 2 },
						"missing_query":       func(p *RecoveryPreparationResult) { p.StoreReadAttempts-- },
						"extra_query_attempt": func(p *RecoveryPreparationResult) { p.StoreReadAttempts = queries.Maximum + 1 },
						"missing_write":       func(p *RecoveryPreparationResult) { p.StoreWriteAttempts = 0 },
						"extra_write_attempt": func(p *RecoveryPreparationResult) { p.StoreWriteAttempts = recoveryPreparationStoreAttempts + 1 },
						"extra_cold_open":     func(p *RecoveryPreparationResult) { p.CandidateColdOpens = 2 },
						"warm_member_read":    func(p *RecoveryPreparationResult) { p.MemberReads = 1 },
						"cold_omits_manifest": func(p *RecoveryPreparationResult) { p.CandidateColdOpens, p.MemberReads = 1, coldMinimum },
						"cold_omits_spool": func(p *RecoveryPreparationResult) {
							*p = cold
							p.MemberReads = 53_204
						},
						"cold_undercounted": func(p *RecoveryPreparationResult) {
							*p = cold
							p.MemberReads--
						},
					} {
						t.Run(name, func(t *testing.T) {
							p := warm
							mutate(&p)
							if validateRecoveryPreparationReads(p, row, measured(p), plan) == nil {
								t.Fatal("invalid native read projection accepted")
							}
						})
					}
					for name, mutate := range map[string]func(*ReceiptMetrics){
						"aggregate_omits_prep": func(m *ReceiptMetrics) { m.ControlReads = CountMetric(warm.OtherPhaseControlReads) },
						"aggregate_omits_other": func(m *ReceiptMetrics) {
							m.ControlReads = CountMetric(warm.ControlFileReads + warm.StoreReadAttempts)
						},
						"aggregate_double_counts": func(m *ReceiptMetrics) { m.ControlReads++ },
						"missing_write_attempts":  func(m *ReceiptMetrics) { m.StoreTransactions-- },
					} {
						t.Run(name, func(t *testing.T) {
							metrics := measured(warm)
							mutate(&metrics)
							if validateRecoveryPreparationReads(warm, row, metrics, plan) == nil {
								t.Fatal("inexact phase accounting accepted")
							}
						})
					}
					t.Run("cold_missing_phase_member_charge", func(t *testing.T) {
						metrics := measured(cold)
						metrics.MemberReads--
						if validateRecoveryPreparationReads(cold, row, metrics, plan) == nil {
							t.Fatal("cold member work disappeared from phase")
						}
					})
					t.Run("query_retries_missing_from_phase", func(t *testing.T) {
						p := warm
						p.StoreReadAttempts++
						if validateRecoveryPreparationReads(p, row, measured(warm), plan) == nil {
							t.Fatal("native query retry disappeared from phase")
						}
					})
					t.Run("subtotal_overflow", func(t *testing.T) {
						p := warm
						p.OtherPhaseControlReads = math.MaxUint64
						metrics := ReceiptMetrics{ControlReads: CountMetric(p.ControlFileReads + p.StoreReadAttempts - 1), StoreTransactions: 1}
						if validateRecoveryPreparationReads(p, row, metrics, plan) == nil {
							t.Fatal("wrapped subtotal accepted")
						}
					})
				})
			}
		})
	}
}

func TestRecoveryPreparationColdMemberFloor(t *testing.T) {
	for _, test := range []struct{ records, reads uint64 }{
		{1, 2}, {511, 1_022}, {512, 1_024}, {513, 1_539},
		{1_023, 3_069}, {1_024, 3_072}, {1_025, 4_099},
		{1_536, 5_632}, {2_047, 8_188}, {2_048, 8_192}, {2_049, 10_243},
	} {
		domains := []ExtractionDomainProfile{{Domain: "proto-contract", CandidateRecords: test.records}}
		got, err := recoveryPreparationColdMemberMinimum(domains)
		if err != nil || got != test.reads {
			t.Fatalf("records=%d reads=%d want=%d err=%v", test.records, got, test.reads, err)
		}
	}
	t.Run("overlapping_domains_not_summed", func(t *testing.T) {
		domains := []ExtractionDomainProfile{
			{Domain: "proto-contract", CandidateRecords: 512}, {Domain: "scip-proto-field", CandidateRecords: 512},
			{Domain: "grpc-caller", CandidateRecords: 1}, {Domain: "thrift-caller", CandidateRecords: 1},
		}
		got, err := recoveryPreparationColdMemberMinimum(domains)
		if err != nil || got != 1_539 {
			t.Fatalf("overlapping plane floor=%d err=%v", got, err)
		}
	})
	for _, records := range []uint64{0, math.MaxUint64} {
		if _, err := recoveryPreparationColdMemberMinimum([]ExtractionDomainProfile{{Domain: "proto-contract", CandidateRecords: records}}); err == nil {
			t.Fatalf("invalid record population %d accepted", records)
		}
	}
}

func TestRecoveryPreparationLineageAndReportBinding(t *testing.T) {
	plan := Plan{Schema: PlanV2Schema, Correction: &ContractCorrection{RecoveryPreparations: correctedRecoveryPreparations()}}
	authority := make(map[string]AuthorityPhaseResult, 3)
	var target, prior string
	for index, phase := range []string{"cold", "physical_delta_b", "return_a"} {
		target = SHA256([]byte("test-target-" + phase))
		// Deliberately different small partition inventories catch deriving all
		// ordinary schedules from the final recovery phase's chunk count.
		partitions := uint64(index + 2)
		if phase == "return_a" {
			partitions = plan.Correction.RecoveryPreparations[0].Chunks
		}
		authority[phase] = AuthorityPhaseResult{Phase: phase, Outcome: "passed", ExtractionRoots: []ExtractionRootResult{
			{GenerationSHA256: target, ApplicablePartitions: 1},
			{GenerationSHA256: target, ApplicablePartitions: partitions - 1},
		}}
		generation := target
		if prior != "" {
			generation = SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + target + "\x00" + prior))
		}
		prior = testExtractionScheduleDigest(t, generation, partitions)
	}
	var values []TransitionResult
	for _, row := range plan.Correction.RecoveryPreparations {
		p := &RecoveryPreparationResult{Phase: row.Phase, TargetGenerationSHA256: target,
			PriorScheduleSHA256: prior, RecoveryScheduleSHA256: SHA256([]byte(row.Phase)), Chunks: row.Chunks}
		values = append(values, TransitionResult{Phase: row.Phase, Outcome: "passed",
			Injections: []InjectionTransition{{Preparation: p}}})
		prior = p.RecoveryScheduleSHA256
	}
	if err := validateRecoveryPreparationLineage(values, authority, plan); err != nil {
		t.Fatal(err)
	}
	t.Run("reject_target_only_return_a_schedule", func(t *testing.T) {
		invalid := append([]TransitionResult(nil), values...)
		changed := *values[0].Injections[0].Preparation
		changed.PriorScheduleSHA256 = testExtractionScheduleDigest(t, target, changed.Chunks)
		invalid[0].Injections = []InjectionTransition{{Preparation: &changed}}
		if validateRecoveryPreparationLineage(invalid, authority, plan) == nil {
			t.Fatal("preparation reset return-A to a fresh schedule instead of following cold and physical B")
		}
	})
	for name, mutate := range map[string]func(map[string]AuthorityPhaseResult){
		"missing_cold": func(values map[string]AuthorityPhaseResult) { delete(values, "cold") },
		"incomplete_physical_b": func(values map[string]AuthorityPhaseResult) {
			value := values["physical_delta_b"]
			value.Outcome = "stopped"
			values["physical_delta_b"] = value
		},
		"empty_return_a": func(values map[string]AuthorityPhaseResult) {
			value := values["return_a"]
			value.ExtractionRoots = nil
			values["return_a"] = value
		},
		"mixed_generation": func(values map[string]AuthorityPhaseResult) {
			values["return_a"].ExtractionRoots[1].GenerationSHA256 = SHA256([]byte("other-generation"))
		},
		"wrong_cold_partitions": func(values map[string]AuthorityPhaseResult) {
			values["cold"].ExtractionRoots[0].ApplicablePartitions++
		},
		"overflow_partitions": func(values map[string]AuthorityPhaseResult) {
			values["return_a"].ExtractionRoots[1].ApplicablePartitions = math.MaxUint64
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := make(map[string]AuthorityPhaseResult, len(authority))
			for phase, value := range authority {
				value.ExtractionRoots = append([]ExtractionRootResult(nil), value.ExtractionRoots...)
				changed[phase] = value
			}
			mutate(changed)
			if validateRecoveryPreparationLineage(values, changed, plan) == nil {
				t.Fatal("invalid ordinary schedule lineage accepted")
			}
		})
	}
	if err := validateRecoveryPreparationLineage(nil, nil, plan); err != nil {
		t.Fatalf("unreached preparation required future ordinary authority: %v", err)
	}
	values[1].Injections[0].Preparation.PriorScheduleSHA256 = values[0].Injections[0].Preparation.PriorScheduleSHA256
	if validateRecoveryPreparationLineage(values, authority, plan) == nil {
		t.Fatal("second preparation reused the pre-stale schedule")
	}
	value := values[0].Injections[0]
	hit, err := injectionHitReportSHA256(value, FailurePoint{})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := injectionRecoveryProjectionSHA256(value, FailurePoint{})
	if err != nil {
		t.Fatal(err)
	}
	value.Preparation.PublicationCalls++
	laterHit, _ := injectionHitReportSHA256(value, FailurePoint{})
	laterRecovery, _ := injectionRecoveryProjectionSHA256(value, FailurePoint{})
	if laterHit != hit || laterRecovery == recovery {
		t.Fatal("hit report included future recovery work or recovery omitted it")
	}
	value.Preparation.PreparationDeletes++
	changedHit, _ := injectionHitReportSHA256(value, FailurePoint{})
	if changedHit == hit {
		t.Fatal("hit report did not bind preparation mutations")
	}
}

func testExtractionScheduleDigest(t *testing.T, generation string, partitions uint64) string {
	t.Helper()
	value, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: t401.RepositoryName, Stage: extractionpublication.ScheduleStage, Generation: generation,
		ResourceClass: store.GenerationResourceExtraction, TotalItems: int64(partitions),
		ChunkItems: extractionpublication.ScheduleChunkItems, MaxAttempts: extractionpublication.ScheduleMaxAttempts,
		RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
