//go:build aix || android || darwin || dragonfly || freebsd || ios || linux

package lifecycle

import (
	"errors"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

func platformCapacity(root *os.File) (int64, int64, error) {
	if root == nil {
		return 0, 0, errors.New("lifecycle capacity descriptor is nil")
	}
	var observed unix.Statfs_t
	if err := unix.Fstatfs(int(root.Fd()), &observed); err != nil {
		return 0, 0, err
	}
	if observed.Bsize <= 0 {
		return 0, 0, errors.New("filesystem returned nonpositive lifecycle block size")
	}
	blockSize := uint64(observed.Bsize)
	if uint64(observed.Blocks) > math.MaxInt64/blockSize ||
		uint64(observed.Bavail) > math.MaxInt64/blockSize {
		return 0, 0, errors.New("filesystem lifecycle capacity overflows int64")
	}
	return int64(uint64(observed.Blocks) * blockSize),
		int64(uint64(observed.Bavail) * blockSize), nil
}
