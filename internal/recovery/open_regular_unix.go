//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package recovery

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const restoreReplayNonblockingAvailable = true

// Open first without blocking on a substituted FIFO, then inspect the actual
// descriptor. This preserves regular-file symlink following; archive callers
// separately require their named input to be regular and identity-stable.
func openRecoveryRegular(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
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
