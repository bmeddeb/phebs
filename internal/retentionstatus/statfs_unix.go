//go:build aix || android || darwin || dragonfly || freebsd || ios || linux

package retentionstatus

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func platformStatFS(file *os.File) (statFSResult, error) {
	if file == nil {
		return statFSResult{}, errors.New("filesystem capacity descriptor is nil")
	}
	var observed unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &observed); err != nil {
		return statFSResult{}, err
	}
	if observed.Bsize <= 0 {
		return statFSResult{}, errors.New("filesystem reported a nonpositive block size")
	}
	return statFSResult{
		Blocks:          uint64(observed.Blocks),
		AvailableBlocks: uint64(observed.Bavail),
		BlockSize:       uint64(observed.Bsize),
	}, nil
}
