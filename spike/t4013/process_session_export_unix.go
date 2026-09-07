//go:build darwin || linux

package t4013

import "time"

// PrivateProcessSessionMembers returns the exact live non-zombie membership
// of a private process session.
func PrivateProcessSessionMembers(sessionID int) (int, error) {
	pids, err := privateServerSessionPIDs(sessionID)
	return len(pids), err
}

// WaitPrivateProcessSession observes the exact live non-zombie session until
// the caller's existing deadline. It neither signals nor joins the root.
func WaitPrivateProcessSession(sessionID int, deadline time.Time) error {
	return waitPrivateServerSession(sessionID, deadline)
}

// KillPrivateProcessSession reuses bounded native session enumeration, not a
// process-group assumption. The caller must retain its owned session identity,
// join the root separately, confirm emptiness, and preserve forced-stop failure.
func KillPrivateProcessSession(sessionID int) error {
	return killPrivateServerSession(sessionID)
}
