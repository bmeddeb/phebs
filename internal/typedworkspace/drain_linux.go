//go:build linux

package typedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const drainMarkerName = "collecting.json"
const drainPendingName = "collecting.next"
const drainMarkerSchema = "phebs-typed-collecting-v1"
const drainMaxPath = 512 + 1 + 45 // longest admitted child path plus stage basename

func (a DrainAuthority) valid() bool {
	return publicationHash(a.PlanningDigest) && publicationHash(a.AttemptDigest) && publicationHash(a.ManifestDigest) && a.DirectoryInode != 0
}
func (a DrainAuthority) relative() string { return a.PlanningDigest[7:] + "/" + a.AttemptDigest[7:] }

type drainMarker struct {
	Schema      string         `json:"schema"`
	Authority   DrainAuthority `json:"authority"`
	Revision    uint64         `json:"revision"`
	Previous    string         `json:"previous"`
	Terminal    bool           `json:"terminal"`
	Cursor      string         `json:"cursor"`
	CursorInode uint64         `json:"cursor_inode"`
}

func drainTopName(name string) bool {
	switch name {
	case ownerManifest, ownerPending, "inventory.json", "input-receipt.json", "publication-receipt.json":
		return true
	}
	if controlTreeName(name) {
		return true
	}
	name = strings.TrimSuffix(name, ".stage")
	return publishedName(name) || publicationName(name)
}

type drainTurn struct {
	ctx       context.Context
	authority DrainAuthority
	report    DrainReport
	root      *os.File
	marker    drainMarker
	markerRaw []byte
	owner     OwnerManifest
	hook      func(string) error
}

