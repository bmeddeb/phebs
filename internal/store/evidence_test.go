package store_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func testAtom(blob, rule, fp string) store.EvidenceAtom {
	return store.EvidenceAtom{
		SchemaVersion: "t12-v1", BlobDigest: blob,
		StartByte: 10, EndByte: 42, RuleID: rule,
		ExtractorVersion: "0.1.0", AdapterConfigDigest: "none",
		FactFingerprint: fp,
	}
}

func testCoverage(assertions, atoms int) store.CoverageManifest {
	coverage := store.CoverageManifest{
		AssertionCount: assertions, AtomCount: atoms,
		SourceScopeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}
	if atoms > 0 {
		coverage.CorpusFileCount = 1
		coverage.CandidateFileCount = 1
		coverage.ReadFileCount = 1
		coverage.ReadBytes = 42
	}
	return coverage
}

func testEvidenceBatch(repo, commit, object string) ([]store.EvidenceAtom, []store.SnapshotEvidence, []store.Assertion) {
	atom := testAtom("sha256:"+object, "rule", "P|"+object)
	atom.ID = store.ComputeAtomID(atom)
	assoc := store.SnapshotEvidence{
		AtomID: atom.ID, Repo: repo, Commit: commit, Path: "api/demo.proto",
		StartLine: 3, EndLine: 3, VisibilityScope: "repo:" + repo,
	}
	assertion := store.Assertion{
		Predicate: "P", Subject: "api/demo.proto", Object: object,
		Tier: store.TierExact, Repo: repo, Supporting: []string{atom.ID},
	}
	return []store.EvidenceAtom{atom}, []store.SnapshotEvidence{assoc}, []store.Assertion{assertion}
}

func seedEvidenceRepo(t *testing.T, s *store.Surreal, repo, commit string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertRepo(ctx, store.Repo{Name: repo, CloneURL: "https://example.com/repo.git"}); err != nil {
		t.Fatalf("seed repo %s: %v", repo, err)
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatalf("index repo %s: %v", repo, err)
	}
}

// AC: identical blob vendored in two repos yields ONE atom and TWO
// associations with independent visibility scopes.
func TestEvidenceAtomDedupAcrossRepos(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	atom := testAtom("sha256:blob1", "proto-rpc-v1", "DECLARES_OPERATION|demo.Svc/Get")
	atom.ID = store.ComputeAtomID(atom)
	if atom.ID != store.ComputeAtomID(atom) {
		t.Fatal("atom id not deterministic")
	}

	publish := func(repo string) {
		seedEvidenceRepo(t, s, repo, "c0ffee")
		run, err := s.BeginExtractionRun(ctx, repo, "c0ffee", "proto-contract", "0.1.0")
		if err != nil {
			t.Fatalf("begin %s: %v", repo, err)
		}
		err = s.AddEvidence(ctx, run.ID,
			[]store.EvidenceAtom{atom},
			[]store.SnapshotEvidence{{
				AtomID: atom.ID, Repo: repo, Commit: "c0ffee",
				Path: "protos/demo.proto", StartLine: 3, EndLine: 3,
				VisibilityScope: "repo:" + repo,
			}},
			[]store.Assertion{{
				Predicate: "DECLARES_OPERATION", Subject: "protos/demo.proto",
				Object: "demo.Svc/Get", Tier: store.TierExact, Repo: repo,
				Supporting: []string{atom.ID},
			}})
		if err != nil {
			t.Fatalf("add %s: %v", repo, err)
		}
		if err := s.PublishExtractionRun(ctx, run.ID, testCoverage(1, 1)); err != nil {
			t.Fatalf("publish %s: %v", repo, err)
		}
	}
	publish("github.com/a/one")
	publish("github.com/b/two")

	// Both repos' assertions visible, each scoped to its own repo.
	for _, repo := range []string{"github.com/a/one", "github.com/b/two"} {
		got, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
		if err != nil {
			t.Fatalf("list %s: %v", repo, err)
		}
		if len(got) != 1 || got[0].Supporting[0] != atom.ID {
			t.Fatalf("%s assertions = %+v", repo, got)
		}
	}

	// One shared atom: sweep neither repo, then supersede repo A's run and
	// sweep — the atom must survive because repo B still references it.
	run2, err := s.BeginExtractionRun(ctx, "github.com/a/one", "c0ffee2", "proto-contract", "0.1.0")
	if err != nil {
		t.Fatalf("begin replacement: %v", err)
	}
	if err := s.SetRepoIndexed(ctx, "github.com/a/one", "c0ffee2", time.Now().UTC()); err != nil {
		t.Fatalf("reindex replacement: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, run2.ID, testCoverage(0, 0)); err != nil {
		t.Fatalf("publish replacement: %v", err)
	}
	if _, err := s.SweepEvidence(ctx, time.Now().UTC(), time.Hour); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	got, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: "github.com/b/two"})
	if err != nil || len(got) != 1 {
		t.Fatalf("repo B lost its assertion after sweeping repo A's superseded run: %v %v", got, err)
	}
	resolved, err := s.ResolveEvidence(ctx, "github.com/b/two", got[0].RunID, atom.ID)
	if err != nil || resolved.Atom.ID != atom.ID || len(resolved.Occurrences) != 1 {
		t.Fatalf("repo B shared atom became dangling after sweep: %+v, %v", resolved, err)
	}
	// Repo A's replacement run published empty — its old assertion is gone.
	got, err = s.ListAssertions(ctx, store.AssertionQuery{Repo: "github.com/a/one"})
	if err != nil || len(got) != 0 {
		t.Fatalf("repo A still shows superseded assertions: %v %v", got, err)
	}
}

