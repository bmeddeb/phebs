package executableidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// RunningDigest hashes a descriptor verified to name the running executable,
// rather than a deployment path that may now name a replacement. It opens no
// child and retains no descriptor after the one bounded startup hash.
func RunningDigest() (string, error) {
	file, err := openRunningExecutable()
	if err != nil {
		return "", err
	}
	digest, readErr := digestRunningFile(file)
	return digest, errors.Join(readErr, file.Close())
}

func digestRunningFile(file *os.File) (string, error) {
	before, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || before.Mode()&0o111 == 0 ||
		before.Size() <= 0 || before.Size() > maxExecutableBytes {
		return "", errors.New("running executable is not a bounded executable regular file")
	}
	hasher := sha256.New()
	read, err := io.Copy(hasher, io.LimitReader(file, maxExecutableBytes+1))
	if err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if read != before.Size() || !os.SameFile(before, after) ||
		before.Size() != after.Size() || before.Mode() != after.Mode() ||
		!before.ModTime().Equal(after.ModTime()) {
		return "", errors.New("running executable changed during digest")
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}
