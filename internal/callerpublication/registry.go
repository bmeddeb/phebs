package callerpublication

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

const (
	// The registry is a parsed-state cache, not the durable authority. These
	// process-wide ceilings retain at most two maximum-size 16,384-pair
	// publications so one exact old/replacement comparison can hold both
	// immutable leases. Smaller publications may occupy the remaining identity
	// slots within the same pair-reference ceiling. Store-authoritative bytes
	// remain cold-openable.
	MaxRegistryPublications = 8
	MaxRegistryPairRefs     = 2 * callerleaf.MaxExpectedPairs
	// MaxConcurrentColdAdmissions bounds cross-repository manifest/leaf content
	// validation. Warm leases never enter this gate.
	MaxConcurrentColdAdmissions = 2

	// MaxRegistryAuthorityTokens is the installation-wide ceiling for compact
	// cleanup authority retained after parsed-state eviction. Each token is only
	// one cryptographic repository-directory name and one exact manifest
	// basename; the manifest reconstructs and validates the full cleanup receipt
	// on retirement. This matches the durable current-publication row ceiling.
	MaxRegistryAuthorityTokens = callerpublicationid.InstallationPublicationRepositories
	// MaxRegistryAuthorityIdentityBytes counts the fixed-length map key and
	// manifest-name payloads, excluding Go map/string headers. Both identities
	// are digest-derived and therefore have no input-dependent length.
	MaxRegistryAuthorityIdentityBytes = MaxRegistryAuthorityTokens *
		(len("phebs-caller-overlay-") + 64 +
			len(callerpublicationid.ManifestPrefix) + 64 + 1 + 64 +
			len(".manifest.json"))
)

type registryAuthority struct {
	manifest string
}

type registryEntry struct {
	publication *Publication
	refs        int
	retired     bool
}

type registrySlot struct {
	// transition serializes cold admission, authority replacement, retirement,
	// and last-release filesystem reclamation for one repository. The registry
	// mutex is never held across those bounded filesystem operations.
	transition         chan struct{}
	users              int
	current            *registryEntry
	retired            map[*registryEntry]struct{}
	pending            map[*registryEntry]struct{}
	busy               bool
	removeWhenUnused   bool
	replacementPending bool
}

// Registry owns process-local reader leases for complete immutable
// generations. Store revisions remain the visibility/result fence; this
// registry only prevents retired leaf bytes from being removed while a reader
// still holds them.
type Registry struct {
	root           string
	coldAdmissions chan struct{}
	mu             sync.Mutex
	slots          map[string]*registrySlot
	entries        map[*registryEntry]*registrySlot
	// authorities is keyed by the same cryptographic repository directory that
	// owns the physical publication. It deliberately retains neither repository
	// spelling, semantic generation, pair arrays, nor aggregate receipts.
	authorities map[string]registryAuthority
	pairRefs    int
	closed      bool
}

type Lease struct {
	registry *Registry
	entry    *registryEntry
	// publication is the descriptor snapshot admitted for this lease. A
	// same-state on-disk replacement may refresh entry.publication for future
	// acquisitions without changing the snapshot already handed to a reader.
	publication *Publication
	once        sync.Once
	released    atomic.Bool
}

// RecordReference binds one leaf-local record reference to the exact pair and
// artifact that produced it. PairIndex is an implementation position rather
// than caller authority; the pair digest and artifact name are rechecked on
// every exact reread.
type RecordReference struct {
	PairIndex    int
	PairDigest   string
	ArtifactName string
	Record       callerleaf.RecordReference
}

func NewRegistry(root string) *Registry {
	return &Registry{
		root:           root,
		coldAdmissions: make(chan struct{}, MaxConcurrentColdAdmissions),
		slots:          make(map[string]*registrySlot),
		entries:        make(map[*registryEntry]*registrySlot),
		authorities:    make(map[string]registryAuthority),
	}
}

