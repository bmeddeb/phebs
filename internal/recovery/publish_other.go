//go:build !darwin && !linux

package recovery

import "errors"

func publishDirectory(string, string) error {
	return errors.New("exclusive atomic backup publication is unsupported on this platform")
}
