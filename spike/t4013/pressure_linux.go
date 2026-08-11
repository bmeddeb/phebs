//go:build linux

package t4013

import (
	"os"

	"golang.org/x/sys/unix"
)

func allocatePressureFile(file *os.File, size int64) error {
	if err := unix.Fallocate(int(file.Fd()), 0, 0, size); err != nil {
		return err
	}
	if err := file.Truncate(size); err != nil {
		return err
	}
	return file.Sync()
}
