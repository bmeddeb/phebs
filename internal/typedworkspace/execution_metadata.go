package typedworkspace

import (
	"context"
	"errors"
	"io"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// ExecutionMetadata is an authenticated in-memory snapshot, not an execution
// permit. The caller holds its lifecycle guard until OpenControls acquires the
// attempt pin. Inputs must already have passed Copy/Verify under that guard.
type ExecutionMetadata struct {
	Inventory                          typedindex.Inventory
	InventoryRaw, HostTools, Selection []byte
	Inputs                             Receipt
}

// LoadOwnerExecutionMetadata reads only retained controls and the two fixed
// execution metadata members. It never re-encodes the inventory or rehashes the
// source corpus. Fixed selection bytes use the existing plan envelope.
func LoadOwnerExecutionMetadata(ctx context.Context, base string, id OwnerIdentity, expected string) (out ExecutionMetadata, err error) {
	if ctx == nil || !id.valid() || !publicationHash(expected) {
		return out, ErrCustody
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	release, err := publicationLease(ctx, base, false, false)
	if err != nil {
		return out, err
	}
	defer release()
	m, err := loadOwner(ctx, base, id, false)
	if err != nil || m.Digest() != expected || m.Revision != 2 || m.Inventory == nil || m.Inputs == nil {
		return out, ErrCustody
	}
	attempt := filepath.Join(base, id.RelativeName())
	dir, err := openDirectory(attempt, true)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, dir.Close())
		if err != nil {
			out = ExecutionMetadata{}
		}
	}()
	node, err := directoryInfo(dir, id.RelativeName(), false)
	if err != nil || node != m.Directory {
		return out, ErrCustody
	}
	out.InventoryRaw, _, err = ownerReadControl(ctx, dir, "inventory.json", typedindex.MaxInventoryBytes, m.Inventory)
	if err != nil {
		return out, err
	}
	out.Inventory, err = typedindex.DecodeInventory(ctx, out.InventoryRaw, id.Request.BundleDigest)
	if err != nil {
		return out, err
	}
	raw, _, err := ownerReadControl(ctx, dir, "input-receipt.json", MaxInputReceiptBytes, m.Inputs)
	if err != nil {
		return out, err
	}
	out.Inputs, err = decodeOwnerInputs(ctx, out.Inventory, raw)
	if err != nil || out.Inputs.Name != m.InputName {
		return out, ErrCustody
	}
	root, err := openRelative(dir, m.InputName, true)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, root.Close())
		if err != nil {
			out = ExecutionMetadata{}
		}
	}()
	rootNode, err := directoryInfo(root, ".", true)
	if err != nil {
		return out, err
	}
	nodes := make(map[string]Node, len(out.Inputs.Nodes))
	for _, n := range out.Inputs.Nodes {
		nodes[n.Path] = n
	}
	if nodes["."] != rootNode {
		return out, ErrCustody
	}
	files := out.Inventory.Files()
	for _, selected := range []struct {
		name  string
		limit int
		dest  *[]byte
	}{{typedindex.HostToolsFile, typedindex.MaxHostToolsBytes, &out.HostTools}, {"typed-bazel-selection.json", typedindex.MaxPlanBytes, &out.Selection}} {
		var row typedindex.BundleFile
		for _, f := range files {
			if f.Path == selected.name {
				row = f
				break
			}
		}
		n, ok := nodes[selected.name]
		if !ok || n.Directory || row.Path == "" || row.Executable || row.Bytes <= 0 || row.Bytes > int64(selected.limit) {
			return out, ErrCustody
		}
		f, e := openRelative(root, selected.name, false)
		if e != nil {
			return out, e
		}
		before, e := fileInfo(f, row, true)
		if e != nil || before.device != n.Device || before.inode != n.Inode {
			_ = f.Close()
			return out, ErrCustody
		}
		b, e := io.ReadAll(io.LimitReader(f, int64(selected.limit)+1))
		after, se := fileInfo(f, row, true)
		ce := f.Close()
		if e != nil || se != nil || ce != nil || before != after || int64(len(b)) != row.Bytes || publicationDigest(b) != row.Digest {
			return out, ErrCustody
		}
		named, e := openRelative(root, selected.name, false)
		if e != nil {
			return out, e
		}
		namedInfo, ne := fileInfo(named, row, true)
		nce := named.Close()
		if ne != nil || nce != nil || namedInfo != after {
			return out, ErrCustody
		}
		*selected.dest = b
	}
	namedRoot, e := openRelative(dir, m.InputName, true)
	if e != nil {
		return out, e
	}
	namedNode, ne := directoryInfo(namedRoot, ".", true)
	ce := namedRoot.Close()
	if ne != nil || ce != nil || namedNode != rootNode {
		return out, ErrCustody
	}
	if err = ownerSameDirectory(dir, attempt); err != nil {
		return out, err
	}
	return out, ctx.Err()
}
