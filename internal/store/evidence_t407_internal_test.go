package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestT407AppendStatementHasNoWholeRunProofScan(t *testing.T) {
	for _, forbidden := range []string{
		"FROM snapshot_evidence WHERE run_id = $run_id",
		"FROM assertion WHERE run_id = $run_id",
		"flatten(SELECT VALUE supporting",
		"flatten(SELECT VALUE contradicting",
	} {
		if strings.Contains(addEvidenceSQL, forbidden) {
			t.Fatalf("append statement regained whole-run scan %q", forbidden)
		}
	}
	for _, required := range []string{
		"FROM $chunk_rid LIMIT 1",
		"staged_fact_count += $fact_count",
		"staged_row_count += 1",
		"staged_reference_count += $reference_delta",
	} {
		if !strings.Contains(addEvidenceSQL, required) {
			t.Fatalf("append statement lost bounded accounting primitive %q", required)
		}
	}
}

type t407AccountingRec struct {
	StagedRevision       int `json:"staged_revision"`
	StagedFactCount      int `json:"staged_fact_count"`
	StagedRowCount       int `json:"staged_row_count"`
	StagedReferenceCount int `json:"staged_reference_count"`
	StagedChunkCount     int `json:"staged_chunk_count"`
}

func t407Batch(repo, commit string, index int) (
	[]EvidenceAtom, []SnapshotEvidence, []Assertion,
) {
	atom := EvidenceAtom{
		SchemaVersion:       "t40.7-test-v1",
		BlobDigest:          fmt.Sprintf("sha256:%064x", index+1),
		StartByte:           index * 8,
		EndByte:             index*8 + 4,
		RuleID:              "t40.7-rule-v1",
		ExtractorVersion:    "t40.7-test-v1",
		AdapterConfigDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		FactFingerprint:     fmt.Sprintf("fact-%d", index),
	}
	atom.ID = ComputeAtomID(atom)
	path := fmt.Sprintf("src/caller_%d.go", index)
	return []EvidenceAtom{atom}, []SnapshotEvidence{{
			AtomID: atom.ID, Repo: repo, Commit: commit, Path: path,
			StartLine: index + 1, EndLine: index + 1,
			VisibilityScope: "repo:" + repo,
		}}, []Assertion{{
			Predicate: "CALLS_OPERATION", Subject: path,
			Object: fmt.Sprintf("/demo.v1.Service/Call%d", index),
			Tier:   TierExact, Repo: repo, Supporting: []string{atom.ID},
		}}
}

