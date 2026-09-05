package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	if os.Stdin.SetReadDeadline(time.Now().Add(dispatchadmission.ProductionBootstrapTimeout)) != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	stop := context.AfterFunc(ctx, func() { _ = os.Stdin.Close() })
	defer stop()
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, t421.MaxExecutionCorpusAuthorRequestBytes+1))
	if err != nil || len(raw) > t421.MaxExecutionCorpusAuthorRequestBytes || ctx.Err() != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	author, err := t421.OpenExecutionCorpusAuthorRequest(ctx, raw)
	if err != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	defer func() { retErr = errors.Join(retErr, author.Close()) }()
	response, err := author.AuthorRequested(ctx)
	if err != nil || len(response) > t421.MaxExecutionCorpusAuthorResponseBytes ||
		os.Stdout.SetWriteDeadline(time.Now().Add(dispatchadmission.ProductionBootstrapTimeout)) != nil {
		return t421.ErrExecutionCorpusAuthor
	}
	if written, err := os.Stdout.Write(response); err != nil || written != len(response) {
		return t421.ErrExecutionCorpusAuthor
	}
	return dispatchadmission.WaitAuthorCheckpoint(ctx)
}