// AC: a staged (or killed-mid-run) extraction never publishes a partial
// replacement set — readers keep seeing the previous published facts.
func TestEvidenceStagedRunsInvisibleAndAtomicPublish(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo := "github.com/x/y"
	seedEvidenceRepo(t, s, repo, "aaaa")

	// Nothing published yet: no assertions, no latest run.
	if _, err := s.LatestPublishedRun(ctx, repo, "proto-contract"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("latest on empty = %v", err)
	}

	run1, err := s.BeginExtractionRun(ctx, repo, "aaaa", "proto-contract", "0.1.0")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	a1 := testAtom("sha256:v1", "r", "f1")
	if err := s.AddEvidence(ctx, run1.ID, []store.EvidenceAtom{a1},
		[]store.SnapshotEvidence{{AtomID: store.ComputeAtomID(a1), Repo: repo, Commit: "aaaa", Path: "p", VisibilityScope: "repo:" + repo}},
		[]store.Assertion{{Predicate: "P", Subject: "s", Object: "o1", Tier: store.TierExact, Repo: repo, Supporting: []string{store.ComputeAtomID(a1)}}}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Staged rows are invisible.
	if got, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo}); err != nil || len(got) != 0 {
		t.Fatalf("staged rows visible: %v %v", got, err)
	}
	if err := s.PublishExtractionRun(ctx, run1.ID, testCoverage(1, 1)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got, _ := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo}); len(got) != 1 || got[0].Object != "o1" {
		t.Fatalf("published assertion missing: %v", got)
	}

	// Simulated kill: a second run stages a replacement and never publishes.
	run2, err := s.BeginExtractionRun(ctx, repo, "bbbb", "proto-contract", "0.1.0")
	if err != nil {
		t.Fatalf("begin run2: %v", err)
	}
	a2 := testAtom("sha256:v2", "r", "f2")
	if err := s.AddEvidence(ctx, run2.ID, []store.EvidenceAtom{a2},
		[]store.SnapshotEvidence{{AtomID: store.ComputeAtomID(a2), Repo: repo, Commit: "bbbb", Path: "p", VisibilityScope: "repo:" + repo}},
		[]store.Assertion{{Predicate: "P", Subject: "s", Object: "o2", Tier: store.TierExact, Repo: repo, Supporting: []string{store.ComputeAtomID(a2)}}}); err != nil {
		t.Fatalf("add run2: %v", err)
	}
	// Reader still sees exactly the old published set.
	got, _ := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
	if len(got) != 1 || got[0].Object != "o1" {
		t.Fatalf("partial replacement leaked: %v", got)
	}
	latest, err := s.LatestPublishedRun(ctx, repo, "proto-contract")
	if err != nil || latest.ID != run1.ID {
		t.Fatalf("latest published = %+v, %v", latest, err)
	}

	// The stale staged run is reaped by the sweep; published facts intact.
	n, err := s.SweepEvidence(ctx, time.Now().UTC().Add(2*time.Hour), time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("sweep stale staged = %d, %v", n, err)
	}
	if got, _ := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo}); len(got) != 1 || got[0].Object != "o1" {
		t.Fatalf("sweep damaged published facts: %v", got)
	}

	// Publishing run3 supersedes run1 atomically.
	if err := s.SetRepoIndexed(ctx, repo, "cccc", time.Now().UTC()); err != nil {
		t.Fatalf("reindex run3: %v", err)
	}
	run3, err := s.BeginExtractionRun(ctx, repo, "cccc", "proto-contract", "0.1.0")
	if err != nil {
		t.Fatalf("begin run3: %v", err)
	}
	a3 := testAtom("sha256:v3", "r", "f3")
	if err := s.AddEvidence(ctx, run3.ID, []store.EvidenceAtom{a3},
		[]store.SnapshotEvidence{{AtomID: store.ComputeAtomID(a3), Repo: repo, Commit: "cccc", Path: "p", VisibilityScope: "repo:" + repo}},
		[]store.Assertion{{Predicate: "P", Subject: "s", Object: "o3", Tier: store.TierExact, Repo: repo, Supporting: []string{store.ComputeAtomID(a3)}}}); err != nil {
		t.Fatalf("add run3: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, run3.ID, testCoverage(1, 1)); err != nil {
		t.Fatalf("publish run3: %v", err)
	}
	got, _ = s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
	if len(got) != 1 || got[0].Object != "o3" {
		t.Fatalf("supersession not atomic: %v", got)
	}
	// Double-publish and adding to a published run both refuse.
	if err := s.PublishExtractionRun(ctx, run3.ID, testCoverage(0, 0)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("double publish = %v", err)
	}
	if err := s.AddEvidence(ctx, run3.ID, nil, nil, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("add to published = %v", err)
	}
}

