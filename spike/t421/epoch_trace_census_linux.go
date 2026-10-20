//go:build linux

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

// Same host-directory bound as the existing Linux process census. This final
// retirement check reads status only (no smaps, executable or corpus hashing).
// It includes Z leaders, which the historical live-session census excludes.
const linuxEpochTraceMaxHostEntries = 8192

func (root *linuxEpochTraceRoot) finalTracerCensus(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || !root.gateOwned || !root.exitSeen || len(root.tasks) != 1 || len(root.pending) != 0 {
		return ErrExecutionEpochOne
	}
	directory, err := os.Open("/proc")
	if err != nil {
		return err
	}
	entries, readErr := directory.Readdirnames(linuxEpochTraceMaxHostEntries + 1)
	closeErr := directory.Close()
	if closeErr != nil || readErr != nil && !errors.Is(readErr, io.EOF) || len(entries) > linuxEpochTraceMaxHostEntries {
		return ErrExecutionEpochOne
	}
	found := false
	for _, name := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pid, e := strconv.Atoi(name)
		if e != nil || pid <= 0 {
			continue
		}
		raw, e := linuxEpochTraceRead("/proc/"+name+"/status", 64<<10)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		tracer, matched := 0, false
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "TracerPid:" {
				if matched {
					return ErrExecutionEpochOne
				}
				tracer, e = strconv.Atoi(fields[1])
				if e != nil {
					return e
				}
				matched = true
			}
		}
		if !matched {
			return ErrExecutionEpochOne
		}
		if tracer != root.tid {
			continue
		}
		if pid != root.original.pid {
			return ErrExecutionEpochOne
		}
		found = true
	}
	if !found {
		return ErrExecutionEpochOne
	}
	return nil
}
