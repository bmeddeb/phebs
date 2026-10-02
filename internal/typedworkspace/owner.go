package typedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const OwnerSchema = "phebs-typed-attempt-owner-v1"
const MaxOwnerBytes = 4096
const MaxOwnerRequests = 4096
const MaxOwnerAttempts = 64

// File path JSON already fits the admitted inventory's 16MiB envelope. Only
// derived directory paths need an additional worst-case six-byte escaping bound;
// 160 bytes per node covers keys, punctuation, two uint64s and the boolean.
const MaxInputReceiptBytes = typedindex.MaxInventoryBytes + (typedindex.MaxInventoryDirectories+1)*(6*512) + (typedindex.MaxInventoryFiles+typedindex.MaxInventoryDirectories+1)*160 + 256
const ownerManifest = "owner.json"
const ownerPending = "owner.next"

// OwnerIdentity is trusted controller authority, never a browser-supplied claim.
// Construct it from the admitted planning request and the exact current lease.
// Only a domain-separated digest of the lease is persisted.
type OwnerIdentity struct {
	PlanningDigest string             `json:"planning_digest"`
	AttemptDigest  string             `json:"attempt_digest"`
	ChunkIdentity  string             `json:"chunk_identity"`
	LeaseDigest    string             `json:"lease_digest"`
	Request        typedindex.Request `json:"request"`
}

func NewOwnerIdentity(parent typedindex.Admission, chunk, lease string) (OwnerIdentity, error) {
	if parent.Digest() == "" || parent.Request().Action != typedindex.Plan || !publicationHash(chunk) || len(lease) == 0 || len(lease) > 512 || strings.ContainsRune(lease, 0) {
		return OwnerIdentity{}, ErrCustody
	}
	return OwnerIdentity{parent.Digest(), publicationDigest([]byte(chunk + "\x00" + lease)), chunk, publicationDigest([]byte("phebs-generation-private-lease-token-v1\x00" + lease)), parent.Request()}, nil
}
func (i OwnerIdentity) valid() bool {
	raw, err := json.Marshal(i.Request)
	return err == nil && i.Request.ValidatePurpose() == nil && len(raw) <= typedindex.MaxRequestBytes && i.Request.Action == typedindex.Plan && i.Request.ParentRequestDigest == "" && i.Request.PlanDigest == "" && publicationDigest(raw) == i.PlanningDigest && publicationHash(i.AttemptDigest) && publicationHash(i.ChunkIdentity) && publicationHash(i.LeaseDigest)
}
func (i OwnerIdentity) RelativeName() string {
	if !i.valid() {
		return ""
	}
	return i.PlanningDigest[7:] + "/" + i.AttemptDigest[7:]
}

// OwnerBudget covers the entire attempt's peak promised growth on this actual
// filesystem: copied inputs, peak receipt/control encodings, publication and any
// colocated scratch. The controller derives it from exact admitted inventories;
// it is not user input, a cross-attempt reservation, or a readiness assertion.
type OwnerBudget struct {
	Bytes  int64  `json:"bytes"`
	Inodes uint64 `json:"inodes"`
}

func (b OwnerBudget) valid() bool {
	return b.Bytes >= 16*MaxOwnerBytes && b.Bytes <= lifecycle.MaxPressureDependentAdmissionBytes && b.Inodes >= 16 && b.Inodes <= math.MaxInt64
}

