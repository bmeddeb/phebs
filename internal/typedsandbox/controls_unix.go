//go:build linux || darwin

package typedsandbox

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const controlDirectoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK

func openControlDirectory(name string) (*os.File, error) {
	if len(name) > 4096 || !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return nil, ErrRefused
	}
	fd, err := unix.Open("/", controlDirectoryFlags, 0)
	if err != nil {
		return nil, ErrRefused
	}
	for _, part := range strings.Split(strings.TrimPrefix(name, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, controlDirectoryFlags, 0)
		closeErr := unix.Close(fd)
		if e != nil || closeErr != nil {
			if e == nil {
				_ = unix.Close(next)
			}
			return nil, ErrRefused
		}
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 && st.Mode&unix.S_ISVTX == 0 {
			_ = unix.Close(fd)
			return nil, ErrRefused
		}
	}
	return os.NewFile(uintptr(fd), "typed-controls"), nil
}

func sameControlStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func controlDirectoryStat(root *os.File, identity ControlIdentity) (unix.Stat_t, error) {
	var st unix.Stat_t
	if unix.Fstat(int(root.Fd()), &st) != nil || uint32(st.Mode) != unix.S_IFDIR|0555 || st.Uid != uint32(os.Geteuid()) || st.Nlink == 0 || uint64(st.Dev) != identity.Device || st.Ino != identity.Inode {
		return st, ErrRefused
	}
	return st, nil
}

func readControlFile(root *os.File, name string, bound int64) ([]byte, error) {
	return readOwnedControlFile(root, name, bound, 0444)
}

func readOwnedControlFile(root *os.File, name string, bound int64, mode uint32) ([]byte, error) {
	var named, opened, after, final unix.Stat_t
	if unix.Fstatat(int(root.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || uint32(named.Mode) != unix.S_IFREG|mode || named.Uid != uint32(os.Geteuid()) || named.Nlink != 1 || named.Size <= 0 || named.Size > bound {
		return nil, ErrRefused
	}
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrRefused
	}
	file := os.NewFile(uintptr(fd), "typed-control")
	defer func() { _ = file.Close() }()
	var directory unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || !sameControlStat(named, opened) || unix.Fstat(int(root.Fd()), &directory) != nil || opened.Dev != directory.Dev {
		return nil, ErrRefused
	}
	raw, err := io.ReadAll(io.LimitReader(file, bound+1))
	if err != nil || int64(len(raw)) != opened.Size || unix.Fstat(fd, &after) != nil || !sameControlStat(opened, after) || unix.Fstatat(int(root.Fd()), name, &final, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameControlStat(after, final) {
		return nil, ErrRefused
	}
	return raw, nil
}

func verifyControls(ctx context.Context, options Options, root *os.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !verifyControlPath(options) || options.scratch == nil {
		return ErrRefused
	}
	before, err := controlDirectoryStat(root, options.Control)
	if err != nil {
		return err
	}
	seal, err := readControlFile(root, ControlSealFile, MaxControlSealBytes)
	if err != nil || controlDigest(seal) != options.Control.SealDigest || checkSealAllowance(seal, options.Allowance, options.Control.Phase, options.Control.RequestDigest) != nil {
		return ErrRefused
	}
	raw, err := readControlFile(root, ScratchAuthorityFile, MaxScratchAuthorityBytes)
	if err != nil {
		return ErrRefused
	}
	authority, err := DecodeScratchAuthority(raw)
	if err != nil || authority != *options.scratch {
		return ErrRefused
	}
	named, err := openControlDirectory(options.Controls)
	if err != nil {
		return ErrRefused
	}
	defer func() { _ = named.Close() }()
	final, err := controlDirectoryStat(named, options.Control)
	if err != nil || !sameControlStat(before, final) {
		return ErrRefused
	}
	return ctx.Err()
}

// readJournalBytes never opens a caller-replaced special file or follows any
// path component. The authenticated owner root remains independently pinned by
// the controller; this checks the named parent again after the bounded read.
func readJournalBytes(name string) ([]byte, error) {
	root, err := openControlDirectory(filepath.Dir(name))
	if err != nil {
		return nil, ErrCustody
	}
	defer func() { _ = root.Close() }()
	var before unix.Stat_t
	if unix.Fstat(int(root.Fd()), &before) != nil || before.Uid != uint32(os.Geteuid()) || before.Mode&0077 != 0 {
		return nil, ErrCustody
	}
	raw, err := readOwnedControlFile(root, filepath.Base(name), MaxContainerJournalBytes, 0600)
	if err != nil {
		return nil, ErrCustody
	}
	named, err := openControlDirectory(filepath.Dir(name))
	if err != nil {
		return nil, ErrCustody
	}
	defer func() { _ = named.Close() }()
	var after unix.Stat_t
	if unix.Fstat(int(named.Fd()), &after) != nil || !sameControlStat(before, after) {
		return nil, ErrCustody
	}
	return raw, nil
}
