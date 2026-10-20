//go:build linux

package main

import (
	"context"
	"github.com/bmeddeb/phebs/spike/t421"
	"golang.org/x/sys/unix"
)

func captureAuthorInputs() (*t421.LinuxExecutionAuthorInputs, error) {
	inputs, err := t421.CaptureLinuxExecutionAuthorInputs()
	if err == nil {
		// This fixed CLI creates private Git controls; never change its controller's mask.
		unix.Umask(0o077)
	}
	return inputs, err
}
func closeAuthorInputs(inputs *t421.LinuxExecutionAuthorInputs) error { return inputs.Close() }
func openAuthorInputs(ctx context.Context, raw []byte, inputs *t421.LinuxExecutionAuthorInputs) (*t421.ExecutionCorpusAuthor, error) {
	return t421.OpenLinuxExecutionCorpusAuthorRequest(ctx, raw, inputs)
}
