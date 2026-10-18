package t421

import (
	"os"
	"testing"
)

func TestLinuxInputCopyOwnershipModel(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       os.FileMode
		nativeMode uint32
		uid        uint32
		euid       int
		links      uint64
		want       bool
	}{
		{"regular", 0o600, 0o100600, 1000, 1000, 1, true},
		{"read-only regular", 0o400, 0o100400, 1000, 1000, 1, true},
		{"executable regular", 0o500, 0o100500, 1000, 1000, 1, true},
		{"directory", os.ModeDir | 0o700, 0o040700, 1000, 1000, 2, true},
		{"one-link directory", os.ModeDir | 0o700, 0o040700, 1000, 1000, 1, true},
		{"multi-link directory", os.ModeDir | 0o700, 0o040700, 1000, 1000, 8, true},
		{"root-owned root caller", 0o600, 0o100600, 0, 0, 1, true},
		{"different owner", 0o600, 0o100600, 1001, 1000, 1, false},
		{"root owner not current", 0o600, 0o100600, 0, 1000, 1, false},
		{"negative effective owner", 0o600, 0o100600, ^uint32(0), -1, 1, false},
		{"wide effective owner", 0o600, 0o100600, 0, 1 << 32, 1, false},
		{"unlinked regular", 0o600, 0o100600, 1000, 1000, 0, false},
		{"hard-linked regular", 0o600, 0o100600, 1000, 1000, 2, false},
		{"unlinked directory", os.ModeDir | 0o700, 0o040700, 1000, 1000, 0, false},
		{"surface setuid", os.ModeSetuid | 0o600, 0o100600, 1000, 1000, 1, false},
		{"surface setgid", os.ModeSetgid | 0o600, 0o100600, 1000, 1000, 1, false},
		{"native setuid", 0o600, 0o104600, 1000, 1000, 1, false},
		{"native setgid", 0o600, 0o102600, 1000, 1000, 1, false},
		{"setgid directory", os.ModeDir | 0o700, 0o042700, 1000, 1000, 2, false},
		{"surface symlink", os.ModeSymlink | 0o600, 0o100600, 1000, 1000, 1, false},
		{"native symlink", 0o600, 0o120600, 1000, 1000, 1, false},
		{"native fifo", 0o600, 0o010600, 1000, 1000, 1, false},
		{"native socket", 0o600, 0o140600, 1000, 1000, 1, false},
		{"native block device", 0o600, 0o060600, 1000, 1000, 1, false},
		{"native character device", 0o600, 0o020600, 1000, 1000, 1, false},
		{"surface directory mismatch", os.ModeDir | 0o600, 0o100600, 1000, 1000, 1, false},
		{"native directory mismatch", 0o600, 0o040600, 1000, 1000, 2, false},
		{"surface directory with symlink", os.ModeDir | os.ModeSymlink | 0o700, 0o040700, 1000, 1000, 2, false},
		{"permission mismatch", 0o600, 0o100400, 1000, 1000, 1, false},
		{"unknown native type", 0o600, 0o600, 1000, 1000, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := linuxInputCopyOwnerMatches(test.mode, test.nativeMode, test.uid, test.euid, test.links); got != test.want {
				t.Fatalf("ownership observation = %t, want %t", got, test.want)
			}
		})
	}
}
