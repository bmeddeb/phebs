package codenav

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/scip-code/scip/bindings/go/scip"
)

// RoutedBinding is projected from exact trusted current-store custody. Selected
// with an empty root is an explicit unavailable publication, never Git fallback.
// This optional resolver is not registered by the navigation package.
type RoutedBinding struct {
	Selected   bool
	Identity   string // exact current epoch/owner/custody identity, not merely root
	RootDigest string
	Source     typedindex.Source
}

func (b RoutedBinding) identity() string {
	return fmt.Sprintf("%t\x00%s\x00%s\x00%v", b.Selected, b.Identity, b.RootDigest, b.Source)
}

// These narrow interfaces break the existing lifecycle→extract→codenav cycle.
// The trusted resolver adapter owns concrete workspace/store authority; metadata
// may only be reused by that adapter for the exact binding it authenticated.
type RoutedMetadata interface {
	Routing() typedindex.Routing
	AccountedBytes() int64
}
type RoutedReader interface {
	ReadMember(context.Context, string) ([]byte, error)
	ReadGenerated(context.Context, string) ([]byte, error)
	Close() error
}
type RoutedResolver interface {
	ResolveRoutedIndex(context.Context, string, string) (RoutedBinding, error)
	OpenRoutedIndex(context.Context, RoutedBinding, RoutedMetadata) (RoutedReader, RoutedMetadata, error)
}

type routedItem struct {
	key        string
	repo       string
	retired    bool
	metadata   RoutedMetadata
	snapshot   *snapshot
	err        error
	bytes      int64
	refs       int
	generation uint64
	element    *list.Element
}
type routedCache struct {
	mu           sync.Mutex
	load         chan struct{}
	entries      map[string]*routedItem
	lru          *list.List
	bytes, limit int64
	count        int
	generation   uint64
	queries      chan struct{}
}

func newRoutedCache(o Options) *routedCache {
	b := o.MaxRoutedCacheBytes
	if b <= 0 {
		b = 64 << 20
	}
	n := o.MaxRoutedCacheEntries
	if n <= 0 {
		n = 64
	}
	q := o.MaxRoutedQueries
	if q <= 0 {
		q = 4
	}
	if q > 16 {
		q = 16
	}
	return &routedCache{entries: map[string]*routedItem{}, lru: list.New(), limit: b, count: n, queries: make(chan struct{}, q), load: make(chan struct{}, 1)}
}
func (c *routedCache) epoch() uint64 { c.mu.Lock(); defer c.mu.Unlock(); return c.generation }
func (c *routedCache) get(key string) *routedItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e != nil && !e.retired {
		e.refs++
		c.lru.MoveToFront(e.element)
	}
	if e != nil && e.retired {
		return nil
	}
	return e
}
func (c *routedCache) put(e *routedItem) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.generation != c.generation {
		return ErrBindingChanged
	}
	if c.entries[e.key] != nil {
		return ErrCacheBudget
	}
	if e.bytes > c.limit {
		return ErrCacheBudget
	}
	for c.bytes+e.bytes > c.limit || len(c.entries) >= c.count {
		var victim *routedItem
		for p := c.lru.Back(); p != nil; p = p.Prev() {
			v := p.Value.(*routedItem)
			if v.refs == 0 {
				victim = v
				break
			}
		}
		if victim == nil {
			return ErrCacheBudget
		}
		delete(c.entries, victim.key)
		c.bytes -= victim.bytes
		c.lru.Remove(victim.element)
	}
	e.refs = 1
	e.element = c.lru.PushFront(e)
	c.entries[e.key] = e
	c.bytes += e.bytes
	return nil
}
func (c *routedCache) release(entries []*routedItem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		e.refs--
		if e.refs == 0 && e.retired {
			delete(c.entries, e.key)
			c.bytes -= e.bytes
			c.lru.Remove(e.element)
		}
	}
}
func (s *Service) finishQuery(ctx context.Context, e cacheEntry, q Query, success bool) error {
	if e.finish != nil {
		return e.finish(ctx, success)
	}
	if !success {
		return nil
	}
	if s.routedResolver != nil {
		b, err := s.routedResolver.ResolveRoutedIndex(ctx, q.Repo, q.Revision)
		if err != nil {
			return err
		}
		if b.Selected {
			return ErrBindingChanged
		}
	}
	return s.validateResultBinding(ctx, q.Repo, q.Revision, e.bindingIdentity)
}

