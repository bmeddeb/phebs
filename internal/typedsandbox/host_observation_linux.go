//go:build linux

package typedsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

func observeHostNamespace(ctx context.Context, selected string) (HostObservation, error) {
	if ctx == nil || hostSelected() || os.Geteuid() != 0 || os.Getegid() != 0 {
		return HostObservation{Held: true}, ErrRefused
	}
	if err := ctx.Err(); err != nil {
		return HostObservation{Held: true}, err
	}
	for _, name := range []string{"/", "/var", "/var/lib"} {
		var st unix.Stat_t
		if unix.Lstat(name, &st) != nil || !hostAncestorMetadata(st.Mode, st.Uid, st.Gid) {
			return HostObservation{Held: true}, ErrCustody
		}
	}
	return observeHostDirectory(ctx, HostScratchBase, selected, 0, 0)
}

// Only tests vary base/owner. The public wrapper fixes all three operator facts.
func observeHostDirectory(ctx context.Context, base, selected string, uid, gid uint32) (out HostObservation, err error) {
	defer func() {
		if err != nil {
			out.Held = true
		}
	}()
	if ctx == nil || selected != "" && !hostRootName(selected) {
		return out, ErrRefused
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	var named unix.Stat_t
	if unix.Lstat(base, &named) != nil || !hostObservationDirectory(named, uid, gid) {
		return out, ErrCustody
	}
	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return out, ErrCustody
	}
	root := os.NewFile(uintptr(fd), "host-observation-base")
	defer func() { err = errors.Join(err, root.Close()) }()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || !hostObservationDirectory(st, uid, gid) || st.Dev != named.Dev || st.Ino != named.Ino {
		return out, ErrCustody
	}
	lock, err := hostObserveOpen(root, ".lock", false)
	if err != nil {
		return out, ErrCustody
	}
	var ls unix.Stat_t
	if unix.Fstat(int(lock.Fd()), &ls) != nil || !hostObservationFile(ls, uid, gid) || ls.Size != 0 || ls.Dev != st.Dev {
		_ = lock.Close()
		return out, ErrCustody
	}
	lock, err = hostAcquireLock(ctx, lock)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	// Detect named lock/base replacement; never lock a detached former authority.
	if !hostObserveNamed(root, ".lock", ls) || unix.Lstat(base, &named) != nil || named.Dev != st.Dev || named.Ino != st.Ino {
		return out, ErrCustody
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) != nil {
		return out, ErrCustody
	}
	out.Capacity, err = hostObservationCapacity(st, fs)
	if err != nil {
		return out, err
	}
	entries, err := hostObserveEntries(root, 3)
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		if e.Name() == ".lock" {
			continue
		}
		if len(out.Names) < 2 {
			out.Names = append(out.Names, e.Name())
		}
		if !hostRootName(e.Name()) || !e.IsDir() {
			out.Held = true
		}
	}
	slices.Sort(out.Names)
	out.Overflow = len(out.Names) > 1
	out.Held = out.Held || out.Overflow || len(out.Names) != 0
	if selected != "" && slices.Contains(out.Names, selected) {
		observation, held, e := hostObserveSelected(ctx, root, selected, uid, gid, HostBaseIdentity{Device: uint64(st.Dev), Inode: st.Ino, BlockSize: out.Capacity.BlockSize})
		if e != nil {
			return out, e
		}
		out.Selected = observation
		out.Held = held || out.Overflow
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if unix.Lstat(base, &named) != nil || named.Dev != st.Dev || named.Ino != st.Ino || !hostObservationDirectory(named, uid, gid) || !hostObserveNamed(root, ".lock", ls) {
		return out, ErrCustody
	}
	return out, nil
}

