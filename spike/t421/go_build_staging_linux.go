//go:build linux

package t421

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// linuxGoBuildStaging owns bounded mutable preparation only. It never supplies
// a protected path or tool identity; H2k construction must independently copy
// these bytes into its read-only tree before any Go command may use them.
type linuxGoBuildStaging struct {
	directory   string
	tree        *os.Root
	names       map[string]os.FileInfo
	pathBytes   int
	inventory   ExecutionGoBuildInventory
	err         error
	observation *executionPreworkObservation
}

func newLinuxGoBuildStaging(directory string) (*linuxGoBuildStaging, error) {
	return newLinuxGoBuildStagingObserved(directory, nil)
}

func newLinuxGoBuildStagingObserved(directory string, observation *executionPreworkObservation) (*linuxGoBuildStaging, error) {
	if !linuxGitPrivateDirectory(directory) {
		return nil, ErrExecutionGoBuildCustody
	}
	tree, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	file, err := tree.Open(".")
	if err != nil {
		_ = observation.close(tree.Close())
		return nil, ErrExecutionGoBuildCustody
	}
	rows, readErr := file.ReadDir(1)
	held, statErr := file.Stat()
	current, pathErr := os.Lstat(directory)
	closeErr := file.Close()
	if readErr != io.EOF || len(rows) != 0 || statErr != nil || pathErr != nil || closeErr != nil || !os.SameFile(held, current) {
		_ = observation.close(tree.Close())
		return nil, ErrExecutionGoBuildCustody
	}
	return &linuxGoBuildStaging{directory: directory, tree: tree, names: map[string]os.FileInfo{".": held},
		pathBytes: len("inputs"), inventory: ExecutionGoBuildInventory{Entries: 1}, observation: observation}, nil
}

func (staging *linuxGoBuildStaging) refuse() error {
	staging.err = ErrExecutionGoBuildCustody
	return staging.err
}

func (staging *linuxGoBuildStaging) addName(path string) error {
	name := "inputs/" + filepath.ToSlash(path)
	if staging.err != nil || len(path) == 0 || path == "." || !filepath.IsLocal(path) || filepath.Clean(path) != path ||
		strings.ContainsAny(path, "\x00\r\n") || len(name) > maxInputCustodyPathBytes || len(name) > maxGoBuildPathBytes-staging.pathBytes ||
		staging.inventory.Entries >= maxGoBuildEntries {
		return staging.refuse()
	}
	if _, exists := staging.names[path]; exists {
		return staging.refuse()
	}
	staging.names[path] = nil // Retain the reservation on partial construction.
	staging.inventory.Entries++
	staging.pathBytes += len(name)
	return nil
}

func (staging *linuxGoBuildStaging) makeDirectory(path string) error {
	if staging.err != nil || !filepath.IsLocal(path) || filepath.Clean(path) != path {
		return staging.refuse()
	}
	current := ""
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if before, exists := staging.names[current]; exists {
			after, err := staging.tree.Lstat(current)
			if err != nil || before == nil || !after.IsDir() || !os.SameFile(before, after) ||
				after.Mode().Perm() != 0o700 || !inputCustodyCopyOwned(after) {
				return staging.refuse()
			}
			continue
		}
		if staging.addName(current) != nil || staging.tree.Mkdir(current, 0o700) != nil {
			return staging.refuse()
		}
		info, err := staging.tree.Lstat(current)
		if err != nil || !inputCustodyCopyOwned(info) {
			return staging.refuse()
		}
		staging.names[current] = info
	}
	return nil
}

func (staging *linuxGoBuildStaging) reserveFile(size int64) error {
	if staging.err != nil || size < 0 || size > maxInputCustodyFileBytes || size > maxGoBuildBytes-staging.inventory.Bytes {
		return staging.refuse()
	}
	staging.inventory.Bytes += size
	staging.inventory.Files++
	return nil
}

func (staging *linuxGoBuildStaging) copyTree(ctx context.Context, sourceRoot, selection, destination string) (retErr error) {
	if ctx == nil || ctx.Err() != nil || staging.err != nil || !executionGitAbsolutePath(sourceRoot) || !filepath.IsLocal(selection) || filepath.Clean(selection) != selection {
		return staging.refuse()
	}
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return staging.refuse()
	}
	defer func() {
		if source.Close() != nil || retErr != nil {
			retErr = staging.refuse()
		}
	}()
	return walkGoBuildTree(ctx, source, selection, func(path string, info os.FileInfo) error {
		relative, err := filepath.Rel(selection, path)
		if err != nil {
			return staging.refuse()
		}
		name := filepath.Join(destination, relative)
		if info.IsDir() {
			return staging.makeDirectory(name)
		}
		if staging.makeDirectory(filepath.Dir(name)) != nil || staging.addName(name) != nil || staging.reserveFile(info.Size()) != nil {
			return staging.refuse()
		}
		file, size, err := copyExecutionInput(ctx, staging.tree, ExecutionInputCopy{
			Name: name, Path: filepath.Join(sourceRoot, path), Executable: info.Mode().Perm()&0o111 != 0,
		}, info.Size())
		if err != nil || size != info.Size() {
			if file != nil {
				_ = file.Close()
			}
			return staging.refuse()
		}
		staging.names[name], err = file.Stat()
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return staging.refuse()
		}
		return nil
	})
}

