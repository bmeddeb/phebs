//go:build darwin

package t4013

import (
	"os"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinSessionInventoryUsesNativeRecords(t *testing.T) {
	pids, err := darwinAllProcessPIDs()
	if err != nil || !slices.Contains(pids, os.Getpid()) {
		t.Fatalf("native host PIDs = %v, %v", pids, err)
	}
	status, present, err := darwinProcessStatus(os.Getpid())
	if err != nil || !present || status == darwinProcessZombie {
		t.Fatalf("native process status = %d, present = %t, %v", status, present, err)
	}
	sessionID, err := unix.Getsid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	session, err := privateServerSessionPIDs(sessionID)
	if err != nil || !slices.Contains(session, os.Getpid()) {
		t.Fatalf("native session PIDs = %v, %v", session, err)
	}
}
