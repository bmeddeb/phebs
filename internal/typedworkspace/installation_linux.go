//go:build linux

package typedworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ReadInstallationControl reads one digest-bound service-owned 0600 control
// through a private 0700 directory. Every component is nonfollowing. It checks
// named and held identity before/after the bounded read and closes all FDs.
// It creates no custody and is not a native execution admission.
func ReadInstallationControl(ctx context.Context, directory, name, digest string, limit int64) (raw []byte, err error) {
	if ctx == nil || limit < 1 || limit > 32<<20 || name == "." || name == ".." || name == "" || filepath.Base(name) != name {
		return nil, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	root, err := openDirectory(directory, true)
	if err != nil {
		return nil, fmt.Errorf("open installation directory: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	file, err := openRelative(root, name, false)
	if err != nil {
		return nil, fmt.Errorf("open installation control: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	var before, after, named unix.Stat_t
	if unix.Fstat(int(file.Fd()), &before) != nil || before.Mode != unix.S_IFREG|0600 || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 || before.Size < 1 || before.Size > limit {
		return nil, fmt.Errorf("installation control metadata: %w", ErrCustody)
	}
	raw, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != before.Size || "sha256:"+hex.EncodeToString(sum[:]) != digest || unix.Fstat(int(file.Fd()), &after) != nil || unix.Fstatat(int(root.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || !installationSame(before, after) || !installationSame(before, named) {
		return nil, fmt.Errorf("installation control read identity: %w", ErrCustody)
	}
	if err = ownerSameDirectory(root, directory); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

func installationSame(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}
