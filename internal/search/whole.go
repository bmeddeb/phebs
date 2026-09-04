package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

// wholeCache is the exact-reader fallback for the asynchronous
// DirectorySearcher. Its steady-state path remembers the filesystem identity
// that the shared reader has acknowledged. A publication replacement changes
// that identity before its database row becomes searchable, so the first
// query for the new row either observes the shared reader's matching branch
// metadata or binds an immutable reader directly to the committed shard set.
type wholeCache struct {
	indexDir string

	mu        sync.Mutex
	repos     map[string]*wholeRepoCache
	selected  map[string]*wholeRepoCache
	closed    bool
	closeCtx  context.Context
	cancel    context.CancelFunc
	loadSlots chan struct{}
}

type wholeRepoCache struct {
	mu            sync.Mutex
	shared        *wholeSharedBinding
	validation    *wholeSharedValidation
	entry         *wholeCacheEntry
	load          *wholeCacheLoad
	retryFailures int
	pruned        bool
}

type wholeSharedBinding struct {
	revisions   []store.IndexedRevision
	fingerprint focusedFingerprint
	memberCount int
	validated   bool
	failures    int
	retryAt     time.Time
	lastError   error
}

type wholeSharedValidation struct {
	ready   chan struct{}
	binding *wholeSharedBinding
	err     error
	ctx     context.Context
	cancel  context.CancelFunc
}

type wholeSharedCandidate struct {
	revisions      []store.IndexedRevision
	manifestDigest string
	fingerprint    focusedFingerprint
	memberCount    int
}

type wholeCacheEntry struct {
	directory    string
	revisions    []store.IndexedRevision
	fingerprint  focusedFingerprint
	searchDigest string
	searcher     zoekt.Streamer
	failures     int
	retryAt      time.Time
	lastError    error
	refs         int
	retired      bool
}

type wholeCacheLoad struct {
	ready     chan struct{}
	directory string
	digest    string
	revisions []store.IndexedRevision
	entry     *wholeCacheEntry
	err       error
	ctx       context.Context
	cancel    context.CancelFunc
	failures  int
}

type wholeLease struct {
	cache      *wholeCache
	repo       *wholeRepoCache
	entry      *wholeCacheEntry
	repository string
	searcher   zoekt.Streamer
}

// ExactGenerationReader is a synchronous, digest-selected whole-repository
// reader for exact validation. It is independent of the asynchronous cache and
// retains its validated file identities until Close.
type ExactGenerationReader struct {
	mu        sync.Mutex
	entry     *wholeCacheEntry
	admission chan struct{}
}

// WholeGenerationWarmingTimeout bounds one cache-owned validation or exact
// reader load independently of the request that started it.
const WholeGenerationWarmingTimeout = 10 * time.Minute
const maxConcurrentWholeLoads = 2

var exactGenerationReaderSlots = make(chan struct{}, maxConcurrentWholeLoads)

var errWholeSharedBaselineChanged = errors.New(
	"whole-repository shared baseline changed",
)

// ErrWholeGenerationWarming means the request deadline expired while the same
// exact generation's cache-owned work was active or raced its successful
// completion. It is retryable; validation remains mandatory.
var ErrWholeGenerationWarming = errors.New(
	"whole-repository generation is warming",
)

func newWholeCache(indexDir string) *wholeCache {
	closeCtx, cancel := context.WithCancel(context.Background())
	return &wholeCache{
		indexDir:  indexDir,
		repos:     make(map[string]*wholeRepoCache),
		selected:  make(map[string]*wholeRepoCache),
		closeCtx:  closeCtx,
		cancel:    cancel,
		loadSlots: make(chan struct{}, maxConcurrentWholeLoads),
	}
}

// OpenExactGenerationReader accounts and validates one immutable generation
// synchronously in the caller's context. Ordinary search does not call it.
func OpenExactGenerationReader(
	ctx context.Context, indexDir, repository, digest string,
) (*ExactGenerationReader, error) {
	loadCtx, cancel := context.WithTimeout(ctx, WholeGenerationWarmingTimeout)
	defer cancel()
	select {
	case exactGenerationReaderSlots <- struct{}{}:
	case <-loadCtx.Done():
		return nil, loadCtx.Err()
	}
	admitted := true
	defer func() {
		if admitted {
			<-exactGenerationReaderSlots
		}
	}()
	controls, err := focusedindex.ReadSearchGenerationControlsWithReceiptIdentity(
		loadCtx, indexDir, repository, digest,
	)
	if err != nil {
		return nil, err
	}
	receiptIdentity := focusedArtifactIdentity{
		name:       controls.ReceiptFileInfo.Name(),
		info:       controls.ReceiptFileInfo,
		changeTime: fileInfoChangeTime(controls.ReceiptFileInfo),
	}
	entry, err := (&wholeCache{}).load(
		loadCtx, controls.Directory, repository, digest, controls.Receipt.Revisions, 0,
	)
	if err != nil {
		return nil, err
	}
	if entry == nil || entry.searcher == nil {
		closeWholeEntry(entry)
		return nil, errors.New("exact whole-repository reader is unavailable")
	}
	entry.fingerprint = append(entry.fingerprint, receiptIdentity)
	if err := exactGenerationReaderFence(loadCtx, entry); err != nil {
		closeWholeEntry(entry)
		return nil, err
	}
	admitted = false
	return &ExactGenerationReader{
		entry: entry, admission: exactGenerationReaderSlots,
	}, nil
}

