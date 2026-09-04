package t421

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

var (
	productionAuthorityOnce       sync.Once
	productionAuthorityCache      map[string]AuthorityPhaseResult
	productionRecoveryCache       map[string]productionRecoverySchedule
	productionAuthorityPlanSHA256 string
)

type productionRecoverySchedule struct {
	Target, Prior, RecoveryGeneration, RecoverySchedule string
}

func productionRecoveryScheduleFixture(t *testing.T, plan Plan, phase string) productionRecoverySchedule {
	t.Helper()
	productionAuthorityFixture(t, plan)
	value, ok := productionRecoveryCache[phase]
	if !ok {
		t.Fatalf("native recovery constructor fixture lacks %s", phase)
	}
	return value
}

func productionAuthorityFixture(t *testing.T, plan Plan) map[string]AuthorityPhaseResult {
	t.Helper()
	planSHA256 := mustReceiptSHA256(t, plan)
	productionAuthorityOnce.Do(func() {
		physical := productionPhysicalIdentities(t, plan)
		productionAuthorityCache = productionLogicalAuthorities(t, plan, physical)
		productionAuthorityPlanSHA256 = planSHA256
	})
	if productionAuthorityCache == nil {
		t.Fatal("native constructor fixture did not complete")
	}
	if productionAuthorityPlanSHA256 != planSHA256 {
		t.Fatal("native constructor fixture belongs to a different exact plan")
	}
	return productionAuthorityCache
}

// The accept side must admit identities constructed by the production graph.
// Measurements/signatures and the search leaf remain explicit test models;
// this proves contract satisfiability, never a live ordinary-worker scale pass.
func TestContractAdmitsProductionDerivedOrdinaryPass(t *testing.T) {
	plan := correctedTestPlan(t)
	commits := executionFreezeTestCommits()
	binding := admittedExecutionFreezeTestBinding(t, plan, commits)
	receipt := completeTestReceipt(t, plan, binding)
	returned := returnedPackageTestBinding(t, receipt, plan, binding)
	if err := ValidateReceipt(receipt, plan, binding, returned); err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production-derived constructor fixture bytes=%d (modeled measurements/search leaf; not ceremony evidence)", len(raw))
}

