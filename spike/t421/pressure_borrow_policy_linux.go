//go:build linux

package t421

import (
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// This is an inherited ceiling, never a grant of usable child capabilities.
const linuxPressureBorrowBoundingMask uint64 = (uint64(1) << 41) - 1

type linuxPressureBorrowPolicy struct {
	uid, gid uint32
}

func (policy linuxPressureBorrowPolicy) valid() bool {
	return policy.uid != 0 && policy.gid != 0 && policy.uid != ^uint32(0) && policy.gid != ^uint32(0)
}

func linuxPressureBorrowBounding() bool {
	var mask uint64
	for capability := 0; capability < 64; capability++ {
		present, err := unix.PrctlRetInt(unix.PR_CAPBSET_READ, uintptr(capability), 0, 0, 0)
		if err == unix.EINVAL {
			continue
		}
		if err != nil || present < 0 || present > 1 {
			return false
		}
		mask |= uint64(present) << capability
	}
	return mask == linuxPressureBorrowBoundingMask
}

// Called only in the new child, before authenticated protocol traffic/helpers.
// The original broker's bounding set and credentials are never changed.
func (policy linuxPressureBorrowPolicy) constrainChild() error {
	if runtime.GOARCH != "amd64" || policy.checkCredentials() != nil {
		return ErrLinuxPressure
	}
	_, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0, 0)
	if errno != 0 {
		return ErrLinuxPressure
	}
	return policy.checkChild()
}

func (policy linuxPressureBorrowPolicy) checkChild() error {
	privs, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || privs != 1 || policy.checkCredentials() != nil {
		return ErrLinuxPressure
	}
	return nil
}

func (policy linuxPressureBorrowPolicy) checkCredentials() error {
	ruid, euid, suid := unix.Getresuid()
	rgid, egid, sgid := unix.Getresgid()
	groups, err := os.Getgroups()
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if !policy.valid() || uint32(ruid) != policy.uid || uint32(euid) != policy.uid || uint32(suid) != policy.uid ||
		uint32(rgid) != policy.gid || uint32(egid) != policy.gid || uint32(sgid) != policy.gid || err != nil || len(groups) != 0 ||
		unix.Capget(&header, &data[0]) != nil || data != [2]unix.CapUserData{} || !linuxPressureBorrowBounding() {
		return ErrLinuxPressure
	}
	for capability := 0; capability <= 40; capability++ {
		present, capErr := unix.PrctlRetInt(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_IS_SET, uintptr(capability), 0, 0)
		if capErr != nil || present != 0 {
			return ErrLinuxPressure
		}
	}
	return nil
}
