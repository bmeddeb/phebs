//go:build !darwin && !linux

package t4013

import "errors"

func processExecutablePaths([]int) ([]string, error) {
	return nil, errors.New("native process executable observation is unavailable")
}
