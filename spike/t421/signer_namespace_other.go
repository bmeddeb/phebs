//go:build !darwin && !linux

package t421

import (
	"context"
	"os"
)

func observeExecutionSignerNamespace(context.Context, *os.File, string) (executionSignerNamespaceIdentity, error) {
	return executionSignerNamespaceIdentity{}, ErrExecutionEpochOne
}

type executionSignerNamespaceIdentity struct {
	device int64
	inode  uint64
	mode   uint32
}
