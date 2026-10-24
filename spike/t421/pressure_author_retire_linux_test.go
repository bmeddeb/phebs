//go:build linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Mechanics fixtures deliberately bypass begin: no genuine creation-scoped
// permit, pressure mount, author provenance or native acceptance is claimed.
func linuxPressureRetirementFixture(t *testing.T) (string, *linuxPressureAuthorRetirementEngine) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	var extended unix.Statx_t
	fd := int(file.Fd())
	if unix.Fstat(fd, &stat) != nil || unix.Fstatfs(fd, &filesystem) != nil ||
		unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &extended) != nil || extended.Mask&unix.STATX_MNT_ID == 0 {
		t.Fatal("actual owned fixture metadata unavailable")
	}
	engine := &linuxPressureAuthorRetirementEngine{root: fd, uid: stat.Uid, gid: stat.Gid, device: uint64(stat.Dev), fsid: filesystem.Fsid.Val,
		mount: extended.Mnt_id, imageBytes: 1 << 30, observation: &executionPreworkObservation{},
		journal: &linuxPressureAuthorRetirementJournal{firstCloseFD: -1}}
	return root, engine
}

func linuxPressureRetirementContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func linuxPressureRetirementFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("owned retirement mechanics\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxPressureAuthorRetirementKernelMechanics(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			root, engine := linuxPressureRetirementFixture(t)
			if populated {
				for _, item := range []struct {
					path string
					mode os.FileMode
				}{{"preparation/tree/payload/readonly", 0o400}, {"preparation/tree/payload/tool", 0o500}, {"preparation/build/work", 0o600}, {"author/git/objects/pack/pack-a.pack", 0o400}, {"author/git/hooks/example", 0o700}, {"author-plan.json", 0o400}} {
					linuxPressureRetirementFile(t, filepath.Join(root, item.path), item.mode)
				}
			}
			if err := engine.run(linuxPressureRetirementContext(t)); err != nil {
				t.Fatal("actual bounded owned-tree retirement", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 || !engine.journal.inventoryComplete || !engine.journal.complete ||
				engine.journal.removed != engine.journal.entries-1 || engine.journal.synced != engine.journal.directories ||
				engine.journal.nativeAttempts == 0 || engine.journal.nativeAttempts > linuxPressureRetireAttempts || engine.journal.closeFailed {
				t.Fatal("actual empty/synced prefix or accounting absent", err)
			}
			attempts := engine.journal.nativeAttempts
			if engine.run(linuxPressureRetirementContext(t)) == nil || engine.journal.nativeAttempts != attempts {
				t.Fatal("mechanical journal was reusable or inventoried again")
			}
		})
	}
}