func (d *drainTurn) event(name string) error {
	if d.hook != nil {
		if err := d.hook(name); err != nil {
			return err
		}
	}
	return d.ctx.Err()
}
func (d *drainTurn) open(parent *os.File, name string, directory bool) (*os.File, error) {
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	d.report.Openat2Calls++
	fd, err := unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{Flags: uint64(flags), Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "typed-drain"), nil
}
func (d *drainTurn) stat(file *os.File) (unix.Stat_t, error) {
	var st unix.Stat_t
	d.report.TraversalStats++
	err := unix.Fstat(int(file.Fd()), &st)
	return st, err
}
func (d *drainTurn) named(dir *os.File, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	d.report.TraversalStats++
	err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
	return st, err
}
func (d *drainTurn) safe(st unix.Stat_t, directory bool) bool {
	kind := uint32(unix.S_IFREG)
	if directory {
		kind = unix.S_IFDIR
	}
	return st.Mode&unix.S_IFMT == kind && st.Uid == uint32(os.Geteuid()) && uint64(st.Dev) == d.authority.DirectoryDevice && st.Mode&07022 == 0 && (directory || st.Nlink == 1)
}
func (d *drainTurn) sync(dir *os.File) error { d.report.Syncs++; return dir.Sync() }
func (d *drainTurn) writable(dir *os.File) error {
	st, err := d.stat(dir)
	if err != nil || !d.safe(st, true) {
		return ErrCustody
	}
	if st.Mode&0777 == 0700 {
		return nil
	}
	if err = d.ctx.Err(); err != nil {
		return err
	}
	d.report.Chmods++
	if err = dir.Chmod(0700); err != nil {
		return err
	}
	if err = d.sync(dir); err != nil {
		return err
	}
	return d.event("chmod")
}
func (d *drainTurn) entries(dir *os.File, n int) ([]os.DirEntry, error) {
	d.report.DirectoryReads++
	entries, err := dir.ReadDir(n)
	d.report.DirectoryEntries += len(entries)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, err
}
func (d *drainTurn) read(name string) ([]byte, error) {
	f, err := d.open(d.root, name, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := d.stat(f)
	if err != nil || !d.safe(st, false) || st.Mode&07777 != 0444 || st.Size <= 0 || st.Size > MaxDrainMarkerBytes {
		return nil, ErrCustody
	}
	d.report.ControlBytesCharged += int(st.Size) + 1
	raw := make([]byte, st.Size)
	buf := bytes.NewBuffer(raw[:0])
	if err = transfer(d.ctx, f, buf, st.Size); err != nil {
		return nil, err
	}
	after, err := d.stat(f)
	if err != nil || after.Dev != st.Dev || after.Ino != st.Ino || after.Mode != st.Mode || after.Uid != st.Uid || after.Gid != st.Gid || after.Size != st.Size || after.Nlink != st.Nlink || after.Mtim != st.Mtim || after.Ctim != st.Ctim {
		return nil, ErrCustody
	}
	return raw, nil
}
func validDrainCursor(s string) bool {
	if s == "." {
		return true
	}
	if len(s) > drainMaxPath || !utf8.ValidString(s) || path.Clean(s) != s || strings.HasPrefix(s, "/") || strings.ContainsAny(s, "\\:") {
		return false
	}
	parts := strings.Split(s, "/")
	if !drainTopName(parts[0]) || !publishedName(strings.TrimSuffix(parts[0], ".stage")) && !publicationName(strings.TrimSuffix(parts[0], ".stage")) && !controlTreeName(parts[0]) {
		return false
	}
	if len(parts) > 1 && len(strings.Join(parts[1:], "/")) > 512 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return false
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	return true
}
func encodeDrainMarker(m drainMarker) ([]byte, error) {
	if m.Schema != drainMarkerSchema || !m.Authority.valid() || m.Revision == 0 || m.Revision == 1 && m.Previous != "" || m.Revision > 1 && !publicationHash(m.Previous) || !validDrainCursor(m.Cursor) || m.CursorInode == 0 || m.Cursor == "." && m.CursorInode != m.Authority.DirectoryInode || m.Terminal && m.Cursor != "." {
		return nil, ErrCustody
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > MaxDrainMarkerBytes {
		return nil, ErrCustody
	}
	return raw, nil
}
func decodeDrainMarker(raw []byte, a DrainAuthority) (drainMarker, error) {
	var m drainMarker
	if len(raw) > MaxDrainMarkerBytes || json.Unmarshal(raw, &m) != nil || m.Authority != a {
		return m, ErrCustody
	}
	want, err := encodeDrainMarker(m)
	if err != nil || !bytes.Equal(raw, want) {
		return drainMarker{}, ErrCustody
	}
	return m, nil
}
func (d *drainTurn) renamePending(initial bool) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if !initial && d.report.Deleted >= MaxDrainDeletes {
		return ErrCustody
	}
	if initial {
		if err := renameExclusive(d.root, drainPendingName, drainMarkerName); err != nil {
			return err
		}
	} else {
		d.report.DeleteCalls++
		if err := ownerReplace(d.root, drainPendingName, drainMarkerName); err != nil {
			return err
		}
		d.report.Deleted++
	}
	if err := d.sync(d.root); err != nil {
		return err
	}
	return d.event("marker-renamed")
}
func (d *drainTurn) save(cursor string, inode uint64, terminal bool) error {
	if len(d.markerRaw) != 0 && d.marker.Cursor == cursor && d.marker.CursorInode == inode && d.marker.Terminal == terminal {
		return nil
	}
	if d.marker.Revision == math.MaxUint64 {
		return ErrCustody
	}
	next := drainMarker{drainMarkerSchema, d.authority, d.marker.Revision + 1, "", terminal, cursor, inode}
	if len(d.markerRaw) != 0 {
		next.Previous = publicationDigest(d.markerRaw)
	}
	raw, err := encodeDrainMarker(next)
	if err != nil {
		return err
	}
	if err = d.ctx.Err(); err != nil {
		return err
	}
	f, err := createFile(d.root, drainPendingName)
	if err != nil {
		return err
	}
	d.report.MarkerWrites++
	_, writeErr := f.Write(raw)
	syncErr := d.sync(f)
	chmodErr := f.Chmod(0444)
	d.report.Chmods++
	syncErr = errors.Join(syncErr, d.sync(f))
	err = errors.Join(writeErr, syncErr, chmodErr, f.Close())
	if err != nil {
		return err
	}
	if err = d.sync(d.root); err != nil {
		return err
	}
	if err = d.event("marker-pending"); err != nil {
		return err
	}
	if err = d.renamePending(len(d.markerRaw) == 0); err != nil {
		return err
	}
	d.marker, d.markerRaw = next, raw
	return nil
}
func (d *drainTurn) loadMarker() (bool, error) {
	raw, err := d.read(drainMarkerName)
	exists := err == nil
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return false, err
	}
	if exists {
		d.marker, err = decodeDrainMarker(raw, d.authority)
		if err != nil {
			return false, err
		}
		d.markerRaw = raw
	}
	pending, err := d.read(drainPendingName)
	if errors.Is(err, unix.ENOENT) {
		return exists, nil
	}
	if err != nil {
		return false, err
	}
	next, err := decodeDrainMarker(pending, d.authority)
	if err != nil {
		return false, err
	}
	if !validDrainSuccessor(exists, d.marker, raw, next) {
		return false, ErrCustody
	}
	if err = d.renamePending(!exists); err != nil {
		return false, err
	}
	d.marker, d.markerRaw = next, pending
	return true, nil
}
func (d *drainTurn) loadOwner() error {
	raw, err := d.read(ownerManifest)
	if err != nil {
		return err
	}
	m, err := decodeOwner(raw)
	if err != nil || m.Digest() != d.authority.ManifestDigest || m.Identity.PlanningDigest != d.authority.PlanningDigest || m.Identity.AttemptDigest != d.authority.AttemptDigest || m.Directory.Device != d.authority.DirectoryDevice || m.Directory.Inode != d.authority.DirectoryInode {
		return ErrCustody
	}
	d.owner = m
	return nil
}
func (d *drainTurn) remove(dir *os.File, name string, st unix.Stat_t, directory bool) error {
	if d.report.Deleted >= MaxDrainDeletes {
		return ErrCustody
	}
	if err := d.ctx.Err(); err != nil {
		return err
	}
	observed, err := d.named(dir, name)
	if err != nil || observed.Dev != st.Dev || observed.Ino != st.Ino || !d.safe(observed, directory) {
		return ErrCustody
	}
	flags := 0
	if directory {
		flags = unix.AT_REMOVEDIR
	}
	d.report.DeleteCalls++
	if err = unix.Unlinkat(int(dir.Fd()), name, flags); err != nil {
		return err
	}
	d.report.Deleted++
	if err = d.sync(dir); err != nil {
		return err
	}
	return d.event("removed:" + name)
}
func (d *drainTurn) controlIdentity(name string, st unix.Stat_t) bool {
	var ref *OwnerControl
	switch name {
	case "inventory.json":
		ref = d.owner.Inventory
	case "input-receipt.json":
		ref = d.owner.Inputs
	case "publication-receipt.json":
		ref = d.owner.Publication
	}
	return ref == nil || uint64(st.Dev) == ref.Device && st.Ino == ref.Inode && st.Size == ref.Bytes
}
func (d *drainTurn) walk() error {
	cursor := d.marker.Cursor
	dir, err := d.open(d.root, cursor, true)
	if err != nil {
		return err
	}
	defer func() {
		if dir != nil {
			_ = dir.Close()
		}
	}()
	st, err := d.stat(dir)
	if err != nil || !d.safe(st, true) || st.Ino != d.marker.CursorInode {
		return ErrCustody
	}
	for d.report.Steps < MaxDrainSteps && d.report.Deleted < MaxDrainDeletes-2 {
		if err = d.ctx.Err(); err != nil {
			return err
		}
		d.report.Steps++
		if err = d.writable(dir); err != nil {
			return err
		}
		entries, e := d.entries(dir, 1)
		if e != nil {
			return e
		}
		if len(entries) == 0 {
			if cursor == "." {
				if err = dir.Close(); err != nil {
					return err
				}
				dir = nil
				if err = d.event("root-eof"); err != nil {
					return err
				}
				terminal, e := d.rootOnlyControls()
				if e != nil {
					return e
				}
				return d.save(".", d.authority.DirectoryInode, terminal)
			}
			parentPath := path.Dir(cursor)
			parent, e := d.open(d.root, parentPath, true)
			if e != nil {
				return e
			}
			parentStat, e := d.stat(parent)
			if e != nil || !d.safe(parentStat, true) {
				_ = parent.Close()
				return ErrCustody
			}
			// Never leave the durable cursor naming a directory this turn removes.
			if d.marker.Cursor == cursor || strings.HasPrefix(d.marker.Cursor, cursor+"/") {
				e = d.save(parentPath, parentStat.Ino, false)
			}
			if e == nil {
				e = d.writable(parent)
			}
			if e == nil {
				e = d.remove(parent, path.Base(cursor), st, true)
			}
			if e != nil {
				_ = parent.Close()
				return e
			}
			_ = dir.Close()
			dir = parent
			cursor = parentPath
			st = parentStat
			// Openat2 returns an offset-zero parent: prior siblings can have vanished.
			continue
		}
		name := entries[0].Name()
		if cursor == "." && (name == publicationLock || name == ownerManifest || name == drainMarkerName) {
			continue
		}
		if cursor == "." && !drainTopName(name) {
			return ErrCustody
		}
		childPath := path.Join(cursor, name)
		if cursor != "." && !validDrainCursor(childPath) {
			return ErrCustody
		}
		named, e := d.named(dir, name)
		if e != nil {
			return e
		}
		isDir := named.Mode&unix.S_IFMT == unix.S_IFDIR
		if cursor == "." {
			treeName := publishedName(strings.TrimSuffix(name, ".stage")) || publicationName(strings.TrimSuffix(name, ".stage")) || controlTreeName(name)
			if isDir != treeName {
				return ErrCustody
			}
		}
		if !d.safe(named, isDir) || cursor == "." && !d.controlIdentity(name, named) {
			return ErrCustody
		}
		child, e := d.open(dir, name, isDir)
		if e != nil {
			return e
		}
		observed, e := d.stat(child)
		if e != nil || observed.Dev != named.Dev || observed.Ino != named.Ino || !d.safe(observed, isDir) {
			_ = child.Close()
			return ErrCustody
		}
		if isDir {
			if !validDrainCursor(childPath) {
				_ = child.Close()
				return ErrCustody
			}
			_ = dir.Close()
			dir = child
			cursor = childPath
			st = observed
			continue
		}
		_ = child.Close()
		if e = d.remove(dir, name, named, false); e != nil {
			return e
		}
	}
	if err = dir.Close(); err != nil {
		return err
	}
	dir = nil
	return d.save(cursor, st.Ino, false)
}