// Search runs one bounded query between complete file-identity fences.
func (reader *ExactGenerationReader) Search(
	ctx context.Context, q query.Q, options Options,
) (*zoekt.SearchResult, error) {
	if reader == nil || q == nil {
		return nil, errors.New("invalid exact whole-repository query")
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.entry == nil || reader.entry.searcher == nil {
		return nil, errors.New("exact whole-repository reader is closed")
	}
	if err := exactGenerationReaderFence(ctx, reader.entry); err != nil {
		return nil, err
	}
	result, searchErr := reader.entry.searcher.Search(ctx, q, options.zoektWithin(ctx))
	fenceErr := exactGenerationReaderFence(ctx, reader.entry)
	if searchErr != nil || fenceErr != nil {
		return nil, errors.Join(searchErr, fenceErr)
	}
	return result, nil
}

func exactGenerationReaderFence(ctx context.Context, entry *wholeCacheEntry) error {
	current, err := focusedFingerprintKnownNames(ctx, entry.directory, entry.fingerprint)
	if err != nil {
		return err
	}
	if !equalFocusedFingerprint(entry.fingerprint, current) {
		return errors.New("exact whole-repository generation changed")
	}
	return nil
}

// Close releases the mmap-backed immutable reader. It is idempotent.
func (reader *ExactGenerationReader) Close() {
	if reader == nil {
		return
	}
	reader.mu.Lock()
	closeWholeEntry(reader.entry)
	reader.entry = nil
	if reader.admission != nil {
		<-reader.admission
		reader.admission = nil
	}
	reader.mu.Unlock()
}

// acquireIfStale returns nil when the shared DirectorySearcher is proven to
// hold the committed revision set. Otherwise it returns an exact immutable
// fallback. Failure to bind the fallback is an error, never a silent omission.
func (c *wholeCache) acquireIfStale(
	ctx context.Context,
	_ zoekt.Streamer,
	repository string,
	revisions []store.IndexedRevision,
) (*wholeLease, error) {
	repo, err := c.repo(repository)
	if err != nil {
		return nil, err
	}
	if binding := c.sharedControlBinding(
		ctx, repo, repository, revisions,
	); binding != nil {
		if err := c.ensureSharedValidated(
			ctx, repo, repository, revisions, binding,
		); err == nil {
			return nil, nil
		} else if !errors.Is(err, errWholeSharedBaselineChanged) {
			return nil, fmt.Errorf(
				"validate startup whole-repository publication %q: %w",
				repository, err,
			)
		}
	}
	lease, err := c.acquireExact(ctx, repo, repository, revisions)
	if err != nil {
		return nil, fmt.Errorf(
			"bind committed whole-repository publication %q: %w",
			repository, err,
		)
	}
	if lease == nil {
		return nil, fmt.Errorf(
			"bind committed whole-repository publication %q: no exact reader",
			repository,
		)
	}
	return lease, nil
}

func (c *wholeCache) captureSharedCandidates(
	ctx context.Context,
	st store.Store,
) (map[string]wholeSharedCandidate, error) {
	repositories, err := st.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make(map[string]wholeSharedCandidate)
	for _, stored := range repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if stored.Deleting || stored.IndexedCommitHash == "" ||
			(stored.IndexedAnalysisUnit != nil &&
				stored.IndexedAnalysisUnit.SearchIndexPosture ==
					analysisunit.SearchIndexFocused) {
			continue
		}
		revisions := canonicalWholeRevisions(stored)
		manifest, before, snapshotErr := wholePublicationSnapshot(
			ctx, c.indexDir, stored.Name, revisions,
		)
		if snapshotErr != nil ||
			focusedindex.IsPublishing(c.indexDir, stored.Name) {
			continue
		}
		candidates[stored.Name] = wholeSharedCandidate{
			revisions: append(
				[]store.IndexedRevision(nil), revisions...,
			),
			manifestDigest: manifest.Digest,
			fingerprint:    before,
			memberCount:    len(manifest.Members),
		}
	}
	return candidates, nil
}

