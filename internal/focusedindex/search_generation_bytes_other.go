//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package focusedindex

import "os"

func allocatedFileBytes(os.FileInfo) (int64, bool) { return 0, false }
