//go:build linux

package t421

import (
	"errors"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

// validateExternalToolNativeImage screens the selected image as a bounded
// native ELF64 executable using the measured header bounds. It proves neither
// vendor authenticity, load-command or CPU-feature compatibility, nor the
// absence of native delegation at runtime. The caller separately pins the
// exact path and digest.
func validateExternalToolNativeImage(path string) (retErr error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o111 == 0 || before.Size() < 8 {
		return errors.New("external tool image is not an executable regular file")
	}
	file, err := t4013.OpenHostImage(path)
	if err != nil {
		return errors.New("external tool image cannot be opened")
	}
	defer func() {
		after, statErr := file.Stat()
		current, pathErr := os.Lstat(path)
		closeErr := file.Close()
		if statErr != nil || pathErr != nil || closeErr != nil ||
			!sameCheckoutFile(before, after) || !sameCheckoutFile(before, current) {
			retErr = errors.Join(retErr, errors.New("external tool image changed during header inspection"))
		}
	}()
	opened, err := file.Stat()
	if err != nil || !sameCheckoutFile(before, opened) {
		return errors.New("external tool image changed before header inspection")
	}
	if !linuxInputELF(file, opened.Size()) {
		return errors.New("external tool image is not a bounded native ELF64 executable")
	}
	return nil
}
