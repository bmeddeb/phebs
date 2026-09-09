package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// OP uses the same closed producer/input/phase framing as SR, but a distinct
// unit: successful native ParsedBlobs events, never parser or source attempts.
func t422ObservationBinding(state dispatchadmission.ProductionSemanticSnapshot) ([]byte, error) {
	raw, err := t422SourceBinding(state)
	if err != nil {
		return nil, err
	}
	raw[0], raw[1] = 'O', 'P'
	return raw, nil
}

func t422ObservationRecord(state, initial dispatchadmission.ProductionSemanticSnapshot) ([8]byte, error) {
	raw, err := t422SourceRecord(state, initial)
	if err != nil {
		return [8]byte{}, err
	}
	raw[0], raw[1] = 'O', 'P'
	return raw, nil
}

func bindT422ObservationReports(ctx context.Context, fail func(error)) (context.Context, error) {
	if !dispatchadmission.ProductionSemanticSelected() {
		return ctx, nil
	}
	state, err := dispatchadmission.ProductionSemanticState()
	if err != nil || fail == nil {
		return nil, errT422AttemptReport
	}
	writer, ok := log.Writer().(*os.File)
	if !ok || writer != os.Stderr {
		return nil, errT422AttemptReport
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422AttemptReport
	}
	binding, err := t422ObservationBinding(state)
	if err != nil {
		return nil, err
	}
	if n, err := writer.Write(binding); err != nil || n != len(binding) {
		fail(errT422AttemptReport)
		return nil, errT422AttemptReport
	}
	return readaccounting.WithParsedBlobObserver(ctx, func() error {
		current, err := dispatchadmission.ProductionSemanticState()
		if err == nil {
			var record [8]byte
			record, err = t422ObservationRecord(current, state)
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
