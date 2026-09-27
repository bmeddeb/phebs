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
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

const MaxControlSealBytes = 4096
const ControlSealFile = "control-seal.json"
const controlSealSchema = "phebs-typed-worker-controls-v1"

type ControlPhase string

const (
	ControlsPlanning  ControlPhase = "plan"
	ControlsExecution ControlPhase = "execute"
)

// ControlSpec is trusted controller authority. Scratch must have just passed
// VerifyHostScratch; its pure Validate method proves shape, not live custody.
// Input verification and helper/formatter inventory admission remain separate.
type ControlSpec struct {
	Phase     ControlPhase
	Parent    typedindex.Admission
	Profile   typedindex.Profile
	Execution typedindex.Admission
	Plan      typedindex.PackagePlan
	Scratch   typedsandbox.HostScratchReceipt
}

type ControlIdentity struct {
	Phase             ControlPhase `json:"phase"`
	PlanningDigest    string       `json:"planning_digest"`
	AttemptDigest     string       `json:"attempt_digest"`
	RequestDigest     string       `json:"request_digest"`
	ProfileDigest     string       `json:"profile_digest"`
	PlanDigest        string       `json:"plan_digest"`
	HostReceiptDigest string       `json:"host_receipt_digest"`
}

// ControlRef must be retained by the trusted controller. Filesystem contents
// cannot supply their own expected reference or authorize native execution.
type ControlRef struct {
	Identity  ControlIdentity `json:"identity"`
	Directory Node            `json:"directory"`
	Seal      OwnerControl    `json:"seal"`
}
type controlFile struct {
	Name    string       `json:"name"`
	Control OwnerControl `json:"control"`
}
type controlSeal struct {
	Schema    string          `json:"schema"`
	Identity  ControlIdentity `json:"identity"`
	Directory Node            `json:"directory"`
	Files     []controlFile   `json:"files"`
}

func controlName(phase ControlPhase) string {
	switch phase {
	case ControlsPlanning:
		return "controls-plan"
	case ControlsExecution:
		return "controls-execute"
	}
	return ""
}
func controlTreeName(name string) bool {
	name = strings.TrimSuffix(name, ".stage")
	return name == controlName(ControlsPlanning) || name == controlName(ControlsExecution)
}
func controlFiles(phase ControlPhase) []string {
	if phase == ControlsPlanning {
		return []string{"parent.json", "profile.json", typedsandbox.ScratchAuthorityFile}
	}
	if phase == ControlsExecution {
		return []string{"parent.json", "plan.json", "profile.json", "request.json", typedsandbox.ScratchAuthorityFile}
	}
	return nil
}
func controlLimit(name string) int {
	switch name {
	case "parent.json", "request.json":
		return typedindex.MaxRequestBytes
	case "profile.json":
		return typedindex.MaxProfileBytes
	case "plan.json":
		return typedindex.MaxPlanBytes
	case typedsandbox.ScratchAuthorityFile:
		return typedsandbox.MaxScratchAuthorityBytes
	}
	return 0
}
func (i ControlIdentity) valid() bool {
	if controlName(i.Phase) == "" || !publicationHash(i.PlanningDigest) || !publicationHash(i.AttemptDigest) || !publicationHash(i.RequestDigest) || !publicationHash(i.ProfileDigest) || !publicationHash(i.HostReceiptDigest) {
		return false
	}
	if i.Phase == ControlsPlanning {
		return i.PlanDigest == "" && i.RequestDigest == i.PlanningDigest
	}
	return publicationHash(i.PlanDigest)
}
func encodeControlSeal(s controlSeal) ([]byte, error) {
	names := controlFiles(s.Identity.Phase)
	if s.Schema != controlSealSchema || !s.Identity.valid() || s.Directory.Path != controlName(s.Identity.Phase) || !s.Directory.Directory || s.Directory.Inode == 0 || len(s.Files) != len(names) {
		return nil, ErrCustody
	}
	for index, f := range s.Files {
		if f.Name != names[index] || !ownerControlValid(&f.Control, controlLimit(f.Name)) || f.Control.Device != s.Directory.Device {
			return nil, ErrCustody
		}
	}
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > MaxControlSealBytes {
		return nil, ErrCustody
	}
	return raw, nil
}
func decodeControlSeal(raw []byte) (controlSeal, error) {
	var s controlSeal
	if len(raw) == 0 || len(raw) > MaxControlSealBytes || json.Unmarshal(raw, &s) != nil {
		return s, ErrCustody
	}
	canonical, err := encodeControlSeal(s)
	if err != nil || !bytes.Equal(canonical, raw) {
		return controlSeal{}, ErrCustody
	}
	return s, nil
}

