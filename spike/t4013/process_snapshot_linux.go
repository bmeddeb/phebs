//go:build linux

package t4013

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const maxLinuxProcessFileBytes = 4096

var errLinuxProcessNameTransition = errors.New("linux process command changed during resident observation")

type linuxProcessStat struct {
	snapshot processSnapshot
	state    string
}

func readLinuxProcessFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxLinuxProcessFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxLinuxProcessFileBytes {
		return nil, errors.New("linux process record exceeds its bound")
	}
	return raw, nil
}

func parseLinuxProcessStat(pid int, raw []byte) (linuxProcessStat, error) {
	text := string(raw)
	start, end := strings.Index(text, "("), strings.LastIndex(text, ") ")
	if pid <= 0 || len(raw) > maxLinuxProcessFileBytes || start < 1 || end <= start+1 {
		return linuxProcessStat{}, errors.New("linux process stat is invalid")
	}
	fields := strings.Fields(text[end+2:])
	if len(fields) < 22 {
		return linuxProcessStat{}, errors.New("linux process stat is incomplete")
	}
	observedPID, pidErr := strconv.Atoi(strings.TrimSpace(text[:start]))
	parent, parentErr := strconv.Atoi(fields[1])
	token, tokenErr := strconv.ParseUint(fields[19], 10, 64)
	name := text[start+1 : end]
	if pidErr != nil || observedPID != pid || parentErr != nil || parent < 0 || parent == pid ||
		tokenErr != nil || token == 0 || len(name) > 256 || len(fields[0]) != 1 || !strings.Contains("RSDTtXZxKWPI", fields[0]) {
		return linuxProcessStat{}, errors.New("linux process stat identity is invalid")
	}
	return linuxProcessStat{snapshot: processSnapshot{parent: parent, name: name,
		identityToken: strconv.FormatUint(token, 10)}, state: fields[0]}, nil
}

func linuxProcessStatAt(procRoot string, pid int) (linuxProcessStat, error) {
	raw, err := readLinuxProcessFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ESRCH) {
			return linuxProcessStat{}, errors.Join(errProcessIdentityMissing, err)
		}
		return linuxProcessStat{}, fmt.Errorf("read Linux process identity: %w", err)
	}
	return parseLinuxProcessStat(pid, raw)
}

// stat/statm RSS is asynchronous on Linux. Use the kernel page-table rollup,
// bracketed by lifetime, parent and command observations, instead.
func linuxProcessResidentBytes(procRoot string, pid int, before linuxProcessStat) (processSnapshot, error) {
	rss := int64(0)
	if before.state != "Z" && before.state != "X" {
		raw, err := readLinuxProcessFile(filepath.Join(procRoot, strconv.Itoa(pid), "smaps_rollup"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ESRCH) {
				if _, identityErr := linuxProcessStatAt(procRoot, pid); errors.Is(identityErr, errProcessIdentityMissing) {
					return processSnapshot{}, identityErr
				}
			}
			return processSnapshot{}, fmt.Errorf("read Linux resident memory: %w", err)
		}
		rss, err = parseLinuxResidentBytes(raw)
		if err != nil {
			return processSnapshot{}, err
		}
	}
	after, err := linuxProcessStatAt(procRoot, pid)
	if err != nil {
		return processSnapshot{}, err
	}
	if before.snapshot.identityToken != after.snapshot.identityToken || before.snapshot.parent != after.snapshot.parent ||
		before.state != after.state && (after.state == "Z" || after.state == "X") {
		return processSnapshot{}, errors.New("linux process changed during resident memory observation")
	}
	if before.snapshot.name != after.snapshot.name {
		return processSnapshot{}, errLinuxProcessNameTransition
	}
	if len(after.snapshot.name) > 16 {
		return processSnapshot{}, errors.New("linux selected process command exceeds its bound")
	}
	result := after.snapshot
	result.rssBytes, result.coherent = rss, true
	return result, nil
}

