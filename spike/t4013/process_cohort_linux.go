//go:build linux

package t4013

import (
	"context"
	"errors"
)

// ObserveLinuxProcessSessionCohortRecords observes the current root tree and
// every candidate in one selected current session using one bounded host
// census. ParentPID and RSS remain native observations. The returned candidate
// IDs grant no custody, signal, retirement or zero-RSS authority: the caller
// must bind every candidate to its original source-owned creation and pidfd.
func ObserveLinuxProcessSessionCohortRecords(ctx context.Context, rootPID, sessionID int) ([]NativeProcessRecord, []int, error) {
	if ctx == nil || rootPID <= 0 || sessionID <= 0 {
		return nil, nil, errors.New("linux process cohort scope is invalid")
	}
	pids, processes, candidates, err := linuxProcessSnapshotWithSessionAt(ctx, "/proc", rootPID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := nativeProcessCohortRecords(rootPID, pids, processes, candidates)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return rows, candidates, err
}

func nativeProcessCohortRecords(rootPID int, pids []int, processes map[int]processSnapshot, candidates []int) ([]NativeProcessRecord, error) {
	if len(pids) == 0 || len(pids) > MaxNativeProcessRecords || pids[0] != rootPID || len(processes) != len(pids) || len(candidates) > MaxNativeProcessRecords {
		return nil, errors.New("linux process cohort is incomplete")
	}
	cohort := make(map[int]bool, len(candidates))
	for _, pid := range candidates {
		if pid <= 0 || cohort[pid] {
			return nil, errors.New("linux process cohort candidate is invalid")
		}
		cohort[pid] = true
	}
	seen := make(map[int]bool, len(pids))
	rows := make([]NativeProcessRecord, 0, len(pids))
	for index, pid := range pids {
		p, ok := processes[pid]
		if pid <= 0 || seen[pid] || !ok || !p.coherent || p.parent < 0 || p.parent == pid || p.rssBytes < 0 || p.identityToken == "" || len(p.identityToken) > 64 || p.name == "" || len(p.name) > 16 || index == 0 && p.rssBytes == 0 || index != 0 && !seen[p.parent] && !cohort[pid] {
			return nil, errors.New("linux process cohort row is incomplete")
		}
		seen[pid] = true
		rows = append(rows, NativeProcessRecord{PID: pid, ParentPID: p.parent, RSSBytes: p.rssBytes, StartIdentity: p.identityToken, ObservedName: p.name})
	}
	for pid := range cohort {
		if !seen[pid] {
			return nil, errors.New("linux process cohort candidate is missing")
		}
	}
	return rows, nil
}
