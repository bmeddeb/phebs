package t421

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t401"
)

// This fixture drives one live production runtime synchronously. Source and
// observation controls are genuine constructor outputs, selected by this test;
// they are not an ordinary server's published upstream selectors. Evidence,
// native run/root authority, leases, result files, and settlement are real.
type productionRuntimeIdentity struct {
	state      *store.Surreal
	provider   *candidatejob.Provider
	runtime    *extractionpublication.Runtime
	reconciler *extractionpublication.Reconciler
	source     *productionIdentitySource
	evidence   *productionIdentityEvidence
	selected   productionPipelineInput
}

type productionRuntimeResult struct {
	Generation   string
	Schedule     store.GenerationSchedule
	Publications map[string]store.PartitionedExtractionDomain
	Domains      []candidate.DownstreamDomainAuthority
}

func newProductionRuntimeIdentity(t *testing.T, ctx context.Context, dataDir string) *productionRuntimeIdentity {
	t.Helper()
	stateDir := filepath.Join(dataDir, "extraction-state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := store.OpenLocalMemory(ctx, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &productionRuntimeIdentity{state: state}
	t.Cleanup(func() { fixture.close(t) })
	if err := state.UpsertRepo(ctx, store.Repo{Name: t401.RepositoryName, CloneURL: "https://" + t401.RepositoryName}); err != nil {
		t.Fatal(err)
	}
	extractors := t421ProductionReplayExtractors()
	policies, err := candidatejob.CompilePolicies(extractors)
	if err != nil {
		t.Fatal(err)
	}
	fixture.provider, err = candidatejob.NewProvider(dataDir, state, policies)
	if err != nil {
		t.Fatal(err)
	}
	fixture.evidence = &productionIdentityEvidence{Surreal: state}
	fixture.runtime = &extractionpublication.Runtime{
		Root: filepath.Join(dataDir, "extraction-publications"), Store: state,
		Executor:  &extract.EvidencePartitionExecutor{Evidence: fixture.evidence, Extractors: extractors},
		Publisher: extractionpublication.StorePublisher{Store: state},
	}
	fixture.reconciler = &extractionpublication.Reconciler{
		Root: fixture.runtime.Root, CandidateRoot: candidatejob.CandidateRoot(dataDir),
		Runtime: fixture.runtime, Evidence: state,
		OpenCandidate: func(ctx context.Context, repository string) (*candidate.Publication, error) {
			return fixture.provider.OpenCurrentPublication(ctx, repository, nil)
		},
		CandidateReference: func(ctx context.Context, repository string) (candidate.State, error) {
			return fixture.provider.CurrentPublicationState(ctx, repository, nil)
		},
		Authority: fixture.readAuthority, AuthorityReference: fixture.readAuthority,
		// Configure the explicit test-only owner before any worker invocation.
		RecoveryPreparationEnabled: true,
	}
	fixture.source = &productionIdentitySource{Source: extractionpublication.GitSparseSource{
		DataDir: dataDir, OpenDomain: fixture.reconciler.OpenDomain,
	}}
	fixture.runtime.Source = fixture.source
	fixture.runtime.Fence = extractionpublication.AuthorityFence{
		Store: state,
		Acquire: func(ctx context.Context) (func(), error) {
			return focusedindex.AcquireExclusiveMutationLock(ctx, filepath.Join(dataDir, "index"))
		},
		Current: func(ctx context.Context, plan candidate.DomainResultPlan) error {
			publication, err := fixture.provider.OpenCurrentPublication(ctx, plan.Repository, nil)
			if err != nil {
				return err
			}
			current := publication.State()
			source, observation, err := fixture.readAuthority(ctx, plan.Repository)
			if err != nil {
				return err
			}
			if current.Repository != fixture.selected.Repository || current.Commit != fixture.selected.Commit || current.UnitDigest != "" ||
				current.ManifestDigest != plan.CandidateManifestDigest || current.GenerationDigest != plan.CandidateGenerationDigest ||
				current.PolicyDigest != plan.CandidatePolicyDigest || source != plan.SourceGenerationDigest ||
				observation != plan.ObservationGenerationDigest {
				return extractionpublication.ErrStale
			}
			return nil
		},
	}
	return fixture
}

func (fixture *productionRuntimeIdentity) close(t *testing.T) {
	t.Helper()
	if fixture.state == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := fixture.state.Close(ctx); err != nil {
		t.Errorf("close native extraction constructor store: %v", err)
	}
	fixture.state = nil
}

func (fixture *productionRuntimeIdentity) readAuthority(ctx context.Context, repository string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	selected := fixture.selected
	if repository != selected.Repository || selected.SourceDirectory == "" || selected.ObservationDirectory == "" {
		return "", "", extractionpublication.ErrStale
	}
	repo, err := fixture.state.GetRepo(ctx, repository)
	if err != nil {
		return "", "", err
	}
	source, sourceErr := repositoryindex.ReadSourceManifest(selected.SourceDirectory, repository)
	observation, observationErr := observationpublication.ReadInventoryRootV2(selected.ObservationDirectory, repository)
	if sourceErr != nil || observationErr != nil {
		return "", "", errors.Join(sourceErr, observationErr)
	}
	if repo == nil || repo.IndexedCommitHash != selected.Commit || source.Digest != selected.SourceDigest ||
		observation.GenerationDigest != selected.ObservationDigest {
		return "", "", extractionpublication.ErrStale
	}
	return source.Digest, observation.GenerationDigest, nil
}

func (fixture *productionRuntimeIdentity) publishCandidate(t *testing.T, ctx context.Context, input productionPipelineInput, selected candidate.State) {
	t.Helper()
	if err := fixture.state.SetRepoIndexed(ctx, input.Repository, input.Commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.state.PublishCandidateManifest(ctx, store.CandidateManifestPublication{
		Repository: selected.Repository, HeadCommit: selected.Commit, UnitDigest: selected.UnitDigest,
		PolicyDigest: selected.PolicyDigest, ManifestDigest: selected.ManifestDigest,
		GenerationDigest: selected.GenerationDigest, ManifestPath: selected.Manifest,
	}); err != nil {
		t.Fatal(err)
	}
	// All work is synchronous, and this selection changes only between settled
	// physical generations. The references above still read live store/controls.
	fixture.selected = input
}

func (fixture *productionRuntimeIdentity) execute(t *testing.T, ctx context.Context, profile PipelineProfile) *productionRuntimeResult {
	t.Helper()
	before := fixture.source.acquisitions
	prior, err := fixture.state.GetGenerationSchedule(ctx, fixture.selected.Repository, extractionpublication.ScheduleStage)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if prior != nil && (store.ValidateGenerationSchedule(*prior) != nil || prior.Status != store.GenerationScheduleSettled ||
		prior.Succeeded != prior.TotalChunks || prior.Failed != 0) {
		t.Fatal("physical transition lacks a genuinely settled predecessor")
	}
	generation, err := fixture.reconciler.Reconcile(ctx, fixture.selected.Repository)
	if err != nil {
		t.Fatal(err)
	}
	schedule := fixture.schedule(t, ctx)
	wantGeneration := generation
	if prior != nil {
		wantGeneration = SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + generation + "\x00" + prior.Digest))
	}
	if schedule.Generation != wantGeneration {
		t.Fatal("physical execution schedule lost its native predecessor chain")
	}
	bound, err := fixture.runtime.SchedulePlanningAuthority(fixture.selected.Repository, schedule.Generation)
	selected := fixture.selected
	if err != nil || bound.Repository != selected.Repository || bound.SourceGenerationDigest != selected.SourceDigest ||
		bound.ObservationGenerationDigest != selected.ObservationDigest {
		t.Fatalf("physical execution schedule changed its native planning authority: %+v, %v", bound, err)
	}
	t.Logf("native physical %s: executing %d extraction chunks", selected.ControlSuffix, schedule.TotalChunks)
	fixture.finish(t, ctx, schedule)
	result := fixture.snapshot(t, ctx, profile, generation)
	if fixture.source.acquisitions-before != result.Schedule.TotalChunks {
		t.Fatal("physical generation did not execute each native source partition exactly once")
	}
	t.Logf("native physical %s: settled %d extraction chunks and %d current domain roots", selected.ControlSuffix, schedule.TotalChunks, len(result.Domains))
	return result
}

