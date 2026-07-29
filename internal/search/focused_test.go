package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
)

type cacheTestStreamer struct {
	closes atomic.Int32
}

type resultTestStreamer struct {
	result      *zoekt.SearchResult
	wait        time.Duration
	gate        <-chan struct{}
	maxWallTime chan time.Duration
	started     chan struct{}
	canceled    chan struct{}
}

func (s *resultTestStreamer) Search(
	ctx context.Context,
	_ query.Q,
	options *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	if s.maxWallTime != nil {
		select {
		case s.maxWallTime <- options.MaxWallTime:
		default:
		}
	}
	if s.wait > 0 {
		timer := time.NewTimer(s.wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.result == nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return &zoekt.SearchResult{}, nil
		}
	}
	copy := *s.result
	copy.Files = append([]zoekt.FileMatch(nil), s.result.Files...)
	return &copy, nil
}

func (s *resultTestStreamer) StreamSearch(
	ctx context.Context,
	q query.Q,
	options *zoekt.SearchOptions,
	sender zoekt.Sender,
) error {
	if s.started != nil {
		close(s.started)
	}
	result, err := s.Search(ctx, q, options)
	if err != nil {
		return err
	}
	sender.Send(result)
	if s.canceled != nil {
		<-ctx.Done()
		close(s.canceled)
		return ctx.Err()
	}
	return nil
}

func (*resultTestStreamer) List(
	context.Context, query.Q, *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{}, nil
}

func (*resultTestStreamer) Close() {}

func (*resultTestStreamer) String() string { return "result-test" }

type ownershipTestStreamer struct {
	sent chan struct{}
}

func (*ownershipTestStreamer) Search(
	context.Context, query.Q, *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	return &zoekt.SearchResult{}, nil
}

func (s *ownershipTestStreamer) StreamSearch(
	_ context.Context,
	_ query.Q,
	_ *zoekt.SearchOptions,
	sender zoekt.Sender,
) error {
	result := &zoekt.SearchResult{Files: []zoekt.FileMatch{{
		Repository: "example.com/acme/ownership",
		FileName:   "original.go",
	}}}
	sender.Send(result)
	result.Files[0].FileName = "mutated-after-send.go"
	close(s.sent)
	return nil
}

func (*ownershipTestStreamer) List(
	context.Context, query.Q, *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{}, nil
}

func (*ownershipTestStreamer) Close() {}

func (*ownershipTestStreamer) String() string { return "ownership-test" }

type cancelErrorTestStreamer struct {
	started chan struct{}
	err     error
}

func (*cancelErrorTestStreamer) Search(
	context.Context, query.Q, *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	return &zoekt.SearchResult{}, nil
}

func (s *cancelErrorTestStreamer) StreamSearch(
	ctx context.Context,
	_ query.Q,
	_ *zoekt.SearchOptions,
	_ zoekt.Sender,
) error {
	close(s.started)
	<-ctx.Done()
	return s.err
}

func (*cancelErrorTestStreamer) List(
	context.Context, query.Q, *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{}, nil
}

func (*cancelErrorTestStreamer) Close() {}

func (*cancelErrorTestStreamer) String() string { return "cancel-error-test" }

type blockingFocusedStreamer struct {
	started    chan struct{}
	release    chan struct{}
	startOnce  sync.Once
	repository string
	version    string
}

func (s *blockingFocusedStreamer) wait(ctx context.Context) error {
	s.startOnce.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *blockingFocusedStreamer) result() *zoekt.SearchResult {
	return &zoekt.SearchResult{Files: []zoekt.FileMatch{{
		Repository: s.repository,
		FileName:   "secret/old.go",
		Version:    s.version,
		ChunkMatches: []zoekt.ChunkMatch{{
			Content: []byte("const RetiredDuringSearch = true\n"),
			Ranges: []zoekt.Range{{
				Start: zoekt.Location{LineNumber: 1, Column: 7},
				End:   zoekt.Location{LineNumber: 1, Column: 26},
			}},
			ContentStart: zoekt.Location{LineNumber: 1, Column: 1},
		}},
	}}}
}

func (s *blockingFocusedStreamer) Search(
	ctx context.Context, _ query.Q, _ *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	return s.result(), nil
}

func (s *blockingFocusedStreamer) StreamSearch(
	ctx context.Context,
	_ query.Q,
	_ *zoekt.SearchOptions,
	sender zoekt.Sender,
) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	sender.Send(s.result())
	return nil
}