func TestLinuxPressureAuthorRetirementRefusesBeforeUnlink(t *testing.T) {
	for _, fault := range []string{"foreign-root", "symlink", "hardlink", "mode", "payload-write", "mount", "uid", "image", "allocated", "oversize", "cancel", "control-name"} {
		t.Run(fault, func(t *testing.T) {
			root, engine := linuxPressureRetirementFixture(t)
			marker := filepath.Join(root, "author", "keep")
			linuxPressureRetirementFile(t, marker, 0o600)
			ctx := linuxPressureRetirementContext(t)
			switch fault {
			case "foreign-root":
				linuxPressureRetirementFile(t, filepath.Join(root, "foreign"), 0o600)
			case "symlink":
				if err := os.Symlink("keep", filepath.Join(root, "author", "alias")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(marker, filepath.Join(root, "author", "alias")); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(marker, 0o644); err != nil {
					t.Fatal(err)
				}
			case "payload-write":
				linuxPressureRetirementFile(t, filepath.Join(root, "preparation", "tree", "payload", "bad"), 0o600)
			case "mount":
				engine.mount++
			case "uid":
				engine.uid++
			case "image":
				engine.imageBytes = 0
			case "allocated":
				engine.imageBytes = 1
			case "oversize":
				if err := os.Truncate(marker, linuxPressureRetireFileBytes+1); err != nil {
					t.Fatal("own sparse size refusal fixture", err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "control-name":
				linuxPressureRetirementFile(t, filepath.Join(root, "author", "bad\nname"), 0o600)
			}
			if engine.run(ctx) == nil || engine.journal.removed != 0 || engine.journal.complete {
				t.Fatal("invalid physical inventory reached deletion")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("preflight refusal deleted original prefix", err)
			}
			attempts := engine.journal.nativeAttempts
			if engine.run(linuxPressureRetirementContext(t)) == nil || engine.journal.nativeAttempts != attempts {
				t.Fatal("failed first attempt gained a new inventory")
			}
		})
	}
}

func TestLinuxPressureAuthorRetirementPartialPrefixIdentity(t *testing.T) {
	root, engine := linuxPressureRetirementFixture(t)
	for _, name := range []string{"one", "two"} {
		linuxPressureRetirementFile(t, filepath.Join(root, "author", name), 0o600)
	}
	ctx := linuxPressureRetirementContext(t)
	// Direct mechanical phase access injects a late inode change without
	// admitting a callback or arbitrary root to the production permit issuer.
	engine.journal.started = true
	if engine.inventory(ctx) != nil || !engine.journal.inventoryComplete {
		t.Fatal("actual full physical inventory")
	}
	var files [2]uint32
	count := 0
	for index := engine.journal.entries; index > 1; index-- {
		if engine.journal.row(uint32(index-1)).mode&unix.S_IFMT == unix.S_IFREG {
			files[count] = uint32(index - 1)
			count++
		}
	}
	if count != 2 || engine.dispose(ctx, files[0]) != nil {
		t.Fatal("first actual basename removal")
	}
	var buffer [linuxPressureRetirePath]byte
	relative, err := engine.relative(files[1], &buffer)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, relative)
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	linuxPressureRetirementFile(t, path, 0o600)
	if engine.dispose(ctx, files[1]) == nil || engine.journal.removed != 1 || engine.journal.complete || engine.journal.firstErr == nil {
		t.Fatal("changed remaining inode accepted or partial journal lost")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("late foreign inode unlinked", err)
	}
}

func TestLinuxPressureAuthorRetirementNativeInvalidCloseFact(t *testing.T) {
	_, engine := linuxPressureRetirementFixture(t)
	// An invalid handle gives a real native refusal without racing descriptor
	// number reuse by any other goroutine in the test process.
	if engine.closeFD(-1) == nil || !engine.journal.closeFailed || engine.journal.firstCloseFD != -1 ||
		engine.journal.firstCloseErr == nil || !engine.observation.nativeCloseFailed.Load() || engine.journal.nativeAttempts != 1 {
		t.Fatal("actual invalid-handle native Close refusal was lost")
	}
}

func TestLinuxPressureAuthorRetirementReservationsBeforeUnlink(t *testing.T) {
	for _, limit := range []string{"native", "path"} {
		t.Run(limit, func(t *testing.T) {
			root, engine := linuxPressureRetirementFixture(t)
			marker := filepath.Join(root, "author", "keep")
			linuxPressureRetirementFile(t, marker, 0o600)
			// Only counters are modeled near their ceilings; the full tiny
			// inventory, native operations and untouched marker are actual.
			if limit == "native" {
				engine.journal.nativeAttempts = linuxPressureRetireAttempts - 1000
			} else {
				engine.journal.pathBytes = linuxPressureRetirePathBytes - 64
			}
			if engine.run(linuxPressureRetirementContext(t)) == nil || !engine.journal.inventoryComplete || engine.deleting || engine.journal.removed != 0 {
				t.Fatal("complete preflight did not refuse insufficient deletion reservation", limit)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("reservation refusal deleted original prefix", err)
			}
			attempts, paths := engine.journal.nativeAttempts, engine.journal.pathBytes
			if engine.run(linuxPressureRetirementContext(t)) == nil || engine.journal.nativeAttempts != attempts || engine.journal.pathBytes != paths {
				t.Fatal("spent reservation refusal restarted work")
			}
		})
	}
}

func TestLinuxPressureAuthorRetirementNamedAncestryBookend(t *testing.T) {
	root, engine := linuxPressureRetirementFixture(t)
	linuxPressureRetirementFile(t, filepath.Join(root, "author", "nested", "keep"), 0o600)
	ctx := linuxPressureRetirementContext(t)
	engine.journal.started = true
	if engine.inventory(ctx) != nil {
		t.Fatal("actual mechanical inventory")
	}
	var directory uint32
	for index := uint32(1); uint64(index) < engine.journal.entries; index++ {
		if engine.journal.row(index).depth == 2 && engine.journal.row(index).mode&unix.S_IFMT == unix.S_IFDIR {
			directory = index
		}
	}
	if directory == 0 {
		t.Fatal("nested directory absent")
	}
	pinned, err := engine.openDirectory(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if engine.closeFD(pinned) != nil {
			t.Error("owned pinned fixture close")
		}
	}()
	if err := os.Rename(filepath.Join(root, "author", "nested"), filepath.Join(root, "author", "moved")); err != nil {
		t.Fatal(err)
	}
	if engine.linkedParent(ctx, directory, pinned) == nil || engine.journal.removed != 0 {
		t.Fatal("pinned but unlinked ancestry was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "author", "moved", "keep")); err != nil {
		t.Fatal("moved prefix changed", err)
	}
}

func TestLinuxPressureAuthorRetirementCapacityAndModes(t *testing.T) {
	if unsafe.Sizeof(linuxPressureAuthorRetirementRow{}) > 128 || unsafe.Sizeof(linuxPressureAuthorRetirementJournal{}) > 64<<20 {
		t.Fatal("fixed record/header backing exceeded declared policy")
	}
	for _, item := range []struct {
		path   string
		mode   uint32
		accept bool
	}{{"preparation", unix.S_IFDIR | 0o700, true}, {"author", unix.S_IFREG | 0o600, false}, {"author-plan.json", unix.S_IFREG | 0o400, true}, {"author-plan.json", unix.S_IFREG | 0o600, false}, {"preparation/copy/payload/tool", unix.S_IFREG | 0o500, true}, {"preparation/copy/payload/tool", unix.S_IFREG | 0o700, false}, {"preparation/copy/payload/tool", unix.S_IFREG | 0o755, false}, {"preparation/phebs-t422-reference-x/source/scripts/x.sh", unix.S_IFREG | 0o755, true}, {"preparation/phebs-t422-reference-x/source/payload/x.sh", unix.S_IFREG | 0o755, false}, {"preparation/phebs-t422-reference-x/reference/tool", unix.S_IFREG | 0o755, false}, {"preparation/other/source/scripts/x.sh", unix.S_IFREG | 0o755, false}, {"preparation/source/scripts/x.sh", unix.S_IFREG | 0o755, false}, {"author/checkout/scripts/x.sh", unix.S_IFREG | 0o755, false}, {"author/git/objects/pack/x", unix.S_IFREG | 0o400, true}, {"foreign/x", unix.S_IFREG | 0o600, false}} {
		if linuxPressureAuthorRetirementMode(item.path, item.mode) != item.accept {
			t.Fatal("source-owned role/mode fence", item.path, item.mode)
		}
	}
	// Synthetic counters exercise refusal arithmetic, not a fabricated 4M tree.
	for _, limit := range []string{"entries", "names", "depth", "path"} {
		journal := &linuxPressureAuthorRetirementJournal{}
		engine := linuxPressureAuthorRetirementEngine{journal: journal}
		row := linuxPressureAuthorRetirementRow{}
		switch limit {
		case "entries":
			journal.entries = linuxPressureRetireEntries
		case "names":
			journal.nameBytes = linuxPressureRetireNames
		case "depth":
			row.depth = linuxPressureRetireDepth + 1
		case "path":
			row.pathLen = linuxPressureRetirePath + 1
		}
		if engine.appendRow(row, []byte("x")) == nil || journal.rowBacking != 0 || journal.nameBacking != 0 {
			t.Fatal("capacity refusal allocated entry backing", limit)
		}
	}
}

// Actual issuance requires the private view, namespace and native admission.
// These fixtures prove only that retirement consumes one exact pre-writer owner
// and refuses an absent, replaced, used or mismatched capability.
func TestLinuxPressureAuthorWorkspaceRetirementPreservesCwd(t *testing.T) {
	root, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	view, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := view.Close(); err != nil {
			t.Error(err)
		}
	})
	owner := &linuxPressureAuthorWorkspace{root: root, closed: true, healthy: true, view: &linuxPressureAuthorView{file: view}}
	owner.self = owner
	permit := &linuxPressureAuthorRetirement{owner: owner, root: root, imageBytes: 1 << 20}
	owner.retirement = permit
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.retire(linuxPressureRetirementContext(t), 1<<20); err == nil {
		t.Fatal("synthetic non-owner retirement succeeded")
	}
	after, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("retirement changed the caller cwd: %q -> %q", before, after)
	}
}