func (fixture *productionRuntimeIdentity) schedule(t *testing.T, ctx context.Context) store.GenerationSchedule {
	t.Helper()
	value, err := fixture.state.GetGenerationSchedule(ctx, fixture.selected.Repository, extractionpublication.ScheduleStage)
	if err != nil || value == nil || store.ValidateGenerationSchedule(*value) != nil {
		t.Fatalf("read genuine extraction schedule: %+v, %v", value, err)
	}
	return *value
}

func (fixture *productionRuntimeIdentity) finish(t *testing.T, ctx context.Context, schedule store.GenerationSchedule) {
	t.Helper()
	for attempts := 0; schedule.NextOffset < schedule.TotalItems; attempts++ {
		if attempts >= schedule.TotalChunks {
			t.Fatal("native extraction expansion did not make bounded progress")
		}
		expanded, err := fixture.state.ExpandGenerationSchedule(ctx, schedule.Repository, schedule.Stage, schedule.Generation)
		if err != nil || expanded == nil || expanded.Digest != schedule.Digest {
			t.Fatalf("expand native extraction schedule: %+v, %v", expanded, err)
		}
		schedule = *expanded
	}
	seen := make(map[int64]bool, schedule.TotalChunks)
	for range schedule.TotalChunks {
		chunk, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "t421-native-identity-witness")
		if err != nil || chunk == nil || chunk.ScheduleDigest != schedule.Digest ||
			chunk.Generation != schedule.Generation || chunk.Length != extractionpublication.ScheduleChunkItems || seen[chunk.Offset] {
			t.Fatalf("claim exact native extraction partition: %+v, %v", chunk, err)
		}
		seen[chunk.Offset] = true
		if err := fixture.runtime.Handle(ctx, *chunk); err != nil {
			t.Fatalf("execute native extraction offset %d: %v", chunk.Offset, err)
		}
		if err := fixture.state.CompleteGenerationChunk(ctx, *chunk); err != nil {
			t.Fatal(err)
		}
	}
	settled := fixture.schedule(t, ctx)
	if settled.Digest != schedule.Digest || settled.Status != store.GenerationScheduleSettled ||
		settled.Succeeded != settled.TotalChunks || settled.Pending != 0 || settled.Running != 0 || settled.Failed != 0 {
		t.Fatalf("native extraction schedule did not genuinely settle: %+v", settled)
	}
}