func (*blockingFocusedStreamer) List(
	context.Context, query.Q, *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{}, nil
}

func (*blockingFocusedStreamer) Close() {}

func (*blockingFocusedStreamer) String() string { return "blocking-focused" }

type mutableFocusedStore struct {
	store.Store
	mu   sync.Mutex
	repo store.Repo
}

type delayedRepoStore struct {
	store.Store
	delay time.Duration
	repo  store.Repo
}

func (s *delayedRepoStore) ListRepos(ctx context.Context) ([]store.Repo, error) {
	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return []store.Repo{cloneFocusedStoreRepo(s.repo)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *delayedRepoStore) GetRepo(
	_ context.Context,
	repository string,
) (*store.Repo, error) {
	if repository != s.repo.Name {
		return nil, store.ErrNotFound
	}
	repo := cloneFocusedStoreRepo(s.repo)
	return &repo, nil
}

func (s *mutableFocusedStore) ListRepos(context.Context) ([]store.Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []store.Repo{cloneFocusedStoreRepo(s.repo)}, nil
}

func (s *mutableFocusedStore) GetRepo(
	_ context.Context, repository string,
) (*store.Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if repository != s.repo.Name {
		return nil, store.ErrNotFound
	}
	repo := cloneFocusedStoreRepo(s.repo)
	return &repo, nil
}

func (s *mutableFocusedStore) setState(state *analysisunit.State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repo.IndexedAnalysisUnit = analysisunit.CloneState(state)
}

func cloneFocusedStoreRepo(repo store.Repo) store.Repo {
	repo.IndexedAnalysisUnit = analysisunit.CloneState(repo.IndexedAnalysisUnit)
	repo.IndexedRevisions = append(
		[]store.IndexedRevision(nil), repo.IndexedRevisions...,
	)
	return repo
}

func (*cacheTestStreamer) Search(
	context.Context, query.Q, *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	return &zoekt.SearchResult{}, nil
}

func (*cacheTestStreamer) StreamSearch(
	context.Context, query.Q, *zoekt.SearchOptions, zoekt.Sender,
) error {
	return nil
}

func (*cacheTestStreamer) List(
	context.Context, query.Q, *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{}, nil
}

func (s *cacheTestStreamer) Close() {
	s.closes.Add(1)
}

func (*cacheTestStreamer) String() string {
	return "cache-test"
}

func TestFocusedCacheHitSkipsValidationAndDefersEvictionClose(t *testing.T) {
	indexDir := t.TempDir()
	repository := "example.com/acme/cache"
	artifact := filepath.Join(indexDir, focusedindex.ManifestName(repository))
	if err := os.WriteFile(artifact, []byte("identity-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := (analysisunit.Scope{
		Repository: repository, Name: "service", Primary: []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: "1111111111111111111111111111111111111111",
	}}
	cache := newFocusedCache(indexDir)
	var validations atomic.Int32
	cache.validate = func(
		_ context.Context,
		_ string,
		gotRepository string,
		gotState *analysisunit.State,
		gotRevisions []store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		validations.Add(1)
		if gotRepository != repository ||
			!analysisunit.EqualState(gotState, state) ||
			len(gotRevisions) != 1 {
			t.Fatalf(
				"validation inputs = %q %+v %+v",
				gotRepository, gotState, gotRevisions,
			)
		}
		return focusedindex.Manifest{
			Repository: repository,
			Digest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}, nil
	}
	var materializations atomic.Int32
	var loaded []*cacheTestStreamer
	cache.materialize = func(
		_ context.Context,
		_ string, _ focusedindex.Manifest,
	) (zoekt.Streamer, func(), error) {
		materializations.Add(1)
		streamer := &cacheTestStreamer{}
		loaded = append(loaded, streamer)
		return streamer, func() {}, nil
	}
	t.Cleanup(cache.close)

	first, err := cache.acquire(t.Context(), repository, state, revisions)
	if err != nil || first == nil {
		t.Fatalf("first acquire = %+v, %v", first, err)
	}
	second, err := cache.acquire(t.Context(), repository, state, revisions)
	if err != nil || second == nil {
		t.Fatalf("cache hit = %+v, %v", second, err)
	}
	if validations.Load() != 1 || materializations.Load() != 1 {
		t.Fatalf(
			"cache hit validation/materialization = %d/%d, want 1/1",
			validations.Load(), materializations.Load(),
		)
	}

	if err := os.WriteFile(artifact, []byte("identity-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := cache.acquire(t.Context(), repository, state, revisions)
	if err != nil || third == nil {
		t.Fatalf("changed publication acquire = %+v, %v", third, err)
	}
	if validations.Load() != 2 || materializations.Load() != 2 {
		t.Fatalf(
			"changed validation/materialization = %d/%d, want 2/2",
			validations.Load(), materializations.Load(),
		)
	}
	if loaded[0].closes.Load() != 0 {
		t.Fatal("eviction closed a searcher with active query leases")
	}
	first.release()
	if loaded[0].closes.Load() != 0 {
		t.Fatal("eviction closed before every active lease was released")
	}
	second.release()
	if loaded[0].closes.Load() != 1 {
		t.Fatalf("retired searcher closes = %d, want 1", loaded[0].closes.Load())
	}
	third.release()
}

func TestFocusedCacheColdLoadDoesNotBlockAnotherRepository(t *testing.T) {
	indexDir := t.TempDir()
	const (
		slowRepository = "example.com/acme/slow"
		fastRepository = "example.com/acme/fast"
	)
	states := make(map[string]*analysisunit.State)
	for _, repository := range []string{slowRepository, fastRepository} {
		state, err := (analysisunit.Scope{
			Repository: repository,
			Name:       "service",
			Primary:    []string{"service"},
		}).State()
		if err != nil {
			t.Fatal(err)
		}
		states[repository] = state
		if err := os.WriteFile(
			filepath.Join(indexDir, focusedindex.ManifestName(repository)),
			[]byte(repository+"\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD",
		Branch:   "HEAD",
		Commit:   "1111111111111111111111111111111111111111",
	}}
	cache := newFocusedCache(indexDir)
	t.Cleanup(cache.close)
	slowStarted := make(chan struct{})
	var slowOnce sync.Once
	cache.validate = func(
		ctx context.Context,
		_ string,
		repository string,
		_ *analysisunit.State,
		_ []store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		if repository == slowRepository {
			slowOnce.Do(func() { close(slowStarted) })
			<-ctx.Done()
			return focusedindex.Manifest{}, ctx.Err()
		}
		return focusedindex.Manifest{
			Repository: repository,
			Digest: "sha256:" +
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}, nil
	}
	cache.materialize = func(
		_ context.Context,
		_ string,
		manifest focusedindex.Manifest,
	) (zoekt.Streamer, func(), error) {
		return &cacheTestStreamer{}, func() {}, nil
	}

	slowCtx, cancelSlow := context.WithCancel(t.Context())
	slowDone := make(chan error, 1)
	go func() {
		_, err := cache.acquire(
			slowCtx, slowRepository, states[slowRepository], revisions,
		)
		slowDone <- err
	}()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow focused validation did not start")
	}

	fastDone := make(chan *focusedLease, 1)
	go func() {
		lease, _ := cache.acquire(
			t.Context(), fastRepository, states[fastRepository], revisions,
		)
		fastDone <- lease
	}()
	select {
	case lease := <-fastDone:
		if lease == nil {
			t.Fatal("unrelated focused cache load failed")
		}
		lease.release()
	case <-time.After(time.Second):
		t.Fatal("cold repository blocked an unrelated focused cache load")
	}

	waitCtx, cancelWait := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancelWait()
	waitStarted := time.Now()
	lease, err := cache.acquire(
		waitCtx, slowRepository, states[slowRepository], revisions,
	)
	if err != nil || lease != nil {
		t.Fatalf(
			"same-repository later caller = %+v, %v, want fail-closed skip",
			lease, err,
		)
	}
	if elapsed := time.Since(waitStarted); elapsed > 100*time.Millisecond {
		t.Fatalf("same-repository skip took %v", elapsed)
	}

	cancelSlow()
	if err := <-slowDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("slow validation error = %v, want canceled", err)
	}
}

func TestFocusedCacheCloseCancelsAndJoinsColdLoad(t *testing.T) {
	indexDir := t.TempDir()
	repository := "example.com/acme/closing"
	state, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(indexDir, focusedindex.ManifestName(repository)),
		[]byte("closing\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD",
		Branch:   "HEAD",
		Commit:   "1111111111111111111111111111111111111111",
	}}
	cache := newFocusedCache(indexDir)
	started := make(chan struct{})
	cache.validate = func(
		ctx context.Context,
		_ string,
		_ string,
		_ *analysisunit.State,
		_ []store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		close(started)
		<-ctx.Done()
		return focusedindex.Manifest{}, ctx.Err()
	}
	acquireDone := make(chan error, 1)
	go func() {
		_, err := cache.acquire(t.Context(), repository, state, revisions)
		acquireDone <- err
	}()
	<-started
	closeDone := make(chan struct{})
	go func() {
		cache.close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("cache close did not join canceled cold load")
	}
	if err := <-acquireDone; err == nil {
		t.Fatal("cold acquire succeeded after cache close")
	}
}

func TestSearchBudgetStartsBeforeCompile(t *testing.T) {
	const repository = "example.com/acme/budget"
	commit := "1111111111111111111111111111111111111111"
	st := &delayedRepoStore{
		delay: 45 * time.Millisecond,
		repo: store.Repo{
			Name:              repository,
			IndexedCommitHash: commit,
		},
	}
	maxWallTimes := make(chan time.Duration, 1)
	backend := &resultTestStreamer{
		wait:        time.Hour,
		maxWallTime: maxWallTimes,
	}
	searcher := &Searcher{
		z:           backend,
		st:          st,
		indexDir:    t.TempDir(),
		focused:     newFocusedCache(t.TempDir()),
		maxWallTime: 90 * time.Millisecond,
	}
	t.Cleanup(searcher.Close)

	started := time.Now()
	if _, err := searcher.Search(t.Context(), "needle", Options{}); !errors.Is(
		err, context.DeadlineExceeded,
	) {
		t.Fatalf("search error = %v, want deadline", err)
	}
	elapsed := time.Since(started)
	if elapsed < 70*time.Millisecond || elapsed > 750*time.Millisecond {
		t.Fatalf("query-wide budget elapsed = %v, want about 90ms", elapsed)
	}
	select {
	case remaining := <-maxWallTimes:
		if remaining >= 75*time.Millisecond {
			t.Fatalf(
				"backend MaxWallTime = %v, compile time was not deducted",
				remaining,
			)
		}
	default:
		t.Fatal("backend did not observe a bounded search option")
	}
}

func TestFocusedColdPhasesUseQueryContext(t *testing.T) {
	for _, phase := range []string{"validation", "materialization"} {
		t.Run(phase, func(t *testing.T) {
			indexDir := t.TempDir()
			repository := "example.com/acme/" + phase
			state, err := (analysisunit.Scope{
				Repository: repository,
				Name:       "service",
				Primary:    []string{"service"},
			}).State()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(indexDir, focusedindex.ManifestName(repository)),
				[]byte(phase+"\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			revisions := []store.IndexedRevision{{
				Selector: "HEAD",
				Branch:   "HEAD",
				Commit:   "1111111111111111111111111111111111111111",
			}}
			cache := newFocusedCache(indexDir)
			t.Cleanup(cache.close)
			cache.validate = func(
				ctx context.Context,
				_ string,
				_ string,
				_ *analysisunit.State,
				_ []store.IndexedRevision,
			) (focusedindex.Manifest, error) {
				if phase == "validation" {
					<-ctx.Done()
					return focusedindex.Manifest{}, ctx.Err()
				}
				return focusedindex.Manifest{
					Repository: repository,
					Digest: "sha256:" +
						"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				}, nil
			}
			cache.materialize = func(
				ctx context.Context,
				_ string,
				_ focusedindex.Manifest,
			) (zoekt.Streamer, func(), error) {
				<-ctx.Done()
				return nil, nil, ctx.Err()
			}
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
			defer cancel()
			started := time.Now()
			if _, err := cache.acquire(
				ctx, repository, state, revisions,
			); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s acquire error = %v, want deadline", phase, err)
			}
			if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
				t.Fatalf("%s cancellation took %v", phase, elapsed)
			}
		})
	}
}

