package callerexecute

import (
	"context"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
)

type backfillStore struct {
	repositories    []store.Repo
	listErr         error
	publications    map[string]*store.ResolverCatalogPublication
	publicationErrs map[string]error
	current         map[string]bool
	currentErrs     map[string]error
	enqueueErr      error
	pending         map[string]store.Job
	created         int
}

func (state *backfillStore) ListRepos(context.Context) ([]store.Repo, error) {
	return state.repositories, state.listErr
}

func (state *backfillStore) GetResolverCatalogPublication(
	_ context.Context,
	repository string,
) (*store.ResolverCatalogPublication, error) {
	if err := state.publicationErrs[repository]; err != nil {
		return nil, err
	}
	publication, ok := state.publications[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	return publication, nil
}

func (state *backfillStore) ResolverCatalogPublicationCurrent(
	_ context.Context,
	publication store.ResolverCatalogPublication,
) (bool, error) {
	if err := state.currentErrs[publication.Repository]; err != nil {
		return false, err
	}
	return state.current[publication.Repository], nil
}

func (state *backfillStore) EnqueuePending(
	_ context.Context,
	kind store.JobKind,
	target string,
	force bool,
) (*store.Job, error) {
	if state.enqueueErr != nil {
		return nil, state.enqueueErr
	}
	if kind != store.JobCallerLeaf || force {
		return nil, errors.New("unexpected caller backfill request")
	}
	if state.pending == nil {
		state.pending = make(map[string]store.Job)
	}
	if job, ok := state.pending[target]; ok {
		return &job, nil
	}
	job := store.Job{
		Kind: kind, Target: target, Status: store.StatusPending,
	}
	state.pending[target] = job
	state.created++
	return &job, nil
}

func TestEnqueueBackfillQueuesOnlyCurrentCatalogsAndDeduplicates(
	t *testing.T,
) {
	const (
		liveOne = "example.com/live/one"
		liveTwo = "example.com/live/two"
		retired = "example.com/retired-catalog"
	)
	state := &backfillStore{
		repositories: []store.Repo{
			{Name: liveOne, IndexedCommitHash: "a"},
			{Name: "example.com/unindexed"},
			{Name: "example.com/deleting", IndexedCommitHash: "b", Deleting: true},
			{Name: "example.com/missing-catalog", IndexedCommitHash: "c"},
			{Name: retired, IndexedCommitHash: "d"},
			{Name: liveTwo, IndexedCommitHash: "e"},
		},
		publications: map[string]*store.ResolverCatalogPublication{
			liveOne: {Repository: liveOne},
			retired: {Repository: retired},
			liveTwo: {Repository: liveTwo},
		},
		current: map[string]bool{liveOne: true, liveTwo: true},
	}
	if err := EnqueueBackfill(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueBackfill(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if state.created != 2 || len(state.pending) != 2 {
		t.Fatalf("created=%d pending=%v, want two", state.created, state.pending)
	}
	for _, repository := range []string{liveOne, liveTwo} {
		if _, ok := state.pending[repository]; !ok {
			t.Errorf("missing caller backfill for %s", repository)
		}
	}
}

func TestEnqueueBackfillPropagatesFailures(t *testing.T) {
	const repository = "example.com/live"
	listFailure := errors.New("list failed")
	pointerFailure := store.ErrInvalidResolverCatalogPublication
	currentFailure := errors.New("current failed")
	enqueueFailure := errors.New("enqueue failed")
	publication := func() map[string]*store.ResolverCatalogPublication {
		return map[string]*store.ResolverCatalogPublication{
			repository: {Repository: repository},
		}
	}
	tests := []struct {
		name  string
		state *backfillStore
		want  error
	}{
		{
			name: "list", state: &backfillStore{listErr: listFailure},
			want: listFailure,
		},
		{
			name: "pointer",
			state: &backfillStore{
				repositories: []store.Repo{{
					Name: repository, IndexedCommitHash: "a",
				}},
				publicationErrs: map[string]error{repository: pointerFailure},
			},
			want: pointerFailure,
		},
		{
			name: "current",
			state: &backfillStore{
				repositories: []store.Repo{{
					Name: repository, IndexedCommitHash: "a",
				}},
				publications: publication(),
				currentErrs:  map[string]error{repository: currentFailure},
			},
			want: currentFailure,
		},
		{
			name: "enqueue",
			state: &backfillStore{
				repositories: []store.Repo{{
					Name: repository, IndexedCommitHash: "a",
				}},
				publications: publication(),
				current:      map[string]bool{repository: true},
				enqueueErr:   enqueueFailure,
			},
			want: enqueueFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := EnqueueBackfill(t.Context(), test.state); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want wrapped %q", err, test.want)
			}
		})
	}
}

func TestEnqueueBackfillRejectsNilPublication(t *testing.T) {
	const repository = "example.com/live"
	err := EnqueueBackfill(t.Context(), &backfillStore{
		repositories: []store.Repo{{
			Name: repository, IndexedCommitHash: "a",
		}},
		publications: map[string]*store.ResolverCatalogPublication{repository: nil},
	})
	if err == nil {
		t.Fatal("nil resolver publication was accepted")
	}
}

func TestEnqueueBackfillHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := EnqueueBackfill(ctx, &backfillStore{repositories: []store.Repo{{
		Name: "example.com/live", IndexedCommitHash: "a",
	}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
