//go:build darwin

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// These are native pressure-volume observations. Teardown ends its disk scope
// immediately before descriptor closure and non-forced detach.
type executionDiskObservation struct {
	Available, Total uint64
	Samples          uint64
	Unavailable      bool
}

type executionWholeResourceEvidence struct {
	Native [15]ProcessObservation
	Disk   [15]executionDiskObservation
	Joined bool
}

// One existing bounded process gauge covers the admitted inner root, including
// author, server, offline and cleanup descendants. Per-server gauges still bind
// runtime health; their overlapping RSS samples are never added to this scope.
// Disk sampling uses the existing volume descriptor only at owned checkpoints;
// no descriptor is duplicated across detach and no new filesystem walk occurs.
type executionWholeResources struct {
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	process    *epochProcessObservation
	volume     *executionPressureVolume
	phase      uint32
	disk       [15]executionDiskObservation
	diskClosed bool
}

func startExecutionWholeResources(ctx context.Context, executePath string) (*executionWholeResources, error) {
	ctx, cancel := context.WithCancel(ctx)
	r := &executionWholeResources{ctx: ctx, cancel: cancel, phase: 1}
	names := make(map[string]string)
	for _, role := range []string{"t422-execute", "t422-author", "phebs", "git", "surreal", "zoekt-git-index", "buf", "phebs-focused-index", "go", "ssh-keygen", "hdiutil", "sh"} {
		name := role
		if len(name) > 16 {
			name = name[:16]
		}
		names[name] = role
	}
	for _, name := range executionGitImageNames() {
		if len(name) > 16 {
			name = name[:16]
		}
		names[name] = "git"
	}
	names["bash"] = "sh"
	rootName := filepath.Base(executePath)
	if len(rootName) > 16 {
		rootName = rootName[:16]
	}
	if !validExecutionLauncherPath(executePath) || !validObservedProcessName(rootName) || names[rootName] != "" && names[rootName] != "t422-execute" {
		cancel()
		return r, ErrExecutionEpochOne
	}
	names[rootName] = "t422-execute"
	meter, err := newExecutionProcessPhaseObservation(ctx, os.Getpid(), 1, rootName, names, cancel, t4013.ObserveProcessTreeRecords)
	r.process = meter
	if err != nil {
		cancel()
	}
	return r, err
}

// Advancing to cleanup preserves sticky failures while still allowing owned
// teardown. An unavailable phase cannot prevent the cleanup operation itself.
func (r *executionWholeResources) begin(phase string) error {
	if r == nil {
		return nil
	}
	next := uint32(slices.Index(frozenPhaseOrder(), phase) + 1)
	meter := r.process
	if meter == nil || next == 0 {
		return ErrExecutionEpochOne
	}
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if !meter.phaseFinished || next != meter.phase+1 && (next != 15 || meter.phase >= 15) {
		return ErrExecutionEpochOne
	}
	meter.phase = next
	meter.phaseFinished = false
	meter.gauge.mu.Lock()
	prior := meter.gauge.observation
	if meter.result.RSSLimitExceeded && prior.FailureClass == "" {
		prior.FailureClass = "measurement_unavailable"
	}
	meter.gauge.observation = ProcessObservation{
		MeasurementKind: prior.MeasurementKind, NativeHistory: prior.NativeHistory,
		SimultaneousBounds: prior.SimultaneousBounds, FailureClass: prior.FailureClass,
		Classes: make([]ProcessObservationClass, len(prior.Classes)),
	}
	for i, class := range prior.Classes {
		meter.gauge.observation.Classes[i].Class = class.Class
	}
	meter.gauge.mu.Unlock()
	err := meter.sampleLocked(context.Background(), "whole-advance")
	r.mu.Lock()
	r.phase = next
	r.mu.Unlock()
	if err != nil {
		r.cancel()
	}
	return nil
}

// Freeze the actual native endpoint before its event row can be called passed.
// The ticker skips a finished scope until the next phase begins.
func (r *executionWholeResources) finish() error {
	if r == nil {
		return nil
	}
	if r.process == nil {
		return ErrExecutionEpochOne
	}
	meter := r.process
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if meter.phaseFinished {
		return ErrExecutionEpochOne
	}
	err := meter.sampleLocked(context.Background(), "whole-finish")
	err = errors.Join(err, meter.saveLocked())
	meter.phaseFinished = true
	if err != nil {
		r.cancel()
	}
	return err
}

// Called with the volume mutex held, including the last pre-detach checkpoint.
func (r *executionWholeResources) sampleDiskLocked(volume *executionPressureVolume, final bool) error {
	if volume == nil {
		return ErrExecutionEpochOne
	}
	return r.sampleDiskRoot(&volume.workspace, final)
}

// The admitted pre-author boundary holds flow.mu and the same borrowed root;
// all later checkpoints hold volume.mu instead. Neither retains another FD.
func (r *executionWholeResources) sampleDiskRoot(root *productionRoot, final bool) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.diskClosed {
		return nil
	}
	if r.phase < 1 || r.phase > 15 {
		return ErrExecutionEpochOne
	}
	row := &r.disk[r.phase-1]
	if row.Unavailable {
		return ErrExecutionEpochOne
	}
	var stat unix.Statfs_t
	if root == nil || root.file == nil ||
		unix.Fstatfs(int(root.file.Fd()), &stat) != nil ||
		stat.Fsid.Val != root.volume || stat.Blocks != (96<<30)/4096 || stat.Bsize != 4096 || stat.Bavail > stat.Blocks {
		row.Unavailable = true
		r.cancel()
		return ErrExecutionEpochOne
	}
	available := stat.Bavail * uint64(stat.Bsize)
	if row.Samples == 0 || available < row.Available {
		row.Available = available
	}
	row.Total = stat.Blocks * uint64(stat.Bsize)
	row.Samples++
	r.diskClosed = final
	return nil
}

func (flow *ExecutionEpochOne) sampleExecutionDisk(final bool) error {
	flow.mu.Lock()
	r := flow.executionWholeResources
	flow.mu.Unlock()
	if r == nil {
		return nil
	}
	volume := r.volume
	if volume == nil {
		return ErrExecutionEpochOne
	}
	volume.mu.Lock()
	defer volume.mu.Unlock()
	return r.sampleDiskLocked(volume, final)
}

func (r *executionWholeResources) close() (executionWholeResourceEvidence, error) {
	var out executionWholeResourceEvidence
	if r == nil || r.process == nil {
		return out, ErrExecutionEpochOne
	}
	value, err := r.process.close()
	r.cancel()
	out.Joined = value.Joined
	for _, row := range value.Phases {
		if row.Phase < 1 || row.Phase > 15 {
			return out, ErrExecutionEpochOne
		}
		out.Native[row.Phase-1] = row.Observation
	}
	r.mu.Lock()
	out.Disk = r.disk
	r.mu.Unlock()
	for _, row := range out.Disk {
		if row.Unavailable {
			err = errors.Join(err, ErrExecutionEpochOne)
		}
	}
	return out, err
}