func controlData(ctx context.Context, id OwnerIdentity, s ControlSpec) (ControlIdentity, map[string][]byte, error) {
	var identity ControlIdentity
	if ctx == nil || !id.valid() || s.Parent.Digest() != id.PlanningDigest || s.Parent.Request() != id.Request || controlName(s.Phase) == "" || s.Profile.Digest() == "" || s.Profile.Digest() != id.Request.ProfileDigest || s.Scratch.Validate() != nil || s.Scratch.Options.RequestDigest != id.PlanningDigest || s.Scratch.Options.AttemptDigest != id.AttemptDigest {
		return identity, nil, ErrCustody
	}
	if err := ctx.Err(); err != nil {
		return identity, nil, err
	}
	identity = ControlIdentity{Phase: s.Phase, PlanningDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest, RequestDigest: id.PlanningDigest, ProfileDigest: s.Profile.Digest()}
	hostRaw, err := json.Marshal(s.Scratch)
	if err != nil {
		return identity, nil, err
	}
	identity.HostReceiptDigest = publicationDigest(hostRaw)
	data := make(map[string][]byte, 5)
	data["parent.json"], err = json.Marshal(s.Parent.Request())
	if err != nil {
		return identity, nil, err
	}
	data["profile.json"], err = json.Marshal(s.Profile.Definition())
	if err != nil {
		return identity, nil, err
	}
	data[typedsandbox.ScratchAuthorityFile], err = typedsandbox.EncodeScratchAuthority(s.Scratch.Authority)
	if err != nil {
		return identity, nil, err
	}
	if publicationDigest(data["parent.json"]) != id.PlanningDigest || publicationDigest(data["profile.json"]) != s.Profile.Digest() {
		return identity, nil, ErrCustody
	}
	if s.Phase == ControlsExecution {
		if !publicationAuthority(s.Parent, s.Execution, s.Plan.Digest()) {
			return identity, nil, ErrCustody
		}
		raw := s.Plan.Bytes()
		if _, err = typedindex.DecodePackagePlan(ctx, s.Parent, raw, s.Plan.Digest()); err != nil {
			return identity, nil, err
		}
		data["plan.json"] = raw
		data["request.json"], err = json.Marshal(s.Execution.Request())
		if err != nil {
			return identity, nil, err
		}
		identity.RequestDigest = s.Execution.Digest()
		identity.PlanDigest = s.Plan.Digest()
	} else if s.Plan.Digest() != "" || s.Execution.Digest() != "" {
		return identity, nil, ErrCustody
	}
	// Preflight the largest inode/device encodings before any filesystem growth.
	seal := controlSeal{Schema: controlSealSchema, Identity: identity, Directory: Node{Path: controlName(s.Phase), Device: math.MaxUint64, Inode: math.MaxUint64, Directory: true}}
	for _, name := range controlFiles(s.Phase) {
		raw := data[name]
		if len(raw) == 0 || len(raw) > controlLimit(name) {
			return identity, nil, ErrCustody
		}
		seal.Files = append(seal.Files, controlFile{name, OwnerControl{Digest: publicationDigest(raw), Bytes: int64(len(raw)), Device: math.MaxUint64, Inode: math.MaxUint64}})
	}
	_, err = encodeControlSeal(seal)
	return identity, data, err
}

