//go:build linux

package typedworkspace

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"golang.org/x/sys/unix"
)

const directoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK

func openDirectory(name string, private bool) (*os.File, error) {
	if len(name) > 4096 || !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, ErrCustody
	}
	fd, err := unix.Open("/", directoryFlags, 0)
	if err != nil {
		return nil, ErrCustody
	}
	root := os.NewFile(uintptr(fd), "typed-root")
	for _, part := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := openRelative(root, part, true)
		closeErr := root.Close()
		if err != nil || closeErr != nil {
			if next != nil {
				_ = next.Close()
			}
			return nil, ErrCustody
		}
		root = next
		if private {
			var ancestor unix.Stat_t
			if unix.Fstat(int(root.Fd()), &ancestor) != nil || ancestor.Uid != 0 && ancestor.Uid != uint32(os.Geteuid()) || ancestor.Mode&0022 != 0 && ancestor.Mode&unix.S_ISVTX == 0 {
				_ = root.Close()
				return nil, ErrCustody
			}
		}
	}
	var st unix.Stat_t
	if unix.Fstat(int(root.Fd()), &st) != nil || private && (st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700) {
		_ = root.Close()
		return nil, ErrCustody
	}
	return root, nil
}
func openRelative(root *os.File, name string, directory bool) (*os.File, error) {
	if name != "." && (name == "" || name == ".." || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.Contains(name, "\\") || strings.HasPrefix(name, "../")) {
		return nil, ErrCustody
	}
	fd, err := unix.Openat(int(root.Fd()), ".", directoryFlags, 0)
	if err != nil {
		return nil, ErrCustody
	}
	current := os.NewFile(uintptr(fd), "typed-directory")
	if name == "." {
		if directory {
			return current, nil
		}
		_ = current.Close()
		return nil, ErrCustody
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		flags := directoryFlags
		wantKind := uint32(unix.S_IFDIR)
		if i == len(parts)-1 && !directory {
			flags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
			wantKind = unix.S_IFREG
		}
		// Inspect without following before opening, then bind the actual descriptor
		// to that inode. O_NONBLOCK prevents a racing FIFO replacement from waiting.
		var named unix.Stat_t
		if unix.Fstatat(int(current.Fd()), part, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || uint32(named.Mode)&unix.S_IFMT != wantKind || wantKind == unix.S_IFREG && named.Nlink != 1 {
			_ = current.Close()
			return nil, ErrCustody
		}
		fd, err = unix.Openat(int(current.Fd()), part, flags, 0)
		closeErr := current.Close()
		if err != nil || closeErr != nil {
			if err == nil {
				_ = unix.Close(fd)
			}
			return nil, ErrCustody
		}
		var opened unix.Stat_t
		if unix.Fstat(fd, &opened) != nil || opened.Dev != named.Dev || opened.Ino != named.Ino || uint32(opened.Mode)&unix.S_IFMT != wantKind {
			_ = unix.Close(fd)
			return nil, ErrCustody
		}
		current = os.NewFile(uintptr(fd), "typed-entry")
	}
	return current, nil
}
func relativeParent(root *os.File, name string) (*os.File, string, error) {
	parent, err := openRelative(root, filepath.Dir(name), true)
	return parent, filepath.Base(name), err
}
func mkdir(root *os.File, name string) error {
	parent, leaf, err := relativeParent(root, name)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	if unix.Mkdirat(int(parent.Fd()), leaf, 0700) != nil {
		return ErrCustody
	}
	return nil
}
func createFile(root *os.File, name string) (*os.File, error) {
	parent, leaf, err := relativeParent(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	fd, err := unix.Openat(int(parent.Fd()), leaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, ErrCustody
	}
	return os.NewFile(uintptr(fd), "typed-output"), nil
}

type metadata struct {
	device, inode  uint64
	size, modified int64
	mode           uint32
}

func fileInfo(file *os.File, row typedindex.BundleFile, readonly bool) (metadata, error) {
	var st unix.Stat_t
	info, err := file.Stat()
	if err != nil || unix.Fstat(int(file.Fd()), &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size != row.Bytes || st.Mode&07000 != 0 || (st.Mode&0111 != 0) != row.Executable {
		return metadata{}, ErrCustody
	}
	want := uint32(0444)
	if row.Executable {
		want = 0555
	}
	if readonly && uint32(st.Mode)&07777 != want {
		return metadata{}, ErrCustody
	}
	return metadata{uint64(st.Dev), uint64(st.Ino), st.Size, info.ModTime().UnixNano(), uint32(st.Mode)}, nil
}
func directoryInfo(file *os.File, name string, readonly bool) (Node, error) {
	var st unix.Stat_t
	if unix.Fstat(int(file.Fd()), &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || readonly && st.Mode&07777 != 0555 {
		return Node{}, ErrCustody
	}
	return Node{name, uint64(st.Dev), uint64(st.Ino), true}, nil
}
func capacity(file *os.File) (space, error) {
	var st unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &st) != nil || st.Bsize <= 0 || uint64(st.Bavail) > math.MaxInt64/uint64(st.Bsize) || uint64(st.Blocks) > math.MaxInt64/uint64(st.Bsize) || st.Bfree > st.Blocks || st.Bavail > st.Bfree || st.Ffree > st.Files {
		return space{}, ErrCustody
	}
	return space{bytes: uint64(st.Bavail) * uint64(st.Bsize), inodes: uint64(st.Ffree), block: uint64(st.Bsize), total: uint64(st.Blocks) * uint64(st.Bsize), free: uint64(st.Bfree) * uint64(st.Bsize), totalInodes: uint64(st.Files)}, nil
}
