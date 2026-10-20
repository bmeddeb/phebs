//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

type linuxEpochClassImage struct {
	file  *os.File
	class string
}

func (held *linuxEpochHeldFlow) gauge(ctx context.Context, pid int, birth string) (*ProcessObservationGauge, string, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, "", ErrExecutionEpochOne
	}
	initial, cancel := context.WithTimeout(ctx, epochProcessProbeTimeout)
	defer cancel()
	row, err := linuxEpochProcessRow(pid)
	if err != nil || birth != "" && row.StartIdentity != birth {
		return nil, "", ErrExecutionEpochOne
	}
	images := []linuxEpochClassImage{{held.controllerImage, "controller"}, {held.shellImage, "sh"}}
	for i, role := range []string{"t422-author", "phebs", "zoekt-git-index", "surreal"} {
		images = append(images, linuxEpochClassImage{held.images[i], role})
	}
	for _, image := range held.gitImages {
		images = append(images, linuxEpochClassImage{image, "git"})
	}
	classes := []string{"controller", "git", "phebs", "sh", "surreal", "t422-author", "zoekt-git-index"}
	if pid == held.controllerPID || held.resources != nil && held.resources.trace != nil {
		// Private prospective Linux observation, never a V1-V5 exec class.
		classes = append(classes, linuxEpochOwnedDefunctClass)
		slices.Sort(classes)
	}
	gauge := &ProcessObservationGauge{rootPID: pid, expectedRootStartIdentity: row.StartIdentity, rootObservedName: row.ObservedName, probe: t4013.ObserveProcessTreeRecords, nameClasses: map[string]int{}, observation: ProcessObservation{MeasurementKind: "sampled_observation", NativeHistory: "not_established", SimultaneousBounds: "not_established", Classes: make([]ProcessObservationClass, len(classes))}}
	for i, class := range classes {
		gauge.observation.Classes[i].Class = class
	}
	gauge.classify = func(checkCtx context.Context, observed t4013.NativeProcessRecord) (int, bool) {
		class, refusal := linuxEpochClassifyRowDetail(checkCtx, observed, images)
		if refusal != "" {
			// The resource probe holds probeGate through this complete census.
			// Startup's own live controller row never takes this branch.
			if held.resources != nil && observed.PID != pid {
				if held.resources.trace != nil {
					if index, ok := held.resources.trace.defunctClass(checkCtx, gauge, observed); ok {
						return index, true
					}
				} else if pid == held.controllerPID {
					if index, ok := held.resources.ownedDefunctClass(checkCtx, gauge, observed); ok {
						return index, true
					}
				}
			}
			if held.resources != nil {
				held.resources.failDetail(linuxEpochRowRefusal(refusal, observed))
			}
			return 0, false
		}
		index, present := slices.BinarySearch(classes, class)
		return index, present
	}
	if _, ok := gauge.classify(initial, row); !ok {
		return nil, "", ErrExecutionEpochOne
	}
	return gauge, row.ObservedName, nil
}
func linuxEpochRowRefusal(stage string, row t4013.NativeProcessRecord) string {
	return fmt.Sprintf("classify/%s PID=%d parent=%d birth=%q name=%q RSS=%d", stage, row.PID, row.ParentPID, row.StartIdentity[:min(len(row.StartIdentity), 64)], row.ObservedName[:min(len(row.ObservedName), 16)], row.RSSBytes)
}

func linuxEpochClassifyRow(ctx context.Context, row t4013.NativeProcessRecord, images []linuxEpochClassImage) (string, bool) {
	class, refusal := linuxEpochClassifyRowDetail(ctx, row, images)
	return class, refusal == ""
}

func linuxEpochClassifyRowDetail(ctx context.Context, row t4013.NativeProcessRecord, images []linuxEpochClassImage) (string, string) {
	if ctx == nil || ctx.Err() != nil || row.PID <= 0 {
		return "", "binding-or-context"
	}
	pidfd, err := unix.PidfdOpen(row.PID, 0)
	if err != nil {
		return "", "pidfd-open"
	}
	defer func() { _ = unix.Close(pidfd) }()
	before, err := linuxEpochProcessRow(row.PID)
	if err != nil {
		return "", "row-before"
	}
	if !linuxEpochSameProcessRow(row, before) {
		return "", "binding-before"
	}
	if observed := linuxEpochClassifierPoll(ctx, pidfd); !observed.live() {
		return "", observed.refusal("live-before")
	}
	actual, err := os.Open("/proc/" + strconv.Itoa(row.PID) + "/exe")
	if err != nil {
		return "", "image-open"
	}
	defer func() { _ = actual.Close() }()
	info, err := actual.Stat()
	if err != nil {
		return "", "image-stat"
	}
	class := ""
	for _, image := range images {
		if image.file == nil {
			return "", "held-image-missing"
		}
		expected, e := image.file.Stat()
		if e != nil {
			return "", "held-image-stat"
		}
		if os.SameFile(info, expected) {
			if class != "" && class != image.class {
				return "", "ambiguous-image"
			}
			class = image.class
		}
	}
	after, err := linuxEpochProcessRow(row.PID)
	if err != nil {
		return "", "row-after"
	}
	if class == "" {
		return "", "unknown-image"
	}
	if !linuxEpochSameProcessRow(row, after) {
		return "", "binding-after"
	}
	if observed := linuxEpochClassifierPoll(ctx, pidfd); !observed.live() {
		return "", observed.refusal("live-after")
	}
	if ctx.Err() != nil {
		return "", "context-after"
	}
	return class, ""
}

