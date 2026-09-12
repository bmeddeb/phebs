package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// SB2 carries classified source-owner bytes, not catalog census reads or whole
// physical passes. B/E close an unsuccessful invocation; B/C closes a successful
// census with its actual regular-owner count, including zero. C replaces E.
func t422CensusRecord(current, initial dispatchadmission.ProductionSemanticSnapshot, event readaccounting.SourceCensusEvent, phase uint32, logical, unique uint64) ([43]byte, int, error) {
	var record [43]byte
	raw, err := t422SourceRecord(current, initial)
	if err != nil || event != readaccounting.SourceCensusBegin && event != readaccounting.SourceCensusBatch && event != readaccounting.SourceCensusEnd && event != readaccounting.SourceCensusComplete ||
		event == readaccounting.SourceCensusBegin && phase != 0 || event != readaccounting.SourceCensusBegin && phase != current.Phase ||
		event == readaccounting.SourceCensusBatch && (logical == 0 || unique > logical) ||
		event == readaccounting.SourceCensusComplete && unique != 0 ||
		event != readaccounting.SourceCensusBatch && event != readaccounting.SourceCensusComplete && (logical != 0 || unique != 0) {
		return record, 0, errT422AttemptReport
	}
	copy(record[:], []byte{'S', 'B', '2', ':', raw[4], ':', raw[6], byte(event), '\n'})
	if event != readaccounting.SourceCensusBatch && event != readaccounting.SourceCensusComplete {
		return record, 9, nil
	}
	record[8], record[25], record[42] = ':', ':', '\n'
	const digits = "0123456789abcdef"
	for i := 0; i < 16; i++ {
		record[24-i], record[41-i] = digits[logical&15], digits[unique&15]
		logical, unique = logical>>4, unique>>4
	}
	if event == readaccounting.SourceCensusComplete {
		record[25] = '\n'
		return record, 26, nil
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
	raw[0], raw[1], raw[3] = 'S', 'B', '2'
	if n, err := writer.Write(raw); err != nil || n != len(raw) {
		fail(errT422AttemptReport)
		return nil, errT422AttemptReport
	}
	return readaccounting.WithSourceCensusObserver(ctx, func(event readaccounting.SourceCensusEvent, phase uint32, logical, unique uint64) (uint32, error) {
		current, err := dispatchadmission.ProductionWorkState()
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