func TestContractRejectsImpossibleReaderRetentionEvidence(t *testing.T) {
	plan := correctedTestPlan(t)
	binding := admittedExecutionFreezeTestBinding(t, plan, executionFreezeTestCommits())
	base := completeTestReceipt(t, plan, binding)
	index := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "physical_delta_b"
	})
	measurement := slices.Index(plan.PhaseOrder, "physical_delta_b")
	if index < 0 || measurement < 0 || base.TransitionResults[index].Reader == nil ||
		base.TransitionResults[index].ReadAccounting == nil {
		t.Fatal("corrected reader fixture is absent")
	}
	for name, mutate := range map[string]func(*Receipt){
		"swapped_roles": func(value *Receipt) {
			reader := value.TransitionResults[index].Reader
			reader.OldRoleAfterReplacement, reader.NewRoleAfterReplacement = "current", "prior"
		},
		"post_release_not_found": func(value *Receipt) {
			value.TransitionResults[index].Reader.PostReleaseOldOutcome = "not_found"
		},
		"omitted_zero_scan": func(value *Receipt) {
			value.TransitionResults[index].Reader.HeldLifecycleScanned = nil
		},
		"held_scan": func(value *Receipt) {
			*value.TransitionResults[index].Reader.HeldLifecycleScanned = 1
		},
		"post_release_scan": func(value *Receipt) {
			*value.TransitionResults[index].Reader.PostReleaseLifecycleScanned = 1
		},
		"incomplete_lifecycle": func(value *Receipt) {
			value.TransitionResults[index].Reader.PostReleaseLifecycleOutcome = "lower_bound"
		},
		"released_old_reader": func(value *Receipt) {
			value.TransitionResults[index].Reader.OldReaderHeldThroughReprobe = false
		},
		"post_release_projection": func(value *Receipt) {
			value.TransitionResults[index].Reader.PostReleaseOldProjectionSHA256 = zeroDigest()
		},
		"legacy_delete_field": func(value *Receipt) {
			value.TransitionResults[index].Reader.DeletedAfterRelease = 1
		},
		"deleted_prior": func(value *Receipt) {
			value.Measurements[measurement].Metrics.LifecycleDeleted = 1
			value.Measurements[measurement].Metrics.MaxLifecycleDeletesTurn = 1
		},
		"missing_read_accounting": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting = nil
		},
		"wrong_read_schema": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting.Schema = "wrong"
		},
		"wrong_read_class": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting.Class = "wrong"
		},
		"wrong_read_calls": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting.ReportCalls++
		},
		"wrong_control_reads": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting.ControlFileReads++
		},
		"store_read": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting.StoreReadAttempts++
		},
		"wrong_member_reads": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting.MemberReads--
		},
		"store_write": func(value *Receipt) {
			value.TransitionResults[index].ReadAccounting.StoreWriteAttempts++
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			mutate(&changed)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("impossible physical reader transition was accepted")
			}
		})
	}
	logical := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "logical_delta_b"
	})
	logicalPoint := slices.IndexFunc(plan.FailurePoints, func(value FailurePoint) bool {
		return value.Name == "partial_service_activation"
	})
	if logical < 0 || logicalPoint < 0 || base.TransitionResults[logical].ReadAccounting == nil ||
		len(base.TransitionResults[logical].Injections) != 1 ||
		base.TransitionResults[logical].Injections[0].RequeueCount != 1 {
		t.Fatal("logical transition read accounting is absent")
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"wrong_class": func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"wrong_calls": func(value *TransitionReadSubtotal) { value.ReportCalls-- },
		"store_read":  func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
	} {
		t.Run("logical_"+name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			mutate(changed.TransitionResults[logical].ReadAccounting)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("impossible logical transition read accounting was accepted")
			}
		})
	}
	t.Run("logical_missing", func(t *testing.T) {
		changed := cloneTestReceipt(t, base)
		changed.TransitionResults[logical].ReadAccounting = nil
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatal("missing logical transition read accounting was accepted")
		}
	})
	for _, requeues := range []uint64{0, 2} {
		t.Run(fmt.Sprintf("logical_requeues_%d", requeues), func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			injection := &changed.TransitionResults[logical].Injections[0]
			injection.RequeueCount = requeues
			digest, err := injectionRecoveryProjectionSHA256(*injection, plan.FailurePoints[logicalPoint])
			if err != nil {
				t.Fatal(err)
			}
			injection.RecoveryProjectionSHA256 = digest
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatalf("V2 logical transition with %d requeues was accepted", requeues)
			}
		})
	}
	returned := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "return_a"
	})
	if returned < 0 || base.TransitionResults[returned].ReadAccounting == nil {
		t.Fatal("return transition read accounting is absent")
	}
	t.Run("return_missing", func(t *testing.T) {
		changed := cloneTestReceipt(t, base)
		changed.TransitionResults[returned].ReadAccounting = nil
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatal("missing return transition read accounting was accepted")
		}
	})
	stale := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "stale_lease"
	})
	if stale < 0 || base.TransitionResults[stale].ReadAccounting == nil {
		t.Fatal("stale lease transition read accounting is absent")
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"wrong_class": func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"wrong_calls": func(value *TransitionReadSubtotal) { value.ReportCalls-- },
		"control_read": func(value *TransitionReadSubtotal) {
			value.ControlFileReads++
		},
		"store_read": func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
	} {
		t.Run("stale_"+name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			mutate(changed.TransitionResults[stale].ReadAccounting)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("impossible stale lease transition read accounting was accepted")
			}
		})
	}
	t.Run("stale_missing", func(t *testing.T) {
		changed := cloneTestReceipt(t, base)
		changed.TransitionResults[stale].ReadAccounting = nil
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatal("missing stale lease transition read accounting was accepted")
		}
	})
	restart := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "process_restart"
	})
	if restart < 0 || base.TransitionResults[restart].ReadAccounting == nil ||
		len(base.TransitionResults[restart].Injections) != 1 ||
		base.TransitionResults[restart].Injections[0].Checkpoint == nil {
		t.Fatal("checkpoint restart transition read accounting is absent")
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"wrong_class": func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"wrong_calls": func(value *TransitionReadSubtotal) { value.ReportCalls-- },
		"control_read": func(value *TransitionReadSubtotal) {
			value.ControlFileReads++
		},
		"store_read": func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
	} {
		t.Run("restart_"+name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			mutate(changed.TransitionResults[restart].ReadAccounting)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("impossible checkpoint restart transition read accounting was accepted")
			}
		})
	}
	t.Run("restart_missing", func(t *testing.T) {
		changed := cloneTestReceipt(t, base)
		changed.TransitionResults[restart].ReadAccounting = nil
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatal("missing checkpoint restart transition read accounting was accepted")
		}
	})
	pressure80 := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "pressure_80"
	})
	if pressure80 < 0 || base.TransitionResults[pressure80].ReadAccounting == nil {
		t.Fatal("pressure-80 transition read accounting is absent")
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"wrong_class":  func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"wrong_calls":  func(value *TransitionReadSubtotal) { value.ReportCalls-- },
		"control_read": func(value *TransitionReadSubtotal) { value.ControlFileReads++ },
		"store_read":   func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
		"member_read":  func(value *TransitionReadSubtotal) { value.MemberReads++ },
		"store_write":  func(value *TransitionReadSubtotal) { value.StoreWriteAttempts++ },
	} {
		t.Run("pressure_80_"+name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			mutate(changed.TransitionResults[pressure80].ReadAccounting)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("impossible pressure-80 transition read accounting was accepted")
			}
		})
	}
	t.Run("pressure_80_missing", func(t *testing.T) {
		changed := cloneTestReceipt(t, base)
		changed.TransitionResults[pressure80].ReadAccounting = nil
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatal("missing pressure-80 transition read accounting was accepted")
		}
	})
	pressure90 := slices.IndexFunc(base.TransitionResults, func(value TransitionResult) bool {
		return value.Phase == "pressure_90"
	})
	if pressure90 < 0 || base.TransitionResults[pressure90].ReadAccounting == nil {
		t.Fatal("pressure-90 transition read accounting is absent")
	}
	for name, mutate := range map[string]func(*TransitionReadSubtotal){
		"wrong_class":  func(value *TransitionReadSubtotal) { value.Class = "wrong" },
		"wrong_calls":  func(value *TransitionReadSubtotal) { value.ReportCalls++ },
		"control_read": func(value *TransitionReadSubtotal) { value.ControlFileReads++ },
		"store_read":   func(value *TransitionReadSubtotal) { value.StoreReadAttempts++ },
		"member_read":  func(value *TransitionReadSubtotal) { value.MemberReads++ },
		"store_write":  func(value *TransitionReadSubtotal) { value.StoreWriteAttempts++ },
	} {
		t.Run("pressure_90_"+name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			mutate(changed.TransitionResults[pressure90].ReadAccounting)
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("impossible pressure-90 transition read accounting was accepted")
			}
		})
	}
	t.Run("pressure_90_missing", func(t *testing.T) {
		changed := cloneTestReceipt(t, base)
		changed.TransitionResults[pressure90].ReadAccounting = nil
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatal("missing pressure-90 transition read accounting was accepted")
		}
	})
	for name, mutate := range map[string]func(*CheckpointRecovery){
		"chunk_identity": func(value *CheckpointRecovery) { value.ChunkIdentitySHA256 = zeroDigest() },
		"hit_schedule":   func(value *CheckpointRecovery) { value.ScheduleStatusAtHit = store.GenerationScheduleSettled },
		"hit_chunk":      func(value *CheckpointRecovery) { value.ChunkStatusAtHit = store.GenerationChunkDone },
		"hit_lease":      func(value *CheckpointRecovery) { value.LeasedAtHit = false },
		"hit_current":    func(value *CheckpointRecovery) { value.CurrentAbsentAtHit = false },
		"after_schedule": func(value *CheckpointRecovery) { value.ScheduleStatusAfter = store.GenerationScheduleActive },
		"after_chunk":    func(value *CheckpointRecovery) { value.ChunkStatusAfter = store.GenerationChunkRunning },
		"after_lease":    func(value *CheckpointRecovery) { value.UnleasedAfter = false },
		"retry_attempt": func(value *CheckpointRecovery) {
			value.AttemptBefore, value.AttemptAfter = 1, 1
		},
	} {
		t.Run("restart_"+name, func(t *testing.T) {
			changed := cloneTestReceipt(t, base)
			injection := &changed.TransitionResults[restart].Injections[0]
			mutate(injection.Checkpoint)
			pointIndex := slices.IndexFunc(plan.FailurePoints, func(value FailurePoint) bool {
				return value.Name == injection.FailurePoint
			})
			if pointIndex < 0 {
				t.Fatal("checkpoint restart failure point is absent")
			}
			var err error
			injection.HitReportSHA256, err = injectionHitReportSHA256(*injection, plan.FailurePoints[pointIndex])
			if err != nil {
				t.Fatal(err)
			}
			injection.RecoveryProjectionSHA256, err = injectionRecoveryProjectionSHA256(*injection, plan.FailurePoints[pointIndex])
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateReceipt(
				changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
			); err == nil {
				t.Fatal("impossible checkpoint restart reader state was accepted")
			}
		})
	}
	t.Run("unfinished_transition_accounting", func(t *testing.T) {
		changed := cloneTestReceipt(t, base)
		unfinished := slices.IndexFunc(changed.TransitionResults, func(value TransitionResult) bool {
			return value.Phase == "pressure_75"
		})
		if unfinished < 0 || changed.TransitionResults[unfinished].ReadAccounting != nil {
			t.Fatal("unfinished transition fixture is invalid")
		}
		subtotal := *changed.TransitionResults[stale].ReadAccounting
		changed.TransitionResults[unfinished].ReadAccounting = &subtotal
		if err := ValidateReceipt(
			changed, plan, binding, returnedPackageTestBinding(t, changed, plan, binding),
		); err == nil {
			t.Fatal("unfinished transition read accounting was accepted")
		}
	})
}

