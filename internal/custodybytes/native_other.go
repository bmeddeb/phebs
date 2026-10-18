//go:build !darwin && !linux

package custodybytes

import "context"

func walkCustodyBytes(context.Context, borrowedRoot) (Sample, error) { return Sample{}, ErrUnavailable }
