//go:build darwin

package t421

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestExecutionInnerOutputOwnsBlockingPipe(t *testing.T) {
	reader, writer := executionLauncherOutputPipe(t, false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if output, err := prepareExecutionInnerOutput(ctx, nil, writer); err == nil || output != nil {
		t.Fatal("missing adopted parent accepted")
	}
	// This fixture models the already-adopted private child-to-parent pipe.
	output, err := prepareExecutionInnerOutput(ctx, &executionParentLiveness{alive: ctx}, writer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.file.Close() }()
	output.used = true
	raw := []byte("modeled authenticated package")
	if err := emitExecutionReturnedPackage(ctx, output, raw, returnedTransportTestBinding(raw)); err != nil {
		t.Fatal(err)
	}
	frame, _ := frameExecutionReturnedPackage(raw, returnedTransportTestBinding(raw))
	got := make([]byte, len(frame))
	if _, err := io.ReadFull(reader, got); err != nil || !bytes.Equal(got, frame) {
		t.Fatal("private output differs", err)
	}
}