// InstallControls creates one fixed phase exactly once. Errors after stage
// creation leave named held residue for DrainOwner, never reusable readiness.
// Installation holds namespace EX then attempt EX through its bounded writes.
func InstallControls(ctx context.Context, base string, id OwnerIdentity, spec ControlSpec, gate *lifecycle.Gate) (ControlRef, error) {
	return installControls(ctx, base, id, spec, gate, capacity, nil)
}
func installControls(ctx context.Context, base string, id OwnerIdentity, spec ControlSpec, gate *lifecycle.Gate, probe func(*os.File) (space, error), hook func(string) error) (ref ControlRef, err error) {
	identity, data, err := controlData(ctx, id, spec)
	if err != nil {
		return ref, err
	}
	if gate == nil {
		return ref, ErrCustody
	}
	guard, cancel := context.WithTimeout(ctx, 2*time.Second)
	release, err := publicationLease(guard, base, true, false)
	if err != nil {
		cancel()
		return ref, err
	}
	defer release()
	m, err := loadOwner(ctx, base, id, false)
	if err != nil {
		cancel()
		return ref, err
	}
	if m.Inputs == nil {
		cancel()
		return ref, ErrCustody
	}
	attemptPath := filepath.Join(base, id.RelativeName())
	releaseAttempt, err := publicationLease(guard, attemptPath, true, false)
	cancel()
	if err != nil {
		return ref, err
	}
	defer releaseAttempt()
	parent, err := openDirectory(attemptPath, true)
	if err != nil {
		return ref, err
	}
	defer func() {
		if parent != nil {
			err = errors.Join(err, parent.Close())
		}
	}()
	node, err := directoryInfo(parent, id.RelativeName(), false)
	if err != nil || node != m.Directory {
		return ref, ErrCustody
	}
	entries, err := ownerEntries(ctx, parent, 16, false)
	if err != nil {
		return ref, err
	}
	name := controlName(spec.Phase)
	for _, entry := range entries {
		if entry.Name() == name || entry.Name() == name+".stage" {
			return ref, ErrCustody
		}
	}
	var rows []typedindex.BundleFile
	var added int64 = MaxControlSealBytes
	for _, file := range controlFiles(spec.Phase) {
		raw := data[file]
		rows = append(rows, typedindex.BundleFile{Path: file, Bytes: int64(len(raw)), Digest: publicationDigest(raw)})
		added += int64(len(raw))
	}
	rows = append(rows, typedindex.BundleFile{Path: ControlSealFile, Bytes: MaxControlSealBytes})
	if err = ownerControlsBudget(ctx, parent, m, added); err != nil {
		return ref, err
	}
	available, err := probe(parent)
	if err != nil {
		return ref, err
	}
	nodes := uint64(len(rows) + 1)
	needed, err := allocationBytes(rows, nodes, available.block)
	if err != nil || needed > math.MaxInt64 {
		return ref, ErrCustody
	}
	pressure, err := gate.CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(available.total), AvailableBytes: int64(available.bytes), UsedBytes: int64(available.total - available.bytes)}, int64(needed))
	if err != nil {
		return ref, err
	}
	ownedNodes := 16 + nodes
	for _, entry := range entries {
		if entry.Name() == controlName(ControlsPlanning) {
			ownedNodes += uint64(len(controlFiles(ControlsPlanning)) + 2)
		}
		if entry.Name() == controlName(ControlsExecution) {
			ownedNodes += uint64(len(controlFiles(ControlsExecution)) + 2)
		}
	}
	if pressure.Pressure != lifecycle.PressureNormal || available.bytes < needed || available.inodes < nodes || m.Budget.Inodes < ownedNodes {
		return ref, lifecycle.ErrPressureRefusal
	}
	event := func(name string) error {
		if hook != nil {
			if e := hook(name); e != nil {
				return e
			}
		}
		return ctx.Err()
	}
	if err = event("before-stage"); err != nil {
		return ref, err
	}
	stage := name + ".stage"
	if err = mkdir(parent, stage); err != nil {
		return ref, err
	}
	if err = parent.Sync(); err != nil {
		return ref, err
	}
	if err = event("stage"); err != nil {
		return ref, err
	}
	dir, err := openRelative(parent, stage, true)
	if err != nil {
		return ref, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	seal := controlSeal{Schema: controlSealSchema, Identity: identity}
	seal.Directory, err = directoryInfo(dir, name, false)
	if err != nil || seal.Directory.Device != m.Directory.Device {
		return ref, ErrCustody
	}
	for _, row := range rows[:len(rows)-1] {
		if err = writePublicationFile(ctx, dir, row, data[row.Path]); err != nil {
			return ref, err
		}
		_, record, e := ownerReadControl(ctx, dir, row.Path, controlLimit(row.Path), nil)
		if e != nil {
			return ref, e
		}
		if record.Digest != row.Digest || record.Bytes != row.Bytes {
			return ref, ErrCustody
		}
		seal.Files = append(seal.Files, controlFile{row.Path, record})
		if err = event(row.Path); err != nil {
			return ref, err
		}
	}
	raw, err := encodeControlSeal(seal)
	if err != nil {
		return ref, err
	}
	if err = writePublicationFile(ctx, dir, typedindex.BundleFile{Path: ControlSealFile, Bytes: int64(len(raw))}, raw); err != nil {
		return ref, err
	}
	if err = errors.Join(dir.Chmod(0555), dir.Sync()); err != nil {
		return ref, err
	}
	if err = event("sealed"); err != nil {
		return ref, err
	}
	if err = controlChildren(dir, seal.Files); err != nil {
		return ref, err
	}
	_, sealRef, err := ownerReadControl(ctx, dir, ControlSealFile, MaxControlSealBytes, nil)
	if err != nil {
		return ref, err
	}
	if err = renameExclusive(parent, stage, name); err != nil {
		return ref, err
	}
	ref = ControlRef{identity, seal.Directory, sealRef}
	if err = parent.Sync(); err != nil {
		return ref, err
	}
	return ref, event("published")
}

