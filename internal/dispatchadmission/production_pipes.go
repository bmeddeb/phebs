package dispatchadmission

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
)

// CommandPipes owns at most one stdin and one stdout pipe for one command.
// Unlike exec.Cmd's pipe helpers, ownership exists before Cmd.Start: admission
// can refuse without entering Start's descriptor cleanup. Do not copy or use
// this owner concurrently. Borrowed command stdio is never closed here.
type CommandPipes struct {
	command *exec.Cmd
	stdin   [2]*os.File
	stdout  [2]*os.File
	started bool
	closed  bool
}

func (pipes *CommandPipes) prepare(command *exec.Cmd) error {
	if pipes == nil || pipes.started {
		return ErrConfig
	}
	if pipes.closed || command == nil || command.Process != nil || pipes.command != nil && pipes.command != command {
		return errors.Join(ErrConfig, pipes.Close())
	}
	pipes.command = command
	return nil
}

func (pipes *CommandPipes) StdinPipe(command *exec.Cmd) (io.WriteCloser, error) {
	if err := pipes.prepare(command); err != nil {
		return nil, err
	}
	if command.Stdin != nil {
		return nil, errors.Join(ErrConfig, pipes.Close())
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(ErrTransport, pipes.Close())
	}
	pipes.stdin = [2]*os.File{reader, writer}
	command.Stdin = reader
	return writer, nil
}

func (pipes *CommandPipes) StdoutPipe(command *exec.Cmd) (io.ReadCloser, error) {
	if err := pipes.prepare(command); err != nil {
		return nil, err
	}
	if command.Stdout != nil {
		return nil, errors.Join(ErrConfig, pipes.Close())
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(ErrTransport, pipes.Close())
	}
	pipes.stdout = [2]*os.File{reader, writer}
	command.Stdout = writer
	return reader, nil
}

func closeCommandPipeFiles(files ...*os.File) error {
	var result error
	for _, file := range files {
		if file != nil {
			if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				result = ErrTransport
			}
		}
	}
	return result
}

// Close also handles setup failures before Start. The caller must join a
// successfully started command; closing pipes alone never proves child cleanup.
func (pipes *CommandPipes) Close() error {
	if pipes == nil {
		return nil
	}
	pipes.closed = true
	return closeCommandPipeFiles(pipes.stdin[0], pipes.stdin[1], pipes.stdout[0], pipes.stdout[1])
}

// PipedHandle preserves Handle's exact-once join/settlement contract and then
// releases the pipe owner. Callers finish consuming stdout before Wait.
type PipedHandle struct {
	handle Handle
	pipes  *CommandPipes
}

func (handle *PipedHandle) Wait() error {
	return errors.Join(handle.handle.Wait(), handle.pipes.Close())
}

// StartPipedProduction closes our child-side copies after every Start attempt.
// Refusal and failed Start close both ends, even while command remains retained
// and without GC. Successful handles keep only the parent ends until Wait.
func StartPipedProduction(ctx context.Context, site uint32, command *exec.Cmd, pipes *CommandPipes) (PipedHandle, error) {
	if pipes == nil || pipes.started {
		return PipedHandle{}, ErrConfig
	}
	if pipes.closed || pipes.command != command || command == nil ||
		pipes.stdin[0] == nil && pipes.stdout[1] == nil ||
		pipes.stdin[0] != nil && command.Stdin != pipes.stdin[0] ||
		pipes.stdout[1] != nil && command.Stdout != pipes.stdout[1] {
		return PipedHandle{}, errors.Join(ErrConfig, pipes.Close())
	}
	pipes.started = true
	handle, err := StartProduction(ctx, site, command)
	childErr := closeCommandPipeFiles(pipes.stdin[0], pipes.stdout[1])
	if err != nil {
		return PipedHandle{}, errors.Join(err, childErr, pipes.Close())
	}
	if childErr != nil {
		if handle.client != nil {
			_ = handle.client.fail(ErrTransport)
		}
		_ = command.Process.Kill()
		return PipedHandle{}, errors.Join(childErr, handle.Wait(), pipes.Close())
	}
	return PipedHandle{handle: handle, pipes: pipes}, nil
}
