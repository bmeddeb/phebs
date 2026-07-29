package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func resourceSearchResult(
	repository, version string,
	source, documents int,
) *zoekt.SearchResult {
	files := make([]zoekt.FileMatch, 0, documents)
	for document := range documents {
		files = append(files, zoekt.FileMatch{
			Repository: repository,
			FileName: fmt.Sprintf(
				"source-%03d-file-%03d.go", source, document,
			),
			Version: version,
			Score:   float64(source*1000 + document),
		})
	}
	reason := zoekt.FlushReasonFinalFlush
	if source == 0 {
		reason = zoekt.FlushReasonTimerExpired
	}
	return &zoekt.SearchResult{
		Stats: zoekt.Stats{
			FileCount:     documents,
			MatchCount:    documents * 2,
			ShardsScanned: 1,
			FlushReason:   reason,
		},
		Files: files,
		RepoURLs: map[string]string{
			repository: "https://" + repository,
			"shared":   fmt.Sprintf("source-%03d", source),
		},
		LineFragments: map[string]string{
			repository: "#L{line}",
			"shared":   fmt.Sprintf("fragment-%03d", source),
		},
	}
}

func TestBoundResultAccumulatorRetainsGlobalTopK(t *testing.T) {
	const (
		sources   = 64
		documents = 20
		limit     = 7
		version   = "1111111111111111111111111111111111111111"
	)
	accumulator := newBoundResultAccumulator(limit)
	var all []zoekt.FileMatch
	for source := sources - 1; source >= 0; source-- {
		repository := fmt.Sprintf("example.com/acme/repo-%03d", source)
		result := resourceSearchResult(
			repository, version, source, documents,
		)
		all = append(all, result.Files...)
		accumulator.add(source, result)
	}
	result := accumulator.finish(23 * time.Millisecond)
	if len(result.Files) != limit || cap(result.Files) != limit {
		t.Fatalf(
			"retained files len/cap = %d/%d, want %d/%d",
			len(result.Files), cap(result.Files), limit, limit,
		)
	}
	sort.Slice(all, func(i, j int) bool {
		return boundFileBefore(all[i], all[j])
	})
	for index := range limit {
		if result.Files[index].FileName != all[index].FileName ||
			result.Files[index].Repository != all[index].Repository ||
			result.Files[index].Score != all[index].Score {
			t.Fatalf(
				"top result %d = %+v, want %+v",
				index, result.Files[index], all[index],
			)
		}
	}
	if result.FileCount != sources*documents ||
		result.MatchCount != sources*documents*2 ||
		result.ShardsScanned != sources {
		t.Fatalf("aggregated stats = %+v", result.Stats)
	}
	if result.Duration != 23*time.Millisecond {
		t.Fatalf("duration = %v", result.Duration)
	}
	if result.FlushReason != zoekt.FlushReasonTimerExpired {
		t.Fatalf("flush reason = %v", result.FlushReason)
	}
	if result.RepoURLs["shared"] != "source-000" ||
		result.LineFragments["shared"] != "fragment-000" {
		t.Fatalf(
			"deterministic shared maps = %q, %q",
			result.RepoURLs["shared"], result.LineFragments["shared"],
		)
	}
}

