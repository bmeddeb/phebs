//go:build !linux

package sandbox

func Supervisor() int       { return 125 }
func ValidateWorker() error { return ErrRefused }
