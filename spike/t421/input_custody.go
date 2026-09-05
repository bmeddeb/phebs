package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/spike/t4013"
)

const (
	// ponytail: flat 64-file custody; trees require their own admitted input recipe.
	maxInputCustodyFiles     = 64
	maxInputCustodyFileBytes = 256 << 20
	maxInputCustodyBytes     = 2 << 30
	maxInputCustodyPathBytes = 4096
)

// ErrExecutionInputCustody contains no private path, content or native error.
var ErrExecutionInputCustody = errors.New("execution input custody unavailable or changed")

// ExecutionInputCopy selects bytes already checked by the owning verifier.
// A matching digest proves the copy, not provenance, a tool role or admission.
type ExecutionInputCopy struct {
	Name       string
	Path       string
	SHA256     string
	Executable bool
}

type protectedExecutionInput struct {
	file *os.File
	info os.FileInfo
}

// ExecutionInputCustody owns only new, flat private copies and read descriptors.
// Darwin's owner-set immutable flag protects their bytes and directory entries.
// It does not protect against hostile same-user flag clearing, ancestor/mount
// replacement or code injection. The caller retains the trusted-host boundary.
// Mode/owner checks are not an ACL audit; external writers during construction
// must be excluded by that host boundary, not inferred absent from these checks.
// No caller-owned file is flagged, and no runtime or private admission is enabled.
type ExecutionInputCustody struct {
	mu         sync.Mutex
	directory  string
	root       *os.File
	tree       *os.Root
	rootInfo   os.FileInfo
	parent     *os.File
	parentInfo os.FileInfo
	volume     [2]int32
	inputs     map[string]protectedExecutionInput
	closed     bool
	err        error
}

