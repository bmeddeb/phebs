package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	"github.com/sourcegraph/zoekt/query"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
)

type focusedValidator func(
	context.Context,
	string,
	string,
	*analysisunit.State,
	[]store.IndexedRevision,
) (focusedindex.Manifest, error)

type focusedMaterializer func(
	context.Context,
	string,
	focusedindex.Manifest,
) (zoekt.Streamer, func(), error)

// focusedCache binds a validated focused publication to a searcher loaded
// from exactly the manifest members. The ordinary DirectorySearcher remains
// useful for whole-repository shards, but its asynchronous watcher is never
// trusted for focused visibility.
type focusedCache struct {
	indexDir    string
	validate    focusedValidator
	materialize focusedMaterializer

	mu        sync.Mutex
	repos     map[string]*focusedRepoCache
	closed    bool
	closeCtx  context.Context
	cancel    context.CancelFunc
	loadSlots chan struct{}
}

const (
	maxConcurrentFocusedLoads = 2
	focusedCacheLoadTimeout   = 10 * time.Minute
)

// focusedRepoCache is a context-selectable singleflight slot. Its mutex is
// held only while publishing entry/refcount state, never while reading,
// hashing, parsing, or opening a shard. A cold repository therefore cannot
// block a cache hit or cold load for another repository.
type focusedRepoCache struct {
	mu      sync.Mutex
	entry   *focusedCacheEntry
	loading bool
	load    *focusedCacheLoad
	pruned  bool
}

type focusedCacheLoad struct {
	ready  chan struct{}
	entry  *focusedCacheEntry
	err    error
	ctx    context.Context
	cancel context.CancelFunc
}

type focusedCacheEntry struct {
	state     *analysisunit.State
	revisions []store.IndexedRevision
	// manifestDigest and fingerprint form the cached validation key. The
	// manifest bytes need not be parsed again while their file identity and
	// every declared member/sidecar identity remain unchanged.
	manifestDigest string
	fingerprint    focusedFingerprint
	searcher       zoekt.Streamer
	cleanup        func()
	refs           int
	retired        bool
}

type focusedFingerprint []focusedArtifactIdentity

type focusedArtifactIdentity struct {
	name       string
	info       os.FileInfo
	changeTime fileChangeTime
}

type fileChangeTime struct {
	seconds     int64
	nanoseconds int64
	available   bool
}

type focusedLease struct {
	cache      *focusedCache
	repo       *focusedRepoCache
	entry      *focusedCacheEntry
	repository string
	searcher   zoekt.Streamer
}

func newFocusedCache(indexDir string) *focusedCache {
	closeCtx, cancel := context.WithCancel(context.Background())
	return &focusedCache{
		indexDir:    indexDir,
		validate:    focusedindex.ValidatePublishedContext,
		materialize: materializeFocused,
		repos:       make(map[string]*focusedRepoCache),
		closeCtx:    closeCtx,
		cancel:      cancel,
		loadSlots:   make(chan struct{}, maxConcurrentFocusedLoads),
	}
}

// acquire returns a reference-counted exact-generation searcher. Validation
// failures are cached against the expected committed identity and the
// repository-local filesystem identity, so a corrupt publication remains
// fail-closed without being re-hashed on every query.
func (c *focusedCache) acquire(
	ctx context.Context,
	repository string,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
) (*focusedLease, error) {
	repo, err := c.repo(repository)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expectedState := analysisunit.CloneState(state)
	expectedRevisions := append([]store.IndexedRevision(nil), revisions...)
	lease, err := c.positiveLease(
		ctx,
		repo,
		repository,
		expectedState,
		expectedRevisions,
	)
	if lease != nil || err != nil {
		return lease, err
	}
	repo.mu.Lock()
	if repo.pruned {
		repo.mu.Unlock()
		return nil, nil
	}
	// Only the caller that starts a cold fill waits for it. Later compiles
	// fail closed for this repository instead of serially spending their
	// query-wide budget behind unrelated in-flight cold repositories.
	if repo.loading {
		repo.mu.Unlock()
		return nil, nil
	}
	select {
	case c.loadSlots <- struct{}{}:
	default:
		repo.mu.Unlock()
		return nil, nil
	}
	loadCtx, cancel := context.WithTimeout(
		c.closeCtx, focusedCacheLoadTimeout,
	)
	load := &focusedCacheLoad{
		ready: make(chan struct{}), ctx: loadCtx, cancel: cancel,
	}
	repo.load = load
	repo.loading = true
	current := repo.entry
	go c.fill(
		repo,
		load,
		repository,
		expectedState,
		expectedRevisions,
		current,
	)
	repo.mu.Unlock()
	return c.waitForLoad(
		ctx,
		repo,
		load,
		repository,
		expectedState,
		expectedRevisions,
	)
}