// A command transition may invalidate one read without losing its lifetime or
// parent. Retry only that class of refusal, under the caller's same deadline.
// Denial, missing memory, lifetime/parent drift and exhausted retries stay fatal.
func collectLinuxResidentObservation(ctx context.Context, census linuxProcessStat,
	observe func(linuxProcessStat) (processSnapshot, error), refresh func() (linuxProcessStat, error),
) (processSnapshot, error) {
	before := census
	for attempt := 0; attempt < maxProcessSampleAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return processSnapshot{}, err
		}
		row, err := observe(before)
		if !errors.Is(err, errLinuxProcessNameTransition) || attempt == maxProcessSampleAttempts-1 {
			return row, err
		}
		before, err = refresh()
		if err != nil {
			return processSnapshot{}, err
		}
		if before.snapshot.identityToken != census.snapshot.identityToken || before.snapshot.parent != census.snapshot.parent {
			return processSnapshot{}, errors.New("linux process lifetime or parent changed during command transition")
		}
	}
	return processSnapshot{}, errLinuxProcessNameTransition
}

func parseLinuxResidentBytes(raw []byte) (int64, error) {
	if len(raw) == 0 || len(raw) > maxLinuxProcessFileBytes {
		return 0, errors.New("linux resident memory record is invalid")
	}
	found := false
	var rss int64
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Rss:" {
			continue
		}
		if found || len(fields) != 3 || fields[2] != "kB" {
			return 0, errors.New("linux resident memory record is invalid")
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || value < 0 || value > (1<<63-1)/1024 {
			return 0, errors.New("linux resident memory overflow or invalid value")
		}
		rss, found = value*1024, true
	}
	if !found {
		return 0, errors.New("linux resident memory is unavailable")
	}
	return rss, nil
}

func nativeProcessSnapshotProbe() func(context.Context, int) ([]int, map[int]processSnapshot, error) {
	return func(ctx context.Context, root int) ([]int, map[int]processSnapshot, error) {
		return linuxProcessSnapshotAt(ctx, "/proc", root)
	}
}

// One bounded host PID census, followed by root-first traversal. Sequential
// reads do not establish an atomic tree or a complete history of short children.
func linuxProcessSnapshotAt(ctx context.Context, procRoot string, root int) ([]int, map[int]processSnapshot, error) {
	if ctx == nil || root <= 0 {
		return nil, nil, errors.New("linux process snapshot scope is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	dir, err := os.Open(procRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("open Linux process census: %w", err)
	}
	entries, readErr := dir.ReadDir(maxProcessSnapshotRows + 1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
		return nil, nil, errors.Join(readErr, closeErr)
	}
	if len(entries) > maxProcessSnapshotRows {
		return nil, nil, errors.New("linux process census exceeds its bound")
	}
	stats := make(map[int]linuxProcessStat)
	children := make(map[int][]int)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 0 {
			continue
		}
		stat, err := linuxProcessStatAt(procRoot, pid)
		if errors.Is(err, errProcessIdentityMissing) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		stats[pid] = stat
		children[stat.snapshot.parent] = append(children[stat.snapshot.parent], pid)
	}

	queue := []int{root}
	pids := []int{root}
	rows := make(map[int]processSnapshot)
	if _, present := stats[root]; !present {
		return pids, rows, nil
	}
	seen := map[int]bool{root: true}
	for index := 0; index < len(queue); index++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		pid := queue[index]
		observed, err := collectLinuxResidentObservation(ctx, stats[pid],
			func(before linuxProcessStat) (processSnapshot, error) {
				return linuxProcessResidentBytes(procRoot, pid, before)
			},
			func() (linuxProcessStat, error) { return linuxProcessStatAt(procRoot, pid) })
		if errors.Is(err, errProcessIdentityMissing) {
			if pid == root {
				return []int{root}, map[int]processSnapshot{}, nil
			}
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if pid != root {
			pids = append(pids, pid)
		}
		rows[pid] = observed
		sort.Ints(children[pid])
		for _, child := range children[pid] {
			if seen[child] || len(queue) >= MaxNativeProcessRecords {
				return nil, nil, errors.New("linux descendant census is duplicate or exceeds its bound")
			}
			seen[child] = true
			queue = append(queue, child)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return pids, rows, nil
}
