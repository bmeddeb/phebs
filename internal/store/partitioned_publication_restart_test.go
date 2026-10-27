package store

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// A bounded real-engine control for V19's store_result_count refusal. It uses
// only a fresh test-owned database; it never opens a retained native fixture.
func TestPartitionedPublicationReplayAfterEngineRestart(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	for _, hardDeath := range []bool{false, true} {
		name := "graceful"
		if hardDeath {
			name = "hard_death"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			directory := t.TempDir()
			state, err := OpenLocal(ctx, directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if state != nil {
					bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
					defer stop()
					if err := state.Close(bounded); err != nil {
						t.Error("test-owned store close", err)
					}
				}
			})
			const repository = "synthetic.invalid/publication-restart"
			commit := strings.Repeat("a", 40)
			digest := func(s string) string { return "sha256:" + strings.Repeat(s, 64) }
			if err := state.UpsertRepo(ctx, Repo{Name: repository, CloneURL: "https://example.invalid/restart.git"}); err != nil {
				t.Fatal(err)
			}
			if err := state.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if err := state.PublishCandidateManifest(ctx, CandidateManifestPublication{Repository: repository, HeadCommit: commit,
				PolicyDigest: digest("1"), ManifestDigest: digest("2"), GenerationDigest: digest("3"), ManifestPath: candidateid.ManifestName(repository)}); err != nil {
				t.Fatal(err)
			}
			run, err := state.BeginPartitionedExtractionRun(ctx, ExtractionScope{Repository: repository, Commit: commit, Domain: "proto-contract"},
				"publication-restart-v1", digest("4"), digest("2"), "", PartitionedExtractionRunLimits{Facts: 1, Rows: 2, References: 1})
			if err != nil {
				t.Fatal(err)
			}
			atoms, associations, assertions := t407Batch(repository, commit, 0)
			if err := state.AddEvidenceChunk(ctx, run.ID, digest("5"), 1, atoms, associations, assertions); err != nil {
				t.Fatal(err)
			}
			publication := PartitionedExtractionDomain{Schema: PartitionedExtractionDomainSchema, Repository: repository, Domain: "proto-contract",
				RunID: run.ID, PlanDigest: digest("4"), RootDigest: digest("6"), CandidateDigest: digest("2"),
				SourceDigest: digest("7"), ObservationDigest: digest("8"), Facts: 1, Rows: 2, References: 1, Plan: `{}`, Root: `{}`}
			if err := state.PublishPartitionedExtractionDomain(ctx, publication); err != nil {
				t.Fatal("initial publication", err)
			}
			if hardDeath {
				if state.engine == nil || state.engine.process == nil {
					t.Fatal("exact owned engine unavailable")
				}
				if err := state.engine.process.Kill(); err != nil {
					t.Fatal("kill test-owned engine", err)
				}
			}
			if err := state.Close(ctx); err != nil && !hardDeath {
				t.Fatal("close predecessor", err)
			}
			state = nil
			state, err = OpenLocal(ctx, directory)
			if err != nil {
				t.Fatal("reopen test-owned database", err)
			}
			var stage readaccounting.PublicationFailureStage
			observed := readaccounting.WithPublicationFailureObserver(ctx, func(s readaccounting.PublicationFailureStage, _ error, _ readaccounting.PublicationFailureFlags) {
				stage = s
			})
			if err := state.PublishPartitionedExtractionDomain(observed, publication); err != nil {
				t.Fatalf("exact sealed publication after restart: stage=%s error=%v", stage, err)
			}
			stored, err := state.GetPartitionedExtractionDomain(ctx, repository, publication.Domain)
			if err != nil || stored == nil || !samePartitionedDomain(*stored, publication) {
				t.Fatal("restart changed immutable publication", err)
			}
			wrongCounts := publication
			wrongCounts.Facts++
			if err := state.PublishPartitionedExtractionDomain(ctx, wrongCounts); !errors.Is(err, ErrConflict) {
				t.Fatal("restart weakened exact accounting refusal", err)
			}
		})
	}
}