// positiveLease rechecks an already materialized exact binding without using
// a cold-fill slot. Two slow cold repositories therefore cannot suppress
// every warmed focused repository on the instance.
func (c *focusedCache) positiveLease(
	ctx context.Context,
	repo *focusedRepoCache,
	repository string,
	expectedState *analysisunit.State,
	expectedRevisions []store.IndexedRevision,
) (*focusedLease, error) {
	repo.mu.Lock()
	entry := repo.entry
	if repo.pruned || repo.loading || entry == nil ||
		entry.retired || entry.searcher == nil ||
		!analysisunit.EqualState(entry.state, expectedState) ||
		!slices.Equal(entry.revisions, expectedRevisions) {
		repo.mu.Unlock()
		return nil, nil
	}
	// Pin the mmap while targeted identity checks run without the slot mutex.
	entry.refs++
	repo.mu.Unlock()

	fingerprint, fingerprintErr := focusedFingerprintKnownNames(
		ctx, c.indexDir, entry.fingerprint,
	)
	valid := fingerprintErr == nil &&
		equalFocusedFingerprint(entry.fingerprint, fingerprint) &&
		!focusedindex.IsPublishing(c.indexDir, repository)

	repo.mu.Lock()
	select {
	case <-c.closeCtx.Done():
		valid = false
	default:
	}
	valid = valid && ctx.Err() == nil && !repo.pruned &&
		repo.entry == entry && !entry.retired
	if valid {
		lease := &focusedLease{
			cache: c, repo: repo, entry: entry,
			repository: repository, searcher: entry.searcher,
		}
		repo.mu.Unlock()
		return lease, nil
	}
	if entry.refs > 0 {
		entry.refs--
	}
	if entry.retired && entry.refs == 0 {
		closeFocusedEntry(entry)
	}
	repo.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (c *focusedCache) waitForLoad(
	ctx context.Context,
	repo *focusedRepoCache,
	load *focusedCacheLoad,
	repository string,
	expectedState *analysisunit.State,
	expectedRevisions []store.IndexedRevision,
) (*focusedLease, error) {
	select {
	case <-load.ready:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		repo.mu.Lock()
		defer repo.mu.Unlock()
		if load.err != nil {
			return nil, load.err
		}
		entry := load.entry
		if entry == nil || repo.pruned || repo.entry != entry ||
			entry.retired || entry.searcher == nil ||
			!analysisunit.EqualState(entry.state, expectedState) ||
			!slices.Equal(entry.revisions, expectedRevisions) {
			return nil, nil
		}
		entry.refs++
		return &focusedLease{
			cache: c, repo: repo, entry: entry,
			repository: repository, searcher: entry.searcher,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closeCtx.Done():
		return nil, errors.New("focused search cache is closed")
	}
}

func (c *focusedCache) fill(
	repo *focusedRepoCache,
	load *focusedCacheLoad,
	repository string,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
	current *focusedCacheEntry,
) {
	defer load.cancel()
	defer func() { <-c.loadSlots }()
	var (
		replacement *focusedCacheEntry
		useCurrent  bool
		loadErr     error
	)
	replacement, useCurrent, loadErr = c.load(
		load.ctx, repository, state, revisions, current,
	)

	repo.mu.Lock()
	closed := c.isClosed()
	switch {
	case repo.pruned:
		closeFocusedEntry(replacement)
		if repo.entry == current {
			retireFocusedEntry(repo.entry)
			repo.entry = nil
		}
		c.mu.Lock()
		if c.repos[repository] == repo {
			delete(c.repos, repository)
		}
		c.mu.Unlock()
		loadErr = nil
	case closed:
		closeFocusedEntry(replacement)
		loadErr = errors.New("focused search cache is closed")
	case useCurrent && repo.entry == current && current != nil:
		load.entry = current
	case replacement != nil && loadErr == nil:
		retireFocusedEntry(repo.entry)
		repo.entry = replacement
		if replacement.searcher != nil {
			load.entry = replacement
		}
	default:
		closeFocusedEntry(replacement)
	}
	load.err = loadErr
	repo.loading = false
	close(load.ready)
	repo.mu.Unlock()
}

// prune removes cache slots for repositories that no longer have focused
// posture in the database. Active leases keep their immutable entry alive
// until release; a concurrent cold load is marked pruned and performs the
// same deferred retirement when it rejoins its slot.
func (c *focusedCache) prune(keep map[string]struct{}) {
	type candidate struct {
		name string
		repo *focusedRepoCache
	}
	c.mu.Lock()
	candidates := make([]candidate, 0, len(c.repos))
	for name, repo := range c.repos {
		if _, ok := keep[name]; !ok {
			candidates = append(candidates, candidate{name: name, repo: repo})
		}
	}
	c.mu.Unlock()

	for _, candidate := range candidates {
		candidate.repo.mu.Lock()
		c.mu.Lock()
		if current := c.repos[candidate.name]; current != candidate.repo {
			c.mu.Unlock()
			candidate.repo.mu.Unlock()
			continue
		}
		if _, ok := keep[candidate.name]; ok {
			c.mu.Unlock()
			candidate.repo.mu.Unlock()
			continue
		}
		candidate.repo.pruned = true
		if !candidate.repo.loading {
			delete(c.repos, candidate.name)
		} else {
			candidate.repo.load.cancel()
		}
		c.mu.Unlock()
		if !candidate.repo.loading {
			retireFocusedEntry(candidate.repo.entry)
			candidate.repo.entry = nil
		}
		candidate.repo.mu.Unlock()
	}
}

// load checks a cache entry or builds its replacement without holding any
// cache mutex. A validation failure produces a negative entry keyed by the
// stable repository-local fingerprint; cancellation and transient I/O errors
// are never cached.
func (c *focusedCache) load(
	ctx context.Context,
	repository string,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
	current *focusedCacheEntry,
) (*focusedCacheEntry, bool, error) {
	if current != nil &&
		analysisunit.EqualState(current.state, state) &&
		slices.Equal(current.revisions, revisions) {
		var (
			fingerprint focusedFingerprint
			err         error
		)
		if current.searcher == nil {
			fingerprint, err = focusedPublicationFingerprint(
				ctx, c.indexDir, repository,
			)
		} else {
			// The immutable static reader cannot ingest an undeclared shard.
			// Positive hits therefore check only the artifacts already bound
			// to it, avoiding one shared-directory ReadDir per focused repo.
			fingerprint, err = focusedFingerprintKnownNames(
				ctx, c.indexDir, current.fingerprint,
			)
		}
		if err == nil &&
			equalFocusedFingerprint(current.fingerprint, fingerprint) &&
			(current.searcher == nil || current.manifestDigest != "") &&
			!focusedindex.IsPublishing(c.indexDir, repository) {
			return nil, current.searcher != nil, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
	}

	fingerprint, err := focusedPublicationFingerprint(
		ctx, c.indexDir, repository,
	)
	if err != nil {
		return nil, false, err
	}
	entry := &focusedCacheEntry{
		state:       analysisunit.CloneState(state),
		revisions:   append([]store.IndexedRevision(nil), revisions...),
		fingerprint: fingerprint,
	}

	manifest, validateErr := c.validate(
		ctx, c.indexDir, repository, state, revisions,
	)
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if contextTermination(validateErr) {
		return nil, false, validateErr
	}
	afterValidation, fingerprintErr := focusedPublicationFingerprint(
		ctx, c.indexDir, repository,
	)
	if fingerprintErr != nil {
		return nil, false, fingerprintErr
	}
	if !equalFocusedFingerprint(fingerprint, afterValidation) {
		return nil, false, errors.New(
			"focused publication changed during validation",
		)
	}
	if validateErr != nil {
		return entry, false, nil
	}

	searcher, cleanup, materializeErr := c.materialize(
		ctx, c.indexDir, manifest,
	)
	if materializeErr != nil {
		return nil, false, materializeErr
	}
	afterMaterialize, fingerprintErr := focusedPublicationFingerprint(
		ctx, c.indexDir, repository,
	)
	if fingerprintErr != nil ||
		!sameFocusedBinding(afterValidation, afterMaterialize, manifest) ||
		focusedindex.IsPublishing(c.indexDir, repository) {
		searcher.Close()
		cleanup()
		if fingerprintErr != nil {
			return nil, false, fingerprintErr
		}
		return nil, false, errors.New(
			"focused publication changed during search binding",
		)
	}

	entry.manifestDigest = manifest.Digest
	entry.fingerprint = afterMaterialize
	entry.searcher = searcher
	entry.cleanup = cleanup
	return entry, false, nil
}

// active rechecks the O(1) state that can retire a lease between streamed
// member batches. Artifact identity is checked once immediately before the
// backend starts; repeating the full 2M+1-artifact fingerprint for M member
// batches would make focused streaming O(M²) in filesystem operations.
func (l *focusedLease) active(repo store.Repo) bool {
	if l == nil || !focusedRepoIdentityMatches(
		repo, l.entry.state, l.entry.revisions,
	) {
		return false
	}
	l.repo.mu.Lock()
	active := !l.cache.isClosed() && l.repo.entry == l.entry &&
		!l.entry.retired
	l.repo.mu.Unlock()
	return active &&
		!focusedindex.IsPublishing(l.cache.indexDir, l.repository)
}

// current rechecks the committed database identity and every artifact in the
// immutable active binding. It performs targeted Lstats only: undeclared new
// files cannot enter the static searcher and are prefix-inventoried on the
// next cache admission.
func (l *focusedLease) current(ctx context.Context, repo store.Repo) bool {
	if ctx.Err() != nil || !l.active(repo) {
		return false
	}
	fingerprint, err := focusedFingerprintKnownNames(
		ctx, l.cache.indexDir, l.entry.fingerprint,
	)
	if err != nil {
		return false
	}
	return l.active(repo) &&
		equalFocusedFingerprint(l.entry.fingerprint, fingerprint)
}

func (l *focusedLease) release() {
	if l == nil {
		return
	}
	l.repo.mu.Lock()
	defer l.repo.mu.Unlock()
	if l.entry.refs > 0 {
		l.entry.refs--
	}
	if l.entry.retired && l.entry.refs == 0 {
		closeFocusedEntry(l.entry)
	}
}

// invalidate retires this exact binding without disturbing a replacement
// already installed in the same repository slot. If a cold load is using the
// entry, it marks the slot pruned and lets that load perform retirement when
// it rejoins, avoiding closing an mmap while the loader inspects it.
func (l *focusedLease) invalidate() {
	if l == nil {
		return
	}
	l.repo.mu.Lock()
	defer l.repo.mu.Unlock()
	if l.repo.entry != l.entry {
		return
	}
	l.repo.pruned = true
	l.cache.mu.Lock()
	if !l.repo.loading && l.cache.repos[l.repository] == l.repo {
		delete(l.cache.repos, l.repository)
	}
	l.cache.mu.Unlock()
	if l.repo.loading {
		l.repo.load.cancel()
	} else {
		retireFocusedEntry(l.repo.entry)
		l.repo.entry = nil
	}
}

func retireFocusedEntry(entry *focusedCacheEntry) {
	if entry == nil {
		return
	}
	entry.retired = true
	if entry.refs == 0 {
		closeFocusedEntry(entry)
	}
}

func (c *focusedCache) repo(repository string) (*focusedRepoCache, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("focused search cache is closed")
	}
	repo := c.repos[repository]
	if repo == nil {
		repo = &focusedRepoCache{}
		c.repos[repository] = repo
	}
	return repo, nil
}

func (c *focusedCache) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *focusedCache) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.cancel()
	repos := make([]*focusedRepoCache, 0, len(c.repos))
	for _, repo := range c.repos {
		repos = append(repos, repo)
	}
	c.mu.Unlock()
	for _, repo := range repos {
		for {
			repo.mu.Lock()
			if repo.loading {
				ready := repo.load.ready
				repo.mu.Unlock()
				<-ready
				continue
			}
			retireFocusedEntry(repo.entry)
			repo.entry = nil
			repo.mu.Unlock()
			break
		}
	}
}

