//go:build linux

package typedworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"golang.org/x/sys/unix"
)

func digest(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }
func fixture(t *testing.T) (string, string, typedindex.Inventory, *lifecycle.Gate) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(base, func(name string, e os.DirEntry, err error) error {
			if err == nil && e.IsDir() {
				_ = os.Chmod(name, 0700)
			}
			return nil
		})
	})
	src, dst := filepath.Join(base, "source"), filepath.Join(base, "private")
	for _, name := range []string{src, dst, filepath.Join(src, "bin")} {
		if err = os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	entries := []typedindex.BundleFile{}
	for _, f := range []struct {
		name, data string
		mode       os.FileMode
	}{{"bin/tool", "tool", 0700}, {"data", "data", 0600}} {
		if err = os.WriteFile(filepath.Join(src, f.name), []byte(f.data), f.mode); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, typedindex.BundleFile{Path: f.name, Bytes: int64(len(f.data)), Digest: digest([]byte(f.data)), Executable: f.mode&0111 != 0})
	}
	raw, _ := json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: entries})
	inv, err := typedindex.DecodeInventory(t.Context(), raw, digest(raw))
	if err != nil {
		t.Fatal(err)
	}
	return src, dst, inv, lifecycle.NewGate(dst)
}
func TestCopyAndFreshVerification(t *testing.T) {
	src, dst, inv, gate := fixture(t)
	receipt, err := Copy(t.Context(), src, dst, inv, gate)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != receiptSchema || receipt.InventoryDigest != inv.Digest() || !publishedName(receipt.Name) || len(receipt.Nodes) != 4 {
		t.Fatal(receipt)
	}
	if err = Verify(t.Context(), dst, inv, receipt); err != nil {
		t.Fatal(err)
	}
	// External mutation after copy cannot alter the private independent inode.
	if err = os.WriteFile(filepath.Join(src, "data"), []byte("evil"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = Verify(t.Context(), dst, inv, receipt); err != nil {
		t.Fatal("source still aliases copied bytes", err)
	}
	raw, _ := json.Marshal(receipt)
	var resumed Receipt
	if err = json.Unmarshal(raw, &resumed); err != nil {
		t.Fatal(err)
	}
	if err = Verify(t.Context(), dst, inv, resumed); err != nil {
		t.Fatal("serialized receipt failed", err)
	}
	if _, err = Copy(t.Context(), src, dst, inv, gate); err == nil {
		t.Fatal("changed source digest accepted")
	}
}
func TestSourceRefusalsBeforeStage(t *testing.T) {
	for _, kind := range []string{"zero-inventory", "extra", "missing", "link-leaf", "link-parent", "fifo", "hardlink", "mode", "oversize", "private-mode", "private-link", "unsafe-ancestor", "source-ancestor-link"} {
		t.Run(kind, func(t *testing.T) {
			src, dst, inv, gate := fixture(t)
			data := filepath.Join(src, "data")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "zero-inventory":
				inv = typedindex.Inventory{}
			case "extra":
				must(os.WriteFile(filepath.Join(src, "extra"), nil, 0600))
			case "missing":
				must(os.Remove(data))
			case "link-leaf":
				must(os.Remove(data))
				must(os.Symlink("bin/tool", data))
			case "link-parent":
				must(os.Rename(filepath.Join(src, "bin"), filepath.Join(filepath.Dir(src), "elsewhere")))
				must(os.Symlink("../elsewhere", filepath.Join(src, "bin")))
			case "fifo":
				must(os.Remove(data))
				must(unix.Mkfifo(data, 0600))
			case "hardlink":
				must(os.Link(data, filepath.Join(filepath.Dir(src), "alias")))
			case "mode":
				must(os.Chmod(data, 0700))
			case "oversize":
				must(os.WriteFile(data, []byte("longer"), 0600))
			case "private-mode":
				must(os.Chmod(dst, 0755))
			case "unsafe-ancestor":
				must(os.Chmod(filepath.Dir(dst), 0777))
			case "private-link":
				alias := dst + "-alias"
				must(os.Symlink(dst, alias))
				dst = alias
			case "source-ancestor-link":
				alias := filepath.Join(filepath.Dir(src), "alias")
				must(os.Symlink(filepath.Dir(src), alias))
				src = filepath.Join(alias, "source")
			}
			receipt, err := Copy(t.Context(), src, dst, inv, gate)
			if err == nil || receipt.Name != "" {
				t.Fatal("unsafe copy created custody", receipt, err)
			}
			entries, e := os.ReadDir(dst)
			if e == nil && len(entries) != 0 {
				t.Fatal("preflight grew destination", entries)
			}
		})
	}
}
func TestCapacityAndCancellationBeforeGrowth(t *testing.T) {
	for _, kind := range []string{"bytes", "inodes", "pressure", "canceled", "probe-failure"} {
		t.Run(kind, func(t *testing.T) {
			src, dst, inv, gate := fixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probe := func(f *os.File) (space, error) { return capacity(f) }
			switch kind {
			case "bytes":
				probe = func(*os.File) (space, error) { return space{bytes: 0, inodes: 100, block: 4096}, nil }
			case "inodes":
				probe = func(*os.File) (space, error) { return space{bytes: 1 << 30, inodes: 1, block: 4096}, nil }
			case "pressure":
				probe = func(*os.File) (space, error) {
					return space{total: 100 << 20, bytes: 5 << 20, inodes: 100, block: 4096}, nil
				}
			case "canceled":
				cancel()
			case "probe-failure":
				probe = func(*os.File) (space, error) { return space{}, ErrCustody }
			}
			receipt, err := copyInputs(ctx, src, dst, inv, gate, probe)
			if err == nil || receipt.Name != "" {
				t.Fatal(receipt, err)
			}
			entries, _ := os.ReadDir(dst)
			if len(entries) != 0 {
				t.Fatal("refusal grew staging", entries)
			}
		})
	}
}

func TestDestinationPressureLatchSurvivesCopyRetries(t *testing.T) {
	src, dst, inv, _ := fixture(t)
	gate := lifecycle.NewGateWithProbe(dst, func(context.Context, string) (lifecycle.Capacity, error) {
		t.Fatal("copy must check the opened destination instead of an ancestor")
		return lifecycle.Capacity{}, nil
	})
	for _, used := range []uint64{100, 85, 73, 95, 85, 73} {
		r, err := copyInputs(t.Context(), src, dst, inv, gate, func(*os.File) (space, error) {
			return space{total: 100 << 20, bytes: (100 - used) << 20, inodes: 1000, block: 4096}, nil
		})
		if used >= 75 {
			if !errors.Is(err, lifecycle.ErrPressureRefusal) || r.Name != "" {
				t.Fatalf("destination refusal latch lost at %d%%: %+v %v", used, r, err)
			}
		} else if err != nil || !publishedName(r.Name) {
			t.Fatalf("resume below low watermark: %+v %v", r, err)
		}
	}
}

type cancelAfterSealContext struct {
	context.Context
	cancel      context.CancelFunc
	destination string
}

func (c cancelAfterSealContext) Err() error {
	entries, _ := os.ReadDir(c.destination)
	for _, e := range entries {
		info, err := os.Stat(filepath.Join(c.destination, e.Name(), "bin"))
		if err == nil && info.Mode().Perm() == 0555 {
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestCancellationStopsDirectorySealBeforeNextMutation(t *testing.T) {
	src, dst, inv, gate := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, err := Copy(cancelAfterSealContext{ctx, cancel, dst}, src, dst, inv, gate)
	if !errors.Is(err, context.Canceled) || !strings.HasSuffix(r.Name, ".stage") {
		t.Fatalf("canceled seal did not retain stage: %+v %v", r, err)
	}
	info, err := os.Stat(filepath.Join(dst, r.Name))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("canceled seal mutated the next directory: %v %v", info, err)
	}
}
func TestMutationAndCancellationRetainOwnedStage(t *testing.T) {
	for _, kind := range []string{"change-next-file", "add-extra", "cancel", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			src, dst, inv, gate := fixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			ctx = custodybytes.WithCheckpoint(ctx, func(context.Context) error {
				calls++
				if calls != 1 {
					return nil
				}
				switch kind {
				case "change-next-file":
					return os.WriteFile(filepath.Join(src, "data"), []byte("evil"), 0600)
				case "add-extra":
					return os.WriteFile(filepath.Join(src, "extra"), nil, 0600)
				case "cancel":
					cancel()
					return context.Canceled
				default:
					return errors.New("injected checkpoint refusal")
				}
			})
			receipt, err := Copy(ctx, src, dst, inv, gate)
			if err == nil || !strings.HasSuffix(receipt.Name, ".stage") {
				t.Fatal("missing owned partial custody", receipt, err)
			}
			if _, err = os.Stat(filepath.Join(dst, receipt.Name)); err != nil {
				t.Fatal("partial stage removed", err)
			}
			if err = Verify(t.Context(), dst, inv, receipt); err == nil {
				t.Fatal("partial stage accepted")
			}
		})
	}
}
func TestResumeRefusesMutationAndInodeSubstitution(t *testing.T) {
	for _, kind := range []string{"bytes", "missing", "extra", "writable", "inode", "directory-inode", "hardlink", "symlink", "receipt", "partial", "zero"} {
		t.Run(kind, func(t *testing.T) {
			src, dst, inv, gate := fixture(t)
			r, err := Copy(t.Context(), src, dst, inv, gate)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(dst, r.Name)
			file := filepath.Join(root, "data")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(os.Chmod(root, 0700))
			switch kind {
			case "bytes":
				must(os.Chmod(file, 0600))
				must(os.WriteFile(file, []byte("evil"), 0600))
				must(os.Chmod(file, 0444))
			case "missing":
				must(os.Remove(file))
			case "extra":
				must(os.WriteFile(filepath.Join(root, "extra"), nil, 0444))
			case "writable":
				must(os.Chmod(file, 0644))
			case "inode":
				must(os.Rename(file, file+".old"))
				must(os.WriteFile(file, []byte("data"), 0444))
				must(os.Remove(file + ".old"))
			case "directory-inode":
				bin := filepath.Join(root, "bin")
				must(os.Rename(bin, bin+".old"))
				must(os.Mkdir(bin, 0700))
				must(os.Chmod(bin+".old", 0700))
				must(os.Rename(filepath.Join(bin+".old", "tool"), filepath.Join(bin, "tool")))
				must(os.Remove(bin + ".old"))
				must(os.Chmod(bin, 0555))
			case "hardlink":
				must(os.Link(file, filepath.Join(dst, "alias")))
			case "symlink":
				must(os.Remove(file))
				must(os.Symlink(filepath.Join(src, "data"), file))
			case "receipt":
				r.Nodes = slices.Clone(r.Nodes)
				r.Nodes[0].Inode++
			case "partial":
				r.Name += ".stage"
			case "zero":
				inv = typedindex.Inventory{}
			}
			must(os.Chmod(root, 0555))
			if err = Verify(t.Context(), dst, inv, r); err == nil {
				t.Fatal("accepted mutation", kind)
			}
		})
	}
}

func TestPublishedTreesAreFreshAndRenameCannotReplace(t *testing.T) {
	src, dst, inv, gate := fixture(t)
	first, err := Copy(t.Context(), src, dst, inv, gate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Copy(t.Context(), src, dst, inv, gate)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name == second.Name || first.Nodes[0].Inode == second.Nodes[0].Inode {
		t.Fatal("reused mutable staging", first, second)
	}
	root, err := openDirectory(dst, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err = renameExclusive(root, first.Name, second.Name); err == nil {
		t.Fatal("replaced existing publication")
	}
	for _, r := range []Receipt{first, second} {
		if err = Verify(t.Context(), dst, inv, r); err != nil {
			t.Fatal("rename damaged publication", err)
		}
	}
}

func TestTransferCancellationAndExactLength(t *testing.T) {
	for _, test := range []struct {
		name   string
		data   string
		size   int64
		cancel bool
		pass   bool
	}{
		{"exact", "test", 4, false, true}, {"short", "test", 5, false, false}, {"extra", "test", 3, false, false}, {"cancel", "test", 4, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "read")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			if _, err = file.WriteString(test.data); err != nil {
				t.Fatal(err)
			}
			if _, err = file.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancel {
				cancel()
			}
			var output strings.Builder
			err = transfer(ctx, file, &output, test.size)
			if (err == nil) != test.pass {
				t.Fatal(err)
			}
			if test.cancel && output.Len() != 0 {
				t.Fatal("canceled copy wrote bytes")
			}
		})
	}
}
