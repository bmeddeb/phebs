//go:build linux

package t421

import (
	"context"
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
	gauge := &ProcessObservationGauge{rootPID: pid, expectedRootStartIdentity: row.StartIdentity, rootObservedName: row.ObservedName, probe: t4013.ObserveProcessTreeRecords, nameClasses: map[string]int{}, observation: ProcessObservation{MeasurementKind: "sampled_observation", NativeHistory: "not_established", SimultaneousBounds: "not_established", Classes: make([]ProcessObservationClass, len(classes))}}
	for i, class := range classes {
		gauge.observation.Classes[i].Class = class
	}
	gauge.classify = func(checkCtx context.Context, observed t4013.NativeProcessRecord) (int, bool) {
		class, ok := linuxEpochClassifyRow(checkCtx, observed, images)
		if !ok {
			return 0, false
		}
		index, present := slices.BinarySearch(classes, class)
		return index, present
	}
	if _, ok := gauge.classify(ctx, row); !ok {
		return nil, "", ErrExecutionEpochOne
	}
	return gauge, row.ObservedName, nil
}
func linuxEpochClassifyRow(ctx context.Context, row t4013.NativeProcessRecord, images []linuxEpochClassImage) (string, bool) {
	if ctx == nil || ctx.Err() != nil || row.PID <= 0 {
		return "", false
	}
	pidfd, err := unix.PidfdOpen(row.PID, 0)
	if err != nil {
		return "", false
	}
	defer func() { _ = unix.Close(pidfd) }()
	live := func() bool {
		p := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
		n, e := unix.Poll(p, 0)
		return e == nil && n == 0 && p[0].Revents == 0
	}
	before, err := linuxEpochProcessRow(row.PID)
	if err != nil || !linuxEpochSameProcessRow(row, before) || !live() {
		return "", false
	}
	actual, err := os.Open("/proc/" + strconv.Itoa(row.PID) + "/exe")
	if err != nil {
		return "", false
	}
	defer func() { _ = actual.Close() }()
	info, err := actual.Stat()
	if err != nil {
		return "", false
	}
	class := ""
	for _, image := range images {
		if image.file == nil {
			return "", false
		}
		expected, e := image.file.Stat()
		if e != nil {
			return "", false
		}
		if os.SameFile(info, expected) {
			if class != "" && class != image.class {
				return "", false
			}
			class = image.class
		}
	}
	after, err := linuxEpochProcessRow(row.PID)
	if err != nil || class == "" || !linuxEpochSameProcessRow(row, after) || !live() || ctx.Err() != nil {
		return "", false
	}
	return class, true
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
