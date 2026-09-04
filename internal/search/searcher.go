package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
	zoektsearch "github.com/sourcegraph/zoekt/search"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/store"
)

// Searcher serves queries over the shard directory in-process. One shared
// DirectorySearcher watches the directory but is queried only for
// whole-repository rows. Focused repositories are deliberately excluded from
// that asynchronous view and served through manifest-bound exact-generation
// searchers in focusedCache.
type Searcher struct {
	z              zoekt.Streamer
	st             store.Store
	indexDir       string
	focused        *focusedCache
	whole          *wholeCache
	generationPins *focusedindex.SearchGenerationPins

	wholeRepairMu sync.Mutex
	wholeRepairs  map[string]wholeRepairRequest
	// maxWallTime is test-configurable; zero means maxSearchWallTime.
	maxWallTime time.Duration
	// Contexts backs `context:<name>` filters (T8.1); assigned once at
	// startup from config.
	Contexts map[string][]string
	// Usage, when set, receives one event per successfully completed search
	// (T10.2). This single hook covers REST, SSE, and MCP, which all funnel
	// through Search/Stream. Assigned once at startup; the callback resolves
	// the actor and must never block on failure.
	Usage func(ctx context.Context, event store.UsageEvent)
	// Visible is the per-user RepoSet hook (T10.3), filling the reservation
	// noted in CLAUDE.md. All-code search resolves it in the pre-pass; service
	// search resolves it before authority work and again after query execution
	// so revocation discards the result. It returns the caller's repo predicate
	// — or nil when the caller may see everything (administrators). A nil field
	// disables permission filtering. REST, SSE, and MCP share these boundaries.
	Visible func(ctx context.Context) func(store.Repo) bool
}

type wholeRepairRequest struct {
	key      string
	failures int
	retryAt  time.Time
}

const (
	// usageRepoCap bounds the distinct repo names recorded per search, in
	// result (relevance) order.
	usageRepoCap = 20
	// maxSearchWallTime is one query-wide budget, beginning before query
	// compilation and focused publication validation/materialization.
	maxSearchWallTime = 10 * time.Second
)

func Open(indexDir string, st store.Store) (*Searcher, error) {
	return open(indexDir, st, nil)
}

func OpenWithGenerationPins(
	indexDir string, st store.Store, pins *focusedindex.SearchGenerationPins,
) (*Searcher, error) {
	return open(indexDir, st, pins)
}

