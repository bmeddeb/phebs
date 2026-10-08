//go:build linux

package t4013

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxSessionInventoryUsesNativeRecords(t *testing.T) {
	pids, err := linuxHostProcessPIDs("/proc")
	if err != nil || !slices.Contains(pids, os.Getpid()) {
		t.Fatalf("native host PIDs = %d entries, %v", len(pids), err)
	}
	defunct, present, err := linuxProcessDefunctStatus(os.Getpid())
	if err != nil || !present || defunct {
		t.Fatalf("native defunct state = %t, present = %t, %v", defunct, present, err)
	}
	sessionID, err := unix.Getsid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	session, err := privateServerSessionPIDs(sessionID)
	if err != nil || !slices.Contains(session, os.Getpid()) {
		t.Fatalf("native session PIDs = %v, %v", session, err)
	}
	// The census launches no helper, so supervision cannot add its own member;
	// every named PID is distinct and positive. The member bound itself is not
	// assertable here because a refusal returns no PIDs at all:
	// TestLinuxSessionMemberBoundRefuses drives both of its sides.
	for index, pid := range session {
		if pid <= 0 || slices.Contains(session[:index], pid) {
			t.Fatalf("native session census is not a distinct positive set: %v", session)
		}
	}
}

// The session member bound refuses instead of truncating. No real single session
// can reach it, so a synthetic census paired with always-matching native hooks
// drives both sides of maxProcessSessionMembers; the same hooks are the ones the
// shared fence takes, so the seam proves the bound without weakening the fence.
func TestLinuxSessionMemberBoundRefuses(t *testing.T) {
	if maxProcessSessionMembers != 8*maxProcessDescendants {
		t.Fatalf("session member bound %d no longer equals eight times maxProcessDescendants %d",
			maxProcessSessionMembers, 8*maxProcessDescendants)
	}
	const sessionID = 4242
	matching := func(int) (int, error) { return sessionID, nil }
	alive := func(int) (bool, bool, error) { return false, true, nil }
	census := func(t *testing.T, entries int) string {
		t.Helper()
		root := t.TempDir()
		for index := 1; index <= entries; index++ {
			if err := os.WriteFile(filepath.Join(root, strconv.Itoa(index)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	t.Run("at bound", func(t *testing.T) {
		pids, err := privateServerSessionPIDsAt(census(t, maxProcessSessionMembers), sessionID, matching, alive)
		if err != nil || len(pids) != maxProcessSessionMembers {
			t.Fatalf("session at its bound = %d entries, %v", len(pids), err)
		}
	})
	t.Run("over bound", func(t *testing.T) {
		pids, err := privateServerSessionPIDsAt(census(t, maxProcessSessionMembers+1), sessionID, matching, alive)
		if err == nil || pids != nil {
			t.Fatalf("session over its bound admitted: %d entries, %v", len(pids), err)
		}
		if err.Error() != "T40.13 private process session exceeds its process bound" {
			t.Fatalf("session bound refusal = %q", err.Error())
		}
	})
}

func TestLinuxSessionInventoryRefusals(t *testing.T) {
	for _, session := range []int{0, -1} {
		if pids, err := privateServerSessionPIDs(session); err == nil || pids != nil {
			t.Fatalf("invalid session %d admitted: %v / %v", session, pids, err)
		}
		if _, present, err := linuxProcessDefunctStatus(session); err == nil || present {
			t.Fatalf("invalid PID %d admitted: present = %t, %v", session, present, err)
		}
		if snapshot, err := linuxProcessObservation(session); err == nil || snapshot != (processSnapshot{}) {
			t.Fatalf("invalid observation PID %d admitted: %+v / %v", session, snapshot, err)
		}
	}
	if _, err := linuxHostProcessPIDs("/proc/does-not-exist"); err == nil {
		t.Fatal("absent process census admitted")
	}
	if _, err := linuxHostProcessPIDs(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("absent private process census admitted")
	}
	if _, err := linuxHostProcessPIDs("/proc/self/stat"); err == nil {
		t.Fatal("non-directory process census admitted")
	}
}

// The census bound lives in readLinuxProcessCensus, which
// TestLinuxSnapshotInventoryCaps/host census already drives through
// linuxProcessSnapshotAt; this test pins only the entry-name filter.
func TestLinuxHostProcessPIDCensusFilter(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"7", "12", "self", "entry-2", "0", "-3", "99999999999999999999"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	pids, err := linuxHostProcessPIDs(root)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(pids)
	if !slices.Equal(pids, []int{7, 12}) {
		t.Fatalf("census filter = %v, %v", pids, err)
	}
}

func TestLinuxProcessObservationNamesSelf(t *testing.T) {
	observed, err := linuxProcessObservation(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !observed.coherent || observed.rssBytes < 0 || observed.identityToken == "" ||
		observed.name == "" || observed.parent < 0 || observed.parent == os.Getpid() {
		t.Fatalf("native self observation is not an individually coherent record: %+v", observed)
	}
	if !validNativeSessionMember(os.Getpid(), observed) {
		t.Fatalf("native self observation is not a valid session member: %+v", observed)
	}
}

func TestLinuxProcessDefunctStatusNamesRealZombie(t *testing.T) {
	const helper = "T4013_LINUX_DEFUNCT_HELPER"
	if os.Getenv(helper) == "1" {
		os.Exit(0)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestLinuxProcessDefunctStatusNamesRealZombie$", "-test.count=1")
	command.Env = append(os.Environ(), helper+"=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// Reaping is deferred so the unreaped child must stay observable as dead.
	defer func() { _ = command.Wait() }()
	deadline := time.Now().Add(60 * time.Second)
	for {
		defunct, present, err := linuxProcessDefunctStatus(command.Process.Pid)
		if err != nil {
			t.Fatalf("native defunct observation refused: %v", err)
		}
		if !present {
			t.Fatalf("unreaped child %d has no native record", command.Process.Pid)
		}
		if defunct {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("child never reached a native dead state")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPrivateProcessSessionMembershipNamesLinuxMembers(t *testing.T) {
	const (
		helper = "T4013_LINUX_SESSION_MEMBERSHIP_HELPER"
		marker = "t4013-linux-session-membership-passed\n"
	)
	if os.Getenv(helper) != "1" {
		// An interactive session may contain a protected login process, not ours.
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrivateProcessSessionMembershipNamesLinuxMembers$", "-test.count=1")
		command.Env = append(os.Environ(), helper+"=1")
		output, err := runCustodyCombinedOutput(command)
		if err != nil {
			t.Fatalf("isolated session membership: %v\n%s", err, output)
		}
		if !bytes.Contains(output, []byte(marker)) {
			t.Fatalf("isolated session membership did not complete:\n%s", output)
		}
		return
	}
	for _, invalid := range []int{0, -1} {
		if members, err := PrivateProcessSessionMembership(invalid); err == nil || members != nil {
			t.Fatalf("invalid session %d was accepted: %v", invalid, members)
		}
	}
	sessionID, err := unix.Getsid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != os.Getpid() {
		t.Fatalf("membership fixture did not own its session: session=%d pid=%d", sessionID, os.Getpid())
	}
	members, err := PrivateProcessSessionMembership(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	self := -1
	for index, member := range members {
		if member.PID <= 0 || member.ParentPID < 0 || member.ParentPID == member.PID ||
			member.RSSBytes < 0 || member.ObservedName == "" || member.StartIdentity == "" {
			t.Fatalf("member %d is not an individually coherent named record: %+v", index, member)
		}
		if member.PID == os.Getpid() {
			self = index
		}
	}
	if self < 0 {
		t.Fatalf("own session membership does not name the test process: %+v", members)
	}
	if _, err := os.Stdout.WriteString(marker); err != nil {
		t.Fatal(err)
	}
}
