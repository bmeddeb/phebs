package search

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/query"
)

type boundBackend struct {
	searcher zoekt.Streamer
	query    query.Q
	focused  *focusedLease
}

var errBoundStreamLimit = errors.New("bound stream display limit reached")

const maxBoundWorkers = 8

func boundBackends(
	shared zoekt.Streamer,
	compiled *compiledSearch,
) []boundBackend {
	backends := make([]boundBackend, 0, len(compiled.focused)+1)
	if compiled.hasBase {
		backends = append(backends, boundBackend{
			searcher: shared,
			query:    compiled.baseQuery,
		})
	}
	for _, lease := range compiled.focused {
		backends = append(backends, boundBackend{
			searcher: lease.searcher,
			focused:  lease,
			query: query.Simplify(query.NewAnd(
				compiled.query,
				query.NewRepoSet(lease.repository),
			)),
		})
	}
	return backends
}

func runBoundSearch(
	ctx context.Context,
	shared zoekt.Streamer,
	compiled *compiledSearch,
	options Options,
) (*zoekt.SearchResult, error) {
	backends := boundBackends(shared, compiled)
	if len(backends) == 0 {
		return &zoekt.SearchResult{
			RepoURLs:      map[string]string{},
			LineFragments: map[string]string{},
		}, nil
	}
	callerCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type job struct {
		index   int
		backend boundBackend
	}
	type outcome struct {
		index  int
		result *zoekt.SearchResult
		err    error
	}
	workerCount := boundWorkerCount(len(backends))
	// Each worker can have at most one completed result waiting for the
	// serialized accumulator. The buffer therefore retains O(workers*K), not
	// O(backends*K), while the caller drains concurrently with execution.
	outcomes := make(chan outcome, workerCount)
	jobs := make(chan job, len(backends))
	for index, backend := range backends {
		jobs <- job{index: index, backend: backend}
	}
	close(jobs)
	var workers sync.WaitGroup
	var cancelOnce sync.Once
	workers.Add(workerCount)
	started := time.Now()
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				result, err := job.backend.searcher.Search(
					ctx, job.backend.query, options.zoektWithin(ctx),
				)
				// This post-search full-generation check is the focused
				// authorization linearization point. Discard the entire
				// outcome before it can evict valid lower-ranked candidates.
				if result != nil &&
					!compiled.focusedCurrent(
						ctx, job.backend.focused, true,
					) {
					result = nil
				}
				if err != nil {
					cancelOnce.Do(cancel)
				}
				// The caller drains until the waiter closes outcomes, so this
				// send remains safe after cancellation and cannot hide the
				// backend error that caused it.
				outcomes <- outcome{
					index: job.index, result: result, err: err,
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(outcomes)
	}()

	accumulator := newBoundResultAccumulator(
		clamp(options.MaxMatches, 50, 500),
	)
	var selectedError *boundSearchError
	for outcome := range outcomes {
		if outcome.result != nil {
			filterResultVersions(outcome.result, compiled.versions)
			if focused := backends[outcome.index].focused; focused != nil {
				filterFocusedLeaseResult(
					outcome.result, focused.repository,
				)
			} else {
				compiled.filterCurrentWholeResult(ctx, outcome.result)
			}
			accumulator.add(outcome.index, outcome.result)
		}
		if outcome.err != nil {
			selectedError = selectBoundSearchError(
				selectedError,
				&boundSearchError{index: outcome.index, err: outcome.err},
			)
		}
	}
	if selectedError != nil {
		return nil, selectedError.err
	}
	if err := callerCtx.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return accumulator.finish(time.Since(started)), nil
}

type boundSearchError struct {
	index int
	err   error
}

func selectBoundSearchError(
	current, candidate *boundSearchError,
) *boundSearchError {
	if current == nil {
		return candidate
	}
	currentContext := contextTermination(current.err)
	candidateContext := contextTermination(candidate.err)
	switch {
	case currentContext && !candidateContext:
		return candidate
	case currentContext == candidateContext && candidate.index < current.index:
		return candidate
	default:
		return current
	}
}

// boundResultAccumulator keeps only the globally best maxFiles documents
// while retaining aggregate statistics and repository metadata from every
// result. Source order is explicit so map collisions and FlushReason remain
// deterministic even when concurrent backends complete out of order.
type boundResultAccumulator struct {
	result *zoekt.SearchResult
	limit  int

	flushReasonSource int
	repoURLSources    map[string]int
	fragmentSources   map[string]int
}

func newBoundResultAccumulator(maxFiles int) *boundResultAccumulator {
	return &boundResultAccumulator{
		result: &zoekt.SearchResult{
			RepoURLs:      make(map[string]string),
			LineFragments: make(map[string]string),
		},
		limit:             maxFiles,
		flushReasonSource: -1,
		repoURLSources:    make(map[string]int),
		fragmentSources:   make(map[string]int),
	}
}

func (a *boundResultAccumulator) add(
	source int,
	result *zoekt.SearchResult,
) {
	if result == nil {
		return
	}
	a.result.Add(result.Stats)
	if result.FlushReason != 0 &&
		(a.flushReasonSource < 0 || source < a.flushReasonSource) {
		a.result.FlushReason = result.FlushReason
		a.flushReasonSource = source
	}
	mergeBoundStringMap(
		a.result.RepoURLs, a.repoURLSources, source, result.RepoURLs,
	)
	mergeBoundStringMap(
		a.result.LineFragments,
		a.fragmentSources,
		source,
		result.LineFragments,
	)
	a.addFiles(result.Files)
}

