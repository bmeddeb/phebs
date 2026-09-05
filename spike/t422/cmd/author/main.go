package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t421"
)

func main() {
	if run() != nil {
		_, _ = fmt.Fprintln(os.Stderr, "t422-author: authenticated author operation unavailable")
		os.Exit(1)
	}
}

// This command has no ordinary/unmetered mode, input-path flags or test recipe.
// The owning launcher supplies one authenticated request and both inherited
// dispatch endpoints. It fences/checkpoints after receiving the actual result.
func run() (retErr error) {
	defer func() {
		if recover() != nil {
			retErr = t421.ErrExecutionCorpusAuthor
		}
	}()
	if len(os.Args) != 1 {
		return t421.ErrExecutionCorpusAuthor
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapAuthor(ctx)
	if err != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	defer func() {
		retErr = errors.Join(retErr, lifetime.Close(context.Background()))
	}()
	ctx = dispatchadmission.ProcessContext()
	raw, err := readAuthorRequest(ctx, os.Stdin)
	if err != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	author, err := t421.OpenExecutionCorpusAuthorRequest(ctx, raw)
	if err != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	defer func() { retErr = errors.Join(retErr, author.Close()) }()
	response, err := author.AuthorRequested(ctx)
	if err != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	if writeAuthorResponse(ctx, os.Stdout, response) != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	return dispatchadmission.WaitAuthorCheckpoint(ctx)
}

func readAuthorRequest(ctx context.Context, input *os.File) (_ []byte, retErr error) {
	connection, closeSocket, err := openAuthorSocket(ctx, input)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, closeSocket()) }()
	raw, err := io.ReadAll(io.LimitReader(connection, t421.MaxExecutionCorpusAuthorRequestBytes+1))
	if err != nil || len(raw) > t421.MaxExecutionCorpusAuthorRequestBytes || ctx.Err() != nil {
		return nil, t421.ErrExecutionCorpusAuthor
	}
	return raw, nil
}

func writeAuthorResponse(ctx context.Context, output *os.File, response []byte) (retErr error) {
	if len(response) > t421.MaxExecutionCorpusAuthorResponseBytes {
		return t421.ErrExecutionCorpusAuthor
	}
	connection, closeSocket, err := openAuthorSocket(ctx, output)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, closeSocket()) }()
	if written, err := connection.Write(response); err != nil || written != len(response) || ctx.Err() != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	return nil
}

// Inherited os.Stdin/os.Stdout are blocking files, even when the parent used
// sockets. FileConn supplies the pollable, close-on-exec duplicate needed for
// deadlines. Borrowed process stdio remains open; only this duplicate is owned.
func openAuthorSocket(ctx context.Context, file *os.File) (*net.UnixConn, func() error, error) {
	if ctx == nil || ctx.Err() != nil || file == nil {
		return nil, nil, t421.ErrExecutionCorpusAuthor
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, nil, t421.ErrExecutionCorpusAuthor
	}
	connection, err := net.FileConn(file)
	if err != nil {
		return nil, nil, t421.ErrExecutionCorpusAuthor
	}
	socket, ok := connection.(*net.UnixConn)
	deadline := time.Now().Add(dispatchadmission.ProductionBootstrapTimeout)
	if earlier, exists := ctx.Deadline(); exists && earlier.Before(deadline) {
		deadline = earlier
	}
	if !ok || connection.SetDeadline(deadline) != nil {
		_ = connection.Close()
		return nil, nil, t421.ErrExecutionCorpusAuthor
	}
	unblocked := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = socket.SetDeadline(time.Now())
		close(unblocked)
	})
	return socket, func() error {
		if !stop() {
			<-unblocked
		}
		if socket.Close() != nil {
			return t421.ErrExecutionCorpusAuthor
		}
		return nil
	}, nil
}
