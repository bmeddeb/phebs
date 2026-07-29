package codenav

import (
	"container/heap"
	"container/list"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/scip-code/scip/bindings/go/scip"
)

const (
	// Result validation is deliberately oversampled but bounded: corrupt ranges
	// cannot trigger source reads for every occurrence in a million-hit index.
	maxDefinitionRangeCandidates = 512
	maxReferenceRangeCandidates  = MaxReferenceLocations*2 + 1
)

type cacheEntry struct {
	revision string
	index    *snapshot
	loadErr  error
	bytes    int64
	element  *list.Element
}

// Service lazily loads immutable SCIP snapshots into a byte-accounted LRU.
// Loading a new revision atomically replaces the previous repository snapshot.
type Service struct {
	dataDir             string
	indexPath           string
	maxIndexBytes       int64
	maxSourceBytes      int64
	maxQuerySourceBytes int64
	maxCacheBytes       int64
	parseLimits         parseLimits

	mu         sync.Mutex
	entries    map[string]*cacheEntry
	lru        *list.List
	cacheBytes int64

	repoLocks [64]sync.Mutex
}

func New(opts Options) *Service {
	indexPath := opts.IndexPath
	if indexPath == "" {
		indexPath = DefaultIndexPath
	}
	maxBytes := opts.MaxIndexBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxIndexBytes
	}
	maxSourceBytes := opts.MaxSourceBytes
	if maxSourceBytes <= 0 {
		maxSourceBytes = DefaultMaxSourceBytes
	}
	maxQuerySourceBytes := opts.MaxQuerySourceBytes
	if maxQuerySourceBytes <= 0 {
		maxQuerySourceBytes = DefaultMaxQuerySourceBytes
	}
	maxCacheBytes := opts.MaxCacheBytes
	if maxCacheBytes <= 0 {
		maxCacheBytes = DefaultMaxCacheBytes
	}
	return &Service{
		dataDir:             opts.DataDir,
		indexPath:           indexPath,
		maxIndexBytes:       maxBytes,
		maxSourceBytes:      maxSourceBytes,
		maxQuerySourceBytes: maxQuerySourceBytes,
		maxCacheBytes:       maxCacheBytes,
		parseLimits:         newParseLimits(opts),
		entries:             make(map[string]*cacheEntry),
		lru:                 list.New(),
	}
}

// Ingest force-loads the configured SCIP file from repo at revision. A missing file is a
// supported state: it replaces any stale snapshot and returns Available=false.
func (s *Service) Ingest(ctx context.Context, repo, revision string) (Availability, error) {
	repo, revision, err := s.validateRevision(repo, revision)
	if err != nil {
		return Availability{}, err
	}
	if err := validateRepoPath(s.indexPath); err != nil {
		return Availability{}, fmt.Errorf("index path: %w", err)
	}

	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	return s.ingestLocked(ctx, repo, revision)
}

func (s *Service) ingestLocked(ctx context.Context, repo, revision string) (Availability, error) {
	availability := Availability{Repo: repo, Revision: revision}
	if err := validateRepoPath(s.indexPath); err != nil {
		return Availability{}, fmt.Errorf("index path: %w", err)
	}
	data, err := s.readBlob(ctx, repo, revision, s.indexPath, s.maxIndexBytes, ErrIndexTooLarge)
	if errors.Is(err, errBlobNotFound) {
		if err := s.storeEntry(repo, cacheEntry{revision: revision}); err != nil {
			return Availability{}, err
		}
		return availability, nil
	}
	if err != nil {
		return Availability{}, fmt.Errorf("read committed SCIP index: %w", err)
	}

	index, err := parseSnapshot(ctx, data, s.parseLimits)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Availability{}, fmt.Errorf("parse committed SCIP index: %w", err)
		}
		// Cache malformed committed indexes by immutable revision. This prevents
		// every query from rereading and reparsing the same bounded-but-large blob.
		loadErr := fmt.Errorf("parse committed SCIP index: %w", err)
		if cacheErr := s.storeEntry(repo, cacheEntry{revision: revision, loadErr: loadErr}); cacheErr != nil {
			return Availability{}, errors.Join(loadErr, cacheErr)
		}
		return Availability{}, loadErr
	}
	if err := s.storeEntry(repo, cacheEntry{revision: revision, index: index}); err != nil {
		return Availability{}, err
	}
	availability.Available = true
	availability.Documents = len(index.documents)
	availability.Occurrences = index.retainedOccurrences
	return availability, nil
}

