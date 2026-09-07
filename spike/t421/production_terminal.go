package t421

import "os"

// executionProcessDeath contains native process/session facts only. Neither
// joined output pumps nor a SIGKILL ExitError establish lossless output, SDK
// health, accounting closure, a consumed PC ACK or a successful phase.
type executionProcessDeath struct {
	RootJoined   bool
	SessionEmpty bool
	ProcessState *os.ProcessState
	WaitErr      error // Private diagnostic from the caller's sole Handle.Wait.
}
