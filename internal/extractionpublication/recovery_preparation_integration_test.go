package extractionpublication_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

// This is a real scheduler/evidence-store and native execution-control test,
// not an ordinary-server, upstream-observation, or ceremony-scale replay. Only
// the selected source/observation authority inputs are fixture values. Git,
// candidate publication, extraction, evidence, results, completion controls,
// roots, scheduler leases, and settlement all use their production paths.
func TestRecoveryPreparationRealStoreCompletedGeneration(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	fixture := newRecoveryPreparationFixture(t, ctx)
	generation, err := fixture.reconciler.Reconcile(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	schedule := fixture.schedule(t, ctx)
	if schedule.Generation != generation || schedule.TotalItems != 1 || schedule.TotalChunks != 1 {
		t.Fatalf("cold schedule is not one native partition: %+v", schedule)
	}
	fixture.finishSchedule(t, ctx, schedule)
	initial := fixture.schedule(t, ctx)
	baseline := fixture.publication(t, ctx)
	if baseline.root.ExpectedResults != 1 || baseline.root.ResultCount != 1 || baseline.root.Totals.Facts == 0 ||
		len(baseline.assertions) == 0 || fixture.source.acquisitions != 1 || fixture.evidence.appends == 0 {
		t.Fatal("precondition lacks actual native content, evidence, and completed publication")
	}
	controls := recoveryPreparationFiles(t, fixture.runtime.Root)
	paths := recoveryPreparationControlPaths(t, controls, generation)
	assertRecoveryPreparationCompletion(t, controls[paths.completion].content, baseline.root.PlanDigest, 1)
	request := extractionpublication.RecoveryPreparationRequest{
		Schema:    extractionpublication.RecoveryPreparationSchema,
		Authority: fixture.authority, GenerationDigest: generation, PriorScheduleDigest: initial.Digest,
		Roots: []extractionpublication.RecoveryPreparationRoot{{
			Domain: baseline.root.Domain, PlanDigest: baseline.root.PlanDigest, RootDigest: baseline.root.Digest,
		}},
		Mode: extractionpublication.RecoveryPreparationScheduleOnly, TargetDomain: baseline.root.Domain, TargetOrdinal: 0,
	}
	if _, err := fixture.reconciler.PrepareRecovery(ctx, request); !errors.Is(err, extractionpublication.ErrRecoveryPreparationDisabled) {
		t.Fatalf("default-inactive recovery preparation returned %v", err)
	}
	if !reflect.DeepEqual(controls, recoveryPreparationFiles(t, fixture.runtime.Root)) ||
		!reflect.DeepEqual(initial, fixture.schedule(t, ctx)) ||
		!reflect.DeepEqual(baseline, fixture.publication(t, ctx)) {
		t.Fatal("disabled preparation changed controls, schedule, or evidence authority")
	}
	if got, err := fixture.reconciler.Reconcile(ctx, fixture.repository); err != nil || got != generation {
		t.Fatalf("completed ordinary reconciliation = %q, %v", got, err)
	}
	if after := fixture.schedule(t, ctx); after.Digest != initial.Digest || after.Status != store.GenerationScheduleSettled {
		t.Fatal("ordinary reconciliation forced a completed same-target schedule")
	}
	// This test drives workers synchronously. Configure before the next worker
	// call; a production owner must set this before starting worker goroutines.
	fixture.reconciler.RecoveryPreparationEnabled = true
	appends := fixture.evidence.appends
	for _, mode := range []string{
		extractionpublication.RecoveryPreparationScheduleOnly,
		extractionpublication.RecoveryPreparationCheckpoint,
	} {
		t.Run(mode, func(t *testing.T) {
			before := fixture.schedule(t, ctx)
			beforeFiles := recoveryPreparationFiles(t, fixture.runtime.Root)
			request.PriorScheduleDigest, request.Mode = before.Digest, mode
			prepared, err := fixture.reconciler.PrepareRecovery(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			wantGeneration := recoveryPreparationDigest("phebs-extraction-recovery-schedule-v1\x00" + generation + "\x00" + before.Digest)
			wantDigest, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
				Repository: fixture.repository, Stage: extractionpublication.ScheduleStage,
				Generation: wantGeneration, ResourceClass: store.GenerationResourceExtraction,
				TotalItems: 1, ChunkItems: extractionpublication.ScheduleChunkItems,
				MaxAttempts:      extractionpublication.ScheduleMaxAttempts,
				RepositoryTokens: extractionpublication.ScheduleRepositoryTokens,
			})
			if err != nil || prepared == nil || prepared.Generation != wantGeneration || prepared.Digest != wantDigest ||
				prepared.Digest == before.Digest || store.ValidateGenerationSchedule(*prepared) != nil ||
				prepared.Status != store.GenerationScheduleActive || prepared.Succeeded != 0 || prepared.Running != 0 ||
				prepared.Failed != 0 || prepared.TotalChunks != 1 {
				t.Fatalf("prepared schedule is not the exact new bounded successor: %+v, %v", prepared, err)
			}
			bound, err := fixture.runtime.SchedulePlanningAuthority(fixture.repository, prepared.Generation)
			if err != nil || bound != fixture.authority {
				t.Fatalf("new scheduler generation lacks its native target binding: %+v, %v", bound, err)
			}
			if !reflect.DeepEqual(baseline, fixture.publication(t, ctx)) {
				t.Fatal("preparation changed the current store publication or evidence")
			}
			afterFiles := recoveryPreparationFiles(t, fixture.runtime.Root)
			assertRecoveryPreparationFileChanges(t, beforeFiles, afterFiles, paths, mode)
			if mode == extractionpublication.RecoveryPreparationCheckpoint {
				if _, err := fixture.runtime.Current(ctx, fixture.repository, baseline.root.Domain); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("checkpoint retained a filesystem current pointer: %v", err)
				}
				assertRecoveryPreparationCompletion(t, afterFiles[paths.completion].content, baseline.root.PlanDigest, 0)
			}
			fixture.finishSchedule(t, ctx, *prepared)
			if !reflect.DeepEqual(baseline, fixture.publication(t, ctx)) {
				t.Fatal("recovery replaced the original run, root, or evidence")
			}
			current, err := fixture.runtime.Current(ctx, fixture.repository, baseline.root.Domain)
			if err != nil || !reflect.DeepEqual(current, baseline.root) {
				t.Fatalf("recovery did not restore the same native current root: %v", err)
			}
			restored := recoveryPreparationFiles(t, fixture.runtime.Root)
			for path, beforeFile := range beforeFiles {
				if !bytes.Equal(beforeFile.content, restored[path].content) {
					t.Fatalf("recovery changed pre-existing control bytes: %s", path)
				}
			}
			if fixture.source.acquisitions != 1 || fixture.evidence.appends != appends {
				t.Fatal("completed-result recovery reopened source or appended evidence")
			}
			if got, err := fixture.reconciler.Reconcile(ctx, fixture.repository); err != nil || got != generation ||
				fixture.schedule(t, ctx).Digest != prepared.Digest || fixture.source.acquisitions != 1 || fixture.evidence.appends != appends {
				t.Fatalf("post-recovery no-op changed the completed schedule: %q, %v", got, err)
			}
		})
		if t.Failed() {
			return
		}
	}
}

