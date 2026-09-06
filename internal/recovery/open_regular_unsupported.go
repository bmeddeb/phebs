//go:build !(aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris || zos)

package recovery

import (
	"errors"
	"os"
)

const restoreReplayNonblockingAvailable = false

// Preserve the pre-existing offline verification path on other platforms.
// Protected native replay is not selected there: this open makes no Unix
// nonblocking/FIFO-substitution guarantee.
func openRecoveryRegular(path string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("recovery input is not a regular file")
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
