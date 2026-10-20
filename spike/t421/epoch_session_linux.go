//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// The sole Wait owner keeps this direct child unreaped until its session has
// drained. A pidfd alone is not a session identifier or post-reap authority.
type linuxEpochSessionAuthority struct {
	held       *linuxEpochHeldFlow
	run        *ExecutionEpochOneRun
	server     *linuxEpochServer
	pid, pidfd int
	birth      string
	exited     bool
}

func captureLinuxEpochSession(held *linuxEpochHeldFlow, run *ExecutionEpochOneRun, server *linuxEpochServer) (*linuxEpochSessionAuthority, error) {
	if held == nil || run == nil || run.command == nil || run.command.Process == nil || server == nil || server.pidfd < 0 || held.servers[run] != server {
		return nil, ErrExecutionEpochOne
	}
	authority := &linuxEpochSessionAuthority{held: held, run: run, server: server, pid: run.command.Process.Pid, pidfd: server.pidfd}
	birth, err := authority.rootIdentity(false)
	if err != nil {
		return nil, err
	}
	authority.birth = birth
	server.session = authority
	return authority, nil
}

func (authority *linuxEpochSessionAuthority) rootIdentity(zombie bool) (string, error) {
	path := "/proc/" + strconv.Itoa(authority.pid)
	file, err := os.Open(path + "/stat")
	if err != nil {
		return "", ErrExecutionEpochOne
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	end := strings.LastIndex(string(raw), ") ")
	if readErr != nil || closeErr != nil || len(raw) > 4096 || end < 1 {
		return "", ErrExecutionEpochOne
	}
	fields := strings.Fields(string(raw[end+2:]))
	if len(fields) < 22 {
		return "", ErrExecutionEpochOne
	}
	parent, pe := strconv.Atoi(fields[1])
	sid, se := strconv.Atoi(fields[3])
	birth, be := strconv.ParseUint(fields[19], 10, 64)
	namespace, ne := os.Stat(path + "/ns/pid")
	actualSID, ae := unix.Getsid(authority.pid)
	if pe != nil || se != nil || be != nil || birth == 0 || parent != authority.held.controllerPID || sid != authority.pid || ae != nil || actualSID != sid || ne != nil || authority.held.pidNamespace == nil || !os.SameFile(namespace, authority.held.pidNamespace) || zombie && fields[0] != "Z" {
		return "", ErrExecutionEpochOne
	}
	return strconv.FormatUint(birth, 10), nil
}

func (authority *linuxEpochSessionAuthority) check(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || authority == nil || authority.held == nil || authority.server == nil || authority.run == nil || authority.run.command == nil || authority.run.command.Process == nil || authority.run.command.Process.Pid != authority.pid || authority.held.servers[authority.run] != authority.server || authority.server.session != authority || authority.pidfd < 0 || authority.server.pidfd != authority.pidfd || !authority.exited || authority.birth == "" {
		return ErrExecutionEpochOne
	}
	var information unix.Siginfo
	for {
		if ctx.Err() != nil {
			return ErrExecutionEpochOne
		}
		err := unix.Waitid(unix.P_PIDFD, authority.pidfd, &information, unix.WEXITED|unix.WNOWAIT|unix.WNOHANG, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || information.Signo != int32(unix.SIGCHLD) {
			return ErrExecutionEpochOne
		}
		break
	}
	birth, err := authority.rootIdentity(true)
	if err != nil || birth != authority.birth || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	return nil
}

// Candidates come from the existing bounded non-zombie census. A candidate PID
// alone never authorizes a signal; all live handles are bound before any signal.
func (held *linuxEpochHeldFlow) stopSession(ctx context.Context, authority *linuxEpochSessionAuthority) (bool, error) {
	if ctx == nil || ctx.Err() != nil || authority == nil || authority.held != held {
		return false, ErrExecutionEpochOne
	}
	sid := authority.pid
	if held.resources != nil {
		select {
		case held.resources.probeGate <- struct{}{}:
			defer func() { <-held.resources.probeGate }()
		case <-ctx.Done():
			return false, ErrExecutionEpochOne
		}
	}
	if authority.check(ctx) != nil {
		return false, ErrExecutionEpochOne
	}
	candidates, err := t4013.ObserveLinuxPrivateProcessSessionPIDs(sid)
	if err != nil {
		return false, ErrExecutionEpochOne
	}
	if len(candidates) == 0 {
		if err := authority.check(ctx); err != nil {
			return false, err
		}
		return true, nil
	}
	fds := make([]int, 0, len(candidates))
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}()
	for _, pid := range candidates {
		if ctx == nil || ctx.Err() != nil || pid == held.controllerPID {
			return false, ErrExecutionEpochOne
		}
		fd, e := unix.PidfdOpen(pid, 0)
		if errors.Is(e, unix.ESRCH) {
			continue
		}
		if e != nil {
			return false, ErrExecutionEpochOne
		}
		fds = append(fds, fd)
		polls := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, e := unix.Poll(polls, 0)
		if e != nil {
			return false, ErrExecutionEpochOne
		}
		if n != 0 && polls[0].Revents == unix.POLLIN {
			continue
		}
		if n != 0 {
			return false, ErrExecutionEpochOne
		}
		before, e := linuxEpochProcessRow(pid)
		firstSID, se := unix.Getsid(pid)
		namespace, ne := os.Stat("/proc/" + strconv.Itoa(pid) + "/ns/pid")
		if e != nil || se != nil || ne != nil || firstSID != sid || held.pidNamespace == nil || !os.SameFile(namespace, held.pidNamespace) {
			return false, ErrExecutionEpochOne
		}
		gauge, _, e := held.gauge(ctx, pid, before.StartIdentity)
		if e != nil {
			return false, ErrExecutionEpochOne
		}
		if _, ok := gauge.classify(ctx, before); !ok {
			return false, ErrExecutionEpochOne
		}
		after, e := linuxEpochProcessRow(pid)
		lastSID, se := unix.Getsid(pid)
		n, pe := unix.Poll(polls, 0)
		if e != nil || se != nil || pe != nil || lastSID != sid || !linuxEpochSameProcessRow(before, after) || n != 0 || polls[0].Revents != 0 {
			return false, ErrExecutionEpochOne
		}
	}
	if authority.check(ctx) != nil {
		return false, ErrExecutionEpochOne
	}
	for _, fd := range fds {
		if ctx.Err() != nil {
			return false, ErrExecutionEpochOne
		}
		if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			return false, ErrExecutionEpochOne
		}
	}
	// Signaling is not death. A later empty census must precede the separate
	// actual Handle.Wait. Zombies keep their historical exclusion.
	return false, ErrExecutionEpochOne
}
