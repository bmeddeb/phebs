package servicecatalogv3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"sync"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

const (
	MaxReadCacheRoots   = 8
	MaxReadCacheMembers = 128
)

var ErrReadCacheFull = errors.New("service catalog v3 read cache is full")

// ReadSource is the precious-byte boundary beneath the verified read cache.
// Implementations return a strict-validated root; member bytes are verified
// against that root exactly once when they enter the cache.
type ReadSource interface {
	ReadServiceCatalogV3Root(
		context.Context,
		string,
		string,
	) (Root, error)
	ReadServiceCatalogV3Member(
		context.Context,
		MemberDescriptor,
	) ([]byte, error)
}

// ReadCatalogContext reads every exact-root member once and opens the complete
// logical catalog. It owns no cache; callers decide whether a validated result
// may be retained after their wider authority fences complete.
func ReadCatalogContext(
	ctx context.Context,
	source ReadSource,
	root Root,
) (servicecatalog.Catalog, error) {
	if ctx == nil || source == nil || ValidateRoot(root) != nil {
		return servicecatalog.Catalog{}, ErrInvalid
	}
	root = cloneReadRoot(root)
	descriptors := make([]MemberDescriptor, 0, len(root.ServiceMembers)+len(root.PlacementMembers))
	descriptors = append(descriptors, root.ServiceMembers...)
	descriptors = append(descriptors, root.PlacementMembers...)
	members := make([]EncodedMember, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return servicecatalog.Catalog{}, err
		}
		raw, err := source.ReadServiceCatalogV3Member(ctx, descriptor)
		if err != nil {
			return servicecatalog.Catalog{}, err
		}
		members = append(members, EncodedMember{
			Kind: descriptor.Kind, Ordinal: descriptor.Ordinal, Content: slices.Clone(raw),
		})
	}
	return (Generation{Root: root, Members: members}).CatalogContext(ctx)
}

type readRootKey struct {
	repository string
	digest     string
}

type readMemberKey struct {
	root   string
	member string
}

type readLoad struct {
	done chan struct{}
	err  error
}

type readRootEntry struct {
	root             Root
	sourceGeneration string
	refs             int
	used             uint64
}

type readMemberEntry struct {
	projections []servicecatalog.ServiceProjection
	refs        int
	used        uint64
}

// ReadCache retains only verified roots and decoded service members. Entries
// with an active lease are never evicted; a fill refuses instead of exceeding
// its configured entry bounds when every retirement candidate is leased.
type ReadCache struct {
	mu sync.Mutex

	rootLimit   int
	memberLimit int
	clock       uint64

	roots       map[readRootKey]*readRootEntry
	members     map[readMemberKey]*readMemberEntry
	rootLoads   map[readRootKey]*readLoad
	memberLoads map[readMemberKey]*readLoad

	rootReads         uint64
	memberReads       uint64
	rootValidations   uint64
	memberValidations uint64
}

type ReadCacheStats struct {
	RootEntries       int
	MemberEntries     int
	RootLeases        int
	MemberLeases      int
	RootReads         uint64
	MemberReads       uint64
	RootValidations   uint64
	MemberValidations uint64
}

func NewReadCache(rootLimit, memberLimit int) (*ReadCache, error) {
	if rootLimit < 1 || rootLimit > MaxReadCacheRoots ||
		memberLimit < 1 || memberLimit > MaxReadCacheMembers {
		return nil, fmt.Errorf("%w: invalid entry limits", ErrInvalid)
	}
	return &ReadCache{
		rootLimit: rootLimit, memberLimit: memberLimit,
		roots:       make(map[readRootKey]*readRootEntry),
		members:     make(map[readMemberKey]*readMemberEntry),
		rootLoads:   make(map[readRootKey]*readLoad),
		memberLoads: make(map[readMemberKey]*readLoad),
	}, nil
}

func NewDefaultReadCache() *ReadCache {
	cache, err := NewReadCache(MaxReadCacheRoots, MaxReadCacheMembers)
	if err != nil {
		panic(err)
	}
	return cache
}

// ReadLease pins one root and every service member opened through it until
// Close. Close is idempotent.
type ReadLease struct {
	mu sync.Mutex

	cache            *ReadCache
	rootKey          readRootKey
	root             Root
	sourceGeneration string
	members          map[readMemberKey]*readMemberEntry
	closed           bool
}

func (cache *ReadCache) Open(
	ctx context.Context,
	source ReadSource,
	repository, digest string,
) (*ReadLease, error) {
	if cache == nil || source == nil || repository == "" || !validDigest(digest) {
		return nil, ErrInvalid
	}
	key := readRootKey{repository: repository, digest: digest}
	entry, err := cache.openRoot(ctx, source, key)
	if err != nil {
		return nil, err
	}
	return &ReadLease{
		cache: cache, rootKey: key, root: cloneReadRoot(entry.root),
		sourceGeneration: entry.sourceGeneration,
		members:          make(map[readMemberKey]*readMemberEntry),
	}, nil
}