// AC: proof-aware retention — a pinned superseded run survives sweeps with
// all its evidence; unpinning is not offered (pins are durable by design).
func TestEvidencePinBlocksSweep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo := "github.com/p/q"
	seedEvidenceRepo(t, s, repo, "aaaa")

	run1, err := s.BeginExtractionRun(ctx, repo, "aaaa", "proto-contract", "0.1.0")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	a1 := testAtom("sha256:pin", "r", "f")
	if err := s.AddEvidence(ctx, run1.ID, []store.EvidenceAtom{a1},
		[]store.SnapshotEvidence{{AtomID: store.ComputeAtomID(a1), Repo: repo, Commit: "aaaa", Path: "p", VisibilityScope: "repo:" + repo}},
		[]store.Assertion{{Predicate: "P", Subject: "s", Object: "o1", Tier: store.TierExact, Repo: repo, Supporting: []string{store.ComputeAtomID(a1)}}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, run1.ID, testCoverage(1, 1)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := s.PinRun(ctx, run1.ID, "bundle"); err != nil {
		t.Fatalf("pin: %v", err)
	}

	// Supersede run1, then sweep: pinned run and its rows must survive.
	run2, err := s.BeginExtractionRun(ctx, repo, "bbbb", "proto-contract", "0.1.0")
	if err != nil {
		t.Fatalf("begin run2: %v", err)
	}
	if err := s.SetRepoIndexed(ctx, repo, "bbbb", time.Now().UTC()); err != nil {
		t.Fatalf("reindex run2: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, run2.ID, testCoverage(0, 0)); err != nil {
		t.Fatalf("publish run2: %v", err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), time.Hour); err != nil || n != 0 {
		t.Fatalf("sweep removed %d runs despite pin (err %v)", n, err)
	}
	resolved, err := s.ResolveEvidence(ctx, repo, run1.ID, store.ComputeAtomID(a1))
	if err != nil || len(resolved.Occurrences) != 1 {
		t.Fatalf("resolve pinned superseded evidence = %+v, %v", resolved, err)
	}
	// The pinned run row still exists with its evidence (readable via the
	// run record — superseded rows stay out of ListAssertions by design).
	latest, err := s.LatestPublishedRun(ctx, repo, "proto-contract")
	if err != nil || latest.ID != run2.ID {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
}

func TestEvidenceBatchValidationIdempotencyAndResolution(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit := "github.com/secure/repo", "aaaa"
	seedEvidenceRepo(t, s, repo, commit)
	run, err := s.BeginExtractionRun(ctx, repo, commit, "proto-contract", "1")
	if err != nil {
		t.Fatal(err)
	}
	atoms, assocs, assertions := testEvidenceBatch(repo, commit, "demo.Service/Get")

	tests := []struct {
		name   string
		mutate func([]store.EvidenceAtom, []store.SnapshotEvidence, []store.Assertion)
	}{
		{"forged atom id", func(a []store.EvidenceAtom, _ []store.SnapshotEvidence, _ []store.Assertion) { a[0].ID = "ea_forged" }},
		{"cross repo occurrence", func(_ []store.EvidenceAtom, e []store.SnapshotEvidence, _ []store.Assertion) {
			e[0].Repo = "github.com/other/repo"
		}},
		{"cross scope", func(_ []store.EvidenceAtom, e []store.SnapshotEvidence, _ []store.Assertion) {
			e[0].VisibilityScope = "repo:github.com/other/repo"
		}},
		{"traversal path", func(_ []store.EvidenceAtom, e []store.SnapshotEvidence, _ []store.Assertion) {
			e[0].Path = "../secret.proto"
		}},
		{"foreign assertion", func(_ []store.EvidenceAtom, _ []store.SnapshotEvidence, x []store.Assertion) {
			x[0].Repo = "github.com/other/repo"
		}},
		{"numeric confidence", func(_ []store.EvidenceAtom, _ []store.SnapshotEvidence, x []store.Assertion) { x[0].Tier = "0.99" }},
		{"missing support", func(_ []store.EvidenceAtom, _ []store.SnapshotEvidence, x []store.Assertion) {
			x[0].Supporting = []string{"ea_missing"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := append([]store.EvidenceAtom(nil), atoms...)
			e := append([]store.SnapshotEvidence(nil), assocs...)
			x := append([]store.Assertion(nil), assertions...)
			x[0].Supporting = append([]string(nil), assertions[0].Supporting...)
			tt.mutate(a, e, x)
			if err := s.AddEvidence(ctx, run.ID, a, e, x); err == nil {
				t.Fatal("invalid evidence batch accepted")
			}
		})
	}
	if err := s.AddEvidence(ctx, run.ID, atoms, nil, nil); err == nil {
		t.Fatal("atom-only batch accepted")
	}
	edgeHeavy := make([]store.Assertion, 5)
	for i := range edgeHeavy {
		edgeHeavy[i] = assertions[0]
		edgeHeavy[i].Object = fmt.Sprintf("edge-heavy-%d", i)
		edgeHeavy[i].Supporting = make([]string, 4096)
		for j := range edgeHeavy[i].Supporting {
			edgeHeavy[i].Supporting[j] = atoms[0].ID
		}
	}
	if err := s.AddEvidence(ctx, run.ID, atoms, assocs, edgeHeavy); err == nil {
		t.Fatal("unbounded assertion-reference payload accepted")
	}
	duplicate := assertions[0]
	duplicate.Supporting = nil
	duplicate.Contradicting = []string{atoms[0].ID}
	if err := s.AddEvidence(ctx, run.ID, atoms, assocs, []store.Assertion{assertions[0], duplicate}); err == nil {
		t.Fatal("same-batch support/contradiction merge accepted")
	}
	attributeConflict := assertions[0]
	attributeConflict.Tier = store.TierDerived
	if err := s.AddEvidence(ctx, run.ID, atoms, assocs, []store.Assertion{assertions[0], attributeConflict}); err == nil {
		t.Fatal("same semantic assertion with conflicting attributes accepted")
	}

	// Exact retry is idempotent: deterministic physical occurrence/assertion
	// keys leave one stored row and database-derived coverage stays 1/1.
	if err := s.AddEvidence(ctx, run.ID, atoms, assocs, assertions); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.AddEvidence(ctx, run.ID, atoms, assocs, []store.Assertion{duplicate}); err == nil {
		t.Fatal("cross-batch support/contradiction merge accepted")
	}
	if err := s.AddEvidence(ctx, run.ID, atoms, assocs, []store.Assertion{attributeConflict}); err == nil {
		t.Fatal("cross-batch assertion attribute mutation accepted")
	}
	if err := s.AddEvidence(ctx, run.ID, atoms, assocs, assertions); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, testCoverage(0, 0)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("false coverage accepted: %v", err)
	}
	badSourceCoverage := testCoverage(1, 1)
	badSourceCoverage.CandidateFileCount = 2
	if err := s.PublishExtractionRun(ctx, run.ID, badSourceCoverage); err == nil {
		t.Fatal("invalid source coverage accepted")
	}
	failedCoverage := testCoverage(1, 1)
	failedCoverage.Failures = []string{"parse failed"}
	if err := s.PublishExtractionRun(ctx, run.ID, failedCoverage); err == nil {
		t.Fatal("coverage with extraction failures accepted")
	}
	coverage := testCoverage(1, 1)
	if err := s.PublishExtractionRun(ctx, run.ID, coverage); err != nil {
		t.Fatalf("publish exact coverage: %v", err)
	}
	published, err := s.LatestPublishedRun(ctx, repo, "proto-contract")
	if err != nil || published.Coverage.SourceScopeDigest != coverage.SourceScopeDigest ||
		published.Coverage.CandidateFileCount != 1 || published.Coverage.ReadBytes != 42 {
		t.Fatalf("persisted source coverage = %+v, %v", published, err)
	}
	if _, err := s.ListAssertions(ctx, store.AssertionQuery{}); err == nil {
		t.Fatal("unscoped assertion read accepted")
	}
	got, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
	if err != nil || len(got) != 1 {
		t.Fatalf("assertions = %+v, %v", got, err)
	}
	wantAssertionID := store.ComputeAssertionID(assertions[0])
	attributeVariant := assertions[0]
	attributeVariant.Tier = store.TierDerived
	attributeVariant.CodeRole = "generated"
	attributeVariant.Detail = `{"name":"renamed"}`
	if gotID := store.ComputeAssertionID(attributeVariant); gotID != wantAssertionID {
		t.Fatalf("attributes changed semantic id: %s != %s", gotID, wantAssertionID)
	}
	if got[0].ID != wantAssertionID || got[0].RunID != run.ID {
		t.Fatalf("semantic identity/run = %+v, want %s/%s", got[0], wantAssertionID, run.ID)
	}
	resolved, err := s.ResolveEvidence(ctx, repo, run.ID, atoms[0].ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Atom.ID != atoms[0].ID || resolved.Atom.BlobDigest != atoms[0].BlobDigest ||
		resolved.Atom.StartByte != atoms[0].StartByte || resolved.Atom.EndByte != atoms[0].EndByte ||
		len(resolved.Occurrences) != 1 ||
		resolved.Occurrences[0].Repo != repo || resolved.Occurrences[0].Commit != commit ||
		resolved.Occurrences[0].Path != "api/demo.proto" {
		t.Fatalf("resolution = %+v", resolved)
	}
	if _, err := s.ResolveEvidence(ctx, "github.com/other/repo", run.ID, atoms[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-repo resolution = %v", err)
	}

	run2, err := s.BeginExtractionRun(ctx, repo, commit, "identity", "1")
	if err != nil {
		t.Fatal(err)
	}
	atoms2, assocs2, assertions2 := testEvidenceBatch(repo, commit, "b")
	assertions2[0].Predicate = "Q"
	second := assertions2[0]
	second.Object = "a"
	if err := s.AddEvidence(ctx, run2.ID, atoms2, assocs2, []store.Assertion{assertions2[0], second}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, run2.ID, testCoverage(2, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo, Limit: 1}); !errors.Is(err, store.ErrResultLimit) {
		t.Fatalf("truncated assertion query = %v", err)
	}
	ordered, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo, Limit: 3})
	if err != nil || len(ordered) != 3 || ordered[0].Predicate != "P" ||
		ordered[1].Object != "a" || ordered[2].Object != "b" {
		t.Fatalf("stable bounded assertions = %+v, %v", ordered, err)
	}
}

func TestEvidencePublishGuardsRepositoryRevisionAndDeletion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo := "github.com/guarded/repo"
	seedEvidenceRepo(t, s, repo, "aaaa")

	stage := func(commit, object string) *store.ExtractionRun {
		t.Helper()
		run, err := s.BeginExtractionRun(ctx, repo, commit, "proto-contract", "1")
		if err != nil {
			t.Fatal(err)
		}
		a, e, x := testEvidenceBatch(repo, commit, object)
		if err := s.AddEvidence(ctx, run.ID, a, e, x); err != nil {
			t.Fatal(err)
		}
		return run
	}

	stale := stage("aaaa", "stale")
	if err := s.SetRepoIndexed(ctx, repo, "bbbb", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, stale.ID, testCoverage(1, 1)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale commit publish = %v", err)
	}
	if got, _ := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo}); len(got) != 0 {
		t.Fatalf("stale evidence became visible: %+v", got)
	}

	deleting := stage("bbbb", "deleting")
	if err := s.SetRepoDeleting(ctx, repo, true); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, deleting.ID, testCoverage(1, 1)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("deleting repo publish = %v", err)
	}
	if err := s.SetRepoDeleting(ctx, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, deleting.ID, testCoverage(1, 1)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("deleted repo publish = %v", err)
	}
}

