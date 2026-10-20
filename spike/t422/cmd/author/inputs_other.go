//go:build !linux

package main

import (
	"context"
	"github.com/bmeddeb/phebs/spike/t421"
)

type authorInputs struct{}

func captureAuthorInputs() (*authorInputs, error) { return nil, nil }
func closeAuthorInputs(*authorInputs) error       { return nil }
func openAuthorInputs(ctx context.Context, raw []byte, _ *authorInputs) (*t421.ExecutionCorpusAuthor, error) {
	return t421.OpenExecutionCorpusAuthorRequest(ctx, raw)
}
