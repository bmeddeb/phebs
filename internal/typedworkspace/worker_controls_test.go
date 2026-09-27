//go:build linux

package typedworkspace

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"golang.org/x/sys/unix"
)

// Only neutral tests bypass the mount syscall proof. The public loader neither
// accepts this function nor lets its caller supply a path or expected seal hash.
func testControlMount(*os.File) error { return nil }
func TestWorkerControlsBothPhases(t *testing.T) {
	f, id, _, spec, _ := controlsFixture(t)
	for _, s := range []ControlSpec{spec, executionControls(f, spec)} {
		ref, err := InstallControls(t.Context(), f.dir, id, s, f.gate)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(f.dir, id.RelativeName(), controlName(s.Phase))
		got, err := loadWorkerControls(t.Context(), path, s.Allowance, string(s.Phase), ref.Identity.RequestDigest, ref.Seal.Digest, testControlMount)
		if err != nil || got.Parent.Digest() != s.Parent.Digest() || got.Execution.Digest() != s.Execution.Digest() || got.Plan.Digest() != s.Plan.Digest() || got.Inventory.Digest() != digest(s.InventoryRaw) || got.Profile.Digest() != s.Profile.Digest() || got.Allowance != s.Allowance || string(got.Phase) != string(s.Phase) {
			t.Fatal("exact snapshot round trip", got, err)
		}
		if _, err = loadWorkerControls(t.Context(), path, s.Allowance, string(s.Phase), ref.Identity.RequestDigest, ref.Seal.Digest, workerReadOnly); err == nil {
			t.Fatal("writable mount accepted")
		}
		raw, err := os.ReadFile(filepath.Join(path, "inventory.json"))
		if err != nil {
			t.Fatal(err)
		}
		expected := s.InventoryRaw
		if !bytes.Equal(raw, expected) || digest(raw) != s.Parent.Request().BundleDigest {
			t.Fatal("inventory transport")
		}
	}
	if _, err := LoadWorkerControls(t.Context(), typedsandbox.WorkerInvocation{}); err == nil {
		t.Fatal("invented token admitted")
	}
}
func TestWorkerControlsCustodyRefusals(t *testing.T) {
	for _, fault := range []string{"missing inventory", "inventory mutation", "oversize inventory", "symlink", "hardlink", "fifo", "writable", "extra", "root inode", "seal hash", "phase", "allowance", "canceled", "ancestor symlink"} {
		t.Run(fault, func(t *testing.T) {
			f, id, _, s, _ := controlsFixture(t)
			ref, err := InstallControls(t.Context(), f.dir, id, s, f.gate)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.dir, id.RelativeName(), controlName(s.Phase))
			target := filepath.Join(path, "inventory.json")
			if err = os.Chmod(path, 0700); err != nil {
				t.Fatal(err)
			}
			phase := string(s.Phase)
			seal := ref.Seal.Digest
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch fault {
			case "missing inventory":
				err = os.Remove(target)
			case "inventory mutation":
				var raw []byte
				raw, err = os.ReadFile(target)
				if err == nil {
					err = os.Chmod(target, 0600)
				}
				if err == nil {
					err = os.WriteFile(target, bytes.Repeat([]byte("x"), len(raw)), 0444)
				}
				if err == nil {
					err = os.Chmod(target, 0444)
				}
			case "oversize inventory":
				err = os.Chmod(target, 0600)
				if err == nil {
					err = os.Truncate(target, typedindex.MaxInventoryBytes+1)
				}
				if err == nil {
					err = os.Chmod(target, 0444)
				}
			case "symlink":
				err = os.Remove(target)
				if err == nil {
					err = os.Symlink("parent.json", target)
				}
			case "hardlink":
				err = os.Link(target, filepath.Join(t.TempDir(), "alias"))
			case "fifo":
				err = os.Remove(target)
				if err == nil {
					err = unix.Mkfifo(target, 0444)
				}
			case "writable":
				err = os.Chmod(target, 0644)
			case "extra":
				err = os.WriteFile(filepath.Join(path, "extra"), []byte("x"), 0444)
			case "root inode":
				raw, e := os.ReadFile(filepath.Join(path, ControlSealFile))
				if e != nil {
					t.Fatal(e)
				}
				decoded, e := decodeControlSeal(raw)
				if e != nil {
					t.Fatal(e)
				}
				decoded.Directory.Inode++
				raw, e = encodeControlSeal(decoded)
				if e != nil {
					t.Fatal(e)
				}
				seal = digest(raw)
				err = os.Chmod(filepath.Join(path, ControlSealFile), 0600)
				if err == nil {
					err = os.WriteFile(filepath.Join(path, ControlSealFile), raw, 0444)
				}
				if err == nil {
					err = os.Chmod(filepath.Join(path, ControlSealFile), 0444)
				}
			case "seal hash":
				seal = digest([]byte("other"))
			case "phase":
				phase = "execute"
			case "allowance":
				s.Allowance.AttemptDigest = digest([]byte("other"))
			case "canceled":
				cancel()
			case "ancestor symlink":
				alias := filepath.Join(t.TempDir(), "alias")
				err = os.Symlink(filepath.Dir(path), alias)
				if err == nil {
					if e := os.Chmod(path, 0555); e != nil {
						t.Fatal(e)
					}
					path = filepath.Join(alias, filepath.Base(path))
				}
			}
			if err != nil {
				t.Fatal("fixture", err)
			}
			if fault != "ancestor symlink" {
				if err = os.Chmod(path, 0555); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = loadWorkerControls(ctx, path, s.Allowance, phase, ref.Identity.RequestDigest, seal, testControlMount); err == nil {
				t.Fatal("custody accepted")
			}
		})
	}
}
func TestWorkerControlSealWorstCaseAndMalformedInventory(t *testing.T) {
	f, id, _, spec, _ := controlsFixture(t)
	spec = executionControls(f, spec)
	identity, data, err := controlData(t.Context(), id, spec)
	if err != nil {
		t.Fatal(err)
	}
	seal := controlSeal{Schema: controlSealSchema, Identity: identity, Directory: Node{Path: controlName(spec.Phase), Device: math.MaxUint64, Inode: math.MaxUint64, Directory: true}}
	for _, name := range controlFiles(spec.Phase) {
		seal.Files = append(seal.Files, controlFile{name, OwnerControl{Digest: digest(data[name]), Bytes: int64(controlLimit(name)), Device: math.MaxUint64, Inode: math.MaxUint64}})
	}
	raw, err := encodeControlSeal(seal)
	if err != nil || len(raw) > 4096 {
		t.Fatal("worst case control seal", len(raw), err)
	}
	if len(seal.Files) != 6 {
		t.Fatal("inventory omitted from file budget")
	}
	data["inventory.json"] = []byte(`{"schema":"phebs-typed-prehydration-v1","files":[]}`)
	if _, err = decodeWorkerControls(t.Context(), seal, data); err == nil {
		t.Fatal("invented inventory admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = loadWorkerControls(ctx, "/absent", spec.Allowance, "execute", identity.RequestDigest, digest(raw), testControlMount); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestWorkerControlsDirectoryAndReadRaces(t *testing.T) {
	for _, fault := range []string{"directory replacement", "cancel after read"} {
		t.Run(fault, func(t *testing.T) {
			f, id, _, s, _ := controlsFixture(t)
			ref, err := InstallControls(t.Context(), f.dir, id, s, f.gate)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.dir, id.RelativeName(), controlName(s.Phase))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			probe := func(*os.File) error {
				calls++
				if fault == "directory replacement" && calls == 1 {
					if e := os.Rename(path, path+"-old"); e != nil {
						t.Fatal(e)
					}
					if e := os.Mkdir(path, 0555); e != nil {
						t.Fatal(e)
					}
				}
				if calls == 2 {
					if fault == "cancel after read" {
						cancel()
					}
				}
				return nil
			}
			_, err = loadWorkerControls(ctx, path, s.Allowance, string(s.Phase), ref.Identity.RequestDigest, ref.Seal.Digest, probe)
			if err == nil {
				t.Fatal("racing custody accepted")
			}
			if fault == "cancel after read" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation", err)
			}
		})
	}
}
func TestWorkerControlsActualReadonlyProbe(t *testing.T) {
	for _, path := range []string{"/", t.TempDir()} {
		root, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var fs unix.Statfs_t
		if err = unix.Fstatfs(int(root.Fd()), &fs); err != nil {
			t.Fatal(err)
		}
		got := workerReadOnly(root)
		if err = root.Close(); err != nil {
			t.Fatal(err)
		}
		readonly := fs.Flags&unix.ST_RDONLY != 0
		if (got == nil) != readonly {
			t.Fatal("mount proof mismatch", readonly, got)
		}
		t.Logf("actual path %s readonly=%t", path, readonly)
	}
}

