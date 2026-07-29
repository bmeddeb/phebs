package codenav

import (
	"container/heap"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/scip-code/scip/bindings/go/scip"
)

const (
	// Result validation is deliberately oversampled but bounded: corrupt ranges
	// cannot trigger source reads for every occurrence in a million-hit index.
	maxDefinitionRangeCandidates     = 512
	maxReferenceRangeCandidates      = MaxReferenceLocations*2 + 1
	analysisUnitMaxSelectedPaths     = 128
	analysisUnitMaxSelectedPathBytes = 64 << 10
	analysisUnitMaxPathBytes         = 4096
)

type cacheEntry struct {
	revision        string
	bindingIdentity string
	binding         typedIndexBinding
	index           *snapshot
	loadErr         error
	bytes           int64
	element         *list.Element
}

type typedIndexBinding struct {
	TypedIndexBinding
	identity string
}

// Service lazily loads immutable SCIP snapshots into a byte-accounted LRU.
// Loading a new revision atomically replaces the previous repository snapshot.
type Service struct {
	dataDir             string
	indexPath           string
	bindingResolver     TypedIndexResolver
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
		bindingResolver:     opts.BindingResolver,
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
	binding, err := s.resolveTypedIndex(ctx, repo, revision)
	if err != nil {
		return Availability{}, err
	}

	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	availability, err := s.ingestLocked(ctx, repo, revision, binding)
	if err != nil {
		return Availability{}, err
	}
	if err := s.validateResultBinding(
		ctx,
		repo,
		revision,
		binding.identity,
	); err != nil {
		return Availability{}, err
	}
	return availability, nil
}