type productionIdentityState struct {
	Authority         AuthorityState
	Source            repositoryindex.SourceManifest
	Sparse            candidate.SparseRoot
	Plans             map[string]candidate.DomainResultPlan
	Roots             map[string]candidate.DomainResultRoot
	ObservationSource observationpublication.DownstreamSource
	Descriptors       []gocaller.DirectDescriptor
	DataDir           string
	ExtractionRoots   []ExtractionRootResult
	NativeDomains     []candidate.DownstreamDomainAuthority
}

func productionExtractionRoots(t *testing.T, frozen Plan, name string, value productionIdentityState, native *productionRuntimeResult) productionIdentityState {
	t.Helper()
	if native == nil || native.Schedule.Status != store.GenerationScheduleSettled ||
		native.Schedule.Succeeded != native.Schedule.TotalChunks || native.Schedule.Pending != 0 || native.Schedule.Running != 0 || native.Schedule.Failed != 0 {
		t.Fatal("extraction receipt requires the genuine completed native schedule")
	}
	physicalRevision, _ := namedPhysicalRevision(frozen.Revisions.Physical, name)
	value.NativeDomains = slices.Clone(native.Domains)
	value.ExtractionRoots = make([]ExtractionRootResult, 0, len(native.Domains))
	for domainIndex, profile := range frozen.Profile.Pipeline.ExtractionDomains {
		plan, root := value.Plans[profile.Domain], value.Roots[profile.Domain]
		if profile.Domain != plan.Domain || len(profile.Partitions) != len(root.Results) {
			t.Fatal("native extraction inventory differs from the frozen profile")
		}
		partitions := make([]ExtractionPartitionResult, len(root.Results))
		for index, result := range root.Results {
			expected := profile.Partitions[index]
			if result.Totals != t421ProductionReplayTotals(expected.Expected) || result.Reserved != t421ProductionReplayTotals(expected.Reservation) {
				t.Fatal("native extraction result differs from frozen exact totals")
			}
			partitions[index] = ExtractionPartitionResult{
				Ordinal: uint64(result.PartitionOrdinal), Kind: plan.Expected[index].Kind,
				MemberOrdinal: expected.MemberOrdinal, CallerPrefix: expected.CallerPrefix,
				SourceStart: uint64(result.SourceStart), SourceEnd: uint64(result.SourceEnd),
				MemberRecordStart: expected.MemberRecordStart, MemberRecordEnd: expected.MemberRecordEnd,
				AdmittedRecords: expected.AdmittedRecords, Reservation: expected.Reservation, Totals: expected.Expected,
				PartitionSHA256: result.PartitionDigest, ExpectationSHA256: result.ExpectationDigest,
				ResultDigestSHA256: result.Digest, ResultIdentitySHA256: result.Identity,
				Disposition: result.Disposition,
			}
		}
		members, err := extractionResultMembers(partitions)
		if err != nil {
			t.Fatal(err)
		}
		converted := ExtractionRootResult{
			Domain: plan.Domain, Current: true, GenerationSHA256: native.Generation, RootSHA256: root.Digest,
			CandidateGenerationSHA256: plan.CandidateGenerationDigest, SourceGenerationSHA256: plan.SourceGenerationDigest,
			ObservationGenerationSHA256: plan.ObservationGenerationDigest, PlanSHA256: plan.Digest,
			Members:              members,
			ApplicablePartitions: uint64(root.ExpectedResults), MemberPartitions: profile.MemberPartitions, TypedPartitions: profile.TypedPartitions,
			TypedScopeRecords: profile.TypedScopeRecords, TypedScopePathBytes: profile.TypedScopePathBytes, TypedScopeEncodedBytes: profile.TypedScopeEncodedBytes,
			Candidates: physicalRevision.ExpectedCandidateInventories[domainIndex].Candidates,
			Reserved:   profile.Reserved, Totals: profile.Expected, PartitionResults: partitions,
			PartitionResultsSHA256: mustReceiptSHA256(t, partitions),
		}
		if typed, ok := namedTypedScopeRevision(profile.TypedScopeRevisions, name); ok {
			found := false
			for _, descriptor := range value.Sparse.Domains {
				if descriptor.Domain != plan.Domain {
					continue
				}
				found = true
				scope := descriptor.TypedScope
				if scope == nil || uint64(scope.Records) != profile.TypedScopeRecords ||
					uint64(scope.ContentBytes) != profile.TypedScopeEncodedBytes ||
					scope.ContentDigest != typed.SHA256 || scope.ContentDigest != typed.DescriptorContentSHA256 {
					t.Fatalf("native %s typed scope differs from the frozen %s digest/count/bytes", plan.Domain, name)
				}
				converted.TypedScopeSHA256, converted.TypedScopeContentSHA256 = scope.ContentDigest, scope.ContentDigest
			}
			if !found {
				t.Fatalf("native sparse root lacks %s typed scope", plan.Domain)
			}
		}
		value.ExtractionRoots = append(value.ExtractionRoots, converted)
	}
	value.Authority.ExtractionRootsSHA256 = mustReceiptSHA256(t, value.ExtractionRoots)
	return value
}