func (fixture *productionRuntimeIdentity) snapshot(t *testing.T, ctx context.Context, profile PipelineProfile, generation string) *productionRuntimeResult {
	t.Helper()
	result := &productionRuntimeResult{Generation: generation, Schedule: fixture.schedule(t, ctx),
		Publications: make(map[string]store.PartitionedExtractionDomain, len(profile.ExtractionDomains))}
	status, err := fixture.runtime.Status(ctx, fixture.selected.Repository, generation)
	if err != nil || len(status.Domains) != len(profile.ExtractionDomains) {
		t.Fatalf("read complete native result status: %+v, %v", status, err)
	}
	for _, domain := range status.Domains {
		if !domain.Current || domain.Settled != domain.Expected || domain.Failed != 0 || domain.RetryExhausted != 0 {
			t.Fatalf("native result files are not complete and current: %+v", domain)
		}
	}
	for _, expected := range profile.ExtractionDomains {
		stored, err := fixture.state.GetPartitionedExtractionDomain(ctx, fixture.selected.Repository, expected.Domain)
		if err != nil || stored == nil || stored.Validate() != nil {
			t.Fatalf("read native current %s publication: %v", expected.Domain, err)
		}
		plan, err := candidate.DecodeDomainResultPlanControl(strings.NewReader(stored.Plan))
		if err != nil {
			t.Fatal(err)
		}
		root, err := candidate.DecodeDomainResultRoot(strings.NewReader(stored.Root), plan)
		if err != nil {
			t.Fatal(err)
		}
		current, err := fixture.runtime.Current(ctx, fixture.selected.Repository, expected.Domain)
		if err != nil || !reflect.DeepEqual(current, root) || root.Totals != t421ProductionReplayTotals(expected.Expected) {
			t.Fatalf("native %s store/filesystem root or totals disagree: %v", expected.Domain, err)
		}
		authority, err := candidate.NewDownstreamDomainAuthority(plan, root, stored.RunID)
		if err != nil {
			t.Fatal(err)
		}
		result.Publications[expected.Domain] = *stored
		result.Domains = append(result.Domains, authority)
	}
	return result
}

