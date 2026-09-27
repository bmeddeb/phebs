package store

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/typedindex"
	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestTypedSource(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	commitA, commitB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	query := func(statement string, vars map[string]any) {
		t.Helper()
		if _, err := surrealdb.Query[any](ctx, s.db, statement, vars); err != nil {
			t.Fatal(err)
		}
	}
	source := func(repo string) typedindex.Source {
		t.Helper()
		got, err := s.GetTypedSource(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	indexed := func(repo, commit string, unit *analysisunit.State) {
		t.Helper()
		if err := s.SetRepoIndexedState(ctx, repo, commit, nil, unit, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	create := func(repo string) {
		t.Helper()
		if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://" + repo, TypedIncarnation: strings.Repeat("f", 32), TypedSourceEpoch: 123}); err != nil {
			t.Fatal(err)
		}
		indexed(repo, commitA, nil)
	}
	t.Run("head-scope-clear-ABA-and-stable-noops", func(t *testing.T) {
		repo := "example.invalid/typed-source"
		create(repo)
		first := source(repo)
		if first.Incarnation == strings.Repeat("f", 32) {
			t.Fatal("caller supplied incarnation accepted")
		}
		indexed(repo, commitA, nil)
		if got := source(repo); got != first {
			t.Fatal("identical indexing changed source")
		}
		if err := s.SetRepoIndexedRevisions(ctx, repo, commitA, []IndexedRevision{{Selector: "release", Branch: "release", Commit: commitB}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := source(repo); got != first {
			t.Fatal("named revision changed HEAD source")
		}
		query("UPDATE $rid SET evidence_revision = (evidence_revision ?? 0) + 1, caller_publication_revision = 99", map[string]any{"rid": repoID(repo)})
		if got := source(repo); got != first {
			t.Fatal("evidence changed typed source")
		}
		if err := s.UpsertRepo(ctx, Repo{Name: repo, TypedIncarnation: strings.Repeat("e", 32), TypedSourceEpoch: 98}); err != nil {
			t.Fatal(err)
		}
		if got := source(repo); got != first {
			t.Fatal("metadata upsert changed source")
		}
		indexed(repo, commitB, nil)
		second := source(repo)
		indexed(repo, commitA, nil)
		third := source(repo)
		if first.Generation == second.Generation || first.Generation == third.Generation || first.Incarnation != third.Incarnation {
			t.Fatal("HEAD ABA not fenced")
		}
		unit, err := (analysisunit.Scope{Repository: repo, Name: "core", Primary: []string{"src"}}).State()
		if err != nil {
			t.Fatal(err)
		}
		indexed(repo, commitA, unit)
		scoped := source(repo)
		indexed(repo, commitA, nil)
		unscoped := source(repo)
		if scoped.Generation == third.Generation || unscoped.Generation == third.Generation {
			t.Fatal("scope ABA not fenced")
		}
		if err := s.ClearRepoIndexState(ctx, repo); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetTypedSource(ctx, repo); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cleared source: %v", err)
		}
		before, err := s.GetRepo(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ClearRepoIndexState(ctx, repo); err != nil {
			t.Fatal(err)
		}
		after, err := s.GetRepo(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if before.TypedSourceEpoch != after.TypedSourceEpoch {
			t.Fatal("empty clear advanced epoch")
		}
		indexed(repo, commitA, nil)
		if source(repo).Generation == unscoped.Generation {
			t.Fatal("clear/reinstall ABA not fenced")
		}
		raw, err := json.Marshal(after)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "typed_source") || strings.Contains(string(raw), "typed_incarnation") || strings.Contains(string(raw), first.Incarnation) {
			t.Fatal("internal source leaked in API JSON")
		}
	})
	t.Run("missing-deleting-and-readdition", func(t *testing.T) {
		repo := "example.invalid/typed-readd"
		if _, err := s.GetTypedSource(ctx, repo); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		if _, err := s.GetRepo(ctx, repo); !errors.Is(err, ErrNotFound) {
			t.Fatalf("read created missing repo: %v", err)
		}
		create(repo)
		first := source(repo)
		if err := s.SetRepoDeleting(ctx, repo, true); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetTypedSource(ctx, repo); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleting: %v", err)
		}
		if err := s.DeleteRepo(ctx, repo); err != nil {
			t.Fatal(err)
		}
		create(repo)
		next := source(repo)
		if next.Incarnation == first.Incarnation || next.Generation == first.Generation {
			t.Fatal("remove/readd reused authority")
		}
	})
	t.Run("legacy-concurrent-initialization", func(t *testing.T) {
		repo := "example.invalid/typed-legacy"
		create(repo)
		query("UPDATE $rid UNSET typed_incarnation, typed_source_epoch", map[string]any{"rid": repoID(repo)})
		const n = 8
		var wg sync.WaitGroup
		results := make(chan typedindex.Source, n)
		errs := make(chan error, n)
		for range n {
			wg.Go(func() { v, err := s.GetTypedSource(ctx, repo); results <- v; errs <- err })
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		want := source(repo)
		for got := range results {
			if got != want {
				t.Fatal("legacy race returned distinct identities")
			}
		}
		query("UPDATE $rid SET deleting = true, typed_incarnation = NONE, typed_source_epoch = NONE", map[string]any{"rid": repoID(repo)})
		if _, err := s.GetTypedSource(ctx, repo); !errors.Is(err, ErrNotFound) {
			t.Fatalf("legacy deleting: %v", err)
		}
		got, err := s.GetRepo(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if got.TypedIncarnation != "" || got.TypedSourceEpoch != 0 {
			t.Fatal("initialized deleting legacy repo")
		}
	})
	t.Run("malformed-present-identity-is-not-legacy", func(t *testing.T) {
		repo := "example.invalid/typed-malformed"
		create(repo)
		query("UPDATE $rid SET typed_incarnation = ''", map[string]any{"rid": repoID(repo)})
		if _, err := s.GetTypedSource(ctx, repo); !errors.Is(err, typedindex.Invalid) {
			t.Fatalf("empty incarnation: %v", err)
		}
		// Simulate a malformed predecessor schema; present zero is not absence.
		query("DEFINE FIELD OVERWRITE typed_source_epoch ON repo TYPE option<int>; UPDATE $rid SET typed_incarnation = $incarnation, typed_source_epoch = 0", map[string]any{"rid": repoID(repo), "incarnation": strings.Repeat("c", 32)})
		if _, err := s.GetTypedSource(ctx, repo); !errors.Is(err, typedindex.Invalid) {
			t.Fatalf("zero epoch: %v", err)
		}
		query("DELETE $rid; DEFINE FIELD OVERWRITE typed_source_epoch ON repo TYPE option<int> ASSERT $value = NONE OR $value > 0", map[string]any{"rid": repoID(repo)})
	})
	t.Run("overflow-is-atomic-and-noop-works", func(t *testing.T) {
		repo := "example.invalid/typed-overflow"
		create(repo)
		query("UPDATE $rid SET typed_source_epoch = $epoch", map[string]any{"rid": repoID(repo), "epoch": int64(math.MaxInt64)})
		before := source(repo)
		indexed(repo, commitA, nil)
		if source(repo) != before {
			t.Fatal("overflow no-op changed identity")
		}
		if err := s.SetRepoIndexed(ctx, repo, commitB, time.Now()); err == nil {
			t.Fatal("source overflow accepted")
		}
		if source(repo) != before {
			t.Fatal("failed overflow mutated source")
		}
		if err := s.ClearRepoIndexState(ctx, repo); err == nil {
			t.Fatal("clear overflow accepted")
		}
		if source(repo) != before {
			t.Fatal("failed clear mutated source")
		}
	})
}
