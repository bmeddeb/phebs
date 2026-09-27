//go:build !linux

package typedsandbox

func Supervisor() int       { return 125 }
func ValidateWorker() error { return ErrRefused }