// ProtectExecutionInputs copies at most 64 direct images/fixed inputs, each at
// most 256 MiB and together at most 2 GiB, into a new private child of parent.
// These local ceilings are not frozen launcher limits. Trees, native helpers,
// dynamic libraries, mutable repositories and later archives are not admitted.
//
// The exclusive writer is closed BEFORE protection and hashing: Darwin permits
// a previously opened writable descriptor to modify an immutable file. Only
// read-only descriptors survive publication. No pathname/stat cache substitutes
// for this ownership step. Same-user interference during construction remains
// outside the trusted single-owner boundary.
//
// After directory creation, failure returns non-nil custody with closed FDs and
// retained private copies. The owner must retain/dispose that exact custody; no
// implicit thaw or source deletion occurs. Close only releases descriptors, so
// a volume owner can drain children, close custody, then detach before removal.
func ProtectExecutionInputs(ctx context.Context, parent string, copies []ExecutionInputCopy) (_ *ExecutionInputCustody, retErr error) {
	if ctx == nil || ctx.Err() != nil || len(copies) == 0 || len(copies) > maxInputCustodyFiles ||
		len(parent) > maxInputCustodyPathBytes || !filepath.IsAbs(parent) || filepath.Clean(parent) != parent || parent == string(filepath.Separator) {
		return nil, ErrExecutionInputCustody
	}
	copies = slices.Clone(copies)
	names := make(map[string]bool, len(copies))
	for _, input := range copies {
		if len(input.Name) > 64 || !validSanitizedToken(strings.ReplaceAll(input.Name, "-", ""), 64) ||
			names[input.Name] || !validExecutionSHA256(input.SHA256) || len(input.Path) > maxInputCustodyPathBytes ||
			!filepath.IsAbs(input.Path) || filepath.Clean(input.Path) != input.Path {
			return nil, ErrExecutionInputCustody
		}
		names[input.Name] = true
	}
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil || canonical != parent {
		return nil, ErrExecutionInputCustody
	}
	parentFile, err := t4013.OpenHostImage(parent)
	if err != nil {
		return nil, ErrExecutionInputCustody
	}
	parentInfo, err := parentFile.Stat()
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode().Perm() != 0o700 || !inputCustodyOwned(parentInfo) {
		_ = parentFile.Close()
		return nil, ErrExecutionInputCustody
	}
	directory, err := os.MkdirTemp(parent, "t422-inputs-")
	if err != nil {
		_ = parentFile.Close()
		return nil, ErrExecutionInputCustody
	}
	custody := &ExecutionInputCustody{directory: directory, parent: parentFile, parentInfo: parentInfo,
		inputs: make(map[string]protectedExecutionInput, len(copies))}
	defer func() {
		if retErr != nil {
			custody.err = ErrExecutionInputCustody
			_ = custody.Close()
			retErr = ErrExecutionInputCustody
		}
	}()
	custody.root, err = t4013.OpenHostImage(directory)
	if err != nil {
		return custody, ErrExecutionInputCustody
	}
	custody.rootInfo, err = custody.root.Stat()
	if err != nil || !custody.rootInfo.IsDir() || !inputCustodyOwned(custody.rootInfo) {
		return custody, ErrExecutionInputCustody
	}
	custody.tree, err = os.OpenRoot(directory)
	if err != nil {
		return custody, ErrExecutionInputCustody
	}
	treeInfo, err := custody.tree.Stat(".")
	if err != nil || !os.SameFile(custody.rootInfo, treeInfo) {
		return custody, ErrExecutionInputCustody
	}
	custody.volume, err = inputCustodyVolume(custody.root)
	if err != nil {
		return custody, ErrExecutionInputCustody
	}
	var total int64
	for _, input := range copies {
		file, size, err := copyExecutionInput(ctx, custody.tree, input, maxInputCustodyBytes-total)
		if err != nil {
			return custody, ErrExecutionInputCustody
		}
		total += size
		custody.inputs[input.Name] = protectedExecutionInput{file: file}
		if ctx.Err() != nil || inputCustodyFlag(file, true) != nil {
			return custody, ErrExecutionInputCustody
		}
	}
	if ctx.Err() != nil || custody.root.Sync() != nil {
		return custody, ErrExecutionInputCustody
	}
	if ctx.Err() != nil || inputCustodyFlag(custody.root, true) != nil {
		return custody, ErrExecutionInputCustody
	}
	custody.rootInfo, err = custody.root.Stat()
	if err != nil || !inputCustodyProtected(custody.rootInfo) {
		return custody, ErrExecutionInputCustody
	}
	for _, input := range copies {
		entry := custody.inputs[input.Name]
		entry.info, err = entry.file.Stat()
		if err != nil || !inputCustodyProtected(entry.info) {
			return custody, ErrExecutionInputCustody
		}
		custody.inputs[input.Name] = entry
		hash := sha256.New()
		if size, err := io.Copy(hash, executionInputReader{ctx, io.NewSectionReader(entry.file, 0, entry.info.Size())}); err != nil || size != entry.info.Size() ||
			"sha256:"+hex.EncodeToString(hash.Sum(nil)) != input.SHA256 {
			return custody, ErrExecutionInputCustody
		}
		if _, err := custody.Check(ctx, input.Name); err != nil {
			return custody, ErrExecutionInputCustody
		}
	}
	return custody, nil
}

func copyExecutionInput(ctx context.Context, root *os.Root, input ExecutionInputCopy, remaining int64) (result *os.File, size int64, retErr error) {
	canonical, err := filepath.EvalSymlinks(input.Path)
	if err != nil || canonical != input.Path || ctx.Err() != nil {
		return nil, 0, ErrExecutionInputCustody
	}
	source, err := t4013.OpenHostImage(input.Path)
	if err != nil {
		return nil, 0, ErrExecutionInputCustody
	}
	defer func() {
		if source.Close() != nil {
			if result != nil {
				_ = result.Close()
				result = nil
			}
			retErr = ErrExecutionInputCustody
		}
	}()
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || before.Size() < 0 || before.Size() > maxInputCustodyFileBytes ||
		before.Size() > remaining || (before.Mode().Perm()&0o111 != 0) != input.Executable || input.Executable && before.Size() == 0 {
		return nil, 0, ErrExecutionInputCustody
	}
	writer, err := root.OpenFile(input.Name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, 0, ErrExecutionInputCustody
	}
	written, copyErr := io.Copy(writer, executionInputReader{ctx, io.LimitReader(source, before.Size())})
	var overflow [1]byte
	_, tailErr := io.ReadFull(executionInputReader{ctx, source}, overflow[:])
	mode := os.FileMode(0o400)
	if input.Executable {
		mode = 0o500
	}
	chmodErr, syncErr := writer.Chmod(mode), writer.Sync()
	writtenInfo, writtenStatErr := writer.Stat()
	closeErr := writer.Close()
	after, statErr := source.Stat()
	pathInfo, pathErr := os.Lstat(input.Path)
	if copyErr != nil || !errors.Is(tailErr, io.EOF) || chmodErr != nil || syncErr != nil || writtenStatErr != nil || closeErr != nil || statErr != nil || pathErr != nil ||
		written != before.Size() || !inputCustodySame(before, after) || !inputCustodySame(after, pathInfo) || ctx.Err() != nil {
		return nil, 0, ErrExecutionInputCustody
	}
	file, err := root.Open(input.Name)
	if err != nil {
		return nil, 0, ErrExecutionInputCustody
	}
	openedInfo, err := file.Stat()
	if err != nil || !inputCustodySame(writtenInfo, openedInfo) || !inputCustodyOwned(openedInfo) {
		_ = file.Close()
		return nil, 0, ErrExecutionInputCustody
	}
	return file, written, nil
}

type executionInputReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader executionInputReader) Read(buffer []byte) (int, error) {
	if reader.ctx.Err() != nil {
		return 0, ErrExecutionInputCustody
	}
	return reader.reader.Read(buffer)
}

// Directory is private custody location information, never returned evidence.
func (custody *ExecutionInputCustody) Directory() string { return custody.directory }

// Check returns the exact private path only while its kernel protection and
// held-object/path/mount identities still match. It hashes no file or tree.
// The owner must also validate argv/environment/input semantics before dispatch;
// this method supplies neither permission nor a complete command admission.
func (custody *ExecutionInputCustody) Check(ctx context.Context, name string) (string, error) {
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || custody.closed || custody.err != nil || len(name) > 64 {
		custody.err = ErrExecutionInputCustody
		return "", custody.err
	}
	entry, exists := custody.inputs[name]
	if !exists || entry.info == nil {
		custody.err = ErrExecutionInputCustody
		return "", custody.err
	}
	path := filepath.Join(custody.directory, name)
	canonical, canonicalErr := filepath.EvalSymlinks(path)
	parentPath, parentErr := os.Lstat(filepath.Dir(custody.directory))
	rootPath, rootErr := os.Lstat(custody.directory)
	rootInfo, rootStatErr := custody.root.Stat()
	filePath, fileErr := os.Lstat(path)
	fileInfo, fileStatErr := entry.file.Stat()
	volume, volumeErr := inputCustodyVolume(custody.root)
	if canonicalErr != nil || canonical != path || parentErr != nil || !os.SameFile(custody.parentInfo, parentPath) ||
		!inputCustodyOwned(parentPath) || parentPath.Mode().Perm() != 0o700 ||
		rootErr != nil || rootStatErr != nil || !inputCustodySame(custody.rootInfo, rootInfo) || !inputCustodySame(rootInfo, rootPath) ||
		fileErr != nil || fileStatErr != nil || !inputCustodySame(entry.info, fileInfo) || !inputCustodySame(fileInfo, filePath) ||
		!inputCustodyProtected(rootInfo) || !inputCustodyProtected(fileInfo) || volumeErr != nil || volume != custody.volume || ctx.Err() != nil {
		custody.err = ErrExecutionInputCustody
		return "", custody.err
	}
	return path, nil
}

// Close releases read descriptors only. Protected bytes remain in custody and
// are never thawed or removed here, including after drift/failure. This is not
// an owned-child join, lease drain, detach or source-removal certificate.
func (custody *ExecutionInputCustody) Close() error {
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if !custody.closed {
		custody.closed = true
		for _, entry := range custody.inputs {
			if entry.file.Close() != nil {
				custody.err = ErrExecutionInputCustody
			}
		}
		if custody.tree != nil && custody.tree.Close() != nil {
			custody.err = ErrExecutionInputCustody
		}
		for _, file := range []*os.File{custody.root, custody.parent} {
			if file != nil && file.Close() != nil {
				custody.err = ErrExecutionInputCustody
			}
		}
	}
	return custody.err
}