// bootstrapShared records only generations loaded synchronously during
// Searcher.Open. Runtime publications never replace this baseline: once its
// manifest identity changes, acquireIfStale uses the exact static reader for
// the rest of the process. This avoids trusting DirectorySearcher.List while
// its watcher may hold a mixed multi-shard generation.
func (c *wholeCache) bootstrapShared(
	ctx context.Context,
	shared zoekt.Streamer,
	st store.Store,
	candidates map[string]wholeSharedCandidate,
) error {
	names := make([]string, 0, len(candidates))
	for repository := range candidates {
		names = append(names, repository)
	}
	if len(names) == 0 {
		return nil
	}
	loaded, err := shared.List(
		ctx, query.NewRepoSet(names...), nil,
	)
	if err != nil {
		return err
	}
	inventory, inventoryOK := wholeRepoInventory(loaded)
	freshRepositories, err := st.ListRepos(ctx)
	if err != nil {
		return err
	}
	freshByName := make(map[string]store.Repo, len(freshRepositories))
	for _, repository := range freshRepositories {
		freshByName[repository.Name] = repository
	}
	for repository, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		revisions := candidate.revisions
		repo, repoErr := c.repo(repository)
		if repoErr != nil {
			return repoErr
		}
		manifest, after, snapshotErr := wholePublicationSnapshot(
			ctx, c.indexDir, repository, revisions,
		)
		if snapshotErr != nil ||
			manifest.Digest != candidate.manifestDigest ||
			!equalFocusedFingerprint(candidate.fingerprint, after) ||
			focusedindex.IsPublishing(c.indexDir, repository) {
			continue
		}
		if !inventoryOK ||
			!wholeRepoEntryMatches(
				inventory[repository],
				repository,
				revisions,
				candidate.memberCount,
			) {
			continue
		}
		fresh, freshOK := freshByName[repository]
		if !freshOK ||
			!wholeRepoIdentityMatches(fresh, revisions) {
			continue
		}
		repo.mu.Lock()
		if !repo.pruned {
			repo.shared = &wholeSharedBinding{
				revisions: append(
					[]store.IndexedRevision(nil), revisions...,
				),
				fingerprint: after,
				memberCount: candidate.memberCount,
			}
		}
		repo.mu.Unlock()
	}
	return nil
}

func (c *wholeCache) sharedControlBinding(
	ctx context.Context,
	repo *wholeRepoCache,
	repository string,
	revisions []store.IndexedRevision,
) *wholeSharedBinding {
	if err := ctx.Err(); err != nil {
		return nil
	}
	if focusedindex.IsPublishing(c.indexDir, repository) {
		return nil
	}
	repo.mu.Lock()
	binding := repo.shared
	pruned := repo.pruned
	repo.mu.Unlock()
	if pruned || binding == nil ||
		!slices.Equal(binding.revisions, revisions) ||
		len(binding.fingerprint) == 0 {
		return nil
	}
	// Compile performs the one complete pre-admission identity proof for both
	// positive and negative bindings. Stream event fences can therefore remain
	// O(1) receipt checks while the final barrier catches post-compile drift.
	expected := binding.fingerprint
	current, err := focusedFingerprintKnownNames(ctx, c.indexDir, expected)
	if err != nil ||
		!equalFocusedFingerprint(expected, current) {
		return nil
	}
	return binding
}

func (c *wholeCache) ensureSharedValidated(
	ctx context.Context,
	repo *wholeRepoCache,
	repository string,
	revisions []store.IndexedRevision,
	binding *wholeSharedBinding,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repo.mu.Lock()
	if repo.pruned || repo.shared != binding {
		repo.mu.Unlock()
		return errWholeSharedBaselineChanged
	}
	if binding.validated {
		repo.mu.Unlock()
		return nil
	}
	if repo.validation != nil {
		validation := repo.validation
		repo.mu.Unlock()
		return c.waitForSharedValidation(
			ctx, repo, validation, binding,
		)
	}
	if binding.lastError != nil &&
		time.Now().Before(binding.retryAt) {
		err := binding.lastError
		repo.mu.Unlock()
		return err
	}
	loadCtx, cancel := context.WithTimeout(
		c.closeCtx, WholeGenerationWarmingTimeout,
	)
	validation := &wholeSharedValidation{
		ready:   make(chan struct{}),
		binding: binding,
		ctx:     loadCtx,
		cancel:  cancel,
	}
	repo.validation = validation
	go c.validateShared(
		repo, validation, repository, revisions,
	)
	repo.mu.Unlock()
	return c.waitForSharedValidation(
		ctx, repo, validation, binding,
	)
}

func (c *wholeCache) waitForSharedValidation(
	ctx context.Context,
	repo *wholeRepoCache,
	validation *wholeSharedValidation,
	binding *wholeSharedBinding,
) error {
	select {
	case <-validation.ready:
	case <-ctx.Done():
	case <-c.closeCtx.Done():
	}
	requestErr := ctx.Err()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	select {
	case <-validation.ready:
		if validation.err != nil {
			return validation.err
		}
		if repo.pruned || repo.shared != binding || !binding.validated {
			return errWholeSharedBaselineChanged
		}
		if errors.Is(requestErr, context.DeadlineExceeded) {
			return errors.Join(ErrWholeGenerationWarming, requestErr)
		}
		return requestErr
	default:
	}
	if cacheErr := validation.ctx.Err(); cacheErr != nil {
		return cacheErr
	}
	if requestErr != nil {
		if errors.Is(requestErr, context.DeadlineExceeded) &&
			!repo.pruned && repo.shared == binding &&
			repo.validation == validation {
			return errors.Join(ErrWholeGenerationWarming, requestErr)
		}
		return requestErr
	}
	return errors.New("whole-repository search cache is closed")
}

