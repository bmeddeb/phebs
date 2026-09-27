//go:build linux

package typedworkspace

import (
	"golang.org/x/sys/unix"
	"os"
)

func renameExclusive(root *os.File, before, after string) error {
	return unix.Renameat2(int(root.Fd()), before, int(root.Fd()), after, unix.RENAME_NOREPLACE)
}
