//go:build darwin

package t421

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/sys/unix"
)

func TestExecutionGoBuildCustodyRealOfflineReference(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	cache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(cache, "example.com/neutral@v1.0.0")
	download := filepath.Join(cache, "cache/download/example.com/neutral/@v")
	for _, directory := range []string{moduleDir, download} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	moduleRaw := []byte("module example.com/neutral\n\ngo 1.26\n")
	for path, content := range map[string][]byte{
		filepath.Join(moduleDir, "go.mod"):     moduleRaw,
		filepath.Join(moduleDir, "neutral.go"): []byte("package neutral\nconst Message = \"exact\"\n"),
		filepath.Join(download, "v1.0.0.mod"):  moduleRaw,
		filepath.Join(download, "v1.0.0.info"): []byte(`{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z","Origin":{"URL":"https://private.example.invalid/ignored"}}`),
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := dirhash.HashDir(moduleDir, "example.com/neutral@v1.0.0", dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(moduleRaw)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(download, "v1.0.0.ziphash"), []byte(sum), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.write(t, "go.mod", "module github.com/bmeddeb/phebs\n\ngo 1.26\n\nrequire example.com/neutral v1.0.0\n")
	fixture.write(t, "go.sum", "example.com/neutral v1.0.0 "+sum+"\nexample.com/neutral v1.0.0/go.mod "+modSum+"\n")
	fixture.write(t, "cmd/phebs-focused-index/main.go", "package main\nimport \"example.com/neutral\"\nfunc main() { println(neutral.Message) }\n")
	fixture.command(t, "add", "go.mod", "go.sum", "cmd/phebs-focused-index/main.go")
	fixture.source = fixture.commit(t, "neutral protected offline build")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	probe := exec.CommandContext(t.Context(), goBinary, "env", "GOROOT")
	probe.Dir, probe.Env = parent, []string{"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "PATH=/usr/bin:/bin", "LC_ALL=C"}
	goRootRaw, err := probe.Output()
	if err != nil {
		t.Fatal(err)
	}
	goRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(goRootRaw)))
	if err != nil {
		t.Fatal(err)
	}
	request := ReferenceToolRequest{RepositoryRoot: fixture.root, GitBinary: fixture.git, GoRoot: goRoot, ModuleCache: cache,
		PlanSourceCommit: fixture.plan, IntegratedMainCommit: fixture.integration, SourceCommit: fixture.source, Role: "phebs-focused-index"}
	workspace := newReferenceToolBuildWorkspace(t, request)
	request.Binary = filepath.Join(workspace, "supplied")
	buildReferenceToolFixture(t, request, workspace)
	// Poison advisory cache material after the supplied build. Neither a ziphash
	// claim nor unrelated cache contents may be imported as build authority.
	if err := os.WriteFile(filepath.Join(download, "v1.0.0.ziphash"), []byte("forged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "unverified-extra"), []byte("not selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	inputs, err := ProtectExecutionGoBuildInputs(t.Context(), parent, ExecutionGoBuildRequest{Git: git,
		RepositoryRoot: fixture.root, PlanSourceCommit: fixture.plan, IntegratedMainCommit: fixture.integration,
		SourceCommit: fixture.source, GoRoot: goRoot, ModuleCache: cache})
	goBuildTestCleanup(t, inputs)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("protected actual SDK + tiny module/source in %s: %+v", time.Since(started), inputs.Inventory())
	goBuildTestDescriptors(t, inputs)
	for _, entry := range inputs.entries {
		if entry.info == nil || !inputCustodyProtected(entry.info) {
			t.Fatal("a Go input was published before protection")
		}
	}
	if _, err := os.Lstat(filepath.Join(inputs.Directory(), "modules/unverified-extra")); !os.IsNotExist(err) {
		t.Fatal("unverified cache residue imported")
	}
	actualMarker, err := os.ReadFile(filepath.Join(inputs.Directory(), "modules/cache/download/example.com/neutral/@v/v1.0.0.ziphash"))
	if err != nil || string(actualMarker) != sum {
		t.Fatal("cache marker did not derive from actual h1")
	}
	for key, value := range map[string]string{"GOFLAGS": "-invalid-ambient", "GOENV": "/private/ignored", "GOWORK": "/private/ignored", "GOTOOLDIR": "/private/ignored", "GIT_EXEC_PATH": "/private/ignored"} {
		t.Setenv(key, value)
	}
	// The original module may change after custody: actual build inputs cannot.
	if err := os.WriteFile(filepath.Join(moduleDir, "neutral.go"), []byte("invalid original after copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	tool, err := inputs.ProtectReferenceTool(t.Context(), parent, request.Role, request.Binary)
	if tool != nil && tool.input != nil {
		inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: request.Role}})
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixed protected reference recipe: %s", time.Since(started))
	identity, _, err := tool.Check(t.Context(), request.Role)
	if err != nil || identity.BuildVCSRevision != fixture.source || identity.Role != request.Role || inputs.Check(t.Context()) != nil {
		t.Fatal("exact protected build did not issue the expected reference identity")
	}
	rows, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.HasPrefix(row.Name(), "t422-go-build-") {
			t.Fatal("joined reference build retained writable scratch")
		}
	}
	// Restore the flag after a native owner change; ctime still invalidates the
	// retained inventory. No per-file content hash is needed at command checks.
	changed, err := t4013.OpenHostImage(filepath.Join(inputs.Directory(), "modules/example.com/neutral@v1.0.0/neutral.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := inputCustodyFlag(changed, false); err != nil {
		t.Fatal(err)
	}
	if err := inputCustodyFlag(changed, true); err != nil {
		t.Fatal(err)
	}
	if err := changed.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if inputs.Check(t.Context()) != ErrExecutionGoBuildCustody {
			t.Fatal("resealed leaf drift was not sticky")
		}
	}
}

func TestExecutionGoBuildCustodyBoundsAndInfo(t *testing.T) {
	for _, test := range []struct{ raw, version string }{
		{`{"Version":"v1.0.1","Time":"2026-01-01T00:00:00Z"}`, "v1.0.0"},
		{`{"Version":"v1.0.0"}`, "v1.0.0"},
		{`{"Version":"v1.0.0","Time":null}`, "v1.0.0"},
		{`{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z","unknown":true}`, "v1.0.0"},
		{`{} {}`, "v1.0.0"},
	} {
		if _, err := normalizeGoBuildModuleInfo([]byte(test.raw), test.version); err != ErrExecutionGoBuildCustody {
			t.Fatalf("invalid module lookup control accepted: %v", err)
		}
	}
	for _, path := range []string{"", "../escape", "/absolute", "a\nline", strings.Repeat("x", 4097)} {
		custody := &ExecutionGoBuildCustody{names: make(map[string]bool)}
		if custody.addName(path) != ErrExecutionGoBuildCustody || len(custody.entries) != 0 {
			t.Fatal("invalid name reserved custody")
		}
	}
	custody := &ExecutionGoBuildCustody{names: make(map[string]bool), inventory: ExecutionGoBuildInventory{Bytes: maxGoBuildBytes - 1}}
	if custody.reserveFile(2) != ErrExecutionGoBuildCustody || custody.reserveFile(maxInputCustodyFileBytes+1) != ErrExecutionGoBuildCustody {
		t.Fatal("file reservation exceeded remaining custody")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if custody.Check(ctx) != ErrExecutionGoBuildCustody || custody.Check(t.Context()) != ErrExecutionGoBuildCustody {
		t.Fatal("closed/empty/canceled state did not fail sticky")
	}
}

func TestExecutionGoBuildTreeRefusesUnboundedOrIrregularInputs(t *testing.T) {
	for _, name := range []string{"symlink", "linked ancestor", "hardlink", "fifo", "oversized", "remaining bytes", "canceled partial"} {
		t.Run(name, func(t *testing.T) {
			custody := newGoBuildTestTree(t)
			_, input := inputCustodyTestFixture(t)
			selected := filepath.Base(input.Path)
			source := filepath.Dir(input.Path)
			ctx := t.Context()
			switch name {
			case "symlink":
				selected = "linked"
				if err := os.Symlink(input.Path, filepath.Join(source, selected)); err != nil {
					t.Fatal(err)
				}
			case "linked ancestor":
				if err := os.Symlink(source, filepath.Join(source, "linked")); err != nil {
					t.Fatal(err)
				}
				selected = "linked/source"
			case "hardlink":
				if err := os.Link(input.Path, filepath.Join(source, "linked")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				selected = "fifo"
				if err := unix.Mkfifo(filepath.Join(source, selected), 0o600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Truncate(input.Path, maxInputCustodyFileBytes+1); err != nil {
					t.Fatal(err)
				}
			case "remaining bytes":
				custody.inventory.Bytes = maxGoBuildBytes - 1
			case "canceled partial":
				if err := os.WriteFile(input.Path, bytes.Repeat([]byte("x"), 128<<10), 0o600); err != nil {
					t.Fatal(err)
				}
				canceled, cancel := context.WithCancel(t.Context())
				defer cancel()
				ctx = &inputCustodyCancelAfterWrite{Context: canceled, cancel: cancel, parent: filepath.Dir(custody.directory), name: "sdk/copied"}
			}
			err := custody.copyTree(ctx, source, selected, "sdk/copied")
			goBuildTestCleanup(t, custody)
			if err != ErrExecutionGoBuildCustody {
				t.Fatalf("unsupported copy accepted: %v", err)
			}
			if name == "canceled partial" {
				info, err := custody.tree.Lstat("sdk/copied")
				if err != nil || info.Size() <= 0 || info.Size() >= 128<<10 {
					t.Fatal("fixture did not retain a genuinely partial bounded copy")
				}
			}
		})
	}
}

func TestExecutionGoBuildTreeProtectsManyFilesWithConstantDescriptors(t *testing.T) {
	custody := newGoBuildTestTree(t)
	for index := 0; index < 256; index++ {
		if err := custody.writeControl(t.Context(), fmt.Sprintf("modules/nested/file-%03d", index), []byte("fixed")); err != nil {
			t.Fatal(err)
		}
	}
	goBuildTestCleanup(t, custody)
	if err := custody.sealDirectories(t.Context()); err != nil {
		t.Fatal(err)
	}
	goBuildTestDescriptors(t, custody)
	for _, entry := range custody.entries {
		if !inputCustodyProtected(entry.info) {
			t.Fatal("file or ancestor was not protected")
		}
	}
	file, err := custody.tree.OpenFile("modules/nested/file-000", os.O_WRONLY, 0)
	if file != nil {
		_ = file.Close()
	}
	if err == nil {
		t.Fatal("protected file admitted a fresh writer")
	}
	if err := custody.Close(); err != nil || custody.Check(t.Context()) != ErrExecutionGoBuildCustody {
		t.Fatal("Close did not release authority")
	}
	if info, err := os.Lstat(filepath.Join(custody.directory, "modules/nested/file-000")); err != nil || !inputCustodyProtected(info) {
		t.Fatal("Close removed or thawed protected custody")
	}
}

func TestExecutionGoBuildConstructorRetainsClosedSourceOnSDKFailure(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	emptySDK, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionGoBuildRequest{Git: git, RepositoryRoot: fixture.root, PlanSourceCommit: fixture.plan,
		IntegratedMainCommit: fixture.integration, SourceCommit: fixture.source, GoRoot: emptySDK, ModuleCache: cache}
	custody, err := ProtectExecutionGoBuildInputs(t.Context(), parent, request)
	goBuildTestCleanup(t, custody)
	if err != ErrExecutionGoBuildCustody || custody == nil || !custody.closed || custody.Check(t.Context()) != ErrExecutionGoBuildCustody {
		t.Fatal("SDK failure did not return closed retained source custody")
	}
	for _, file := range []*os.File{custody.root, custody.parent} {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("constructor failure kept an owned descriptor")
		}
	}
	if _, err := os.Lstat(filepath.Join(custody.directory, "source/.git/HEAD")); err != nil {
		t.Fatal("constructor failure removed generated source custody")
	}
	request.SourceCommit = "HEAD"
	if other, err := ProtectExecutionGoBuildInputs(t.Context(), parent, request); other != nil || err != ErrExecutionGoBuildCustody {
		goBuildTestCleanup(t, other)
		t.Fatal("invalid authority created custody")
	}
}

func TestExecutionGoBuildModuleChecksumRefusal(t *testing.T) {
	for _, altered := range []string{"descriptor", "directory", "version control"} {
		t.Run(altered, func(t *testing.T) {
			custody := newGoBuildTestTree(t)
			cache, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(cache, "example.com/neutral@v1.0.0")
			download := filepath.Join(cache, "cache/download/example.com/neutral/@v")
			for _, path := range []string{directory, download} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			raw := []byte("module example.com/neutral\n\ngo 1.26\n")
			for _, path := range []string{filepath.Join(directory, "go.mod"), filepath.Join(download, "v1.0.0.mod")} {
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			sum, err := dirhash.HashDir(directory, "example.com/neutral@v1.0.0", dirhash.Hash1)
			if err != nil {
				t.Fatal(err)
			}
			modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := custody.writeControl(t.Context(), "source/go.mod", []byte("module github.com/bmeddeb/phebs\n\ngo 1.26\n")); err != nil {
				t.Fatal(err)
			}
			if err := custody.writeControl(t.Context(), "source/go.sum", []byte("example.com/neutral v1.0.0 "+sum+"\nexample.com/neutral v1.0.0/go.mod "+modSum+"\n")); err != nil {
				t.Fatal(err)
			}
			path, content := filepath.Join(download, "v1.0.0.mod"), []byte("altered descriptor")
			switch altered {
			case "directory":
				path = filepath.Join(directory, "go.mod")
			case "version control":
				path, content = filepath.Join(download, "v1.0.0.info"), []byte(`{"Version":"v1.0.1","Time":"2026-01-01T00:00:00Z"}`)
			}
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			err = custody.prepareModules(t.Context(), cache)
			goBuildTestCleanup(t, custody)
			if err != ErrExecutionGoBuildCustody {
				t.Fatalf("source-independent cache alteration accepted: %v", err)
			}
		})
	}
}

func TestExecutionGoBuildModuleInfoPreservesObservedZeroTime(t *testing.T) {
	// Generated-module proxies can explicitly report zero time. The selected
	// Go driver's module lookup accepts it; chronology is not checksum authority.
	const version = "v1.20.0-20250718181942-e35f9b667443.1"
	raw := []byte(`{"Version":"` + version + `","Time":"0001-01-01T00:00:00Z"}`)
	normalized, err := normalizeGoBuildModuleInfo(raw, version)
	if err != nil || string(normalized) != string(raw) {
		t.Fatalf("observed zero metadata time was changed or invented: %s, %v", normalized, err)
	}
}

func TestExecutionGoBuildModuleDescriptorOnlyIgnoresCachedContents(t *testing.T) {
	for _, cached := range []string{"absent", "directory", "symlink"} {
		t.Run(cached, func(t *testing.T) {
			custody := newGoBuildTestTree(t)
			defer func() { goBuildTestCleanup(t, custody) }()
			cache, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			const directory = "example.com/neutral@v1.0.0"
			const control = "cache/download/example.com/neutral/@v/v1.0.0"
			if err := os.MkdirAll(filepath.Dir(filepath.Join(cache, control)), 0o700); err != nil {
				t.Fatal(err)
			}
			raw := []byte("module example.com/neutral\n\ngo 1.26\n")
			if err := os.WriteFile(filepath.Join(cache, control+".mod"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			sum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(raw)), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := custody.writeControl(t.Context(), "source/go.mod", []byte("module github.com/bmeddeb/phebs\n\ngo 1.26\n")); err != nil {
				t.Fatal(err)
			}
			if err := custody.writeControl(t.Context(), "source/go.sum", []byte("example.com/neutral v1.0.0/go.mod "+sum+"\n")); err != nil {
				t.Fatal(err)
			}
			switch cached {
			case "directory":
				if err := os.MkdirAll(filepath.Join(cache, directory), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cache, directory, "untrusted.go"), []byte("unverified ambient contents"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Mkdir(filepath.Join(cache, "example.com"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(cache, directory)); err != nil {
					t.Fatal(err)
				}
			}
			if err := custody.prepareModules(t.Context(), cache); err != nil {
				t.Fatal("descriptor-only historical module refused", err)
			}
			if _, err := custody.tree.Lstat(filepath.Join("modules", control+".mod")); err != nil {
				t.Fatal("verified descriptor was not retained", err)
			}
			for _, absent := range []string{directory, control + ".ziphash"} {
				if _, err := custody.tree.Lstat(filepath.Join("modules", absent)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("descriptor-only authority imported module contents or checksum", err)
				}
			}
		})
	}
}

// A unit-only tree owner exercises the bounded copier without executing Go or
// issuing a protected build identity. Public construction uses actual Git/source.
func newGoBuildTestTree(t *testing.T) *ExecutionGoBuildCustody {
	t.Helper()
	parent, _ := inputCustodyTestFixture(t)
	directory, err := os.MkdirTemp(parent, "t422-go-inputs-")
	if err != nil {
		t.Fatal(err)
	}
	root, err := t4013.OpenHostImage(directory)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := os.OpenRoot(directory)
	if err != nil {
		_ = root.Close()
		t.Fatal(err)
	}
	parentFile, err := t4013.OpenHostImage(parent)
	if err != nil {
		_ = root.Close()
		_ = tree.Close()
		t.Fatal(err)
	}
	parentInfo, err := parentFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	volume, err := inputCustodyVolume(root)
	if err != nil {
		t.Fatal(err)
	}
	custody := &ExecutionGoBuildCustody{directory: directory, root: root, tree: tree, parent: parentFile,
		parentInfo: parentInfo, volume: volume, names: make(map[string]bool)}
	if err := custody.addName("."); err != nil {
		t.Fatal(err)
	}
	return custody
}

func goBuildTestDescriptors(t *testing.T, custody *ExecutionGoBuildCustody) {
	t.Helper()
	identities := make(map[[2]uint64]bool, len(custody.entries))
	for _, entry := range custody.entries {
		if entry.info != nil {
			stat := entry.info.Sys().(*syscall.Stat_t)
			identities[[2]uint64{uint64(stat.Dev), stat.Ino}] = entry.path == "."
		}
	}
	directory, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := directory.Readdirnames(-1)
	closeErr := directory.Close()
	if err != nil || closeErr != nil {
		t.Fatal("descriptor names cannot be enumerated")
	}
	rootFDs := 0
	for _, row := range rows {
		fd, err := strconv.Atoi(row)
		var info unix.Stat_t
		if err != nil || unix.Fstat(fd, &info) != nil {
			continue
		}
		isRoot, found := identities[[2]uint64{uint64(info.Dev), info.Ino}]
		if !found {
			continue
		}
		if !isRoot {
			t.Fatal("Go input retained a per-file or per-subdirectory descriptor")
		}
		rootFDs++
		mode, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil || mode&unix.O_ACCMODE != unix.O_RDONLY {
			t.Fatal("Go input descriptor is writable")
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatal("Go input descriptor lacks CLOEXEC")
		}
	}
	// Explicit root, os.Root, and this test's independent cleanup os.Root.
	if rootFDs != 3 {
		t.Fatalf("root descriptor count = %d, want 3 including cleanup", rootFDs)
	}
}

// Test cleanup keeps one independent rooted descriptor and captures identities
// before mutation. It clears only owner immutable flags on these exact private
// copies, then removes children before parents. No installed/cache tree is used.
func goBuildTestCleanup(t *testing.T, custody *ExecutionGoBuildCustody) {
	t.Helper()
	if custody == nil {
		return
	}
	root, err := os.OpenRoot(custody.Directory())
	if err != nil {
		t.Fatal(err)
	}
	var entries []goBuildEntry
	if err := walkGoBuildTree(t.Context(), root, ".", func(path string, info os.FileInfo) error {
		entries = append(entries, goBuildEntry{path: path, info: info})
		return nil
	}); err != nil {
		_ = root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = custody.Close()
		defer func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		}()
		for _, entry := range entries {
			file, err := root.Open(entry.path)
			if err != nil {
				t.Error(err)
				return
			}
			info, statErr := file.Stat()
			if statErr != nil || !os.SameFile(info, entry.info) || inputCustodyFlag(file, false) != nil {
				_ = file.Close()
				t.Error("cleanup copy identity changed")
				return
			}
			if err := file.Close(); err != nil {
				t.Error(err)
				return
			}
		}
		for index := len(entries) - 1; index >= 1; index-- {
			if err := root.Remove(entries[index].path); err != nil {
				t.Error(err)
				return
			}
		}
		if err := os.Remove(custody.Directory()); err != nil {
			t.Error(fmt.Errorf("owned Go build cleanup: %w", err))
		}
	})
}