// Remove drops the cached snapshot for repo. It is safe to call repeatedly.
func (s *Service) Remove(repo string) error {
	repo, err := s.validateRepo(repo)
	if err != nil {
		return err
	}
	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	s.deleteEntry(repo)
	return nil
}

func (s *Service) Definition(ctx context.Context, q Query) (DefinitionResult, error) {
	_, entry, doc, occurrence, converter, err := s.resolve(ctx, q)
	if err != nil {
		return DefinitionResult{}, err
	}
	if entry.index == nil {
		return DefinitionResult{Available: false}, nil
	}
	result := DefinitionResult{Available: true}
	if occurrence == nil {
		return result, nil
	}
	result.Symbol = occurrence.GetSymbol()
	key := symbolKey(doc.path, result.Symbol)
	keys := entry.index.relatedDefinitionSymbols(key)

	indexed, _ := topLocations(entry.index.definitions, keys, maxDefinitionRangeCandidates)
	for _, location := range indexed {
		if err := ctx.Err(); err != nil {
			return DefinitionResult{}, err
		}
		converted, err := converter.location(location)
		if err == nil {
			result.Location = &converted
			return result, nil
		}
		if !isSkippableLocationError(err) {
			return DefinitionResult{}, err
		}
	}
	return result, nil
}

func (s *Service) References(ctx context.Context, q Query) (ReferencesResult, error) {
	_, entry, doc, occurrence, converter, err := s.resolve(ctx, q)
	if err != nil {
		return ReferencesResult{}, err
	}
	result := ReferencesResult{Available: entry.index != nil, Locations: []Location{}}
	if entry.index == nil || occurrence == nil {
		return result, nil
	}
	result.Symbol = occurrence.GetSymbol()
	start := symbolKey(doc.path, result.Symbol)
	keys := entry.index.relatedReferenceSymbols(start)

	indexed, candidatesTruncated := entry.index.topReferences(keys, maxReferenceRangeCandidates)
	result.Truncated = candidatesTruncated
	for _, location := range indexed {
		if err := ctx.Err(); err != nil {
			return ReferencesResult{}, err
		}
		converted, err := converter.location(location)
		if err != nil {
			if isSkippableLocationError(err) {
				continue
			}
			return ReferencesResult{}, err
		}
		if len(result.Locations) == MaxReferenceLocations {
			result.Truncated = true
			break
		}
		result.Locations = append(result.Locations, converted)
	}
	return result, nil
}

func (s *Service) Hover(ctx context.Context, q Query) (HoverResult, error) {
	q, entry, doc, occurrence, converter, err := s.resolve(ctx, q)
	if err != nil {
		return HoverResult{}, err
	}
	result := HoverResult{Available: entry.index != nil}
	if entry.index == nil || occurrence == nil {
		return result, nil
	}
	info := entry.index.symbols[symbolKey(doc.path, occurrence.GetSymbol())]
	override := occurrence.GetOverrideDocumentation()
	if info == nil && len(override) == 0 {
		return result, nil
	}

	r, ok := occurrence.SourceRange()
	if !ok {
		return HoverResult{}, fmt.Errorf("hover occurrence range is missing")
	}
	if err := r.Validate(); err != nil {
		return HoverResult{}, fmt.Errorf("hover occurrence range: %w", err)
	}
	converted, err := converter.codeRange(doc.path, fromSCIPRange(r), doc.encoding)
	if err != nil {
		if isSkippableLocationError(err) {
			return result, nil
		}
		return HoverResult{}, err
	}
	hover := &HoverInfo{
		Symbol:   occurrence.GetSymbol(),
		Range:    converted,
		Encoding: q.Encoding,
	}
	if info != nil {
		hover.DisplayName = info.displayName
		hover.Kind = info.kind
		hover.Signature = info.signature
		hover.Language = info.language
		hover.Documentation = append([]string(nil), info.documentation...)
	}
	if len(override) > 0 {
		hover.Documentation = append([]string(nil), override...)
	}
	if err := validateHoverResponse(hover, s.parseLimits); err != nil {
		return HoverResult{}, err
	}
	result.Hover = hover
	return result, nil
}