func (c *wholeCache) validateShared(
	repo *wholeRepoCache,
	validation *wholeSharedValidation,
	repository string,
	revisions []store.IndexedRevision,
) {
	defer validation.cancel()
	var err error
	select {
	case c.loadSlots <- struct{}{}:
		manifest, validateErr := validateWholeSearchPublication(
			validation.ctx, c.indexDir, repository, revisions,
		)
		err = validateErr
		if err == nil {
			afterManifest, after, snapshotErr :=
				wholePublicationSnapshot(
					validation.ctx,
					c.indexDir,
					repository,
					revisions,
				)
			if snapshotErr != nil {
				err = snapshotErr
			} else if manifest.Digest != afterManifest.Digest ||
				!equalFocusedFingerprint(
					validation.binding.fingerprint, after,
				) ||
				focusedindex.IsPublishing(c.indexDir, repository) {
				err = errWholeSharedBaselineChanged
			}
		}
		<-c.loadSlots
	case <-validation.ctx.Done():
		err = validation.ctx.Err()
	}

	repo.mu.Lock()
	if repo.shared == validation.binding && !repo.pruned {
		if err == nil {
			validation.binding.validated = true
			validation.binding.failures = 0
			validation.binding.retryAt = time.Time{}
			validation.binding.lastError = nil
		} else if !errors.Is(err, errWholeSharedBaselineChanged) &&
			!contextTermination(err) {
			validation.binding.failures = min(
				validation.binding.failures+1, 32,
			)
			validation.binding.retryAt = time.Now().Add(
				focusedNegativeRetryDelay(
					validation.binding.failures,
				),
			)
			validation.binding.lastError = err
		}
	}
	validation.err = err
	if repo.validation == validation {
		repo.validation = nil
	}
	close(validation.ready)
	repo.mu.Unlock()
}

func (c *wholeCache) sharedCurrent(
	ctx context.Context,
	repository string,
	revisions []store.IndexedRevision,
	full bool,
) bool {
	repo, err := c.repo(repository)
	if err != nil || ctx.Err() != nil {
		return false
	}
	repo.mu.Lock()
	binding := repo.shared
	pruned := repo.pruned
	validated := binding != nil && binding.validated
	repo.mu.Unlock()
	if pruned || binding == nil ||
		!slices.Equal(binding.revisions, revisions) ||
		!validated ||
		focusedindex.IsPublishing(c.indexDir, repository) {
		return false
	}
	expected := binding.fingerprint[:1]
	if full {
		expected = binding.fingerprint
	}
	current, err := focusedFingerprintKnownNames(
		ctx, c.indexDir, expected,
	)
	return err == nil && equalFocusedFingerprint(expected, current)
}

func (c *wholeCache) invalidateShared(
	repository string,
	revisions []store.IndexedRevision,
) {
	c.mu.Lock()
	repo := c.repos[repository]
	c.mu.Unlock()
	if repo == nil {
		return
	}
	repo.mu.Lock()
	if repo.shared != nil &&
		slices.Equal(repo.shared.revisions, revisions) {
		repo.shared = nil
		if repo.validation != nil {
			repo.validation.cancel()
		}
	}
	repo.mu.Unlock()
}

func (c *wholeCache) acquireExact(
	ctx context.Context,
	repo *wholeRepoCache,
	repository string,
	revisions []store.IndexedRevision,
) (*wholeLease, error) {
	return c.acquireExactAt(
		ctx, repo, repository, c.indexDir, "", revisions,
	)
}

func (c *wholeCache) acquireSelected(
	ctx context.Context,
	repository, directory, digest string,
	revisions []store.IndexedRevision,
) (*wholeLease, error) {
	repo, err := c.selectedRepo(repository)
	if err != nil {
		return nil, err
	}
	return c.acquireExactAt(
		ctx, repo, repository, directory, digest, revisions,
	)
}

