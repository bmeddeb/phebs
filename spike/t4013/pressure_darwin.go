//go:build darwin

package t4013

import (
	"os"

	"golang.org/x/sys/unix"
)

func allocatePressureFile(file *os.File, size int64) error {
	allocation := &unix.Fstore_t{
		Flags: unix.F_ALLOCATEALL, Posmode: unix.F_PEOFPOSMODE, Length: size,
	}
	if err := unix.FcntlFstore(file.Fd(), unix.F_PREALLOCATE, allocation); err != nil {
		return err
	}
	if err := file.Truncate(size); err != nil {
		return err
	}
	return file.Sync()
}
