package packrelease

import (
	"os"
	"syscall"
)

func artifactChangeTimeEqual(before, after os.FileInfo) bool {
	a, aOK := before.Sys().(*syscall.Stat_t)
	b, bOK := after.Sys().(*syscall.Stat_t)
	return aOK && bOK && a.Ctimespec == b.Ctimespec
}
