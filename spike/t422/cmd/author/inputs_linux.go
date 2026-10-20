//go:build linux

package main

import (
	"context"
	"github.com/bmeddeb/phebs/spike/t421"
)

func captureAuthorInputs() (*t421.LinuxExecutionAuthorInputs, error) {
	return t421.CaptureLinuxExecutionAuthorInputs()
}
func closeAuthorInputs(inputs *t421.LinuxExecutionAuthorInputs) error { return inputs.Close() }
func openAuthorInputs(ctx context.Context, raw []byte, inputs *t421.LinuxExecutionAuthorInputs) (*t421.ExecutionCorpusAuthor, error) {
	return t421.OpenLinuxExecutionCorpusAuthorRequest(ctx, raw, inputs)
}
