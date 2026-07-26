package extract_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newTestStore(t *testing.T) *store.Surreal {
	t.Helper()
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	s, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

type fakeCorpus struct{ repo, commit string }

func (f fakeCorpus) RepoName() string { return f.repo }
func (f fakeCorpus) Commit() string   { return f.commit }
func (f fakeCorpus) WalkFiles(_ context.Context, visit func(string) error) error {
	return visit("a.proto")
}
func (f fakeCorpus) Read(context.Context, string) (sdk.Blob, error) {
	sum := sha256.Sum256([]byte("x"))
	return sdk.Blob{Content: "x", Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

type fakeCorpusFactory struct{}

func (fakeCorpusFactory) Lock(context.Context, string) (func(), error) {
	return func() {}, nil
}

func (fakeCorpusFactory) New(repo, commit string) sdk.Corpus {
	return fakeCorpus{repo, commit}
}

type fakeExtractor struct {
	domain  string
	version string
	calls   int
	fail    bool
	// emits one assertion derived from the corpus commit so runs differ per commit
}

func (f *fakeExtractor) Domain() string                 { return f.domain }
func (f *fakeExtractor) Version() string                { return f.version }
func (f *fakeExtractor) Candidate(filePath string) bool { return filePath == "a.proto" }
func (f *fakeExtractor) Extract(ctx context.Context, c extract.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	f.calls++
	if f.fail {
		return sdk.Coverage{}, errors.New("boom")
	}
	if _, err := c.Read(ctx, "a.proto"); err != nil {
		return sdk.Coverage{}, err
	}
	sum := sha256.Sum256([]byte("x"))
	err := emit(sdk.Fact{
		Atom: sdk.AtomInput{
			SchemaVersion: "t12-v2", BlobDigest: "sha256:" + hex.EncodeToString(sum[:]),
			StartByte: 0, EndByte: 1, RuleID: "fake",
			AdapterConfigDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			FactFingerprint:     "P|" + c.Commit(),
		},
		Path: "a.proto", StartLine: 1, EndLine: 1,
		Assertion: sdk.AssertionInput{
			Predicate: "P", Subject: "a.proto", Object: "o@" + c.Commit(), Tier: store.TierExact,
		},
	})
	return sdk.Coverage{}, err
}

func fakeFactory() extract.CorpusFactory {
	return fakeCorpusFactory{}
}

func seedIndexedRepo(t *testing.T, s *store.Surreal, name, commit string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertRepo(ctx, store.Repo{Name: name, CloneURL: "https://example.com/x.git"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetRepoIndexed(ctx, name, commit, time.Now().UTC()); err != nil {
		t.Fatalf("set indexed: %v", err)
	}
}

// AC: one extraction per new commit — a re-run against the same indexed
// commit short-circuits; a new commit extracts again; force re-extracts.
func TestWorkerShortCircuitPerCommit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo := "github.com/w/x"
	seedIndexedRepo(t, s, repo, commitA)

	ex := &fakeExtractor{domain: "proto-contract", version: "1"}
	w := &extract.Worker{
		Repos: s, Evidence: s,
		NewCorpus:  fakeFactory(),
		Extractors: []extract.Extractor{ex},
	}
	job := store.Job{Target: repo}

	if err := w.Handle(ctx, job); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := w.Handle(ctx, job); err != nil {
		t.Fatalf("second: %v", err)
	}
	if ex.calls != 1 {
		t.Fatalf("extractor ran %d times for one commit", ex.calls)
	}
	got, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
	if err != nil || len(got) != 1 || got[0].Object != "o@"+commitA {
		t.Fatalf("assertions after short-circuit: %v %v", got, err)
	}

	// New indexed commit → exactly one more extraction, superseding the old.
	if err := s.SetRepoIndexed(ctx, repo, commitB, time.Now().UTC()); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if err := w.Handle(ctx, job); err != nil {
		t.Fatalf("third: %v", err)
	}
	if ex.calls != 2 {
		t.Fatalf("extractor ran %d times across two commits", ex.calls)
	}
	got, _ = s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
	if len(got) != 1 || got[0].Object != "o@"+commitB {
		t.Fatalf("supersession after reindex: %v", got)
	}

	// Force bypasses the short-circuit.
	if err := w.Handle(ctx, store.Job{Target: repo, Force: true}); err != nil {
		t.Fatalf("force: %v", err)
	}
	if ex.calls != 3 {
		t.Fatalf("force did not re-extract (calls=%d)", ex.calls)
	}
}

// AC: a failing extractor aborts its run (classified `extract`) — published
// facts stay intact, and nothing staged becomes visible.
func TestWorkerFailureAbortsClassified(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo := "github.com/w/y"
	seedIndexedRepo(t, s, repo, commitA)

	good := &fakeExtractor{domain: "proto-contract", version: "1"}
	w := &extract.Worker{
		Repos: s, Evidence: s,
		NewCorpus:  fakeFactory(),
		Extractors: []extract.Extractor{good},
	}
	if err := w.Handle(ctx, store.Job{Target: repo}); err != nil {
		t.Fatalf("seed publish: %v", err)
	}
	visible := []store.Repo{{Name: repo, IndexedCommitHash: commitA}}
	baseline, err := extract.BuildCoverageCertificate(ctx, s, visible, []string{"proto-contract"})
	if err != nil {
		t.Fatalf("baseline certificate: %v", err)
	}

	bad := &fakeExtractor{domain: "proto-contract", version: "2", fail: true}
	w.Extractors = []extract.Extractor{bad}
	err = w.Handle(ctx, store.Job{Target: repo, Force: true})
	if err == nil {
		t.Fatal("failing extractor reported success")
	}
	if store.Classify(err) != store.ClassExtract {
		t.Fatalf("class = %s, want extract", store.Classify(err))
	}
	// Old published facts intact; failed run left nothing visible.
	got, _ := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
	if len(got) != 1 || got[0].Object != "o@"+commitA {
		t.Fatalf("failure disturbed published facts: %v", got)
	}
	latest, err := s.LatestPublishedRun(ctx, repo, "proto-contract")
	if err != nil || latest.Extractor != "1" {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	failed, err := extract.BuildCoverageCertificate(ctx, s, visible, []string{"proto-contract"})
	if err != nil {
		t.Fatalf("failed certificate: %v", err)
	}
	certRun := failed.Repositories[0].Runs[0]
	if failed.Digest == baseline.Digest || certRun.RunID != latest.ID || !certRun.Fresh ||
		certRun.LatestAttempt == nil || certRun.LatestAttempt.Status != "aborted" ||
		certRun.LatestAttempt.Extractor != "2" {
		t.Fatalf("failed replacement certificate = %+v", certRun)
	}
	// The aborted run is sweepable immediately.
	swept := 0
	for swept == 0 {
		progress, err := s.SweepEvidence(ctx, time.Now().UTC(), time.Hour)
		if err != nil {
			t.Fatalf("sweep aborted: %v", err)
		}
		if !progress.DidWork() {
			t.Fatal("sweep aborted run made no progress")
		}
		swept += progress.RunsDeleted
	}
	afterSweep, err := extract.BuildCoverageCertificate(ctx, s, visible, []string{"proto-contract"})
	if err != nil || afterSweep.Digest != failed.Digest {
		t.Fatalf("sweep changed durable failure certificate = %+v, %v", afterSweep, err)
	}
}

// Unindexed and deleting repos are quietly skipped: extraction only ever
// reads the committed indexed revision (fail-closed, indexed-revision
// contract).
func TestWorkerSkipsUnready(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo := "github.com/w/z"
	if err := s.UpsertRepo(ctx, store.Repo{Name: repo, CloneURL: "https://example.com/z.git"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	ex := &fakeExtractor{domain: "proto-contract", version: "1"}
	w := &extract.Worker{
		Repos: s, Evidence: s,
		NewCorpus:  fakeFactory(),
		Extractors: []extract.Extractor{ex},
	}
	if err := w.Handle(ctx, store.Job{Target: repo}); err != nil {
		t.Fatalf("unindexed: %v", err)
	}
	if err := w.Handle(ctx, store.Job{Target: "github.com/does/not-exist"}); err != nil {
		t.Fatalf("missing repo: %v", err)
	}
	if ex.calls != 0 {
		t.Fatalf("extractor ran on unready repo")
	}
}
