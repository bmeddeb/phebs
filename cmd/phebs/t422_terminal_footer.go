package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// This footer attests only the source-owned terminal boundary. It does not
// assert pump health, SDK/EOF closure, owned death or metric completeness.
func t422TerminalFooter(initial, current dispatchadmission.ProductionSemanticSnapshot) ([]byte, error) {
	if initial.ProducerID != 4 || initial.Phase != 6 || current.Phase != 8 {
		return nil, errT422AttemptReport
	}
	if _, err := t422SourceRecord(current, initial); err != nil {
		return nil, err
	}
	binding, err := t422SourceBinding(initial)
	if err != nil {
		return nil, err
	}
	return bytes.Replace(binding, []byte("SRB1:4:"), []byte("TFE1:4:8:"), 1), nil
}

func writeT422TerminalFooter(ctx context.Context, writer io.Writer, initial, current dispatchadmission.ProductionSemanticSnapshot) error {
	if ctx == nil || ctx.Err() != nil || writer == nil {
		return errT422AttemptReport
	}
	footer, err := t422TerminalFooter(initial, current)
	if err != nil {
		return err
	}
	n, err := writer.Write(footer)
	if err != nil || n != len(footer) || ctx.Err() != nil {
		return errT422AttemptReport
	}
	return nil
}

// Main registers this only for genuine epoch-three terminal mode. Quiesce is
// one-shot and joins every other owner/request plus the held claim's heartbeat.
// Its parked handler cannot emit another metric. The parent must still reject
// any reserved metric after this footer and retain its own output-pump errors.
func (control *t422CheckpointControl) quiesceAndReport(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return control.stop(errT422AttemptReport)
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return control.stop(errT422AttemptReport)
	}
	operation, finish := control.operationContext(ctx, nil)
	defer finish()
	if err := control.quiesce(operation); err != nil {
		return err
	}
	writer, ok := log.Writer().(*os.File)
	if !ok || writer != os.Stderr {
		return control.stop(errT422AttemptReport)
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return control.stop(errT422AttemptReport)
	}
	current, err := dispatchadmission.ProductionSemanticState()
	if err != nil || !control.current(operation, 8, false, false) {
		return control.stop(errT422AttemptReport)
	}
	if err := writeT422TerminalFooter(operation, writer, control.launch.initial, current); err != nil {
		return control.stop(err)
	}
	control.mu.Lock()
	valid := control.err == nil && control.parked && control.terminal && control.hit.reported && control.hit.observer.Err() == nil
	control.mu.Unlock()
	if !valid || !control.current(operation, 8, false, false) {
		return control.stop(errT422AttemptReport)
	}
	return nil
}