func (registry *Registry) beginColdAdmission(ctx context.Context) (func(), error) {
	select {
	case registry.coldAdmissions <- struct{}{}:
		return func() { <-registry.coldAdmissions }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newRegistrySlot() *registrySlot {
	slot := &registrySlot{
		transition: make(chan struct{}, 1),
		retired:    make(map[*registryEntry]struct{}),
		pending:    make(map[*registryEntry]struct{}),
	}
	slot.transition <- struct{}{}
	return slot
}

func (registry *Registry) loadSlot(
	repository string,
	create bool,
) (*registrySlot, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	slot := registry.slots[repository]
	if slot == nil && create && !registry.closed {
		slot = newRegistrySlot()
		registry.slots[repository] = slot
	}
	if slot != nil {
		slot.users++
	}
	return slot, registry.closed
}

// loadAuthoritySlot reconstructs only the transient serialization slot needed
// to retire compact authority after parsed-state eviction. Unlike new reader
// admission, exact retirement remains available after Close.
func (registry *Registry) loadAuthoritySlot(repository string) *registrySlot {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	slot := registry.slots[repository]
	if slot == nil {
		if _, present := registry.authorityLocked(repository); !present {
			return nil
		}
		slot = newRegistrySlot()
		registry.slots[repository] = slot
	}
	slot.users++
	return slot
}

func authorityKey(repository string) string {
	return callerpublicationid.RepositoryDirectory(repository)
}

func (registry *Registry) authorityLocked(
	repository string,
) (registryAuthority, bool) {
	authority, present := registry.authorities[authorityKey(repository)]
	return authority, present
}

func (registry *Registry) setAuthorityWithinLimitLocked(
	repository string,
	authority registryAuthority,
	limit int,
) bool {
	key := authorityKey(repository)
	if _, present := registry.authorities[key]; !present && len(registry.authorities) >= limit {
		return false
	}
	registry.authorities[key] = authority
	return true
}

func (registry *Registry) setAuthorityLocked(
	repository string,
	state State,
) bool {
	return registry.setAuthorityWithinLimitLocked(
		repository, registryAuthority{manifest: state.Manifest},
		MaxRegistryAuthorityTokens,
	)
}

func (registry *Registry) clearAuthorityLocked(repository string) {
	delete(registry.authorities, authorityKey(repository))
}

func (registry *Registry) beginTransition(
	ctx context.Context,
	slot *registrySlot,
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-slot.transition:
	}
	registry.mu.Lock()
	slot.busy = true
	registry.mu.Unlock()
	return nil
}

func (registry *Registry) endTransition(
	repository string,
	slot *registrySlot,
) {
	registry.mu.Lock()
	slot.busy = false
	registry.releaseSlotLocked(repository, slot)
	registry.mu.Unlock()
	slot.transition <- struct{}{}
}

func (registry *Registry) releaseSlot(repository string, slot *registrySlot) {
	registry.mu.Lock()
	registry.releaseSlotLocked(repository, slot)
	registry.mu.Unlock()
}

func (registry *Registry) releaseSlotLocked(
	repository string,
	slot *registrySlot,
) {
	if slot == nil || slot.users <= 0 {
		return
	}
	slot.users--
	registry.pruneSlotLocked(repository, slot)
}

func (registry *Registry) pruneSlotLocked(
	repository string,
	slot *registrySlot,
) {
	if slot.users == 0 && !slot.busy && slot.current == nil &&
		len(slot.retired) == 0 && len(slot.pending) == 0 &&
		!slot.removeWhenUnused &&
		!slot.replacementPending &&
		registry.slots[repository] == slot {
		delete(registry.slots, repository)
	}
}

func (registry *Registry) trackEntryLocked(
	slot *registrySlot,
	entry *registryEntry,
) bool {
	if _, tracked := registry.entries[entry]; tracked {
		return true
	}
	wantedPairs := len(entry.publication.manifest.Pairs)
	for len(registry.entries)+1 > MaxRegistryPublications ||
		registry.pairRefs+wantedPairs > MaxRegistryPairRefs {
		var victim *registryEntry
		var victimSlot *registrySlot
		for candidate, candidateSlot := range registry.entries {
			if candidate.refs == 0 && !candidateSlot.busy &&
				candidateSlot.current == candidate {
				victim, victimSlot = candidate, candidateSlot
				break
			}
		}
		if victim == nil {
			return false
		}
		victimSlot.current = nil
		registry.untrackEntryLocked(victim)
		registry.pruneSlotLocked(
			victim.publication.state.Generation.Repository, victimSlot,
		)
	}
	registry.entries[entry] = slot
	registry.pairRefs += wantedPairs
	return true
}

func (registry *Registry) untrackEntryLocked(entry *registryEntry) {
	if _, tracked := registry.entries[entry]; !tracked {
		return
	}
	delete(registry.entries, entry)
	registry.pairRefs -= len(entry.publication.manifest.Pairs)
}

// Acquire returns a reference-counted exact generation after its caller has
// rechecked that State against the store pointer. The current cached
// publication takes only identity checks. A cold miss is serialized per
// repository and may fill an empty slot or refresh the same logical state, but
// it never replaces a different current generation; only Observe may perform
// that store-authoritative transition.
func (registry *Registry) Acquire(
	ctx context.Context,
	state State,
) (*Lease, error) {
	if registry == nil || ValidateState(state) != nil {
		return nil, errors.New("caller publication registry request is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repository := state.Generation.Repository

	// Fast path: take a ref before checking captured file identities. Observe
	// can then retire the entry, but cannot reclaim its leaves until Release.
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return nil, errors.New("caller publication registry is closed")
	}
	slot := registry.slots[repository]
	var entry *registryEntry
	var publication *Publication
	if slot != nil && !slot.removeWhenUnused && slot.current != nil &&
		reflect.DeepEqual(slot.current.publication.state, state) {
		entry = slot.current
		publication = entry.publication
		entry.refs++
	}
	registry.mu.Unlock()
	if entry != nil {
		lease := &Lease{
			registry: registry, entry: entry, publication: publication,
		}
		current, err := publication.CurrentResultContext(ctx)
		if err != nil {
			_ = lease.Release()
			return nil, err
		}
		if current {
			return lease, nil
		}
		_ = lease.Release()
	}

	slot, closed := registry.loadSlot(repository, true)
	if closed || slot == nil {
		registry.releaseSlot(repository, slot)
		return nil, errors.New("caller publication registry is closed")
	}
	if err := registry.beginTransition(ctx, slot); err != nil {
		registry.releaseSlot(repository, slot)
		return nil, err
	}
	defer registry.endTransition(repository, slot)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return nil, errors.New("caller publication registry is closed")
	}
	if slot.removeWhenUnused {
		registry.mu.Unlock()
		return nil, ErrRegistryConflict
	}
	if authority, present := registry.authorityLocked(repository); present && authority.manifest != state.Manifest {
		registry.mu.Unlock()
		return nil, ErrRegistryConflict
	}
	entry = slot.current
	registry.mu.Unlock()
	if entry != nil && !reflect.DeepEqual(entry.publication.state, state) {
		return nil, ErrRegistryConflict
	}
	entryCurrent := false
	if entry != nil {
		var err error
		entryCurrent, err = entry.publication.CurrentResultContext(ctx)
		if err != nil {
			return nil, err
		}
	}
	if entry != nil && entryCurrent {
		registry.mu.Lock()
		if registry.closed || slot.removeWhenUnused || slot.current != entry {
			registry.mu.Unlock()
			return nil, ErrRegistryConflict
		}
		entry.refs++
		publication = entry.publication
		registry.mu.Unlock()
		return &Lease{
			registry: registry, entry: entry, publication: publication,
		}, nil
	}

	finishColdAdmission, err := registry.beginColdAdmission(ctx)
	if err != nil {
		return nil, err
	}
	publication, err = Open(ctx, registry.root, state)
	finishColdAdmission()
	if err != nil {
		return nil, err
	}
	publicationCurrent, err := publication.CurrentResultContext(ctx)
	if err != nil {
		return nil, err
	}
	if !publicationCurrent {
		return nil, ErrRegistryConflict
	}
	// A prior authoritative Observe may have been unable to cache this state
	// while a replaced max-size publication was lease-pinned. Once that lease
	// releases, reclaim its pending parsed entry with this exact new manifest
	// protected before retrying cache admission.
	if err := registry.cleanupPending(
		ctx, repository, slot, &publication.manifest,
	); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	publicationCurrent, err = publication.CurrentResultContext(ctx)
	if err != nil {
		return nil, err
	}
	if !publicationCurrent {
		return nil, ErrRegistryConflict
	}
	registry.mu.Lock()
	if registry.closed || slot.removeWhenUnused || slot.current != entry {
		registry.mu.Unlock()
		return nil, ErrRegistryConflict
	}
	// Acquire is reached only after its caller has rechecked the exact store
	// pointer. Preserve its compact durable authority separately from the
	// bounded parsed cache so a later eviction can reconstruct the full
	// manifest cleanup receipt during Retire.
	if !registry.setAuthorityLocked(repository, state) {
		registry.mu.Unlock()
		return nil, fmt.Errorf(
			"%w: caller publication registry authority is full",
			callerleaf.ErrCapacity,
		)
	}
	if entry == nil {
		entry = &registryEntry{publication: publication}
		if !registry.trackEntryLocked(slot, entry) {
			registry.mu.Unlock()
			return nil, fmt.Errorf(
				"%w: caller publication registry cache is full",
				callerleaf.ErrCapacity,
			)
		}
		slot.current = entry
	} else {
		// Same logical immutable state, new descriptor snapshot. Existing leases
		// retain their old snapshot while future acquisitions use this one.
		entry.publication = publication
	}
	entry.refs++
	registry.mu.Unlock()
	lease := &Lease{
		registry: registry, entry: entry, publication: publication,
	}
	publicationCurrent, err = publication.CurrentResultContext(ctx)
	if err != nil || !publicationCurrent {
		// The per-repository transition is already held, so unwind directly
		// rather than recursively entering Release.
		registry.mu.Lock()
		entry.refs--
		registry.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, ErrRegistryConflict
	}
	return lease, nil
}

// Observe installs one already cold-validated store-authoritative publication
// as the process current entry. A prior manifest is removed immediately;
// unshared prior leaf bytes are reclaimed only after its last lease releases.
func (registry *Registry) Observe(
	ctx context.Context,
	publication *Publication,
) error {
	if registry == nil || publication == nil {
		return errors.New("caller publication registry observation is not current")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	repository := publication.state.Generation.Repository
	slot, closed := registry.loadSlot(repository, true)
	if closed || slot == nil {
		registry.releaseSlot(repository, slot)
		return errors.New("caller publication registry is closed")
	}
	if err := registry.beginTransition(ctx, slot); err != nil {
		registry.releaseSlot(repository, slot)
		return err
	}
	defer registry.endTransition(repository, slot)
	if !publication.Current() {
		return errors.New("caller publication registry observation is not current")
	}
	if _, err := clearRepositoryDeleting(registry.root, repository); err != nil {
		return err
	}

	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return errors.New("caller publication registry is closed")
	}
	current := slot.current
	registry.mu.Unlock()
	if current != nil &&
		reflect.DeepEqual(current.publication.state, publication.state) {
		if current.publication.Current() {
			registry.mu.Lock()
			if !registry.setAuthorityLocked(repository, publication.state) {
				registry.mu.Unlock()
				return fmt.Errorf(
					"%w: caller publication registry authority is full",
					callerleaf.ErrCapacity,
				)
			}
			slot.removeWhenUnused = false
			slot.replacementPending = false
			registry.mu.Unlock()
			return registry.cleanupPending(
				ctx, repository, slot, &publication.manifest,
			)
		}
		registry.mu.Lock()
		if registry.closed || slot.current != current {
			registry.mu.Unlock()
			return ErrRegistryConflict
		}
		if !registry.setAuthorityLocked(repository, publication.state) {
			registry.mu.Unlock()
			return fmt.Errorf(
				"%w: caller publication registry authority is full",
				callerleaf.ErrCapacity,
			)
		}
		current.publication = publication
		slot.removeWhenUnused = false
		slot.replacementPending = false
		registry.mu.Unlock()
		return registry.cleanupPending(
			ctx, repository, slot, &publication.manifest,
		)
	}

	registry.mu.Lock()
	if registry.closed || slot.current != current {
		registry.mu.Unlock()
		return ErrRegistryConflict
	}
	if !registry.setAuthorityLocked(repository, publication.state) {
		registry.mu.Unlock()
		return fmt.Errorf(
			"%w: caller publication registry authority is full",
			callerleaf.ErrCapacity,
		)
	}
	if current != nil {
		current.retired = true
		if current.refs > 0 {
			slot.retired[current] = struct{}{}
		} else {
			slot.pending[current] = struct{}{}
		}
	}
	slot.current = nil
	slot.removeWhenUnused = false
	slot.replacementPending = false
	keepLeaves := artifactKeepSetLocked(slot)
	keepManifests := manifestKeepSetLocked(slot)
	addArtifactNames(keepLeaves, publication.manifest)
	registry.mu.Unlock()
	if err := cleanupPublicationResidue(
		ctx, registry.root, publication.state, keepLeaves, keepManifests,
	); err != nil {
		return err
	}
	if err := registry.cleanupPending(
		ctx, repository, slot, &publication.manifest,
	); err != nil {
		return err
	}
	registry.mu.Lock()
	authority, authorityPresent := registry.authorityLocked(repository)
	if registry.closed || slot.removeWhenUnused || slot.current != nil ||
		!authorityPresent || authority.manifest != publication.state.Manifest {
		registry.mu.Unlock()
		return ErrRegistryConflict
	}
	entry := &registryEntry{publication: publication}
	if !registry.trackEntryLocked(slot, entry) {
		registry.mu.Unlock()
		return fmt.Errorf(
			"%w: caller publication registry cache is full",
			callerleaf.ErrCapacity,
		)
	}
	slot.current = entry
	registry.mu.Unlock()
	return nil
}

// Retire removes current visibility from the process cache. Its immutable
// manifest is not needed by an existing lease, which holds parsed state; leaf
// bytes remain until the last reference releases.
func (registry *Registry) Retire(
	ctx context.Context,
	repository string,
) error {
	if registry == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	slot := registry.loadAuthoritySlot(repository)
	if slot == nil {
		return nil
	}
	if err := registry.beginTransition(ctx, slot); err != nil {
		registry.releaseSlot(repository, slot)
		return err
	}
	defer registry.endTransition(repository, slot)
	registry.mu.Lock()
	current := slot.current
	authority, authorityPresent := registry.authorityLocked(repository)
	if current == nil {
		registry.mu.Unlock()
		if authorityPresent {
			manifest, err := readExactManifest(
				ctx, registry.root, repository, authority,
			)
			if err != nil {
				return err
			}
			registry.mu.Lock()
			keepLeaves := artifactKeepSetLocked(slot)
			registry.mu.Unlock()
			if err := removeArtifactRefsExcept(
				ctx, registry.root, manifest, true, keepLeaves,
			); err != nil {
				return err
			}
			registry.mu.Lock()
			registry.clearAuthorityLocked(repository)
			registry.mu.Unlock()
		}
		return registry.cleanupPending(ctx, repository, slot, nil)
	}
	slot.current = nil
	registry.clearAuthorityLocked(repository)
	current.retired = true
	if current.refs > 0 {
		slot.retired[current] = struct{}{}
	} else {
		slot.pending[current] = struct{}{}
	}
	registry.mu.Unlock()
	return registry.cleanupPending(ctx, repository, slot, nil)
}

// readExactManifest reconstructs the bounded cleanup receipt for an
// authoritative publication evicted from the parsed-state cache. It performs
// a descriptor-stable manifest read and exact repository/name comparison, but
// does not hash or open leaf content merely to retire it.
func readExactManifest(
	ctx context.Context,
	root string,
	repository string,
	authority registryAuthority,
) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if !callerpublicationid.ValidManifestName(authority.manifest) {
		return Manifest{}, fmt.Errorf(
			"%w: authoritative cleanup manifest identity is invalid",
			ErrInvalidManifest,
		)
	}
	_, rootAuthority, err := openRepositoryAuthority(
		root, repository, false,
	)
	if err != nil {
		return Manifest{}, err
	}
	defer rootAuthority.close()
	raw, _, err := readStableRegularAt(
		rootAuthority, authority.manifest, MaxManifestBytes,
	)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := decodeManifest(raw)
	if err != nil || manifest.Generation.Repository != repository ||
		manifest.State().Manifest != authority.manifest {
		return Manifest{}, fmt.Errorf(
			"%w: authoritative cleanup manifest differs", ErrInvalidManifest,
		)
	}
	return manifest, nil
}

