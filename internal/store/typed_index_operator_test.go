package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexOperatorReadonlyAndExpectedRevision(t *testing.T) {
	s := newRunnerStore(t)
	ctx := t.Context()
	f := newTypedFixture(t, s, "operator")
	before, err := s.ReadTypedIndexOperator(ctx, f.repo)
	if err != nil || before.Revision == "" || before.Status.Desired != "" {
		t.Fatal(before, err)
	}
	request := typedindex.NewManagedRequest(before.Source, before.Profile, before.ProfileEpoch, before.UniverseDigest, typedindex.Canary)
	raw := typedTestJSON(t, request)
	if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, "sha256:"+strings.Repeat("b", 64), raw); !errors.Is(err, typedindex.Stale) {
		t.Fatal("stale preview accepted", err)
	}
	for range 2 {
		if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, raw); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.ReadTypedIndexOperator(ctx, f.repo)
	if err != nil || after.Revision != before.Revision || after.Coordinator != StatusPending {
		t.Fatal(after, err)
	}
	chunk := f.claim(t)
	if _, err = s.BeginTypedIndex(ctx, chunk); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRepoIndexed(ctx, f.repo, strings.Repeat("b", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueTypedIndexExpected(ctx, f.repo, before.Revision, raw); !errors.Is(err, typedindex.Stale) {
		t.Fatal("old HEAD accepted", err)
	}
	stale, err := s.ReadTypedIndexOperator(ctx, f.repo)
	if err != nil || !stale.Status.Stale || stale.Status.Stage != "" {
		t.Fatal("old active stage crossed HEAD", stale, err)
	}
	repo := "example.invalid/uninitialized-operator"
	if err = s.UpsertRepo(ctx, Repo{Name: repo}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRepoIndexed(ctx, repo, strings.Repeat("a", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	// Remove optional identity to reproduce legacy/restored state. Read must not mint it.
	_, err = storeQuery[any](ctx, s.accounting, s.db, "UPDATE $repo SET typed_incarnation=NONE, typed_source_epoch=NONE RETURN NONE", map[string]any{"repo": repoID(repo)}, storeWrite(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadTypedIndexOperator(ctx, repo); !errors.Is(err, typedindex.Unprepared) {
		t.Fatal("read initialized source", err)
	}
	row, err := s.GetRepo(ctx, repo)
	if err != nil || row.TypedIncarnation != "" || row.TypedSourceEpoch != 0 {
		t.Fatal("read mutated source", row, err)
	}
}
