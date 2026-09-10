//go:build darwin

package t421

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var errCustodyByteObservation = errors.New("custody byte observation unavailable")

type custodyByteSample struct{ LogicalBytes, AllocatedBytes uint64 }
type custodyBytePhase struct {
	Maximum   custodyByteSample
	Completed bool
}
type custodyByteSnapshot struct {
	Phases      [15]custodyBytePhase
	Unavailable bool
}

// Samples are non-atomic, per-linked-path totals, not unique physical allocation
// or complete instantaneous phase high-water. Only completed traversals update
// maxima. The owner is borrowed; this observer neither closes nor replaces it.
type custodyByteObservation struct {
	owner  productionRoot
	turn   chan struct{}
	mu     sync.Mutex
	phase  uint32
	result custodyByteSnapshot
	err    error
}

func newCustodyByteObservation(owner productionRoot) *custodyByteObservation {
	return &custodyByteObservation{owner: owner, turn: make(chan struct{}, 1)}
}

func (g *custodyByteObservation) Snapshot() custodyByteSnapshot {
	if g == nil {
		return custodyByteSnapshot{Unavailable: true}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.result
}

func (g *custodyByteObservation) fail() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err, g.result.Unavailable = errCustodyByteObservation, true
	return g.err
}

// Phase labels are monotonic caller metadata, not controller admission proof.
// Skipped phases remain incomplete; later parent integration must bind actual
// required observation points. No default timeout extends the caller's budget.
func (g *custodyByteObservation) Sample(ctx context.Context, phase uint32) (custodyByteSample, error) {
	if g == nil {
		return custodyByteSample{}, errCustodyByteObservation
	}
	if ctx == nil {
		return custodyByteSample{}, g.fail()
	}
	deadline, bounded := ctx.Deadline()
	if !bounded || !time.Now().Before(deadline) || ctx.Err() != nil || phase < 1 || phase > 15 {
		return custodyByteSample{}, g.fail()
	}
	select {
	case g.turn <- struct{}{}:
	case <-ctx.Done():
		return custodyByteSample{}, g.fail()
	}
	defer func() { <-g.turn }()
	g.mu.Lock()
	valid := g.err == nil && phase >= g.phase
	if valid {
		g.phase = phase
	}
	g.mu.Unlock()
	if !valid {
		return custodyByteSample{}, g.fail()
	}
	value, err := walkCustodyBytes(ctx, g.owner)
	if err != nil || ctx.Err() != nil {
		return custodyByteSample{}, g.fail()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	// A concurrent canceled waiter may already have latched unavailable.
	if g.err != nil {
		return custodyByteSample{}, g.err
	}
	row := &g.result.Phases[phase-1]
	row.Completed = true
	row.Maximum.LogicalBytes = max(row.Maximum.LogicalBytes, value.LogicalBytes)
	row.Maximum.AllocatedBytes = max(row.Maximum.AllocatedBytes, value.AllocatedBytes)
	return value, nil
}

type custodyByteDirectory struct {
	file      *os.File
	name      string
	pathBytes int
	before    unix.Stat_t
}

// Serial DFS retains at most (4096+1)/2 directory cursors. Pinned Darwin Go
// duplicates each cursor FD for libc fdopendir: at most 4096 traversal FDs plus
// one os.Root anchor, excluding the borrowed owner FD. Readdirnames(1) adds no
// whole-directory list; libc DIR storage/buffering is additional and unmeasured.
// Union mounts are refused because libc may inventory their whole directory.
// OS FD exhaustion refuses rather than raising limits. No rescans, content
// reads or child tools.
// Cancellation is checked between native calls; it is not a syscall deadline.
func walkCustodyBytes(ctx context.Context, owner productionRoot) (value custodyByteSample, retErr error) {
	if checkCustodyByteRoot(ctx, owner) != nil {
		return value, errCustodyByteObservation
	}
	root, err := os.OpenRoot(owner.path)
	if err != nil {
		return value, errCustodyByteObservation
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	file, err := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return value, errCustodyByteObservation
	}
	var initial unix.Stat_t
	if unix.Fstat(int(file.Fd()), &initial) != nil {
		_ = file.Close()
		return value, errCustodyByteObservation
	}
	info, err := file.Stat()
	volume, volumeErr := custodyByteVolume(file)
	if err != nil || volumeErr != nil || volume != owner.volume || !os.SameFile(owner.info, info) || checkCustodyByteRoot(ctx, owner) != nil {
		_ = file.Close()
		return value, errCustodyByteObservation
	}
	stack := []custodyByteDirectory{{file: file, pathBytes: len(owner.path), before: initial}}
	defer func() {
		for _, entry := range stack {
			retErr = errors.Join(retErr, entry.file.Close())
		}
	}()
	if addCustodyByteStat(&value, initial, initial.Dev) != nil {
		return custodyByteSample{}, errCustodyByteObservation
	}
	for len(stack) > 0 {
		if ctx.Err() != nil {
			return custodyByteSample{}, errCustodyByteObservation
		}
		current := &stack[len(stack)-1]
		entries, readErr := current.file.Readdirnames(1)
		if readErr != nil && readErr != io.EOF {
			return custodyByteSample{}, errCustodyByteObservation
		}
		if len(entries) == 0 {
			var after unix.Stat_t
			volume, e := custodyByteVolume(current.file)
			if readErr != io.EOF || e != nil || volume != owner.volume || unix.Fstat(int(current.file.Fd()), &after) != nil || !sameCustodyByteStat(current.before, after) {
				return custodyByteSample{}, errCustodyByteObservation
			}
			if len(stack) > 1 {
				var linked unix.Stat_t
				parent := stack[len(stack)-2].file
				if unix.Fstatat(int(parent.Fd()), current.name, &linked, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameCustodyByteStat(after, linked) {
					return custodyByteSample{}, errCustodyByteObservation
				}
			}
			if current.file.Close() != nil {
				return custodyByteSample{}, errCustodyByteObservation
			}
			stack = stack[:len(stack)-1]
			continue
		}
		name := entries[0]
		pathBytes := current.pathBytes + 1 + len(name)
		if name == "." || name == ".." || filepath.Base(name) != name || pathBytes > maxInputCustodyPathBytes {
			return custodyByteSample{}, errCustodyByteObservation
		}
		var before, after unix.Stat_t
		if unix.Fstatat(int(current.file.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil || before.Dev != initial.Dev {
			return custodyByteSample{}, errCustodyByteObservation
		}
		if before.Mode&unix.S_IFMT == unix.S_IFDIR {
			fd, e := unix.Openat(int(current.file.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if e != nil {
				return custodyByteSample{}, errCustodyByteObservation
			}
			child := os.NewFile(uintptr(fd), name)
			volume, volumeErr := custodyByteVolume(child)
			if volumeErr != nil || volume != owner.volume || unix.Fstat(fd, &after) != nil || !sameCustodyByteStat(before, after) {
				_ = child.Close()
				return custodyByteSample{}, errCustodyByteObservation
			}
			stack = append(stack, custodyByteDirectory{file: child, name: name, pathBytes: pathBytes, before: after})
		} else if unix.Fstatat(int(current.file.Fd()), name, &after, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameCustodyByteStat(before, after) {
			return custodyByteSample{}, errCustodyByteObservation
		}
		if addCustodyByteStat(&value, before, initial.Dev) != nil {
			return custodyByteSample{}, errCustodyByteObservation
		}
	}
	var final unix.Stat_t
	if unix.Fstat(int(owner.file.Fd()), &final) != nil || !sameCustodyByteStat(initial, final) || checkCustodyByteRoot(ctx, owner) != nil {
		return custodyByteSample{}, errCustodyByteObservation
	}
	return value, nil
}

func checkCustodyByteRoot(ctx context.Context, owner productionRoot) error {
	if ctx == nil || ctx.Err() != nil || owner.file == nil || owner.info == nil || owner.path == "/" || !filepath.IsAbs(owner.path) || len(owner.path) > maxInputCustodyPathBytes {
		return errCustodyByteObservation
	}
	held, e := owner.file.Stat()
	linked, pathErr := os.Lstat(owner.path)
	volume, volumeErr := custodyByteVolume(owner.file)
	canonical, canonicalErr := filepath.EvalSymlinks(owner.path)
	if e != nil || pathErr != nil || volumeErr != nil || canonicalErr != nil || canonical != owner.path || volume != owner.volume || !os.SameFile(owner.info, held) || !os.SameFile(held, linked) || !inputCustodyOwned(linked) || !linked.IsDir() || linked.Mode().Perm() != 0o700 {
		return errCustodyByteObservation
	}
	return nil
}

func sameCustodyByteStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Gen == b.Gen && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Uid == b.Uid && a.Gid == b.Gid && a.Flags == b.Flags && a.Size == b.Size && a.Blocks == b.Blocks && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func custodyByteVolume(file *os.File) ([2]int32, error) {
	var stat unix.Statfs_t
	if file == nil || unix.Fstatfs(int(file.Fd()), &stat) != nil {
		return [2]int32{}, errCustodyByteObservation
	}
	return custodyByteFilesystem(stat)
}

func custodyByteFilesystem(stat unix.Statfs_t) ([2]int32, error) {
	if stat.Flags&unix.MNT_UNION != 0 {
		return [2]int32{}, errCustodyByteObservation
	}
	return stat.Fsid.Val, nil
}

func addCustodyByteStat(value *custodyByteSample, stat unix.Stat_t, device int32) error {
	if stat.Dev != device || stat.Size < 0 || stat.Blocks < 0 || uint64(stat.Blocks) > math.MaxUint64/512 {
		return errCustodyByteObservation
	}
	allocated := uint64(stat.Blocks) * 512
	if allocated > math.MaxUint64-value.AllocatedBytes {
		return errCustodyByteObservation
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFREG {
		if uint64(stat.Size) > math.MaxUint64-value.LogicalBytes {
			return errCustodyByteObservation
		}
		value.LogicalBytes += uint64(stat.Size)
	}
	value.AllocatedBytes += allocated
	return nil
}
