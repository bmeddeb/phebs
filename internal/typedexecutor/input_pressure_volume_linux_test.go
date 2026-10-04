//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"golang.org/x/sys/unix"
)

const nativePressureImageBytes = int64(4 << 30)

var errNativePressurePath = errors.New("pressure volume path is outside the closed fixture")

var nativePressurePath = regexp.MustCompile(`^/var/lib/phebs-typed-acceptance/t459-workspace-pressure-[a-z0-9-]+/volume$`)

// The operational driver owns the pinned formatter, fully allocated 4GiB image,
// exact loop backing and mount, and ordinary unmount/detach. This helper never
// creates a mount or changes that authority. Its ballast is outside the closed
// workspace census and consumes only the already bounded dedicated filesystem.
type nativePressureVolume struct {
	Workspace                string
	base                     string
	root, workspace, ballast *os.File
	rootID, workspaceID      unix.Stat_t
	ballastID                unix.Stat_t
	geometry, empty          typedworkspace.CapacityObservation
}

func nativePressureOpen(t *testing.T, ctx context.Context, base string) *nativePressureVolume {
	t.Helper()
	p, err := nativePressureBind(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.close(); err != nil {
			t.Error(err)
		}
	})
	return p
}

func nativePressureBind(ctx context.Context, base string) (_ *nativePressureVolume, err error) {
	if len(base) > 256 || !nativePressurePath.MatchString(base) || filepath.Clean(base) != base {
		return nil, errNativePressurePath
	}
	if ctx == nil || os.Geteuid() != 0 {
		return nil, errors.New("pressure volume requires root and a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := &nativePressureVolume{base: base, Workspace: filepath.Join(base, "workspace")}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.close())
		}
	}()
	p.root, err = nativePressureDirectory(base)
	if err != nil {
		return nil, err
	}
	if err = unix.Fstat(int(p.root.Fd()), &p.rootID); err != nil || unix.Major(uint64(p.rootID.Dev)) != 7 {
		return nil, errors.New("pressure volume is not a loop filesystem")
	}
	parent, err := nativePressureDirectory(filepath.Dir(base))
	if err != nil {
		return nil, err
	}
	var backing unix.Stat_t
	err = errors.Join(unix.Fstat(int(parent.Fd()), &backing), parent.Close())
	if err != nil || backing.Dev == p.rootID.Dev {
		return nil, errors.New("pressure volume shares its backing device")
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(int(p.root.Fd()), &fs); err != nil || fs.Type != unix.EXT4_SUPER_MAGIC || fs.Bsize != 4096 || fs.Files != 262144 || fs.Flags&unix.ST_RDONLY != 0 || fs.Blocks > uint64(nativePressureImageBytes/4096) || fs.Blocks < uint64(3<<30)/4096 {
		return nil, errors.New("pressure volume geometry differs from the dedicated ext4 fixture")
	}
	// A fresh ext4 root may contain only its empty formatter-created directory.
	entries, err := p.root.ReadDir(2)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 1 || len(entries) == 1 && (entries[0].Name() != "lost+found" || !entries[0].IsDir()) {
		return nil, errors.New("pressure volume was already used")
	}
	if len(entries) == 1 {
		if err = unix.Unlinkat(int(p.root.Fd()), "lost+found", unix.AT_REMOVEDIR); err != nil {
			return nil, fmt.Errorf("remove empty pressure lost+found: %w", err)
		}
	}
	if err = unix.Mkdirat(int(p.root.Fd()), "workspace", 0700); err != nil {
		return nil, err
	}
	p.workspace, err = nativePressureDirectory(p.Workspace)
	if err != nil {
		return nil, err
	}
	if err = unix.Fstat(int(p.workspace.Fd()), &p.workspaceID); err != nil || p.workspaceID.Dev != p.rootID.Dev {
		return nil, errors.New("pressure workspace is on another device")
	}
	for _, name := range []string{".phebs-index-publication.lock", "ballast"} {
		dir := p.workspace
		if name == "ballast" {
			dir = p.root
		}
		fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if e != nil {
			return nil, e
		}
		f := os.NewFile(uintptr(fd), name)
		if name == "ballast" {
			p.ballast = f
			if err = unix.Fstat(fd, &p.ballastID); err != nil {
				return nil, err
			}
		} else if err = errors.Join(f.Sync(), f.Close()); err != nil {
			return nil, err
		}
	}
	if err = errors.Join(p.workspace.Sync(), p.root.Sync()); err != nil {
		return nil, err
	}
	p.geometry, err = typedworkspace.ObserveCapacity(ctx, p.Workspace)
	if err != nil {
		return nil, err
	}
	p.empty = p.geometry
	if _, err = p.observe(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// Open every ancestor without following links. The final directory must be
// private; ancestors must be root-owned and may not be writable by other users.
func nativePressureDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("noncanonical pressure directory")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		closeErr := unix.Close(fd)
		if openErr != nil || closeErr != nil {
			if openErr == nil {
				_ = unix.Close(next)
			}
			return nil, errors.Join(openErr, closeErr)
		}
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&0022 != 0 || i == len(parts)-1 && st.Mode&07777 != 0700 {
			_ = unix.Close(fd)
			return nil, errors.New("unsafe pressure directory ancestry")
		}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func (p *nativePressureVolume) close() error {
	var err error
	for _, f := range []*os.File{p.ballast, p.workspace, p.root} {
		if f != nil {
			err = errors.Join(err, f.Close())
		}
	}
	p.ballast, p.workspace, p.root = nil, nil, nil
	return err
}

func (p *nativePressureVolume) observe(ctx context.Context) (typedworkspace.CapacityObservation, error) {
	if ctx == nil || p.root == nil || p.workspace == nil || p.ballast == nil {
		return typedworkspace.CapacityObservation{}, errors.New("pressure volume is closed")
	}
	for _, row := range []struct {
		f    *os.File
		path string
		id   unix.Stat_t
	}{{p.root, p.base, p.rootID}, {p.workspace, p.Workspace, p.workspaceID}, {p.ballast, filepath.Join(p.base, "ballast"), p.ballastID}} {
		var held, named unix.Stat_t
		if unix.Fstat(int(row.f.Fd()), &held) != nil || unix.Lstat(row.path, &named) != nil || held.Dev != row.id.Dev || held.Ino != row.id.Ino || named.Dev != held.Dev || named.Ino != held.Ino || held.Uid != 0 || held.Gid != 0 || held.Mode != row.id.Mode || named.Mode != held.Mode || held.Nlink != named.Nlink || held.Uid != named.Uid || held.Gid != named.Gid || held.Size != named.Size || held.Blocks != named.Blocks {
			return typedworkspace.CapacityObservation{}, errors.New("pressure volume identity changed")
		}
	}
	var file unix.Stat_t
	if unix.Fstat(int(p.ballast.Fd()), &file) != nil || file.Mode != unix.S_IFREG|0600 || file.Nlink != 1 || file.Dev != p.rootID.Dev || file.Size < 0 || file.Size > nativePressureImageBytes || file.Size%4096 != 0 || file.Blocks < file.Size/512 || file.Blocks > (file.Size+(1<<20))/512 {
		return typedworkspace.CapacityObservation{}, errors.New("pressure ballast is not bounded allocated custody")
	}
	o, err := typedworkspace.ObserveCapacity(ctx, p.Workspace)
	if err != nil || !sameBase(o, p.geometry) {
		return o, errors.Join(err, errors.New("pressure capacity identity or geometry changed"))
	}
	return o, nil
}

func (p *nativePressureVolume) Observe(t *testing.T, ctx context.Context) typedworkspace.CapacityObservation {
	t.Helper()
	o, err := p.observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (p *nativePressureVolume) RequireBudget(t *testing.T, ctx context.Context, b typedworkspace.OwnerBudget) {
	t.Helper()
	o := p.Observe(t, ctx)
	if b.Bytes <= 0 || b.Inodes == 0 || uint64(b.Bytes) > o.AvailableBytes || nativePressurePercent(o, b.Bytes) < 0 || nativePressurePercent(o, b.Bytes) >= 75 || inodeFloor(o, b.Inodes) != nil {
		t.Fatal("dedicated pressure volume cannot fit the full owner budget with recovery headroom")
	}
}

func nativePressurePercent(o typedworkspace.CapacityObservation, future int64) int {
	if !validCapacity(o) || o.TotalBytes > uint64(nativePressureImageBytes) || future < 0 || uint64(future) > o.TotalBytes {
		return -1
	}
	projected := min(o.TotalBytes, o.TotalBytes-o.AvailableBytes+uint64(future))
	return int((projected*100 + o.TotalBytes - 1) / o.TotalBytes)
}

func (p *nativePressureVolume) setProjected(ctx context.Context, future int64, target int) (typedworkspace.CapacityObservation, error) {
	if target < 1 || target > 94 || nativePressurePercent(p.empty, future) < 0 || nativePressurePercent(p.empty, future) >= 75 {
		return typedworkspace.CapacityObservation{}, errors.New("pressure target or complete future budget is outside the fixture bounds")
	}
	// Aim at the middle of the one-percent production ceiling band. At most four
	// measured corrections compensate for extent metadata; no logical file size
	// or requested percentage substitutes for the final actual statfs result.
	for range 4 {
		o, err := p.observe(ctx)
		if err != nil {
			return o, err
		}
		if nativePressurePercent(o, future) == target {
			return o, nil
		}
		var file unix.Stat_t
		if err = unix.Fstat(int(p.ballast.Fd()), &file); err != nil {
			return o, err
		}
		goal := int64(o.TotalBytes) * int64(2*target-1) / 200
		projected := int64(o.TotalBytes-o.AvailableBytes) + future
		size := (file.Size + goal - projected) / 4096 * 4096
		if size < 0 || size > nativePressureImageBytes {
			return o, errors.New("pressure target cannot be reached with the bounded ballast")
		}
		if err = p.resize(ctx, size); err != nil {
			return o, err
		}
	}
	o, err := p.observe(ctx)
	if err != nil || nativePressurePercent(o, future) != target {
		return o, errors.Join(err, errors.New("actual pressure did not reach the requested one-percent band"))
	}
	return o, nil
}

func (p *nativePressureVolume) resize(ctx context.Context, size int64) error {
	if size < 0 || size > nativePressureImageBytes || size%4096 != 0 {
		return errors.New("pressure ballast size is outside the allocated fixture bound")
	}
	if _, err := p.observe(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.ballast.Truncate(size); err != nil {
		return err
	}
	if size > 0 {
		if err := unix.Fallocate(int(p.ballast.Fd()), 0, 0, size); err != nil {
			return err
		}
	}
	if err := errors.Join(p.ballast.Sync(), unix.Syncfs(int(p.root.Fd()))); err != nil {
		return err
	}
	_, err := p.observe(ctx)
	return err
}

func (p *nativePressureVolume) SetProjected(t *testing.T, ctx context.Context, future int64, target int) typedworkspace.CapacityObservation {
	t.Helper()
	o, err := p.setProjected(ctx, future, target)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (p *nativePressureVolume) clear(ctx context.Context) (typedworkspace.CapacityObservation, error) {
	if err := p.resize(ctx, 0); err != nil {
		return typedworkspace.CapacityObservation{}, err
	}
	return p.observe(ctx)
}

func (p *nativePressureVolume) Clear(t *testing.T, ctx context.Context) typedworkspace.CapacityObservation {
	t.Helper()
	o, err := p.clear(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestNativePressureVolumeClosedBounds(t *testing.T) {
	for _, path := range []string{"", "/var/lib/phebs-typed-acceptance/t459-workspace-pressure-" + strings.Repeat("x", 257) + "/volume", "/tmp/volume", "/var/lib/phebs-typed-index/volume", "/var/lib/phebs-typed-acceptance/t459-workspace-pressure-x/volume/", "/var/lib/phebs-typed-acceptance/t459-workspace-pressure-x/../volume"} {
		t.Run(path, func(t *testing.T) {
			if _, err := nativePressureBind(t.Context(), path); !errors.Is(err, errNativePressurePath) {
				t.Fatal("nonfixture volume did not reach path validation", err)
			}
		})
	}
	for _, size := range []int64{-1, 1, nativePressureImageBytes + 4096} {
		t.Run(fmt.Sprintf("size-%d", size), func(t *testing.T) {
			if err := (&nativePressureVolume{}).resize(t.Context(), size); err == nil {
				t.Fatal("unbounded or unaligned ballast size accepted")
			}
		})
	}
	o := typedworkspace.CapacityObservation{Device: 1, Inode: 1, BlockSize: 4096, TotalBytes: 4 << 30, FreeBytes: 1 << 30, AvailableBytes: 1 << 30, TotalInodes: 262144, FreeInodes: 262144}
	for _, row := range []struct {
		name   string
		future int64
		want   int
	}{{"actual75", 0, 75}, {"projected82", 1 << 28, 82}, {"clamped100", 4 << 30, 100}, {"negative", -1, -1}, {"oversized", (4 << 30) + 1, -1}} {
		t.Run(row.name, func(t *testing.T) {
			if got := nativePressurePercent(o, row.future); got != row.want {
				t.Fatalf("percentage %d, want %d", got, row.want)
			}
		})
	}
}
