//go:build darwin || linux

package t421

import (
	"context"
	"encoding/binary"
)

// emitExecutionReturnedPackage spends its sole output attempt even on failure.
// Package delivery uses the enclosing wall deadline, not the earlier expired
// final-admission deadline. The caller still owns complete outer verification.
func emitExecutionReturnedPackage(ctx context.Context, output *executionAuthorizationOutput, raw []byte, binding ReturnedPackageBinding) error {
	if output == nil || !output.used || output.packageUsed {
		return ErrExecutionLauncher
	}
	output.packageUsed = true
	frame, err := frameExecutionReturnedPackage(raw, binding)
	if err != nil || writeExecutionOutput(ctx, output, frame, output.deadline) != nil {
		return ErrExecutionLauncher
	}
	return nil
}

// Outer publication consumes only the independent verifier's output capability.
func emitExecutionVerifiedReturnedPackage(ctx context.Context, output *executionAuthorizationOutput, value *executionVerifiedReturnedPackage) error {
	if output == nil || !output.used || output.packageUsed || value == nil || value.used ||
		len(value.raw) == 0 || uint64(len(value.raw)) > frozenSealPolicy().MaximumPackageBytes || SHA256(value.raw) != value.digest {
		return ErrExecutionLauncher
	}
	output.packageUsed, value.used = true, true
	frame := make([]byte, len(executionReturnedFrameMagic)+4+len(value.raw))
	copy(frame, executionReturnedFrameMagic)
	binary.BigEndian.PutUint32(frame[len(executionReturnedFrameMagic):], uint32(len(value.raw)))
	copy(frame[len(executionReturnedFrameMagic)+4:], value.raw)
	return writeExecutionOutput(ctx, output, frame, output.deadline)
}