func (s *Service) resolve(ctx context.Context, q Query) (Query, cacheEntry, *document, *scip.Occurrence, *rangeConverter, error) {
	var err error
	q.Repo, q.Revision, err = s.validateRevision(q.Repo, q.Revision)
	if err != nil {
		return Query{}, cacheEntry{}, nil, nil, nil, err
	}
	if err := validateRepoPath(q.Path); err != nil {
		return Query{}, cacheEntry{}, nil, nil, nil, fmt.Errorf("query path: %w", err)
	}
	if q.Line < 0 || q.Character < 0 {
		return Query{}, cacheEntry{}, nil, nil, nil, fmt.Errorf("negative position: %w", ErrInvalidInput)
	}
	q.Encoding, err = normalizeEncoding(q.Encoding)
	if err != nil {
		return Query{}, cacheEntry{}, nil, nil, nil, err
	}
	entry, err := s.ensure(ctx, q.Repo, q.Revision)
	if err != nil || entry.index == nil {
		return q, entry, nil, nil, nil, err
	}
	doc := entry.index.documents[q.Path]
	if doc == nil {
		return q, entry, nil, nil, nil, nil
	}
	converter := newRangeConverter(s, ctx, q.Repo, q.Revision, q.Encoding)
	character, err := converter.position(q.Path, q.Line, q.Character, q.Encoding, doc.encoding)
	if err != nil {
		return Query{}, cacheEntry{}, nil, nil, nil, err
	}
	occurrences := scip.FindOccurrences(doc.occurrences, q.Line, character)
	for _, occurrence := range occurrences {
		if occurrence.GetSymbol() != "" {
			return q, entry, doc, occurrence, converter, nil
		}
	}
	return q, entry, doc, nil, converter, nil
}

func (s *Service) ensure(ctx context.Context, repo, revision string) (cacheEntry, error) {
	if entry, ok := s.loadEntry(repo, revision); ok {
		return entry, entry.loadErr
	}
	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	if entry, ok := s.loadEntry(repo, revision); ok {
		return entry, entry.loadErr
	}
	if _, err := s.ingestLocked(ctx, repo, revision); err != nil {
		return cacheEntry{}, err
	}
	entry, _ := s.loadEntry(repo, revision)
	return entry, entry.loadErr
}

func (s *Service) validateRevision(repo, revision string) (string, string, error) {
	repo, err := s.validateRepo(repo)
	if err != nil {
		return "", "", err
	}
	revision = strings.ToLower(strings.TrimSpace(revision))
	if !validRevision(revision) {
		return "", "", fmt.Errorf("revision must be a full Git object ID: %w", ErrInvalidInput)
	}
	return repo, revision, nil
}

func (s *Service) validateRepo(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", fmt.Errorf("empty repository: %w", ErrInvalidInput)
	}
	if _, err := s.safeRepoDir(repo); err != nil {
		return "", fmt.Errorf("repository %q: %w", repo, ErrInvalidInput)
	}
	return repo, nil
}

func (s *Service) repoLock(repo string) *sync.Mutex {
	const (
		offset32 = uint32(2166136261)
		prime32  = uint32(16777619)
	)
	hash := offset32
	for i := 0; i < len(repo); i++ {
		hash ^= uint32(repo[i])
		hash *= prime32
	}
	return &s.repoLocks[hash%uint32(len(s.repoLocks))]
}

func (s *Service) loadEntry(repo, revision string) (cacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[repo]
	if !ok || entry.revision != revision {
		return cacheEntry{}, false
	}
	s.lru.MoveToFront(entry.element)
	return *entry, true
}