func (a *boundResultAccumulator) addFiles(files []zoekt.FileMatch) {
	if a.limit <= 0 || len(files) == 0 {
		return
	}
	if len(files) > a.limit {
		sort.Slice(files, func(i, j int) bool {
			return boundFileBefore(files[i], files[j])
		})
		files = files[:a.limit]
	}
	combined := make(
		[]zoekt.FileMatch, 0, len(a.result.Files)+len(files),
	)
	combined = append(combined, a.result.Files...)
	combined = append(combined, files...)
	sort.Slice(combined, func(i, j int) bool {
		return boundFileBefore(combined[i], combined[j])
	})
	if len(combined) > a.limit {
		combined = combined[:a.limit]
	}
	// Copy into an exact-capacity slice. Reslicing combined would otherwise
	// retain discarded FileMatch values and their content/match payloads.
	retained := make([]zoekt.FileMatch, len(combined))
	copy(retained, combined)
	a.result.Files = retained
}

func (a *boundResultAccumulator) finish(
	duration time.Duration,
) *zoekt.SearchResult {
	a.result.Duration = duration
	return a.result
}

func mergeBoundStringMap(
	target map[string]string,
	sources map[string]int,
	source int,
	values map[string]string,
) {
	for key, value := range values {
		previous, exists := sources[key]
		if !exists || source < previous {
			target[key] = value
			sources[key] = source
		}
	}
}

func boundFileBefore(left, right zoekt.FileMatch) bool {
	switch {
	case left.Score != right.Score:
		return left.Score > right.Score
	case left.Repository != right.Repository:
		return left.Repository < right.Repository
	case left.FileName != right.FileName:
		return left.FileName < right.FileName
	case left.Version != right.Version:
		return left.Version < right.Version
	case left.Language != right.Language:
		return left.Language < right.Language
	case left.SubRepositoryName != right.SubRepositoryName:
		return left.SubRepositoryName < right.SubRepositoryName
	case left.SubRepositoryPath != right.SubRepositoryPath:
		return left.SubRepositoryPath < right.SubRepositoryPath
	case left.RepositoryPriority != right.RepositoryPriority:
		return left.RepositoryPriority > right.RepositoryPriority
	case left.RepositoryID != right.RepositoryID:
		return left.RepositoryID < right.RepositoryID
	default:
		return bytes.Compare(left.Checksum, right.Checksum) < 0
	}
}

func boundDocDisplayLimit(options *zoekt.SearchOptions) int {
	const hardMax = 500
	if options == nil ||
		options.MaxDocDisplayCount <= 0 ||
		options.MaxDocDisplayCount > hardMax {
		return hardMax
	}
	return options.MaxDocDisplayCount
}

func runBoundStream(
	ctx context.Context,
	shared zoekt.Streamer,
	compiled *compiledSearch,
	options Options,
	sink func(*zoekt.SearchResult, *focusedLease) error,
) error {
	backends := boundBackends(shared, compiled)
	if len(backends) == 0 {
		return nil
	}
	callerCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type event struct {
		result  *zoekt.SearchResult
		focused *focusedLease
		ack     chan struct{}
		err     error
	}
	workerCount := boundWorkerCount(len(backends))
	events := make(chan event, workerCount*2)
	jobs := make(chan boundBackend, len(backends))
	for _, backend := range backends {
		jobs <- backend
	}
	close(jobs)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for backend := range jobs {
				if ctx.Err() != nil {
					return
				}
				if !compiled.focusedCurrent(ctx, backend.focused, true) {
					continue
				}
				err := backend.searcher.StreamSearch(
					ctx,
					backend.query,
					options.zoektWithin(ctx),
					zoekt.SenderFunc(func(result *zoekt.SearchResult) {
						ack := make(chan struct{})
						select {
						case events <- event{
							result: result, focused: backend.focused, ack: ack,
						}:
							// Sender retains result ownership. Apply bounded
							// backpressure until the serialized consumer has
							// finished filtering and sinking this batch.
							<-ack
						case <-ctx.Done():
						}
					}),
				)
				if err != nil {
					// The consumer drains until every worker exits. Never
					// select this against ctx.Done: cancellation caused by a
					// display limit must not hide a concurrent integrity error.
					events <- event{err: err}
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(events)
	}()

	limitReached := false
	var streamErr error
	for event := range events {
		if event.err != nil {
			if (limitReached || streamErr != nil) &&
				contextTermination(event.err) {
				continue
			}
			if streamErr == nil {
				streamErr = event.err
				cancel()
			}
			continue
		}
		if event.result == nil || streamErr != nil || limitReached {
			if event.ack != nil {
				close(event.ack)
			}
			continue
		}
		if !compiled.focusedCurrent(ctx, event.focused, false) {
			if event.ack != nil {
				close(event.ack)
			}
			continue
		}
		if event.focused == nil {
			filterResultVersions(event.result, compiled.versions)
			compiled.filterCurrentWholeResult(ctx, event.result)
		}
		sinkErr := sink(event.result, event.focused)
		if event.ack != nil {
			close(event.ack)
		}
		if sinkErr != nil {
			if errors.Is(sinkErr, errBoundStreamLimit) {
				limitReached = true
			} else {
				streamErr = sinkErr
			}
			cancel()
		}
	}
	if err := callerCtx.Err(); err != nil {
		return err
	}
	return streamErr
}

func contextTermination(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func boundWorkerCount(backends int) int {
	if backends <= 0 {
		return 0
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > maxBoundWorkers {
		workers = maxBoundWorkers
	}
	if workers > backends {
		workers = backends
	}
	return workers
}
