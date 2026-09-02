// Package executableidentity verifies an optional content identity for a
// canonical executable path immediately before a caller launches it.
package executableidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

const maxExecutableBytes = int64(2 << 30)

// Verify is a no-op when expected is empty. A supplied identity is closed
// sha256 syntax and binds one non-symlink executable regular file.
func Verify(path, expected string) error {
	if expected == "" {
		return nil
	}
	if len(expected) != len("sha256:")+sha256.Size*2 ||
		!strings.HasPrefix(expected, "sha256:") {
		return errors.New("expected executable digest is invalid")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(expected, "sha256:")); err != nil {
		return errors.New("expected executable digest is invalid")
	}
	actual, err := Digest(path)
	if err != nil || actual != expected {
		return errors.Join(err, errors.New("executable identity changed before launch"))
	}
	return nil
}

// Digest returns the exact sha256 identity of one stable executable regular
// file while rejecting path replacement during the read.
func Digest(path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Mode()&0o111 == 0 || before.Size() <= 0 || before.Size() > maxExecutableBytes {
		return "", errors.Join(err, errors.New("executable identity changed during digest"))
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	opened, openStatErr := file.Stat()
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, maxExecutableBytes+1))
	afterOpen, afterOpenErr := file.Stat()
	closeErr := file.Close()
	afterPath, afterPathErr := os.Lstat(path)
	if openStatErr != nil || copyErr != nil || afterOpenErr != nil || closeErr != nil ||
		afterPathErr != nil || !os.SameFile(before, opened) || !os.SameFile(opened, afterOpen) ||
		!os.SameFile(afterOpen, afterPath) || afterPath.Mode()&os.ModeSymlink != 0 ||
		written != before.Size() {
		return "", errors.Join(
			openStatErr, copyErr, afterOpenErr, closeErr, afterPathErr,
			errors.New("executable identity changed during digest"),
		)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
