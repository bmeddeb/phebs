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
	return readaccounting.WithRelationshipObserver(ctx, func(event readaccounting.RelationshipEvent) error {
		current, err := dispatchadmission.ProductionSemanticState()
		if err == nil {
			var record [9]byte
			record, err = t422RelationshipRecord(current, initial, event)
			if err == nil {
				var n int
				n, err = writer.Write(record[:])
				if err == nil && n != len(record) {
					err = io.ErrShortWrite
				}
			}
		}
		if err != nil {
			fail(errT422AttemptReport)
			return errT422AttemptReport
		}
		return nil
	})
}