func (s *Service) storeEntry(repo string, entry cacheEntry) error {
	entry.bytes = cacheEntryBytes(repo, entry)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteEntryLocked(repo)
	if entry.bytes > s.maxCacheBytes {
		return fmt.Errorf("snapshot needs %d bytes (cache limit %d): %w", entry.bytes, s.maxCacheBytes, ErrCacheBudget)
	}
	entry.element = s.lru.PushFront(repo)
	s.entries[repo] = &entry
	s.cacheBytes += entry.bytes
	for s.cacheBytes > s.maxCacheBytes {
		oldest := s.lru.Back()
		if oldest == nil {
			break
		}
		s.deleteEntryLocked(oldest.Value.(string))
	}
	return nil
}

func (s *Service) deleteEntry(repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteEntryLocked(repo)
}

func (s *Service) deleteEntryLocked(repo string) {
	entry := s.entries[repo]
	if entry == nil {
		return
	}
	delete(s.entries, repo)
	s.cacheBytes -= entry.bytes
	s.lru.Remove(entry.element)
}

func cacheEntryBytes(repo string, entry cacheEntry) int64 {
	const entryOverhead = 256
	bytes := int64(entryOverhead + len(repo) + len(entry.revision))
	if entry.index != nil {
		bytes += entry.index.estimatedBytes
	}
	if entry.loadErr != nil {
		bytes += int64(len(entry.loadErr.Error()))
	}
	return bytes
}

func isSkippableLocationError(err error) bool {
	return errors.Is(err, ErrInvalidInput) ||
		errors.Is(err, ErrUnsupportedEncoding) ||
		errors.Is(err, errBlobNotFound)
}

func sortIndexedLocations(locations []indexedLocation) {
	sort.Slice(locations, func(i, j int) bool {
		return compareIndexedLocations(locations[i], locations[j]) < 0
	})
}

func compareIndexedLocations(a, b indexedLocation) int {
	if value := strings.Compare(a.path, b.path); value != 0 {
		return value
	}
	positions := [][2]int32{
		{a.codeRange.Start.Line, b.codeRange.Start.Line},
		{a.codeRange.Start.Character, b.codeRange.Start.Character},
		{a.codeRange.End.Line, b.codeRange.End.Line},
		{a.codeRange.End.Character, b.codeRange.End.Character},
	}
	for _, pair := range positions {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

type maxLocationHeap []indexedLocation

func (h maxLocationHeap) Len() int { return len(h) }
func (h maxLocationHeap) Less(i, j int) bool {
	return compareIndexedLocations(h[i], h[j]) > 0
}
func (h maxLocationHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxLocationHeap) Push(value any) {
	*h = append(*h, value.(indexedLocation))
}
func (h *maxLocationHeap) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func (s *snapshot) topReferences(keys []string, limit int) ([]indexedLocation, bool) {
	return topLocations(s.references, keys, limit)
}

func topLocations(locationsBySymbol map[string][]indexedLocation, keys []string, limit int) ([]indexedLocation, bool) {
	if limit <= 0 {
		return []indexedLocation{}, true
	}
	// The max-heap and membership map never exceed limit. Every candidate is
	// scanned, but only the deterministic smallest locations are materialized.
	selected := make(map[indexedLocation]struct{}, limit)
	locations := &maxLocationHeap{}
	heap.Init(locations)
	truncated := false
	for _, key := range keys {
		for _, location := range locationsBySymbol[key] {
			if _, ok := selected[location]; ok {
				continue
			}
			if locations.Len() < limit {
				heap.Push(locations, location)
				selected[location] = struct{}{}
				continue
			}
			truncated = true
			if compareIndexedLocations(location, (*locations)[0]) >= 0 {
				continue
			}
			removed := heap.Pop(locations).(indexedLocation)
			delete(selected, removed)
			heap.Push(locations, location)
			selected[location] = struct{}{}
		}
	}
	result := append([]indexedLocation(nil), (*locations)...)
	sortIndexedLocations(result)
	return result, truncated
}
