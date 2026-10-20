//go:build linux

package dispatchadmission

import (
	"encoding/binary"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const selectedLinuxToolSeals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_EXEC | unix.F_SEAL_SEAL

func openSelectedProductionToolImage(role, path string) (*os.File, bool, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil || lifetime.program != ProgramPhebs || lifetime.semanticMode != ProductionSemanticV3 ||
		role != "surreal" && role != "zoekt-git-index" || lifetime.tools[role].Path != path || !strings.HasPrefix(path, "/proc/") {
		return nil, false, nil
	}
	if runtime.GOARCH != "amd64" || lifetime.client == nil || lifetime.client.Context().Err() != nil || lifetime.inputSHA256 == ([32]byte{}) {
		return nil, true, ErrProductionBootstrap
	}
	parent := os.Getppid()
	file, err := openLinuxSelectedToolImage(path, parent)
	if err == nil && (os.Getppid() != parent || lifetime.client.Context().Err() != nil) {
		_ = file.Close()
		return nil, true, ErrProductionBootstrap
	}
	return file, true, err
}

func openLinuxSelectedToolImage(path string, parent int) (*os.File, error) {
	parts := strings.Split(path, "/")
	if parent <= 1 || len(parts) != 5 || parts[0] != "" || parts[1] != "proc" || parts[2] != strconv.Itoa(parent) || parts[3] != "fd" {
		return nil, ErrProductionBootstrap
	}
	fd, err := strconv.Atoi(parts[4])
	if err != nil || fd < 3 || fd > 1<<20 || parts[4] != strconv.Itoa(fd) {
		return nil, ErrProductionBootstrap
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrProductionBootstrap
	}
	if verifyLinuxSelectedToolImage(file) != nil {
		_ = file.Close()
		return nil, ErrProductionBootstrap
	}
	return file, nil
}

func verifyLinuxSelectedToolImage(file *os.File) error {
	if file == nil {
		return ErrProductionBootstrap
	}
	fd := int(file.Fd())
	var stat unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode != unix.S_IFREG|0o500 || stat.Nlink != 0 || stat.Uid != uint32(os.Geteuid()) || stat.Size < 64 || stat.Size > 256<<20 || unix.Fstatfs(fd, &fs) != nil || fs.Type != unix.TMPFS_MAGIC {
		return ErrProductionBootstrap
	}
	seals, sealErr := unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
	flags, flagErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	inheritance, inheritanceErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if sealErr != nil || seals&selectedLinuxToolSeals != selectedLinuxToolSeals || flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY || inheritanceErr != nil || inheritance&unix.FD_CLOEXEC == 0 {
		return ErrProductionBootstrap
	}
	var header [64]byte
	if n, err := file.ReadAt(header[:], 0); err != nil || n != len(header) || string(header[:4]) != "\x7fELF" || header[4] != 2 || header[5] != 1 || header[6] != 1 || binary.LittleEndian.Uint16(header[18:20]) != 62 {
		return ErrProductionBootstrap
	}
	return nil
}
