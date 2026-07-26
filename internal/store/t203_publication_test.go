package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

const t203WorkerChunkFacts = 256

// TestT203ProductionTargetPublicationIntegrity drives the frozen T20.1
// population through the public production writer limits. The old publication
// must remain the only visible run after a duplicate delivery, a caller-count
// lie, and cancellation; only the final store-recounted transaction may flip
// visibility.
func TestT203ProductionTargetPublicationIntegrity(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	const (
		repo      = "synthetic.invalid/t203-scale"
		commit    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		domain    = "t20-caller-publication"
		extractor = "t203-production-v1"
	)
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "file:///synthetic/t203-scale.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	previous, err := s.BeginExtractionRun(ctx, repo, commit, domain, extractor)
	if err != nil {
		t.Fatal(err)
	}
	atoms, assocs, assertions := t201EvidenceBatch(previous, 0, 1)
	if err := s.AddEvidence(ctx, previous.ID, atoms, assocs, assertions); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, previous.ID, t203Coverage(1)); err != nil {
		t.Fatal(err)
	}

	target, err := s.BeginExtractionRun(ctx, repo, commit, domain, extractor)
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < t201TargetFacts; offset += t203WorkerChunkFacts {
		end := min(offset+t203WorkerChunkFacts, t201TargetFacts)
		atoms, assocs, assertions = t201EvidenceBatch(target, offset, end)
		if err := s.AddEvidence(ctx, target.ID, atoms, assocs, assertions); err != nil {
			t.Fatalf("stage production chunk %d: %v", offset/t203WorkerChunkFacts, err)
		}
		if offset == 0 {
			// Exact transaction replay must change neither rows nor final
			// store-derived counts.
			if err := s.AddEvidence(ctx, target.ID, atoms, assocs, assertions); err != nil {
				t.Fatalf("replay first production chunk: %v", err)
			}
		}
	}
	assertOnlyPublishedRun(t, ctx, s, repo, domain, previous.ID)

	badCoverage := t203Coverage(t201TargetFacts)
	badCoverage.AssertionCount--
	if err := s.PublishExtractionRun(ctx, target.ID, badCoverage); !errors.Is(err, ErrConflict) {
		t.Fatalf("caller-count lie publish = %v", err)
	}
	assertOnlyPublishedRun(t, ctx, s, repo, domain, previous.ID)
	staged, err := s.getRun(ctx, target.ID)
	if err != nil || staged.Status != "staged" {
		t.Fatalf("target after refused recount = %+v, %v", staged, err)
	}

	canceled, cancelPublish := context.WithCancel(ctx)
	cancelPublish()
	if err := s.PublishExtractionRun(canceled, target.ID, t203Coverage(t201TargetFacts)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled target publish = %v", err)
	}
	assertOnlyPublishedRun(t, ctx, s, repo, domain, previous.ID)

	started := time.Now()
	if err := s.PublishExtractionRun(ctx, target.ID, t203Coverage(t201TargetFacts)); err != nil {
		t.Fatalf("publish target: %v", err)
	}
	publishWall := time.Since(started)
	if publishWall > 2*time.Second {
		t.Fatalf("target publication took %v; frozen ceiling is 2s", publishWall)
	}

	assertOnlyPublishedRun(t, ctx, s, repo, domain, target.ID)
	old, err := s.getRun(ctx, previous.ID)
	if err != nil || old.Status != "superseded" {
		t.Fatalf("previous run after target publish = %+v, %v", old, err)
	}
	counts := t203StoredCounts(t, ctx, s, target.ID)
	if counts.Associations != t201TargetFacts || counts.Assertions != t201TargetFacts ||
		counts.Atoms != t201TargetFacts || counts.Unresolved != 0 ||
		counts.ReferenceEdges != t201TargetFacts {
		t.Fatalf("store-derived target counts = %+v", counts)
	}
	t.Logf("T20.3 production publication: %d rows in %v",
		counts.Associations+counts.Assertions, publishWall)
}

func t203Coverage(facts int) CoverageManifest {
	return CoverageManifest{
		Protocols:       []string{"t20-neutral-scale-v1"},
		CorpusFileCount: 103, CandidateFileCount: 101, ReadFileCount: 103,
		ReadBytes: 1, SourceScopeDigest: "sha256:" + strings.Repeat("0", 64),
		AssertionCount: facts, AtomCount: facts,
	}
}

func assertOnlyPublishedRun(
	t *testing.T, ctx context.Context, s *Surreal, repo, domain, wantRunID string,
) {
	t.Helper()
	latest, err := s.LatestPublishedRun(ctx, repo, domain)
	if err != nil || latest.ID != wantRunID {
		t.Fatalf("latest publication = %+v, %v; want %s", latest, err, wantRunID)
	}
	rows, err := surrealdb.Query[[]extractionRunIdentityRec](ctx, s.db,
		`SELECT id FROM extraction_run
			WHERE repo = $repo AND domain = $domain AND status = 'published'`,
		map[string]any{"repo": repo, "domain": domain})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, result := range *rows {
		count += len(result.Result)
	}
	if count != 1 {
		t.Fatalf("visible physical publication count = %d, want 1", count)
	}
}

type t203Counts struct {
	Associations   int `json:"associations"`
	Assertions     int `json:"assertions"`
	Atoms          int `json:"atoms"`
	Unresolved     int `json:"unresolved"`
	ReferenceEdges int `json:"reference_edges"`
}

func t203StoredCounts(t *testing.T, ctx context.Context, s *Surreal, runID string) t203Counts {
	t.Helper()
	results, err := surrealdb.Query[[]t203Counts](ctx, s.db,
		`RETURN [{
			associations: array::len(SELECT VALUE occurrence_id FROM snapshot_evidence WHERE run_id = $run),
			assertions: array::len(SELECT VALUE assertion_id FROM assertion WHERE run_id = $run),
			atoms: array::len(array::distinct(SELECT VALUE atom_id FROM snapshot_evidence WHERE run_id = $run)),
			unresolved: array::len(SELECT VALUE assertion_id FROM assertion
				WHERE run_id = $run AND tier = 'unresolved'),
			reference_edges:
				array::len(array::flatten(SELECT VALUE supporting FROM assertion WHERE run_id = $run)) +
				array::len(array::flatten(SELECT VALUE contradicting FROM assertion WHERE run_id = $run))
		}]`, map[string]any{"run": runID})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatal("store-derived count query returned no row")
	return t203Counts{}
}
