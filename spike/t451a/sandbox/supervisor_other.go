//go:build !linux

package sandbox

func Supervisor() int                  { return 125 }
func SupervisorNativeT451b() int       { return 125 }
func ValidateWorker() error            { return ErrRefused }
func ValidateNativeT451bWorker() error { return ErrRefused }