func closeFocusedEntry(entry *focusedCacheEntry) {
	if entry == nil {
		return
	}
	if entry.searcher != nil {
		entry.searcher.Close()
		entry.searcher = nil
	}
	if entry.cleanup != nil {
		entry.cleanup()
		entry.cleanup = nil
	}
}

func focusedRepoIdentityMatches(
	repo store.Repo,
	state *analysisunit.State,
	revisions []store.IndexedRevision,
) bool {
	return !repo.Deleting && repo.IndexedCommitHash != "" &&
		analysisunit.EqualState(repo.IndexedAnalysisUnit, state) &&
		slices.Equal(repo.IndexedRevisions, revisions)
}

func focusedPublicationFingerprint(
	ctx context.Context,
	indexDir, repository string,
) (focusedFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return nil, err
	}
	prefix := focusedindex.ArtifactBase(repository)
	fingerprint := make(focusedFingerprint, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := os.Lstat(filepath.Join(indexDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		fingerprint = append(fingerprint, focusedArtifactIdentity{
			name:       entry.Name(),
			info:       info,
			changeTime: fileInfoChangeTime(info),
		})
	}
	sort.Slice(fingerprint, func(i, j int) bool {
		return fingerprint[i].name < fingerprint[j].name
	})
	return fingerprint, nil
}

func focusedFingerprintKnownNames(
	ctx context.Context,
	indexDir string,
	expected focusedFingerprint,
) (focusedFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fingerprint := make(focusedFingerprint, 0, len(expected))
	for _, artifact := range expected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Lstat(filepath.Join(indexDir, artifact.name))
		if err != nil {
			return nil, err
		}
		fingerprint = append(fingerprint, focusedArtifactIdentity{
			name:       artifact.name,
			info:       info,
			changeTime: fileInfoChangeTime(info),
		})
	}
	return fingerprint, nil
}