func TestWorkerControlsWhitespaceInventory(t *testing.T) {
	f, id, _, s, _ := controlsFixtureInventory(t, true)
	ref, err := InstallControls(t.Context(), f.dir, id, s, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dir, id.RelativeName(), controlName(s.Phase))
	got, err := loadWorkerControls(t.Context(), path, s.Allowance, string(s.Phase), ref.Identity.RequestDigest, ref.Seal.Digest, testControlMount)
	if err != nil || got.Inventory.Digest() != digest(s.InventoryRaw) {
		t.Fatal("whitespace inventory identity lost", err)
	}
	raw, err := os.ReadFile(filepath.Join(path, "inventory.json"))
	if err != nil || !bytes.Equal(raw, s.InventoryRaw) {
		t.Fatal("original bytes rewritten", err)
	}
	s.InventoryRaw = append(bytes.Clone(s.InventoryRaw), ' ')
	if _, err = OpenControls(t.Context(), f.dir, id, s, ref); err == nil {
		t.Fatal("changed whitespace changed identity but was accepted")
	}
}

func TestWorkerControlsReturnedSnapshotDoesNotAliasPath(t *testing.T) {
	f, id, _, s, _ := controlsFixture(t)
	ref, err := InstallControls(t.Context(), f.dir, id, s, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dir, id.RelativeName(), controlName(s.Phase))
	got, err := loadWorkerControls(t.Context(), path, s.Allowance, string(s.Phase), ref.Identity.RequestDigest, ref.Seal.Digest, testControlMount)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(path, "inventory.json")
	if err = os.Chmod(target, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(target, bytes.Repeat([]byte("x"), len(s.InventoryRaw)), 0444); err != nil {
		t.Fatal(err)
	}
	expected, err := typedindex.DecodeInventory(t.Context(), s.InventoryRaw, s.Parent.Request().BundleDigest)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inventory.Digest() != expected.Digest() || !slices.Equal(got.Inventory.Files(), expected.Files()) || got.Parent.Request() != s.Parent.Request() {
		t.Fatal("returned snapshot followed mutable pathname")
	}
	escaped := got.Inventory.Files()
	escaped[0].Path = "changed"
	if !slices.Equal(got.Inventory.Files(), expected.Files()) {
		t.Fatal("returned inventory aliased caller slice")
	}
}
