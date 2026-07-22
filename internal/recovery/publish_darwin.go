//go:build darwin

package recovery

import "golang.org/x/sys/unix"

func publishDirectory(from, to string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_EXCL)
}