func equalFocusedFingerprint(left, right focusedFingerprint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a.name != b.name ||
			a.info.Mode() != b.info.Mode() ||
			a.info.Size() != b.info.Size() ||
			!a.info.ModTime().Equal(b.info.ModTime()) ||
			!os.SameFile(a.info, b.info) ||
			a.changeTime != b.changeTime {
			return false
		}
	}
	return true
}

// The static mmap binding creates no hard links and therefore permits no
// artifact identity change between validation and opening.
func sameFocusedBinding(
	left, right focusedFingerprint,
	_ focusedindex.Manifest,
) bool {
	return equalFocusedFingerprint(left, right)
}

func fileInfoChangeTime(info os.FileInfo) fileChangeTime {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return fileChangeTime{}
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return fileChangeTime{}
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return fileChangeTime{}
	}
	for _, name := range []string{"Ctim", "Ctimespec"} {
		field := value.FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.Struct {
			seconds := field.FieldByName("Sec")
			nanoseconds := field.FieldByName("Nsec")
			if seconds.IsValid() && nanoseconds.IsValid() &&
				seconds.CanInt() && nanoseconds.CanInt() {
				return fileChangeTime{
					seconds: seconds.Int(), nanoseconds: nanoseconds.Int(),
					available: true,
				}
			}
		}
	}
	seconds := value.FieldByName("Ctime")
	if !seconds.IsValid() || !seconds.CanInt() {
		return fileChangeTime{}
	}
	result := fileChangeTime{seconds: seconds.Int(), available: true}
	for _, name := range []string{"Ctimensec", "CtimeNsec"} {
		nanoseconds := value.FieldByName(name)
		if nanoseconds.IsValid() && nanoseconds.CanInt() {
			result.nanoseconds = nanoseconds.Int()
			break
		}
	}
	return result
}