func hostObservationDirectory(st unix.Stat_t, uid, gid uint32) bool {
	return st.Mode&0177777 == unix.S_IFDIR|0700 && st.Uid == uid && st.Gid == gid
}
func hostObservationFile(st unix.Stat_t, uid, gid uint32) bool {
	return st.Mode&0177777 == unix.S_IFREG|0600 && st.Uid == uid && st.Gid == gid && st.Nlink == 1
}
func hostObserveOpen(dir *os.File, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat2(int(dir.Fd()), name, &unix.OpenHow{Flags: uint64(flags), Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "host-observation"), nil
}
func hostObserveNamed(dir *os.File, name string, expected unix.Stat_t) bool {
	var st unix.Stat_t
	return unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Dev == expected.Dev && st.Ino == expected.Ino && st.Mode == expected.Mode && st.Uid == expected.Uid && st.Gid == expected.Gid && st.Nlink == expected.Nlink && st.Size == expected.Size && st.Mtim == expected.Mtim && st.Ctim == expected.Ctim
}
func hostObserveEntries(dir *os.File, limit int) ([]os.DirEntry, error) {
	e, err := dir.ReadDir(limit)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return e, err
}
func hostObservationCapacity(st unix.Stat_t, fs unix.Statfs_t) (HostCapacity, error) {
	if fs.Bsize <= 0 || fs.Bsize > 1<<20 || fs.Blocks == 0 || fs.Blocks > uint64(math.MaxInt64)/uint64(fs.Bsize) || fs.Bfree > fs.Blocks || fs.Bavail > fs.Bfree || fs.Files == 0 || fs.Ffree > fs.Files {
		return HostCapacity{}, ErrCustody
	}
	b := uint64(fs.Bsize)
	return HostCapacity{uint64(st.Dev), st.Ino, b, fs.Blocks * b, fs.Bfree * b, fs.Bavail * b, fs.Files, fs.Ffree}, nil
}

func hostObserveSelected(ctx context.Context, base *os.File, name string, uid, gid uint32, expected HostBaseIdentity) (*HostJournalObservation, bool, error) {
	dir, err := hostObserveOpen(base, name, true)
	if err != nil {
		return nil, true, ErrCustody
	}
	defer func() { _ = dir.Close() }()
	var st unix.Stat_t
	if unix.Fstat(int(dir.Fd()), &st) != nil || !hostObservationDirectory(st, uid, gid) || uint64(st.Dev) != expected.Device {
		return nil, true, ErrCustody
	}
	entries, err := hostObserveEntries(dir, 5)
	if err != nil || len(entries) > 4 {
		return nil, true, ErrCustody
	}
	main, pending, imagePresent, scratchPresent := false, false, false, false
	var image unix.Stat_t
	for _, e := range entries {
		switch e.Name() {
		case "owner.json":
			main = true
		case "owner.next":
			pending = true
		case "image.ext4":
			imagePresent = true
			if unix.Fstatat(int(dir.Fd()), e.Name(), &image, unix.AT_SYMLINK_NOFOLLOW) != nil || !hostObservationFile(image, uid, gid) || image.Dev != st.Dev {
				return nil, true, ErrCustody
			}
		case "scratch":
			scratchPresent = true
			// This may be the live mounted worker-owned ext4 root. Do not
			// follow/traverse it or apply host-private ownership to it.
			var scratch unix.Stat_t
			if unix.Fstatat(int(dir.Fd()), e.Name(), &scratch, unix.AT_SYMLINK_NOFOLLOW) != nil || scratch.Mode&unix.S_IFMT != unix.S_IFDIR {
				return nil, true, ErrCustody
			}
		default:
			return nil, true, ErrCustody
		}
	}
	if !main {
		return nil, true, ErrCustody
	}
	j, err := hostObserveJournal(ctx, dir, "owner.json", uid, gid, expected.Device)
	if err != nil {
		return nil, true, err
	}
	if j.Options.Base != expected || strings.TrimPrefix(j.Options.root(), HostScratchBase+"/") != name {
		return nil, true, ErrCustody
	}
	if j.Phase == "ready" && imagePresent && (uint64(image.Dev) != j.ImageDevice || image.Ino != j.ImageInode) {
		return nil, true, ErrCustody
	}
	if pending {
		next, e := hostObserveJournal(ctx, dir, "owner.next", uid, gid, expected.Device)
		if e != nil {
			return nil, true, e
		}
		if !hostOwnerAdvance(j, next) {
			return nil, true, ErrCustody
		}
	}
	if !hostObserveNamed(base, name, st) {
		return nil, true, ErrCustody
	}
	return &HostJournalObservation{name, j.Options, j.Phase, j.ImageDevice, j.ImageInode, j.Loop}, pending || j.Phase != "ready" || !imagePresent || !scratchPresent, nil
}
func hostObserveJournal(ctx context.Context, dir *os.File, name string, uid, gid uint32, device uint64) (hostOwner, error) {
	if err := ctx.Err(); err != nil {
		return hostOwner{}, err
	}
	f, err := hostObserveOpen(dir, name, false)
	if err != nil {
		return hostOwner{}, ErrCustody
	}
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil || !hostObservationFile(st, uid, gid) || uint64(st.Dev) != device || st.Size <= 0 || st.Size > 8192 {
		return hostOwner{}, ErrCustody
	}
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return hostOwner{}, err
	}
	var candidate hostOwner
	if json.Unmarshal(raw, &candidate) != nil {
		return candidate, ErrCustody
	}
	j, err := decodeHostOwner(raw, candidate.Options)
	if err != nil || !hostObserveNamed(dir, name, st) {
		return hostOwner{}, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return hostOwner{}, err
	}
	return j, nil
}

func hostRootName(name string) bool { return hostDigest("sha256:" + name) }
