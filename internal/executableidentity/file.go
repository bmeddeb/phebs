package executableidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

// DigestFile measures an already-open executable. The caller establishes its
// route/protection authority; this function grants none and changes no offset.
func DigestFile(file *os.File) (string, error) {
	if file == nil {
		return "", errors.New("executable descriptor is nil")
	}
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode()&0o111 == 0 || before.Size() <= 0 || before.Size() > maxExecutableBytes {
		return "", errors.Join(err, errors.New("executable descriptor is invalid"))
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.NewSectionReader(file, 0, before.Size()+1))
	after, statErr := file.Stat()
	if copyErr != nil || statErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || written != before.Size() {
		return "", errors.Join(copyErr, statErr, errors.New("executable descriptor changed during digest"))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// VerifyFile requires a nonempty digest. It is used only after a separate
// selected-route verifier has checked the held kernel object.
func VerifyFile(file *os.File, expected string) error {
	if len(expected) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(expected, "sha256:") {
		return errors.New("expected executable digest is invalid")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(expected, "sha256:")); err != nil {
		return errors.New("expected executable digest is invalid")
	}
	actual, err := DigestFile(file)
	if err != nil || actual != expected {
		return errors.Join(err, errors.New("executable identity changed before launch"))
	}
	return nil
}
