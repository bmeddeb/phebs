//go:build !darwin && !linux

package t4013

import "errors"

func PrivateProcessSessionMembers(int) (int, error) {
	return 0, errors.New("private process sessions require Linux or macOS")
}
