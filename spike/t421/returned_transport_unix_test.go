//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestExecutionReturnedPackageOutput(t *testing.T) {
	for _, mode := range []string{"complete", "canceled", "full_pipe", "no_handoff", "wrong_package"} {
		t.Run(mode, func(t *testing.T) {
			reader, writer := executionLauncherOutputPipe(t, true)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			output, err := prepareExecutionAuthorizationOutput(ctx, writer)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = output.file.Close() }()
			output.used = mode != "no_handoff" // Explicitly modeled prior handoff.
			raw := []byte("modeled signed package")
			binding := returnedTransportTestBinding(raw)
			if mode == "wrong_package" {
				binding.packageSHA256 = SHA256(nil)
			}
			if mode == "canceled" {
				cancel()
			}
			if mode == "full_pipe" {
				executionLauncherFillOutputPipe(t, writer)
			}
			done := make(chan error, 1)
			go func() { done <- emitExecutionReturnedPackage(ctx, output, raw, binding) }()
			if mode == "full_pipe" {
				select {
				case err := <-done:
					t.Fatal("full output did not wait", err)
				case <-time.After(20 * time.Millisecond):
				}
				cancel()
			}
			select {
			case err = <-done:
			case <-time.After(time.Second):
				_ = output.file.Close()
				<-done
				t.Fatal("package output escaped cancellation")
			}
			if (err == nil) != (mode == "complete") {
				t.Fatal("unexpected output result", err)
			}
			if mode == "complete" {
				want, _ := frameExecutionReturnedPackage(raw, binding)
				got := make([]byte, len(want))
				if _, err := io.ReadFull(reader, got); err != nil || !bytes.Equal(got, want) {
					t.Fatal("output bytes differ", err)
				}
			}
			if err := emitExecutionReturnedPackage(ctx, output, raw, binding); err == nil {
				t.Fatal("package replay accepted")
			}
		})
	}
}
