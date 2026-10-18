//go:build darwin

package custodybytes

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestCustodyByteUnionFilesystemRefusal(t *testing.T) {
	owner, _ := custodyByteFixture(t)
	var actual unix.Statfs_t
	if err := unix.Fstatfs(int(owner.file.Fd()), &actual); err != nil {
		t.Fatal(err)
	}
	if id, err := custodyByteFilesystem(actual); err != nil || id != owner.volume {
		t.Fatal("actual private fixture filesystem", err)
	}
	// This exercises the guard, not a claim that a union volume was mounted.
	actual.Flags |= unix.MNT_UNION
	if _, err := custodyByteFilesystem(actual); err == nil {
		t.Fatal("union directory buffering accepted")
	}
}