func TestEvidenceConcurrentPublishLeavesOneVisibleRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit := "github.com/concurrent/repo", "aaaa"
	seedEvidenceRepo(t, s, repo, commit)

	runs := make([]*store.ExtractionRun, 2)
	for i := range runs {
		var err error
		runs[i], err = s.BeginExtractionRun(ctx, repo, commit, "proto-contract", "1")
		if err != nil {
			t.Fatal(err)
		}
		a, e, x := testEvidenceBatch(repo, commit, fmt.Sprintf("object-%d", i))
		if err := s.AddEvidence(ctx, runs[i].ID, a, e, x); err != nil {
			t.Fatal(err)
		}
	}

	errs := make(chan error, len(runs))
	var wg sync.WaitGroup
	for _, run := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.PublishExtractionRun(ctx, run.ID, testCoverage(1, 1))
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatalf("publish race error = %v", err)
		}
	}
	if successes == 0 {
		t.Fatal("no concurrent publisher succeeded")
	}
	latest, err := s.LatestPublishedRun(ctx, repo, "proto-contract")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo})
	if err != nil || len(got) != 1 || got[0].RunID != latest.ID {
		t.Fatalf("visible assertions/latest = %+v / %+v / %v", got, latest, err)
	}
}

func TestEvidenceDeleteRetiresRunsAndPreservesPins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit := "github.com/delete/repo", "aaaa"
	seedEvidenceRepo(t, s, repo, commit)
	run, err := s.BeginExtractionRun(ctx, repo, commit, "proto-contract", "1")
	if err != nil {
		t.Fatal(err)
	}
	a, e, x := testEvidenceBatch(repo, commit, "kept")
	if err := s.AddEvidence(ctx, run.ID, a, e, x); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, testCoverage(1, 1)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.PinRun(ctx, run.ID, "bundle"); err != nil {
			t.Fatalf("idempotent pin: %v", err)
		}
	}
	if _, err := s.EnqueuePending(ctx, store.JobExtract, repo, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestPublishedRun(ctx, repo, "proto-contract"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted repo latest = %v", err)
	}
	if got, err := s.ListAssertions(ctx, store.AssertionQuery{Repo: repo}); err != nil || len(got) != 0 {
		t.Fatalf("deleted repo assertions = %+v, %v", got, err)
	}
	canceled, err := s.ListJobs(ctx, store.JobExtract, store.StatusCanceled)
	if err != nil || len(canceled) != 1 {
		t.Fatalf("canceled extraction jobs = %+v, %v", canceled, err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 0 {
		t.Fatalf("pinned deleted evidence swept = %d, %v", n, err)
	}
	// Retained row is now superseded and can still be pinned idempotently;
	// it is not query-visible after repository revocation/deletion.
	if err := s.PinRun(ctx, run.ID, "release-checkpoint"); err != nil {
		t.Fatalf("retained run missing: %v", err)
	}
}

func TestEvidenceAbortAndSweepAreBounded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const runs = 3
	for i := range runs {
		run, err := s.BeginExtractionRun(ctx, "github.com/sweep/repo", fmt.Sprintf("%040d", i), "proto-contract", "1")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AbortExtractionRun(ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := s.AbortExtractionRun(ctx, run.ID); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("double abort = %v", err)
			}
		}
	}
	if err := s.AbortExtractionRun(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing abort = %v", err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 1 {
		t.Fatalf("first bounded sweep = %d, %v", n, err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 1 {
		t.Fatalf("second bounded sweep = %d, %v", n, err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 1 {
		t.Fatalf("third bounded sweep = %d, %v", n, err)
	}
	if n, err := s.SweepEvidence(ctx, time.Now().UTC(), 0); err != nil || n != 0 {
		t.Fatalf("empty bounded sweep = %d, %v", n, err)
	}
}

func TestExtractionPendingConcurrentUnique(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.EnqueuePending(ctx, store.JobExtract, "github.com/jobs/repo", false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, err := s.ListJobs(ctx, store.JobExtract, store.StatusPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending extraction jobs = %+v, %v", pending, err)
	}
}

func TestEvidenceSchemaReopenPreservesCurrentPublication(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	repo, commit := "github.com/reopen/repo", "aaaa"
	seedEvidenceRepo(t, s, repo, commit)
	run, err := s.BeginExtractionRun(ctx, repo, commit, "proto-contract", "1")
	if err != nil {
		t.Fatal(err)
	}
	a, e, x := testEvidenceBatch(repo, commit, "persisted")
	if err := s.AddEvidence(ctx, run.ID, a, e, x); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, testCoverage(1, 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatalf("idempotent reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	latest, err := reopened.LatestPublishedRun(ctx, repo, "proto-contract")
	if err != nil || latest.ID != run.ID {
		t.Fatalf("publication changed across reopen: %+v, %v", latest, err)
	}
	resolved, err := reopened.ResolveEvidence(ctx, repo, run.ID, a[0].ID)
	if err != nil || resolved.Atom.BlobDigest != a[0].BlobDigest || len(resolved.Occurrences) != 1 {
		t.Fatalf("evidence changed across reopen: %+v, %v", resolved, err)
	}
}

func TestEvidenceOccurrenceResolutionBoundary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	repo, commit := "github.com/occurrences/repo", "aaaa"
	seedEvidenceRepo(t, s, repo, commit)

	stage := func(domain string, count int) (*store.ExtractionRun, store.EvidenceAtom) {
		t.Helper()
		run, err := s.BeginExtractionRun(ctx, repo, commit, domain, "1")
		if err != nil {
			t.Fatal(err)
		}
		atoms, _, assertions := testEvidenceBatch(repo, commit, domain)
		assocs := make([]store.SnapshotEvidence, count)
		for i := range count {
			assocs[i] = store.SnapshotEvidence{
				AtomID: atoms[0].ID, Repo: repo, Commit: commit,
				Path: fmt.Sprintf("copies/%03d/demo.proto", i), StartLine: 1, EndLine: 1,
				VisibilityScope: "repo:" + repo,
			}
		}
		if err := s.AddEvidence(ctx, run.ID, atoms, assocs, assertions); err != nil {
			t.Fatal(err)
		}
		return run, atoms[0]
	}

	atLimit, atom := stage("cap-100", 100)
	if err := s.PublishExtractionRun(ctx, atLimit.ID, testCoverage(1, 1)); err != nil {
		t.Fatalf("publish at resolution cap: %v", err)
	}
	resolved, err := s.ResolveEvidence(ctx, repo, atLimit.ID, atom.ID)
	if err != nil || len(resolved.Occurrences) != 100 {
		t.Fatalf("resolve at cap = %d, %v", len(resolved.Occurrences), err)
	}

	overLimit, overAtom := stage("cap-101", 101)
	if err := s.PublishExtractionRun(ctx, overLimit.ID, testCoverage(1, 1)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("publish above resolution cap = %v", err)
	}
	if _, err := s.ResolveEvidence(ctx, repo, overLimit.ID, overAtom.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("staged over-cap run resolved = %v", err)
	}
}