func materializeFocused(
	ctx context.Context,
	indexDir string,
	manifest focusedindex.Manifest,
) (zoekt.Streamer, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	cleanup := func() {}
	shards := make([]staticFocusedShard, 0, len(manifest.Members))
	closeShards := func() {
		for _, shard := range shards {
			shard.searcher.Close()
		}
		shards = nil
	}
	for _, member := range manifest.Members {
		if err := ctx.Err(); err != nil {
			closeShards()
			return nil, nil, err
		}
		source := filepath.Join(indexDir, member.Name)
		sourceBefore, err := os.Lstat(source)
		if err != nil {
			closeShards()
			return nil, nil, fmt.Errorf(
				"inspect focused member %q before binding: %w",
				member.Name, err,
			)
		}
		if !sourceBefore.Mode().IsRegular() {
			closeShards()
			return nil, nil, fmt.Errorf(
				"focused member %q is not regular before binding",
				member.Name,
			)
		}
		file, err := os.Open(source)
		if err != nil {
			closeShards()
			return nil, nil, fmt.Errorf(
				"open focused member %q: %w", member.Name, err,
			)
		}
		opened, statErr := file.Stat()
		current, lstatErr := os.Lstat(source)
		if statErr != nil || lstatErr != nil ||
			!opened.Mode().IsRegular() ||
			!current.Mode().IsRegular() ||
			!os.SameFile(sourceBefore, opened) ||
			!os.SameFile(opened, current) ||
			opened.Size() != sourceBefore.Size() ||
			current.Size() != sourceBefore.Size() ||
			!opened.ModTime().Equal(sourceBefore.ModTime()) ||
			!current.ModTime().Equal(sourceBefore.ModTime()) {
			_ = file.Close()
			closeShards()
			return nil, nil, fmt.Errorf(
				"focused member %q changed before mmap", member.Name,
			)
		}
		indexFile, err := index.NewIndexFile(file)
		if err != nil {
			closeShards()
			return nil, nil, fmt.Errorf(
				"mmap focused member %q: %w", member.Name, err,
			)
		}
		digest, err := digestIndexFileContext(ctx, indexFile)
		if err != nil || digest != member.ContentDigest {
			indexFile.Close()
			closeShards()
			if err != nil {
				return nil, nil, fmt.Errorf(
					"hash focused member %q after mmap: %w",
					member.Name, err,
				)
			}
			return nil, nil, fmt.Errorf(
				"focused member %q differs after private mmap",
				member.Name,
			)
		}
		shard, err := index.NewSearcher(indexFile)
		if err != nil {
			indexFile.Close()
			closeShards()
			return nil, nil, fmt.Errorf(
				"open focused member %q searcher: %w", member.Name, err,
			)
		}
		priority, err := focusedShardPriority(ctx, shard)
		if err != nil {
			shard.Close()
			closeShards()
			return nil, nil, fmt.Errorf(
				"read focused member %q priority: %w", member.Name, err,
			)
		}
		sourceAfter, err := os.Lstat(source)
		if err != nil ||
			!sourceAfter.Mode().IsRegular() ||
			!os.SameFile(sourceBefore, sourceAfter) ||
			sourceAfter.Size() != sourceBefore.Size() ||
			!sourceAfter.ModTime().Equal(sourceBefore.ModTime()) {
			shard.Close()
			closeShards()
			return nil, nil, fmt.Errorf(
				"focused member %q changed after mmap", member.Name,
			)
		}
		shards = append(shards, staticFocusedShard{
			name: member.Name,
			path: source,
			identity: focusedArtifactIdentity{
				name:       member.Name,
				info:       sourceAfter,
				changeTime: fileInfoChangeTime(sourceAfter),
			},
			searcher: shard,
			priority: priority,
		})
	}
	sort.Slice(shards, func(i, j int) bool {
		if shards[i].priority != shards[j].priority {
			return shards[i].priority > shards[j].priority
		}
		return shards[i].name < shards[j].name
	})
	return &staticFocusedSearcher{
		name: manifest.Repository, shards: shards,
	}, cleanup, nil
}

