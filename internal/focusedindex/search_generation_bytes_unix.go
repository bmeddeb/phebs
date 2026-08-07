//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package focusedindex

import (
	"os"
	"syscall"
)

func allocatedFileBytes(info os.FileInfo) (int64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks < 0 {
		return 0, false
	}
	return stat.Blocks * 512, true
}
