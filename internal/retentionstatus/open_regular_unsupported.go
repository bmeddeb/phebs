//go:build !(aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris || zos)

package retentionstatus

import (
	"errors"
	"os"
)

func openRootedRegular(*os.Root, string) (*os.File, error) {
	return nil, errors.New("rooted nonblocking metadata opens are unsupported on this platform")
}
