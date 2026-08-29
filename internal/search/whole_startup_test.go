package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestColdWholeValidationDeadlineIsRetryableAndReused(t *testing.T) {
	fixture := buildServiceSearchFixture(t)
	searcher, err := Open(fixture.indexDir, fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()
	for range cap(searcher.whole.loadSlots) {
		searcher.whole.loadSlots <- struct{}{}
	}
	searcher.maxWallTime = 50 * time.Millisecond
	if _, err := searcher.Search(
		t.Context(), "T343_NEEDLE", Options{MaxMatches: 10},
	); !errors.Is(err, ErrWholeGenerationWarming) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cold shared search error = %v", err)
	}
	repo := searcher.whole.repos[fixture.store.repo.Name]
	repo.mu.Lock()
	validation := repo.validation
	validated := repo.shared != nil && repo.shared.validated
	repo.mu.Unlock()
	if validation == nil || validated {
		t.Fatalf("cold shared state validation=%t validated=%t", validation != nil, validated)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := searcher.Search(canceled, "T343_NEEDLE", Options{}); !errors.Is(
		err, context.Canceled,
	) || errors.Is(err, ErrWholeGenerationWarming) {
		t.Fatalf("canceled shared search error = %v", err)
	}
	for range cap(searcher.whole.loadSlots) {
		<-searcher.whole.loadSlots
	}
	searcher.maxWallTime = 5 * time.Second
	result, err := searcher.Search(
		t.Context(), "T343_NEEDLE", Options{MaxMatches: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("warmed shared search files=%d", len(result.Files))
	}
	repo.mu.Lock()
	validated = repo.shared != nil && repo.shared.validated
	validation = repo.validation
	repo.mu.Unlock()
	if !validated || validation != nil {
		t.Fatalf("warmed shared state validation=%t validated=%t", validation != nil, validated)
	}
}

func TestWholeWaitClassifiesRequestAndCacheDeadlinesExactly(t *testing.T) {
	deadlineContext := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithDeadline(
			t.Context(), time.Now().Add(-time.Second),
		)
		t.Cleanup(cancel)
		return ctx
	}
	t.Run("shared completed success", func(t *testing.T) {
		binding := &wholeSharedBinding{validated: true}
		validation := &wholeSharedValidation{
			ready: make(chan struct{}), binding: binding,
			ctx: t.Context(), cancel: func() {},
		}
		close(validation.ready)
		repo := &wholeRepoCache{shared: binding}
		cache := newWholeCache(t.TempDir())
		defer cache.close()
		err := cache.waitForSharedValidation(
			deadlineContext(t), repo, validation, binding,
		)
		if !errors.Is(err, ErrWholeGenerationWarming) ||
			!errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("completed shared wait error = %v", err)
		}
	})
	t.Run("shared cache deadline", func(t *testing.T) {
		binding := &wholeSharedBinding{}
		validation := &wholeSharedValidation{
			ready: make(chan struct{}), binding: binding,
			ctx: deadlineContext(t), cancel: func() {},
		}
		repo := &wholeRepoCache{shared: binding, validation: validation}
		cache := newWholeCache(t.TempDir())
		defer cache.close()
		err := cache.waitForSharedValidation(
			deadlineContext(t), repo, validation, binding,
		)
		if !errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, ErrWholeGenerationWarming) {
			t.Fatalf("expired shared cache error = %v", err)
		}
	})
	t.Run("exact completed error", func(t *testing.T) {
		terminal := errors.New("exact load failed")
		load := &wholeCacheLoad{
			ready: make(chan struct{}), err: terminal,
			ctx: t.Context(), cancel: func() {},
		}
		close(load.ready)
		cache := newWholeCache(t.TempDir())
		defer cache.close()
		_, err := cache.waitForLoad(
			deadlineContext(t), &wholeRepoCache{}, load, "repo", nil,
		)
		if !errors.Is(err, terminal) ||
			errors.Is(err, ErrWholeGenerationWarming) {
			t.Fatalf("completed exact load error = %v", err)
		}
	})
	t.Run("exact cache deadline", func(t *testing.T) {
		load := &wholeCacheLoad{
			ready: make(chan struct{}), revisions: nil,
			ctx: deadlineContext(t), cancel: func() {},
		}
		repo := &wholeRepoCache{load: load}
		cache := newWholeCache(t.TempDir())
		defer cache.close()
		_, err := cache.waitForLoad(
			deadlineContext(t), repo, load, "repo", nil,
		)
		if !errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, ErrWholeGenerationWarming) {
			t.Fatalf("expired exact cache error = %v", err)
		}
	})
}

