//go:build linux

package t421

import (
	"os"
	"path/filepath"
	"time"
)

func newLinuxPressureAuthorExecutorPrework(builds *LinuxExecutionGoBuildCustody, tool *LinuxExecutionToolCustody, value linuxPressureAuthorSelection) *linuxExecutorPrework {
	owner := &linuxExecutorPrework{builds: builds, tool: tool, selection: value.Borrow.Broker, result: make(chan struct{}), joined: make(chan struct{}), release: make(chan struct{}), observation: &executionPreworkObservation{}, pressure: &linuxPressureExecutorPrework{value: value.Borrow, author: &value, hold: make(chan struct{})}}
	owner.self = owner
	if tool != nil {
		owner.claim = tool.executorClaim
		owner.claim.bindAuthorOuter(owner)
	}
	return owner
}
func validLinuxPressureAuthorSelection(value linuxPressureAuthorSelection, entered time.Time) bool {
	if value.Schema != executionLinuxPressureAuthorSchema || !validLinuxPressureBrokerSelection(value.Borrow, entered) || !linuxPressureAuthorOriginalPaths(value.Borrow.Broker) || !linuxPressureAuthorOriginalPaths(value.Borrow.Target) || !executionGitAbsolutePath(value.AuthorBinary) {
		return false
	}
	path, err := filepath.EvalSymlinks(value.AuthorBinary)
	info, statErr := os.Lstat(value.AuthorBinary)
	return err == nil && path == value.AuthorBinary && statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
