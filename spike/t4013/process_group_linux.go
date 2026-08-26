//go:build linux

package t4013

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func privateServerSessionPIDs(sessionID int) ([]int, error) {
	if sessionID <= 0 {
		return nil, errors.New("T40.13 private process session is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()
	output, err := boundedCommandOutput(ctx, maxProcessProbeBytes, "/bin/ps", "-Ao", "pid=,stat=")
	if ctx.Err() != nil {
		return nil, fmt.Errorf("inspect T40.13 private process session: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("inspect T40.13 private process session: %w", err)
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	pids := make([]int, 0, 16)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, errors.New("T40.13 private process session inventory is invalid")
		}
		pid, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil || pid <= 0 {
			return nil, errors.New("T40.13 private process session inventory is invalid")
		}
		session, sessionErr := unix.Getsid(pid)
		if errors.Is(sessionErr, syscall.ESRCH) {
			continue
		}
		if sessionErr != nil {
			return nil, fmt.Errorf("inspect T40.13 process session identity: %w", sessionErr)
		}
		if session != sessionID || strings.HasPrefix(fields[1], "Z") {
			continue
		}
		pids = append(pids, pid)
		if len(pids) > 1024 {
			return nil, errors.New("T40.13 private process session exceeds its process bound")
		}
	}
	return pids, nil
}