func TestOpenDefersWholeContentValidationUntilFirstQuery(t *testing.T) {
	const repository = "example.com/acme/lazy-startup"
	const commit = "1111111111111111111111111111111111111111"
	revisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}
	indexDir := t.TempDir()
	manifest := publishWholeSearchFixture(
		t, indexDir, repository, revisions,
		"LazyStartupNeedle", 3,
	)
	if len(manifest.Members) < 2 {
		t.Fatalf(
			"startup fixture members = %d, want multiple",
			len(manifest.Members),
		)
	}
	// Ensure the receipt exists as a normal file before Open. The assertion
	// below inspects cache state rather than timing or filesystem atime.
	if info, err := os.Lstat(filepath.Join(
		indexDir, focusedindex.WholeManifestName(repository),
	)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("whole receipt before Open = %v, %v", info, err)
	}
	st := &resourceRepoStore{repos: []store.Repo{{
		Name: repository, IndexedCommitHash: commit,
		IndexedRevisions: revisions,
	}}}
	searcher, err := Open(indexDir, st)
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.Close()
	repo := searcher.whole.repos[repository]
	if repo == nil {
		t.Fatal("startup generation was not captured")
	}
	repo.mu.Lock()
	binding := repo.shared
	validation := repo.validation
	entry := repo.entry
	repo.mu.Unlock()
	if binding == nil || binding.memberCount != len(manifest.Members) {
		t.Fatalf("startup binding = %+v, want %d members", binding, len(manifest.Members))
	}
	if binding.validated || validation != nil || entry != nil {
		t.Fatalf(
			"Open performed cold validation/materialization: "+
				"validated=%v validation=%v exact=%v",
			binding.validated, validation != nil, entry != nil,
		)
	}
}

type mixedSharedStreamer struct {
	*resultTestStreamer
	searchCalls atomic.Int32
	streamCalls atomic.Int32
}

func (s *mixedSharedStreamer) Search(
	ctx context.Context,
	q query.Q,
	options *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	s.searchCalls.Add(1)
	return s.resultTestStreamer.Search(ctx, q, options)
}

func (s *mixedSharedStreamer) StreamSearch(
	ctx context.Context,
	q query.Q,
	options *zoekt.SearchOptions,
	sender zoekt.Sender,
) error {
	s.streamCalls.Add(1)
	return s.resultTestStreamer.StreamSearch(
		ctx, q, options, sender,
	)
}

