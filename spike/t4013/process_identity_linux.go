//go:build linux

package t4013

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func processStartIdentity(pid int, _ processSnapshot) (processIdentityObservation, error) {
	if pid <= 0 {
		return processIdentityObservation{}, errors.New("T40.13 process identity PID is invalid")
	}
	file, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return processIdentityObservation{}, errors.Join(
				errProcessIdentityDisappeared,
				fmt.Errorf("T40.13 open process identity: %w", err),
			)
		}
		return processIdentityObservation{}, fmt.Errorf("T40.13 open process identity: %w", err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return processIdentityObservation{}, fmt.Errorf("T40.13 read process identity: %w", err)
	}
	if len(content) == 0 || len(content) > 4096 {
		return processIdentityObservation{}, errors.New("T40.13 process identity exceeds its bound")
	}
	text := string(content)
	commStart := strings.Index(text, "(")
	commEnd := strings.LastIndex(text, ") ")
	if commStart < 0 || commEnd <= commStart+1 {
		return processIdentityObservation{}, errors.New("T40.13 process identity is invalid")
	}
	fields := strings.Fields(text[commEnd+2:])
	if len(fields) < 20 {
		return processIdentityObservation{}, errors.New("T40.13 process identity is invalid")
	}
	observedPID, pidErr := strconv.Atoi(strings.TrimSpace(text[:commStart]))
	parent, parentErr := strconv.Atoi(fields[1])
	started := fields[19]
	if value, err := strconv.ParseUint(started, 10, 64); pidErr != nil || parentErr != nil || err != nil ||
		observedPID != pid || parent < 0 || value == 0 {
		return processIdentityObservation{}, errors.New("T40.13 process identity is invalid")
	}
	return processIdentityObservation{token: started, parent: parent, name: text[commStart+1 : commEnd]}, nil
}