type OwnerControl struct {
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

// OwnerManifest is a small immutable snapshot. Digest is the canonical CAS token;
// presence of a receipt reference alone never establishes input/publication readiness.
type OwnerManifest struct {
	Schema          string        `json:"schema"`
	Identity        OwnerIdentity `json:"identity"`
	Directory       Node          `json:"directory"`
	Budget          OwnerBudget   `json:"budget"`
	Revision        uint64        `json:"revision"`
	Inventory       *OwnerControl `json:"inventory,omitempty"`
	Inputs          *OwnerControl `json:"inputs,omitempty"`
	Publication     *OwnerControl `json:"publication,omitempty"`
	InputName       string        `json:"input_name,omitempty"`
	PublicationName string        `json:"publication_name,omitempty"`
}

func (m OwnerManifest) Digest() string {
	raw, err := encodeOwner(m)
	if err != nil {
		return ""
	}
	return publicationDigest(raw)
}
func (m OwnerManifest) RelativeName() string { return m.Identity.RelativeName() }
func ownerControlValid(r *OwnerControl, limit int) bool {
	return r != nil && publicationHash(r.Digest) && r.Bytes > 0 && r.Bytes <= int64(limit) && r.Inode != 0
}
func encodeOwner(m OwnerManifest) ([]byte, error) {
	if m.Schema != OwnerSchema || !m.Identity.valid() || !m.Budget.valid() || m.Directory.Path != m.RelativeName() || !m.Directory.Directory || m.Directory.Inode == 0 || m.Revision < 1 || m.Revision > 3 {
		return nil, ErrCustody
	}
	if (m.Inventory == nil) != (m.Inputs == nil) || m.Inventory != nil && (!ownerControlValid(m.Inventory, typedindex.MaxInventoryBytes) || !ownerControlValid(m.Inputs, MaxInputReceiptBytes) || m.Inventory.Digest != m.Identity.Request.BundleDigest) || m.Publication != nil && (!ownerControlValid(m.Publication, MaxPublicationReceiptBytes) || m.Inputs == nil) {
		return nil, ErrCustody
	}
	if (m.Inputs == nil && m.InputName != "") || (m.Inputs != nil && !publishedName(m.InputName)) || (m.Publication == nil && m.PublicationName != "") || (m.Publication != nil && !publicationName(m.PublicationName)) {
		return nil, ErrCustody
	}
	revision := uint64(1)
	if m.Inputs != nil {
		revision++
	}
	if m.Publication != nil {
		if m.Identity.Request.Schema == typedindex.ManagedRequestSchema && m.Identity.Request.Purpose != typedindex.Publish {
			return nil, ErrCustody
		}
		revision++
	}
	if m.Revision != revision {
		return nil, ErrCustody
	}
	var total int64 = 16 * MaxOwnerBytes
	for _, r := range []*OwnerControl{m.Inventory, m.Inputs, m.Publication} {
		if r != nil {
			if r.Bytes > m.Budget.Bytes-total {
				return nil, ErrCustody
			}
			total += r.Bytes
		}
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > MaxOwnerBytes {
		return nil, ErrCustody
	}
	return raw, nil
}
func decodeOwner(raw []byte) (OwnerManifest, error) {
	var m OwnerManifest
	if len(raw) == 0 || len(raw) > MaxOwnerBytes {
		return m, ErrCustody
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil {
		return OwnerManifest{}, ErrCustody
	}
	want, err := encodeOwner(m)
	if err != nil || !bytes.Equal(raw, want) {
		return OwnerManifest{}, ErrCustody
	}
	return m, nil
}

// CreateOwner returns a named manifest even on a later error once the attempt
// directory was created. Such a result is residue, never ready custody. Existing
// attempts refuse; use LoadOwner with trusted identity for exact reopening.
// The private base and its existing empty 0600 publication lock are provisioned
// by the operator. expected is the controller-observed base identity/geometry;
// capacity is reread, and gate is shared with every allocation on that device.
func CreateOwner(ctx context.Context, base string, expected CapacityObservation, id OwnerIdentity, budget OwnerBudget, gate *lifecycle.Gate) (OwnerManifest, error) {
	return createOwner(ctx, base, expected, id, budget, gate, capacity)
}

// Reserve the largest later metadata encoding before any namespace growth.
// Receipt contents are separately budgeted; only their bounded references live
// here. Marshal directly because this wire upper bound need not fit the actual
// receipt-byte budget. Every admitted revision must still pass encodeOwner.
func ownerEnvelopeFits(id OwnerIdentity, budget OwnerBudget) bool {
	control := func(limit int) *OwnerControl {
		return &OwnerControl{Digest: id.Request.BundleDigest, Bytes: int64(limit), Device: math.MaxUint64, Inode: math.MaxUint64}
	}
	m := OwnerManifest{
		Schema: OwnerSchema, Identity: id, Budget: budget, Revision: 3,
		Directory: Node{Path: id.RelativeName(), Device: math.MaxUint64, Inode: math.MaxUint64, Directory: true},
		Inventory: control(typedindex.MaxInventoryBytes), Inputs: control(MaxInputReceiptBytes), Publication: control(MaxPublicationReceiptBytes),
		InputName: "inputs-" + strings.Repeat("f", 32), PublicationName: "bundle-" + strings.Repeat("f", 32),
	}
	raw, err := json.Marshal(m)
	return err == nil && len(raw) <= MaxOwnerBytes
}

func createOwner(ctx context.Context, base string, expected CapacityObservation, id OwnerIdentity, budget OwnerBudget, gate *lifecycle.Gate, probe func(*os.File) (space, error)) (m OwnerManifest, err error) {
	if ctx == nil || gate == nil || !id.valid() || !budget.valid() || !expected.valid() {
		return m, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if !ownerEnvelopeFits(id, budget) {
		return m, ErrCustody
	}
	dir, err := openDirectory(base, true)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	// Check before acquiring the provisioned namespace lock; recheck under it.
	if err = ownerAdmission(ctx, dir, expected, budget, gate, probe); err != nil {
		return m, err
	}
	release, err := ownerBaseLease(ctx, dir)
	if err != nil {
		return m, err
	}
	defer release()
	if err = ownerSameDirectory(dir, base); err != nil {
		return m, err
	}
	if err = ownerAdmission(ctx, dir, expected, budget, gate, probe); err != nil {
		return m, err
	}
	if err = ownerSameDirectory(dir, base); err != nil {
		return m, err
	}
	requests, err := ownerEntries(ctx, dir, MaxOwnerRequests, true)
	if err != nil {
		return m, err
	}
	request := id.PlanningDigest[7:]
	exists := slices.ContainsFunc(requests, func(e os.DirEntry) bool { return e.Name() == request })
	if !exists {
		if len(requests) >= MaxOwnerRequests {
			return m, ErrCustody
		}
		if err = ctx.Err(); err != nil {
			return m, err
		}
		if err = mkdir(dir, request); err != nil {
			return m, err
		}
		if err = dir.Sync(); err != nil {
			return m, err
		}
	}
	parent, err := openDirectory(filepath.Join(base, request), true)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	// A mounted request directory is a different capacity domain and cannot use
	// the base's pressure latch or promise. Refuse before child growth.
	before, err := directoryInfo(dir, ".", false)
	if err != nil {
		return m, err
	}
	after, err := directoryInfo(parent, request, false)
	if err != nil || before.Device != after.Device {
		return m, ErrCustody
	}
	entries, err := ownerEntries(ctx, parent, MaxOwnerAttempts, false)
	if err != nil || len(entries) >= MaxOwnerAttempts {
		return m, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	name := id.AttemptDigest[7:]
	if err = mkdir(parent, name); err != nil {
		return m, err
	}
	m = OwnerManifest{Schema: OwnerSchema, Identity: id, Budget: budget, Revision: 1}
	if err = parent.Sync(); err != nil {
		return m, err
	}
	attempt, err := openDirectory(filepath.Join(base, id.RelativeName()), true)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, attempt.Close()) }()
	m.Directory, err = directoryInfo(attempt, id.RelativeName(), false)
	if err != nil {
		return m, err
	}
	raw, err := encodeOwner(m)
	if err != nil {
		return m, err
	}
	// Every owned attempt, even before Copy, uses the same publication guard.
	// A crash before the manifest leaves held residue, never an unlocked owner.
	if err = ctx.Err(); err != nil {
		return m, err
	}
	lock, lockErr := createFile(attempt, publicationLock)
	if lockErr != nil {
		return m, lockErr
	}
	if err = errors.Join(lock.Sync(), lock.Close(), attempt.Sync()); err != nil {
		return m, err
	}
	if err = ownerWriteControl(ctx, attempt, ownerManifest, raw); err != nil {
		return m, err
	}
	return m, attempt.Sync()
}

// ownerAdmission binds the previous controller observation to the actual opened
// allocation root. Capacity is always fresh; the observation is not a reservation.
func ownerAdmission(ctx context.Context, dir *os.File, expected CapacityObservation, budget OwnerBudget, gate *lifecycle.Gate, probe func(*os.File) (space, error)) error {
	node, err := directoryInfo(dir, ".", false)
	if err != nil || node.Device != expected.Device || node.Inode != expected.Inode {
		return ErrCustody
	}
	actual, err := probe(dir)
	if err != nil || actual.block != expected.BlockSize {
		return ErrCustody
	}
	return ownerCapacity(ctx, dir, budget, gate, func(*os.File) (space, error) { return actual, nil })
}

func ownerCapacity(ctx context.Context, dir *os.File, b OwnerBudget, gate *lifecycle.Gate, probe func(*os.File) (space, error)) error {
	s, err := probe(dir)
	if err != nil || s.total > math.MaxInt64 || s.bytes > s.total || s.block == 0 || s.block > 1<<20 {
		return ErrCustody
	}
	observed, err := gate.CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(s.total), AvailableBytes: int64(s.bytes), UsedBytes: int64(s.total - s.bytes)}, b.Bytes)
	if err != nil {
		return err
	}
	if b.Bytes < int64(16*s.block) || observed.Pressure != lifecycle.PressureNormal || s.bytes < uint64(b.Bytes) || s.inodes < b.Inodes {
		return lifecycle.ErrPressureRefusal
	}
	return nil
}
func ownerEntries(ctx context.Context, dir *os.File, limit int, lock bool) ([]os.DirEntry, error) {
	fresh, err := openRelative(dir, ".", true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fresh.Close() }()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	count := limit + 1
	if lock {
		count++
	}
	entries, err := fresh.ReadDir(count)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, ErrCustody
	}
	if lock {
		entries = slices.DeleteFunc(entries, func(e os.DirEntry) bool { return e.Name() == publicationLock })
	}
	if len(entries) > limit {
		// Base scans read at most 4098 raw entries to allow the lock. If that
		// lock is outside the prefix, still return at most 4097 non-lock names.
		return entries[:limit+1], ErrCustody
	}
	return entries, ctx.Err()
}