func (c *wholeCache) acquireExactAt(
	ctx context.Context,
	repo *wholeRepoCache,
	repository, directory, digest string,
	revisions []store.IndexedRevision,
) (*wholeLease, error) {
	expected := append([]store.IndexedRevision(nil), revisions...)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		repo.mu.Lock()
		if repo.pruned {
			repo.mu.Unlock()
			return nil, nil
		}
		entry := repo.entry
		if entry != nil && !entry.retired &&
			entry.directory == directory &&
			(digest == "" || entry.searchDigest == digest) &&
			slices.Equal(entry.revisions, expected) {
			if entry.searcher != nil {
				entry.refs++
			}
			repo.mu.Unlock()
			expectedFingerprint := entry.fingerprint
			current, err := focusedFingerprintKnownNames(
				ctx, entry.directory, expectedFingerprint,
			)
			valid := err == nil &&
				equalFocusedFingerprint(expectedFingerprint, current) &&
				!focusedindex.IsPublishing(entry.directory, repository)
			repo.mu.Lock()
			valid = valid && !repo.pruned && repo.entry == entry &&
				!entry.retired && !c.isClosed()
			if valid && entry.searcher != nil {
				lease := &wholeLease{
					cache: c, repo: repo, entry: entry,
					repository: repository, searcher: entry.searcher,
				}
				repo.mu.Unlock()
				return lease, nil
			}
			if valid && entry.searcher == nil &&
				entry.lastError != nil &&
				time.Now().Before(entry.retryAt) {
				cachedErr := entry.lastError
				repo.mu.Unlock()
				return nil, cachedErr
			}
			if valid && entry.searcher == nil {
				repo.retryFailures = entry.failures
			} else if entry.searcher == nil {
				repo.retryFailures = 0
			}
			if entry.searcher != nil && entry.refs > 0 {
				entry.refs--
			}
			if repo.entry == entry {
				retireWholeEntry(entry)
				repo.entry = nil
			}
			repo.mu.Unlock()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		}
		if repo.load != nil {
			load := repo.load
			sameGeneration := load.directory == directory &&
				load.digest == digest && slices.Equal(load.revisions, expected)
			repo.mu.Unlock()
			lease, err := c.waitForLoadAt(
				ctx, repo, load, repository, directory, digest, expected,
			)
			if lease != nil || sameGeneration {
				return lease, err
			}
			continue
		}
		loadCtx, cancel := context.WithTimeout(
			c.closeCtx, WholeGenerationWarmingTimeout,
		)
		load := &wholeCacheLoad{
			ready: make(chan struct{}), directory: directory, digest: digest,
			revisions: append([]store.IndexedRevision(nil), expected...),
			ctx:       loadCtx,
			cancel:    cancel,
			failures:  repo.retryFailures,
		}
		repo.retryFailures = 0
		repo.load = load
		go c.fill(repo, load, repository, directory, digest, expected)
		repo.mu.Unlock()
		return c.waitForLoadAt(
			ctx, repo, load, repository, directory, digest, expected,
		)
	}
}

func (c *wholeCache) waitForLoad(
	ctx context.Context,
	repo *wholeRepoCache,
	load *wholeCacheLoad,
	repository string,
	revisions []store.IndexedRevision,
) (*wholeLease, error) {
	directory := load.directory
	if directory == "" {
		directory = c.indexDir
	}
	return c.waitForLoadAt(
		ctx, repo, load, repository, directory, load.digest, revisions,
	)
}

func (c *wholeCache) waitForLoadAt(
	ctx context.Context,
	repo *wholeRepoCache,
	load *wholeCacheLoad,
	repository, directory, digest string,
	revisions []store.IndexedRevision,
) (*wholeLease, error) {
	select {
	case <-load.ready:
	case <-ctx.Done():
	case <-c.closeCtx.Done():
	}
	requestErr := ctx.Err()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	select {
	case <-load.ready:
		if load.err != nil {
			return nil, load.err
		}
		if requestErr != nil {
			if errors.Is(requestErr, context.DeadlineExceeded) &&
				!repo.pruned && load.directory == directory && load.digest == digest &&
				slices.Equal(load.revisions, revisions) {
				return nil, errors.Join(ErrWholeGenerationWarming, requestErr)
			}
			return nil, requestErr
		}
		entry := load.entry
		if entry == nil || repo.pruned || repo.entry != entry ||
			entry.retired || entry.searcher == nil ||
			entry.directory != directory ||
			(digest != "" && entry.searchDigest != digest) ||
			!slices.Equal(entry.revisions, revisions) {
			return nil, nil
		}
		entry.refs++
		return &wholeLease{
			cache: c, repo: repo, entry: entry,
			repository: repository, searcher: entry.searcher,
		}, nil
	default:
	}
	if cacheErr := load.ctx.Err(); cacheErr != nil {
		return nil, cacheErr
	}
	if requestErr != nil {
		if errors.Is(requestErr, context.DeadlineExceeded) &&
			!repo.pruned && repo.load == load &&
			load.directory == directory && load.digest == digest &&
			slices.Equal(load.revisions, revisions) {
			return nil, errors.Join(ErrWholeGenerationWarming, requestErr)
		}
		return nil, requestErr
	}
	return nil, errors.New("whole-repository search cache is closed")
}

func (c *wholeCache) fill(
	repo *wholeRepoCache,
	load *wholeCacheLoad,
	repository, directory, digest string,
	revisions []store.IndexedRevision,
) {
	defer load.cancel()
	var (
		entry *wholeCacheEntry
		err   error
	)
	select {
	case c.loadSlots <- struct{}{}:
		entry, err = c.load(
			load.ctx, directory, repository, digest, revisions, load.failures,
		)
		<-c.loadSlots
	case <-load.ctx.Done():
		err = load.ctx.Err()
	}

	repo.mu.Lock()
	switch {
	case repo.pruned:
		closeWholeEntry(entry)
		err = nil
	case c.isClosed():
		closeWholeEntry(entry)
		err = errors.New("whole-repository search cache is closed")
	case entry != nil && (err == nil || entry.searcher == nil):
		retireWholeEntry(repo.entry)
		repo.entry = entry
		if entry.searcher != nil {
			load.entry = entry
		}
	default:
		closeWholeEntry(entry)
	}
	load.err = err
	repo.load = nil
	close(load.ready)
	repo.mu.Unlock()
}

