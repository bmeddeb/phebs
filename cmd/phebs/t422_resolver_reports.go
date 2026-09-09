package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// RM counts successful resolver materialization blob returns and actual returned
// bytes before declared-size validation, not attempts or unique input objects.
func t422ResolverBinding(state dispatchadmission.ProductionSemanticSnapshot) ([]byte, error) {
	raw, err := t422SourceBinding(state)
	if err != nil {
		return nil, err
	}
	raw[0], raw[1] = 'R', 'M'
	return raw, nil
}

func t422ResolverRecord(state, initial dispatchadmission.ProductionSemanticSnapshot, size uint64) ([25]byte, error) {
	prefix, err := t422SourceRecord(state, initial)
	if err != nil {
		return [25]byte{}, err
	}
	var raw [25]byte
	copy(raw[:7], prefix[:7])
	raw[0], raw[1], raw[7], raw[24] = 'R', 'M', ':', '\n'
	for index := 23; index >= 8; index-- {
		raw[index] = "0123456789abcdef"[size&15]
		size >>= 4
	}
	return raw, nil
}

func bindT422ResolverReports(ctx context.Context, initial dispatchadmission.ProductionSemanticSnapshot, fail func(error)) (context.Context, error) {
	binding, err := t422ResolverBinding(initial)
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
	return readaccounting.WithResolverBlobObserver(ctx, func(size uint64) error {
		current, err := dispatchadmission.ProductionSemanticState()
		if err == nil {
			var record [25]byte
			record, err = t422ResolverRecord(current, initial, size)
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