func TestRunBoundSearchManyBackendsRetainsGlobalTopK(t *testing.T) {
	const (
		backends  = 48
		documents = 12
		limit     = 9
		version   = "1111111111111111111111111111111111111111"
	)
	baseRepository := "example.com/acme/repo-000"
	compiled := &compiledSearch{
		query:     &query.Const{Value: true},
		baseQuery: &query.Const{Value: true},
		hasBase:   true,
		versions:  make(map[string]string, backends),
	}
	compiled.versions[baseRepository] = version
	base := &resultTestStreamer{result: resourceSearchResult(
		baseRepository, version, 0, documents,
	)}
	for source := 1; source < backends; source++ {
		repository := fmt.Sprintf("example.com/acme/repo-%03d", source)
		compiled.versions[repository] = version
		compiled.focused = append(compiled.focused, &focusedLease{
			repository: repository,
			searcher: &resultTestStreamer{result: resourceSearchResult(
				repository, version, source, documents,
			)},
		})
	}
	result, err := runBoundSearch(
		t.Context(), base, compiled, Options{MaxMatches: limit},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != limit || cap(result.Files) != limit {
		t.Fatalf(
			"bounded fanout len/cap = %d/%d, want %d/%d",
			len(result.Files), cap(result.Files), limit, limit,
		)
	}
	if result.FileCount != backends*documents ||
		result.ShardsScanned != backends {
		t.Fatalf("fanout stats = %+v", result.Stats)
	}
	if len(result.RepoURLs) != backends+1 {
		t.Fatalf("repo URL count = %d, want %d", len(result.RepoURLs), backends+1)
	}
	for index, file := range result.Files {
		if !strings.Contains(
			file.Repository, fmt.Sprintf("repo-%03d", backends-1),
		) {
			t.Fatalf("top file %d came from %q", index, file.Repository)
		}
	}
}

func TestRunBoundSearchRetiredFocusedCannotEvictValidBase(t *testing.T) {
	const version = "1111111111111111111111111111111111111111"
	baseRepository := "example.com/acme/base"
	focusedRepository := "example.com/acme/retired"
	compiled := &compiledSearch{
		query:     &query.Const{Value: true},
		baseQuery: &query.Const{Value: true},
		hasBase:   true,
		versions: map[string]string{
			baseRepository:    version,
			focusedRepository: version,
		},
		focused: []*focusedLease{{
			repository: focusedRepository,
			searcher: &resultTestStreamer{result: &zoekt.SearchResult{
				Stats: zoekt.Stats{ShardsScanned: 1},
				Files: []zoekt.FileMatch{{
					Repository: focusedRepository,
					FileName:   "retired.go",
					Version:    version,
					Score:      100,
				}},
				RepoURLs: map[string]string{focusedRepository: "retired"},
			}},
		}},
		validateFocused: func(
			_ context.Context, lease *focusedLease, full bool,
		) bool {
			return lease == nil || !full
		},
	}
	base := &resultTestStreamer{result: &zoekt.SearchResult{
		Stats: zoekt.Stats{ShardsScanned: 1},
		Files: []zoekt.FileMatch{{
			Repository: baseRepository,
			FileName:   "valid.go",
			Version:    version,
			Score:      1,
		}},
		RepoURLs: map[string]string{baseRepository: "valid"},
	}}
	result, err := runBoundSearch(
		t.Context(), base, compiled, Options{MaxMatches: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 ||
		result.Files[0].Repository != baseRepository {
		t.Fatalf("bounded result = %+v, want valid base", result.Files)
	}
	if result.ShardsScanned != 1 ||
		result.RepoURLs[focusedRepository] != "" {
		t.Fatalf("retired focused outcome was aggregated: %+v", result)
	}
}

func TestStaticFocusedSearchManyMembersRetainsGlobalTopK(t *testing.T) {
	const (
		members   = 64
		documents = 12
		limit     = 8
		version   = "1111111111111111111111111111111111111111"
	)
	searcher := &staticFocusedSearcher{name: "many-members"}
	for member := range members {
		repository := fmt.Sprintf("example.com/acme/member-%03d", member)
		searcher.shards = append(searcher.shards, staticFocusedShard{
			name: fmt.Sprintf("member-%03d.zoekt", member),
			searcher: &resultTestStreamer{result: resourceSearchResult(
				repository, version, member, documents,
			)},
		})
	}
	t.Cleanup(searcher.Close)
	result, err := searcher.Search(
		t.Context(),
		&query.Const{Value: true},
		&zoekt.SearchOptions{MaxDocDisplayCount: limit},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != limit || cap(result.Files) != limit {
		t.Fatalf(
			"static result len/cap = %d/%d, want %d/%d",
			len(result.Files), cap(result.Files), limit, limit,
		)
	}
	if result.FileCount != members*documents ||
		result.ShardsScanned != members {
		t.Fatalf("static aggregate stats = %+v", result.Stats)
	}
	if len(result.RepoURLs) != members+1 {
		t.Fatalf("static RepoURLs = %d, want %d", len(result.RepoURLs), members+1)
	}
}

func TestBoundStreamUsesOneFullGateAndConstantWorkPerMember(t *testing.T) {
	const members = 64
	repository := "example.com/acme/stream-gate"
	searcher := &staticFocusedSearcher{name: repository}
	for member := range members {
		searcher.shards = append(searcher.shards, staticFocusedShard{
			name: fmt.Sprintf("member-%03d.zoekt", member),
			searcher: &resultTestStreamer{result: &zoekt.SearchResult{
				Files: []zoekt.FileMatch{{
					Repository: repository,
					FileName:   fmt.Sprintf("file-%03d.go", member),
				}},
			}},
		})
	}
	t.Cleanup(searcher.Close)
	var fullChecks, quickChecks atomic.Int32
	compiled := &compiledSearch{
		query: &query.Const{Value: true},
		focused: []*focusedLease{{
			repository: repository,
			searcher:   searcher,
		}},
		validateFocused: func(
			_ context.Context, _ *focusedLease, full bool,
		) bool {
			if full {
				fullChecks.Add(1)
			} else {
				quickChecks.Add(1)
			}
			return true
		},
	}
	var batches int
	if err := runBoundStream(
		t.Context(),
		&cacheTestStreamer{},
		compiled,
		Options{},
		func(*zoekt.SearchResult, *focusedLease) error {
			batches++
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	// staticFocusedSearcher emits one progress batch plus one per member.
	if fullChecks.Load() != 1 ||
		quickChecks.Load() != members+1 ||
		batches != members+1 {
		t.Fatalf(
			"stream gates full/quick/batches = %d/%d/%d, want 1/%d/%d",
			fullChecks.Load(), quickChecks.Load(), batches,
			members+1, members+1,
		)
	}
}

func TestBoundWorkerCountHasFixedCeiling(t *testing.T) {
	previous := runtime.GOMAXPROCS(64)
	defer runtime.GOMAXPROCS(previous)
	if got := boundWorkerCount(1000); got != maxBoundWorkers {
		t.Fatalf("worker count = %d, want fixed ceiling %d", got, maxBoundWorkers)
	}
	if got := boundWorkerCount(3); got != 3 {
		t.Fatalf("small worker count = %d, want 3", got)
	}
}

func TestBoundSearchErrorSelectionIsDeterministic(t *testing.T) {
	contextError := &boundSearchError{
		index: 0, err: context.DeadlineExceeded,
	}
	later := errors.New("later integrity error")
	earlier := errors.New("earlier integrity error")
	selected := selectBoundSearchError(contextError, &boundSearchError{
		index: 7, err: later,
	})
	selected = selectBoundSearchError(selected, &boundSearchError{
		index: 2, err: earlier,
	})
	if !errors.Is(selected.err, earlier) || selected.index != 2 {
		t.Fatalf("selected error = %+v, want index 2 integrity error", selected)
	}
}

type resourceRepoStore struct {
	store.Store
	repos []store.Repo
}

func (s *resourceRepoStore) ListRepos(context.Context) ([]store.Repo, error) {
	result := make([]store.Repo, len(s.repos))
	for index, repo := range s.repos {
		result[index] = cloneFocusedStoreRepo(repo)
	}
	return result, nil
}

func (s *resourceRepoStore) GetRepo(
	_ context.Context,
	repository string,
) (*store.Repo, error) {
	for _, repo := range s.repos {
		if repo.Name == repository {
			copy := cloneFocusedStoreRepo(repo)
			return &copy, nil
		}
	}
	return nil, store.ErrNotFound
}

func TestCompilePrunesDeletedAndWholeFocusedCacheEntries(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	wholeRepository := "example.com/acme/switched-whole"
	deletedRepository := "example.com/acme/deleted"
	focusedState, err := (analysisunit.Scope{
		Repository: deletedRepository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	cache := newFocusedCache(t.TempDir())
	t.Cleanup(cache.close)
	type retiredFixture struct {
		repo     *focusedRepoCache
		entry    *focusedCacheEntry
		searcher *cacheTestStreamer
		lease    *focusedLease
	}
	fixtures := make(map[string]retiredFixture)
	for _, repository := range []string{wholeRepository, deletedRepository} {
		backend := &cacheTestStreamer{}
		entry := &focusedCacheEntry{searcher: backend, refs: 1}
		repo := &focusedRepoCache{entry: entry}
		cache.repos[repository] = repo
		fixtures[repository] = retiredFixture{
			repo: repo, entry: entry, searcher: backend,
			lease: &focusedLease{
				cache: cache, repo: repo, entry: entry,
				repository: repository, searcher: backend,
			},
		}
	}
	searcher := &Searcher{
		st: &resourceRepoStore{repos: []store.Repo{
			{
				Name: wholeRepository, IndexedCommitHash: commit,
				IndexedRevisions: []store.IndexedRevision{{
					Selector: "HEAD", Branch: "HEAD", Commit: commit,
				}},
			},
			{
				Name: deletedRepository, IndexedCommitHash: commit,
				IndexedAnalysisUnit: focusedState, Deleting: true,
			},
		}},
		indexDir: t.TempDir(),
		focused:  cache,
	}
	compiled, err := searcher.compile(t.Context(), "needle")
	if err != nil {
		t.Fatal(err)
	}
	compiled.release()
	cache.mu.Lock()
	remaining := len(cache.repos)
	cache.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("cache entries after compile prune = %d, want 0", remaining)
	}
	for repository, fixture := range fixtures {
		if !fixture.entry.retired || fixture.searcher.closes.Load() != 0 {
			t.Fatalf(
				"%s retired/closed = %v/%d before active release",
				repository, fixture.entry.retired,
				fixture.searcher.closes.Load(),
			)
		}
		fixture.lease.release()
		if fixture.searcher.closes.Load() != 1 {
			t.Fatalf("%s close count = %d", repository, fixture.searcher.closes.Load())
		}
	}
}

func TestFocusedCachePruneJoinsConcurrentLoad(t *testing.T) {
	repository := "example.com/acme/pruned-load"
	state, err := (analysisunit.Scope{
		Repository: repository, Name: "service",
		Primary: []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: "1111111111111111111111111111111111111111",
	}}
	cache := newFocusedCache(t.TempDir())
	t.Cleanup(cache.close)
	cache.validate = func(
		context.Context,
		string,
		string,
		*analysisunit.State,
		[]store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		return focusedindex.Manifest{
			Repository: repository,
			Digest: "sha256:" +
				strings.Repeat("a", 64),
		}, nil
	}
	started := make(chan struct{})
	release := make(chan struct{})
	loaded := &cacheTestStreamer{}
	cache.materialize = func(
		context.Context, string, focusedindex.Manifest,
	) (zoekt.Streamer, func(), error) {
		close(started)
		<-release
		return loaded, func() {}, nil
	}
	type outcome struct {
		lease *focusedLease
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		lease, err := cache.acquire(
			t.Context(), repository, state, revisions,
		)
		done <- outcome{lease: lease, err: err}
	}()
	<-started
	cache.prune(map[string]struct{}{})
	close(release)
	result := <-done
	if result.err != nil || result.lease != nil {
		t.Fatalf("pruned load result = %+v, %v", result.lease, result.err)
	}
	if loaded.closes.Load() != 1 {
		t.Fatalf("pruned loaded searcher closes = %d, want 1", loaded.closes.Load())
	}
	cache.mu.Lock()
	_, remains := cache.repos[repository]
	cache.mu.Unlock()
	if remains {
		t.Fatal("pruned loading slot remained cached")
	}
}

func TestFocusedCacheFillOutlivesTimedOutWaiter(t *testing.T) {
	repository := "example.com/acme/background-fill"
	state, err := (analysisunit.Scope{
		Repository: repository, Name: "service",
		Primary: []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: "1111111111111111111111111111111111111111",
	}}
	cache := newFocusedCache(t.TempDir())
	t.Cleanup(cache.close)
	if cap(cache.loadSlots) != maxConcurrentFocusedLoads {
		t.Fatalf(
			"cold-fill slots = %d, want %d",
			cap(cache.loadSlots), maxConcurrentFocusedLoads,
		)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var validations atomic.Int32
	cache.validate = func(
		ctx context.Context,
		_ string,
		_ string,
		_ *analysisunit.State,
		_ []store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		if validations.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return focusedindex.Manifest{
				Repository: repository,
				Digest: "sha256:" +
					strings.Repeat("b", 64),
			}, nil
		case <-ctx.Done():
			return focusedindex.Manifest{}, ctx.Err()
		}
	}
	loaded := &cacheTestStreamer{}
	cache.materialize = func(
		context.Context, string, focusedindex.Manifest,
	) (zoekt.Streamer, func(), error) {
		return loaded, func() {}, nil
	}
	waiterCtx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := cache.acquire(
			waiterCtx, repository, state, revisions,
		)
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cache-owned fill did not start")
	}
	if err := <-firstDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first waiter error = %v, want deadline", err)
	}
	otherState, err := (analysisunit.Scope{
		Repository: repository, Name: "other",
		Primary: []string{"other"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	type acquireOutcome struct {
		lease *focusedLease
		err   error
	}
	repoSlot, err := cache.repo(repository)
	if err != nil {
		t.Fatal(err)
	}
	repoSlot.mu.Lock()
	inflight := repoSlot.load
	repoSlot.mu.Unlock()
	mismatchDone := make(chan acquireOutcome, 1)
	go func() {
		lease, err := cache.waitForLoad(
			t.Context(),
			repoSlot,
			inflight,
			repository,
			otherState,
			revisions,
		)
		mismatchDone <- acquireOutcome{lease: lease, err: err}
	}()
	close(release)
	mismatch := <-mismatchDone
	if mismatch.err != nil || mismatch.lease != nil {
		t.Fatalf(
			"mismatched waiter received old-state load = %+v, %v",
			mismatch.lease, mismatch.err,
		)
	}
	nextCtx, nextCancel := context.WithTimeout(t.Context(), time.Second)
	defer nextCancel()
	lease, err := cache.acquire(
		nextCtx, repository, state, revisions,
	)
	if err != nil || lease == nil {
		t.Fatalf("post-timeout cache acquire = %+v, %v", lease, err)
	}
	lease.release()
	if validations.Load() != 1 {
		t.Fatalf(
			"background-fill validations = %d, want 1",
			validations.Load(),
		)
	}
	if loaded.closes.Load() != 0 {
		t.Fatalf("background-filled searcher closed early")
	}
}

func TestFocusedCacheColdSlotSaturationDoesNotSuppressWarmHit(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	repositories := map[string]string{
		"cold-a": "example.com/acme/cold-a",
		"cold-b": "example.com/acme/cold-b",
		"warm-c": "example.com/acme/warm-c",
		"cold-d": "example.com/acme/cold-d",
	}
	states := make(map[string]*analysisunit.State, len(repositories))
	for _, repository := range repositories {
		state, err := (analysisunit.Scope{
			Repository: repository, Name: "service",
			Primary: []string{"service"},
		}).State()
		if err != nil {
			t.Fatal(err)
		}
		states[repository] = state
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	cache := newFocusedCache(t.TempDir())
	t.Cleanup(cache.close)
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	releaseCold := make(chan struct{})
	var warmValidations, overflowValidations atomic.Int32
	cache.validate = func(
		ctx context.Context,
		_ string,
		repository string,
		_ *analysisunit.State,
		_ []store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		switch repository {
		case repositories["cold-a"]:
			close(startedA)
			select {
			case <-releaseCold:
			case <-ctx.Done():
				return focusedindex.Manifest{}, ctx.Err()
			}
		case repositories["cold-b"]:
			close(startedB)
			select {
			case <-releaseCold:
			case <-ctx.Done():
				return focusedindex.Manifest{}, ctx.Err()
			}
		case repositories["warm-c"]:
			warmValidations.Add(1)
		case repositories["cold-d"]:
			overflowValidations.Add(1)
		}
		return focusedindex.Manifest{
			Repository: repository,
			Digest: "sha256:" +
				strings.Repeat("c", 64),
		}, nil
	}
	cache.materialize = func(
		context.Context, string, focusedindex.Manifest,
	) (zoekt.Streamer, func(), error) {
		return &cacheTestStreamer{}, func() {}, nil
	}
	warm, err := cache.acquire(
		t.Context(),
		repositories["warm-c"],
		states[repositories["warm-c"]],
		revisions,
	)
	if err != nil || warm == nil {
		t.Fatalf("initial warm acquire = %+v, %v", warm, err)
	}
	warm.release()

	type loadOutcome struct {
		lease *focusedLease
		err   error
	}
	coldDone := make(chan loadOutcome, 2)
	for _, key := range []string{"cold-a", "cold-b"} {
		repository := repositories[key]
		go func() {
			lease, err := cache.acquire(
				t.Context(), repository, states[repository], revisions,
			)
			coldDone <- loadOutcome{lease: lease, err: err}
		}()
	}
	for name, started := range map[string]<-chan struct{}{
		"cold-a": startedA,
		"cold-b": startedB,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("%s did not occupy a cold-fill slot", name)
		}
	}
	overflow, err := cache.acquire(
		t.Context(),
		repositories["cold-d"],
		states[repositories["cold-d"]],
		revisions,
	)
	if err != nil || overflow != nil || overflowValidations.Load() != 0 {
		t.Fatalf(
			"overflow cold acquire = %+v, %v, validations %d",
			overflow, err, overflowValidations.Load(),
		)
	}
	warm, err = cache.acquire(
		t.Context(),
		repositories["warm-c"],
		states[repositories["warm-c"]],
		revisions,
	)
	if err != nil || warm == nil {
		t.Fatalf("saturated warm acquire = %+v, %v", warm, err)
	}
	warm.release()
	if warmValidations.Load() != 1 {
		t.Fatalf(
			"warm validations under saturation = %d, want 1",
			warmValidations.Load(),
		)
	}
	close(releaseCold)
	for range 2 {
		result := <-coldDone
		if result.err != nil || result.lease == nil {
			t.Fatalf("cold fill result = %+v, %v", result.lease, result.err)
		}
		result.lease.release()
	}
}

func TestSearchWarmFocusedRepoSkipsEarlierInflightColdRepos(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	repositories := []string{
		"example.com/acme/cold-a",
		"example.com/acme/cold-b",
		"example.com/acme/warm-c",
	}
	states := make(map[string]*analysisunit.State, len(repositories))
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	rows := make([]store.Repo, 0, len(repositories))
	for _, repository := range repositories {
		state, err := (analysisunit.Scope{
			Repository: repository, Name: "service",
			Primary: []string{"service"},
		}).State()
		if err != nil {
			t.Fatal(err)
		}
		states[repository] = state
		rows = append(rows, store.Repo{
			Name: repository, IndexedCommitHash: commit,
			IndexedAnalysisUnit: state,
			IndexedRevisions:    revisions,
		})
	}
	indexDir := t.TempDir()
	cache := newFocusedCache(indexDir)
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	releaseCold := make(chan struct{})
	cache.validate = func(
		ctx context.Context,
		_ string,
		repository string,
		_ *analysisunit.State,
		_ []store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		switch repository {
		case repositories[0]:
			close(startedA)
			select {
			case <-releaseCold:
			case <-ctx.Done():
				return focusedindex.Manifest{}, ctx.Err()
			}
		case repositories[1]:
			close(startedB)
			select {
			case <-releaseCold:
			case <-ctx.Done():
				return focusedindex.Manifest{}, ctx.Err()
			}
		}
		return focusedindex.Manifest{
			Repository: repository,
			Digest: "sha256:" +
				strings.Repeat("d", 64),
		}, nil
	}
	cache.materialize = func(
		_ context.Context,
		_ string,
		manifest focusedindex.Manifest,
	) (zoekt.Streamer, func(), error) {
		return &resultTestStreamer{result: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{{
				Repository: manifest.Repository,
				FileName:   "service/main.go",
				Version:    commit,
				Score:      1,
			}},
		}}, func() {}, nil
	}
	warm, err := cache.acquire(
		t.Context(), repositories[2], states[repositories[2]], revisions,
	)
	if err != nil || warm == nil {
		t.Fatalf("warm seed acquire = %+v, %v", warm, err)
	}
	warm.release()

	type loadOutcome struct {
		lease *focusedLease
		err   error
	}
	coldDone := make(chan loadOutcome, 2)
	for _, repository := range repositories[:2] {
		go func() {
			lease, err := cache.acquire(
				t.Context(), repository, states[repository], revisions,
			)
			coldDone <- loadOutcome{lease: lease, err: err}
		}()
	}
	for name, started := range map[string]<-chan struct{}{
		repositories[0]: startedA,
		repositories[1]: startedB,
	} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("%s did not start its cold fill", name)
		}
	}

	searcher := &Searcher{
		z:           &cacheTestStreamer{},
		st:          &resourceRepoStore{repos: rows},
		indexDir:    indexDir,
		focused:     cache,
		maxWallTime: 2 * time.Second,
	}
	t.Cleanup(searcher.Close)
	type searchOutcome struct {
		result *Result
		err    error
	}
	searchDone := make(chan searchOutcome, 1)
	go func() {
		result, err := searcher.Search(
			t.Context(), "needle", Options{},
		)
		searchDone <- searchOutcome{result: result, err: err}
	}()
	select {
	case outcome := <-searchDone:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result == nil || len(outcome.result.Files) != 1 ||
			outcome.result.Files[0].Repo != repositories[2] {
			t.Fatalf("warm C search result = %+v", outcome.result)
		}
	case <-time.After(500 * time.Millisecond):
		close(releaseCold)
		<-searchDone
		t.Fatal("warm C search waited behind in-flight cold A/B")
	}
	close(releaseCold)
	for range 2 {
		outcome := <-coldDone
		if outcome.err != nil || outcome.lease == nil {
			t.Fatalf("cold fill result = %+v, %v", outcome.lease, outcome.err)
		}
		outcome.lease.release()
	}
}

func TestSearchColdOnlyFocusedRepoWaitsForStarterFill(t *testing.T) {
	const (
		repository = "example.com/acme/cold-only"
		commit     = "1111111111111111111111111111111111111111"
	)
	state, err := (analysisunit.Scope{
		Repository: repository, Name: "service",
		Primary: []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	indexDir := t.TempDir()
	cache := newFocusedCache(indexDir)
	var validations atomic.Int32
	cache.validate = func(
		context.Context,
		string,
		string,
		*analysisunit.State,
		[]store.IndexedRevision,
	) (focusedindex.Manifest, error) {
		validations.Add(1)
		return focusedindex.Manifest{
			Repository: repository,
			Digest: "sha256:" +
				strings.Repeat("e", 64),
		}, nil
	}
	cache.materialize = func(
		context.Context, string, focusedindex.Manifest,
	) (zoekt.Streamer, func(), error) {
		return &resultTestStreamer{result: &zoekt.SearchResult{
			Files: []zoekt.FileMatch{{
				Repository: repository,
				FileName:   "service/main.go",
				Version:    commit,
				Score:      1,
			}},
		}}, func() {}, nil
	}
	searcher := &Searcher{
		z: &cacheTestStreamer{},
		st: &resourceRepoStore{repos: []store.Repo{{
			Name: repository, IndexedCommitHash: commit,
			IndexedAnalysisUnit: state,
			IndexedRevisions:    revisions,
		}}},
		indexDir: indexDir,
		focused:  cache,
	}
	t.Cleanup(searcher.Close)
	result, err := searcher.Search(
		t.Context(), "repo:"+repository+" needle", Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Repo != repository {
		t.Fatalf("first cold-only search = %+v", result.Files)
	}
	if validations.Load() != 1 {
		t.Fatalf("first cold-only validations = %d, want 1", validations.Load())
	}
}

func TestWholeResultTimeGateDropsSameCommitFocusedTransition(t *testing.T) {
	const (
		repository = "example.com/acme/whole-to-focused"
		commit     = "1111111111111111111111111111111111111111"
	)
	focusedState, err := (analysisunit.Scope{
		Repository: repository,
		Name:       "service",
		Primary:    []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []bool{false, true} {
		name := "search"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			st := &mutableFocusedStore{repo: store.Repo{
				Name:              repository,
				IndexedCommitHash: commit,
				IndexedRevisions: []store.IndexedRevision{{
					Selector: "HEAD", Branch: "HEAD", Commit: commit,
				}},
			}}
			backend := &blockingFocusedStreamer{
				started:    make(chan struct{}),
				release:    make(chan struct{}),
				repository: repository,
				version:    commit,
			}
			indexDir := t.TempDir()
			searcher := &Searcher{
				z: backend, st: st, indexDir: indexDir,
				focused: newFocusedCache(indexDir),
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
					t.Context(),
					"RetiredDuringSearch",
					Options{},
					func(batch *Result) {
						files = append(files, batch.Files...)
					},
				)
				done <- outcome{files: files, err: err}
			}()
			select {
			case <-backend.started:
			case <-time.After(5 * time.Second):
				t.Fatal("whole backend did not start")
			}
			st.setState(focusedState)
			close(backend.release)
			select {
			case result := <-done:
				if result.err != nil {
					t.Fatal(result.err)
				}
				if len(result.files) != 0 {
					t.Fatalf(
						"same-commit whole-to-focused files = %+v",
						result.files,
					)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("whole-to-focused query did not finish")
			}
		})
	}
}

type mutatingMemberSearcher struct {
	path string
}

func (s *mutatingMemberSearcher) Search(
	context.Context, query.Q, *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	if err := os.WriteFile(s.path, []byte("changed!"), 0o600); err != nil {
		return nil, err
	}
	return &zoekt.SearchResult{}, nil
}

func (*mutatingMemberSearcher) List(
	context.Context, query.Q, *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	return &zoekt.RepoList{}, nil
}

func (*mutatingMemberSearcher) Close() {}

func (*mutatingMemberSearcher) String() string { return "mutating-member" }

func TestStaticFocusedMemberIdentityCheckedAfterSearch(t *testing.T) {
	path := t.TempDir() + "/member.zoekt"
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	searcher := &staticFocusedSearcher{
		name: "identity",
		shards: []staticFocusedShard{{
			name: "member.zoekt",
			path: path,
			identity: focusedArtifactIdentity{
				name:       "member.zoekt",
				info:       info,
				changeTime: fileInfoChangeTime(info),
			},
			searcher: &mutatingMemberSearcher{path: path},
		}},
	}
	t.Cleanup(searcher.Close)
	if _, err := searcher.Search(
		t.Context(),
		&query.Const{Value: true},
		Options{}.zoekt(),
	); err == nil || !strings.Contains(err.Error(), "changed during search") {
		t.Fatalf("member mutation error = %v", err)
	}
}
