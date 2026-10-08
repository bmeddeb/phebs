//go:build !darwin && !linux

package t4013

import "errors"

// PrivateProcessSessionMembership requires coherent native Linux or Darwin
// records, and refuses before any inventory or helper process is started.
func PrivateProcessSessionMembership(int) ([]NativeProcessRecord, error) {
	return nil, errors.New("native session-member observation requires Linux or macOS")
}
