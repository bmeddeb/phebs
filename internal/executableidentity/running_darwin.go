package executableidentity

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Darwin's public PROC_PIDREGIONPATHINFO ABI: a 96-byte region descriptor,
// 152-byte vnode descriptor and 1,024-byte path. Inspect the mapping containing
// this function, then require the opened file's device/inode to match that
// mapping. A renamed image remains usable if its vnode path can be opened;
// an unlinked or replaced image refuses rather than claiming replacement bytes.
// ABI reference: https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info.h
const darwinRegionPathBytes = 96 + 152 + 1024

func openRunningExecutable() (*os.File, error) {
	pc := reflect.ValueOf(openRunningExecutable).Pointer()
	var record [darwinRegionPathBytes]byte
	returned, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, //nolint:staticcheck // x/sys has no proc_pidinfo wrapper.
		2, uintptr(os.Getpid()), 8, pc,
		uintptr(unsafe.Pointer(&record[0])), uintptr(len(record)),
	)
	if errno != 0 {
		return nil, fmt.Errorf("inspect running executable mapping: %w", errno)
	}
	if returned != uintptr(len(record)) {
		return nil, errors.New("running executable mapping is unavailable")
	}
	start := binary.NativeEndian.Uint64(record[80:88])
	size := binary.NativeEndian.Uint64(record[88:96])
	if uint64(pc) < start || uint64(pc)-start >= size ||
		binary.NativeEndian.Uint32(record[:4])&4 == 0 {
		return nil, errors.New("running executable mapping does not contain executable code")
	}
	pathBytes := record[248:]
	end := bytes.IndexByte(pathBytes, 0)
	if end <= 0 {
		return nil, errors.New("running executable mapping has no bounded path")
	}
	path := string(pathBytes[:end])
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("running executable mapping path is not absolute and clean")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open running executable mapping: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() ||
			uint32(stat.Dev) != binary.NativeEndian.Uint32(record[96:100]) ||
			stat.Ino != binary.NativeEndian.Uint64(record[104:112]) {
			err = errors.New("running executable pathname now names another image")
		}
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
