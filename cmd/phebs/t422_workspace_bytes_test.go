package main

import (
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"testing"
)

func TestT422WorkspaceByteMissingIsNotZero(t *testing.T) {
	// Ordinary/no-collector construction remains inert; no fake native values.
	control := &t422LifecycleControl{}
	if err := control.bindWorkspaceBytes(nil); err != nil || !control.workspaceByteSnapshot().Unavailable {
		t.Fatal(err)
	}
	control.collector = &lifecycle.CycleCollector{}
	if err := control.bindWorkspaceBytes(nil); err != nil || !control.workspaceByteSnapshot().Unavailable {
		t.Fatal(err)
	}
}