func (staging *linuxGoBuildStaging) writeControl(ctx context.Context, path string, raw []byte) error {
	if ctx == nil || ctx.Err() != nil || len(raw) > maxReferenceGoSumBytes || staging.err != nil ||
		staging.makeDirectory(filepath.Dir(path)) != nil || staging.addName(path) != nil || staging.reserveFile(int64(len(raw))) != nil {
		return staging.refuse()
	}
	file, err := staging.tree.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return staging.refuse()
	}
	n, writeErr := file.Write(raw)
	modeErr, syncErr := file.Chmod(0o400), file.Sync()
	info, statErr := file.Stat()
	closeErr := file.Close()
	if writeErr != nil || n != len(raw) || modeErr != nil || syncErr != nil || statErr != nil || closeErr != nil {
		return staging.refuse()
	}
	staging.names[path] = info
	return nil
}

func (staging *linuxGoBuildStaging) prepareModules(ctx context.Context, cache string) error {
	if ctx == nil || ctx.Err() != nil || staging.err != nil {
		return staging.refuse()
	}
	if prepareGoBuildModules(ctx, staging.tree, staging, cache) != nil {
		return staging.refuse()
	}
	return nil
}

// Call only after every writer/reader has joined. Unknown, replaced or partially
// recorded entries retain the exact private prefix; cleanup is nonrecursive.
func (staging *linuxGoBuildStaging) removeContents(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || staging.err != nil || !linuxGitPrivateDirectory(staging.directory) {
		return ErrExecutionGoBuildCustody
	}
	held, err := staging.tree.Stat(".")
	linked, pathErr := os.Lstat(staging.directory)
	if err != nil || pathErr != nil || !os.SameFile(staging.names["."], held) || !os.SameFile(held, linked) {
		return ErrExecutionGoBuildCustody
	}
	var paths []string
	err = walkGoBuildTree(ctx, staging.tree, ".", func(path string, current os.FileInfo) error {
		before, exists := staging.names[path]
		if !exists || before == nil || !os.SameFile(before, current) || before.Mode() != current.Mode() ||
			!current.IsDir() && !inputCustodySame(before, current) {
			return ErrExecutionGoBuildCustody
		}
		if path != "." {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil || len(paths)+1 != len(staging.names) {
		return ErrExecutionGoBuildCustody
	}
	slices.SortFunc(paths, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(b, a)
	})
	for _, path := range paths {
		if staging.removeEntry(ctx, path) != nil {
			return ErrExecutionGoBuildCustody
		}
	}
	return nil
}

func (staging *linuxGoBuildStaging) checkRoot() error {
	held, err := staging.tree.Stat(".")
	linked, pathErr := os.Lstat(staging.directory)
	if err != nil || pathErr != nil || !linuxGitPrivateDirectory(staging.directory) || !os.SameFile(staging.names["."], held) || !os.SameFile(held, linked) {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

// Pin each parent before unlinking a basename. A renamed directory followed by
// an internal symlink must not redirect cleanup to a still-matching file inode.
func (staging *linuxGoBuildStaging) removeEntry(ctx context.Context, path string) (retErr error) {
	before := staging.names[path]
	if ctx == nil || ctx.Err() != nil || before == nil || path == "." || staging.checkRoot() != nil {
		return ErrExecutionGoBuildCustody
	}
	parent, err := staging.tree.OpenRoot(".")
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	defer func() {
		if staging.observation.close(parent.Close()) != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	currentPath := ""
	if directory := filepath.Dir(path); directory != "." {
		for _, part := range strings.Split(directory, string(filepath.Separator)) {
			currentPath = filepath.Join(currentPath, part)
			expected := staging.names[currentPath]
			linked, err := parent.Lstat(part)
			if ctx.Err() != nil || err != nil || expected == nil || !linked.IsDir() || !os.SameFile(expected, linked) || expected.Mode() != linked.Mode() {
				return ErrExecutionGoBuildCustody
			}
			next, err := parent.OpenRoot(part)
			if err != nil {
				return ErrExecutionGoBuildCustody
			}
			held, statErr := next.Stat(".")
			if closeErr := staging.observation.close(parent.Close()); closeErr != nil || statErr != nil || !os.SameFile(expected, held) {
				parent = next
				return ErrExecutionGoBuildCustody
			}
			parent = next
		}
	}
	// Recheck the complete named ancestry after pinning and immediately before
	// removing only from the held parent; never follow a replacement prefix.
	for ancestor := filepath.Dir(path); ancestor != "."; ancestor = filepath.Dir(ancestor) {
		linked, err := staging.tree.Lstat(ancestor)
		expected := staging.names[ancestor]
		if err != nil || expected == nil || !linked.IsDir() || !os.SameFile(expected, linked) || expected.Mode() != linked.Mode() {
			return ErrExecutionGoBuildCustody
		}
	}
	current, err := parent.Lstat(filepath.Base(path))
	if ctx.Err() != nil || err != nil || !os.SameFile(before, current) || before.Mode() != current.Mode() ||
		!current.IsDir() && !inputCustodySame(before, current) || staging.checkRoot() != nil || parent.Remove(filepath.Base(path)) != nil {
		return ErrExecutionGoBuildCustody
	}
	delete(staging.names, path)
	return nil
}
