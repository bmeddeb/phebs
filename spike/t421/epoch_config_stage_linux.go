//go:build linux

package t421

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// Only fixed flat generated names are owned here. A failed writer retains its
// reservation; cleanup never recursively traverses a replacement directory.
type linuxEpochInputStage struct {
	root  linuxAuthorRoot
	files map[string]os.FileInfo
}

func (stage *linuxEpochInputStage) write(ctx context.Context, inputs []epochGeneratedInput, limit int) error {
	if stage.root.check() != nil || len(stage.files) != 0 || len(inputs) == 0 || len(inputs) > 5 {
		return ErrExecutionEpochConfigs
	}
	for _, input := range inputs {
		if ctx.Err() != nil || !validSanitizedToken(strings.ReplaceAll(input.name, "-", ""), 64) || len(input.raw) == 0 || len(input.raw) > limit || stage.root.check() != nil {
			return ErrExecutionEpochConfigs
		}
		if _, exists := stage.files[input.name]; exists {
			return ErrExecutionEpochConfigs
		}
		stage.files[input.name] = nil
		file, err := os.OpenFile(filepath.Join(stage.root.path, input.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return ErrExecutionEpochConfigs
		}
		written, writeErr := file.Write(input.raw)
		info, statErr := file.Stat()
		closeErr := file.Close()
		stage.files[input.name] = info
		if writeErr != nil || statErr != nil || closeErr != nil || written != len(input.raw) || stage.root.check() != nil {
			return ErrExecutionEpochConfigs
		}
	}
	return nil
}
func (stage *linuxEpochInputStage) remove(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || stage.root.check() != nil {
		return ErrExecutionEpochConfigs
	}
	// A pinned directory makes basename unlink independent of path replacement.
	fd, err := unix.Openat(int(stage.root.file.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrExecutionEpochConfigs
	}
	directory := os.NewFile(uintptr(fd), "epoch-stage-cleanup")
	defer func() { _ = directory.Close() }()
	rows, err := directory.ReadDir(len(stage.files) + 1)
	if err != nil && err != io.EOF || len(rows) != len(stage.files) {
		return ErrExecutionEpochConfigs
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		before := stage.files[row.Name()]
		after, err := linuxEpochStageEntry(fd, row.Name())
		if before == nil || err != nil || !inputCustodySame(before, after) || !after.Mode().IsRegular() || !inputCustodyCopyOwned(after) {
			return ErrExecutionEpochConfigs
		}
		names = append(names, row.Name())
	}
	slices.Sort(names)
	for _, name := range names {
		before := stage.files[name]
		if ctx.Err() != nil || stage.root.check() != nil {
			return ErrExecutionEpochConfigs
		}
		current, err := linuxEpochStageEntry(fd, name)
		if err != nil || !inputCustodySame(before, current) || unix.Unlinkat(fd, name, 0) != nil {
			return ErrExecutionEpochConfigs
		}
		delete(stage.files, name)
	}
	return nil
}

// DirEntry.Info resolves through os.File.Name, which is only a display label
// for this Openat descriptor. Inspect each basename against the held directory.
func linuxEpochStageEntry(parent int, name string) (os.FileInfo, error) {
	fd, err := unix.Openat(parent, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrExecutionEpochConfigs
	}
	file := os.NewFile(uintptr(fd), "epoch-stage-entry")
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		return nil, ErrExecutionEpochConfigs
	}
	return info, nil
}

// This one-time post-copy comparison binds all eight fixed generated payloads
// to the actual immutable tree. Later checks inspect mount identities only.
func (owner *LinuxExecutionEpochConfigCustody) verifyInputs(ctx context.Context, tree *LinuxExecutionInputTree, selection string, inputs []epochGeneratedInput, limit int) error {
	if !linuxEpochTreeReady(tree) || tree.identity.Files != uint64(len(inputs)) || tree.identity.Entries != uint64(len(inputs)+1) || owner.checkStage() != nil {
		return ErrExecutionEpochConfigs
	}
	var total uint64
	for _, input := range inputs {
		total += uint64(len(input.raw))
		path := linuxEpochInputRoute(tree, selection, input.name)
		before, err := os.Lstat(path)
		if ctx.Err() != nil || err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o400 || before.Size() != int64(len(input.raw)) || !inputCustodyCopyOwned(before) {
			return ErrExecutionEpochConfigs
		}
		file, err := os.Open(path)
		if err != nil {
			return ErrExecutionEpochConfigs
		}
		after, statErr := file.Stat()
		raw, readErr := io.ReadAll(executionInputReader{ctx, io.LimitReader(file, int64(limit)+1)})
		closeErr := file.Close()
		if statErr != nil || !inputCustodySame(before, after) || readErr != nil || closeErr != nil || !bytes.Equal(raw, input.raw) || ctx.Err() != nil {
			return ErrExecutionEpochConfigs
		}
	}
	if tree.identity.Bytes != total || tree.checkRoot() != nil {
		return ErrExecutionEpochConfigs
	}
	return nil
}
