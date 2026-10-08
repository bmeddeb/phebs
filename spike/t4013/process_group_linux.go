//go:build linux

package t4013

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// maxProcessSessionMembers bounds one session census at sixteen times
// maxProcessDescendants, so a runaway session refuses instead of enumerating an
// unbounded host. Per-poll work stays bounded at this many entries, each costing
// one getsid(2) plus, for a member, one record read and one confirming getsid(2).
const maxProcessSessionMembers = 1024

// privateServerSessionPIDs enumerates the real host /proc through the native
// session and defunct observers.
func privateServerSessionPIDs(sessionID int) ([]int, error) {
	return privateServerSessionPIDsAt("/proc", sessionID, unix.Getsid, linuxProcessDefunctStatus)
}

// privateServerSessionPIDsAt enumerates one session from a single bounded native
// /proc census. It launches no helper, so supervision cannot itself add a
// session member and cannot meet a setuid-tool denial; the same census also
// widens the defunct filter from ps's "Z" prefix to every native dead state.
// Sequential reads are not an atomic session snapshot: membership is confirmed
// before and after the liveness read, and a vanished or defunct member is
// skipped rather than reported.
//
// procRoot, sessionOf and defunctOf are the same seams the shared fence takes:
// production passes "/proc" and the native observers, while a synthetic census
// drives the member bound, which no real single session can reach.
func privateServerSessionPIDsAt(
	procRoot string,
	sessionID int,
	sessionOf func(int) (int, error),
	defunctOf func(int) (bool, bool, error),
) ([]int, error) {
	if sessionID <= 0 {
		return nil, errors.New("T40.13 private process session is invalid")
	}
	hostPIDs, err := linuxHostProcessPIDs(procRoot)
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, 16)
	for _, pid := range hostPIDs {
		session, err := sessionOf(pid)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect T40.13 process session identity: %w", err)
		}
		if session != sessionID {
			continue
		}
		defunct, present, err := defunctOf(pid)
		if err != nil {
			return nil, err
		}
		if !present || defunct {
			continue
		}
		confirmedSession, err := sessionOf(pid)
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
		if len(pids) > maxProcessSessionMembers {
			return nil, errors.New("T40.13 private process session exceeds its process bound")
		}
	}
	return pids, nil
}
