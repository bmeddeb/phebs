// Package typedworkspace copies prehydration bytes into private immutable Linux custody.
// Other operating systems refuse; mode bits alone cannot prove Darwin ACL isolation.
// It neither reserves execution scratch nor proves sandbox/direct-I/O readiness.
package typedworkspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

var ErrCustody = errors.New("typed workspace custody invalid or incomplete")

// Node records service-observed inode identity, not a caller assertion of bytes.
// A receipt must be persisted in trusted attempt state, never accepted from a client.
type Node struct {
	Path      string `json:"path"`
	Device    uint64 `json:"device"`
	Inode     uint64 `json:"inode"`
	Directory bool   `json:"directory"`
}

// Receipt names remaining owned custody even on error. Only a successful Copy or
// a fresh successful Verify establishes readiness; callers must not infer it from Name.
type Receipt struct {
	Schema          string `json:"schema"`
	InventoryDigest string `json:"inventory_digest"`
	Name            string `json:"name"`
	Nodes           []Node `json:"nodes"`
}

const receiptSchema = "phebs-typed-inputs-v1"

type layout struct {
	files       []typedindex.BundleFile
	directories []string
	entries     map[string]bool
}

func inventoryLayout(inventory typedindex.Inventory) (layout, error) {
	files := inventory.Files()
	if inventory.Digest() == "" || len(files) == 0 || len(files) > typedindex.MaxInventoryFiles {
		return layout{}, ErrCustody
	}
	entries := map[string]bool{".": true}
	for _, file := range files {
		entries[file.Path] = false
		for dir := path.Dir(file.Path); dir != "."; dir = path.Dir(dir) {
			entries[dir] = true
		}
	}
	dirs := make([]string, 0, len(entries)-len(files))
	for name, dir := range entries {
		if dir {
			dirs = append(dirs, name)
		}
	}
	if len(dirs) > typedindex.MaxInventoryDirectories+1 {
		return layout{}, ErrCustody
	}
	slices.Sort(dirs)
	return layout{files, dirs, entries}, nil
}

// Copy requires an existing service-owned 0700 privateParent. The source tree is
// untrusted: every path component is opened without following links; copied bytes
// must match inventory. Before any mkdir it checks pressure and actual destination
// bytes/inodes. Capacity is an observation, not a reservation against other writers.
// The persistent gate belongs to this destination filesystem; callers must not
// clear its latch by using it to check an unrelated filesystem.
func Copy(ctx context.Context, source, privateParent string, inventory typedindex.Inventory, gate *lifecycle.Gate) (Receipt, error) {
	return copyInputs(ctx, source, privateParent, inventory, gate, capacity)
}
func copyInputs(ctx context.Context, source, parent string, inventory typedindex.Inventory, gate *lifecycle.Gate, probe func(*os.File) (space, error)) (receipt Receipt, err error) {
	if ctx == nil {
		return receipt, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return receipt, err
	}
	tree, err := inventoryLayout(inventory)
	if err != nil {
		return receipt, err
	}
	destination, err := openDirectory(parent, true)
	if err != nil {
		return receipt, ErrCustody
	}
	defer func() { err = errors.Join(err, destination.Close()) }()
	available, err := probe(destination)
	if err != nil {
		return receipt, ErrCustody
	}
	needed, err := neededSpace(tree, available.block)
	if err != nil {
		return receipt, err
	}
	// Reuse the caller's latch with the actual descriptor observation. Probing an
	// ancestor first could clear a refusal on this destination filesystem.
	if _, err = gate.CheckObserved(ctx, lifecycle.Capacity{TotalBytes: int64(available.total), AvailableBytes: int64(available.bytes), UsedBytes: int64(available.total - available.bytes)}, int64(needed)); err != nil {
		return receipt, err
	}
	if available.bytes < needed || available.inodes < uint64(len(tree.entries)) {
		return receipt, lifecycle.ErrPressureRefusal
	}
	input, err := openDirectory(source, false)
	if err != nil {
		return receipt, ErrCustody
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	if _, err = inspect(ctx, input, tree, false); err != nil {
		return receipt, err
	}
	if err = ctx.Err(); err != nil {
		return receipt, err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return receipt, ErrCustody
	}
	final := "inputs-" + hex.EncodeToString(token[:])
	stage := final + ".stage"
	if err = mkdir(destination, stage); err != nil {
		return receipt, ErrCustody
	}
	receipt = Receipt{Schema: receiptSchema, InventoryDigest: inventory.Digest(), Name: stage}
	if err = destination.Sync(); err != nil {
		return receipt, ErrCustody
	}
	output, err := openRelative(destination, stage, true)
	if err != nil {
		return receipt, ErrCustody
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	for _, dir := range tree.directories {
		if dir != "." {
			if err = ctx.Err(); err != nil {
				return receipt, err
			}
			if err = mkdir(output, dir); err != nil {
				return receipt, ErrCustody
			}
		}
	}
	for _, file := range tree.files {
		if err = copyFile(ctx, input, output, file); err != nil {
			return receipt, err
		}
		if err = custodybytes.Checkpoint(ctx); err != nil {
			return receipt, err
		}
	}
	// Publish no tree that gained an undeclared source entry during copying.
	if _, err = inspect(ctx, input, tree, false); err != nil {
		return receipt, err
	}
	// Directory children are durable before parents; close all write descriptors
	// before making the tree traversable by the unprivileged worker.
	for i := len(tree.directories) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return receipt, err
		}
		dir, openErr := openRelative(output, tree.directories[i], true)
		if openErr != nil {
			return receipt, ErrCustody
		}
		closeErr := errors.Join(dir.Chmod(0555), dir.Sync(), dir.Close())
		if closeErr != nil {
			return receipt, ErrCustody
		}
	}
	receipt.Nodes, err = inspect(ctx, output, tree, true)
	if err != nil {
		return receipt, err
	}
	if err = custodybytes.Checkpoint(ctx); err != nil {
		return receipt, err
	}
	if err = renameExclusive(destination, stage, final); err != nil {
		return receipt, ErrCustody
	}
	receipt.Name = final
	if err = destination.Sync(); err != nil {
		return receipt, ErrCustody
	}
	return receipt, nil
}

