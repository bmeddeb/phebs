//go:build linux

package t4013

// ObserveLinuxPrivateProcessSessionPIDs returns candidates from the unchanged
// bounded non-zombie native census. It grants no signaling or join authority.
// Callers must independently pin and bind each lifetime before any action, and
// a fresh empty census is separate from the direct child's actual Wait.
func ObserveLinuxPrivateProcessSessionPIDs(sessionID int) ([]int, error) {
	return privateServerSessionPIDs(sessionID)
}
