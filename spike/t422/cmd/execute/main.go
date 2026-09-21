package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/bmeddeb/phebs/spike/t421"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := t421.RunExecutionCommand(ctx, os.Args, os.Environ()); err != nil {
		writeExecutionCommandFailure(os.Stderr, t421.ExecutionCommandFailureReported(err))
		os.Exit(1)
	}
}

func writeExecutionCommandFailure(stderr io.Writer, alreadyReported bool) {
	if !alreadyReported {
		_, _ = fmt.Fprintln(stderr, "t422-execute: authenticated execution operation unavailable")
	}
}
