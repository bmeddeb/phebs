package recovery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// spoolNext captures bytes consumed by this pass's recognizer, not a second
// offset-based source read. The returned file has no name or owned writable
// descriptor. It is private transient transport custody, not an immutable
// archive, durable receipt, or guarantee that later source drift cannot occur.
func (prepared *preparedRestoreReplay) spoolNext(ctx context.Context, directory string) (_ *os.File, unit restoreReplayUnit, resultErr error) {
	if prepared.terminal != nil {
		return nil, restoreReplayUnit{}, prepared.terminal
	}
	defer func() {
		if resultErr != nil && !errors.Is(resultErr, io.EOF) {
			prepared.terminal = resultErr
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, restoreReplayUnit{}, err
	}
	file, err := os.CreateTemp(directory, "unit-")
	if err != nil {
		return nil, restoreReplayUnit{}, fmt.Errorf("create import unit spool: %w", err)
	}
	name := file.Name()
	adopted := false
	defer func() {
		if !adopted {
			_ = file.Close()
			_ = os.Remove(name)
		}
	}()
	start := int64(0)
	if prepared.scanner != nil {
		start = prepared.scanner.offset
	}
	if prepared.spoolWriter == nil {
		prepared.spoolWriter = bufio.NewWriterSize(file, restoreReplayBufferBytes)
	} else {
		prepared.spoolWriter.Reset(file)
	}
	capture := prepared.spoolWriter
	defer capture.Reset(nil)
	unit, err = prepared.nextCaptured(ctx, capture)
	if err != nil {
		return nil, restoreReplayUnit{}, err
	}
	if err := capture.Flush(); err != nil {
		prepared.terminal = err
		return nil, restoreReplayUnit{}, fmt.Errorf("flush import unit spool: %w", err)
	}
	unit.Span.Start -= start
	unit.Span.End -= start
	info, err := file.Stat()
	if err != nil {
		return nil, restoreReplayUnit{}, fmt.Errorf("inspect import unit spool: %w", err)
	}
	if unit.Count < 1 || unit.Count > restoreReplayRecordLimit || unit.Span.Start < 0 ||
		unit.Span.End <= unit.Span.Start || unit.Span.End > info.Size() ||
		(unit.Definition && unit.Count != 1) {
		return nil, restoreReplayUnit{}, errors.New("import unit spool bounds are invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, restoreReplayUnit{}, err
	}
	if err := file.Chmod(0o400); err != nil {
		return nil, restoreReplayUnit{}, fmt.Errorf("protect import unit spool: %w", err)
	}
	readonly, err := openRecoveryRegular(name)
	if err != nil {
		return nil, restoreReplayUnit{}, fmt.Errorf("adopt import unit spool: %w", err)
	}
	defer func() {
		if !adopted {
			_ = readonly.Close()
		}
	}()
	current, err := readonly.Stat()
	if err != nil {
		return nil, restoreReplayUnit{}, fmt.Errorf("inspect adopted import unit spool: %w", err)
	}
	if !os.SameFile(info, current) || current.Size() != info.Size() || current.Mode().Perm() != 0o400 {
		return nil, restoreReplayUnit{}, errors.New("import unit spool identity changed")
	}
	if err := os.Remove(name); err != nil {
		return nil, restoreReplayUnit{}, fmt.Errorf("unlink import unit spool: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, restoreReplayUnit{}, fmt.Errorf("close writable import unit spool: %w", err)
	}
	adopted = true
	return readonly, unit, nil
}
