package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// SB carries classified source-owner byte batches, not catalog census reads or
// physical passes. Begin/End prove observation coverage even for an empty call.
func t422CensusRecord(current, initial dispatchadmission.ProductionSemanticSnapshot, event readaccounting.SourceCensusEvent, phase uint32, logical, unique uint64) ([43]byte, int, error) {
	var record [43]byte
	raw, err := t422SourceRecord(current, initial)
	if err != nil || event != readaccounting.SourceCensusBegin && event != readaccounting.SourceCensusBatch && event != readaccounting.SourceCensusEnd ||
		event == readaccounting.SourceCensusBegin && phase != 0 || event != readaccounting.SourceCensusBegin && phase != current.Phase ||
		unique > logical || event != readaccounting.SourceCensusBatch && (logical != 0 || unique != 0) || event == readaccounting.SourceCensusBatch && logical == 0 {
		return record, 0, errT422AttemptReport
	}
	copy(record[:], []byte{'S', 'B', '1', ':', raw[4], ':', raw[6], byte(event), '\n'})
	if event != readaccounting.SourceCensusBatch {
		return record, 9, nil
	}
	record[8], record[25], record[42] = ':', ':', '\n'
	const digits = "0123456789abcdef"
	for i := 0; i < 16; i++ {
		record[24-i], record[41-i] = digits[logical&15], digits[unique&15]
		logical, unique = logical>>4, unique>>4
	}
	return record, len(record), nil
}

func bindT422CensusReports(ctx context.Context, initial dispatchadmission.ProductionSemanticSnapshot, fail func(error)) (context.Context, error) {
	raw, err := t422SourceBinding(initial)
	writer, ok := log.Writer().(*os.File)
	if ctx == nil || err != nil || fail == nil || !ok || writer != os.Stderr {
		return nil, errT422AttemptReport
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422AttemptReport
	}
	raw[0], raw[1] = 'S', 'B'
	if n, err := writer.Write(raw); err != nil || n != len(raw) {
		fail(errT422AttemptReport)
		return nil, errT422AttemptReport
	}
	return readaccounting.WithSourceCensusObserver(ctx, func(event readaccounting.SourceCensusEvent, phase uint32, logical, unique uint64) (uint32, error) {
		current, err := dispatchadmission.ProductionSemanticState()
		if err == nil {
			var record [43]byte
			var size int
			record, size, err = t422CensusRecord(current, initial, event, phase, logical, unique)
			if err == nil {
				var n int
				n, err = writer.Write(record[:size])
				if err == nil && n != size {
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
