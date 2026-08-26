//go:build darwin

package compat

import (
	"os/exec"
	"slices"
	"syscall"
	"testing"
)

func TestProcessGroupRSSObservesLiveGroupWithoutHelperProcess(t *testing.T) {
	command := exec.Command("/bin/sleep", "30")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		killProcessGroup(command.Process.Pid)
		_ = command.Wait()
	}()

	pids, err := darwinProcessGroupPIDs(command.Process.Pid)
	if err != nil || !slices.Contains(pids, command.Process.Pid) {
		t.Fatalf("process group PIDs = %v, %v", pids, err)
	}
	resident, present, err := darwinProcessRSS(command.Process.Pid, command.Process.Pid)
	if err != nil || !present || resident == 0 {
		t.Fatalf("process RSS = %d, present = %t, %v", resident, present, err)
	}
	total, err := processGroupRSS(command.Process.Pid)
	if err != nil || total <= 0 {
		t.Fatalf("process group RSS = %d, %v", total, err)
	}
}