// productionPhysicalIdentities calls native source, observation, candidate,
// partition-result, resolver and caller constructors over exact frozen Git
// inputs. Only the search shard leaf is modeled: its bytes are a labeled test
// artifact, never claimed to be a live index or measured ceremony evidence.
func productionPhysicalIdentities(t *testing.T, plan Plan) map[string]productionIdentityState {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	dataDir := t.TempDir()
	repository := productionIdentityRepository(t, ctx, plan, dataDir)
	native := newProductionRuntimeIdentity(t, ctx, dataDir)
	defer native.close(t)
	combined, err := BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := BuildIndependentOracle()
	if err != nil {
		t.Fatal(err)
	}
	blobs := make(map[string][]byte, 31_602)
	if err := WalkCombinedAdditions(func(_ string, content []byte) error {
		blobs[gitSHA1ObjectID("blob", content)] = slices.Clone(content)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	readBlob := func(ctx context.Context, directory, oid string, limit int64) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if content, ok := blobs[oid]; ok {
			if int64(len(content)) > limit {
				return nil, fmt.Errorf("constructor fixture blob exceeds limit %d", limit)
			}
			return slices.Clone(content), nil
		}
		return gitobj.ReadBlob(ctx, directory, oid, limit)
	}
	result := make(map[string]productionIdentityState, 3)
	for _, physical := range plan.Revisions.Physical {
		t.Logf("native physical identity constructors: %s", physical.Name)
		revisions := []store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: physical.ExpectedCommit}}
		sourceDir := filepath.Join(dataDir, "source-"+physical.Name)
		source, err := repositoryindex.BuildSourceGeneration(ctx, repository, sourceDir, t401.RepositoryName, revisions)
		if err != nil {
			t.Fatal(err)
		}
		if source.RegularOwnerCount != int(plan.Profile.Physical.CombinedRegularFiles) {
			t.Fatal("native source census differs from the frozen combined corpus")
		}
		leaf := []byte("MODELED SEARCH LEAF FOR CONSTRUCTOR TEST ONLY\n" + source.Digest + "\n")
		physicalRoot := repositoryindex.PhysicalRoot{
			Schema: "t421-constructor-test-search-leaf-v1", ManifestName: "modeled-search.json",
			ManifestDigest: SHA256(leaf), Members: []repositoryindex.PhysicalMember{{
				Ordinal: 0, Count: 1, Name: "modeled.00000.zoekt", ContentBytes: int64(len(leaf)),
				ContentDigest: SHA256(leaf), MetadataDigest: SHA256([]byte("MODELED SEARCH METADATA\n" + source.Digest)),
			}},
		}
		search, err := repositoryindex.WriteSearchManifest(sourceDir, t401.RepositoryName, revisions, source, physicalRoot)
		if err != nil {
			t.Fatal(err)
		}
		partitionDir := filepath.Join(dataDir, "source-partitions-"+physical.Name)
		if err := os.Mkdir(partitionDir, 0o700); err != nil {
			t.Fatal(err)
		}
		sourceRoot, err := sourcepartition.BuildSuperRoot(ctx, sourcepartition.BuildRequest{
			SourceDirectory: sourceDir, OutputDirectory: partitionDir, Repository: t401.RepositoryName,
			Source: source, Policy: sourcepartition.Policy{
				Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0", IncludeSuffixes: []string{".go"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		partition, err := sourcepartition.OpenSuperRoot(ctx, partitionDir, sourceRoot)
		if err != nil {
			t.Fatal(err)
		}
		observationDir := filepath.Join(dataDir, "observation-"+physical.Name)
		observations, err := observationpublication.BuildInventoryStageV2(ctx, observationpublication.InventoryBuildRequestV2{
			OutputDirectory: observationDir, RepositoryDirectory: repository, Plan: partition,
		})
		if err != nil {
			t.Fatal(err)
		}
		observed, err := observationpublication.OpenInventoryV2(ctx, observationDir, observations)
		if err != nil {
			t.Fatal(err)
		}
		pipeline := runProductionIdentityPipeline(t, ctx, combined, oracle, productionPipelineInput{
			DataDir: dataDir, RepositoryDir: repository, Repository: t401.RepositoryName, Commit: physical.ExpectedCommit,
			ControlSuffix: "-" + physical.Name, SourceDigest: source.Digest,
			ObservationDigest: observations.GenerationDigest, ReadBlob: readBlob,
			SourceDirectory: sourceDir, ObservationDirectory: observationDir, Runtime: native,
		})
		value := productionIdentityState{
			Authority: AuthorityState{
				PhysicalRevision: physical.Name, PhysicalCommit: physical.ExpectedCommit, PhysicalTree: physical.ExpectedTree,
				SourceGenerationSHA256: source.Digest, SearchGenerationSHA256: search.Digest,
				ObservationGenerationSHA256: observations.GenerationDigest, CandidateGenerationSHA256: pipeline.Candidate.GenerationDigest,
				ResolverCatalogGenerationSHA256: pipeline.Resolver.GenerationDigest, ResolverCatalogRootSHA256: pipeline.Resolver.AuthorityDigest,
				CallerGenerationSHA256: pipeline.Caller.Generation.Digest, CallerRootSHA256: pipeline.Caller.Digest,
				SearchInventory: physical.ExpectedTreeInventory, ObservationInputInventory: physical.ExpectedObservationInputInventory, Current: true,
			},
			Source: source, Sparse: pipeline.Sparse, Plans: pipeline.Plans, Roots: pipeline.Roots, ObservationSource: observed,
			Descriptors: pipeline.Descriptors, DataDir: dataDir,
		}
		result[physical.Name] = productionExtractionRoots(t, plan, physical.Name, value, pipeline.Runtime)
		if physical.Name == "a-return" {
			native.prepareRecoveries(t, ctx, plan, pipeline.Runtime)
		}
	}
	return result
}

func productionIdentityRepository(t *testing.T, ctx context.Context, plan Plan, dataDir string) string {
	t.Helper()
	structural, err := frozenStructuralProfile()
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dataDir, "structural")
	if _, err := t401.Author(ctx, t401.AuthorRequest{ModuleRoot: moduleRoot, Output: output, Profile: structural, ConfirmFrozen: true}); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(dataDir, "repos", "example.invalid", "t401-neutral-scale.git")
	if err := os.MkdirAll(filepath.Dir(repository), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(output, "repository.git"), repository); err != nil {
		t.Fatal(err)
	}
	files := make([]t421ProductionReplayFile, 0, 31_602)
	if err := WalkCombinedAdditions(func(path string, content []byte) error {
		files = append(files, t421ProductionReplayFile{path: path, content: slices.Clone(content)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	command := t421ProductionReplayGit(ctx, "--git-dir", repository, "fast-import", "--quiet")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriterSize(input, 1<<20)
	maxPathDepth := 0
	writeErr := writeProductionIdentityCommits(ctx, writer, plan, structural, files, &maxPathDepth)
	if err := writer.Flush(); writeErr == nil {
		writeErr = err
	}
	if err := input.Close(); writeErr == nil {
		writeErr = err
	}
	waitErr := command.Wait()
	if writeErr != nil || waitErr != nil {
		t.Fatalf("author exact combined identity witness: write=%v wait=%v: %s", writeErr, waitErr, stderr.String())
	}
	for _, revision := range plan.Revisions.Physical {
		output, err := t421ProductionReplayGit(ctx, "--git-dir", repository, "rev-parse",
			"refs/heads/t422-identity-"+revision.Name, "refs/heads/t422-identity-"+revision.Name+"^{tree}").Output()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Fields(string(output)), []string{revision.ExpectedCommit, revision.ExpectedTree}; !slices.Equal(got, want) {
			t.Fatalf("native combined %s commit/tree = %v, want %v", revision.Name, got, want)
		}
	}
	t.Logf("native constructor-author source path depth=%d (components including basename)", maxPathDepth)
	productionGitObjectPosture(t, repository)
	return repository
}

// This inventories only this constructor author's Git store. It does not
// replace the separately frozen post-clone/post-fetch operator observations.
func productionGitObjectPosture(t *testing.T, repository string) {
	t.Helper()
	objects := filepath.Join(repository, "objects")
	entries, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	var packs, packObjects, looseObjects uint64
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "info" {
			continue
		}
		children, err := os.ReadDir(filepath.Join(objects, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, child := range children {
			if entry.Name() == "pack" {
				if strings.HasSuffix(child.Name(), ".pack") {
					packs++
					file, err := os.Open(filepath.Join(objects, entry.Name(), child.Name()))
					if err != nil {
						t.Fatal(err)
					}
					var header [12]byte
					_, readErr := io.ReadFull(file, header[:])
					closeErr := file.Close()
					if readErr != nil || closeErr != nil || string(header[:4]) != "PACK" {
						t.Fatalf("invalid constructor Git pack header: read=%v close=%v", readErr, closeErr)
					}
					packObjects += uint64(binary.BigEndian.Uint32(header[8:]))
				}
			} else if len(entry.Name()) == 2 && len(child.Name()) == 38 && !child.IsDir() {
				looseObjects++
			}
		}
	}
	t.Logf("native constructor-author Git posture: packs=%d packed_objects=%d loose_objects=%d (not post-clone/fetch ceremony evidence)", packs, packObjects, looseObjects)
}

func writeProductionIdentityCommits(ctx context.Context, writer *bufio.Writer, plan Plan, structural t401.Profile, files []t421ProductionReplayFile, maxPathDepth *int) error {
	for index, file := range files {
		if _, err := fmt.Fprintf(writer, "blob\nmark :%d\ndata %d\n", index+1, len(file.content)); err != nil {
			return err
		}
		if _, err := writer.Write(file.content); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
	}
	for index, revision := range plan.Revisions.Physical {
		if err := ctx.Err(); err != nil {
			return err
		}
		recipe, message := plan.Revisions.SourceRecipe, revision.CommitMessage+"\n"
		if _, err := fmt.Fprintf(writer,
			"commit refs/heads/t422-identity-%s\nauthor %s <%s> %d %s\ncommitter %s <%s> %d %s\ndata %d\n%s",
			revision.Name, recipe.AuthorName, recipe.AuthorEmail, recipe.Timestamp, recipe.Timezone,
			recipe.CommitterName, recipe.CommitterEmail, recipe.Timestamp, recipe.Timezone, len(message), message); err != nil {
			return err
		}
		if index > 0 {
			if _, err := fmt.Fprintf(writer, "from refs/heads/t422-identity-%s\n", plan.Revisions.Physical[index-1].Name); err != nil {
				return err
			}
		}
		if _, err := writer.WriteString("deleteall\n"); err != nil {
			return err
		}
		goOrdinal := uint64(0)
		if err := t401.WalkFrozenTreeRecords(structural, revision.Name, func(record t401.FrozenTreeRecord) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := record.Path
			if strings.HasSuffix(path, ".go") {
				if goOrdinal > 0 {
					path = strings.TrimSuffix(path, ".go") + ".txt"
				}
				goOrdinal++
			}
			*maxPathDepth = max(*maxPathDepth, strings.Count(path, "/")+1)
			_, err := fmt.Fprintf(writer, "M 100644 %s %q\n", record.BlobOID, path)
			return err
		}); err != nil {
			return err
		}
		for index, file := range files {
			*maxPathDepth = max(*maxPathDepth, strings.Count(file.path, "/")+1)
			if _, err := fmt.Fprintf(writer, "M 100644 :%d %q\n", index+1, file.path); err != nil {
				return err
			}
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
	}
	_, err := writer.WriteString("done\n")
	return err
}

// This is a dependency counterexample, not a full-corpus authority fixture or
// live work evidence. Artifact contents are deliberately empty; the production
// identity constructors derive every resolver/caller digest under comparison.
func TestLogicalCatalogDeltaCannotReplaceRepositoryIdentities(t *testing.T) {
	ctx := context.Background()
	extractors := t421ProductionReplayExtractors()
	resolverRegistry, err := resolvermaterialize.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	callerRegistry, err := callerexecute.NewRegistry(extractors)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	commit := strings.Repeat("a", 40)
	candidateManifest := SHA256([]byte("explicit counterexample candidate artifact"))
	candidatePolicy := SHA256([]byte("explicit counterexample candidate policy"))
	derive := func(commit string, runID string) AuthorityState {
		t.Helper()
		declarations := []resolvercatalog.DeclarationPublication{{
			Domain: "proto-contract", RunID: runID,
			GenerationDigest: SHA256([]byte("counterexample declaration content")),
			AuthoritySchema:  store.PartitionedExtractionDomainSchema,
			PlanDigest:       SHA256([]byte("counterexample declaration plan")),
			RootDigest:       SHA256([]byte("counterexample declaration content")),
		}}
		identity, err := resolvercatalog.NewIdentity(t401.RepositoryName, commit, "", candidateManifest,
			declarations, resolverRegistry.Packs())
		if err != nil {
			t.Fatal(err)
		}
		stage, err := resolvercatalog.NewStage(root, identity)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal(ctx)
		if err != nil {
			_ = stage.Discard()
			t.Fatal(err)
		}
		resolver, err := prepared.Publish(ctx, func(context.Context, resolvercatalog.State) error { return nil })
		if err != nil {
			_ = prepared.Discard()
			t.Fatal(err)
		}
		pointer := t421ProductionReplayResolverPointer(resolver)
		caller, err := callerexecute.GenerationIdentity(callerexecute.GenerationAuthority{
			Repository: &store.Repo{Name: t401.RepositoryName, IndexedCommitHash: commit},
			Candidate: &store.CandidateManifestPublication{
				Repository: t401.RepositoryName, HeadCommit: commit,
				ManifestDigest: candidateManifest, PolicyDigest: candidatePolicy,
			},
			Resolver: &pointer,
		}, callerRegistry)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := callerpublication.BuildManifest(caller, []callerpublication.PairReceipt{})
		if err != nil {
			t.Fatal(err)
		}
		return AuthorityState{
			ResolverCatalogGenerationSHA256: identity.GenerationDigest,
			ResolverCatalogRootSHA256:       resolver.AuthorityDigest,
			CallerGenerationSHA256:          caller.Digest,
			CallerRootSHA256:                manifest.Digest,
		}
	}
	physical := derive(commit, "run-before-logical-change")
	logical := derive(commit, "run-before-logical-change")
	if physical != logical {
		t.Fatal("logical-only catalog state changed repository-bound production identities")
	}
	restored := derive(commit, "restored-declaration-run")
	if restored != physical {
		t.Fatal("replacement declaration run ID changed semantic resolver/caller authority")
	}
	returned := derive(strings.Repeat("b", 40), "return-declaration-run")
	if returned.ResolverCatalogGenerationSHA256 == physical.ResolverCatalogGenerationSHA256 ||
		returned.ResolverCatalogRootSHA256 == physical.ResolverCatalogRootSHA256 ||
		returned.CallerGenerationSHA256 == physical.CallerGenerationSHA256 ||
		returned.CallerRootSHA256 == physical.CallerRootSHA256 {
		t.Fatal("changed commit failed to replace source-bound production identities")
	}
}

func TestIdentityDerivationTableIsComplete(t *testing.T) {
	rows := frozenIdentityDerivations()
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		expanded, err := row.ExpandedChangedInputs(frozenPhaseOrder())
		if row.Identity == "" || row.Constructor == "" || len(row.Inputs) == 0 || err != nil || len(expanded) != 13 || seen[row.Identity] {
			t.Fatalf("invalid identity derivation row: %+v", row)
		}
		seen[row.Identity] = true
		if expanded[0] != "initial" {
			t.Fatalf("%s lacks cold origin", row.Identity)
		}
		for _, index := range []int{1, 5, 6, 7, 8, 9, 11, 12} {
			if expanded[index] != "none" {
				t.Fatalf("%s changed protected authority in operational phase %d", row.Identity, index)
			}
		}
	}
	fields := reflect.TypeFor[AuthorityState]()
	for index := 0; index < fields.NumField(); index++ {
		name := strings.Split(fields.Field(index).Tag.Get("json"), ",")[0]
		if strings.HasSuffix(name, "_sha256") || slices.Contains([]string{"physical_commit", "physical_tree"}, name) {
			if !seen[name] {
				t.Fatalf("compared authority %s lacks a production derivation", name)
			}
		}
	}
	for _, nested := range []struct {
		prefix string
		fields reflect.Type
	}{
		{"extraction_roots[].", reflect.TypeFor[ExtractionRootResult]()},
		{"extraction_roots[].partition_results[].", reflect.TypeFor[ExtractionPartitionResult]()},
	} {
		for index := 0; index < nested.fields.NumField(); index++ {
			name := strings.Split(nested.fields.Field(index).Tag.Get("json"), ",")[0]
			if strings.HasSuffix(name, "_sha256") && !seen[nested.prefix+name] {
				t.Fatalf("compared nested authority %s lacks a production derivation", nested.prefix+name)
			}
		}
	}
}

func TestIdentityDerivationExpansionRejectsAmbiguousRules(t *testing.T) {
	for _, test := range []struct {
		name    string
		changes map[string]string
	}{
		{"cold override", map[string]string{"cold": "commit"}},
		{"unknown phase", map[string]string{"unfrozen_phase": "commit"}},
		{"empty inputs", map[string]string{"return_a": ""}},
		{"ambiguous overlap", map[string]string{"physical_revision": "commit", "return_a": "tree"}},
		{"redundant default", map[string]string{"return_a": "none"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := IdentityDerivation{ChangedInputs: test.changes}
			if _, err := row.ExpandedChangedInputs(frozenPhaseOrder()); err == nil {
				t.Fatal("ambiguous or undefined identity transition rule was accepted")
			}
		})
	}
}

func TestExtractionResultMembersRejectsMismatchedFraming(t *testing.T) {
	results := []ExtractionPartitionResult{{ResultIdentitySHA256: SHA256([]byte("explicit test result identity"))}}
	members, err := extractionResultMembers(results)
	if err != nil || members.Records != 1 || members.FramedBytes <= 71 {
		t.Fatalf("invalid framed result inventory: %+v, %v", members, err)
	}
	changed, err := extractionResultMembers([]ExtractionPartitionResult{{ResultIdentitySHA256: SHA256([]byte("different explicit test identity"))}})
	if err != nil || members.SHA256 == changed.SHA256 || members.FramedBytes != changed.FramedBytes {
		t.Fatal("framed member identity did not follow its native result-identity input")
	}
	empty, err := extractionResultMembers(nil)
	if err != nil || empty.Records != 0 || empty.FramedBytes == 0 || !validDigest(empty.SHA256) {
		t.Fatal("empty exact result inventory has no domain-separated frame")
	}
}

func TestV2AuthorityCoverageRejectsIncorrectResultInventory(t *testing.T) {
	plan := correctedTestPlan(t)
	encoded, err := MarshalCanonical(plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("corrected plan bytes=%d limit=%d", len(encoded), MaxPlanBytes)
	physical := plan.Revisions.Physical[0]
	// Deliberately modeled unit-test leaves isolate framing rejection. This
	// is not the production-constructed ordinary-pass fixture above.
	value := AuthorityPhaseResult{AuthorityState: AuthorityState{
		SourceGenerationSHA256:      SHA256([]byte("modeled coverage source")),
		CandidateGenerationSHA256:   SHA256([]byte("modeled coverage candidate")),
		ObservationGenerationSHA256: SHA256([]byte("modeled coverage observation")),
		SearchInventory:             physical.ExpectedTreeInventory, ObservationInputInventory: physical.ExpectedObservationInputInventory,
	}}
	value.ExtractionRoots = testExtractionRoots(t, plan, physical, value.AuthorityState, "modeled framing unit test")
	for index := range value.ExtractionRoots {
		root := &value.ExtractionRoots[index]
		root.ScheduleSHA256 = ""
		var err error
		root.Members, err = extractionResultMembers(root.PartitionResults)
		if err != nil {
			t.Fatal(err)
		}
	}
	value.ExtractionRootsSHA256 = mustReceiptSHA256(t, value.ExtractionRoots)
	if err := validateAuthorityCoverage(value, physical, plan); err != nil {
		t.Fatalf("coherent unit-test coverage failed: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*SetIdentity)
	}{
		{"digest", func(value *SetIdentity) { value.SHA256 = SHA256([]byte("unrelated framing")) }},
		{"framed bytes", func(value *SetIdentity) { value.FramedBytes++ }},
		{"record count", func(value *SetIdentity) { value.Records++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := value
			changed.ExtractionRoots = slices.Clone(value.ExtractionRoots)
			test.edit(&changed.ExtractionRoots[0].Members)
			changed.ExtractionRootsSHA256 = mustReceiptSHA256(t, changed.ExtractionRoots)
			if err := validateAuthorityCoverage(changed, physical, plan); err == nil {
				t.Fatal("incorrect result inventory passed with a recomputed outer digest")
			}
		})
	}
}

func TestAuthorityContinuityPreservesV1AndRejectsImpossibleV2Changes(t *testing.T) {
	legacy := frozenTestPlan(t)
	outcomes := make(map[string]string, len(legacy.PhaseOrder))
	for _, phase := range legacy.PhaseOrder {
		outcomes[phase] = "passed"
	}
	_, _, _, authorities := completeTestAuthorities(t, legacy, outcomes, completeTestRevisionResults(t, legacy, outcomes))
	values := make(map[string]AuthorityPhaseResult, len(authorities))
	for _, authority := range authorities {
		values[authority.Phase] = authority
	}
	if err := validateAuthorityContinuity(values, legacy); err != nil {
		t.Fatalf("historical V1 fixture changed: %v", err)
	}
	prospective := Plan{Schema: PlanV2Schema}
	if err := validateAuthorityContinuity(values, prospective); err == nil {
		t.Fatal("V2 accepted V1's impossible logical-only resolver/caller replacements")
	}
	for phase, authority := range values {
		base := authority
		switch phase {
		case "logical_delta_b":
			base = values["physical_delta_b"]
		case "archive_restore", "lifecycle_collection", "product_queries":
			base = values["return_a"]
		}
		authority.ResolverCatalogGenerationSHA256 = base.ResolverCatalogGenerationSHA256
		authority.ResolverCatalogRootSHA256 = base.ResolverCatalogRootSHA256
		authority.CallerGenerationSHA256 = base.CallerGenerationSHA256
		authority.CallerRootSHA256 = base.CallerRootSHA256
		// Explicit modeled provenance inputs for the validator counterexample;
		// production construction itself is exercised by the accept-side test.
		authority.RelationshipProvenanceSHA256 = SHA256([]byte(authority.PhysicalRevision))
		if phase == "archive_restore" || phase == "lifecycle_collection" || phase == "product_queries" {
			authority.RelationshipProvenanceSHA256 = SHA256([]byte("replacement extraction runs"))
		}
		values[phase] = authority
	}
	if err := validateAuthorityContinuity(values, prospective); err != nil {
		t.Fatalf("V2 corrected dependency pattern refused: %v", err)
	}
	if err := validateAuthorityContinuity(values, legacy); err == nil {
		t.Fatal("prospective identity correction silently changed V1 semantics")
	}
	mutations := []struct {
		name string
		edit func(map[string]AuthorityPhaseResult)
	}{
		{"logical resolver replacement", func(changed map[string]AuthorityPhaseResult) {
			value := changed["logical_delta_b"]
			value.ResolverCatalogGenerationSHA256 = SHA256([]byte("impossible resolver replacement"))
			changed[value.Phase] = value
		}},
		{"logical provenance replacement", func(changed map[string]AuthorityPhaseResult) {
			value := changed["logical_delta_b"]
			value.RelationshipProvenanceSHA256 = SHA256([]byte("unexplained logical extraction run"))
			changed[value.Phase] = value
		}},
		{"archive identity change without provenance", func(changed map[string]AuthorityPhaseResult) {
			value := changed["archive_restore"]
			value.RelationshipProvenanceSHA256 = changed["pressure_75"].RelationshipProvenanceSHA256
			changed[value.Phase] = value
		}},
		{"archive provenance change without new identity", func(changed map[string]AuthorityPhaseResult) {
			value, prior := changed["archive_restore"], changed["pressure_75"]
			value.RelationshipGenerationSHA256, value.RelationshipRootSHA256 = prior.RelationshipGenerationSHA256, prior.RelationshipRootSHA256
			changed[value.Phase] = value
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := make(map[string]AuthorityPhaseResult, len(values))
			for phase, value := range values {
				changed[phase] = value
			}
			mutation.edit(changed)
			if err := validateAuthorityContinuity(changed, prospective); err == nil {
				t.Fatal("contradictory identity transition was accepted")
			}
		})
	}
}
