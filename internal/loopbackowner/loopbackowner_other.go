//go:build !linux

package loopbackowner

func check(int, string) error { return ErrUnsupported }
