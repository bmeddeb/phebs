//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/sumdb/dirhash"
)

func newLinuxGoStagingFixture(t *testing.T) *linuxGoBuildStaging {
	t.Helper()
	directory := t.TempDir()
	if os.Chmod(directory, 0o700) != nil {
		t.Fatal("private staging")
	}
	staging, err := newLinuxGoBuildStaging(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if staging.tree.Close() != nil {
			t.Error("staging root close")
		}
	})
	return staging
}

func TestLinuxGoBuildStagingCopiesBoundedOwnedBytes(t *testing.T) {
	staging := newLinuxGoStagingFixture(t)
	source := t.TempDir()
	if os.WriteFile(filepath.Join(source, "tool"), []byte("selected bytes"), 0o700) != nil {
		t.Fatal("input")
	}
	if err := staging.copyTree(t.Context(), source, ".", "sdk"); err != nil {
		t.Fatal(err)
	}
	info, err := staging.tree.Stat("sdk/tool")
	raw, readErr := staging.tree.ReadFile("sdk/tool")
	if err != nil || readErr != nil || info.Mode().Perm() != 0o500 || string(raw) != "selected bytes" ||
		staging.inventory != (ExecutionGoBuildInventory{Entries: 3, Files: 1, Bytes: 14}) || staging.pathBytes != len("inputs")+len("inputs/sdk")+len("inputs/sdk/tool") {
		t.Fatal("staging copy/modes/aggregate differs", staging.inventory, err, readErr)
	}
	if err := staging.writeControl(t.Context(), "control", []byte("fixed")); err != nil {
		t.Fatal(err)
	}
	if staging.inventory.Files != 2 || staging.inventory.Bytes != 19 {
		t.Fatal("control bytes not charged")
	}
}

