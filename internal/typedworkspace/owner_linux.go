//go:build linux

package typedworkspace

import (
	"golang.org/x/sys/unix"
	"os"
)

func ownerReplace(root *os.File, before, after string) error {
	return unix.Renameat(int(root.Fd()), before, int(root.Fd()), after)
}
