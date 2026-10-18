//go:build linux

package custodybytes

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestCustodyByteLinuxMetadataIdentity(t *testing.T) {
	before := unix.Stat_t{Dev: 1, Ino: 2, Mode: unix.S_IFREG | 0o600, Nlink: 1, Uid: 3, Gid: 4,
		Rdev: 5, Size: 6, Blksize: 4096, Blocks: 8, Mtim: unix.Timespec{Sec: 9}, Ctim: unix.Timespec{Sec: 10}}
	for _, test := range []struct {
		name   string
		mutate func(*unix.Stat_t)
	}{
		{"device", func(s *unix.Stat_t) { s.Dev++ }},
		{"inode", func(s *unix.Stat_t) { s.Ino++ }},
		{"mode", func(s *unix.Stat_t) { s.Mode++ }},
		{"links", func(s *unix.Stat_t) { s.Nlink++ }},
		{"uid", func(s *unix.Stat_t) { s.Uid++ }},
		{"gid", func(s *unix.Stat_t) { s.Gid++ }},
		{"rdev", func(s *unix.Stat_t) { s.Rdev++ }},
		{"size", func(s *unix.Stat_t) { s.Size++ }},
		{"block_size", func(s *unix.Stat_t) { s.Blksize++ }},
		{"allocated_blocks", func(s *unix.Stat_t) { s.Blocks++ }},
		{"mtime", func(s *unix.Stat_t) { s.Mtim.Nsec++ }},
		{"ctime", func(s *unix.Stat_t) { s.Ctim.Nsec++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := before
			test.mutate(&after)
			if !sameCustodyByteStat(before, before) || sameCustodyByteStat(before, after) {
				t.Fatal("changed native metadata admitted")
			}
		})
	}
}

func TestCustodyByteLinuxFilesystemIdentity(t *testing.T) {
	want := [2]int32{12, -34}
	filesystem := unix.Statfs_t{Fsid: unix.Fsid{Val: want}}
	got, err := custodyByteFilesystem(filesystem)
	if err != nil || got != want {
		t.Fatal("actual filesystem identity was synthesized", got, err)
	}
}
