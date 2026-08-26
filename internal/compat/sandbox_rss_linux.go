//go:build linux

package compat

import "errors"

func processGroupRSS(int) (int64, error) {
	return 0, errors.New("compatibility sandbox process RSS is unavailable on Linux")
}
