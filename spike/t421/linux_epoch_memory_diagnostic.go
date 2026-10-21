//go:build linux

package t421

import (
	"runtime/metrics"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

type linuxEpochInitialMemoryDiagnostic struct {
	ControllerCensusStartedUTC time.Time
	ControllerCensusWallNS     int64
	ControllerRSSAvailable     bool
	ControllerRSSBytes         uint64
	Heap                       linuxEpochHeapSnapshot
}

type linuxEpochHeapSnapshot struct {
	Available         bool
	StartedUTC        time.Time
	WallNS            int64
	HeapObjectsBytes  uint64
	HeapUnusedBytes   uint64
	HeapFreeBytes     uint64
	HeapReleasedBytes uint64
	HeapStackBytes    uint64
}

func (diagnostic *linuxEpochInitialMemoryDiagnostic) recordController(rows []t4013.NativeProcessRecord, pid int, start string) bool {
	if len(rows) != 1 || rows[0].PID != pid || rows[0].StartIdentity != start || rows[0].RSSBytes <= 0 {
		return false
	}
	diagnostic.ControllerRSSAvailable = true
	diagnostic.ControllerRSSBytes = uint64(rows[0].RSSBytes)
	return diagnostic.ControllerRSSBytes <= linuxEpochRSSReservation
}

// Five scalar runtime metrics, once after cleanup: no GC or new native census.
// These Go heap classes are not RSS, unique physical memory or admission inputs.
func readLinuxEpochHeapSnapshot() linuxEpochHeapSnapshot {
	started := time.Now()
	result := linuxEpochHeapSnapshot{StartedUTC: started.UTC()}
	samples := [5]metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
		{Name: "/memory/classes/heap/free:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
	}
	metrics.Read(samples[:])
	result.WallNS = time.Since(started).Nanoseconds()
	for _, sample := range samples {
		if sample.Value.Kind() != metrics.KindUint64 {
			return result
		}
	}
	result.Available = true
	result.HeapObjectsBytes = samples[0].Value.Uint64()
	result.HeapUnusedBytes = samples[1].Value.Uint64()
	result.HeapFreeBytes = samples[2].Value.Uint64()
	result.HeapReleasedBytes = samples[3].Value.Uint64()
	result.HeapStackBytes = samples[4].Value.Uint64()
	return result
}