func t407Accounting(t *testing.T, ctx context.Context, s *Surreal, runID string) t407AccountingRec {
	t.Helper()
	results, err := surrealdb.Query[[]t407AccountingRec](ctx, s.db,
		`SELECT staged_revision, staged_fact_count, staged_row_count,
			staged_reference_count, staged_chunk_count FROM $rid`,
		map[string]any{"rid": extractionRunID(runID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		if len(result.Result) == 1 {
			return result.Result[0]
		}
	}
	t.Fatal("accounting row absent")
	return t407AccountingRec{}
}

func t407Run(t *testing.T, ctx context.Context, s *Surreal, repo, commit, domain string) *ExtractionRun {
	t.Helper()
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.invalid/repo.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginExtractionRun(ctx, ExtractionScope{
		Repository: repo, Commit: commit, Domain: domain,
	}, "t40.7-test-v1")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestT407ChunkReplayAccountingAndPublicationDrift(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repo   = "synthetic.invalid/t407-replay"
		commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		chunk  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	)
	run := t407Run(t, ctx, s, repo, commit, "t407-replay")
	atoms, assocs, assertions := t407Batch(repo, commit, 0)
	if err := s.AddEvidenceChunk(ctx, run.ID, chunk, 1, atoms, assocs, assertions); err != nil {
		t.Fatal(err)
	}
	before := t407Accounting(t, ctx, s, run.ID)
	wantOne := t407AccountingRec{
		StagedRevision: 1, StagedFactCount: 1, StagedRowCount: 2,
		StagedReferenceCount: 1, StagedChunkCount: 1,
	}
	if before != wantOne {
		t.Fatalf("charged accounting = %+v", before)
	}
	if err := s.AddEvidenceChunk(ctx, run.ID, chunk, 1, atoms, assocs, assertions); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	after := t407Accounting(t, ctx, s, run.ID)
	if after != before {
		t.Fatalf("replay changed counters: before=%+v after=%+v", before, after)
	}
	conflicting := append([]Assertion(nil), assertions...)
	conflicting[0].Detail = "different payload under the same chunk identity"
	if err := s.AddEvidenceChunk(ctx, run.ID, chunk, 1, atoms, assocs, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay = %v, want ErrConflict", err)
	}
	if got := t407Accounting(t, ctx, s, run.ID); got != before {
		t.Fatalf("conflicting replay changed counters: %+v", got)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE $rid SET staged_row_count += 1 RETURN NONE`,
		map[string]any{"rid": extractionRunID(run.ID)}); err != nil {
		t.Fatal(err)
	}
	coverage := CoverageManifest{
		AssertionCount: 1, AtomCount: 1, CorpusFileCount: 1,
		CandidateFileCount: 1, ReadFileCount: 1, ReadBytes: 1,
		SourceScopeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}
	if err := s.PublishExtractionRun(ctx, run.ID, coverage); !errors.Is(err, ErrConflict) {
		t.Fatalf("publish with drifted accounting = %v, want ErrConflict", err)
	}

	ledgerRun := t407Run(t, ctx, s, repo, commit, "t407-ledger-drift")
	if err := s.AddEvidenceChunk(ctx, ledgerRun.ID, chunk, 1, atoms, assocs, assertions); err != nil {
		t.Fatal(err)
	}
	if _, err := surrealdb.Query[any](ctx, s.db,
		`UPDATE $rid SET row_delta += 1 RETURN NONE`, map[string]any{
			"rid": evidenceChunkRecordID(ledgerRun.ID, chunk),
		}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, ledgerRun.ID, coverage); !errors.Is(err, ErrConflict) {
		t.Fatalf("publish with tampered chunk ledger = %v, want ErrConflict", err)
	}
}

func TestT407ConcurrentChunkAppendsSerializeExactCounters(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repo   = "synthetic.invalid/t407-concurrent"
		commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	run := t407Run(t, ctx, s, repo, commit, "t407-concurrent")
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for index := range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			atoms, assocs, assertions := t407Batch(repo, commit, index)
			chunkID := fmt.Sprintf("sha256:%064x", index+10)
			errs <- s.AddEvidenceChunk(ctx, run.ID, chunkID, 1, atoms, assocs, assertions)
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := t407AccountingRec{
		StagedRevision: 2, StagedFactCount: 2, StagedRowCount: 4,
		StagedReferenceCount: 2, StagedChunkCount: 2,
	}
	if got := t407Accounting(t, ctx, s, run.ID); got != want {
		t.Fatalf("concurrent accounting = %+v", got)
	}
}

func TestT407AbortSweepRemovesChunkLedger(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	const (
		repo   = "synthetic.invalid/t407-sweep"
		commit = "cccccccccccccccccccccccccccccccccccccccc"
		chunk  = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	)
	run := t407Run(t, ctx, s, repo, commit, "t407-sweep")
	atoms, assocs, assertions := t407Batch(repo, commit, 0)
	if err := s.AddEvidenceChunk(ctx, run.ID, chunk, 1, atoms, assocs, assertions); err != nil {
		t.Fatal(err)
	}
	if err := s.AbortExtractionRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	var total EvidenceSweepProgress
	for total.RunsDeleted == 0 {
		step, err := s.SweepEvidence(ctx, time.Now().UTC(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if !step.DidWork() {
			t.Fatal("sweep drained before deleting aborted accounting owner")
		}
		addEvidenceSweepProgress(&total, step)
	}
	if total.ChunkRowsDeleted != 1 || total.AssociationRowsDeleted != 1 ||
		total.AssertionRowsDeleted != 1 || total.RunsDeleted != 1 {
		t.Fatalf("sweep accounting = %+v", total)
	}
	results, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db, `SELECT count() FROM evidence_chunk WHERE run_id = $run GROUP ALL`,
		map[string]any{"run": run.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *results {
		for _, row := range result.Result {
			if row.Count != 0 {
				t.Fatalf("chunk ledger survived sweep: %d", row.Count)
			}
		}
	}
}