func (c *wholeCache) load(
	ctx context.Context,
	directory, repository, digest string,
	revisions []store.IndexedRevision,
	priorFailures int,
) (*wholeCacheEntry, error) {
	beforeManifest, before, err := wholePublicationSnapshot(
		ctx, directory, repository,
		revisions,
	)
	if err != nil {
		return nil, err
	}
	if focusedindex.IsPublishing(directory, repository) {
		return nil, errors.New("whole-repository publication is in progress")
	}
	manifest, err := validateWholeSearchPublication(
		ctx, directory, repository, revisions,
	)
	if err != nil {
		if !contextTermination(err) {
			_, after, snapshotErr := wholePublicationSnapshot(
				ctx, directory, repository, revisions,
			)
			if snapshotErr == nil &&
				equalFocusedFingerprint(before, after) &&
				!focusedindex.IsPublishing(directory, repository) {
				failures := min(priorFailures+1, 32)
				return &wholeCacheEntry{
					directory: directory, searchDigest: digest,
					revisions: append(
						[]store.IndexedRevision(nil), revisions...,
					),
					fingerprint: after,
					failures:    failures,
					retryAt: time.Now().Add(
						focusedNegativeRetryDelay(failures),
					),
					lastError: err,
				}, err
			}
		}
		return nil, err
	}
	if manifest.Digest != beforeManifest.Digest {
		return nil, errors.New(
			"whole-repository manifest changed during validation",
		)
	}
	members := make([]staticShardDescriptor, 0, len(manifest.Members))
	for _, member := range manifest.Members {
		members = append(members, staticShardDescriptor{
			name: member.Name, contentDigest: member.ContentDigest,
		})
	}
	searcher, err := materializeStaticShards(
		ctx, directory, "whole-repository", repository, members,
	)
	if err != nil {
		return nil, err
	}
	if list, listErr := searcher.List(
		ctx, query.NewRepoSet(repository), nil,
	); listErr != nil ||
		!wholeRepoListMatches(
			list, repository, revisions, len(manifest.Members),
		) {
		searcher.Close()
		if listErr != nil {
			return nil, fmt.Errorf(
				"inspect bound whole-repository publication: %w", listErr,
			)
		}
		return nil, errors.New(
			"bound whole-repository publication metadata mismatch",
		)
	}
	afterManifest, after, err := wholePublicationSnapshot(
		ctx, directory, repository,
		revisions,
	)
	if err != nil || afterManifest.Digest != manifest.Digest ||
		!equalFocusedFingerprint(before, after) ||
		focusedindex.IsPublishing(directory, repository) {
		searcher.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New(
			"whole-repository publication changed during exact binding",
		)
	}
	searchDigest := ""
	searchManifestName := repositoryindex.SearchManifestName(repository)
	if digest != "" || slices.ContainsFunc(after, func(identity focusedArtifactIdentity) bool {
		return identity.name == searchManifestName
	}) {
		searchDigest, err = repositorySearchDigest(ctx, directory, repository)
		if err != nil {
			searcher.Close()
			return nil, err
		}
	}
	if digest != "" && searchDigest != digest {
		searcher.Close()
		return nil, errors.New("bound whole-repository search generation mismatch")
	}
	return &wholeCacheEntry{
		directory:    directory,
		revisions:    append([]store.IndexedRevision(nil), revisions...),
		fingerprint:  after,
		searchDigest: searchDigest,
		searcher:     searcher,
	}, nil
}

func repositorySearchDigest(ctx context.Context, indexDir, repository string) (string, error) {
	manifest, err := repositoryindex.ReadSearchManifestContext(ctx, indexDir, repository)
	if err != nil {
		return "", err
	}
	return manifest.Digest, nil
}

func wholePublicationSnapshot(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (focusedindex.WholeManifest, focusedFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return focusedindex.WholeManifest{}, nil, err
	}
	manifest, err := focusedindex.ReadWholeManifestContext(
		ctx, indexDir, repository, revisions,
	)
	if err != nil {
		return focusedindex.WholeManifest{}, nil, err
	}
	names := make([]string, 0, len(manifest.Members)+3)
	names = append(names, focusedindex.WholeManifestName(repository))
	for _, member := range manifest.Members {
		names = append(names, member.Name)
	}
	searchPath := filepath.Join(
		indexDir, repositoryindex.SearchManifestName(repository),
	)
	if _, statErr := os.Lstat(searchPath); statErr == nil {
		_, source, openErr := focusedindex.ReadRepositorySearchGenerationContext(
			ctx, indexDir, repository, revisions,
		)
		if openErr != nil {
			return focusedindex.WholeManifest{}, nil, openErr
		}
		names = append(
			names,
			repositoryindex.SearchManifestName(repository),
			repositoryindex.SourceManifestName(repository),
		)
		for _, member := range source.Members {
			names = append(names, member.Name)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return focusedindex.WholeManifest{}, nil, statErr
	}
	fingerprint := make(focusedFingerprint, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return focusedindex.WholeManifest{}, nil, err
		}
		path := filepath.Join(indexDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return focusedindex.WholeManifest{}, nil, err
		}
		if !info.Mode().IsRegular() {
			return focusedindex.WholeManifest{}, nil, fmt.Errorf(
				"whole-repository member %q is not regular",
				filepath.Base(path),
			)
		}
		fingerprint = append(fingerprint, focusedArtifactIdentity{
			name:       filepath.Base(path),
			info:       info,
			changeTime: fileInfoChangeTime(info),
		})
	}
	return manifest, fingerprint, nil
}