func TestLinuxGoBuildStagingRefusesBoundsAndIrregularCopies(t *testing.T) {
	for _, test := range []string{"symlink", "hardlink", "oversized", "remaining-bytes", "entry-budget", "path-budget", "wire-path-limit", "canceled"} {
		t.Run(test, func(t *testing.T) {
			staging := newLinuxGoStagingFixture(t)
			source := t.TempDir()
			path := filepath.Join(source, "input")
			if os.WriteFile(path, []byte("input"), 0o600) != nil {
				t.Fatal("input")
			}
			ctx := t.Context()
			switch test {
			case "symlink":
				if os.Symlink(path, filepath.Join(source, "linked")) != nil {
					t.Fatal("link")
				}
			case "hardlink":
				if os.Link(path, filepath.Join(source, "linked")) != nil {
					t.Fatal("link")
				}
			case "oversized":
				if os.Truncate(path, maxInputCustodyFileBytes+1) != nil {
					t.Fatal("size")
				}
			case "remaining-bytes":
				staging.inventory.Bytes = maxGoBuildBytes - 1
			case "entry-budget":
				staging.inventory.Entries = maxGoBuildEntries
			case "path-budget":
				staging.pathBytes = maxGoBuildPathBytes
			case "wire-path-limit":
				if staging.addName(strings.Repeat("a/", 2045)+"x") != ErrExecutionGoBuildCustody {
					t.Fatal("wire prefix excluded from path bound")
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if staging.copyTree(ctx, source, ".", "sdk") == nil || staging.err != ErrExecutionGoBuildCustody {
				t.Fatal("unsafe copy accepted")
			}
			if staging.writeControl(t.Context(), "later", []byte("must refuse")) != ErrExecutionGoBuildCustody {
				t.Fatal("construction failure did not latch")
			}
		})
	}
}

func TestLinuxGoBuildStagingRequiresEmptyPrivateRoot(t *testing.T) {
	for _, test := range []string{"nonprivate", "nonempty", "symlink"} {
		t.Run(test, func(t *testing.T) {
			path := t.TempDir()
			if os.Chmod(path, 0o700) != nil {
				t.Fatal("root")
			}
			switch test {
			case "nonprivate":
				if os.Chmod(path, 0o755) != nil {
					t.Fatal("mode")
				}
			case "nonempty":
				if os.WriteFile(filepath.Join(path, "existing"), nil, 0o600) != nil {
					t.Fatal("input")
				}
			case "symlink":
				link := filepath.Join(t.TempDir(), "root")
				if os.Symlink(path, link) != nil {
					t.Fatal("link")
				}
				path = link
			}
			if staging, err := newLinuxGoBuildStaging(path); err == nil || staging != nil {
				t.Fatal("unowned staging root accepted")
			}
		})
	}
}

func TestLinuxGoBuildStagingExactCleanup(t *testing.T) {
	for _, test := range []string{"valid", "unknown", "replacement", "canceled"} {
		t.Run(test, func(t *testing.T) {
			staging := newLinuxGoStagingFixture(t)
			if staging.writeControl(t.Context(), "nested/control", []byte("selected")) != nil {
				t.Fatal("control")
			}
			ctx := t.Context()
			switch test {
			case "unknown":
				if staging.tree.WriteFile("unknown", []byte("retain"), 0o600) != nil {
					t.Fatal("unknown entry")
				}
			case "replacement":
				if staging.tree.Rename("nested/control", "old") != nil || staging.tree.WriteFile("nested/control", []byte("retain"), 0o400) != nil {
					t.Fatal("replacement")
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := staging.removeContents(ctx)
			if test == "valid" {
				if err != nil || len(staging.names) != 1 {
					t.Fatal("exact joined cleanup refused", err)
				}
				if _, err := staging.tree.Stat("nested"); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("owned entries survived", err)
				}
			} else if err != ErrExecutionGoBuildCustody {
				t.Fatal("uncertain cleanup accepted", err)
			} else if _, err := staging.tree.Stat("nested/control"); err != nil {
				t.Fatal("refusal deleted retained entry", err)
			}
		})
	}
}

func TestLinuxGoBuildStagingCleanupRejectsRedirectedPrefix(t *testing.T) {
	for _, test := range []string{"ancestor", "root"} {
		t.Run(test, func(t *testing.T) {
			staging := newLinuxGoStagingFixture(t)
			if staging.writeControl(t.Context(), "nested/control", []byte("retain")) != nil {
				t.Fatal("control")
			}
			// The baseline inventory is complete. Change its named ancestry
			// before the deletion step while keeping the selected file inode.
			if test == "ancestor" {
				if staging.tree.Rename("nested", "retained-nested") != nil || os.Symlink("retained-nested", filepath.Join(staging.directory, "nested")) != nil {
					t.Fatal("internal redirect")
				}
			} else if os.Rename(staging.directory, staging.directory+"-retained") != nil || os.Mkdir(staging.directory, 0o700) != nil {
				t.Fatal("root replacement")
			}
			if staging.removeEntry(t.Context(), "nested/control") != ErrExecutionGoBuildCustody {
				t.Fatal("cleanup followed changed prefix")
			}
			path := "nested/control"
			if test == "ancestor" {
				path = "retained-nested/control"
			}
			if raw, err := staging.tree.ReadFile(path); err != nil || string(raw) != "retain" {
				t.Fatal("redirected original inode was removed", err)
			}
		})
	}
}

func TestLinuxGoBuildStagingChecksumSelectedModules(t *testing.T) {
	for _, test := range []string{"valid", "descriptor", "directory", "metadata-version", "descriptor-only", "descriptor-only-linked-contents"} {
		t.Run(test, func(t *testing.T) {
			staging := newLinuxGoStagingFixture(t)
			cache := t.TempDir()
			moduleDir := filepath.Join(cache, "example.com/neutral@v1.0.0")
			download := filepath.Join(cache, "cache/download/example.com/neutral/@v")
			for _, path := range []string{moduleDir, download} {
				if os.MkdirAll(path, 0o700) != nil {
					t.Fatal("cache")
				}
			}
			raw := []byte("module example.com/neutral\n\ngo 1.26\n")
			for _, path := range []string{filepath.Join(moduleDir, "go.mod"), filepath.Join(download, "v1.0.0.mod")} {
				if os.WriteFile(path, raw, 0o600) != nil {
					t.Fatal("module")
				}
			}
			sum, err := dirhash.HashDir(moduleDir, "example.com/neutral@v1.0.0", dirhash.Hash1)
			if err != nil {
				t.Fatal(err)
			}
			modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil })
			if err != nil {
				t.Fatal(err)
			}
			if os.WriteFile(filepath.Join(download, "v1.0.0.info"), []byte(`{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z","Origin":{"URL":"discard lookup metadata"}}`), 0o600) != nil ||
				os.WriteFile(filepath.Join(download, "v1.0.0.ziphash"), []byte("unverified cache claim"), 0o600) != nil {
				t.Fatal("lookup controls")
			}
			sums := "example.com/neutral v1.0.0/go.mod " + modSum + "\n"
			if !strings.HasPrefix(test, "descriptor-only") {
				sums += "example.com/neutral v1.0.0 " + sum + "\n"
			}
			if staging.writeControl(t.Context(), "source/go.mod", []byte("module github.com/bmeddeb/phebs\n\ngo 1.26\n")) != nil ||
				staging.writeControl(t.Context(), "source/go.sum", []byte(sums)) != nil {
				t.Fatal("exact source controls")
			}
			switch test {
			case "descriptor":
				if os.WriteFile(filepath.Join(download, "v1.0.0.mod"), []byte("altered"), 0o600) != nil {
					t.Fatal("alter")
				}
			case "directory":
				if os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("altered"), 0o600) != nil {
					t.Fatal("alter")
				}
			case "metadata-version":
				if os.WriteFile(filepath.Join(download, "v1.0.0.info"), []byte(`{"Version":"v1.0.1","Time":"2026-01-01T00:00:00Z"}`), 0o600) != nil {
					t.Fatal("alter")
				}
			case "descriptor-only-linked-contents":
				if os.RemoveAll(moduleDir) != nil || os.Symlink(t.TempDir(), moduleDir) != nil {
					t.Fatal("ambient link")
				}
			}
			err = staging.prepareModules(t.Context(), cache)
			if test == "descriptor" || test == "directory" || test == "metadata-version" {
				if err != ErrExecutionGoBuildCustody {
					t.Fatal("unverified cache alteration accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			prefix := "modules/cache/download/example.com/neutral/@v/v1.0.0"
			info, err := staging.tree.ReadFile(prefix + ".info")
			if err != nil || bytes.Contains(info, []byte("Origin")) {
				t.Fatal("lookup origin imported", err)
			}
			marker, err := staging.tree.ReadFile(prefix + ".ziphash")
			if strings.HasPrefix(test, "descriptor-only") {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("descriptor-only imported content authority")
				}
				if _, err := staging.tree.Lstat("modules/example.com/neutral@v1.0.0"); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("descriptor-only imported cache contents")
				}
			} else if err != nil || string(marker) != sum {
				t.Fatal("did not regenerate checksum from verified module", err)
			}
		})
	}
}
