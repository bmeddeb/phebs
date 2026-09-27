//go:build linux

package typedsandbox

import (
	"golang.org/x/sys/unix"
)

func readScratch() (ScratchAuthority, error) {
	raw, err := readSmall("/inputs/"+ScratchAuthorityFile, 4096)
	if err != nil {
		return ScratchAuthority{}, ErrRefused
	}
	return DecodeScratchAuthority(raw)
}
func ValidateWorker() error {
	scratch, err := readScratch()
	if err != nil || verifyKernelLimits() != nil {
		return ErrRefused
	}
	return validateWorker(&scratch)
}
func verifyScratch(a ScratchAuthority) error {
	var fs unix.Statfs_t
	var st unix.Stat_t
	if a.Validate() != nil || unix.Statfs("/scratch", &fs) != nil || unix.Stat("/scratch", &st) != nil ||
		fs.Type != unix.EXT4_SUPER_MAGIC || fs.Bsize != int64(a.BlockSize) || fs.Blocks != a.Blocks || fs.Files != a.Inodes ||
		fs.Bfree > fs.Blocks || fs.Bavail > fs.Bfree || fs.Ffree > fs.Files ||
		fs.Flags&(unix.ST_NOSUID|unix.ST_NODEV) != unix.ST_NOSUID|unix.ST_NODEV || fs.Flags&(unix.ST_NOEXEC|unix.ST_RDONLY) != 0 ||
		unix.Major(uint64(st.Dev)) != a.DeviceMajor || unix.Minor(uint64(st.Dev)) != a.DeviceMinor {
		return ErrRefused
	}
	raw, err := readSmall("/proc/self/mountinfo", 1<<20)
	if err != nil {
		return ErrRefused
	}
	return verifyScratchMounts(string(raw), a)
}