func (cache *ReadCache) openRoot(
	ctx context.Context,
	source ReadSource,
	key readRootKey,
) (*readRootEntry, error) {
	for {
		cache.mu.Lock()
		if entry := cache.roots[key]; entry != nil {
			cache.touchRootLocked(entry)
			entry.refs++
			cache.mu.Unlock()
			return entry, nil
		}
		if loading := cache.rootLoads[key]; loading != nil {
			done := loading.done
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				if loading.err != nil {
					return nil, loading.err
				}
				continue
			}
		}
		if len(cache.roots)+len(cache.rootLoads) >= cache.rootLimit &&
			!cache.evictRootLocked() {
			cache.mu.Unlock()
			return nil, ErrReadCacheFull
		}
		loading := &readLoad{done: make(chan struct{})}
		cache.rootLoads[key] = loading
		cache.rootReads++
		cache.mu.Unlock()

		root, err := source.ReadServiceCatalogV3Root(
			ctx, key.repository, key.digest,
		)
		cache.mu.Lock()
		cache.rootValidations++
		cache.mu.Unlock()
		if err == nil &&
			(root.Digest != key.digest || root.Binding.Repository != key.repository) {
			err = ErrInvalid
		}
		var sourceGeneration string
		if err == nil {
			sourceGeneration, err = sourceGenerationDigest(root)
		}

		cache.mu.Lock()
		var entry *readRootEntry
		if err == nil {
			entry = &readRootEntry{
				root: cloneReadRoot(root), sourceGeneration: sourceGeneration, refs: 1,
			}
			cache.touchRootLocked(entry)
			cache.roots[key] = entry
		}
		loading.err = err
		delete(cache.rootLoads, key)
		close(loading.done)
		cache.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return entry, nil
	}
}

func (cache *ReadCache) openMember(
	ctx context.Context,
	source ReadSource,
	root Root,
	sourceGeneration string,
	descriptor MemberDescriptor,
) (*readMemberEntry, error) {
	key := readMemberKey{root: root.Digest, member: descriptor.Digest}
	for {
		cache.mu.Lock()
		if entry := cache.members[key]; entry != nil {
			cache.touchMemberLocked(entry)
			entry.refs++
			cache.mu.Unlock()
			return entry, nil
		}
		if loading := cache.memberLoads[key]; loading != nil {
			done := loading.done
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				if loading.err != nil {
					return nil, loading.err
				}
				continue
			}
		}
		if len(cache.members)+len(cache.memberLoads) >= cache.memberLimit &&
			!cache.evictMemberLocked() {
			cache.mu.Unlock()
			return nil, ErrReadCacheFull
		}
		loading := &readLoad{done: make(chan struct{})}
		cache.memberLoads[key] = loading
		cache.memberReads++
		cache.mu.Unlock()

		raw, err := source.ReadServiceCatalogV3Member(ctx, descriptor)
		cache.mu.Lock()
		cache.memberValidations++
		cache.mu.Unlock()
		var projections []servicecatalog.ServiceProjection
		if err == nil {
			projections, err = projectServiceMember(
				ctx, root, descriptor, raw, sourceGeneration,
			)
		}

		cache.mu.Lock()
		var entry *readMemberEntry
		if err == nil {
			entry = &readMemberEntry{
				projections: cloneReadProjections(projections), refs: 1,
			}
			cache.touchMemberLocked(entry)
			cache.members[key] = entry
		}
		loading.err = err
		delete(cache.memberLoads, key)
		close(loading.done)
		cache.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return entry, nil
	}
}

func (lease *ReadLease) Root() (Root, bool) {
	if lease == nil {
		return Root{}, false
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return Root{}, false
	}
	return cloneReadRoot(lease.root), true
}

func (lease *ReadLease) Service(
	ctx context.Context,
	source ReadSource,
	key string,
) (servicecatalog.ServiceProjection, error) {
	if lease == nil || source == nil || key == "" {
		return servicecatalog.ServiceProjection{}, ErrInvalid
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return servicecatalog.ServiceProjection{}, ErrInvalid
	}
	descriptor, found := serviceDescriptor(lease.root, key)
	if !found {
		return servicecatalog.ServiceProjection{}, os.ErrNotExist
	}
	entry, err := lease.memberLocked(ctx, source, descriptor)
	if err != nil {
		return servicecatalog.ServiceProjection{}, err
	}
	index, found := slices.BinarySearchFunc(
		entry.projections, key,
		func(projection servicecatalog.ServiceProjection, target string) int {
			return compareText(projection.Service.Key, target)
		},
	)
	if !found {
		return servicecatalog.ServiceProjection{}, os.ErrNotExist
	}
	return cloneReadProjection(entry.projections[index]), nil
}

