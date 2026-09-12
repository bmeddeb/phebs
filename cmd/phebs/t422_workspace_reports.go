package main

import (
	"io"
	"log"
	"os"
	"sync"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

// One existing serial lifecycle callback owns each pair. This mutex spans only
// framing/write, never the walk or engine/SDK locks. Counts are derived from the
// fixed operation sequence, not new permission to execute capacity callbacks.
type t422WorkspaceReports struct {
	mu             sync.Mutex
	writer         io.Writer
	initial        dispatchadmission.ProductionSemanticSnapshot
	phase          uint32
	sequence       uint64
	counts         [15]uint64
	archiveMaximum uint64
	pending        bool
	physicalReady  bool
	markerReady    bool
	err            error
}

func t422WorkspaceSampleSlot(producer, phase uint32) (int, uint64) {
	switch {
	case producer == 4 && phase == 7:
		return 12, 3
	case producer == 4 && phase == 8:
		return 13, 2
	case producer == 5 && phase == 8:
		return 14, 1
	case producer == 2 && phase == 4:
		return 9, 3
	case producer == 3 && phase == 5:
		return 10, 1
	case producer == 4 && phase == 6:
		return 11, 2
	case producer == 2 && phase == 2:
		return 7, 1
	case producer == 2 && phase == 3:
		return 8, 2
	case producer == 5 && phase == 9:
		return 0, uint64(lifecycle.MaxCycleObservationTurns) + 1 + 4
	case producer == 5 && phase == 10:
		return 1, 1 + 3
	case producer == 5 && phase == 11:
		return 2, uint64(lifecycle.MaxCycleObservationTurns) + 2 + 4
	case producer == 6 && phase == 13:
		return 3, uint64(lifecycle.MaxCycleObservationTurns) + 2
	case producer == 6 && phase == 12:
		return 5, 1
	case producer == 6 && phase == 14:
		return 6, 2
	default:
		return 0, 0
	}
}

func newT422WorkspaceReports(initial dispatchadmission.ProductionSemanticSnapshot) (*t422WorkspaceReports, error) {
	var archiveMaximum uint32
	if initial.Mode == "" && (initial.ProducerID == 10 || initial.ProducerID == 11) && initial.Phase == 12 {
		var err error
		archiveMaximum, err = dispatchadmission.ProductionArchiveMeasurements()
		if err != nil || archiveMaximum == 0 {
			return nil, errT422LifecycleControl
		}
	} else if initial.ProducerID != 2 && initial.ProducerID != 3 && initial.ProducerID != 4 && initial.ProducerID != 5 && initial.ProducerID != 6 || initial.Mode != dispatchadmission.ProductionSemanticV3 {
		return nil, errT422LifecycleControl
	}
	binding, err := t422SourceBinding(initial)
	writer, ok := log.Writer().(*os.File)
	if err != nil || !ok || writer != os.Stderr {
		return nil, errT422LifecycleControl
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422LifecycleControl
	}
	binding[0], binding[1] = 'W', 'B'
	reports := &t422WorkspaceReports{writer: writer, initial: initial, archiveMaximum: uint64(archiveMaximum)}
	if err := reports.write(binding); err != nil {
		return nil, err
	}
	return reports, nil
}

func (reports *t422WorkspaceReports) write(raw []byte) error {
	if reports.err != nil {
		return reports.err
	}
	n, err := reports.writer.Write(raw)
	if err != nil || n != len(raw) {
		reports.err = errT422LifecycleControl
	}
	return reports.err
}

func t422WorkspaceHex(raw []byte, value uint64) {
	for index := len(raw) - 1; index >= 0; index-- {
		raw[index] = "0123456789abcdef"[value&15]
		value >>= 4
	}
}

func (reports *t422WorkspaceReports) record(kind byte, sample custodybytes.Sample) error {
	var raw [60]byte
	copy(raw[:], "WB1:")
	raw[4], raw[5], raw[6], raw[7], raw[8] = "0123456789ABCDEF"[reports.initial.ProducerID], ':', "0123456789ABCDEF"[reports.phase], kind, ':'
	t422WorkspaceHex(raw[9:25], reports.sequence)
	if kind != 'S' {
		raw[25] = '\n'
		return reports.write(raw[:26])
	}
	raw[25], raw[42], raw[59] = ':', ':', '\n'
	t422WorkspaceHex(raw[26:42], sample.LogicalBytes)
	t422WorkspaceHex(raw[43:59], sample.AllocatedBytes)
	return reports.write(raw[:])
}

func (reports *t422WorkspaceReports) begin(current dispatchadmission.ProductionSemanticSnapshot) error {
	reports.mu.Lock()
	defer reports.mu.Unlock()
	_, identityErr := t422SourceRecord(current, reports.initial)
	slot, maximum := t422WorkspaceSampleSlot(current.ProducerID, current.Phase)
	if current.Mode == "" && (current.ProducerID == 10 || current.ProducerID == 11) && current.Phase == 12 {
		slot, maximum = 4, reports.archiveMaximum
	}
	if reports.err != nil || reports.pending || identityErr != nil || maximum == 0 || current.Phase < reports.phase || reports.counts[slot] >= maximum ||
		current.ProducerID == 2 && current.Phase == 4 && reports.counts[slot] == 2 && !reports.physicalReady ||
		current.ProducerID == 4 && current.Phase == 6 && reports.counts[slot] == 1 && !reports.markerReady {
		reports.err = errT422LifecycleControl
		return reports.err
	}
	reports.phase, reports.pending = current.Phase, true
	reports.counts[slot]++
	// The fixed lifecycle slots or the authenticated archive allowance bound
	// this counter well below overflow; they grant no work permission.
	reports.sequence++
	return reports.record('B', custodybytes.Sample{})
}

// The successful value was already committed by SampleGuarded after its actual
// semantic confirmation. Preserve it even if cancellation follows that commit;
// no semantic/store callback belongs inside these report locks.
func (reports *t422WorkspaceReports) complete(sample custodybytes.Sample) error {
	reports.mu.Lock()
	defer reports.mu.Unlock()
	if reports.err != nil || !reports.pending {
		reports.err = errT422LifecycleControl
		return reports.err
	}
	if err := reports.record('S', sample); err != nil {
		return err
	}
	reports.pending = false
	return nil
}

// A failed traversal contributes no bytes. Saved phase/sequence remain usable
// after cancellation; if the sink is broken, the pending B remains incomplete.
func (reports *t422WorkspaceReports) failed() error {
	reports.mu.Lock()
	defer reports.mu.Unlock()
	if reports.pending && reports.err == nil {
		_ = reports.record('F', custodybytes.Sample{})
	}
	reports.err = errT422LifecycleControl
	return reports.err
}

func (reports *t422WorkspaceReports) physicalReopenReady() error {
	reports.mu.Lock()
	defer reports.mu.Unlock()
	if reports.err != nil || reports.pending || reports.physicalReady ||
		reports.initial.ProducerID != 2 || reports.initial.Mode != dispatchadmission.ProductionSemanticV3 ||
		reports.phase != 4 || reports.sequence != 5 || reports.counts[7] != 1 ||
		reports.counts[8] != 2 || reports.counts[9] != 2 {
		reports.err = errT422LifecycleControl
		return reports.err
	}
	if err := reports.record('R', custodybytes.Sample{}); err != nil {
		return err
	}
	reports.physicalReady = true
	return nil
}

func (reports *t422WorkspaceReports) markerReopenReady() error {
	reports.mu.Lock()
	defer reports.mu.Unlock()
	if reports.err != nil || reports.pending || reports.markerReady || reports.initial.ProducerID != 4 ||
		reports.initial.Mode != dispatchadmission.ProductionSemanticV3 || reports.phase != 6 || reports.sequence != 1 || reports.counts[11] != 1 {
		reports.err = errT422LifecycleControl
		return reports.err
	}
	if err := reports.record('R', custodybytes.Sample{}); err != nil {
		return err
	}
	reports.markerReady = true
	return nil
}