func TestRuntimeMultiShardGenerationNeverUsesMixedSharedReader(
	t *testing.T,
) {
	const repository = "example.com/acme/mixed-watcher"
	oldRevisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: "1111111111111111111111111111111111111111",
	}}
	newRevisions := []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD",
		Commit: "2222222222222222222222222222222222222222",
	}}
	indexDir := t.TempDir()
	oldManifest := publishWholeSearchFixture(
		t, indexDir, repository, oldRevisions,
		"RetiredMixedNeedle", 4,
	)
	cache := newWholeCache(indexDir)
	defer cache.close()
	_, oldFingerprint, err := wholePublicationSnapshot(
		t.Context(), indexDir, repository, oldRevisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := cache.repo(repository)
	if err != nil {
		t.Fatal(err)
	}
	repo.shared = &wholeSharedBinding{
		revisions: oldRevisions, fingerprint: oldFingerprint,
		memberCount: len(oldManifest.Members), validated: true,
	}

	newManifest := publishWholeSearchFixture(
		t, indexDir, repository, newRevisions,
		"CurrentMixedNeedle", 4,
	)
	if len(newManifest.Members) < 3 {
		t.Fatalf(
			"new fixture members = %d, want at least 3",
			len(newManifest.Members),
		)
	}
	// This backend models the forbidden watcher state. It claims the new repo
	// identity but would return retired content assembled from a mixed shard
	// view if the query ever touched it.
	mixed := &mixedSharedStreamer{
		resultTestStreamer: &resultTestStreamer{
			result: &zoekt.SearchResult{Files: []zoekt.FileMatch{{
				Repository: repository,
				FileName:   "retired.go",
				Version:    newRevisions[0].Commit,
			}}},
		},
	}
	lease, err := cache.acquireIfStale(
		t.Context(), mixed, repository, newRevisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("runtime generation trusted mixed shared reader")
	}
	defer lease.release()
	parsed, err := query.Parse("CurrentMixedNeedle")
	if err != nil {
		t.Fatal(err)
	}
	compiled := &compiledSearch{
		query: parsed,
		versions: map[string]string{
			repository: newRevisions[0].Commit,
		},
		baseRevisions: map[string]store.IndexedRevision{
			repository: newRevisions[0],
		},
		whole: []*wholeLease{lease},
		validateWholeGenerations: func(
			context.Context,
			map[string][]store.IndexedRevision,
			[]*wholeLease,
			bool,
		) bool {
			return true
		},
	}
	result, err := runBoundSearch(
		t.Context(), mixed, compiled, Options{},
	)
	if err != nil || len(result.Files) == 0 {
		t.Fatalf("exact mixed-generation JSON = %+v, %v", result, err)
	}
	var streamed int
	if err := runBoundStream(
		t.Context(), mixed, compiled, Options{},
		func(result *zoekt.SearchResult, _ *focusedLease) error {
			streamed += len(result.Files)
			return nil
		},
	); err != nil || streamed == 0 {
		t.Fatalf(
			"exact mixed-generation Stream files/error = %d, %v",
			streamed, err,
		)
	}
	if mixed.searchCalls.Load() != 0 ||
		mixed.streamCalls.Load() != 0 {
		t.Fatalf(
			"mixed shared reader calls = Search %d Stream %d, want 0/0",
			mixed.searchCalls.Load(), mixed.streamCalls.Load(),
		)
	}
}

func publishWholeSearchFixture(
	t *testing.T,
	indexDir, repository string,
	revisions []store.IndexedRevision,
	needle string,
	documents int,
) focusedindex.WholeManifest {
	t.Helper()
	stageDir := t.TempDir()
	builder, err := index.NewBuilder(index.Options{
		IndexDir:            stageDir,
		ShardPrefixOverride: "startup",
		ShardMax:            1,
		Parallelism:         1,
		DisableCTags:        true,
		RepositoryDescription: zoekt.Repository{
			Name: repository,
			Branches: []zoekt.RepositoryBranch{{
				Name:    revisions[0].Branch,
				Version: revisions[0].Commit,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for document := range documents {
		if err := builder.Add(index.Document{
			Name: "file-" + string(rune('a'+document)) + ".go",
			Content: []byte(
				"package fixture\nconst " + needle + " = true\n",
			),
			Branches: []string{revisions[0].Branch},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.PublishWhole(
		t.Context(), indexDir, stageDir, repository, revisions,
	); err != nil {
		t.Fatal(err)
	}
	if err := focusedindex.FinishPublication(
		indexDir, repository,
	); err != nil {
		t.Fatal(err)
	}
	manifest, err := focusedindex.ReadWholeManifest(
		indexDir, repository, revisions,
	)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
