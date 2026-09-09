package main

import (
	"bytes"
	"io"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Only the selected lifecycle constructor uses this bound native pipe. Keep
// ordinary/T40 report sinks unchanged, and bind even producers with no turns.
func newT422LifecycleSink(initial dispatchadmission.ProductionSemanticSnapshot, fail func(error)) (func([]byte) error, error) {
	binding, err := t422SourceBinding(initial)
	writer, ok := log.Writer().(*os.File)
	if err != nil || fail == nil || !ok || writer != os.Stderr {
		return nil, errT422LifecycleControl
	}
	info, err := writer.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errT422LifecycleControl
	}
	write := func(raw []byte) error {
		n, err := writer.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		if err != nil {
			fail(errT422LifecycleControl)
			return errT422LifecycleControl
		}
		return nil
	}
	if err := write(bytes.Replace(binding, []byte("SRB1:"), []byte("LCB1:"), 1)); err != nil {
		return nil, err
	}
	return func(raw []byte) error {
		if len(raw) == 0 || len(raw) > t422LifecycleEventBytes {
			fail(errT422LifecycleControl)
			return errT422LifecycleControl
		}
		line := make([]byte, 0, len(raw)+5)
		line = append(line, "LC1:"...)
		line = append(line, raw...)
		line = append(line, '\n')
		return write(line)
	}, nil
}
