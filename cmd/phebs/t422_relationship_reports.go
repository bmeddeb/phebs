package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// RL records independent actual builder entries and projector invocations,
// including failed/repeated work, never handler calls or final cardinality.
func t422RelationshipBinding(state dispatchadmission.ProductionSemanticSnapshot) ([]byte, error) {
	raw, err := t422SourceBinding(state)
	if err != nil {
		return nil, err
	}
	raw[0], raw[1] = 'R', 'L'
	return raw, nil
}

func t422RelationshipRecord(state, initial dispatchadmission.ProductionSemanticSnapshot, event readaccounting.RelationshipEvent) ([9]byte, error) {
	prefix, err := t422SourceRecord(state, initial)
	if err != nil || event != readaccounting.RelationshipBuild && event != readaccounting.RelationshipProjection {
		return [9]byte{}, errT422AttemptReport
	}
	return [9]byte{'R', 'L', '1', ':', prefix[4], ':', prefix[6], byte(event), '\n'}, nil
}

func t422RelationshipReferenceRecord(state, initial dispatchadmission.ProductionSemanticSnapshot, quantity uint64) ([26]byte, error) {
	// Reuse the existing fixed-width, lowercase uint64 encoding and binding
	// checks. References have a distinct opcode, not resolver-read meaning.
	encoded, err := t422ResolverRecord(state, initial, quantity)
	if err != nil || quantity == 0 {
		return [26]byte{}, errT422AttemptReport
	}
	var raw [26]byte
	copy(raw[:7], encoded[:7])
	raw[1], raw[7], raw[8], raw[25] = 'L', 'R', ':', '\n'
	copy(raw[9:25], encoded[8:24])
	return raw, nil
}

func writeT422RelationshipRecord(writer *os.File, state, initial dispatchadmission.ProductionSemanticSnapshot, event readaccounting.RelationshipEvent, quantity uint64) error {
	var n, want int
	var err error
	if event == readaccounting.RelationshipReferences {
		if quantity == 0 {
			// Validate real observer/phase coverage without inventing an R
			// record. This does not assert contemporaneous pipe-peer liveness.
			_, err = t422SourceRecord(state, initial)
			return err
		}
		var record [26]byte
		record, err = t422RelationshipReferenceRecord(state, initial, quantity)
		if err == nil {
			n, err = writer.Write(record[:])
			want = len(record)
		}
	} else {
		if quantity != 1 {
			return errT422AttemptReport
		}
		var record [9]byte
		record, err = t422RelationshipRecord(state, initial, event)
		if err == nil {
			n, err = writer.Write(record[:])
			want = len(record)
		}
	}
	if err == nil && n != want {
		return io.ErrShortWrite
	}
	return err
}

func bindT422RelationshipReports(ctx context.Context, initial dispatchadmission.ProductionSemanticSnapshot, fail func(error)) (context.Context, error) {
	binding, err := t422RelationshipBinding(initial)
	writer, ok := log.Writer().(*os.File)
	if ctx == nil || err != nil || fail == nil || !ok || writer != os.Stderr {
		return nil, errT422AttemptReport
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422AttemptReport
	}
	// The independent binding is mandatory even when no call is observed.
	if n, err := writer.Write(binding); err != nil || n != len(binding) {
		fail(errT422AttemptReport)
		return nil, errT422AttemptReport
	}
	return readaccounting.WithRelationshipObserver(ctx, func(event readaccounting.RelationshipEvent, quantity uint64) error {
		current, err := dispatchadmission.ProductionWorkState()
		if err == nil {
			err = writeT422RelationshipRecord(writer, current, initial, event, quantity)
		}
		if err != nil {
			fail(errT422AttemptReport)
			return errT422AttemptReport
		}
		return nil
	})
}
