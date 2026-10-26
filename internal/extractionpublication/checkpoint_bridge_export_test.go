package extractionpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

// Test-only fixture export, never a production API. The caller supplies a new
// private directory; only bounded generated runtime controls are copied there.
// No existing native fixture, database or corpus is accessed.
func TestCheckpointBridgeExportHelper(t *testing.T) {
	out := os.Getenv("PHEBS_TEST_CHECKPOINT_BRIDGE_EXPORT")
	if out == "" {
		return
	}
	if !filepath.IsAbs(out) || os.Mkdir(out, 0o700) != nil {
		t.Fatal("bridge export requires a new absolute directory")
	}
	const repository = "local/tmp/t422-source" // Same logical name as the inherited command fixture; never opened as a host path.
	repo, candidates := t.TempDir(), t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "fixture@example.invalid")
	runGit(t, repo, "config", "user.name", "Checkpoint Fixture")
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("f%d.proto", i)), []byte("syntax = \"proto3\";\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "add", "--", "f0.proto", "f1.proto", "f2.proto")
	runGit(t, repo, "commit", "-q", "-m", "bounded fixture")
	commit := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	policies := []candidate.Policy{{Domain: "proto-contract", Version: "3.0.0", EnumerationPolicy: "bridge-proto-v1", Plane: candidate.PlaneLocal,
		ExecutionPartitionPolicy: candidate.WholeRepositoryExecutionSubrangePolicy, ExecutionMaxRecords: 1,
		Enumerate: func(p string) bool { return strings.HasSuffix(p, ".proto") }, Required: func(p string) bool { return strings.HasSuffix(p, ".proto") }}}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := candidate.PolicyDigest(identities)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := candidate.Build(t.Context(), candidate.Request{RepoDir: repo, OutputDir: candidates, Repository: repository, Commit: commit, Policies: policies})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidates, candidate.Expected{Repository: repository, Commit: commit, Policies: identities,
		PolicyDigest: policy, GenerationDigest: manifest.GenerationDigest, ManifestDigest: manifest.Digest})
	if err != nil {
		t.Fatal(err)
	}
	fixture := newRecoveryPreparationFixtureFromCandidate(t, RecoveryPreparationCheckpoint, repository,
		candidateFixture{repository: repository, repositoryDirectory: repo, candidateDirectory: candidates, publication: publication})
	if len(fixture.domain.Plan.Expected) != 3 {
		t.Fatal("fixture must retain genuine ordinal two", len(fixture.domain.Plan.Expected))
	}
	fixture.request.TargetOrdinal = 2
	schedule, err := fixture.reconciler.PrepareRecovery(t.Context(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	runtime := fixture.reconciler.Runtime
	state := &staleLeaseTransitionTestStore{testScheduleStore: fixture.schedules.testScheduleStore}
	runtime.Store = state
	chunk := currentChunk(t, state.testScheduleStore, repository, 2)
	chunk.Identity, err = store.GenerationChunkIdentity(schedule.Digest, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	chunk.LeaseToken = "killed-native-fixture-lease"
	cut := errors.New("bounded checkpoint cut")
	var event store.GenerationStaleLeaseTransition
	runtime.OnPartitionCheckpoint = func(_ context.Context, value store.GenerationStaleLeaseTransition) error {
		event = value
		return cut
	}
	if err := runtime.Handle(t.Context(), chunk); !errors.Is(err, cut) {
		t.Fatal("actual reused-result checkpoint did not stop", err)
	}
	row := event
	row.ChunkStateDigest, row.CheckpointStateDigest = digest("bridge-row", nil), digest("bridge-stable-row", nil)
	state.set(row, false)
	result, present, err := readPartitionResult(filepath.Join(fixture.resultDirectory(), resultName(2)), fixture.domain.Plan, 2)
	if err != nil || !present {
		t.Fatal(err)
	}
	request := CheckpointRestartTransitionRequest{Transition: event, TargetGeneration: fixture.request.GenerationDigest,
		PriorScheduleDigest: fixture.request.PriorScheduleDigest, Domain: "proto-contract", Ordinal: 2,
		PlanDigest: fixture.domain.Plan.Digest, ResultIdentity: result.Identity}
	hit := readCheckpointRestartTransition(t, runtime, request)
	hit.ObservedScheduleChunks, hit.ObservedScheduleSuccesses = 3, 2 // Supplied store inventory, not database proof.
	metadata := struct {
		Repository string                      `json:"repository"`
		Hit        CheckpointRestartTransition `json:"hit"`
		Root       RecoveryPreparationRoot     `json:"root"`
		Schedule   store.GenerationSchedule    `json:"schedule"`
	}{repository, hit, fixture.request.Roots[0], *schedule}
	raw, err := json.Marshal(metadata)
	if err != nil || os.WriteFile(filepath.Join(out, "metadata.json"), raw, 0o600) != nil {
		t.Fatal("metadata export refused", err)
	}
	files, total := 0, int64(0)
	err = filepath.WalkDir(runtime.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(runtime.Root, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(out, "runtime", rel)
		if entry.IsDir() {
			return os.Mkdir(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return errors.New("non-bounded bridge control")
		}
		files++
		total += info.Size()
		if files > 128 || total > 1<<20 {
			return errors.New("bridge export bound exceeded")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bounded checkpoint controls exported: files=%d bytes=%d", files, total)
}