func open(
	indexDir string, st store.Store, pins *focusedindex.SearchGenerationPins,
) (*Searcher, error) {
	// NewDirectorySearcher uses filepath.Glob internally. Reject metacharacters
	// at this boundary too so library callers cannot mount sibling shards.
	if strings.ContainsAny(indexDir, `*?[\`) {
		return nil, fmt.Errorf("open shard dir: path contains glob metacharacters")
	}
	if info, err := os.Lstat(indexDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("open shard dir: managed index path is not a real directory")
		}
		entries, readErr := os.ReadDir(indexDir)
		if readErr != nil {
			return nil, fmt.Errorf("open shard dir: %w", readErr)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".zoekt") && entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("open shard dir: symlinked shard %q", entry.Name())
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("open shard dir: %w", err)
	}
	whole := newWholeCache(indexDir)
	var candidates map[string]wholeSharedCandidate
	var err error
	if st != nil {
		bootstrapCtx, cancel := context.WithTimeout(
			context.Background(), WholeGenerationWarmingTimeout,
		)
		candidates, err = whole.captureSharedCandidates(
			bootstrapCtx, st,
		)
		cancel()
		if err != nil {
			whole.close()
			return nil, fmt.Errorf(
				"capture whole-repository startup generations: %w", err,
			)
		}
	}
	z, err := zoektsearch.NewDirectorySearcher(indexDir)
	if err != nil {
		whole.close()
		return nil, fmt.Errorf("open shard dir %s: %w", indexDir, err)
	}
	if st != nil {
		bootstrapCtx, cancel := context.WithTimeout(
			context.Background(), WholeGenerationWarmingTimeout,
		)
		err = whole.bootstrapShared(
			bootstrapCtx, z, st, candidates,
		)
		cancel()
		if err != nil {
			z.Close()
			whole.close()
			return nil, fmt.Errorf(
				"bind whole-repository startup generations: %w", err,
			)
		}
	}
	return &Searcher{
		z: z, st: st, indexDir: indexDir,
		focused: newFocusedCache(indexDir),
		whole:   whole, generationPins: pins,
	}, nil
}

func (s *Searcher) Close() {
	if s.focused != nil {
		s.focused.close()
	}
	if s.whole != nil {
		s.whole.close()
	}
	if s.z != nil {
		s.z.Close()
	}
}

// Options bound the work a single query may do.
type Options struct {
	MaxMatches   int // documents shown; default 50, cap 500
	ContextLines int // lines around each match; default 0, cap 10
}

func (o Options) zoekt() *zoekt.SearchOptions {
	max := clamp(o.MaxMatches, 50, 500)
	return &zoekt.SearchOptions{
		ChunkMatches:       true,
		MaxDocDisplayCount: max, // documents returned to the client
		// TotalMaxMatchCount bounds total matches COLLECTED across shards, not
		// documents shown. It must be >> MaxDocDisplayCount or a common term
		// fills the budget inside the first shard and the remaining
		// shards/repos are never searched (zoekt's own default is 1,000,000).
		// Generous safety cap; MaxWallTime is the real backstop.
		TotalMaxMatchCount: 100000,
		NumContextLines:    clamp(o.ContextLines, 0, 10),
		MaxWallTime:        maxSearchWallTime,
	}
}

func (o Options) zoektWithin(ctx context.Context) *zoekt.SearchOptions {
	options := o.zoekt()
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		if remaining < options.MaxWallTime {
			options.MaxWallTime = remaining
		}
	}
	return options
}

func clamp(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	}
	return v
}

// Result is the wire shape of a search response.
type Result struct {
	Files []FileResult  `json:"files"`
	Stats Stats         `json:"stats"`
	Scope *ScopeReceipt `json:"scope_receipt,omitempty"`
}

type FileResult struct {
	Repo     string  `json:"repo"`
	Path     string  `json:"path"`
	Ref      string  `json:"ref,omitempty"` // immutable commit indexed by zoekt
	Language string  `json:"language,omitempty"`
	Chunks   []Chunk `json:"chunks"`
}

// Chunk is a contiguous run of lines containing one or more matches.
type Chunk struct {
	Content   string  `json:"content"`
	StartLine int     `json:"start_line"` // 1-based line of Content's first line
	Ranges    []Range `json:"ranges"`
}

// Range is a match location, 1-based, relative to the file.
type Range struct {
	StartLine int `json:"start_line"`
	StartCol  int `json:"start_col"`
	EndLine   int `json:"end_line"`
	EndCol    int `json:"end_col"`
}

type Stats struct {
	MatchCount int   `json:"match_count"`
	FileCount  int   `json:"file_count"`
	DurationMS int64 `json:"duration_ms"`
}

// Search compiles raw (T4.1 pre-pass included) and runs one bounded search.
func (s *Searcher) Search(ctx context.Context, raw string, opts Options) (*Result, error) {
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, s.queryWallTime())
	defer cancel()
	compiled, err := s.compile(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer compiled.release()
	res, err := runBoundSearch(ctx, s.z, compiled, opts)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	filterResultVersions(res, compiled.versions)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("search result validation: %w", err)
	}
	if maxFiles := clamp(opts.MaxMatches, 50, 500); len(res.Files) > maxFiles {
		res.Files = res.Files[:maxFiles]
	}
	result := toResult(res, compiled.versions)
	if s.Usage != nil {
		repos := newRepoCollector()
		for _, f := range result.Files {
			repos.add(f.Repo)
		}
		s.Usage(callerCtx, usageEvent(result.Stats, repos))
	}
	return result, nil
}

// Stream compiles raw and forwards each native shard-level result batch as it
// arrives. Bound backends fan out concurrently, while one serialized consumer
// applies the global display limit and result-time focused identity gate.
func (s *Searcher) Stream(ctx context.Context, raw string, opts Options, sink func(*Result)) (*Stats, error) {
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, s.queryWallTime())
	defer cancel()
	compiled, err := s.compile(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer compiled.release()
	var agg Stats
	repos := newRepoCollector()
	remaining := clamp(opts.MaxMatches, 50, 500)
	err = runBoundStream(
		ctx, s.z, compiled, opts,
		func(r *zoekt.SearchResult, lease *focusedLease) error {
			if lease != nil {
				filterFocusedLeaseResult(r, lease.repository)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			filterResultVersions(r, compiled.versions)
			if remaining == 0 {
				return errBoundStreamLimit
			}
			if len(r.Files) > remaining {
				trimmed := *r
				trimmed.Files = r.Files[:remaining]
				r = &trimmed
			}
			remaining -= len(r.Files)
			batch := toResult(r, compiled.versions)
			agg.MatchCount += batch.Stats.MatchCount
			agg.FileCount += batch.Stats.FileCount
			agg.DurationMS += batch.Stats.DurationMS
			for _, f := range batch.Files {
				repos.add(f.Repo)
			}
			if len(batch.Files) > 0 {
				sink(batch)
			}
			if remaining == 0 {
				return errBoundStreamLimit
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("stream search: %w", err)
	}
	if s.Usage != nil {
		s.Usage(callerCtx, usageEvent(agg, repos))
	}
	return &agg, nil
}

func filterFocusedLeaseResult(
	result *zoekt.SearchResult,
	repository string,
) {
	if result == nil {
		return
	}
	files := result.Files[:0]
	for _, file := range result.Files {
		if file.Repository == repository {
			files = append(files, file)
		}
	}
	result.Files = files
}

func filterResultVersions(
	result *zoekt.SearchResult,
	versions map[string]string,
) {
	if result == nil {
		return
	}
	files := result.Files[:0]
	for _, file := range result.Files {
		if versions[file.Repository] == file.Version {
			files = append(files, file)
		}
	}
	result.Files = files
}

func (s *Searcher) queryWallTime() time.Duration {
	if s.maxWallTime > 0 {
		return s.maxWallTime
	}
	return maxSearchWallTime
}

// repoCollector keeps the first usageRepoCap distinct repo names in delivery
// order. JSON Search delivers relevance order; progressive SSE intentionally
// records native shard/backend arrival order.
type repoCollector struct {
	seen  map[string]bool
	names []string
}

func newRepoCollector() *repoCollector {
	return &repoCollector{seen: make(map[string]bool)}
}

func (c *repoCollector) add(name string) {
	if len(c.names) >= usageRepoCap || c.seen[name] {
		return
	}
	c.seen[name] = true
	c.names = append(c.names, name)
}

func usageEvent(stats Stats, repos *repoCollector) store.UsageEvent {
	return store.UsageEvent{
		Kind: "search", Repos: repos.names,
		MatchCount: stats.MatchCount, FileCount: stats.FileCount,
		DurationMS: stats.DurationMS,
	}
}

// compile applies the user query and then fails closed to repository rows the
// store still considers searchable. Stale/untracked shards can remain on disk
// after an interrupted cleanup, but they must never leak into search results.
type compiledSearch struct {
	query               query.Q
	baseQuery           query.Q
	hasBase             bool
	versions            map[string]string
	baseRevisions       map[string]store.IndexedRevision
	sharedRevisions     map[string][]store.IndexedRevision
	focused             []*focusedLease
	whole               []*wholeLease
	searchGenerations   []*focusedindex.SearchGenerationLease
	legacyWhole         bool
	validateFocused     func(context.Context, *focusedLease, bool) bool
	validateWholeLease  func(context.Context, *wholeLease, bool) bool
	validateWhole       func(context.Context, string, store.IndexedRevision) bool
	validateSharedWhole func(
		context.Context,
		string,
		[]store.IndexedRevision,
		bool,
	) bool
	validateWholeGenerations func(
		context.Context,
		map[string][]store.IndexedRevision,
		[]*wholeLease,
		bool,
	) bool
	validateWholeTouched func(
		context.Context,
		map[string][]store.IndexedRevision,
		[]*wholeLease,
		bool,
	) bool
}

func (c *compiledSearch) release() {
	for _, lease := range c.focused {
		lease.release()
	}
	c.focused = nil
	for _, lease := range c.whole {
		lease.release()
	}
	c.whole = nil
	for _, lease := range c.searchGenerations {
		lease.Release()
	}
	c.searchGenerations = nil
}

func (c *compiledSearch) focusedCurrent(
	ctx context.Context,
	lease *focusedLease,
	full bool,
) bool {
	if lease == nil || c.validateFocused == nil {
		return true
	}
	return c.validateFocused(ctx, lease, full)
}

func (c *compiledSearch) wholeCurrent(
	ctx context.Context,
	lease *wholeLease,
	full bool,
) bool {
	if lease == nil || c.validateWholeLease == nil {
		return true
	}
	return c.validateWholeLease(ctx, lease, full)
}

func (c *compiledSearch) wholeLeasesCurrent(
	ctx context.Context,
	full bool,
) bool {
	for _, lease := range c.whole {
		if !c.wholeCurrent(ctx, lease, full) {
			return false
		}
	}
	return true
}

func (c *compiledSearch) wholeGenerationsCurrent(
	ctx context.Context,
	full bool,
) bool {
	if c.validateWholeGenerations == nil {
		return c.sharedWholeCurrent(ctx, full) &&
			c.wholeLeasesCurrent(ctx, full)
	}
	return c.validateWholeGenerations(
		ctx, c.sharedRevisions, c.whole, full,
	)
}

func (c *compiledSearch) sharedWholeCurrent(
	ctx context.Context,
	full bool,
) bool {
	if c.validateSharedWhole == nil {
		return true
	}
	for repository, revisions := range c.sharedRevisions {
		if !c.validateSharedWhole(ctx, repository, revisions, full) {
			return false
		}
	}
	return true
}

func (s *Searcher) validateFocusedLease(
	ctx context.Context,
	lease *focusedLease,
	full bool,
) bool {
	repo, err := s.st.GetRepo(ctx, lease.repository)
	if err != nil || repo == nil {
		if ctx.Err() == nil {
			lease.invalidate()
		}
		return false
	}
	if full {
		if !lease.current(ctx, *repo) {
			if ctx.Err() == nil {
				lease.invalidate()
			}
			return false
		}
		// The artifact scan can take measurable time. Re-fetch the committed
		// row so a scope/posture transition during that scan cannot pass by
		// reusing the pre-scan Repo value.
		repo, err = s.st.GetRepo(ctx, lease.repository)
		if err != nil || repo == nil || !lease.active(*repo) {
			if ctx.Err() == nil {
				lease.invalidate()
			}
			return false
		}
		return true
	}
	valid := ctx.Err() == nil && lease.active(*repo)
	if !valid && ctx.Err() == nil {
		lease.invalidate()
	}
	return valid
}

func (c *compiledSearch) filterCurrentWholeResult(
	ctx context.Context,
	result *zoekt.SearchResult,
	full bool,
) bool {
	if result == nil || c.validateWhole == nil {
		return true
	}
	if c.validateWholeTouched != nil {
		names := make(map[string]struct{})
		for _, file := range result.Files {
			names[file.Repository] = struct{}{}
		}
		if len(names) == 0 {
			return true
		}
		shared := make(
			map[string][]store.IndexedRevision, len(names),
		)
		leases := make([]*wholeLease, 0, len(names))
		for repository := range names {
			if revisions, ok := c.sharedRevisions[repository]; ok {
				shared[repository] = revisions
				continue
			}
			found := false
			for _, lease := range c.whole {
				if lease.repository == repository {
					leases = append(leases, lease)
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return c.validateWholeTouched(ctx, shared, leases, full)
	}
	current := make(map[string]bool)
	check := func(repository string) bool {
		if valid, ok := current[repository]; ok {
			return valid
		}
		valid := false
		if revisions, ok := c.sharedRevisions[repository]; ok &&
			c.validateSharedWhole != nil {
			valid = c.validateSharedWhole(
				ctx, repository, revisions, full,
			)
		} else {
			matchedLease := false
			for _, lease := range c.whole {
				if lease.repository == repository {
					matchedLease = true
					valid = c.wholeCurrent(ctx, lease, full)
					break
				}
			}
			if !matchedLease {
				expected, ok := c.baseRevisions[repository]
				valid = ok &&
					c.validateWhole(ctx, repository, expected)
			}
		}
		current[repository] = valid
		return valid
	}
	allCurrent := true
	files := result.Files[:0]
	for _, file := range result.Files {
		if check(file.Repository) {
			files = append(files, file)
		} else {
			allCurrent = false
		}
	}
	result.Files = files
	for repository := range result.RepoURLs {
		if !check(repository) {
			delete(result.RepoURLs, repository)
			allCurrent = false
		}
	}
	for repository := range result.LineFragments {
		if !check(repository) {
			delete(result.LineFragments, repository)
			allCurrent = false
		}
	}
	return allCurrent
}

func (s *Searcher) validateWholeRepository(
	ctx context.Context,
	repository string,
	expected store.IndexedRevision,
) bool {
	repo, err := s.st.GetRepo(ctx, repository)
	if err != nil || repo == nil || repo.Deleting ||
		repo.IndexedCommitHash == "" ||
		(repo.IndexedAnalysisUnit != nil &&
			repo.IndexedAnalysisUnit.SearchIndexPosture ==
				analysisunit.SearchIndexFocused) ||
		focusedindex.IsPublishing(s.indexDir, repository) {
		return false
	}
	current, ok := indexedRevision(*repo, expected.Selector)
	return ok && current == expected
}

func (s *Searcher) validateWholeLease(
	ctx context.Context,
	lease *wholeLease,
	full bool,
) bool {
	if lease == nil {
		return true
	}
	repo, err := s.st.GetRepo(ctx, lease.repository)
	current := err == nil && repo != nil &&
		lease.current(ctx, *repo, full)
	if full && !current {
		lease.invalidate()
	}
	return current
}

func (s *Searcher) requestWholeRepair(
	ctx context.Context,
	repository string,
	revisions []store.IndexedRevision,
) error {
	key := "missing-or-invalid"
	if manifest, err := focusedindex.ReadWholeManifestContext(
		ctx, s.indexDir, repository, revisions,
	); err == nil {
		key = manifest.Digest
	} else if info, statErr := os.Lstat(filepath.Join(
		s.indexDir, focusedindex.WholeManifestName(repository),
	)); statErr == nil {
		key = fmt.Sprintf(
			"invalid:%d:%d:%d",
			info.Size(), info.ModTime().UnixNano(), info.Mode(),
		)
	}
	s.wholeRepairMu.Lock()
	if s.wholeRepairs == nil {
		s.wholeRepairs = make(map[string]wholeRepairRequest)
	}
	previous := s.wholeRepairs[repository]
	if previous.key == key && time.Now().Before(previous.retryAt) {
		s.wholeRepairMu.Unlock()
		return nil
	}
	failures := 1
	if previous.key == key {
		failures = min(previous.failures+1, 32)
	}
	request := wholeRepairRequest{
		key:      key,
		failures: failures,
		retryAt: time.Now().Add(
			focusedNegativeRetryDelay(failures),
		),
	}
	s.wholeRepairs[repository] = request
	s.wholeRepairMu.Unlock()
	if _, err := s.st.EnqueuePending(
		ctx, store.JobIndex, repository, true,
	); err != nil {
		s.wholeRepairMu.Lock()
		if s.wholeRepairs[repository] == request {
			delete(s.wholeRepairs, repository)
		}
		s.wholeRepairMu.Unlock()
		return err
	}
	return nil
}

func (s *Searcher) clearWholeRepair(repository string) {
	s.wholeRepairMu.Lock()
	delete(s.wholeRepairs, repository)
	s.wholeRepairMu.Unlock()
}

func (s *Searcher) validateSharedWhole(
	ctx context.Context,
	repository string,
	revisions []store.IndexedRevision,
	full bool,
) bool {
	repo, err := s.st.GetRepo(ctx, repository)
	if err != nil || repo == nil ||
		!wholeRepoIdentityMatches(*repo, revisions) {
		if full && s.whole != nil {
			s.whole.invalidateShared(repository, revisions)
		}
		return false
	}
	if !full {
		return !focusedindex.IsPublishing(s.indexDir, repository)
	}
	if s.whole == nil {
		return false
	}
	if !s.whole.sharedCurrent(
		ctx, repository, revisions, full,
	) {
		if full {
			s.whole.invalidateShared(repository, revisions)
		}
		return false
	}
	if !full {
		return true
	}
	manifest, err := focusedindex.ReadWholeManifestContext(
		ctx, s.indexDir, repository, revisions,
	)
	if err != nil {
		s.whole.invalidateShared(repository, revisions)
		return false
	}
	list, err := s.z.List(
		ctx, query.NewRepoSet(repository), nil,
	)
	current := err == nil && wholeRepoListMatches(
		list, repository, revisions, len(manifest.Members),
	)
	if !current {
		s.whole.invalidateShared(repository, revisions)
	}
	return current
}

// validateWholeGenerations batches the committed-row barrier for all
// whole-repository backends. Filesystem identities remain repository-local,
// but each query phase performs one store scan rather than one round trip per
// repository.
func (s *Searcher) validateWholeGenerations(
	ctx context.Context,
	shared map[string][]store.IndexedRevision,
	leases []*wholeLease,
	full bool,
) bool {
	if len(shared) == 0 && len(leases) == 0 {
		return ctx.Err() == nil
	}
	repositories, err := s.st.ListRepos(ctx)
	if err != nil {
		return false
	}
	rows := make(map[string]store.Repo, len(repositories))
	for _, repository := range repositories {
		rows[repository.Name] = repository
	}
	var inventory map[string]*zoekt.RepoListEntry
	inventoryOK := true
	if full && len(shared) > 0 {
		names := make([]string, 0, len(shared))
		for repository := range shared {
			names = append(names, repository)
		}
		list, listErr := s.z.List(
			ctx, query.NewRepoSet(names...), nil,
		)
		if listErr != nil {
			inventoryOK = false
		} else {
			inventory, inventoryOK = wholeRepoInventory(list)
		}
	}
	allSharedCurrent := inventoryOK
	for repository, revisions := range shared {
		row, ok := rows[repository]
		current := ok && wholeRepoIdentityMatches(row, revisions) &&
			s.whole != nil &&
			s.whole.sharedCurrent(
				ctx, repository, revisions, full,
			)
		if current && full {
			manifest, manifestErr := focusedindex.ReadWholeManifestContext(
				ctx, s.indexDir, repository, revisions,
			)
			current = manifestErr == nil && inventoryOK &&
				wholeRepoEntryMatches(
					inventory[repository],
					repository,
					revisions,
					len(manifest.Members),
				)
		}
		if !current {
			if s.whole != nil {
				s.whole.invalidateShared(repository, revisions)
			}
			allSharedCurrent = false
		}
	}
	if !allSharedCurrent {
		return false
	}
	for _, lease := range leases {
		row, ok := rows[lease.repository]
		if !ok || !lease.current(ctx, row, full) {
			if full {
				lease.invalidate()
			}
			return false
		}
	}
	return ctx.Err() == nil
}

// validateWholeTouchedSet is the pre-emission Stream fence. It reads only
// repository rows represented by the current event. The ordinary quick path
// checks each touched marker and receipt identity; its full mode can also
// batch shared inventory and member fingerprints. The final barrier remains
// the only result-time fleet scan.
func (s *Searcher) validateWholeTouchedSet(
	ctx context.Context,
	shared map[string][]store.IndexedRevision,
	leases []*wholeLease,
	full bool,
) bool {
	if len(shared) == 0 && len(leases) == 0 {
		return ctx.Err() == nil
	}
	rows := make(
		map[string]store.Repo, len(shared)+len(leases),
	)
	for repository := range shared {
		row, err := s.st.GetRepo(ctx, repository)
		if err != nil || row == nil {
			return false
		}
		rows[repository] = *row
	}
	for _, lease := range leases {
		if _, exists := rows[lease.repository]; exists {
			continue
		}
		row, err := s.st.GetRepo(ctx, lease.repository)
		if err != nil || row == nil {
			return false
		}
		rows[lease.repository] = *row
	}
	var inventory map[string]*zoekt.RepoListEntry
	inventoryOK := true
	if full && len(shared) > 0 {
		names := make([]string, 0, len(shared))
		for repository := range shared {
			names = append(names, repository)
		}
		list, err := s.z.List(
			ctx, query.NewRepoSet(names...), nil,
		)
		if err != nil {
			inventoryOK = false
		} else {
			inventory, inventoryOK = wholeRepoInventory(list)
		}
	}
	allSharedCurrent := inventoryOK
	for repository, revisions := range shared {
		current := wholeRepoIdentityMatches(
			rows[repository], revisions,
		) && s.whole != nil &&
			s.whole.sharedCurrent(
				ctx, repository, revisions, full,
			)
		if current && full {
			manifest, err := focusedindex.ReadWholeManifestContext(
				ctx, s.indexDir, repository, revisions,
			)
			current = err == nil && inventoryOK &&
				wholeRepoEntryMatches(
					inventory[repository],
					repository,
					revisions,
					len(manifest.Members),
				)
		}
		if !current {
			if s.whole != nil {
				s.whole.invalidateShared(repository, revisions)
			}
			allSharedCurrent = false
		}
	}
	if !allSharedCurrent {
		return false
	}
	for _, lease := range leases {
		if !lease.current(
			ctx, rows[lease.repository], full,
		) {
			if full {
				lease.invalidate()
			}
			return false
		}
	}
	return ctx.Err() == nil
}

func (s *Searcher) compile(ctx context.Context, raw string) (*compiledSearch, error) {
	q, revision, err := compileQuery(ctx, s.st, s.Contexts, raw)
	if err != nil {
		return nil, err
	}
	if revision == "" {
		revision = "HEAD"
	}
	repos, err := s.st.ListRepos(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve searchable repos: %w", err)
	}
	focusedRepositories := make(map[string]struct{})
	wholeRepositories := make(map[string]struct{})
	for _, repo := range repos {
		if repo.Deleting || repo.IndexedCommitHash == "" {
			continue
		}
		if repo.IndexedAnalysisUnit != nil &&
			repo.IndexedAnalysisUnit.SearchIndexPosture ==
				analysisunit.SearchIndexFocused {
			focusedRepositories[repo.Name] = struct{}{}
		} else {
			wholeRepositories[repo.Name] = struct{}{}
		}
	}
	s.focused.prune(focusedRepositories)
	if s.whole != nil {
		s.whole.prune(wholeRepositories)
	}
	var validateWholeTouched func(
		context.Context,
		map[string][]store.IndexedRevision,
		[]*wholeLease,
		bool,
	) bool
	if s.whole != nil {
		validateWholeTouched = s.validateWholeTouchedSet
	}
	var allow func(store.Repo) bool
	if s.Visible != nil {
		allow = s.Visible(ctx)
	}
	versions := make(map[string]string, len(repos))
	baseRevisions := make(map[string]store.IndexedRevision)
	sharedRevisions := make(map[string][]store.IndexedRevision)
	branchRepos := make(map[string][]string)
	baseBranchRepos := make(map[string][]string)
	var focusedLeases []*focusedLease
	var wholeLeases []*wholeLease
	var searchGenerationLeases []*focusedindex.SearchGenerationLease
	legacyWhole := false
	releaseLeases := func() {
		for _, lease := range focusedLeases {
			lease.release()
		}
		for _, lease := range wholeLeases {
			lease.release()
		}
		for _, lease := range searchGenerationLeases {
			lease.Release()
		}
	}
	for _, repo := range repos {
		if repo.Deleting || repo.IndexedCommitHash == "" {
			continue
		}
		// T10.3: filtering versions too makes toResult's revision check a
		// second fail-closed gate over the permission boundary.
		if allow != nil && !allow(repo) {
			continue
		}
		indexed, ok := indexedRevision(repo, revision)
		if !ok {
			continue
		}
		focused := repo.IndexedAnalysisUnit != nil &&
			repo.IndexedAnalysisUnit.SearchIndexPosture ==
				analysisunit.SearchIndexFocused
		if focusedindex.IsPublishing(s.indexDir, repo.Name) {
			if !focused {
				releaseLeases()
				return nil, fmt.Errorf(
					"%w: repository %q publication is in progress",
					errWholeGenerationChanged, repo.Name,
				)
			}
			continue
		}
		if focused {
			lease, acquireErr := s.focused.acquire(
				ctx, repo.Name, repo.IndexedAnalysisUnit, repo.IndexedRevisions,
			)
			if acquireErr != nil || lease == nil {
				if err := ctx.Err(); err != nil {
					releaseLeases()
					return nil, err
				}
				continue
			}
			if !s.validateFocusedLease(ctx, lease, true) {
				lease.release()
				continue
			}
			focusedLeases = append(focusedLeases, lease)
		} else {
			revisions := canonicalWholeRevisions(repo)
			if s.generationPins != nil {
				root, rootErr := focusedindex.ReadSearchGenerationRootContext(
					ctx, s.indexDir, repo.Name,
				)
				if rootErr == nil {
					if !slices.Equal(root.Current.Revisions, revisions) {
						releaseLeases()
						return nil, fmt.Errorf(
							"%w: repository %q search-generation root is stale",
							errWholeGenerationChanged, repo.Name,
						)
					}
					generationLease, acquireErr := s.generationPins.Acquire(
						repo.Name, root.Current.GenerationDigest,
					)
					if acquireErr != nil {
						releaseLeases()
						return nil, acquireErr
					}
					searchGenerationLeases = append(searchGenerationLeases, generationLease)
				} else if !errors.Is(rootErr, os.ErrNotExist) {
					releaseLeases()
					return nil, fmt.Errorf(
						"read search-generation root for %q: %w", repo.Name, rootErr,
					)
				}
			}
			if s.whole == nil {
				// Test/compatibility construction without the generation
				// cache retains the pre-receipt result fence. Production Open
				// always installs wholeCache and requires a receipt.
				baseBranchRepos[indexed.Branch] = append(
					baseBranchRepos[indexed.Branch], repo.Name,
				)
				legacyWhole = true
				baseRevisions[repo.Name] = indexed
				branchRepos[indexed.Branch] = append(
					branchRepos[indexed.Branch], repo.Name,
				)
				versions[repo.Name] = indexed.Commit
				continue
			}
			lease, acquireErr := s.whole.acquireIfStale(
				ctx, s.z, repo.Name, revisions,
			)
			if acquireErr != nil {
				releaseLeases()
				bindErr := fmt.Errorf(
					"resolve whole-repository publication %q: %w",
					repo.Name, acquireErr,
				)
				if ctx.Err() != nil ||
					contextTermination(acquireErr) {
					return nil, bindErr
				}
				if repairErr := s.requestWholeRepair(
					ctx, repo.Name, revisions,
				); repairErr != nil {
					return nil, errors.Join(
						bindErr,
						fmt.Errorf(
							"queue forced replacement for %q: %w",
							repo.Name, repairErr,
						),
					)
				}
				return nil, bindErr
			}
			s.clearWholeRepair(repo.Name)
			if lease == nil {
				baseBranchRepos[indexed.Branch] = append(
					baseBranchRepos[indexed.Branch], repo.Name,
				)
				sharedRevisions[repo.Name] = revisions
			} else {
				wholeLeases = append(wholeLeases, lease)
			}
			baseRevisions[repo.Name] = indexed
		}
		branchRepos[indexed.Branch] = append(branchRepos[indexed.Branch], repo.Name)
		versions[repo.Name] = indexed.Commit
	}
	if err := ctx.Err(); err != nil {
		releaseLeases()
		return nil, err
	}
	if len(branchRepos) == 0 {
		releaseLeases()
		if revision != "HEAD" {
			return nil, fmt.Errorf(
				"revision %q is not indexed in any visible repository", revision,
			)
		}
		empty := query.Simplify(query.NewAnd(query.NewRepoSet(), q))
		return &compiledSearch{
			query: empty, versions: versions, baseRevisions: baseRevisions,
			sharedRevisions:          sharedRevisions,
			searchGenerations:        searchGenerationLeases,
			validateFocused:          s.validateFocusedLease,
			validateWholeLease:       s.validateWholeLease,
			validateWhole:            s.validateWholeRepository,
			validateSharedWhole:      s.validateSharedWhole,
			validateWholeGenerations: s.validateWholeGenerations,
			validateWholeTouched:     validateWholeTouched,
		}, nil
	}
	scoped := scopeQuery(branchRepos, q)
	baseScoped, hasBase := scopeQueryIfAny(baseBranchRepos, q)
	return &compiledSearch{
		query: scoped, baseQuery: baseScoped, hasBase: hasBase,
		versions: versions, baseRevisions: baseRevisions,
		sharedRevisions:          sharedRevisions,
		focused:                  focusedLeases,
		whole:                    wholeLeases,
		searchGenerations:        searchGenerationLeases,
		legacyWhole:              legacyWhole,
		validateFocused:          s.validateFocusedLease,
		validateWholeLease:       s.validateWholeLease,
		validateWhole:            s.validateWholeRepository,
		validateSharedWhole:      s.validateSharedWhole,
		validateWholeGenerations: s.validateWholeGenerations,
		validateWholeTouched:     validateWholeTouched,
	}, nil
}

func scopeQueryIfAny(branchRepos map[string][]string, q query.Q) (query.Q, bool) {
	if len(branchRepos) == 0 {
		return nil, false
	}
	return scopeQuery(branchRepos, q), true
}

func scopeQuery(branchRepos map[string][]string, q query.Q) query.Q {
	branches := make([]string, 0, len(branchRepos))
	for branch := range branchRepos {
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	var scopes []query.Q
	for _, branch := range branches {
		names := branchRepos[branch]
		sort.Strings(names)
		scopes = append(scopes, query.NewAnd(
			query.NewRepoSet(names...),
			&query.Branch{Pattern: branch, Exact: true},
		))
	}
	scope := scopes[0]
	if len(scopes) > 1 {
		scope = query.NewOr(scopes...)
	}
	return query.Simplify(query.NewAnd(scope, q))
}

func indexedRevision(repo store.Repo, selector string) (store.IndexedRevision, bool) {
	if len(repo.IndexedRevisions) == 0 {
		if selector == "HEAD" && repo.IndexedCommitHash != "" {
			return store.IndexedRevision{Selector: "HEAD", Branch: "HEAD", Commit: repo.IndexedCommitHash}, true
		}
		return store.IndexedRevision{}, false
	}
	if len(repo.IndexedRevisions) > 8 {
		return store.IndexedRevision{}, false
	}
	bySelector := make(map[string]store.IndexedRevision, len(repo.IndexedRevisions))
	branches := make(map[string]bool, len(repo.IndexedRevisions))
	validDefault := false
	for _, revision := range repo.IndexedRevisions {
		if revision.Selector == "" || revision.Branch == "" || revision.Commit == "" ||
			bySelector[revision.Selector].Selector != "" || branches[revision.Branch] {
			return store.IndexedRevision{}, false
		}
		if revision.Selector == "HEAD" {
			validDefault = revision.Branch == "HEAD" && revision.Commit == repo.IndexedCommitHash
		}
		bySelector[revision.Selector] = revision
		branches[revision.Branch] = true
	}
	if !validDefault {
		return store.IndexedRevision{}, false
	}
	revision, ok := bySelector[selector]
	return revision, ok
}

func toResult(res *zoekt.SearchResult, versions map[string]string) *Result {
	out := &Result{
		Files: make([]FileResult, 0, len(res.Files)),
		Stats: Stats{
			DurationMS: res.Duration.Milliseconds(),
		},
	}
	for _, f := range res.Files {
		if versions[f.Repository] != f.Version {
			continue
		}
		fr := FileResult{
			Repo:     f.Repository,
			Path:     f.FileName,
			Ref:      f.Version,
			Language: f.Language,
			Chunks:   make([]Chunk, 0, len(f.ChunkMatches)),
		}
		for _, c := range f.ChunkMatches {
			if c.FileName {
				continue // filename-only matches carry no content chunk worth showing
			}
			ch := Chunk{
				Content:   string(c.Content),
				StartLine: int(c.ContentStart.LineNumber),
				Ranges:    make([]Range, 0, len(c.Ranges)),
			}
			for _, r := range c.Ranges {
				ch.Ranges = append(ch.Ranges, Range{
					StartLine: int(r.Start.LineNumber), StartCol: int(r.Start.Column),
					EndLine: int(r.End.LineNumber), EndCol: int(r.End.Column),
				})
				out.Stats.MatchCount++
			}
			fr.Chunks = append(fr.Chunks, ch)
		}
		out.Files = append(out.Files, fr)
		out.Stats.FileCount++
	}
	return out
}
