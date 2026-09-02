package t4013

import (
	"context"
	"errors"
)

// ProcessTreeObservation is one bounded native snapshot of a process and all
// descendants. It is shared by source-free gates that need live aggregate RSS
// or an exact post-teardown descendant count without launching a helper.
type ProcessTreeObservation struct {
	RSSBytes    int64
	Descendants int
}

// ObserveProcessTree uses the same bounded native inventory as the T40.13
// ceremony sampler. Platforms without a native probe fail closed.
func ObserveProcessTree(
	ctx context.Context,
	rootPID int,
) (ProcessTreeObservation, error) {
	probe := nativeProcessSnapshotProbe()
	if ctx == nil || rootPID <= 0 || probe == nil {
		return ProcessTreeObservation{}, errors.New("native process-tree observation is unavailable")
	}
	pids, processes, err := probe(ctx, rootPID)
	if err != nil {
		return ProcessTreeObservation{}, err
	}
	if len(pids) == 0 || len(pids) > maxProcessDescendants+1 || processes[rootPID].rssBytes <= 0 {
		return ProcessTreeObservation{}, errors.New("native process-tree observation is incomplete")
	}
	var total int64
	for _, pid := range pids {
		process, present := processes[pid]
		if !present || process.rssBytes <= 0 {
			return ProcessTreeObservation{}, errors.New("native process-tree record is incomplete")
		}
		total, err = checkedAddInt64(total, process.rssBytes)
		if err != nil {
			return ProcessTreeObservation{}, err
		}
	}
	return ProcessTreeObservation{RSSBytes: total, Descendants: len(pids) - 1}, nil
}
