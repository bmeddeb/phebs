package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// CC uses one mandatory binding and nine-byte records. A load and its result
// admission are separate events; a hit is one existing cache/lease decision.
func t422CacheRecord(current, initial dispatchadmission.ProductionSemanticSnapshot, event readaccounting.CacheEvent, phase uint32) ([9]byte, error) {
	raw, err := t422SourceRecord(current, initial)
	validation := event == readaccounting.CacheRootValidation || event == readaccounting.CacheMemberValidation
	if err != nil || validation && phase != current.Phase || !validation && phase != 0 ||
		!validation && event != readaccounting.CacheHit && event != readaccounting.CacheRootLoad && event != readaccounting.CacheMemberLoad {
		return [9]byte{}, errT422AttemptReport
	}
	return [9]byte{'C', 'C', '1', ':', raw[4], ':', raw[6], byte(event), '\n'}, nil
}

func bindT422CacheReports(ctx context.Context, initial dispatchadmission.ProductionSemanticSnapshot, fail func(error)) (context.Context, error) {
	raw, err := t422SourceBinding(initial)
	writer, ok := log.Writer().(*os.File)
	if ctx == nil || err != nil || fail == nil || !ok || writer != os.Stderr {
		return nil, errT422AttemptReport
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422AttemptReport
	}
	raw[0], raw[1] = 'C', 'C'
	if n, err := writer.Write(raw); err != nil || n != len(raw) {
		fail(errT422AttemptReport)
		return nil, errT422AttemptReport
	}
	return readaccounting.WithCacheObserver(ctx, func(event readaccounting.CacheEvent, phase uint32) (uint32, error) {
		current, err := dispatchadmission.ProductionWorkState()
		if err == nil {
			var record [9]byte
			record, err = t422CacheRecord(current, initial, event, phase)
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
			return 0, errT422AttemptReport
		}
		return current.Phase, nil
	})
}