func TestLinuxPressureAuthorWorkspaceConsumesPreissuedRetirement(t *testing.T) {
	const imageBytes = 1 << 30
	for _, fault := range []string{"absent", "foreign-owner", "root-swap", "used", "image", "root-closed", "not-closed", "unhealthy", "workspace-error"} {
		t.Run(fault, func(t *testing.T) {
			root, err := os.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			})
			other, err := os.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := other.Close(); err != nil {
					t.Error(err)
				}
			})
			owner := &linuxPressureAuthorWorkspace{self: nil, root: root, closed: true, healthy: true}
			owner.self = owner
			permit := &linuxPressureAuthorRetirement{owner: owner, root: root, imageBytes: imageBytes}
			switch fault {
			case "absent":
			case "foreign-owner":
				permit.owner = &linuxPressureAuthorWorkspace{}
				owner.retirement = permit
			case "root-swap":
				permit.root = other
				owner.retirement = permit
			case "used":
				permit.used.Store(true)
				owner.retirement = permit
			case "image":
				permit.imageBytes = imageBytes + 1
				owner.retirement = permit
			case "root-closed":
				owner.rootClosed = true
				owner.retirement = permit
			case "not-closed":
				owner.closed = false
				owner.retirement = permit
			case "unhealthy":
				owner.healthy = false
				owner.retirement = permit
			case "workspace-error":
				owner.err = ErrLinuxPressure
				owner.retirement = permit
			}
			if err := owner.retire(linuxPressureRetirementContext(t), imageBytes); err == nil || permit.used.Load() != (fault == "used") {
				t.Fatal("retirement minted or consumed a non-exact owner", fault)
			}
		})
	}
}