func (s *Service) resolveRouted(ctx context.Context, q Query) (entry cacheEntry, doc *document, occurrence *scip.Occurrence, converter *rangeConverter, selected bool, err error) {
	b, err := s.routedResolver.ResolveRoutedIndex(ctx, q.Repo, q.Revision)
	if err != nil {
		return entry, nil, nil, nil, false, err
	}
	if !b.Selected {
		return entry, nil, nil, nil, false, nil
	}
	selected = true
	select {
	case s.routed.queries <- struct{}{}:
	case <-ctx.Done():
		return entry, nil, nil, nil, true, ctx.Err()
	}
	var held []*routedItem
	var publication RoutedReader
	finish := func(check bool) error {
		defer func() { s.routed.release(held); <-s.routed.queries }()
		var e error
		if check {
			current, lookupErr := s.routedResolver.ResolveRoutedIndex(ctx, q.Repo, q.Revision)
			e = lookupErr
			if e == nil && current.identity() != b.identity() {
				e = ErrBindingChanged
			}
		}
		if publication != nil {
			e = errors.Join(e, publication.Close())
		}
		return e
	}
	deferred := true
	defer func() {
		if deferred {
			err = errors.Join(err, finish(false))
		}
	}()
	entry.finish = func(_ context.Context, check bool) error { return finish(check) }
	if b.RootDigest == "" {
		deferred = false
		return entry, nil, nil, nil, true, nil
	}
	source := b.Source
	if source.Repository != q.Repo || source.Commit != q.Revision || !validUnitDigest(b.Identity) || !validUnitDigest(b.RootDigest) {
		return entry, nil, nil, nil, true, ErrTypedIndexBinding
	}
	generation := s.routed.epoch()
	key := "routing\x00" + b.identity()
	// Warm opens use the already-accounted metadata independently. Only one cold
	// construction runs at a time; waiting for that slot respects cancellation.
	cached := s.routed.get(key)
	var meta RoutedMetadata
	if cached == nil {
		select {
		case s.routed.load <- struct{}{}:
		case <-ctx.Done():
			return entry, nil, nil, nil, true, ctx.Err()
		}
		cached = s.routed.get(key)
		if cached == nil {
			publication, meta, err = s.routedResolver.OpenRoutedIndex(ctx, b, nil)
			if err == nil && !routedMetadataMatches(publication, meta, b) {
				err = ErrTypedIndexBinding
			}
			if err == nil {
				cached = &routedItem{generation: generation, key: key, repo: q.Repo, metadata: meta, bytes: 256 + int64(len(key)) + meta.AccountedBytes()}
				if err = s.routed.put(cached); err == nil {
					held = append(held, cached)
				}
			}
		} else {
			held = append(held, cached)
		}
		<-s.routed.load
	} else {
		held = append(held, cached)
	}
	if err == nil && publication == nil {
		publication, meta, err = s.routedResolver.OpenRoutedIndex(ctx, b, cached.metadata)
		if err == nil && !routedMetadataMatches(publication, meta, b) {
			err = ErrTypedIndexBinding
		}
	}
	if err != nil {
		return entry, nil, nil, nil, true, err
	}
	routing := meta.Routing()
	route, ok := routing.Document(q.Path)
	// Publication availability does not invent membership for an absent path.
	count, occurrences := routing.Counts()
	entry.index = &snapshot{documentCount: count, retainedOccurrences: occurrences}
	if !ok && q.Path != "" {
		deferred = false
		return entry, nil, nil, nil, true, nil
	}
	loaded := map[string]*snapshot{}
	load := func(name string) error {
		if loaded[name] != nil {
			return nil
		}
		if len(loaded) >= typedindex.MaxSCIPMembers {
			return ErrSemanticLimit
		}
		memberKey := key + "\x00" + name
		item := s.routed.get(memberKey)
		if item == nil {
			select {
			case s.routed.load <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-s.routed.load }()
			item = s.routed.get(memberKey)
		}
		if item == nil {
			raw, e := publication.ReadMember(ctx, name)
			if e != nil {
				return e
			} // Physical reads may recover; only decoded-byte failures are cached.
			if e == nil && int64(len(raw)) > s.maxIndexBytes {
				e = ErrIndexTooLarge
			}
			var parsed *snapshot
			if e == nil {
				e = routing.VerifyMember(ctx, name, raw)
			}
			if e == nil {
				parsed, e = parseSnapshot(ctx, raw, s.parseLimits)
			}
			if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return e
			}
			item = &routedItem{generation: generation, key: memberKey, repo: q.Repo, snapshot: parsed, err: e, bytes: 256 + int64(len(memberKey))}
			if parsed != nil {
				item.bytes += parsed.estimatedBytes
			}
			if e != nil {
				item.bytes += int64(len(e.Error()))
			}
			if cacheErr := s.routed.put(item); cacheErr != nil {
				return errors.Join(e, cacheErr)
			}
		}
		held = append(held, item)
		if item.err != nil {
			return item.err
		}
		loaded[name] = item.snapshot
		return nil
	}
	if q.Path == "" {
		// Explicit Ingest force-loads every member to report navigable counts,
		// excluding charged but symbol-empty occurrences just like legacy SCIP.
		entry.index = &snapshot{}
		for _, ref := range routing.Files() {
			if _, member := routing.Member(ref.Name); !member {
				continue
			}
			if err = load(ref.Name); err != nil {
				return entry, nil, nil, nil, true, err
			}
			entry.index.documentCount += len(loaded[ref.Name].documents)
			entry.index.retainedOccurrences += loaded[ref.Name].retainedOccurrences
		}
		deferred = false
		return entry, nil, nil, nil, true, nil
	}
	if err = load(route.Member); err != nil {
		return entry, nil, nil, nil, true, err
	}
	doc = loaded[route.Member].documents[q.Path]
	if doc == nil {
		deferred = false
		return entry, nil, nil, nil, true, nil
	}
	converter = newRangeConverter(s, ctx, q.Repo, q.Revision, q.Encoding)
	converter.readSource = func(ctx context.Context, name string, limit int64) ([]byte, error) {
		if typedindex.IsGeneratedPath(name) {
			d, ok := routing.Generated(name)
			if !ok {
				return nil, ErrTypedIndexBinding
			}
			if d.Bytes > limit {
				return nil, ErrSourceTooLarge
			}
			return publication.ReadGenerated(ctx, name)
		}
		return s.readBlob(ctx, q.Repo, q.Revision, name, limit, ErrSourceTooLarge)
	}
	character, e := converter.position(q.Path, q.Line, q.Character, q.Encoding, doc.encoding)
	if e != nil {
		return entry, nil, nil, nil, true, e
	}
	for _, o := range scip.FindOccurrences(doc.occurrences, q.Line, character) {
		if o.Symbol != "" {
			occurrence = o
			break
		}
	}
	if occurrence == nil {
		deferred = false
		return entry, doc, nil, converter, true, nil
	}
	start := symbolKey(doc.path, occurrence.Symbol)
	for _, name := range routing.SymbolMembers(doc.path, occurrence.Symbol) {
		if err = load(name); err != nil {
			return entry, nil, nil, nil, true, err
		}
	}
	keys := map[string]bool{start: true}
	// Routes include relationship endpoints, so loading the queried symbol brings
	// direct outgoing and incoming reference declarations. Preserve legacy one-hop
	// query semantics; loading related postings does not expand the semantic family.
	for _, index := range loaded {
		for _, k := range index.relatedDefinitionSymbols(start) {
			keys[k] = true
		}
		for _, k := range index.relatedReferenceSymbols(start) {
			keys[k] = true
		}
	}
	for k := range keys {
		local, symbol := splitRoutedSymbol(k)
		for _, name := range routing.SymbolMembers(local, symbol) {
			if err = load(name); err != nil {
				return entry, nil, nil, nil, true, err
			}
		}
	}
	entry.index = routedView(loaded, keys, start, doc)
	deferred = false
	return entry, doc, occurrence, converter, true, nil
}
func routedMetadataMatches(p RoutedReader, m RoutedMetadata, b RoutedBinding) bool {
	return p != nil && m != nil && m.Routing().Digest() == b.RootDigest && m.Routing().Root().Binding.Source == b.Source && m.AccountedBytes() > 0
}
func splitRoutedSymbol(k string) (string, string) {
	if i := strings.IndexByte(k, 0); i >= 0 {
		return k[:i], k[i+1:]
	}
	return "", k
}
func routedView(members map[string]*snapshot, keys map[string]bool, start string, doc *document) *snapshot {
	out := &snapshot{documents: map[string]*document{doc.path: doc}, definitions: map[string][]indexedLocation{}, references: map[string][]indexedLocation{}, symbols: map[string]*symbolInfo{}, referenceTargets: map[string]map[string]struct{}{}, referenceSources: map[string]map[string]struct{}{}}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		index := members[name]
		for k := range keys {
			out.definitions[k] = append(out.definitions[k], index.definitions[k]...)
			out.references[k] = append(out.references[k], index.references[k]...)
		}
		if info := index.symbols[start]; info != nil && out.symbols[start] == nil {
			out.symbols[start] = info
		}
		for _, pair := range []struct {
			source, dest map[string]map[string]struct{}
		}{{index.referenceTargets, out.referenceTargets}, {index.referenceSources, out.referenceSources}} {
			if pair.dest[start] == nil {
				pair.dest[start] = map[string]struct{}{}
			}
			for k := range pair.source[start] {
				pair.dest[start][k] = struct{}{}
			}
		}
	}
	return out
}

func (c *routedCache) remove(repo string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++ // In-flight cold results may not repopulate after invalidation.
	for k, e := range c.entries {
		if e.repo != repo {
			continue
		}
		e.retired = true
		if e.refs == 0 {
			delete(c.entries, k)
			c.bytes -= e.bytes
			c.lru.Remove(e.element)
		}
	}
}
