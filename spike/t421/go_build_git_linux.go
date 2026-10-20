//go:build linux

package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// The protected Git tree has no canonical host pathname. Copy its seven fixed
// aliases through the borrowed root, retaining the loan through every read.
func (staging *linuxGoBuildStaging) prepareGit(ctx context.Context, git *LinuxExecutionGitCustody, home, temporary string) error {
	if ctx == nil || ctx.Err() != nil || staging.err != nil || git == nil || staging.makeDirectory("git/core") != nil {
		return staging.refuse()
	}
	err := git.WithGit(ctx, home, temporary, func(identity ExecutionToolIdentity, root *os.File, _ []string) error {
		for _, name := range executionGitImageNames() {
			if staging.copyGitAlias(ctx, root, name, identity.SHA256) != nil {
				return staging.refuse()
			}
		}
		return nil
	})
	if err != nil {
		return staging.refuse()
	}
	return nil
}

func (staging *linuxGoBuildStaging) copyGitAlias(ctx context.Context, root *os.File, name, expected string) (retErr error) {
	path := "git/core/" + name
	fd, err := unix.Openat(int(root.Fd()), "core/"+name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return staging.refuse()
	}
	source := os.NewFile(uintptr(fd), "protected-go-git-alias")
	defer func() {
		if source.Close() != nil || retErr != nil {
			retErr = staging.refuse()
		}
	}()
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o500 || before.Size() <= 0 ||
		staging.addName(path) != nil || staging.reserveFile(before.Size()) != nil {
		return staging.refuse()
	}
	output, err := staging.tree.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return staging.refuse()
	}
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, digest), executionInputReader{ctx, io.NewSectionReader(source, 0, before.Size())})
	chmodErr, syncErr := output.Chmod(0o500), output.Sync()
	info, statErr := output.Stat()
	closeErr := output.Close()
	after, afterErr := source.Stat()
	if copyErr != nil || n != before.Size() || chmodErr != nil || syncErr != nil || statErr != nil || closeErr != nil || afterErr != nil ||
		!inputCustodySame(before, after) || "sha256:"+hex.EncodeToString(digest.Sum(nil)) != expected || ctx.Err() != nil {
		return staging.refuse()
	}
	staging.names[path] = info
	return nil
}
