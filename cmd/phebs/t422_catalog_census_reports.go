package main

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// GC carries actual catalog ls-tree children and classified regular records.
// D/E close positive/empty children; N closes an invocation without a child.
func t422CatalogCensusRecord(current, initial dispatchadmission.ProductionSemanticSnapshot, event readaccounting.CatalogCensusEvent, phase uint32, records uint64) ([26]byte, int, error) {
	var record [26]byte
	raw, err := t422SourceRecord(current, initial)
	if err != nil || event != readaccounting.CatalogCensusBegin && event != readaccounting.CatalogCensusRecords && event != readaccounting.CatalogCensusEnd && event != readaccounting.CatalogCensusChild && event != readaccounting.CatalogCensusNoChild ||
		event == readaccounting.CatalogCensusBegin && phase != 0 || event != readaccounting.CatalogCensusBegin && phase != current.Phase ||
		(event == readaccounting.CatalogCensusRecords) != (records > 0) {
		return record, 0, errT422AttemptReport
	}
	copy(record[:], []byte{'G', 'C', '1', ':', raw[4], ':', raw[6], byte(event), '\n'})
	if event != readaccounting.CatalogCensusRecords {
		return record, 9, nil
	}
	record[8], record[25] = ':', '\n'
	const digits = "0123456789abcdef"
	for i := 0; i < 16; i++ {
		record[24-i] = digits[records&15]
		records >>= 4
	}
	return record, len(record), nil
}

func bindT422CatalogCensusReports(ctx context.Context, initial dispatchadmission.ProductionSemanticSnapshot, fail func(error)) (context.Context, error) {
	raw, err := t422SourceBinding(initial)
	writer, ok := log.Writer().(*os.File)
	if ctx == nil || err != nil || fail == nil || !ok || writer != os.Stderr {
		return nil, errT422AttemptReport
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422AttemptReport
	}
	raw[0], raw[1] = 'G', 'C'
	if n, err := writer.Write(raw); err != nil || n != len(raw) {
		fail(errT422AttemptReport)
		return nil, errT422AttemptReport
	}
	return readaccounting.WithCatalogCensusObserver(ctx, func(event readaccounting.CatalogCensusEvent, phase uint32, records uint64) (uint32, error) {
		current, err := dispatchadmission.ProductionWorkState()
		if err == nil {
			var record [26]byte
			var size int
			record, size, err = t422CatalogCensusRecord(current, initial, event, phase, records)
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
