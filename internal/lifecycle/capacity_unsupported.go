//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !ios && !linux

package lifecycle

import (
	"errors"
	"os"
)

func platformCapacity(*os.File) (int64, int64, error) {
	return 0, 0, errors.New("filesystem lifecycle capacity is unavailable on this platform")
}