func (lease *ReadLease) StreamServices(
	ctx context.Context,
	source ReadSource,
	visit func([]servicecatalog.ServiceProjection) error,
) error {
	if lease == nil || source == nil || visit == nil {
		return ErrInvalid
	}
	for _, descriptor := range lease.root.ServiceMembers {
		lease.mu.Lock()
		if lease.closed {
			lease.mu.Unlock()
			return ErrInvalid
		}
		entry, err := lease.memberLocked(ctx, source, descriptor)
		if err != nil {
			lease.mu.Unlock()
			return err
		}
		projections := cloneReadProjections(entry.projections)
		lease.mu.Unlock()
		if err := visit(projections); err != nil {
			return err
		}
	}
	return nil
}

func (lease *ReadLease) memberLocked(
	ctx context.Context,
	source ReadSource,
	descriptor MemberDescriptor,
) (*readMemberEntry, error) {
	key := readMemberKey{root: lease.root.Digest, member: descriptor.Digest}
	if entry := lease.members[key]; entry != nil {
		return entry, nil
	}
	entry, err := lease.cache.openMember(
		ctx, source, lease.root, lease.sourceGeneration, descriptor,
	)
	if err != nil {
		return nil, err
	}
	lease.members[key] = entry
	return entry, nil
}

func (lease *ReadLease) Close() {
	if lease == nil {
		return
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return
	}
	lease.cache.mu.Lock()
	if entry := lease.cache.roots[lease.rootKey]; entry != nil && entry.refs > 0 {
		entry.refs--
	}
	for key := range lease.members {
		if entry := lease.cache.members[key]; entry != nil && entry.refs > 0 {
			entry.refs--
		}
	}
	lease.cache.mu.Unlock()
	lease.closed = true
	lease.members = nil
}

func (cache *ReadCache) Stats() ReadCacheStats {
	if cache == nil {
		return ReadCacheStats{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	stats := ReadCacheStats{
		RootEntries: len(cache.roots), MemberEntries: len(cache.members),
		RootReads: cache.rootReads, MemberReads: cache.memberReads,
		RootValidations:   cache.rootValidations,
		MemberValidations: cache.memberValidations,
	}
	for _, entry := range cache.roots {
		stats.RootLeases += entry.refs
	}
	for _, entry := range cache.members {
		stats.MemberLeases += entry.refs
	}
	return stats
}

func (cache *ReadCache) touchRootLocked(entry *readRootEntry) {
	cache.clock++
	entry.used = cache.clock
}

func (cache *ReadCache) touchMemberLocked(entry *readMemberEntry) {
	cache.clock++
	entry.used = cache.clock
}

func (cache *ReadCache) evictRootLocked() bool {
	var victim readRootKey
	found := false
	var oldest uint64
	for key, entry := range cache.roots {
		if entry.refs != 0 || found && entry.used >= oldest {
			continue
		}
		victim, oldest, found = key, entry.used, true
	}
	if found {
		delete(cache.roots, victim)
	}
	return found
}

func (cache *ReadCache) evictMemberLocked() bool {
	var victim readMemberKey
	found := false
	var oldest uint64
	for key, entry := range cache.members {
		if entry.refs != 0 || found && entry.used >= oldest {
			continue
		}
		victim, oldest, found = key, entry.used, true
	}
	if found {
		delete(cache.members, victim)
	}
	return found
}

func serviceDescriptor(root Root, key string) (MemberDescriptor, bool) {
	index := sort.Search(len(root.ServiceMembers), func(index int) bool {
		return root.ServiceMembers[index].Last >= key
	})
	if index >= len(root.ServiceMembers) || key < root.ServiceMembers[index].First {
		return MemberDescriptor{}, false
	}
	return root.ServiceMembers[index], true
}

// ServiceMemberDescriptor returns the one immutable service-member range that
// contains key. Product cursors bind this descriptor without opening a member.
func ServiceMemberDescriptor(root Root, key string) (MemberDescriptor, bool) {
	return serviceDescriptor(root, key)
}

func cloneReadRoot(root Root) Root {
	root.ServiceMembers = slices.Clone(root.ServiceMembers)
	root.PlacementMembers = slices.Clone(root.PlacementMembers)
	root.Binding.Override = cloneOverride(root.Binding.Override)
	return root
}

func cloneReadProjection(
	projection servicecatalog.ServiceProjection,
) servicecatalog.ServiceProjection {
	projection.Service.Successors = slices.Clone(projection.Service.Successors)
	projection.Memberships = slices.Clone(projection.Memberships)
	return projection
}

func cloneReadProjections(
	projections []servicecatalog.ServiceProjection,
) []servicecatalog.ServiceProjection {
	result := make([]servicecatalog.ServiceProjection, len(projections))
	for index := range projections {
		result[index] = cloneReadProjection(projections[index])
	}
	return result
}

func compareText(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
