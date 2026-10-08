//go:build linux

package t4013

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// privateServerSessionPIDs enumerates one session from a single bounded native
// /proc census. It launches no helper, so supervision cannot itself add a
// session member and cannot meet a setuid-tool denial; the same census also
// widens the defunct filter from ps's "Z" prefix to every native dead state.
// Sequential reads are not an atomic session snapshot: membership is confirmed
// before and after the liveness read, and a vanished or defunct member is
// skipped rather than reported.
func privateServerSessionPIDs(sessionID int) ([]int, error) {
	if sessionID <= 0 {
		return nil, errors.New("T40.13 private process session is invalid")
	}
	hostPIDs, err := linuxHostProcessPIDs("/proc")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, 16)
	for _, pid := range hostPIDs {
		session, err := unix.Getsid(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect T40.13 process session identity: %w", err)
		}
		if session != sessionID {
			continue
		}
		defunct, present, err := linuxProcessDefunctStatus(pid)
		if err != nil {
			return nil, err
		}
		if !present || defunct {
			continue
		}
		confirmedSession, err := unix.Getsid(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reinspect T40.13 process session identity: %w", err)
		}
		if confirmedSession != sessionID {
			continue
		}
		pids = append(pids, pid)
		if len(pids) > 1024 {
			return nil, errors.New("T40.13 private process session exceeds its process bound")
		}
	}
	return pids, nil
}
