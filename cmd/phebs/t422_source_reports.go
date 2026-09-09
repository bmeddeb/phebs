package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// SR1:p:h\n is exactly eight bytes (phase is one uppercase hexadecimal digit).
// The one startup binding is mandatory even when this producer reads nothing.
func t422SourceBinding(state dispatchadmission.ProductionSemanticSnapshot) ([]byte, error) {
	if state.Mode != dispatchadmission.ProductionSemanticV3 || state.ProducerID < 2 || state.ProducerID > 6 ||
		state.InputSHA256 == ([32]byte{}) || !t422SemanticEpochPhase(uint64(state.ProducerID-1), state.Phase, true) {
		return nil, errT422AttemptReport
	}
	return []byte(fmt.Sprintf("SRB1:%d:sha256:%s\n", state.ProducerID, hex.EncodeToString(state.InputSHA256[:]))), nil
}

func t422SourceRecord(state, initial dispatchadmission.ProductionSemanticSnapshot) ([8]byte, error) {
	if state.Mode != dispatchadmission.ProductionSemanticV3 || state.Mode != initial.Mode || state.ProducerID != initial.ProducerID || state.InputSHA256 != initial.InputSHA256 ||
		!t422SemanticEpochPhase(uint64(state.ProducerID-1), state.Phase, false) {
		return [8]byte{}, errT422AttemptReport
	}
	return [8]byte{'S', 'R', '1', ':', byte('0' + state.ProducerID), ':', "0123456789ABCDEF"[state.Phase], '\n'}, nil
}

func bindT422SourceReports(ctx context.Context, fail func(error)) (context.Context, error) {
	if !dispatchadmission.ProductionSemanticSelected() {
		return ctx, nil
	}
	state, err := dispatchadmission.ProductionSemanticState()
	if err != nil || fail == nil {
		return nil, errT422AttemptReport
	}
	// Use the same native file object as the standard logger, so its write
	// serialization also covers long concurrent log records. No new FD and
	// no global logger mutation; bypass log.Output's timestamp per blob.
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
	if n, err := writer.Write(binding); err != nil || n != len(binding) {
		fail(errT422AttemptReport)
		return nil, errT422AttemptReport
	}
	ctx, err = bindT422CacheReports(ctx, state, fail)
	if err != nil {
		return nil, err
	}
	ctx, err = bindT422PublicationReports(ctx, state, fail)
	if err != nil {
		return nil, err
	}
	ctx, err = bindT422ResolverReports(ctx, state, fail)
	if err != nil {
		return nil, err
	}
	return readaccounting.WithSourceObserver(ctx, func() error {
		current, err := dispatchadmission.ProductionSemanticState()
		if err == nil {
			var record [8]byte
			record, err = t422SourceRecord(current, state)
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