func (fixture *productionRuntimeIdentity) prepareRecoveries(t *testing.T, ctx context.Context, frozen Plan, baseline *productionRuntimeResult) {
	t.Helper()
	productionRecoveryCache = make(map[string]productionRecoverySchedule, 2)
	beforeSource, beforeAppends := fixture.source.acquisitions, fixture.evidence.appends
	resultFiles := fixture.resultFiles(t, ctx, baseline)
	for _, step := range []struct{ phase, mode string }{
		{"stale_lease", extractionpublication.RecoveryPreparationScheduleOnly},
		{"process_restart", extractionpublication.RecoveryPreparationCheckpoint},
	} {
		current := fixture.snapshot(t, ctx, frozen.Profile.Pipeline, baseline.Generation)
		if !reflect.DeepEqual(current.Publications, baseline.Publications) || current.Schedule.Status != store.GenerationScheduleSettled ||
			current.Schedule.Succeeded != current.Schedule.TotalChunks || current.Schedule.Pending != 0 || current.Schedule.Running != 0 || current.Schedule.Failed != 0 {
			t.Fatal("recovery predecessor is not the genuinely completed native generation")
		}
		if got, err := fixture.reconciler.Reconcile(ctx, fixture.selected.Repository); err != nil || got != baseline.Generation ||
			fixture.schedule(t, ctx).Digest != current.Schedule.Digest {
			t.Fatalf("completed ordinary reconciliation did not reuse its schedule: %q, %v", got, err)
		}
		authority, err := fixture.runtime.SchedulePlanningAuthority(fixture.selected.Repository, current.Schedule.Generation)
		if err != nil {
			t.Fatal(err)
		}
		request := extractionpublication.RecoveryPreparationRequest{
			Schema: extractionpublication.RecoveryPreparationSchema, Authority: authority,
			GenerationDigest: baseline.Generation, PriorScheduleDigest: current.Schedule.Digest, Mode: step.mode,
		}
		for _, domain := range frozen.Profile.Pipeline.ExtractionDomains {
			stored := current.Publications[domain.Domain]
			request.Roots = append(request.Roots, extractionpublication.RecoveryPreparationRoot{
				Domain: stored.Domain, PlanDigest: stored.PlanDigest, RootDigest: stored.RootDigest,
			})
		}
		for _, point := range frozen.FailurePoints {
			if point.Phase == step.phase {
				selected := current.Publications[point.TargetDomain]
				plan, err := candidate.DecodeDomainResultPlanControl(strings.NewReader(selected.Plan))
				if err != nil || point.TargetOrdinal >= uint64(len(plan.Expected)) {
					t.Fatalf("frozen preparation target is outside its actual native plan: %v", err)
				}
				request.TargetDomain, request.TargetOrdinal = point.TargetDomain, int(point.TargetOrdinal)
			}
		}
		prepared, err := fixture.reconciler.PrepareRecovery(ctx, request)
		if err != nil || prepared == nil {
			t.Fatalf("native %s completed-generation preparation: %v", step.phase, err)
		}
		if !reflect.DeepEqual(resultFiles, fixture.resultFiles(t, ctx, baseline)) {
			t.Fatal("preparation changed canonical partition result bytes")
		}
		wantGeneration := SHA256([]byte("phebs-extraction-recovery-schedule-v1\x00" + baseline.Generation + "\x00" + current.Schedule.Digest))
		wantDigest, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
			Repository: fixture.selected.Repository, Stage: extractionpublication.ScheduleStage,
			Generation: wantGeneration, ResourceClass: store.GenerationResourceExtraction,
			TotalItems: current.Schedule.TotalItems, ChunkItems: extractionpublication.ScheduleChunkItems,
			MaxAttempts: extractionpublication.ScheduleMaxAttempts, RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
		})
		if err != nil || prepared.Generation != wantGeneration || prepared.Digest != wantDigest || prepared.Digest == current.Schedule.Digest {
			t.Fatalf("native %s preparation broke its exact predecessor chain: %+v, %v", step.phase, prepared, err)
		}
		bound, err := fixture.runtime.SchedulePlanningAuthority(fixture.selected.Repository, prepared.Generation)
		if err != nil || bound != authority {
			t.Fatalf("native recovery binding changed the immutable authority: %+v, %v", bound, err)
		}
		fixture.finish(t, ctx, *prepared)
		after := fixture.snapshot(t, ctx, frozen.Profile.Pipeline, baseline.Generation)
		if !reflect.DeepEqual(after.Publications, baseline.Publications) || !reflect.DeepEqual(resultFiles, fixture.resultFiles(t, ctx, baseline)) ||
			fixture.source.acquisitions != beforeSource || fixture.evidence.appends != beforeAppends {
			t.Fatal("recovery changed native result/run/root evidence or performed source work")
		}
		productionRecoveryCache[step.phase] = productionRecoverySchedule{
			Target: baseline.Generation, Prior: current.Schedule.Digest,
			RecoveryGeneration: prepared.Generation, RecoverySchedule: prepared.Digest,
		}
		t.Logf("native %s preparation/recovery: completed %d result-reuse chunks, source acquisitions=0 evidence appends=0 (no stale-lease or process-death injection)", step.phase, prepared.TotalChunks)
	}
}

