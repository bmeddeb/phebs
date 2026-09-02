//go:build darwin || linux

package t4013

// PrivateProcessSessionMembers returns the exact live non-zombie membership
// of a private process session.
func PrivateProcessSessionMembers(sessionID int) (int, error) {
	pids, err := privateServerSessionPIDs(sessionID)
	return len(pids), err
}