// validateWholeSearchPublication is the dual-read/single-write transition
// boundary. A repository with no v2 search root may still use its exact legacy
// whole receipt. Once a v2 root exists, any malformed or incomplete v2 byte is
// an error and never falls back to the legacy receipt beside it.
func validateWholeSearchPublication(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (focusedindex.WholeManifest, error) {
	searchPath := filepath.Join(
		indexDir, repositoryindex.SearchManifestName(repository),
	)
	if _, err := os.Lstat(searchPath); err == nil {
		if _, err := focusedindex.ValidateRepositorySearchGeneration(
			ctx, indexDir, repository, revisions,
		); err != nil {
			return focusedindex.WholeManifest{}, err
		}
		return focusedindex.ReadWholeManifestContext(ctx, indexDir, repository, revisions)
	} else if !errors.Is(err, os.ErrNotExist) {
		return focusedindex.WholeManifest{}, err
	}
	return focusedindex.ValidateWholePublishedContext(
		ctx, indexDir, repository, revisions,
	)
}

func wholeRepoListMatches(
	list *zoekt.RepoList,
	repository string,
	revisions []store.IndexedRevision,
	expectedMembers int,
) bool {
	inventory, ok := wholeRepoInventory(list)
	if !ok {
		return false
	}
	return wholeRepoEntryMatches(
		inventory[repository],
		repository,
		revisions,
		expectedMembers,
	)
}

func wholeRepoInventory(
	list *zoekt.RepoList,
) (map[string]*zoekt.RepoListEntry, bool) {
	if list == nil || list.Crashes != 0 {
		return nil, false
	}
	inventory := make(
		map[string]*zoekt.RepoListEntry, len(list.Repos),
	)
	for _, entry := range list.Repos {
		if entry == nil || entry.Repository.Name == "" {
			return nil, false
		}
		if _, exists := inventory[entry.Repository.Name]; exists {
			return nil, false
		}
		inventory[entry.Repository.Name] = entry
	}
	return inventory, true
}

func wholeRepoEntryMatches(
	entry *zoekt.RepoListEntry,
	repository string,
	revisions []store.IndexedRevision,
	expectedMembers int,
) bool {
	if entry == nil || entry.Repository.Name != repository {
		return false
	}
	expected := make([]zoekt.RepositoryBranch, 0, len(revisions))
	for _, revision := range revisions {
		expected = append(expected, zoekt.RepositoryBranch{
			Name: revision.Branch, Version: revision.Commit,
		})
	}
	return slices.Equal(entry.Repository.Branches, expected) &&
		strings.TrimSpace(
			entry.Repository.Metadata["phebs.analysis_unit"],
		) == "" &&
		(expectedMembers <= 0 ||
			entry.Stats.Shards == expectedMembers)
}

func wholeRepoIdentityMatches(
	repo store.Repo,
	revisions []store.IndexedRevision,
) bool {
	return !repo.Deleting && repo.IndexedCommitHash != "" &&
		(repo.IndexedAnalysisUnit == nil ||
			repo.IndexedAnalysisUnit.SearchIndexPosture !=
				analysisunit.SearchIndexFocused) &&
		slices.Equal(canonicalWholeRevisions(repo), revisions)
}

func canonicalWholeRevisions(repo store.Repo) []store.IndexedRevision {
	if len(repo.IndexedRevisions) == 0 && repo.IndexedCommitHash != "" {
		return []store.IndexedRevision{{
			Selector: "HEAD",
			Branch:   "HEAD",
			Commit:   repo.IndexedCommitHash,
		}}
	}
	return append([]store.IndexedRevision(nil), repo.IndexedRevisions...)
}

func (l *wholeLease) current(
	ctx context.Context,
	repo store.Repo,
	full bool,
) bool {
	if l == nil || ctx.Err() != nil ||
		!wholeRepoIdentityMatches(repo, l.entry.revisions) ||
		focusedindex.IsPublishing(l.entry.directory, l.repository) {
		return false
	}
	l.repo.mu.Lock()
	active := !l.cache.isClosed() && !l.repo.pruned &&
		l.repo.entry == l.entry && !l.entry.retired
	l.repo.mu.Unlock()
	if !active || !full {
		if !active {
			return false
		}
		current, err := focusedFingerprintKnownNames(
			ctx, l.entry.directory, l.entry.fingerprint[:1],
		)
		return err == nil && equalFocusedFingerprint(
			l.entry.fingerprint[:1], current,
		)
	}
	current, err := focusedFingerprintKnownNames(
		ctx, l.entry.directory, l.entry.fingerprint,
	)
	return err == nil &&
		equalFocusedFingerprint(l.entry.fingerprint, current)
}

func (l *wholeLease) selectedCurrent(ctx context.Context, full bool) bool {
	if l == nil || ctx.Err() != nil || l.entry.directory == "" ||
		focusedindex.IsPublishing(l.entry.directory, l.repository) {
		return false
	}
	l.repo.mu.Lock()
	active := !l.cache.isClosed() && !l.repo.pruned &&
		l.repo.entry == l.entry && !l.entry.retired
	l.repo.mu.Unlock()
	if !active {
		return false
	}
	expected := l.entry.fingerprint[:1]
	if full {
		expected = l.entry.fingerprint
	}
	current, err := focusedFingerprintKnownNames(ctx, l.entry.directory, expected)
	return err == nil && equalFocusedFingerprint(expected, current)
}

func (l *wholeLease) release() {
	if l == nil {
		return
	}
	l.repo.mu.Lock()
	if l.entry.refs > 0 {
		l.entry.refs--
	}
	if l.entry.retired && l.entry.refs == 0 {
		closeWholeEntry(l.entry)
	}
	l.repo.mu.Unlock()
}

func (l *wholeLease) invalidate() {
	if l == nil {
		return
	}
	l.repo.mu.Lock()
	if l.repo.entry == l.entry {
		retireWholeEntry(l.entry)
		l.repo.entry = nil
	}
	l.repo.mu.Unlock()
}

func (l *wholeLease) matchesSearchDigest(digest string) bool {
	if l == nil || digest == "" {
		return false
	}
	l.repo.mu.Lock()
	defer l.repo.mu.Unlock()
	return !l.entry.retired && l.entry.searcher != nil &&
		l.entry.searchDigest == digest
}

func retireWholeEntry(entry *wholeCacheEntry) {
	if entry == nil {
		return
	}
	entry.retired = true
	if entry.refs == 0 {
		closeWholeEntry(entry)
	}
}

func closeWholeEntry(entry *wholeCacheEntry) {
	if entry != nil && entry.searcher != nil {
		entry.searcher.Close()
		entry.searcher = nil
	}
}

func (c *wholeCache) repo(repository string) (*wholeRepoCache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("whole-repository search cache is closed")
	}
	repo := c.repos[repository]
	if repo == nil {
		repo = &wholeRepoCache{}
		c.repos[repository] = repo
	}
	return repo, nil
}