type recoveryPreparationFixture struct {
	repository string
	state      *store.Surreal
	runtime    *extractionpublication.Runtime
	reconciler *extractionpublication.Reconciler
	authority  extractionpublication.PlanningAuthority
	commit     string
	source     *recoveryPreparationSource
	evidence   *recoveryPreparationEvidence
}

func newRecoveryPreparationFixture(t *testing.T, ctx context.Context) *recoveryPreparationFixture {
	t.Helper()
	const repository = "example.invalid/recovery-preparation"
	dataDir := t.TempDir()
	state, err := store.OpenLocalMemory(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := state.Close(cleanup); err != nil {
			t.Errorf("close recovery preparation store: %v", err)
		}
	})
	repositoryDir, err := phebssync.SafeRepoDir(dataDir, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repositoryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	recoveryPreparationGit(t, ctx, repositoryDir, "init", "-q")
	const proto = "syntax = \"proto3\";\npackage neutral;\nmessage Request {}\nmessage Response {}\nservice Orders { rpc Get(Request) returns (Response); }\n"
	if err := os.WriteFile(filepath.Join(repositoryDir, "orders.proto"), []byte(proto), 0o600); err != nil {
		t.Fatal(err)
	}
	recoveryPreparationGit(t, ctx, repositoryDir, "add", "--", "orders.proto")
	recoveryPreparationGit(t, ctx, repositoryDir, "-c", "user.name=Neutral", "-c", "user.email=neutral@example.invalid", "commit", "-q", "-m", "neutral recovery fixture")
	commit := strings.TrimSpace(recoveryPreparationGit(t, ctx, repositoryDir, "rev-parse", "HEAD"))
	extractor := protodecl.New()
	policies := []candidate.Policy{{
		Domain: extractor.Domain(), Version: extractor.Version(), Plane: candidate.PlaneRepository,
		EnumerationPolicy: "proto-contract-paths-v1", Enumerate: extractor.Candidate, Required: extractor.Candidate,
	}}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot, candidateStage := filepath.Join(dataDir, "candidates"), filepath.Join(dataDir, "candidate-stage")
	for _, directory := range []string{candidateRoot, candidateStage} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := candidate.Build(ctx, candidate.Request{
		RepoDir: repositoryDir, OutputDir: candidateStage, Repository: repository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := candidate.Expected{
		Repository: repository, Commit: commit, Policies: identities, PolicyDigest: manifest.PolicyDigest,
		GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest,
	}
	selected, err := candidate.PublishContext(ctx, candidateRoot, candidateStage, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.FinishPublication(candidateRoot, repository); err != nil {
		t.Fatal(err)
	}
	if err := state.UpsertRepo(ctx, store.Repo{Name: repository, CloneURL: "file://" + repositoryDir}); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := state.PublishCandidateManifest(ctx, store.CandidateManifestPublication{
		Repository: repository, HeadCommit: commit, ManifestPath: candidateid.ManifestName(repository),
		PolicyDigest: selected.PolicyDigest, ManifestDigest: selected.ManifestDigest, GenerationDigest: selected.GenerationDigest,
	}); err != nil {
		t.Fatal(err)
	}
	fixture := &recoveryPreparationFixture{repository: repository, state: state, commit: commit}
	fixture.authority = extractionpublication.PlanningAuthority{
		Repository: repository, CandidateManifestDigest: selected.ManifestDigest,
		CandidateGenerationDigest: selected.GenerationDigest, CandidatePolicyDigest: selected.PolicyDigest,
		SourceGenerationDigest:      recoveryPreparationDigest("selected test source"),
		ObservationGenerationDigest: recoveryPreparationDigest("selected test observation"),
	}
	fixture.evidence = &recoveryPreparationEvidence{Surreal: state}
	fixture.runtime = &extractionpublication.Runtime{
		Root: filepath.Join(dataDir, "extraction-publications"), Store: state,
		Executor:  &extract.EvidencePartitionExecutor{Evidence: fixture.evidence, Extractors: []extract.Extractor{extractor}},
		Publisher: extractionpublication.StorePublisher{Store: state},
	}
	readAuthority := func(context.Context, string) (string, string, error) {
		return fixture.authority.SourceGenerationDigest, fixture.authority.ObservationGenerationDigest, nil
	}
	readCandidate := func(ctx context.Context, repository string) (candidate.State, error) {
		current, err := state.GetCandidateManifestPublication(ctx, repository)
		if err != nil {
			return candidate.State{}, err
		}
		if current == nil {
			return candidate.State{}, store.ErrNotFound
		}
		value := selected
		value.Repository, value.Commit, value.UnitDigest = current.Repository, current.HeadCommit, current.UnitDigest
		value.PolicyDigest, value.ManifestDigest = current.PolicyDigest, current.ManifestDigest
		value.GenerationDigest, value.Manifest = current.GenerationDigest, current.ManifestPath
		return value, nil
	}
	fixture.reconciler = &extractionpublication.Reconciler{
		Root: fixture.runtime.Root, CandidateRoot: candidateRoot, Runtime: fixture.runtime, Evidence: state,
		OpenCandidate: func(ctx context.Context, _ string) (*candidate.Publication, error) {
			return candidate.OpenContext(ctx, candidateRoot, expected)
		},
		CandidateReference: readCandidate,
		Authority:          readAuthority, AuthorityReference: readAuthority,
	}
	fixture.source = &recoveryPreparationSource{Source: extractionpublication.GitSparseSource{
		DataDir: dataDir, OpenDomain: fixture.reconciler.OpenDomain,
	}}
	fixture.runtime.Source = fixture.source
	fixture.runtime.Fence = extractionpublication.AuthorityFence{
		Store: state,
		Acquire: func(ctx context.Context) (func(), error) {
			return focusedindex.AcquireExclusiveMutationLock(ctx, filepath.Join(dataDir, "index"))
		},
		Current: func(ctx context.Context, plan candidate.DomainResultPlan) error {
			current, err := readCandidate(ctx, plan.Repository)
			if err != nil {
				return err
			}
			if current.Repository != repository || current.Commit != commit || current.UnitDigest != selected.UnitDigest ||
				current.ManifestDigest != plan.CandidateManifestDigest || current.GenerationDigest != plan.CandidateGenerationDigest ||
				current.PolicyDigest != plan.CandidatePolicyDigest || plan.SourceGenerationDigest != fixture.authority.SourceGenerationDigest ||
				plan.ObservationGenerationDigest != fixture.authority.ObservationGenerationDigest {
				return extractionpublication.ErrStale
			}
			return nil
		},
	}
	return fixture
}

func (fixture *recoveryPreparationFixture) schedule(t *testing.T, ctx context.Context) store.GenerationSchedule {
	t.Helper()
	value, err := fixture.state.GetGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage)
	if err != nil || value == nil || store.ValidateGenerationSchedule(*value) != nil {
		t.Fatalf("read actual schedule: %+v, %v", value, err)
	}
	return *value
}

func (fixture *recoveryPreparationFixture) finishSchedule(t *testing.T, ctx context.Context, schedule store.GenerationSchedule) {
	t.Helper()
	if _, err := fixture.state.ExpandGenerationSchedule(ctx, fixture.repository, extractionpublication.ScheduleStage, schedule.Generation); err != nil {
		t.Fatal(err)
	}
	chunk, err := fixture.state.ClaimGenerationChunk(ctx, store.GenerationResourceExtraction, "native-recovery-test")
	if err != nil || chunk == nil || chunk.ScheduleDigest != schedule.Digest || chunk.Offset != 0 || chunk.Length != 1 {
		t.Fatalf("claim exact bounded partition: %+v, %v", chunk, err)
	}
	if err := fixture.runtime.Handle(ctx, *chunk); err != nil {
		t.Fatal(err)
	}
	if err := fixture.state.CompleteGenerationChunk(ctx, *chunk); err != nil {
		t.Fatal(err)
	}
	settled := fixture.schedule(t, ctx)
	if settled.Digest != schedule.Digest || settled.Status != store.GenerationScheduleSettled ||
		settled.Succeeded != 1 || settled.Pending != 0 || settled.Running != 0 || settled.Failed != 0 {
		t.Fatalf("actual scheduler did not settle the completed native partition: %+v", settled)
	}
}

type recoveryPreparationPublication struct {
	stored     store.PartitionedExtractionDomain
	root       candidate.DomainResultRoot
	assertions []store.Assertion
	atoms      map[string]store.EvidenceResolution
}

func (fixture *recoveryPreparationFixture) publication(t *testing.T, ctx context.Context) recoveryPreparationPublication {
	t.Helper()
	stored, err := fixture.state.GetPartitionedExtractionDomain(ctx, fixture.repository, "proto-contract")
	if err != nil || stored == nil {
		t.Fatalf("read current store root: %v", err)
	}
	plan, err := candidate.DecodeDomainResultPlanControl(strings.NewReader(stored.Plan))
	if err != nil {
		t.Fatal(err)
	}
	root, err := candidate.DecodeDomainResultRoot(strings.NewReader(stored.Root), plan)
	if err != nil {
		t.Fatal(err)
	}
	authority := store.PartitionedAssertionAuthority{
		Repository: fixture.repository, Domain: stored.Domain, RunID: stored.RunID,
		PlanDigest: stored.PlanDigest, RootDigest: stored.RootDigest, Commit: fixture.commit,
		CandidateManifestDigest: plan.CandidateManifestDigest, CandidatePolicyDigest: plan.CandidatePolicyDigest,
	}
	assertions, err := fixture.state.ListPartitionedAssertions(ctx, store.AssertionQuery{
		Repo: fixture.repository, RunID: stored.RunID, Limit: 128,
	}, authority)
	if err != nil {
		t.Fatal(err)
	}
	atoms := make(map[string]store.EvidenceResolution)
	for _, assertion := range assertions {
		for _, refs := range [][]string{assertion.Supporting, assertion.Contradicting} {
			for _, atomID := range refs {
				if _, present := atoms[atomID]; present {
					continue
				}
				if len(atoms) >= 128 {
					t.Fatal("small evidence fixture exceeded its atom bound")
				}
				atom, err := fixture.state.ResolvePartitionedEvidence(ctx, authority, atomID)
				if err != nil || atom == nil {
					t.Fatalf("resolve original native evidence: %v", err)
				}
				atoms[atomID] = *atom
			}
		}
	}
	return recoveryPreparationPublication{stored: *stored, root: root, assertions: assertions, atoms: atoms}
}

type recoveryPreparationSource struct {
	extractionpublication.Source
	acquisitions int
}

func (source *recoveryPreparationSource) AcquirePartition(ctx context.Context, plan candidate.DomainResultPlan, ordinal int) (extractionpublication.PartitionLease, error) {
	source.acquisitions++
	return source.Source.AcquirePartition(ctx, plan, ordinal)
}

type recoveryPreparationEvidence struct {
	*store.Surreal
	appends int
}

func (evidence *recoveryPreparationEvidence) AddEvidenceChunk(ctx context.Context, runID, chunkID string, factCount int, atoms []store.EvidenceAtom, associations []store.SnapshotEvidence, assertions []store.Assertion) error {
	evidence.appends++
	return evidence.Surreal.AddEvidenceChunk(ctx, runID, chunkID, factCount, atoms, associations, assertions)
}

type recoveryPreparationFile struct {
	content []byte
	mode    fs.FileMode
	mtime   int64
}

func recoveryPreparationFiles(t *testing.T, root string) map[string]recoveryPreparationFile {
	t.Helper()
	result := make(map[string]recoveryPreparationFile)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if len(result) >= 128 || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("native test controls exceed the closed regular-file inventory")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file := recoveryPreparationFile{mode: info.Mode(), mtime: info.ModTime().UnixNano()}
		if !entry.IsDir() {
			if !info.Mode().IsRegular() || info.Size() > 4<<20 {
				return errors.New("native test control is not a bounded regular file")
			}
			opened, err := os.Open(path)
			if err != nil {
				return err
			}
			file.content, err = io.ReadAll(io.LimitReader(opened, (4<<20)+1))
			err = errors.Join(err, opened.Close())
			if err != nil {
				return err
			}
			if len(file.content) > 4<<20 {
				return errors.New("native test control grew above the read ceiling")
			}
		}
		result[relative] = file
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

type recoveryPreparationPaths struct{ pointer, root, completion, result string }

func recoveryPreparationControlPaths(t *testing.T, files map[string]recoveryPreparationFile, generation string) recoveryPreparationPaths {
	t.Helper()
	var paths recoveryPreparationPaths
	var generationDirectory, rootName string
	for path, file := range files {
		if filepath.Base(path) == "generation.json" {
			var value extractionpublication.Generation
			if json.Unmarshal(file.content, &value) == nil && value.Digest == generation {
				generationDirectory = filepath.Dir(path)
			}
		}
		if strings.HasPrefix(filepath.Base(path), "current-") {
			var value extractionpublication.Pointer
			if json.Unmarshal(file.content, &value) == nil && value.GenerationDigest == generation {
				paths.pointer, rootName = path, value.RootName
			}
		}
	}
	if generationDirectory == "" || paths.pointer == "" || rootName == "" {
		t.Fatal("actual completed control inventory has no exact generation/current pointer")
	}
	paths.root = filepath.Join(generationDirectory, rootName)
	paths.completion = filepath.Join(filepath.Dir(paths.root), "completion.json")
	paths.result = filepath.Join(filepath.Dir(paths.root), "result-00000.json")
	for _, path := range []string{paths.pointer, paths.root, paths.completion, paths.result} {
		if file, exists := files[path]; !exists || len(file.content) == 0 {
			t.Fatalf("actual completed control is absent: %s", path)
		}
	}
	return paths
}

func assertRecoveryPreparationCompletion(t *testing.T, raw []byte, plan string, count int) {
	t.Helper()
	var control struct {
		Schema     string `json:"schema"`
		PlanDigest string `json:"plan_digest"`
		Expected   int    `json:"expected"`
		Count      int    `json:"count"`
		Bits       []byte `json:"bits"`
	}
	if err := json.Unmarshal(raw, &control); err != nil || control.Schema != extractionpublication.CompletionSchema ||
		control.PlanDigest != plan || control.Expected != 1 || control.Count != count ||
		len(control.Bits) != 1 || control.Bits[0] != byte(count) {
		t.Fatalf("native completion control does not match the exact one-bit checkpoint: %+v, %v", control, err)
	}
}

func assertRecoveryPreparationFileChanges(t *testing.T, before, after map[string]recoveryPreparationFile, paths recoveryPreparationPaths, mode string) {
	t.Helper()
	for path, old := range before {
		current, exists := after[path]
		if mode == extractionpublication.RecoveryPreparationCheckpoint {
			switch path {
			case paths.pointer, paths.root:
				if exists {
					t.Fatalf("checkpoint retained reconstructible target control: %s", path)
				}
				continue
			case paths.completion:
				if !exists || bytes.Equal(old.content, current.content) {
					t.Fatal("checkpoint did not change its exact completion control")
				}
				continue
			}
		}
		if !exists || old.mode != current.mode || !bytes.Equal(old.content, current.content) {
			t.Fatalf("preparation changed a protected control: %s", path)
		}
	}
	added := 0
	for path, current := range after {
		if _, exists := before[path]; exists {
			continue
		}
		var binding struct {
			Schema string `json:"schema"`
		}
		if current.mode.IsDir() || json.Unmarshal(current.content, &binding) != nil || binding.Schema != extractionpublication.BindingSchema {
			t.Fatalf("preparation created a non-binding control: %s", path)
		}
		added++
	}
	if added != 1 {
		t.Fatalf("preparation created %d binding controls, want exactly one", added)
	}
}

func recoveryPreparationGit(t *testing.T, ctx context.Context, directory string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("neutral Git fixture: %v: %s", err, output)
	}
	return string(output)
}

func recoveryPreparationDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