func TestFocusedCacheActualMaterializationHitsWithoutRevalidation(t *testing.T) {
	ctx := t.Context()
	repository := "example.com/acme/actual-cache"
	repoDir := t.TempDir()
	runCacheGit(t, repoDir, "init", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repoDir, "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repoDir, "service", "main.go"),
		[]byte("package service\nconst CacheNeedle = true\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repoDir, "secret", "old.go"),
		[]byte("package secret\nconst UndeclaredNeedle = true\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	runCacheGit(t, repoDir, "add", ".")
	runCacheGit(t, repoDir, "commit", "-m", "fixture")
	head := runCacheGit(t, repoDir, "rev-parse", "HEAD")
	scope := analysisunit.Scope{
		Repository: repository, Name: "service", Primary: []string{"service"},
	}
	state, err := scope.State()
	if err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: head,
	}}
	indexDir := t.TempDir()
	if _, err := focusedindex.Build(ctx, focusedindex.Request{
		Schema: focusedindex.RequestSchema, RepoDir: repoDir, OutputDir: indexDir,
		Scope: scope, Revisions: revisions,
	}, focusedindex.BuildOptions{}); err != nil {
		t.Fatal(err)
	}

	cache := newFocusedCache(indexDir)
	realValidate := cache.validate
	var validations atomic.Int32
	cache.validate = func(
		ctx context.Context,
		indexDir, repository string,
		state *analysisunit.State,
		revisions []store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		validations.Add(1)
		return realValidate(ctx, indexDir, repository, state, revisions)
	}
	t.Cleanup(cache.close)
	first, err := cache.acquire(t.Context(), repository, state, revisions)
	if err != nil || first == nil {
		t.Fatalf("actual first acquire = %+v, %v", first, err)
	}
	second, err := cache.acquire(t.Context(), repository, state, revisions)
	if err != nil || second == nil {
		t.Fatalf("actual cache hit = %+v, %v", second, err)
	}
	if validations.Load() != 1 {
		t.Fatalf("actual cache-hit validations = %d, want 1", validations.Load())
	}

	typeRepoQuery, err := query.Parse("type:repo CacheNeedle")
	if err != nil {
		t.Fatal(err)
	}
	typeRepoResult, err := first.searcher.Search(
		ctx, typeRepoQuery, Options{}.zoekt(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(typeRepoResult.Files) == 0 {
		t.Fatal("static focused searcher lost type:repo evaluation")
	}

	// Build a valid broader shard for the same repository, then inject only
	// that undeclared shard beside the committed narrow publication. A watched
	// directory searcher could load it; the static manifest-member composite
	// must never serve it. A positive cache hit checks only its exact bound
	// artifacts, so unrelated additions do not force one shared-directory
	// enumeration per focused repository.
	broadDir := t.TempDir()
	if _, err := focusedindex.Build(ctx, focusedindex.Request{
		Schema:    focusedindex.RequestSchema,
		RepoDir:   repoDir,
		OutputDir: broadDir,
		Scope: analysisunit.Scope{
			Repository: repository,
			Name:       "broad",
			Primary:    []string{"secret", "service"},
		},
		Revisions: revisions,
	}, focusedindex.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	broadShards, err := filepath.Glob(filepath.Join(broadDir, "*.zoekt"))
	if err != nil || len(broadShards) == 0 {
		t.Fatalf("broad shards = %v, %v", broadShards, err)
	}
	for _, shard := range broadShards {
		if err := os.Link(
			shard, filepath.Join(indexDir, filepath.Base(shard)),
		); err != nil {
			t.Fatal(err)
		}
	}
	if !first.current(ctx, store.Repo{
		Name:                repository,
		IndexedCommitHash:   head,
		IndexedRevisions:    revisions,
		IndexedAnalysisUnit: state,
	}) {
		t.Fatal(
			"undeclared artifact invalidated immutable active binding",
		)
	}
	undeclaredQuery, err := query.Parse("UndeclaredNeedle")
	if err != nil {
		t.Fatal(err)
	}
	undeclaredResult, err := first.searcher.Search(
		ctx, undeclaredQuery, Options{}.zoekt(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(undeclaredResult.Files) != 0 {
		t.Fatalf(
			"static focused searcher served undeclared shard: %+v",
			undeclaredResult.Files,
		)
	}
	third, err := cache.acquire(t.Context(), repository, state, revisions)
	if err != nil {
		t.Fatal(err)
	}
	if third == nil {
		t.Fatal("undeclared artifact evicted immutable exact binding")
	}
	if validations.Load() != 1 {
		t.Fatalf(
			"undeclared-addition validations = %d, want 1",
			validations.Load(),
		)
	}
	third.release()
	first.release()
	second.release()
}

func TestBoundStreamEmitsFastBackendBeforeSlowBackend(t *testing.T) {
	commit := "1111111111111111111111111111111111111111"
	fast := &resultTestStreamer{result: &zoekt.SearchResult{
		Files: []zoekt.FileMatch{{
			Repository: "example.com/acme/fast",
			FileName:   "fast.go",
			Version:    commit,
			Score:      1,
		}},
	}}
	slowGate := make(chan struct{})
	slow := &resultTestStreamer{
		gate: slowGate,
		result: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{{
				Repository: "example.com/acme/slow",
				FileName:   "slow.go",
				Version:    commit,
				Score:      100,
			}},
		},
	}
	compiled := &compiledSearch{
		query:     &query.Const{Value: true},
		baseQuery: &query.Const{Value: true},
		hasBase:   true,
		versions: map[string]string{
			"example.com/acme/fast": commit,
			"example.com/acme/slow": commit,
		},
		focused: []*focusedLease{{
			repository: "example.com/acme/slow",
			searcher:   slow,
		}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- runBoundStream(
			ctx,
			fast,
			compiled,
			Options{},
			func(result *zoekt.SearchResult, _ *focusedLease) error {
				if len(result.Files) > 0 {
					select {
					case first <- result.Files[0].Repository:
					default:
					}
				}
				return nil
			},
		)
	}()
	select {
	case repository := <-first:
		if repository != "example.com/acme/fast" {
			t.Fatalf("first streamed repository = %q", repository)
		}
	case <-time.After(time.Second):
		t.Fatal("fast backend did not stream while slow backend was blocked")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stream cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream fan-out ignored query cancellation")
	}
}

func TestBoundStreamAppliesOneCapAcrossBackends(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previousProcs)
	makeBackend := func(repository string) *resultTestStreamer {
		files := make([]zoekt.FileMatch, 3)
		for index := range files {
			files[index] = zoekt.FileMatch{
				Repository: repository,
				FileName:   fmt.Sprintf("file-%d.go", index),
			}
		}
		return &resultTestStreamer{
			result:   &zoekt.SearchResult{Files: files},
			canceled: make(chan struct{}),
		}
	}
	shared := makeBackend("example.com/acme/shared-cap")
	focused := makeBackend("example.com/acme/focused-cap")
	focused.started = make(chan struct{})
	compiled := &compiledSearch{
		query:     &query.Const{Value: true},
		baseQuery: &query.Const{Value: true},
		hasBase:   true,
		versions: map[string]string{
			"example.com/acme/shared-cap":  "",
			"example.com/acme/focused-cap": "",
		},
		focused: []*focusedLease{{
			repository: "example.com/acme/focused-cap",
			searcher:   focused,
		}},
	}
	remaining := 2
	emitted := 0
	if err := runBoundStream(
		t.Context(),
		shared,
		compiled,
		Options{},
		func(result *zoekt.SearchResult, _ *focusedLease) error {
			count := len(result.Files)
			if count > remaining {
				count = remaining
			}
			emitted += count
			remaining -= count
			if remaining == 0 {
				return errBoundStreamLimit
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if emitted != 2 {
		t.Fatalf("emitted files = %d, want global cap 2", emitted)
	}
	select {
	case <-shared.canceled:
	case <-time.After(time.Second):
		t.Fatal("shared backend did not observe cap cancellation")
	}
	select {
	case <-focused.canceled:
	case <-focused.started:
		select {
		case <-focused.canceled:
		case <-time.After(time.Second):
			t.Fatal("focused backend did not observe cap cancellation after starting")
		}
	case <-time.After(time.Second):
		// The global cap may cancel the query before this worker is scheduled;
		// an unstarted backend cannot observe cancellation.
	}
}

func TestBoundStreamHoldsSenderResultUntilSinkCompletes(t *testing.T) {
	backend := &ownershipTestStreamer{sent: make(chan struct{})}
	compiled := &compiledSearch{
		query:     &query.Const{Value: true},
		baseQuery: &query.Const{Value: true},
		hasBase:   true,
		versions: map[string]string{
			"example.com/acme/ownership": "",
		},
	}
	if err := runBoundStream(
		t.Context(),
		backend,
		compiled,
		Options{},
		func(result *zoekt.SearchResult, _ *focusedLease) error {
			select {
			case <-backend.sent:
				t.Fatal("backend regained result ownership before sink completed")
			case <-time.After(25 * time.Millisecond):
			}
			if got := result.Files[0].FileName; got != "original.go" {
				t.Fatalf("sink observed post-Send mutation %q", got)
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.sent:
	case <-time.After(time.Second):
		t.Fatal("backend remained blocked after sink acknowledged result")
	}
}

func TestBoundStreamLimitDoesNotHideBackendIntegrityError(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previousProcs)
	integrityErr := errors.New("focused backend integrity failure")
	errorBackend := &cancelErrorTestStreamer{
		started: make(chan struct{}),
		err:     integrityErr,
	}
	fastBackend := &resultTestStreamer{
		gate: errorBackend.started,
		result: &zoekt.SearchResult{Files: []zoekt.FileMatch{{
			Repository: "example.com/acme/fast-limit",
			FileName:   "fast.go",
		}}},
	}
	compiled := &compiledSearch{
		query:     &query.Const{Value: true},
		baseQuery: &query.Const{Value: true},
		hasBase:   true,
		versions: map[string]string{
			"example.com/acme/fast-limit": "",
			"example.com/acme/error":      "",
		},
		focused: []*focusedLease{{
			repository: "example.com/acme/error",
			searcher:   errorBackend,
		}},
	}
	err := runBoundStream(
		t.Context(),
		fastBackend,
		compiled,
		Options{},
		func(*zoekt.SearchResult, *focusedLease) error {
			return errBoundStreamLimit
		},
	)
	if !errors.Is(err, integrityErr) {
		t.Fatalf("stream error = %v, want integrity error", err)
	}
}

func TestStaticFocusedStreamEmitsDeclaredMembersProgressively(t *testing.T) {
	searcher := &staticFocusedSearcher{
		name: "example.com/acme/static-progress",
		shards: []staticFocusedShard{
			{
				name: "member-0.zoekt",
				searcher: &resultTestStreamer{result: &zoekt.SearchResult{
					Files: []zoekt.FileMatch{{FileName: "first.go"}},
				}},
			},
			{
				name: "member-1.zoekt",
				searcher: &resultTestStreamer{result: &zoekt.SearchResult{
					Files: []zoekt.FileMatch{{FileName: "second.go"}},
				}},
			},
		},
	}
	t.Cleanup(searcher.Close)
	var batches []string
	if err := searcher.StreamSearch(
		t.Context(),
		&query.Const{Value: true},
		Options{}.zoekt(),
		zoekt.SenderFunc(func(result *zoekt.SearchResult) {
			if len(result.Files) > 0 {
				batches = append(batches, result.Files[0].FileName)
			}
		}),
	); err != nil {
		t.Fatal(err)
	}
	if want := []string{"first.go", "second.go"}; !slices.Equal(batches, want) {
		t.Fatalf("static member batches = %v, want %v", batches, want)
	}
}

func TestStreamGlobalDisplayCapAndWholePublicationMarker(t *testing.T) {
	const repository = "example.com/acme/whole-stream"
	commit := "1111111111111111111111111111111111111111"
	files := make([]zoekt.FileMatch, 5)
	for index := range files {
		files[index] = zoekt.FileMatch{
			Repository: repository,
			FileName:   fmt.Sprintf("file-%d.go", index),
			Version:    commit,
			Score:      float64(len(files) - index),
		}
	}
	for _, marker := range []bool{false, true} {
		name := "visible"
		if marker {
			name = "publishing"
		}
		t.Run(name, func(t *testing.T) {
			indexDir := t.TempDir()
			if marker {
				if err := os.WriteFile(
					filepath.Join(
						indexDir,
						focusedindex.PublishingName(repository),
					),
					[]byte(repository+"\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			st := &mutableFocusedStore{repo: store.Repo{
				Name:              repository,
				IndexedCommitHash: commit,
			}}
			searcher := &Searcher{
				z:        &resultTestStreamer{result: &zoekt.SearchResult{Files: files}},
				st:       st,
				indexDir: indexDir,
				focused:  newFocusedCache(indexDir),
			}
			t.Cleanup(searcher.Close)
			var streamed []FileResult
			if _, err := searcher.Stream(
				t.Context(),
				"needle",
				Options{MaxMatches: 2},
				func(result *Result) {
					streamed = append(streamed, result.Files...)
				},
			); err != nil {
				t.Fatal(err)
			}
			want := 2
			if marker {
				want = 0
			}
			if len(streamed) != want {
				t.Fatalf("streamed files = %d, want %d", len(streamed), want)
			}
		})
	}
}

func TestStreamStaleVersionDoesNotConsumeDisplayCap(t *testing.T) {
	const repository = "example.com/acme/version-cap"
	commit := "1111111111111111111111111111111111111111"
	indexDir := t.TempDir()
	st := &mutableFocusedStore{repo: store.Repo{
		Name:              repository,
		IndexedCommitHash: commit,
	}}
	searcher := &Searcher{
		z: &resultTestStreamer{result: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{
				{
					Repository: repository,
					FileName:   "stale.go",
					Version:    "2222222222222222222222222222222222222222",
					Score:      100,
				},
				{
					Repository: repository,
					FileName:   "current.go",
					Version:    commit,
					Score:      1,
				},
			},
		}},
		st:       st,
		indexDir: indexDir,
		focused:  newFocusedCache(indexDir),
	}
	t.Cleanup(searcher.Close)
	var streamed []FileResult
	if _, err := searcher.Stream(
		t.Context(),
		"needle",
		Options{MaxMatches: 1},
		func(result *Result) {
			streamed = append(streamed, result.Files...)
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 1 || streamed[0].Path != "current.go" {
		t.Fatalf("streamed files = %+v, want current.go", streamed)
	}
	result, err := searcher.Search(
		t.Context(), "needle", Options{MaxMatches: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "current.go" {
		t.Fatalf("search files = %+v, want current.go", result.Files)
	}
}

func TestFocusedResultTimeGateDropsScopeChangedQuery(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "search"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			indexDir := t.TempDir()
			repository := "example.com/acme/result-time"
			commit := "1111111111111111111111111111111111111111"
			oldState, err := (analysisunit.Scope{
				Repository: repository, Name: "broad",
				Primary: []string{"secret", "service"},
			}).State()
			if err != nil {
				t.Fatal(err)
			}
			newState, err := (analysisunit.Scope{
				Repository: repository, Name: "narrow",
				Primary: []string{"service"},
			}).State()
			if err != nil {
				t.Fatal(err)
			}
			revisions := []store.IndexedRevision{{
				Selector: "HEAD", Branch: "HEAD", Commit: commit,
			}}
			st := &mutableFocusedStore{repo: store.Repo{
				Name: repository, IndexedCommitHash: commit,
				IndexedRevisions: revisions, IndexedAnalysisUnit: oldState,
			}}
			if err := os.WriteFile(
				filepath.Join(indexDir, focusedindex.ManifestName(repository)),
				[]byte("stable manifest identity\n"), 0o600,
			); err != nil {
				t.Fatal(err)
			}
			blocking := &blockingFocusedStreamer{
				started: make(chan struct{}), release: make(chan struct{}),
				repository: repository, version: commit,
			}
			cache := newFocusedCache(indexDir)
			cache.validate = func(
				_ context.Context,
				_ string,
				_ string,
				_ *analysisunit.State,
				_ []store.IndexedRevision,
			) (focusedindex.Manifest, error) {
				return focusedindex.Manifest{
					Repository: repository,
					Digest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				}, nil
			}
			cache.materialize = func(
				_ context.Context,
				_ string, _ focusedindex.Manifest,
			) (zoekt.Streamer, func(), error) {
				return blocking, func() {}, nil
			}
			searcher := &Searcher{
				z: &cacheTestStreamer{}, st: st,
				indexDir: indexDir, focused: cache,
			}
			t.Cleanup(searcher.Close)

			type outcome struct {
				files []FileResult
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				if !stream {
					result, err := searcher.Search(
						t.Context(), "RetiredDuringSearch", Options{},
					)
					done <- outcome{files: resultFiles(result), err: err}
					return
				}
				var files []FileResult
				_, err := searcher.Stream(
					t.Context(), "RetiredDuringSearch", Options{},
					func(batch *Result) {
						files = append(files, batch.Files...)
					},
				)
				done <- outcome{files: files, err: err}
			}()
			select {
			case <-blocking.started:
			case <-time.After(5 * time.Second):
				t.Fatal("focused backend did not start")
			}
			st.setState(newState)
			if err := os.WriteFile(
				filepath.Join(indexDir, focusedindex.PublishingName(repository)),
				[]byte(repository+"\n"), 0o600,
			); err != nil {
				t.Fatal(err)
			}
			close(blocking.release)
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			if len(got.files) != 0 {
				t.Fatalf(
					"retired focused result escaped result-time gate: %+v",
					got.files,
				)
			}
		})
	}
}

func resultFiles(result *Result) []FileResult {
	if result == nil {
		return nil
	}
	return result.Files
}

func runCacheGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=t",
		"-c", "user.email=t@t",
		"-c", "commit.gpgSign=false",
		"-C", dir,
	}, args...)
	output, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
