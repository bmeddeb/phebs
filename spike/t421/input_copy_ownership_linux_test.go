//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

type linuxInputCopyInfo struct {
	os.FileInfo
	metadata any
}

func (info linuxInputCopyInfo) Sys() any { return info.metadata }

func TestLinuxInputCopyOwnershipRequiresNativeMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !inputCustodyCopyOwned(info) {
		t.Fatal("actual owned regular source was not observed", err)
	}
	var nilStat *syscall.Stat_t
	for _, test := range []struct {
		name string
		info os.FileInfo
	}{
		{"nil info", nil},
		{"nil metadata", linuxInputCopyInfo{info, nil}},
		{"typed nil metadata", linuxInputCopyInfo{info, nilStat}},
		{"foreign metadata", linuxInputCopyInfo{info, &struct{}{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if inputCustodyCopyOwned(test.info) {
				t.Fatal("unavailable or foreign native metadata accepted")
			}
		})
	}
}

func TestLinuxInputCopyMutableVerifiedPath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"source", "source/bin", "source/empty", "destination"} {
		if err := os.Mkdir(filepath.Join(base, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"source/README": "neutral input\n", "source/bin/tool": "neutral executable bytes\n"}
	before := make(map[string]os.FileInfo, len(files))
	for path, content := range files {
		mode := os.FileMode(0o600)
		if path == "source/bin/tool" {
			mode = 0o700
		}
		if err := os.WriteFile(filepath.Join(base, path), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		before[path], err = os.Lstat(filepath.Join(base, path))
		if err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	destination, err := os.OpenRoot(filepath.Join(base, "destination"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = destination.Close() }()
	var visited []string
	err = walkGoBuildTree(t.Context(), source, "source", func(path string, info os.FileInfo) (retErr error) {
		visited = append(visited, path)
		if info.IsDir() {
			return destination.Mkdir(path, 0o700)
		}
		file, size, err := copyExecutionInput(t.Context(), destination, ExecutionInputCopy{
			Name: path, Path: filepath.Join(base, path), Executable: info.Mode().Perm()&0o111 != 0,
		}, info.Size())
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, file.Close()) }()
		if size != int64(len(files[path])) {
			return errors.New("mutable copy has wrong byte count")
		}
		if _, err := file.WriteAt([]byte("x"), 0); err == nil {
			return errors.New("copy returned a writable descriptor")
		}
		return nil
	})
	if err != nil {
		t.Fatal("actual mutable verified-copy path refused", err)
	}
	slices.Sort(visited)
	if !slices.Equal(visited, []string{"source", "source/README", "source/bin", "source/bin/tool", "source/empty"}) {
		t.Fatalf("copy census: %q", visited)
	}
	for path, content := range files {
		raw, err := os.ReadFile(filepath.Join(base, "destination", path))
		if err != nil || string(raw) != content {
			t.Fatal("copy bytes differ", path, err)
		}
		copied, err := os.Lstat(filepath.Join(base, "destination", path))
		wantMode := os.FileMode(0o400)
		if before[path].Mode().Perm()&0o111 != 0 {
			wantMode = 0o500
		}
		current, sourceErr := os.Lstat(filepath.Join(base, path))
		if err != nil || copied.Mode().Perm() != wantMode || sourceErr != nil ||
			!inputCustodySame(before[path], current) || os.SameFile(current, copied) {
			t.Fatal("copy changed source or target metadata", path, err, sourceErr)
		}
	}
	// Ordinary copy ownership is not immutability: the owner can still change
	// the copy, and neither the source nor an execution authority is modified.
	copyPath := filepath.Join(base, "destination/source/README")
	if err := os.Chmod(copyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte("copy changed\n"), 0o600); err != nil {
		t.Fatal("mutable copy was misrepresented as protected", err)
	}
	raw, err := os.ReadFile(filepath.Join(base, "source/README"))
	if err != nil || string(raw) != files["source/README"] {
		t.Fatal("copy mutation changed source", err)
	}
}

func TestLinuxInputCopyWalkRefusesLinkedAndCanceledSources(t *testing.T) {
	for _, kind := range []string{"hardlink", "symlink", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			if err := os.Mkdir(filepath.Join(base, "source"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "original"), []byte("neutral\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := os.Link
			if kind == "symlink" {
				link = os.Symlink
			}
			if kind != "canceled" {
				if err := link(filepath.Join(base, "original"), filepath.Join(base, "source/linked")); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "canceled" {
				cancel()
			}
			var visits int
			err = walkGoBuildTree(ctx, root, "source", func(path string, _ os.FileInfo) error {
				visits++
				if path != "source" {
					t.Fatal("linked source reached the copy visitor")
				}
				return nil
			})
			if !errors.Is(err, ErrExecutionGoBuildCustody) || visits > 1 || kind == "canceled" && visits != 0 {
				t.Fatal("linked or canceled source was accepted", err, visits)
			}
		})
	}
}

func TestLinuxInputCopyOwnershipNeverAdmitsProtectedCustody(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "source")
	raw := []byte("mutable input\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(parent)
	if err != nil || !inputCustodyCopyOwned(info) || inputCustodyOwned(info) || inputCustodyProtected(info) || executionGitPrivateDirectory(parent) {
		t.Fatal("copy ownership promoted to protected-directory authority", err)
	}
	input := ExecutionInputCopy{Name: "input", Path: path, SHA256: SHA256(raw)}
	if custody, err := ProtectExecutionInputs(t.Context(), parent, []ExecutionInputCopy{input}); custody != nil || !errors.Is(err, ErrExecutionInputCustody) {
		t.Fatal("Linux path custody bypassed its pre-copy refusal", err)
	}
	if custody, err := ProtectExecutionGoBuildInputs(t.Context(), parent, ExecutionGoBuildRequest{Git: &ExecutionGitCustody{}}); custody != nil || !errors.Is(err, ErrExecutionGoBuildCustody) {
		t.Fatal("Linux Go build custody bypassed its platform refusal", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	for _, protected := range []bool{true, false} {
		if inputCustodyFlag(file, protected) == nil {
			t.Fatal("Linux immutable-flag operation was admitted")
		}
	}
	if volume, err := inputCustodyVolume(file); err == nil || volume != [2]int32{} {
		t.Fatal("Linux volume custody was admitted", err)
	}
	rows, err := os.ReadDir(parent)
	if err != nil || len(rows) != 0 {
		t.Fatal("refused constructor left a root or copied bytes", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("protection refusal changed caller source", err)
	}
}