func (c *wholeCache) selectedRepo(repository string) (*wholeRepoCache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("whole-repository search cache is closed")
	}
	repo := c.selected[repository]
	if repo == nil {
		repo = &wholeRepoCache{}
		c.selected[repository] = repo
	}
	return repo, nil
}

func (c *wholeCache) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *wholeCache) prune(keep map[string]struct{}) {
	type candidate struct {
		name     string
		repo     *wholeRepoCache
		selected bool
	}
	c.mu.Lock()
	var candidates []candidate
	for name, repo := range c.repos {
		if _, ok := keep[name]; !ok {
			candidates = append(candidates, candidate{name: name, repo: repo})
		}
	}
	for name, repo := range c.selected {
		if _, ok := keep[name]; !ok {
			candidates = append(candidates, candidate{
				name: name, repo: repo, selected: true,
			})
		}
	}
	c.mu.Unlock()
	for _, candidate := range candidates {
		candidate.repo.mu.Lock()
		c.mu.Lock()
		current := c.repos[candidate.name]
		if candidate.selected {
			current = c.selected[candidate.name]
		}
		if current != candidate.repo {
			c.mu.Unlock()
			candidate.repo.mu.Unlock()
			continue
		}
		candidate.repo.pruned = true
		if candidate.selected {
			delete(c.selected, candidate.name)
		} else {
			delete(c.repos, candidate.name)
		}
		if candidate.repo.load != nil {
			candidate.repo.load.cancel()
		}
		if candidate.repo.validation != nil {
			candidate.repo.validation.cancel()
		}
		c.mu.Unlock()
		retireWholeEntry(candidate.repo.entry)
		candidate.repo.entry = nil
		candidate.repo.shared = nil
		candidate.repo.mu.Unlock()
	}
}

func (c *wholeCache) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.cancel()
	repos := make([]*wholeRepoCache, 0, len(c.repos))
	for _, repo := range c.repos {
		repos = append(repos, repo)
	}
	for _, repo := range c.selected {
		repos = append(repos, repo)
	}
	c.mu.Unlock()
	for _, repo := range repos {
		for {
			repo.mu.Lock()
			if repo.validation != nil {
				ready := repo.validation.ready
				repo.validation.cancel()
				repo.mu.Unlock()
				<-ready
				continue
			}
			if repo.load != nil {
				ready := repo.load.ready
				repo.mu.Unlock()
				<-ready
				continue
			}
			retireWholeEntry(repo.entry)
			repo.entry = nil
			repo.shared = nil
			repo.mu.Unlock()
			break
		}
	}
}
