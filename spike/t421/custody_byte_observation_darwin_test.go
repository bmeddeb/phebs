//go:build darwin

package t421

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/custodybytes"
)

func custodyByteFixture(t *testing.T) (productionRoot, context.Context) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "custody")
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	owner, err := openProductionRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.file.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	return owner, ctx
}

func actualCustodyByteFixtureTotal(t *testing.T, path string) custodyByteSample {
	t.Helper()
	var want custodyByteSample
	err := filepath.WalkDir(path, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			want.LogicalBytes += uint64(info.Size())
		}
		want.AllocatedBytes += uint64(info.Sys().(*syscall.Stat_t).Blocks) * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return want
}

func TestCustodyByteSharedAdapter(t *testing.T) {
	if maxInputCustodyPathBytes != custodybytes.MaximumPathBytes {
		t.Fatal("path bound changed")
	}
	owner, ctx := custodyByteFixture(t)
	if err := os.WriteFile(filepath.Join(owner.path, "actual"), []byte("shared walker"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := newCustodyByteObservation(owner).Sample(ctx, 2)
	if err != nil || got != actualCustodyByteFixtureTotal(t, owner.path) {
		t.Fatal(got, err)
	}
}