// Verify reopens an exact published tree, validates all file and directory inode
// identities, modes and inventory, and rehashes every bounded file. No cached flag
// replaces these reads. Private ancestry and caller-held lifecycle pin are required.
func Verify(ctx context.Context, privateParent string, inventory typedindex.Inventory, receipt Receipt) (err error) {
	if ctx == nil {
		return ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	tree, err := inventoryLayout(inventory)
	if err != nil {
		return err
	}
	if receipt.Schema != receiptSchema || receipt.InventoryDigest != inventory.Digest() || !publishedName(receipt.Name) || len(receipt.Nodes) != len(tree.entries) {
		return ErrCustody
	}
	parent, err := openDirectory(privateParent, true)
	if err != nil {
		return ErrCustody
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	root, err := openRelative(parent, receipt.Name, true)
	if err != nil {
		return ErrCustody
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	observed, err := inspect(ctx, root, tree, true)
	if err != nil {
		return err
	}
	if !slices.Equal(observed, receipt.Nodes) {
		return ErrCustody
	}
	for _, file := range tree.files {
		if err = hashFile(ctx, root, file); err != nil {
			return err
		}
	}
	again, err := inspect(ctx, root, tree, true)
	if err != nil || !slices.Equal(again, receipt.Nodes) {
		return errors.Join(ErrCustody, err)
	}
	return custodybytes.Checkpoint(ctx)
}
func publishedName(s string) bool {
	if !strings.HasPrefix(s, "inputs-") || len(s) != 39 {
		return false
	}
	token := strings.TrimPrefix(s, "inputs-")
	_, err := hex.DecodeString(token)
	return err == nil && strings.ToLower(token) == token
}

type space struct{ bytes, inodes, block, total uint64 }

func neededSpace(tree layout, block uint64) (uint64, error) {
	if block == 0 || block > 1<<20 {
		return 0, ErrCustody
	}
	// One allocation unit per directory plus rounded file extents and an allocation
	// unit per file for metadata. Actual filesystem allocations remain observable.
	total := uint64(len(tree.entries)) * block
	for _, f := range tree.files {
		total += (uint64(f.Bytes) + block - 1) / block * block
	}
	return total, nil
}

func copyFile(ctx context.Context, src, dst *os.File, row typedindex.BundleFile) (err error) {
	input, err := openRelative(src, row.Path, false)
	if err != nil {
		return ErrCustody
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	before, err := fileInfo(input, row, false)
	if err != nil {
		return err
	}
	output, err := createFile(dst, row.Path)
	if err != nil {
		return ErrCustody
	}
	// The writer remains private until synced and closed. Only this fresh inode is chmodded.
	sum := sha256.New()
	err = transfer(ctx, input, io.MultiWriter(output, sum), row.Bytes)
	if err == nil && "sha256:"+hex.EncodeToString(sum.Sum(nil)) != row.Digest {
		err = ErrCustody
	}
	after, statErr := fileInfo(input, row, false)
	if statErr != nil || before != after {
		err = errors.Join(err, ErrCustody)
	}
	written, statErr := output.Stat()
	err = errors.Join(err, statErr, output.Sync(), output.Close())
	if err != nil {
		return err
	}
	// No writable descriptor remains when permission changes. Reopen only the
	// fresh output inode beneath the still-private stage, never the source path.
	sealed, err := openRelative(dst, row.Path, false)
	if err != nil {
		return ErrCustody
	}
	reopened, statErr := sealed.Stat()
	if statErr != nil || !os.SameFile(written, reopened) {
		_ = sealed.Close()
		return ErrCustody
	}
	mode := os.FileMode(0444)
	if row.Executable {
		mode = 0555
	}
	return errors.Join(sealed.Chmod(mode), sealed.Sync(), sealed.Close())
}
func hashFile(ctx context.Context, root *os.File, row typedindex.BundleFile) (err error) {
	file, err := openRelative(root, row.Path, false)
	if err != nil {
		return ErrCustody
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	before, err := fileInfo(file, row, true)
	if err != nil {
		return err
	}
	sum := sha256.New()
	if err = transfer(ctx, file, sum, row.Bytes); err != nil {
		return err
	}
	after, err := fileInfo(file, row, true)
	if err != nil || before != after || "sha256:"+hex.EncodeToString(sum.Sum(nil)) != row.Digest {
		return ErrCustody
	}
	return nil
}
func transfer(ctx context.Context, input *os.File, output io.Writer, size int64) error {
	var buf [32 << 10]byte
	remaining := size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := min(int64(len(buf)), remaining)
		n, err := io.ReadFull(input, buf[:count])
		if err != nil {
			return ErrCustody
		}
		written, err := output.Write(buf[:n])
		if err != nil || written != n {
			return ErrCustody
		}
		remaining -= int64(n)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := input.Read(buf[:1])
	if n != 0 || err != io.EOF {
		return ErrCustody
	}
	return nil
}

func inspect(ctx context.Context, root *os.File, tree layout, readonly bool) ([]Node, error) {
	rows := make(map[string]typedindex.BundleFile, len(tree.files))
	for _, r := range tree.files {
		rows[r.Path] = r
	}
	queue := []string{"."}
	seen := map[string]bool{}
	nodes := make([]Node, 0, len(tree.entries))
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		directory, err := openRelative(root, name, true)
		if err != nil {
			return nil, ErrCustody
		}
		node, err := directoryInfo(directory, name, readonly)
		if err != nil {
			_ = directory.Close()
			return nil, err
		}
		nodes = append(nodes, node)
		seen[name] = true
		for {
			if err = ctx.Err(); err != nil {
				_ = directory.Close()
				return nil, err
			}
			entries, readErr := directory.ReadDir(128)
			for _, entry := range entries {
				child := path.Join(name, entry.Name())
				isdir, expected := tree.entries[child]
				if !expected || seen[child] || len(nodes)+len(queue) >= len(tree.entries) {
					_ = directory.Close()
					return nil, ErrCustody
				}
				seen[child] = true
				if isdir {
					queue = append(queue, child)
					continue
				}
				file, openErr := openRelative(root, child, false)
				if openErr != nil {
					_ = directory.Close()
					return nil, ErrCustody
				}
				st, statErr := fileInfo(file, rows[child], readonly)
				closeErr := file.Close()
				if statErr != nil || closeErr != nil {
					_ = directory.Close()
					return nil, ErrCustody
				}
				nodes = append(nodes, Node{child, st.device, st.inode, false})
			}
			if readErr != nil {
				if readErr != io.EOF {
					_ = directory.Close()
					return nil, ErrCustody
				}
				break
			}
		}
		if err = directory.Close(); err != nil {
			return nil, ErrCustody
		}
	}
	if len(nodes) != len(tree.entries) {
		return nil, ErrCustody
	}
	slices.SortFunc(nodes, func(a, b Node) int { return strings.Compare(a.Path, b.Path) })
	return nodes, nil
}