// Inspect, never create, the runtime's observed immutable result files. This
// constructor-only bounded byte check is not charged as ceremony measurement.
func (fixture *productionRuntimeIdentity) resultFiles(t *testing.T, ctx context.Context, baseline *productionRuntimeResult) map[string]string {
	t.Helper()
	result := make(map[string]string, baseline.Schedule.TotalChunks)
	entries := 0
	generationDirectory := string(filepath.Separator) + strings.TrimPrefix(baseline.Generation, "sha256:") + string(filepath.Separator)
	err := filepath.WalkDir(fixture.runtime.Root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 4_096 || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("constructor control inspection exceeded its regular-file inventory")
		}
		if entry.IsDir() || !strings.Contains(path, generationDirectory) || !strings.HasPrefix(entry.Name(), "result-") || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > candidate.MaxPartitionResultBytes || len(result) >= baseline.Schedule.TotalChunks {
			return errors.New("constructor result inspection exceeded its exact bound")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, candidate.MaxPartitionResultBytes+1))
		if err := errors.Join(readErr, file.Close()); err != nil {
			return err
		}
		if int64(len(raw)) > candidate.MaxPartitionResultBytes {
			return errors.New("constructor result grew beyond the read bound")
		}
		result[path] = SHA256(raw)
		return nil
	})
	if err != nil || len(result) != baseline.Schedule.TotalChunks {
		t.Fatalf("inspect complete canonical native result inventory: count=%d, %v", len(result), err)
	}
	return result
}

type productionIdentitySource struct {
	extractionpublication.Source
	acquisitions int
}

func (source *productionIdentitySource) AcquirePartition(ctx context.Context, plan candidate.DomainResultPlan, ordinal int) (extractionpublication.PartitionLease, error) {
	source.acquisitions++
	return source.Source.AcquirePartition(ctx, plan, ordinal)
}

type productionIdentityEvidence struct {
	*store.Surreal
	appends int
}

func (evidence *productionIdentityEvidence) AddEvidenceChunk(ctx context.Context, runID, chunkID string, factCount int, atoms []store.EvidenceAtom, associations []store.SnapshotEvidence, assertions []store.Assertion) error {
	evidence.appends++
	return evidence.Surreal.AddEvidenceChunk(ctx, runID, chunkID, factCount, atoms, associations, assertions)
}
