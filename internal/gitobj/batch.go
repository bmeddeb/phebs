package gitobj

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/store"
)

const maxBatchHeaderBytes = 256

// BatchBlobReader keeps one hardened git cat-file child for a bounded caller
// lease. Calls are serialized, exact-OID-only, and exact-size-fenced; Close
// ends the child at the same partition boundary that releases the repository
// lock. A malformed response poisons and terminates the session rather than
// risking protocol desynchronization.
type BatchBlobReader struct {
	ctx    context.Context
	cmd    *exec.Cmd
	handle dispatchadmission.PipedHandle
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *StderrBuffer

	mu     sync.Mutex
	closed bool
}

func NewBatchBlobReader(ctx context.Context, dir string) (*BatchBlobReader, error) {
	if ctx == nil || dir == "" {
		return nil, errors.New("batch blob reader configuration is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := Command(ctx, dir, "cat-file", "--batch")
	var pipes dispatchadmission.CommandPipes
	stdin, err := pipes.StdinPipe(cmd)
	if err != nil {
		return nil, err
	}
	stdout, err := pipes.StdoutPipe(cmd)
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr := &StderrBuffer{}
	cmd.Stderr = stderr
	handle, err := dispatchadmission.StartPipedProduction(ctx, dispatchadmission.SiteGitBlobBatch, cmd, &pipes)
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	return &BatchBlobReader{
		ctx: ctx, cmd: cmd, handle: handle, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr,
	}, nil
}

func (reader *BatchBlobReader) ReadBlob(
	ctx context.Context,
	oid string,
	expectedBytes int64,
) ([]byte, error) {
	if reader == nil || ctx == nil || !IsObjectID(oid) ||
		expectedBytes < 0 || expectedBytes > math.MaxInt {
		return nil, errors.New("batch blob read is invalid")
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return nil, errors.New("batch blob reader is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := reader.ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(reader.stdin, oid+"\n"); err != nil {
		return nil, reader.failLocked(err)
	}
	header, err := reader.stdout.ReadSlice('\n')
	if err != nil {
		return nil, reader.failLocked(err)
	}
	if len(header) < 2 || len(header) > maxBatchHeaderBytes || header[len(header)-1] != '\n' {
		return nil, reader.failLocked(errors.New("git batch returned an invalid header"))
	}
	fields := strings.Fields(string(header[:len(header)-1]))
	if len(fields) == 2 && fields[0] == oid && fields[1] == "missing" {
		return nil, fmt.Errorf("selected immutable object is missing: %w", store.ErrNotFound)
	}
	if len(fields) != 3 || fields[0] != oid || fields[1] != "blob" {
		return nil, reader.failLocked(errors.New("git batch returned a mismatched object header"))
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return nil, reader.failLocked(errors.New("git batch returned an invalid object size"))
	}
	if size != expectedBytes {
		cause := errors.New("selected immutable object size changed")
		if size > expectedBytes {
			cause = fmt.Errorf("selected immutable object exceeds %d-byte limit: %w", expectedBytes, ErrTooLarge)
		}
		return nil, reader.failLocked(cause)
	}
	content := make([]byte, int(size))
	if _, err := io.ReadFull(reader.stdout, content); err != nil {
		return nil, reader.failLocked(err)
	}
	terminator, err := reader.stdout.ReadByte()
	if err != nil || terminator != '\n' {
		return nil, reader.failLocked(errors.Join(err, errors.New("git batch object terminator is invalid")))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return content, nil
}

func (reader *BatchBlobReader) Close() error {
	if reader == nil {
		return nil
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return nil
	}
	reader.closed = true
	closeErr := reader.stdin.Close()
	waitErr := reader.handle.Wait()
	if waitErr != nil {
		waitErr = WrapError(reader.ctx, []string{"cat-file", "--batch"}, waitErr, reader.stderr.String())
	}
	return errors.Join(closeErr, waitErr)
}

func (reader *BatchBlobReader) failLocked(cause error) error {
	if reader.closed {
		return cause
	}
	reader.closed = true
	_ = reader.stdin.Close()
	if reader.cmd.Process != nil {
		_ = reader.cmd.Process.Kill()
	}
	_ = reader.handle.Wait()
	return cause
}