// LoadOwner reads only the small owner control and checks referenced file inode,
// mode and size. It never reads/hashes the large receipts. Call LoadOwnerInputs
// or OpenOwnerPublication for actual readiness.
func LoadOwner(ctx context.Context, base string, id OwnerIdentity) (OwnerManifest, error) {
	return loadOwnerPinned(ctx, base, id, false)
}

// LoadOwnerWithNativeCustody inspects owner references while exact named native
// journal files remain. It validates their physical shape, not their authority.
// Callers must authenticate the journal through typedsandbox before signaling
// or cleanup. Publication and drain continue using the strict ordinary loader.
func LoadOwnerWithNativeCustody(ctx context.Context, base string, id OwnerIdentity) (OwnerManifest, error) {
	return loadOwnerPinned(ctx, base, id, true)
}
func loadOwnerPinned(ctx context.Context, base string, id OwnerIdentity, native bool) (OwnerManifest, error) {
	if ctx == nil || !id.valid() {
		return OwnerManifest{}, ErrCustody
	}
	release, err := publicationLease(ctx, base, false, false)
	if err != nil {
		return OwnerManifest{}, err
	}
	defer release()
	return loadOwnerMode(ctx, base, id, false, native)
}
func loadOwner(ctx context.Context, base string, id OwnerIdentity, growing bool) (OwnerManifest, error) {
	return loadOwnerMode(ctx, base, id, growing, false)
}
func loadOwnerMode(ctx context.Context, base string, id OwnerIdentity, growing, native bool) (m OwnerManifest, err error) {
	dir, err := openDirectory(filepath.Join(base, id.RelativeName()), true)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	raw, _, err := ownerReadControl(ctx, dir, ownerManifest, MaxOwnerBytes, nil)
	if err != nil {
		return m, err
	}
	m, err = decodeOwner(raw)
	if err != nil || m.Identity != id {
		return OwnerManifest{}, ErrCustody
	}
	node, err := directoryInfo(dir, id.RelativeName(), false)
	if err != nil || node != m.Directory {
		return m, ErrCustody
	}
	if err = ownerNamesMode(ctx, dir, m, growing, native); err != nil {
		return m, err
	}
	if err = ownerReferences(dir, m); err != nil {
		return m, err
	}
	return m, nil
}

