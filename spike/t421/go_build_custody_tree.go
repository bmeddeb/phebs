package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func (custody *ExecutionGoBuildCustody) addName(path string) error {
	if len(path) == 0 || len(path) > maxInputCustodyPathBytes || !filepath.IsLocal(path) ||
		filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") || custody.names[path] ||
		len(custody.entries) >= maxGoBuildEntries || len(path) > maxGoBuildPathBytes-custody.pathBytes {
		return ErrExecutionGoBuildCustody
	}
	custody.pathBytes += len(path)
	custody.names[path] = true
	custody.entries = append(custody.entries, goBuildEntry{path: path})
	custody.inventory.Entries++
	return nil
}

func (custody *ExecutionGoBuildCustody) makeDirectory(path string) error {
	current := ""
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if custody.names[current] {
			info, err := custody.tree.Lstat(current)
			if err != nil || !info.IsDir() {
				return ErrExecutionGoBuildCustody
			}
			continue
		}
		if err := custody.addName(current); err != nil {
			return err
		}
		if err := custody.tree.Mkdir(current, 0o700); err != nil {
			return ErrExecutionGoBuildCustody
		}
	}
	return nil
}

func (custody *ExecutionGoBuildCustody) reserveFile(size int64) error {
	if size < 0 || size > maxInputCustodyFileBytes || size > maxGoBuildBytes-custody.inventory.Bytes {
		return ErrExecutionGoBuildCustody
	}
	// A failed copy retains at most the reservation, including partial files.
	custody.inventory.Bytes += size
	custody.inventory.Files++
	return nil
}