func drainOwner(ctx context.Context, base string, a DrainAuthority, hook func(string) error) (report DrainReport, err error) {
	d := &drainTurn{ctx: ctx, authority: a, hook: hook}
	defer func() {
		report = d.report
		if err != nil {
			report.Held = true
		}
	}()
	if ctx == nil || !a.valid() {
		return report, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	release, err := drainLease(ctx, base)
	if err != nil {
		return report, err
	}
	d.report.GuardAcquisitions++
	defer release()
	d.report.PrivateRootOpens++
	root, err := openDirectory(base, true)
	if err != nil {
		return report, err
	}
	request, err := d.open(root, a.PlanningDigest[7:], true)
	if errors.Is(err, unix.ENOENT) {
		err = d.sync(root)
		_ = root.Close()
		d.report.Done = err == nil
		return report, err
	}
	if err != nil {
		_ = root.Close()
		return report, err
	}
	attempt, err := d.open(request, a.AttemptDigest[7:], true)
	if errors.Is(err, unix.ENOENT) {
		err = d.absent(root, request)
		_ = request.Close()
		_ = root.Close()
		return report, err
	}
	_ = request.Close()
	_ = root.Close()
	if err != nil {
		return report, err
	}
	d.root = attempt
	defer func() { _ = attempt.Close() }()
	st, err := d.stat(attempt)
	if err != nil || !d.safe(st, true) || st.Mode&07777 != 0700 || st.Ino != a.DirectoryInode {
		return report, ErrCustody
	}
	lock, lockErr := d.named(attempt, publicationLock)
	haveLock := lockErr == nil
	if haveLock {
		if !d.safe(lock, false) || lock.Mode&07777 != 0600 || lock.Size != 0 {
			return report, ErrCustody
		}
		attemptRelease, e := drainLease(ctx, filepath.Join(base, a.relative()))
		if e != nil {
			return report, e
		}
		d.report.GuardAcquisitions++
		defer attemptRelease()
	} else if !errors.Is(lockErr, unix.ENOENT) {
		return report, lockErr
	}
	exists, err := d.loadMarker()
	if err != nil {
		return report, err
	}
	if !exists {
		probe, e := d.open(attempt, ".", true)
		if e != nil {
			return report, e
		}
		entries, e := d.entries(probe, 1)
		_ = probe.Close()
		if e != nil {
			return report, e
		}
		if len(entries) == 0 {
			return report, d.finishRoot(base)
		}
		if !haveLock {
			return report, ErrCustody
		}
		if err = d.loadOwner(); err != nil {
			return report, err
		}
		if err = d.save(".", a.DirectoryInode, false); err != nil {
			return report, err
		}
	}
	if !d.marker.Terminal {
		if !haveLock {
			return report, ErrCustody
		}
		if err = d.loadOwner(); err != nil {
			return report, err
		}
		if err = d.walk(); err != nil {
			return report, err
		}
	}
	if d.marker.Terminal && d.report.Deleted < MaxDrainDeletes {
		return report, d.terminal(base, haveLock)
	}
	return report, nil
}
func (d *drainTurn) terminal(base string, haveLock bool) error {
	probe, err := d.open(d.root, ".", true)
	if err != nil {
		return err
	}
	entries, err := d.entries(probe, 4)
	_ = probe.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != ownerManifest && e.Name() != publicationLock && e.Name() != drainMarkerName {
			return ErrCustody
		}
	}
	for _, name := range []string{ownerManifest, publicationLock, drainMarkerName} {
		if d.report.Deleted >= MaxDrainDeletes {
			return nil
		}
		st, err := d.named(d.root, name)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil || !d.safe(st, false) {
			return ErrCustody
		}
		if name == ownerManifest {
			if err = d.loadOwner(); err != nil {
				return err
			}
		}
		if name == publicationLock && (!haveLock || st.Mode&07777 != 0600 || st.Size != 0) {
			return ErrCustody
		}
		if err = d.remove(d.root, name, st, false); err != nil {
			return err
		}
	}
	if d.report.Deleted >= MaxDrainDeletes {
		return nil
	}
	return d.finishRoot(base)
}
func (d *drainTurn) finishRoot(base string) error {
	// The trusted inode remains the authority after the last marker disappeared.
	probe, err := d.open(d.root, ".", true)
	if err != nil {
		return err
	}
	entries, err := d.entries(probe, 1)
	_ = probe.Close()
	if err != nil || len(entries) != 0 {
		return ErrCustody
	}
	d.report.PrivateRootOpens++
	root, err := openDirectory(base, true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	request, err := d.open(root, d.authority.PlanningDigest[7:], true)
	if err != nil {
		return err
	}
	defer func() { _ = request.Close() }()
	st, err := d.named(request, d.authority.AttemptDigest[7:])
	if err != nil || st.Ino != d.authority.DirectoryInode || !d.safe(st, true) {
		return ErrCustody
	}
	if err = d.remove(request, d.authority.AttemptDigest[7:], st, true); err != nil {
		return err
	}
	return d.absent(root, request)
}
func (d *drainTurn) absent(base, request *os.File) error {
	if err := d.sync(request); err != nil {
		return err
	}
	entries, err := d.entries(request, 1)
	if err != nil {
		return err
	}
	if len(entries) == 0 && d.report.Deleted >= MaxDrainDeletes {
		return d.sync(base)
	}
	if len(entries) == 0 && d.report.Deleted < MaxDrainDeletes {
		st, err := d.stat(request)
		if err != nil || !d.safe(st, true) {
			return ErrCustody
		}
		if err = d.remove(base, d.authority.PlanningDigest[7:], st, true); err != nil {
			return err
		}
	}
	if err = d.sync(base); err != nil {
		return err
	}
	d.report.Done = true
	return nil
}

func drainLease(ctx context.Context, name string) (func(), error) {
	lockCtx, cancel := context.WithTimeout(ctx, DrainLockWait)
	defer cancel()
	return publicationLease(lockCtx, name, true, false)
}

func (d *drainTurn) rootOnlyControls() (bool, error) {
	// EOF on an iterator changed by this turn's deletions is not terminal proof.
	// A fresh cap+one read makes the transition independent of directory cookies.
	probe, err := d.open(d.root, ".", true)
	if err != nil {
		return false, err
	}
	entries, err := d.entries(probe, 4)
	closeErr := probe.Close()
	if err != nil || closeErr != nil {
		return false, errors.Join(err, closeErr)
	}
	for _, e := range entries {
		if e.Name() != ownerManifest && e.Name() != publicationLock && e.Name() != drainMarkerName {
			return false, nil
		}
	}
	return true, nil
}

func inspectDrainOwner(ctx context.Context, base string, expected Node, a DrainAuthority) (out DrainInspection, err error) {
	if ctx == nil || !a.valid() || expected.Device != a.DirectoryDevice {
		return out, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, DrainLockWait)
	defer cancel()
	release, err := publicationLease(lockCtx, base, false, false)
	if err != nil {
		return out, err
	}
	defer release()
	root, err := openDirectory(base, true)
	if err != nil {
		return out, err
	}
	defer func() { _ = root.Close() }()
	var baseStat unix.Stat_t
	if unix.Fstat(int(root.Fd()), &baseStat) != nil || expected.Inode == 0 || uint64(baseStat.Dev) != expected.Device || baseStat.Ino != expected.Inode {
		return out, ErrCustody
	}
	d := &drainTurn{ctx: ctx, authority: a}
	request, err := d.open(root, a.PlanningDigest[7:], true)
	if errors.Is(err, unix.ENOENT) {
		return DrainInspection{Absent: true}, nil
	}
	if err != nil {
		return out, err
	}
	defer func() { _ = request.Close() }()
	requestStat, e := d.stat(request)
	if e != nil || !d.safe(requestStat, true) || requestStat.Mode&07777 != 0700 {
		return out, ErrCustody
	}

	attempt, err := d.open(request, a.AttemptDigest[7:], true)
	if errors.Is(err, unix.ENOENT) {
		return DrainInspection{Absent: true}, nil
	}
	if err != nil {
		return out, err
	}
	defer func() { _ = attempt.Close() }()
	d.root = attempt
	st, err := d.stat(attempt)
	if err != nil || !d.safe(st, true) || st.Mode&07777 != 0700 || st.Ino != a.DirectoryInode {
		return out, ErrCustody
	}
	entries, err := d.entries(attempt, 17)
	if err != nil || len(entries) > 16 {
		return out, ErrCustody
	}
	if len(entries) == 0 {
		return DrainInspection{Resume: true}, nil
	}
	haveLock := false
	for _, entry := range entries {
		name := entry.Name()
		if name != publicationLock && name != drainMarkerName && name != drainPendingName && !drainTopName(name) {
			return out, ErrCustody
		}
		named, e := d.named(attempt, name)
		if e != nil {
			return out, e
		}
		directory := named.Mode&unix.S_IFMT == unix.S_IFDIR
		if !d.safe(named, directory) {
			return out, ErrCustody
		}
		tree := controlTreeName(name) || publishedName(strings.TrimSuffix(name, ".stage")) || publicationName(strings.TrimSuffix(name, ".stage"))
		if directory != tree {
			return out, ErrCustody
		}
		if name == publicationLock {
			if named.Mode&07777 != 0600 || named.Size != 0 {
				return out, ErrCustody
			}
			haveLock = true
		}
	}
	if haveLock {
		unlock, e := publicationLease(lockCtx, filepath.Join(base, a.relative()), false, false)
		if e != nil {
			return out, e
		}
		defer unlock()
	}
	var marker drainMarker
	raw, e := d.read(drainMarkerName)
	exists := e == nil
	if e != nil && !errors.Is(e, unix.ENOENT) {
		return out, e
	}
	if exists {
		marker, e = decodeDrainMarker(raw, a)
		if e != nil {
			return out, e
		}
	}
	pending, e := d.read(drainPendingName)
	if e != nil && !errors.Is(e, unix.ENOENT) {
		return out, e
	}
	if e == nil {
		next, e := decodeDrainMarker(pending, a)
		if e != nil {
			return out, e
		}
		if !validDrainSuccessor(exists, marker, raw, next) {
			return out, ErrCustody
		}
		marker = next
		exists = true
	}
	if !exists {
		if !haveLock {
			return out, ErrCustody
		}
		if err = d.loadOwner(); err != nil {
			return out, err
		}
		if err = ownerNames(ctx, attempt, d.owner, false); err != nil {
			return out, err
		}
		if err = ownerReferences(attempt, d.owner); err != nil {
			return out, err
		}
		return DrainInspection{Live: &d.owner}, ctx.Err()
	}
	if marker.Terminal {
		for _, entry := range entries {
			if entry.Name() != ownerManifest && entry.Name() != publicationLock && entry.Name() != drainMarkerName && entry.Name() != drainPendingName {
				return out, ErrCustody
			}
		}
		for _, entry := range entries {
			if entry.Name() == ownerManifest {
				if err = d.loadOwner(); err != nil {
					return out, err
				}
			}
		}
	} else {
		if !haveLock {
			return out, ErrCustody
		}
		if err = d.loadOwner(); err != nil {
			return out, err
		}
		cursor, e := d.open(attempt, marker.Cursor, true)
		if e != nil {
			return out, e
		}
		observed, e := d.stat(cursor)
		_ = cursor.Close()
		if e != nil || !d.safe(observed, true) || observed.Ino != marker.CursorInode {
			return out, ErrCustody
		}
	}
	return DrainInspection{Resume: true}, ctx.Err()
}

func inspectDrainNamespace(ctx context.Context, base, planning string, expected Node) ([]OwnerCensusEntry, bool, error) {
	if ctx == nil || !publicationHash(planning) {
		return nil, false, ErrCustody
	}
	lockCtx, cancel := context.WithTimeout(ctx, DrainLockWait)
	defer cancel()
	release, err := publicationLease(lockCtx, base, false, false)
	if err != nil {
		return nil, false, err
	}
	defer release()
	root, err := openDirectory(base, true)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = root.Close() }()
	var baseStat unix.Stat_t
	if unix.Fstat(int(root.Fd()), &baseStat) != nil || expected.Inode == 0 || uint64(baseStat.Dev) != expected.Device || baseStat.Ino != expected.Inode {
		return nil, false, ErrCustody
	}
	d := &drainTurn{ctx: ctx}
	request, err := d.open(root, planning[7:], true)
	if errors.Is(err, unix.ENOENT) {
		return nil, true, ctx.Err()
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = request.Close() }()
	st, err := d.stat(request)
	if err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		return nil, false, ErrCustody
	}
	entries, err := d.entries(request, MaxOwnerAttempts+1)
	if err != nil || len(entries) > MaxOwnerAttempts {
		return nil, false, ErrCustody
	}
	out := make([]OwnerCensusEntry, 0, len(entries))
	for _, entry := range entries {
		named, e := d.named(request, entry.Name())
		if e != nil {
			return nil, false, e
		}
		held := !publicationHash("sha256:"+entry.Name()) || named.Mode&unix.S_IFMT != unix.S_IFDIR || named.Uid != uint32(os.Geteuid()) || named.Dev != st.Dev || named.Mode&07777 != 0700
		out = append(out, OwnerCensusEntry{Name: entry.Name(), Directory: named.Mode&unix.S_IFMT == unix.S_IFDIR, Held: held})
	}
	slices.SortFunc(out, func(a, b OwnerCensusEntry) int { return strings.Compare(a.Name, b.Name) })
	return out, false, ctx.Err()
}

func validDrainSuccessor(exists bool, current drainMarker, raw []byte, next drainMarker) bool {
	if !exists {
		return next.Revision == 1 && next.Previous == "" && !next.Terminal && next.Cursor == "."
	}
	return current.Revision != math.MaxUint64 && next.Revision == current.Revision+1 && next.Previous == publicationDigest(raw) && (!current.Terminal || next.Terminal)
}
