//go:build linux

package t421

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxPressureBorrowPolicyShape(t *testing.T) {
	tests := []struct {
		name   string
		policy linuxPressureBorrowPolicy
		valid  bool
	}{
		{"fixed-nonzero", linuxPressureBorrowPolicy{uid: 123, gid: 456}, true},
		{"zero-uid", linuxPressureBorrowPolicy{gid: 456}, false},
		{"zero-gid", linuxPressureBorrowPolicy{uid: 123}, false},
		{"sentinel-uid", linuxPressureBorrowPolicy{uid: ^uint32(0), gid: 456}, false},
		{"sentinel-gid", linuxPressureBorrowPolicy{uid: 123, gid: ^uint32(0)}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.policy.valid() != test.valid {
				t.Fatal("unexpected policy shape")
			}
		})
	}
	if linuxPressureBorrowBoundingMask != 0x1ffffffffff {
		t.Fatal("fixed source recipe ceiling changed")
	}
}

func TestLinuxPressureBorrowPolicyOwnedChildRefusals(t *testing.T) {
	before, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxPressureBorrowPolicyChild$", "--", "pressure-policy-child")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("owned policy child joined with %v: %s", err, output)
	}
	after, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || after != before {
		t.Fatalf("parent NNP changed: before=%d after=%d err=%v", before, after, err)
	}
}

func TestLinuxPressureBorrowPolicyChild(t *testing.T) {
	if os.Args[len(os.Args)-1] != "pressure-policy-child" {
		return
	}
	// All constrainChild presentations are isolated in this fresh owned child.
	// A regression that mutates NNP before refusing cannot alter the parent.
	ruid, euid, suid := unix.Getresuid()
	rgid, egid, sgid := unix.Getresgid()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	before, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	wrongID := func(ids ...int) uint32 {
		for candidate := uint32(1); ; candidate++ {
			matches := false
			for _, id := range ids {
				matches = matches || candidate == uint32(id)
			}
			if !matches {
				return candidate
			}
		}
	}
	uid, gid := wrongID(ruid, euid, suid), wrongID(rgid, egid, sgid)
	tests := []struct {
		name   string
		policy linuxPressureBorrowPolicy
	}{
		{"wrong-actual-ids", linuxPressureBorrowPolicy{uid: uid, gid: gid}},
		{"zero-uid", linuxPressureBorrowPolicy{gid: gid}},
		{"zero-gid", linuxPressureBorrowPolicy{uid: uid}},
		{"sentinel-uid", linuxPressureBorrowPolicy{uid: ^uint32(0), gid: gid}},
		{"sentinel-gid", linuxPressureBorrowPolicy{uid: uid, gid: ^uint32(0)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.policy.checkCredentials() != ErrLinuxPressure || test.policy.checkChild() != ErrLinuxPressure || test.policy.constrainChild() != ErrLinuxPressure {
				t.Fatal("invalid actual identity/shape accepted")
			}
			after, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
			if err != nil || after != before {
				t.Fatalf("refusal mutated NNP: before=%d after=%d err=%v", before, after, err)
			}
			ar, ae, as := unix.Getresuid()
			gr, ge, gs := unix.Getresgid()
			ag, err := os.Getgroups()
			if ar != ruid || ae != euid || as != suid || gr != rgid || ge != egid || gs != sgid || err != nil || !reflect.DeepEqual(ag, groups) {
				t.Fatal("refusal changed actual IDs or supplementary groups")
			}
		})
	}
	if before == 1 {
		t.Log("inherited NNP=1: unchanged state cannot prove setter omission; source precheck ordering remains an independent review obligation")
	}
	// No positive capability/group-drop/NNP-setting admission is inferred from
	// ordinary unprivileged execution or from a preexisting NNP=1 baseline.
}