func (custody *ExecutionGoBuildCustody) copyTree(ctx context.Context, sourceRoot, selection, destination string) (retErr error) {
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	defer func() {
		if source.Close() != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	return walkGoBuildTree(ctx, source, selection, func(path string, info os.FileInfo) error {
		relative, err := filepath.Rel(selection, path)
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		name := filepath.Join(destination, relative)
		if info.IsDir() {
			return custody.makeDirectory(name)
		}
		if err := custody.makeDirectory(filepath.Dir(name)); err != nil {
			return err
		}
		if err := custody.addName(name); err != nil {
			return err
		}
		if err := custody.reserveFile(info.Size()); err != nil {
			return err
		}
		file, size, err := copyExecutionInput(ctx, custody.tree, ExecutionInputCopy{
			Name: name, Path: filepath.Join(sourceRoot, path), Executable: info.Mode().Perm()&0o111 != 0,
		}, info.Size())
		if err != nil || size != info.Size() {
			return ErrExecutionGoBuildCustody
		}
		return custody.protectFile(ctx, len(custody.entries)-1, file)
	})
}

// walkGoBuildTree is intentionally private to this one input recipe. A bounded
// breadth-first queue and 128-row reads avoid recursive/whole-directory reads.
func walkGoBuildTree(ctx context.Context, root *os.Root, selection string, visit func(string, os.FileInfo) error) error {
	queue := []string{selection}
	count, pathBytes := 0, len(selection)
	for cursor := 0; cursor < len(queue); cursor++ {
		path := queue[cursor]
		info, err := referenceModuleFileInfo(root, path)
		if ctx.Err() != nil || err != nil || !inputCustodyOwned(info) ||
			len(path) > maxInputCustodyPathBytes || strings.ContainsAny(path, "\x00\r\n") {
			return ErrExecutionGoBuildCustody
		}
		count++
		if count > maxGoBuildEntries || pathBytes > maxGoBuildPathBytes || visit(path, info) != nil {
			return ErrExecutionGoBuildCustody
		}
		if !info.IsDir() {
			continue
		}
		directory, err := root.Open(path)
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		readErr := func() error {
			opened, err := directory.Stat()
			if err != nil || !inputCustodySame(info, opened) {
				return ErrExecutionGoBuildCustody
			}
			for {
				rows, err := directory.ReadDir(128)
				if ctx.Err() != nil || err != nil && !errors.Is(err, io.EOF) || len(rows) > maxGoBuildEntries-len(queue) {
					return ErrExecutionGoBuildCustody
				}
				for _, row := range rows {
					child := filepath.Join(path, row.Name())
					if len(child) > maxInputCustodyPathBytes || len(child) > maxGoBuildPathBytes-pathBytes {
						return ErrExecutionGoBuildCustody
					}
					pathBytes += len(child)
					queue = append(queue, child)
				}
				if errors.Is(err, io.EOF) {
					break
				}
			}
			after, statErr := directory.Stat()
			current, pathErr := root.Lstat(path)
			if statErr != nil || pathErr != nil || !inputCustodySame(info, after) || !inputCustodySame(after, current) {
				return ErrExecutionGoBuildCustody
			}
			return nil
		}()
		if closeErr := directory.Close(); readErr != nil || closeErr != nil {
			return ErrExecutionGoBuildCustody
		}
	}
	return nil
}

func (custody *ExecutionGoBuildCustody) protectFile(ctx context.Context, index int, file *os.File) (retErr error) {
	defer func() {
		if file.Close() != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	if ctx.Err() != nil || inputCustodyFlag(file, true) != nil {
		return ErrExecutionGoBuildCustody
	}
	info, err := file.Stat()
	if err != nil || !inputCustodyProtected(info) || !info.Mode().IsRegular() {
		return ErrExecutionGoBuildCustody
	}
	digest := sha256.New()
	size, hashErr := io.Copy(digest, executionInputReader{ctx, io.NewSectionReader(file, 0, info.Size())})
	after, statErr := file.Stat()
	current, pathErr := custody.tree.Lstat(custody.entries[index].path)
	if hashErr != nil || size != info.Size() || statErr != nil || pathErr != nil ||
		!inputCustodySame(info, after) || !inputCustodySame(info, current) {
		return ErrExecutionGoBuildCustody
	}
	custody.entries[index].info = info
	custody.entries[index].digest = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	return nil
}

func (custody *ExecutionGoBuildCustody) sealDirectories(ctx context.Context) error {
	// Parents were added before their children; reverse order seals bottom-up.
	for index := len(custody.entries) - 1; index >= 0; index-- {
		entry := &custody.entries[index]
		if entry.info != nil {
			continue
		}
		file, err := custody.tree.Open(entry.path)
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		info, statErr := file.Stat()
		if ctx.Err() != nil || statErr != nil || !info.IsDir() || !inputCustodyOwned(info) ||
			file.Sync() != nil || inputCustodyFlag(file, true) != nil {
			_ = file.Close()
			return ErrExecutionGoBuildCustody
		}
		entry.info, statErr = file.Stat()
		if closeErr := file.Close(); statErr != nil || closeErr != nil || !inputCustodyProtected(entry.info) {
			return ErrExecutionGoBuildCustody
		}
	}
	return nil
}

func (custody *ExecutionGoBuildCustody) prepareSource(ctx context.Context, origin executionCheckoutInspector, commit string) error {
	raw, err := origin.run(ctx, maxCheckoutInventoryBytes, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	entries, err := executionCheckoutEntries(raw)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	// The fixed SHA-1 loose-object recipe writes a worktree, zlib blobs, one
	// index and trees, one <=4 MiB commit, and constant init controls. Twice the
	// raw bytes plus eight path bytes and 512 bytes/entry conservatively reserves
	// compression/header/index/tree overhead; 8 MiB covers commit+init controls.
	reservation := int64(8 << 20)
	var rawBytes int64
	pathReservation := 32 << 10
	directories := map[string]bool{}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(origin.root, entry.path))
		if ctx.Err() != nil || err != nil || !info.Mode().IsRegular() || info.Size() < 0 ||
			info.Size() > maxInputCustodyFileBytes || len(entry.path) > maxInputCustodyPathBytes {
			return ErrExecutionGoBuildCustody
		}
		cost := 2*info.Size() + 8*int64(len(entry.path)) + 512
		if cost > maxGoBuildBytes-reservation {
			return ErrExecutionGoBuildCustody
		}
		reservation += cost
		rawBytes += info.Size()
		pathReservation += len(entry.path) + 80
		for directory := filepath.Dir(entry.path); directory != "."; directory = filepath.Dir(directory) {
			if !directories[directory] {
				directories[directory] = true
				pathReservation += len(directory) + 80
			}
			if 2*len(entries)+2*len(directories)+300 > maxGoBuildEntries-len(custody.entries) || pathReservation > maxGoBuildPathBytes-custody.pathBytes {
				return ErrExecutionGoBuildCustody
			}
		}
	}
	if reservation > maxGoBuildBytes-custody.inventory.Bytes || 2*len(entries)+2*len(directories)+300 > maxGoBuildEntries-len(custody.entries) ||
		pathReservation > maxGoBuildPathBytes-custody.pathBytes {
		return ErrExecutionGoBuildCustody
	}
	custody.inventory.Bytes += reservation
	reference, err := createExecutionReferenceSourceBounded(ctx, origin, commit, filepath.Join(custody.directory, "source"), rawBytes)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	custody.reference = reference
	var actual int64
	err = walkGoBuildTree(ctx, custody.tree, "source", func(path string, info os.FileInfo) error {
		if err := custody.addName(path); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if info.Size() < 0 || info.Size() > maxInputCustodyFileBytes || info.Size() > reservation-actual {
			return ErrExecutionGoBuildCustody
		}
		actual += info.Size()
		custody.inventory.Files++
		file, err := t4013.OpenHostImage(filepath.Join(custody.directory, path))
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		return custody.protectFile(ctx, len(custody.entries)-1, file)
	})
	if err != nil {
		return err
	}
	custody.inventory.Bytes -= reservation - actual
	return nil
}
