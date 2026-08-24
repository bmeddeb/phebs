//go:build !darwin

package t4013

import "context"

func nativeProcessSnapshotProbe() func(context.Context, int) ([]int, map[int]processSnapshot, error) {
	return nil
}