// ActivateRepository cancels a durable leased-deletion tombstone before a
// same-name live repository starts installing new caller artifacts. Until its
// store-authoritative Observe arrives, final old-lease release preserves every
// basename because it does not yet have the successor's exact keep set.
func (registry *Registry) ActivateRepository(
	ctx context.Context,
	repository string,
) error {
	if registry == nil {
		return errors.New("caller publication registry is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	slot := registry.loadAuthoritySlot(repository)
	if slot == nil {
		_, err := clearRepositoryDeleting(registry.root, repository)
		return err
	}
	if err := registry.beginTransition(ctx, slot); err != nil {
		registry.releaseSlot(repository, slot)
		return err
	}
	defer registry.endTransition(repository, slot)
	cleared, err := clearRepositoryDeleting(registry.root, repository)
	if err != nil || !cleared {
		return err
	}
	registry.mu.Lock()
	// Keep the old deletion lifecycle active until either Observe supplies the
	// successor's exact keep set or the last old lease releases. The paired
	// replacement flag converts that final release from whole-directory removal
	// into metadata-only retirement, preserving bytes a concurrent successor may
	// already be validating or reusing.
	slot.removeWhenUnused = true
	slot.replacementPending = true
	registry.mu.Unlock()
	return nil
}

// RemoveRepository retires process authority for a repository being deleted.
// Whole-directory removal is immediate without a lease. With a lease, an exact
// tombstone makes final-release removal crash-recoverable and lets a same-name
// replacement cancel deletion before writing new bytes.
func (registry *Registry) RemoveRepository(
	ctx context.Context,
	repository string,
) error {
	if registry == nil {
		return errors.New("caller publication registry is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	slot := registry.loadAuthoritySlot(repository)
	if slot == nil {
		return callerleaf.RemoveRepository(ctx, registry.root, repository)
	}
	if err := registry.beginTransition(ctx, slot); err != nil {
		registry.releaseSlot(repository, slot)
		return err
	}
	defer registry.endTransition(repository, slot)
	registry.mu.Lock()
	current := slot.current
	registry.clearAuthorityLocked(repository)
	if current != nil {
		slot.current = nil
		current.retired = true
		if current.refs > 0 {
			slot.retired[current] = struct{}{}
		} else {
			slot.pending[current] = struct{}{}
		}
	}
	slot.removeWhenUnused = true
	slot.replacementPending = false
	active := len(slot.retired) > 0
	registry.mu.Unlock()
	if !active {
		if err := callerleaf.RemoveRepository(
			ctx, registry.root, repository,
		); err != nil {
			return err
		}
		registry.mu.Lock()
		for entry := range slot.pending {
			registry.untrackEntryLocked(entry)
		}
		clear(slot.pending)
		slot.removeWhenUnused = false
		slot.replacementPending = false
		registry.mu.Unlock()
		return nil
	}
	if err := markRepositoryDeleting(ctx, registry.root, repository); err != nil {
		return err
	}
	// Keep every exact manifest while a reader lease remains. It is the durable
	// cleanup receipt if the process exits before final release.
	return registry.cleanupPending(ctx, repository, slot, nil)
}

func (lease *Lease) Publication() *Publication {
	if lease == nil || lease.entry == nil || lease.publication == nil {
		return nil
	}
	return lease.publication
}

func (lease *Lease) State() State {
	if publication := lease.Publication(); publication != nil {
		return publication.State()
	}
	return State{}
}

// ScanRecords verifies every leaf in the leased complete generation exactly
// once and yields canonical records with exact bounded reread references. The
// visitor must discard any accumulated state when ScanRecords returns an
// error; the complete generation is not accepted until every leaf succeeds.
func (lease *Lease) ScanRecords(
	ctx context.Context,
	visit func(PairReceipt, RecordReference, callerleaf.Record) error,
) error {
	if ctx == nil || lease == nil || lease.released.Load() || visit == nil {
		return errors.New("caller publication record scan requires an active lease")
	}
	publication := lease.Publication()
	if publication == nil || len(publication.manifest.Pairs) != len(publication.leaves) {
		return errors.New("caller publication lease is invalid")
	}
	for pairIndex, pair := range publication.manifest.Pairs {
		if err := ctx.Err(); err != nil {
			return err
		}
		leaf := publication.leaves[pairIndex]
		if leaf == nil {
			return errors.New("caller publication leaf is unavailable")
		}
		err := leaf.ScanRecords(
			ctx, publication.manifest.Generation, pair.Pair,
			func(reference callerleaf.RecordReference, record callerleaf.Record) error {
				return visit(pair, RecordReference{
					PairIndex: pairIndex, PairDigest: pair.Pair.Digest,
					ArtifactName: pair.Receipt.Name, Record: reference,
				}, record)
			},
		)
		if err != nil {
			return err
		}
	}
	if lease.released.Load() {
		return errors.New("caller publication lease was released during record scan")
	}
	return nil
}

// ReadRecord reads and validates only the exact leased record named by a
// ScanRecords reference. It does not hash or materialize another record or
// leaf, making it suitable for bounded result-page hydration.
func (lease *Lease) ReadRecord(
	ctx context.Context,
	reference RecordReference,
) (PairReceipt, callerleaf.Record, error) {
	if ctx == nil || lease == nil || lease.released.Load() {
		return PairReceipt{}, callerleaf.Record{}, fmt.Errorf(
			"%w: caller publication record read requires an active lease",
			callerleaf.ErrInvalidArtifact,
		)
	}
	publication := lease.Publication()
	if publication == nil || reference.PairIndex < 0 ||
		reference.PairIndex >= len(publication.manifest.Pairs) ||
		reference.PairIndex >= len(publication.leaves) {
		return PairReceipt{}, callerleaf.Record{}, fmt.Errorf(
			"%w: caller publication record reference is invalid",
			callerleaf.ErrInvalidArtifact,
		)
	}
	pair := publication.manifest.Pairs[reference.PairIndex]
	if pair.Pair.Digest != reference.PairDigest ||
		pair.Receipt.Name != reference.ArtifactName ||
		publication.leaves[reference.PairIndex] == nil {
		return PairReceipt{}, callerleaf.Record{}, fmt.Errorf(
			"%w: caller publication record reference differs from its lease",
			callerleaf.ErrInvalidArtifact,
		)
	}
	record, err := publication.leaves[reference.PairIndex].ReadRecord(
		ctx, reference.Record,
	)
	if err != nil {
		return PairReceipt{}, callerleaf.Record{}, err
	}
	if lease.released.Load() {
		return PairReceipt{}, callerleaf.Record{}, fmt.Errorf(
			"%w: caller publication lease was released during record read",
			callerleaf.ErrInvalidArtifact,
		)
	}
	return pair, record, nil
}

func (lease *Lease) Release() (resultErr error) {
	if lease == nil || lease.registry == nil || lease.entry == nil {
		return nil
	}
	lease.once.Do(func() {
		lease.released.Store(true)
		registry := lease.registry
		entry := lease.entry
		repository := entry.publication.state.Generation.Repository
		slot, _ := registry.loadSlot(repository, false)
		if slot == nil {
			return
		}
		if err := registry.beginTransition(context.Background(), slot); err != nil {
			registry.releaseSlot(repository, slot)
			resultErr = err
			return
		}
		defer registry.endTransition(repository, slot)
		registry.mu.Lock()
		if entry.refs > 0 {
			entry.refs--
		}
		cleanup := entry.retired && entry.refs == 0
		if cleanup {
			delete(slot.retired, entry)
			slot.pending[entry] = struct{}{}
		}
		removeRepository := slot.removeWhenUnused &&
			slot.current == nil && len(slot.retired) == 0
		hasPending := len(slot.pending) > 0
		registry.mu.Unlock()
		if removeRepository {
			registry.mu.Lock()
			replacementPending := slot.replacementPending
			registry.mu.Unlock()
			if !replacementPending {
				resultErr = callerleaf.RemoveRepository(
					context.Background(), registry.root, repository,
				)
				if resultErr != nil {
					return
				}
			}
			// A same-name repository may already have installed successor bytes
			// before its store-authoritative Observe reaches this registry. Without
			// that new exact keep set, final release must not delete either the whole
			// directory or shared immutable basenames. Drop only process metadata;
			// the retained manifests make the next startup/deletion pass resumable.
			registry.mu.Lock()
			for pending := range slot.pending {
				registry.untrackEntryLocked(pending)
			}
			clear(slot.pending)
			slot.removeWhenUnused = false
			slot.replacementPending = false
			registry.mu.Unlock()
			return
		}
		if cleanup || hasPending {
			resultErr = registry.cleanupPending(
				context.Background(), repository, slot, nil,
			)
		}
	})
	return resultErr
}

func (registry *Registry) cleanupPending(
	ctx context.Context,
	repository string,
	slot *registrySlot,
	protected *Manifest,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	registry.mu.Lock()
	// Observe records durable authority before it attempts cache admission. If
	// admission is temporarily refused by the process-wide cache bound, there
	// is no parsed current entry from which to reconstruct the authoritative
	// leaf keep set. Defer reclamation until Observe/Acquire admits that exact
	// state or Retire clears its authority.
	authority, authorityPresent := registry.authorityLocked(repository)
	if authorityPresent && slot.current == nil && protected == nil {
		registry.mu.Unlock()
		return nil
	}
	pending := make([]*registryEntry, 0, len(slot.pending))
	for entry := range slot.pending {
		pending = append(pending, entry)
	}
	keepLeaves := artifactKeepSetLocked(slot)
	if protected != nil {
		addArtifactNames(keepLeaves, *protected)
	}
	currentManifest := ""
	if authorityPresent {
		currentManifest = authority.manifest
	} else if slot.current != nil {
		currentManifest = slot.current.publication.state.Manifest
	}
	registry.mu.Unlock()
	for index, entry := range pending {
		if index%32 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := removeArtifactRefsExcept(
			ctx, registry.root, entry.publication.manifest, false, keepLeaves,
		); err != nil {
			return err
		}
		if entry.publication.state.Manifest != currentManifest {
			if err := RemoveManifest(
				registry.root, entry.publication.state,
			); err != nil {
				return err
			}
		}
		registry.mu.Lock()
		delete(slot.pending, entry)
		registry.untrackEntryLocked(entry)
		registry.mu.Unlock()
	}
	return nil
}

func artifactKeepSetLocked(slot *registrySlot) map[string]struct{} {
	keep := make(map[string]struct{})
	if slot == nil {
		return keep
	}
	if slot.current != nil {
		addArtifactNames(keep, slot.current.publication.manifest)
	}
	for entry := range slot.retired {
		if entry.refs > 0 {
			addArtifactNames(keep, entry.publication.manifest)
		}
	}
	return keep
}

func manifestKeepSetLocked(slot *registrySlot) map[string]struct{} {
	keep := make(map[string]struct{})
	if slot == nil {
		return keep
	}
	if slot.current != nil {
		keep[slot.current.publication.state.Manifest] = struct{}{}
	}
	for entry := range slot.retired {
		if entry.refs > 0 {
			keep[entry.publication.state.Manifest] = struct{}{}
		}
	}
	return keep
}

func addArtifactNames(names map[string]struct{}, manifest Manifest) {
	for _, pair := range manifest.Pairs {
		names[pair.Receipt.Name] = struct{}{}
	}
}

// RetainedArtifactNames returns a copy of every leaf basename protected by an
// active retired lease. It is the bounded keep set for reconciliation cleanup.
func (registry *Registry) RetainedArtifactNames(repository string) map[string]struct{} {
	result := make(map[string]struct{})
	if registry == nil {
		return result
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	slot := registry.slots[repository]
	if slot == nil {
		return result
	}
	for entry := range slot.retired {
		if entry.refs > 0 {
			addArtifactNames(result, entry.publication.manifest)
		}
	}
	return result
}

func (registry *Registry) Close() error {
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return nil
	}
	registry.closed = true
	registry.mu.Unlock()
	// Closing is only a process-local admission fence. Store pointers remain
	// authoritative, so shutdown must not retire or remove their durable bytes.
	// Active leases may still release normally.
	return nil
}