func (s *Service) ingestLocked(
	ctx context.Context,
	repo, revision string,
	binding typedIndexBinding,
) (Availability, error) {
	availability := Availability{Repo: repo, Revision: revision}
	if binding.Focused && binding.Path == "" {
		if err := s.storeEntry(repo, cacheEntry{
			revision:        revision,
			bindingIdentity: binding.identity,
			binding:         binding,
		}); err != nil {
			return Availability{}, err
		}
		return availability, nil
	}
	data, err := s.readBlob(
		ctx,
		repo,
		revision,
		binding.Path,
		s.maxIndexBytes,
		ErrIndexTooLarge,
	)
	if errors.Is(err, errBlobNotFound) {
		if err := s.storeEntry(repo, cacheEntry{
			revision:        revision,
			bindingIdentity: binding.identity,
			binding:         binding,
		}); err != nil {
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
		if cacheErr := s.storeEntry(repo, cacheEntry{
			revision:        revision,
			bindingIdentity: binding.identity,
			binding:         binding,
			loadErr:         loadErr,
		}); cacheErr != nil {
			return Availability{}, errors.Join(loadErr, cacheErr)
		}
		return Availability{}, loadErr
	}
	if binding.Focused {
		if err := validateScopedSnapshot(index, binding); err != nil {
			loadErr := fmt.Errorf("validate scoped SCIP index: %w", err)
			if cacheErr := s.storeEntry(repo, cacheEntry{
				revision:        revision,
				bindingIdentity: binding.identity,
				binding:         binding,
				loadErr:         loadErr,
			}); cacheErr != nil {
				return Availability{}, errors.Join(loadErr, cacheErr)
			}
			return Availability{}, loadErr
		}
	}
	if err := s.storeEntry(repo, cacheEntry{
		revision:        revision,
		bindingIdentity: binding.identity,
		binding:         binding,
		index:           index,
	}); err != nil {
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

func (s *Service) Definition(
	ctx context.Context,
	q Query,
) (result DefinitionResult, resultErr error) {
	q, entry, doc, occurrence, converter, err := s.resolve(ctx, q)
	if err != nil {
		return DefinitionResult{}, err
	}
	defer func() {
		if resultErr != nil {
			return
		}
		if err := s.validateResultBinding(
			ctx,
			q.Repo,
			q.Revision,
			entry.bindingIdentity,
		); err != nil {
			result = DefinitionResult{}
			resultErr = err
		}
	}()
	if entry.index == nil {
		return DefinitionResult{Available: false}, nil
	}
	result = DefinitionResult{Available: true}
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

func (s *Service) References(
	ctx context.Context,
	q Query,
) (result ReferencesResult, resultErr error) {
	q, entry, doc, occurrence, converter, err := s.resolve(ctx, q)
	if err != nil {
		return ReferencesResult{}, err
	}
	defer func() {
		if resultErr != nil {
			return
		}
		if err := s.validateResultBinding(
			ctx,
			q.Repo,
			q.Revision,
			entry.bindingIdentity,
		); err != nil {
			result = ReferencesResult{}
			resultErr = err
		}
	}()
	result = ReferencesResult{Available: entry.index != nil, Locations: []Location{}}
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

func (s *Service) Hover(
	ctx context.Context,
	q Query,
) (result HoverResult, resultErr error) {
	q, entry, doc, occurrence, converter, err := s.resolve(ctx, q)
	if err != nil {
		return HoverResult{}, err
	}
	defer func() {
		if resultErr != nil {
			return
		}
		if err := s.validateResultBinding(
			ctx,
			q.Repo,
			q.Revision,
			entry.bindingIdentity,
		); err != nil {
			result = HoverResult{}
			resultErr = err
		}
	}()
	result = HoverResult{Available: entry.index != nil}
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
	if entry.binding.Focused && !entry.binding.admits(q.Path) {
		return Query{}, cacheEntry{}, nil, nil, nil, fmt.Errorf(
			"query path %q: %w",
			q.Path,
			ErrDocumentOutsideUnit,
		)
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
	binding, err := s.resolveTypedIndex(ctx, repo, revision)
	if err != nil {
		return cacheEntry{}, err
	}
	if entry, ok := s.loadBoundEntry(repo, revision, binding.identity); ok {
		return entry, entry.loadErr
	}
	lock := s.repoLock(repo)
	lock.Lock()
	defer lock.Unlock()
	if entry, ok := s.loadBoundEntry(repo, revision, binding.identity); ok {
		return entry, entry.loadErr
	}
	if _, err := s.ingestLocked(ctx, repo, revision, binding); err != nil {
		return cacheEntry{}, err
	}
	entry, _ := s.loadBoundEntry(repo, revision, binding.identity)
	return entry, entry.loadErr
}

func (s *Service) resolveTypedIndex(
	ctx context.Context,
	repo, revision string,
) (typedIndexBinding, error) {
	resolved := TypedIndexBinding{
		Path:   s.indexPath,
		Commit: revision,
	}
	if s.bindingResolver != nil {
		var err error
		resolved, err = s.bindingResolver.ResolveTypedIndex(ctx, repo, revision)
		if err != nil {
			return typedIndexBinding{}, fmt.Errorf(
				"resolve typed-index binding: %w",
				err,
			)
		}
	}
	binding, err := canonicalTypedIndexBinding(resolved, revision, s.indexPath)
	if err != nil {
		return typedIndexBinding{}, err
	}
	return binding, nil
}

func (s *Service) validateResultBinding(
	ctx context.Context,
	repo, revision, expectedIdentity string,
) error {
	current, err := s.resolveTypedIndex(ctx, repo, revision)
	if err != nil {
		return err
	}
	if current.identity != expectedIdentity {
		return fmt.Errorf(
			"committed typed-index binding changed while producing the result: %w",
			ErrBindingChanged,
		)
	}
	return nil
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

func (s *Service) loadBoundEntry(
	repo, revision, bindingIdentity string,
) (cacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[repo]
	if !ok || entry.revision != revision ||
		entry.bindingIdentity != bindingIdentity {
		return cacheEntry{}, false
	}
	s.lru.MoveToFront(entry.element)
	return *entry, true
}

func (s *Service) storeEntry(repo string, entry cacheEntry) error {
	if entry.bindingIdentity == "" && entry.binding.identity != "" {
		entry.bindingIdentity = entry.binding.identity
	}
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
	bytes := int64(
		entryOverhead +
			len(repo) +
			len(entry.revision) +
			len(entry.bindingIdentity) +
			len(entry.binding.Path) +
			len(entry.binding.Commit) +
			len(entry.binding.UnitDigest),
	)
	for _, selected := range entry.binding.PrimaryPaths {
		bytes += int64(len(selected))
	}
	for _, selected := range entry.binding.SupportingPaths {
		bytes += int64(len(selected))
	}
	if entry.index != nil {
		bytes += entry.index.estimatedBytes
	}
	if entry.loadErr != nil {
		bytes += int64(len(entry.loadErr.Error()))
	}
	return bytes
}

func canonicalTypedIndexBinding(
	input TypedIndexBinding,
	revision, defaultPath string,
) (typedIndexBinding, error) {
	if input.Commit == "" {
		input.Commit = revision
	}
	if input.Commit != revision || !validRevision(input.Commit) {
		return typedIndexBinding{}, fmt.Errorf(
			"typed-index commit %q does not match requested revision: %w",
			input.Commit,
			ErrTypedIndexBinding,
		)
	}
	if !input.Focused {
		if input.Path == "" {
			input.Path = defaultPath
		}
		if input.UnitDigest != "" ||
			len(input.PrimaryPaths) != 0 ||
			len(input.SupportingPaths) != 0 {
			return typedIndexBinding{}, fmt.Errorf(
				"whole-repository typed-index binding carries analysis-unit scope: %w",
				ErrTypedIndexBinding,
			)
		}
		if err := validateRepoPath(input.Path); err != nil {
			return typedIndexBinding{}, fmt.Errorf(
				"typed-index path: %w",
				ErrTypedIndexBinding,
			)
		}
		return bindTypedIndex(input)
	}
	if !validUnitDigest(input.UnitDigest) {
		return typedIndexBinding{}, fmt.Errorf(
			"focused typed-index binding has invalid unit digest: %w",
			ErrTypedIndexBinding,
		)
	}
	if len(input.PrimaryPaths)+len(input.SupportingPaths) >
		analysisUnitMaxSelectedPaths {
		return typedIndexBinding{}, fmt.Errorf(
			"focused typed-index binding has too many selected paths: %w",
			ErrTypedIndexBinding,
		)
	}
	primary, err := canonicalBindingPaths(input.PrimaryPaths, true)
	if err != nil {
		return typedIndexBinding{}, err
	}
	supporting, err := canonicalBindingPaths(input.SupportingPaths, false)
	if err != nil {
		return typedIndexBinding{}, err
	}
	if err := validateBindingPathOverlap(primary, supporting); err != nil {
		return typedIndexBinding{}, err
	}
	input.PrimaryPaths = primary
	input.SupportingPaths = supporting
	var selectedPathBytes int
	for _, selected := range append(slices.Clone(primary), supporting...) {
		selectedPathBytes += len(selected)
	}
	if selectedPathBytes > analysisUnitMaxSelectedPathBytes {
		return typedIndexBinding{}, fmt.Errorf(
			"focused typed-index binding selected paths are too large: %w",
			ErrTypedIndexBinding,
		)
	}
	if input.Path != "" {
		if err := validateRepoPath(input.Path); err != nil {
			return typedIndexBinding{}, fmt.Errorf(
				"typed-index path: %w",
				ErrTypedIndexBinding,
			)
		}
		if !slices.Contains(input.SupportingPaths, input.Path) {
			return typedIndexBinding{}, fmt.Errorf(
				"typed-index path %q is not an exact supporting path: %w",
				input.Path,
				ErrTypedIndexBinding,
			)
		}
	}
	return bindTypedIndex(input)
}

func bindTypedIndex(input TypedIndexBinding) (typedIndexBinding, error) {
	canonical, err := json.Marshal(input)
	if err != nil {
		return typedIndexBinding{}, fmt.Errorf(
			"encode typed-index binding: %w",
			err,
		)
	}
	sum := sha256.Sum256(
		append([]byte("phebs-typed-index-binding-v1\x00"), canonical...),
	)
	return typedIndexBinding{
		TypedIndexBinding: input,
		identity:          "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func canonicalBindingPaths(
	input []string,
	required bool,
) ([]string, error) {
	if required && len(input) == 0 {
		return nil, fmt.Errorf(
			"focused typed-index binding has no primary paths: %w",
			ErrTypedIndexBinding,
		)
	}
	result := slices.Clone(input)
	slices.Sort(result)
	for index, selected := range result {
		if len(selected) > analysisUnitMaxPathBytes {
			return nil, fmt.Errorf(
				"typed-index scope path is too large: %w",
				ErrTypedIndexBinding,
			)
		}
		if err := validateRepoPath(selected); err != nil {
			return nil, fmt.Errorf(
				"typed-index scope path %q: %w",
				selected,
				ErrTypedIndexBinding,
			)
		}
		if index > 0 && result[index-1] == selected {
			return nil, fmt.Errorf(
				"duplicate typed-index scope path %q: %w",
				selected,
				ErrTypedIndexBinding,
			)
		}
	}
	return result, nil
}

func validateBindingPathOverlap(primary, supporting []string) error {
	all := append(slices.Clone(primary), supporting...)
	slices.Sort(all)
	for index, selected := range all {
		for parent := path.Dir(selected); parent != "."; parent = path.Dir(parent) {
			search := sort.SearchStrings(all, parent)
			if search < len(all) && all[search] == parent {
				return fmt.Errorf(
					"overlapping typed-index scope paths %q and %q: %w",
					parent,
					selected,
					ErrTypedIndexBinding,
				)
			}
		}
		if index > 0 && all[index-1] == selected {
			return fmt.Errorf(
				"duplicate typed-index scope path %q: %w",
				selected,
				ErrTypedIndexBinding,
			)
		}
	}
	return nil
}

func (binding typedIndexBinding) admits(filePath string) bool {
	for _, selected := range binding.PrimaryPaths {
		if selected == filePath || strings.HasPrefix(filePath, selected+"/") {
			return true
		}
	}
	for _, selected := range binding.SupportingPaths {
		if selected == filePath || strings.HasPrefix(filePath, selected+"/") {
			return true
		}
	}
	return false
}

func validateScopedSnapshot(
	index *snapshot,
	binding typedIndexBinding,
) error {
	for documentPath := range index.documentPaths {
		if !binding.admits(documentPath) {
			return fmt.Errorf(
				"document %q: %w",
				documentPath,
				ErrDocumentOutsideUnit,
			)
		}
	}
	return nil
}

func validUnitDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 ||
		!strings.HasPrefix(value, "sha256:") ||
		value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
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