// SaveOwnerInputs persists an exact inventory and verified complete inode receipt.
// It is create-only for these controls and CAS-updates the small manifest. Any
// error may leave named readonly controls or owner.next; reopen fails closed.
func SaveOwnerInputs(ctx context.Context, base string, id OwnerIdentity, expected string, inventoryRaw []byte, r Receipt, gate *lifecycle.Gate) (OwnerManifest, error) {
	return saveOwnerInputs(ctx, base, id, expected, inventoryRaw, r, gate, capacity)
}
func saveOwnerInputs(ctx context.Context, base string, id OwnerIdentity, expected string, inventoryRaw []byte, r Receipt, gate *lifecycle.Gate, probe func(*os.File) (space, error)) (OwnerManifest, error) {
	if ctx == nil || !id.valid() || !publicationHash(expected) {
		return OwnerManifest{}, ErrCustody
	}
	inv, err := typedindex.DecodeInventory(ctx, inventoryRaw, id.Request.BundleDigest)
	if err != nil {
		return OwnerManifest{}, err
	}
	raw, err := encodeOwnerInputs(ctx, inv, r)
	if err != nil {
		return OwnerManifest{}, err
	}
	return updateOwner(ctx, base, id, expected, gate, []int64{int64(len(inventoryRaw)), int64(len(raw))}, probe, func(m *OwnerManifest, dir *os.File) error {
		if m.Inputs != nil || m.Publication != nil {
			return ErrCustody
		}
		if err := Verify(ctx, filepath.Join(base, id.RelativeName()), inv, r); err != nil {
			return err
		}
		if err := ownerControlsBudget(ctx, dir, *m, int64(len(inventoryRaw)+len(raw))); err != nil {
			return err
		}
		var err error
		m.Inventory, err = ownerInstallControl(ctx, dir, "inventory.json", inventoryRaw)
		if err != nil {
			return err
		}
		m.Inputs, err = ownerInstallControl(ctx, dir, "input-receipt.json", raw)
		m.InputName = r.Name
		return err
	})
}
func ownerControlsBudget(ctx context.Context, dir *os.File, m OwnerManifest, extra int64) error {
	installed, err := installedControlsBytes(ctx, dir, m.Identity)
	if err != nil {
		return err
	}
	total := int64(16*MaxOwnerBytes) + installed
	for _, r := range []*OwnerControl{m.Inventory, m.Inputs, m.Publication} {
		if r != nil {
			total += r.Bytes
		}
	}
	if extra < 0 || extra > m.Budget.Bytes-total {
		return ErrCustody
	}
	return nil
}
func encodeOwnerInputs(ctx context.Context, inv typedindex.Inventory, r Receipt) ([]byte, error) {
	tree, err := inventoryLayout(ctx, inv)
	if err != nil {
		return nil, err
	}
	if r.Schema != receiptSchema || r.InventoryDigest != inv.Digest() || !publishedName(r.Name) || len(r.Nodes) != len(tree.entries) {
		return nil, ErrCustody
	}
	previous := ""
	for _, n := range r.Nodes {
		directory, ok := tree.entries[n.Path]
		if !ok || n.Path <= previous || n.Inode == 0 || n.Directory != directory {
			return nil, ErrCustody
		}
		previous = n.Path
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxInputReceiptBytes {
		return nil, ErrCustody
	}
	return raw, nil
}
func decodeOwnerInputs(ctx context.Context, inv typedindex.Inventory, raw []byte) (Receipt, error) {
	var r Receipt
	if len(raw) > MaxInputReceiptBytes {
		return r, ErrCustody
	}
	// Decode one node at a time with the existing inventory's exact entry bound.
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	take := func(want any) bool { v, err := d.Token(); return err == nil && v == want }
	if !take(json.Delim('{')) || !take("schema") || d.Decode(&r.Schema) != nil || !take("inventory_digest") || d.Decode(&r.InventoryDigest) != nil || !take("name") || d.Decode(&r.Name) != nil || !take("nodes") || !take(json.Delim('[')) {
		return r, ErrCustody
	}
	tree, err := inventoryLayout(ctx, inv)
	if err != nil {
		return r, err
	}
	for d.More() {
		if len(r.Nodes) >= len(tree.entries) {
			return Receipt{}, ErrCustody
		}
		var n Node
		if d.Decode(&n) != nil {
			return Receipt{}, ErrCustody
		}
		r.Nodes = append(r.Nodes, n)
	}
	if !take(json.Delim(']')) || !take(json.Delim('}')) {
		return Receipt{}, ErrCustody
	}
	want, err := encodeOwnerInputs(ctx, inv, r)
	if err != nil {
		return Receipt{}, err
	}
	if !bytes.Equal(raw, want) {
		return Receipt{}, ErrCustody
	}
	return r, nil
}
func LoadOwnerInputs(ctx context.Context, base string, id OwnerIdentity) (typedindex.Inventory, Receipt, error) {
	if ctx == nil || !id.valid() {
		return typedindex.Inventory{}, Receipt{}, ErrCustody
	}
	release, err := publicationLease(ctx, base, false, false)
	if err != nil {
		return typedindex.Inventory{}, Receipt{}, err
	}
	defer release()
	m, err := loadOwner(ctx, base, id, false)
	if err != nil || m.Inputs == nil {
		return typedindex.Inventory{}, Receipt{}, ErrCustody
	}
	dir, err := openDirectory(filepath.Join(base, id.RelativeName()), true)
	if err != nil {
		return typedindex.Inventory{}, Receipt{}, err
	}
	defer func() { _ = dir.Close() }()
	raw, _, err := ownerReadControl(ctx, dir, "inventory.json", typedindex.MaxInventoryBytes, m.Inventory)
	if err != nil {
		return typedindex.Inventory{}, Receipt{}, err
	}
	inv, err := typedindex.DecodeInventory(ctx, raw, id.Request.BundleDigest)
	if err != nil {
		return inv, Receipt{}, err
	}
	raw, _, err = ownerReadControl(ctx, dir, "input-receipt.json", MaxInputReceiptBytes, m.Inputs)
	if err != nil {
		return inv, Receipt{}, err
	}
	r, err := decodeOwnerInputs(ctx, inv, raw)
	if err != nil {
		return inv, r, err
	}
	return inv, r, Verify(ctx, filepath.Join(base, id.RelativeName()), inv, r)
}

func SaveOwnerPublication(ctx context.Context, base string, id OwnerIdentity, expected string, parent, execution typedindex.Admission, r PublicationReceipt, gate *lifecycle.Gate) (OwnerManifest, error) {
	if !publicationAuthority(parent, execution, r.PlanDigest) {
		return OwnerManifest{}, ErrCustody
	}
	if ctx == nil || !id.valid() || parent.Digest() != id.PlanningDigest || parent.Request() != id.Request || !publicationHash(expected) {
		return OwnerManifest{}, ErrCustody
	}
	raw, err := EncodePublicationReceipt(r)
	if err != nil {
		return OwnerManifest{}, err
	}
	return updateOwner(ctx, base, id, expected, gate, []int64{int64(len(raw))}, capacity, func(m *OwnerManifest, dir *os.File) error {
		if m.Inputs == nil || m.Publication != nil {
			return ErrCustody
		}
		p, err := OpenPublication(ctx, filepath.Join(base, id.RelativeName()), parent, execution, r.PlanDigest, r.RootDigest, r)
		if err != nil {
			return err
		}
		if err = p.Close(); err != nil {
			return err
		}
		if err = ownerControlsBudget(ctx, dir, *m, int64(len(raw))); err != nil {
			return err
		}
		m.Publication, err = ownerInstallControl(ctx, dir, "publication-receipt.json", raw)
		m.PublicationName = r.Name
		return err
	})
}

// OpenOwnerPublication holds the base pin only through manifest resolution and
// acquisition of the attempt's publication pin. Lifecycle must acquire that
// attempt's exclusive guard before any attempt rename/unlink; request directories
// may only be removed empty, never renamed wholesale. The returned child pin
// protects bytes without blocking unrelated attempt creation or publication.
func OpenOwnerPublication(ctx context.Context, base string, id OwnerIdentity, parent, execution typedindex.Admission, plan, root string) (*Publication, error) {
	if ctx == nil || !id.valid() || id.PlanningDigest != parent.Digest() || id.Request != parent.Request() {
		return nil, ErrCustody
	}
	release, err := publicationLease(ctx, base, false, false)
	if err != nil {
		return nil, err
	}
	defer release()
	m, err := loadOwner(ctx, base, id, false)
	if err != nil || m.Publication == nil {
		return nil, ErrCustody
	}
	dir, err := openDirectory(filepath.Join(base, id.RelativeName()), true)
	if err != nil {
		return nil, err
	}
	raw, _, readErr := ownerReadControl(ctx, dir, "publication-receipt.json", MaxPublicationReceiptBytes, m.Publication)
	err = errors.Join(readErr, dir.Close())
	if err != nil {
		return nil, err
	}
	r, err := DecodePublicationReceipt(raw)
	if err != nil {
		return nil, err
	}
	p, err := OpenPublication(ctx, filepath.Join(base, id.RelativeName()), parent, execution, plan, root, r)
	if err != nil {
		return nil, err
	}
	return p, nil
}
func updateOwner(ctx context.Context, base string, id OwnerIdentity, expected string, gate *lifecycle.Gate, sizes []int64, probe func(*os.File) (space, error), edit func(*OwnerManifest, *os.File) error) (m OwnerManifest, err error) {
	release, err := publicationLease(ctx, base, true, false)
	if err != nil {
		return m, err
	}
	defer release()
	m, err = loadOwner(ctx, base, id, true)
	if err != nil || m.Digest() != expected {
		return m, ErrCustody
	}
	dir, err := openDirectory(filepath.Join(base, id.RelativeName()), true)
	if err != nil {
		return m, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	// Statfs already includes copied inputs and published bytes. Only new control
	// files and the replacement manifest are additional growth at this boundary.
	if err = ownerControlCapacity(ctx, dir, sizes, gate, probe); err != nil {
		return m, err
	}
	if err = edit(&m, dir); err != nil {
		return m, err
	}
	if err = ownerNames(ctx, dir, m, false); err != nil {
		return m, err
	}
	m.Revision++
	raw, err := encodeOwner(m)
	if err != nil {
		return m, err
	}
	if err = ownerWriteControl(ctx, dir, ownerPending, raw); err != nil {
		return m, err
	}
	if err = dir.Sync(); err != nil {
		return m, err
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if err = ownerReplace(dir, ownerPending, ownerManifest); err != nil {
		return m, err
	}
	return m, dir.Sync()
}
func ownerInstallControl(ctx context.Context, dir *os.File, name string, raw []byte) (*OwnerControl, error) {
	if err := ownerWriteControl(ctx, dir, name, raw); err != nil {
		return nil, err
	}
	if err := dir.Sync(); err != nil {
		return nil, err
	}
	_, ref, err := ownerReadControl(ctx, dir, name, len(raw), nil)
	return &ref, err
}
func ownerWriteControl(ctx context.Context, dir *os.File, name string, raw []byte) error {
	return writePublicationFile(ctx, dir, typedindex.BundleFile{Path: name, Bytes: int64(len(raw)), Digest: publicationDigest(raw)}, raw)
}
func ownerReadControl(ctx context.Context, dir *os.File, name string, limit int, expected *OwnerControl) ([]byte, OwnerControl, error) {
	if err := ctx.Err(); err != nil {
		return nil, OwnerControl{}, err
	}
	f, err := openRelative(dir, name, false)
	if err != nil {
		return nil, OwnerControl{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > int64(limit) {
		return nil, OwnerControl{}, ErrCustody
	}
	row := typedindex.BundleFile{Path: name, Bytes: info.Size()}
	before, err := fileInfo(f, row, true)
	if err != nil {
		return nil, OwnerControl{}, err
	}
	raw := make([]byte, info.Size())
	if err = transfer(ctx, f, bytes.NewBuffer(raw[:0]), info.Size()); err != nil {
		return nil, OwnerControl{}, err
	}
	after, err := fileInfo(f, row, true)
	if err != nil || before != after {
		return nil, OwnerControl{}, ErrCustody
	}
	ref := OwnerControl{publicationDigest(raw), info.Size(), before.device, before.inode}
	if expected != nil && ref != *expected {
		return nil, ref, ErrCustody
	}
	return raw, ref, nil
}

// CensusOwners inventories one bounded namespace level without loading receipt
// trees. Empty request selects request roots; otherwise it selects that exact
// request's attempts. Unknown entries are returned Held, and overflow returns a
// bounded prefix plus ErrCustody; callers must never label that prefix complete.
type OwnerCensusEntry struct {
	Name      string
	Directory bool
	Held      bool
}

func CensusOwners(ctx context.Context, base, request string) ([]OwnerCensusEntry, error) {
	if ctx == nil || request != "" && !publicationHash(request) {
		return nil, ErrCustody
	}
	release, err := publicationLease(ctx, base, false, false)
	if err != nil {
		return nil, err
	}
	defer release()
	name := base
	limit := MaxOwnerRequests
	lock := true
	if request != "" {
		name = filepath.Join(base, request[7:])
		limit = MaxOwnerAttempts
		lock = false
	}
	dir, err := openDirectory(name, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	entries, scanErr := ownerEntries(ctx, dir, limit, lock)
	out := make([]OwnerCensusEntry, 0, len(entries))
	for _, e := range entries {
		held := !e.IsDir() || !publicationHash("sha256:"+e.Name())
		if !held && request != "" {
			child, e2 := openDirectory(filepath.Join(name, e.Name()), true)
			if e2 != nil {
				held = true
			} else {
				raw, _, e3 := ownerReadControl(ctx, child, ownerManifest, MaxOwnerBytes, nil)
				m, e4 := decodeOwner(raw)
				node, e5 := directoryInfo(child, request[7:]+"/"+e.Name(), false)
				held = e3 != nil || e4 != nil || e5 != nil || m.Directory != node || m.Identity.PlanningDigest != request || m.Identity.AttemptDigest != "sha256:"+e.Name()
				if !held {
					held = ownerNames(ctx, child, m, false) != nil || ownerReferences(child, m) != nil
				}
				_ = child.Close()
			}
		}
		out = append(out, OwnerCensusEntry{e.Name(), e.IsDir(), held})
	}
	slices.SortFunc(out, func(a, b OwnerCensusEntry) int { return strings.Compare(a.Name, b.Name) })
	return out, errors.Join(scanErr, ctx.Err())
}

func ownerCheckControl(dir *os.File, name string, expected *OwnerControl) error {
	f, err := openRelative(dir, name, false)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	meta, err := fileInfo(f, typedindex.BundleFile{Path: name, Bytes: expected.Bytes}, true)
	if err != nil || meta.device != expected.Device || meta.inode != expected.Inode {
		return ErrCustody
	}
	return nil
}

func ownerControlCapacity(ctx context.Context, dir *os.File, sizes []int64, gate *lifecycle.Gate, probe func(*os.File) (space, error)) error {
	s, err := probe(dir)
	if err != nil || s.total > math.MaxInt64 || s.bytes > s.total || s.block == 0 || s.block > 1<<20 || len(sizes) > 2 {
		return ErrCustody
	}
	needed := uint64(len(sizes)+1) * s.block // one metadata block per new inode
	for _, n := range append(slices.Clone(sizes), MaxOwnerBytes) {
		if n <= 0 || n > MaxInputReceiptBytes+MaxPublicationReceiptBytes {
			return ErrCustody
		}
		needed += (uint64(n) + s.block - 1) / s.block * s.block
	}
	observed, err := gate.CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(s.total), AvailableBytes: int64(s.bytes), UsedBytes: int64(s.total - s.bytes)}, int64(needed))
	if err != nil {
		return err
	}
	if observed.Pressure != lifecycle.PressureNormal || s.bytes < needed || s.inodes < uint64(len(sizes)+1) {
		return lifecycle.ErrPressureRefusal
	}
	return nil
}

func ownerNames(ctx context.Context, dir *os.File, m OwnerManifest, growing bool) error {
	return ownerNamesMode(ctx, dir, m, growing, false)
}
func ownerNamesMode(ctx context.Context, dir *os.File, m OwnerManifest, growing, native bool) error {
	entries, err := ownerEntries(ctx, dir, 16, false)
	if err != nil {
		return err
	}
	seenLock, seenInput, seenPublication := false, m.InputName == "", m.PublicationName == ""
	seenNative, seenPendingNative := false, false
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case publicationLock:
			seenLock = true
			f, e := openRelative(dir, publicationLock, false)
			if e != nil {
				return ErrCustody
			}
			info, e := f.Stat()
			closeErr := f.Close()
			if e != nil || closeErr != nil || info.Size() != 0 || info.Mode() != 0600 {
				return ErrCustody
			}
		case ownerManifest:
		case "inventory.json":
			if m.Inventory == nil {
				return ErrCustody
			}
		case "input-receipt.json":
			if m.Inputs == nil {
				return ErrCustody
			}
		case "publication-receipt.json":
			if m.Publication == nil {
				return ErrCustody
			}
		default:
			if native && m.Inputs != nil && m.InputName != "" && (name == m.InputName+".typed-container.json" || name == m.InputName+".typed-container.json.next") {
				if err := ownerNativeJournal(ctx, dir, name); err != nil {
					return err
				}
				if strings.HasSuffix(name, ".next") {
					seenPendingNative = true
				} else {
					seenNative = true
				}
				continue
			}
			if controlTreeName(name) {
				control, seal, _, _, e := controlMetadata(ctx, dir, name)
				if e != nil {
					return e
				}
				e = control.Close()
				if e != nil || seal.Identity.PlanningDigest != m.Identity.PlanningDigest || seal.Identity.AttemptDigest != m.Identity.AttemptDigest || seal.Identity.ProfileDigest != m.Identity.Request.ProfileDigest {
					return ErrCustody
				}
				continue
			}
			if name == m.InputName {
				seenInput = true
			}
			if name == m.PublicationName {
				seenPublication = true
			}
			// Exact child names are checked after their receipt is decoded. A pending
			// control, incomplete child or unexplained sibling is held, never resumed.
			if !entry.IsDir() || name != m.InputName && name != m.PublicationName && (!growing || !publishedName(name) && !publicationName(name)) {
				return ErrCustody
			}
		}
	}

	if !seenLock || !seenInput || !seenPublication || seenPendingNative && !seenNative {
		return ErrCustody
	}
	return nil
}

func ownerSameDirectory(dir *os.File, name string) error {
	fresh, err := openDirectory(name, true)
	if err != nil {
		return err
	}
	defer func() { _ = fresh.Close() }()
	before, err := dir.Stat()
	if err != nil {
		return err
	}
	after, err := fresh.Stat()
	if err != nil || !os.SameFile(before, after) {
		return ErrCustody
	}
	return nil
}

func ownerReferences(dir *os.File, m OwnerManifest) error {
	var err error
	for _, ref := range []struct {
		name  string
		r     *OwnerControl
		limit int
	}{{"inventory.json", m.Inventory, typedindex.MaxInventoryBytes}, {"input-receipt.json", m.Inputs, MaxInputReceiptBytes}, {"publication-receipt.json", m.Publication, MaxPublicationReceiptBytes}} {
		if ref.r != nil {
			if err = ownerCheckControl(dir, ref.name, ref.r); err != nil {
				return err
			}
		}
	}
	return nil
}
