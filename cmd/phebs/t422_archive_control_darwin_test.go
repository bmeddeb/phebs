//go:build darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/recovery"
)

// Tiny real filesystem custody; the malformed manifest deliberately exercises
// the shared strict decoder without artifacts, a store, or a native engine.
func TestT422ArchiveRootedCustody(t *testing.T) {
	for _, mode := range []string{"descriptor-read", "wrong-inode", "wrong-volume", "root-symlink", "archive-symlink", "manifest-symlink", "fifo", "root-replaced", "archive-replaced", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			path, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o700); err != nil {
				t.Fatal(err)
			}
			workspace, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = workspace.Close() }()
			info, err := workspace.Stat()
			if err != nil {
				t.Fatal(err)
			}
			input := t422ArchiveInputFixture()
			backup := filepath.Join(path, input.BackupRoot)
			archive := filepath.Join(backup, "archive")
			if err := os.MkdirAll(archive, 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(archive, recovery.ManifestName)
			if err := os.WriteFile(manifest, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			held, err := os.Open(backup)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := dispatchadmission.DescribeProductionWorkspace(held, backup)
			if closeErr := held.Close(); err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			input.Device, input.Inode, input.FSID = binding.Device, binding.Inode, binding.FSID
			volume := binding.FSID
			switch mode {
			case "wrong-inode":
				input.Inode++
			case "wrong-volume":
				input.FSID[0] ^= 1
			case "root-symlink", "archive-symlink":
				target := backup
				if mode == "archive-symlink" {
					target = archive
				}
				if err := os.Rename(target, target+"-held"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target+"-held", target); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			custody, err := openT422ArchiveCustody(ctx, workspace, path, info, volume, input)
			startupRefusal := mode == "wrong-inode" || mode == "wrong-volume" || mode == "root-symlink" || mode == "archive-symlink"
			if (err != nil) != startupRefusal {
				t.Fatalf("custody open: %v", err)
			}
			if startupRefusal {
				if _, err := workspace.Stat(); err != nil {
					t.Fatal("borrowed FD closed", err)
				}
				return
			}
			defer func() {
				if err := custody.close(); err != nil {
					t.Error(err)
				}
			}()
			switch mode {
			case "manifest-symlink", "fifo":
				if err := os.Rename(manifest, manifest+"-held"); err != nil {
					t.Fatal(err)
				}
				if mode == "fifo" {
					err = unix.Mkfifo(manifest, 0o600)
				} else {
					err = os.Symlink(manifest+"-held", manifest)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "root-replaced", "archive-replaced":
				target := backup
				if mode == "archive-replaced" {
					target = archive
				}
				if err := os.Rename(target, target+"-held"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				cancel()
			}
			ctx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := custody.read(ctx, input); err == nil {
				t.Fatal("invalid custody/manifest read accepted")
			}
			counts, err := ledger.Finish()
			want := readaccounting.Counts{}
			if mode == "descriptor-read" {
				want.ControlFileReads = 1
			}
			if err != nil || counts != want {
				t.Fatalf("counts %+v: %v", counts, err)
			}
			if err := custody.close(); err != nil {
				t.Fatal(err)
			}
			if _, err := workspace.Stat(); err != nil {
				t.Fatal("borrowed FD closed", err)
			}
		})
	}
}
