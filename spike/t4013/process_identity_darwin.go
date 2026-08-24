//go:build darwin

package t4013

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/sys/unix"
)

func processStartIdentity(pid int, _ processSnapshot) (processIdentityObservation, error) {
	if pid <= 0 {
		return processIdentityObservation{}, errors.New("T40.13 process identity PID is invalid")
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.EIO) || errors.Is(err, unix.ESRCH) {
			return processIdentityObservation{}, errors.Join(
				errProcessIdentityDisappeared,
				fmt.Errorf("T40.13 inspect process identity: %w", err),
			)
		}
		return processIdentityObservation{}, fmt.Errorf("T40.13 inspect process identity: %w", err)
	}
	started := process.Proc.P_starttime
	nameBytes := process.Proc.P_comm[:]
	if end := bytes.IndexByte(nameBytes, 0); end >= 0 {
		nameBytes = nameBytes[:end]
	}
	if process.Proc.P_pid != int32(pid) || process.Eproc.Ppid < 0 || len(nameBytes) == 0 ||
		started.Sec <= 0 || started.Usec < 0 || started.Usec >= 1_000_000 {
		return processIdentityObservation{}, errors.New("T40.13 process identity is invalid")
	}
	return processIdentityObservation{
		token:  strconv.FormatInt(started.Sec, 10) + ":" + strconv.FormatInt(int64(started.Usec), 10),
		parent: int(process.Eproc.Ppid),
		name:   string(nameBytes),
	}, nil
}
