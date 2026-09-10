//go:build !darwin

package custodybytes

import "context"

func walkCustodyBytes(context.Context, borrowedRoot) (Sample, error) { return Sample{}, ErrUnavailable }