// Controls pins this attempt until Close, without holding the installation-wide
// namespace lock. Keep the handle alive through child join and native cleanup.
// Path/Reference are scalar adapters for a sandbox that cannot import workspace.
type Controls struct {
	mu      sync.Mutex
	root    *os.File
	release func()
	path    string
	ref     ControlRef
}

func (c *Controls) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.root == nil {
		return ""
	}
	return c.path
}
func (c *Controls) Reference() ControlRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.root == nil {
		return ControlRef{}
	}
	return c.ref
}
func (c *Controls) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.root == nil {
		return nil
	}
	err := c.root.Close()
	c.root = nil
	c.release()
	return err
}

// OpenControls revalidates all bounded control bytes and inode proofs once.
// expected and spec originate from trusted controller custody, never the seal.
func OpenControls(ctx context.Context, base string, id OwnerIdentity, spec ControlSpec, expected ControlRef) (_ *Controls, err error) {
	identity, data, err := controlData(ctx, id, spec)
	if err != nil {
		return nil, err
	}
	if expected.Identity != identity || !ownerControlValid(&expected.Seal, MaxControlSealBytes) {
		return nil, ErrCustody
	}
	guard, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	release, err := publicationLease(guard, base, false, false)
	if err != nil {
		return nil, err
	}
	m, err := loadOwner(ctx, base, id, false)
	if err != nil {
		release()
		return nil, err
	}
	if m.Inputs == nil {
		release()
		return nil, ErrCustody
	}
	attempt := filepath.Join(base, id.RelativeName())
	releaseAttempt, err := publicationLease(guard, attempt, false, false)
	release()
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			releaseAttempt()
		}
	}()
	parent, err := openDirectory(attempt, true)
	if err != nil {
		return nil, err
	}
	defer func() {
		if parent != nil {
			err = errors.Join(err, parent.Close())
		}
	}()
	node, err := directoryInfo(parent, id.RelativeName(), false)
	if err != nil || node != m.Directory {
		return nil, ErrCustody
	}
	dir, seal, sealRef, _, err := controlMetadata(ctx, parent, controlName(spec.Phase))
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			_ = dir.Close()
		}
	}()
	if seal.Identity != identity || seal.Directory != expected.Directory || sealRef != expected.Seal {
		return nil, ErrCustody
	}
	for _, file := range seal.Files {
		raw, _, e := ownerReadControl(ctx, dir, file.Name, controlLimit(file.Name), &file.Control)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(raw, data[file.Name]) {
			return nil, ErrCustody
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = parent.Close(); err != nil {
		return nil, err
	}
	parent = nil
	success = true
	return &Controls{root: dir, release: releaseAttempt, path: filepath.Join(attempt, controlName(spec.Phase)), ref: expected}, nil
}

// Metadata paths read only the <=4KiB seal and at most seven direct entries
// (six expected plus overflow), never plans.
func controlMetadata(ctx context.Context, parent *os.File, name string) (_ *os.File, seal controlSeal, sealRef OwnerControl, total int64, err error) {
	if !controlTreeName(name) || strings.HasSuffix(name, ".stage") {
		return nil, seal, sealRef, 0, ErrCustody
	}
	dir, err := openRelative(parent, name, true)
	if err != nil {
		return nil, seal, sealRef, 0, err
	}
	success := false
	defer func() {
		if !success {
			_ = dir.Close()
		}
	}()
	raw, sealRef, err := ownerReadControl(ctx, dir, ControlSealFile, MaxControlSealBytes, nil)
	if err != nil {
		return nil, seal, sealRef, 0, err
	}
	seal, err = decodeControlSeal(raw)
	if err != nil {
		return nil, seal, sealRef, 0, err
	}
	node, err := directoryInfo(dir, name, true)
	parentNode, e := directoryInfo(parent, ".", false)
	if err != nil || e != nil || node != seal.Directory || node.Device != parentNode.Device || controlName(seal.Identity.Phase) != name || sealRef.Device != node.Device {
		return nil, seal, sealRef, 0, ErrCustody
	}
	if err = controlChildren(dir, seal.Files); err != nil {
		return nil, seal, sealRef, 0, err
	}
	total = sealRef.Bytes
	for _, f := range seal.Files {
		if err = ownerCheckControl(dir, f.Name, &f.Control); err != nil {
			return nil, seal, sealRef, 0, err
		}
		total += f.Control.Bytes
	}
	success = true
	return dir, seal, sealRef, total, nil
}
func installedControlsBytes(ctx context.Context, dir *os.File, id OwnerIdentity) (int64, error) {
	entries, err := ownerEntries(ctx, dir, 16, false)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if !controlTreeName(entry.Name()) {
			continue
		}
		control, seal, _, n, err := controlMetadata(ctx, dir, entry.Name())
		if err != nil {
			return 0, err
		}
		err = control.Close()
		if err != nil {
			return 0, err
		}
		if seal.Identity.PlanningDigest != id.PlanningDigest || seal.Identity.AttemptDigest != id.AttemptDigest || seal.Identity.ProfileDigest != id.Request.ProfileDigest {
			return 0, ErrCustody
		}
		total += n
	}
	return total, nil
}

func controlChildren(dir *os.File, files []controlFile) error {
	entries, err := dir.ReadDir(len(files) + 2)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) != len(files)+1 {
		return ErrCustody
	}
	names := []string{ControlSealFile}
	for _, file := range files {
		names = append(names, file.Name)
	}
	for _, entry := range entries {
		if entry.IsDir() || !slices.Contains(names, entry.Name()) {
			return ErrCustody
		}
	}
	return nil
}
