package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/indexer"
)

func t422IndexRecord(state, initial dispatchadmission.ProductionSemanticSnapshot, event indexer.IndexOfferEvent) ([]byte, error) {
	if _, err := t422SourceRecord(state, initial); err != nil {
		return nil, err
	}
	phase := "0123456789ABCDEF"[state.Phase]
	switch event.Kind {
	case 'b':
		if event.Count == 0 {
			return []byte{'I', 'b', phase, '\n'}, nil
		}
	case 'i':
		if event.Count == 1 {
			return []byte{'I', phase, '\n'}, nil
		}
	case 'e', 'f':
		return []byte(fmt.Sprintf("I%c%c:%d\n", event.Kind, phase, event.Count)), nil
	}
	return nil, errT422AttemptReport
}

func bindT422IndexReports(ctx context.Context, fail func(error)) (context.Context, error) {
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
	binding, err := t422SourceBinding(state)
	if err != nil {
		return nil, err
	}
	binding = bytes.Replace(binding, []byte("SRB1:"), []byte("IXB1:"), 1)
	write := func(raw []byte) error {
		n, err := writer.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		if err != nil {
			fail(errT422AttemptReport)
			return errT422AttemptReport
		}
		return nil
	}
	if err := write(binding); err != nil {
		return nil, err
	}
	return indexer.WithIndexOfferObserver(ctx, func(event indexer.IndexOfferEvent) error {
		current, err := dispatchadmission.ProductionSemanticState()
		if err == nil {
			var raw []byte
			raw, err = t422IndexRecord(current, state, event)
			if err == nil {
				err = write(raw)
			}
		}
		if err != nil {
			fail(errT422AttemptReport)
			return errT422AttemptReport
		}
		return nil
	})
}