func focusedShardPriority(
	ctx context.Context,
	shard zoekt.Searcher,
) (float64, error) {
	repositories, err := shard.List(
		ctx, &query.Const{Value: true}, nil,
	)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var priority float64
	for _, repository := range repositories.Repos {
		value, err := strconv.ParseFloat(
			repository.Repository.RawConfig["priority"], 64,
		)
		if err == nil && value > priority {
			priority = value
		}
	}
	return priority, nil
}

func digestIndexFileContext(
	ctx context.Context,
	file index.IndexFile,
) (string, error) {
	size, err := file.Size()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	const chunkSize = uint32(128 << 10)
	for offset := uint32(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count := chunkSize
		if remaining := size - offset; remaining < count {
			count = remaining
		}
		content, err := file.Read(offset, count)
		if err != nil {
			return "", err
		}
		if _, err := hash.Write(content); err != nil {
			return "", err
		}
		offset += count
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// staticFocusedSearcher is an immutable composite over only the files named by
// a validated manifest. It deliberately has no directory watcher: adding a
// valid but undeclared shard to the shared index directory cannot alter the
// served generation.
type staticFocusedSearcher struct {
	name      string
	shards    []staticFocusedShard
	closeOnce sync.Once
}

type staticFocusedShard struct {
	name     string
	path     string
	identity focusedArtifactIdentity
	searcher zoekt.Searcher
	priority float64
}

func (s staticFocusedShard) current(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Tests and non-filesystem implementations may construct static members
	// directly. Production bindings always carry an exact file identity.
	if s.path == "" || s.identity.info == nil {
		return nil
	}
	info, err := os.Lstat(s.path)
	if err != nil {
		return fmt.Errorf("inspect focused member %q: %w", s.name, err)
	}
	actual := focusedFingerprint{{
		name:       s.identity.name,
		info:       info,
		changeTime: fileInfoChangeTime(info),
	}}
	if !equalFocusedFingerprint(focusedFingerprint{s.identity}, actual) {
		return fmt.Errorf("focused member %q changed during search", s.name)
	}
	return nil
}

func (s *staticFocusedSearcher) Search(
	ctx context.Context,
	q query.Q,
	options *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	var err error
	q, err = s.evalTypeRepo(ctx, q)
	if err != nil {
		return nil, err
	}
	return s.search(ctx, q, options)
}

func (s *staticFocusedSearcher) search(
	ctx context.Context,
	q query.Q,
	options *zoekt.SearchOptions,
) (*zoekt.SearchResult, error) {
	started := time.Now()
	accumulator := newBoundResultAccumulator(boundDocDisplayLimit(options))
	for index, shard := range s.shards {
		if err := shard.current(ctx); err != nil {
			return nil, err
		}
		result, err := shard.searcher.Search(ctx, q, options)
		if err != nil {
			return nil, err
		}
		if err := shard.current(ctx); err != nil {
			return nil, err
		}
		accumulator.add(index, result)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return accumulator.finish(time.Since(started)), nil
}

func (s *staticFocusedSearcher) StreamSearch(
	ctx context.Context,
	q query.Q,
	options *zoekt.SearchOptions,
	sender zoekt.Sender,
) error {
	var err error
	q, err = s.evalTypeRepo(ctx, q)
	if err != nil {
		return err
	}
	maxPending := math.Inf(-1)
	if len(s.shards) > 0 {
		maxPending = s.shards[0].priority
	}
	sender.Send(&zoekt.SearchResult{
		Progress: zoekt.Progress{MaxPendingPriority: maxPending},
	})
	for index, shard := range s.shards {
		if err := shard.current(ctx); err != nil {
			return err
		}
		result, err := shard.searcher.Search(ctx, q, options)
		if err != nil {
			return err
		}
		if err := shard.current(ctx); err != nil {
			return err
		}
		if result == nil {
			result = &zoekt.SearchResult{}
		}
		nextPriority := math.Inf(-1)
		if index+1 < len(s.shards) {
			nextPriority = s.shards[index+1].priority
		}
		result.Priority = shard.priority
		result.MaxPendingPriority = nextPriority
		sender.Send(result)
	}
	return nil
}

func (s *staticFocusedSearcher) List(
	ctx context.Context,
	q query.Q,
	options *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	var err error
	q, err = s.evalTypeRepo(ctx, q)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, q, options)
}

func (s *staticFocusedSearcher) list(
	ctx context.Context,
	q query.Q,
	options *zoekt.ListOptions,
) (*zoekt.RepoList, error) {
	result := &zoekt.RepoList{}
	repositories := make(map[string]*zoekt.RepoListEntry)
	for _, shard := range s.shards {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		list, err := shard.searcher.List(ctx, q, options)
		if err != nil {
			return nil, err
		}
		result.Crashes += list.Crashes
		result.Stats.Add(&list.Stats)
		for id, repository := range list.ReposMap {
			if result.ReposMap == nil {
				result.ReposMap = make(zoekt.ReposMap)
			}
			if _, exists := result.ReposMap[id]; !exists {
				result.ReposMap[id] = repository
			}
		}
		for _, repository := range list.Repos {
			if existing := repositories[repository.Repository.Name]; existing != nil {
				existing.Stats.Add(&repository.Stats)
				continue
			}
			copy := *repository
			repositories[repository.Repository.Name] = &copy
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(repositories))
	for name := range repositories {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		result.Repos = append(result.Repos, repositories[name])
	}
	return result, nil
}

func (s *staticFocusedSearcher) evalTypeRepo(
	ctx context.Context,
	q query.Q,
) (query.Q, error) {
	var evalErr error
	q = query.Map(q, func(candidate query.Q) query.Q {
		if evalErr != nil {
			return nil
		}
		typed, ok := candidate.(*query.Type)
		if !ok || typed.Type != query.TypeRepo {
			return candidate
		}
		repositories, err := s.list(ctx, typed.Child, nil)
		if err != nil {
			evalErr = err
			return nil
		}
		names := make([]string, 0, len(repositories.Repos))
		for _, repository := range repositories.Repos {
			names = append(names, repository.Repository.Name)
		}
		return query.NewRepoSet(names...)
	})
	return q, evalErr
}

func (s *staticFocusedSearcher) Close() {
	s.closeOnce.Do(func() {
		for _, shard := range s.shards {
			shard.searcher.Close()
		}
		s.shards = nil
	})
}

func (s *staticFocusedSearcher) String() string {
	return "static-focused(" + s.name + ")"
}
