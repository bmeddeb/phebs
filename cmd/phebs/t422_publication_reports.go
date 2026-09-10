package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// EP counts actual StorePublisher.PublishDomain calls, including exact-current
// recounts and failures. It does not count committed rows or authority movement.
func t422PublicationBinding(state dispatchadmission.ProductionSemanticSnapshot) ([]byte, error) {
	raw, err := t422SourceBinding(state)
	if err != nil {
		return nil, err
	}
	raw[0], raw[1] = 'E', 'P'
	return raw, nil
}

func t422PublicationRecord(state, initial dispatchadmission.ProductionSemanticSnapshot) ([8]byte, error) {
	raw, err := t422SourceRecord(state, initial)
	if err != nil {
		return [8]byte{}, err
	}
	raw[0], raw[1] = 'E', 'P'
	return raw, nil
}

func bindT422PublicationReports(ctx context.Context, initial dispatchadmission.ProductionSemanticSnapshot, fail func(error)) (context.Context, error) {
	binding, err := t422PublicationBinding(initial)
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
	return readaccounting.WithPublicationObserver(ctx, func() error {
		current, err := dispatchadmission.ProductionWorkState()
		if err == nil {
			var record [8]byte
			record, err = t422PublicationRecord(current, initial)
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