// Private syscall result, never caller-supplied process/image authority. Only
// an interrupted syscall may reach another real zero-timeout poll. The trace
// actor shares this engine and applies its separate IN/HUP result predicates.
type linuxEpochClassifierPollResult struct {
	n                      int
	revents                int16
	syscallErr, contextErr error
}

func (result linuxEpochClassifierPollResult) live() bool {
	return result.syscallErr == nil && result.contextErr == nil && result.n == 0 && result.revents == 0
}
func (result linuxEpochClassifierPollResult) refusal(stage string) string {
	errnoNumber := int64(0)
	if result.syscallErr != nil {
		var errno unix.Errno
		if errors.As(result.syscallErr, &errno) {
			errnoNumber = int64(errno)
		} else {
			errnoNumber = -1
		}
	}
	canceled := errors.Is(result.contextErr, context.Canceled)
	deadline := errors.Is(result.contextErr, context.DeadlineExceeded)
	return fmt.Sprintf("%s poll_n=%d poll_revents=%d poll_errno=%d context_canceled=%t context_deadline=%t context_other=%t", stage, result.n, uint16(result.revents), errnoNumber, canceled, deadline, result.contextErr != nil && !canceled && !deadline)
}
func linuxEpochClassifierPoll(ctx context.Context, pidfd int) linuxEpochClassifierPollResult {
	return linuxEpochClassifierPollContinue(ctx, pidfd, linuxEpochClassifierPollOnce(ctx, pidfd))
}
func linuxEpochClassifierPollOnce(ctx context.Context, pidfd int) linuxEpochClassifierPollResult {
	result := linuxEpochClassifierPollResult{n: -1}
	if ctx == nil {
		result.contextErr = ErrExecutionEpochOne
		return result
	}
	if result.contextErr = ctx.Err(); result.contextErr != nil {
		return result
	}
	if pidfd < 0 {
		result.syscallErr = unix.EBADF
		return result
	}
	// Recreate the record on every attempt: no undefined interrupted revents
	// can enter a later successful result. Poll never waits or changes ctx.
	polls := [1]unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	result.n, result.syscallErr = unix.Poll(polls[:], 0)
	result.revents = polls[0].Revents
	result.contextErr = ctx.Err()
	return result
}

// This narrow lower syscall-result seam permits a deterministic EINTR control
// followed by an actual same-fd poll, without fake alive results, function
// callbacks or global mutable hooks. Runtime supplies its actual first result.
func linuxEpochClassifierPollContinue(ctx context.Context, pidfd int, result linuxEpochClassifierPollResult) linuxEpochClassifierPollResult {
	for {
		if ctx == nil {
			result.contextErr = ErrExecutionEpochOne
			return result
		}
		if err := ctx.Err(); err != nil {
			result.contextErr = err
			return result
		}
		if result.contextErr != nil || !errors.Is(result.syscallErr, unix.EINTR) {
			return result
		}
		result = linuxEpochClassifierPollOnce(ctx, pidfd)
	}
}

func linuxEpochSameProcessRow(a, b t4013.NativeProcessRecord) bool {
	return a.PID == b.PID && a.ParentPID == b.ParentPID && a.StartIdentity == b.StartIdentity && a.ObservedName == b.ObservedName
}
func linuxEpochProcessRow(pid int) (t4013.NativeProcessRecord, error) {
	var row t4013.NativeProcessRecord
	if pid <= 0 {
		return row, ErrExecutionEpochOne
	}
	file, err := os.Open("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return row, ErrExecutionEpochOne
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > 4096 {
		return row, ErrExecutionEpochOne
	}
	text := string(raw)
	start, end := strings.Index(text, "("), strings.LastIndex(text, ") ")
	if start < 1 || end <= start+1 {
		return row, ErrExecutionEpochOne
	}
	fields := strings.Fields(text[end+2:])
	if len(fields) < 22 {
		return row, ErrExecutionEpochOne
	}
	parsed, pidErr := strconv.Atoi(strings.TrimSpace(text[:start]))
	parent, parentErr := strconv.Atoi(fields[1])
	birth, birthErr := strconv.ParseUint(fields[19], 10, 64)
	name := text[start+1 : end]
	if pidErr != nil || parsed != pid || parentErr != nil || parent < 0 || parent == pid || birthErr != nil || birth == 0 || !validObservedProcessName(name) || len(fields[0]) != 1 || !strings.Contains("RSDTtKWPI", fields[0]) {
		return row, ErrExecutionEpochOne
	}
	return t4013.NativeProcessRecord{PID: pid, ParentPID: parent, StartIdentity: strconv.FormatUint(birth, 10), ObservedName: name}, nil
}
