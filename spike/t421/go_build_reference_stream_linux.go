//go:build linux

package t421

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The source is a borrowed H2k root, not an ambient pathname or a foreign bind
// mount. Its owner keeps the read-only loan through transfer and helper join.
func gatherGoReferenceHeldInputs(ctx context.Context, root *os.File) (_ *os.Root, _ []inputTreeSourceEntry, retErr error) {
	return gatherGoReferenceHeldInputsObserved(ctx, root, nil)
}

func gatherGoReferenceHeldInputsObserved(ctx context.Context, root *os.File, observation *executionPreworkObservation) (_ *os.Root, _ []inputTreeSourceEntry, retErr error) {
	if ctx == nil || ctx.Err() != nil || root == nil {
		return nil, nil, ErrExecutionGoBuildCustody
	}
	tree, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", root.Fd()))
	if err != nil {
		return nil, nil, ErrExecutionGoBuildCustody
	}
	defer func() {
		if retErr != nil {
			_ = observation.close(tree.Close())
		}
	}()
	held, err := root.Stat()
	opened, treeErr := tree.Stat(".")
	if err != nil || treeErr != nil || !os.SameFile(held, opened) {
		return nil, nil, ErrExecutionGoBuildCustody
	}
	source := &inputTreeSourceRoot{file: root, tree: tree, info: held, observation: observation}
	var rows []inputTreeSourceEntry
	var paths, bytes uint64
	err = walkGoBuildTree(ctx, tree, "inputs", func(path string, info os.FileInfo) error {
		name := filepath.ToSlash(path)
		entry := inputTreeEntry{name: name, mode: 0o700}
		if !info.IsDir() {
			if info.Size() < 0 || info.Size() > maxInputCustodyFileBytes {
				return ErrExecutionGoBuildCustody
			}
			entry.mode, entry.size = 0o400, uint64(info.Size())
			if info.Mode().Perm()&0o111 != 0 {
				entry.mode = 0o500
			}
		}
		if len(rows) >= maxGoBuildEntries || uint64(len(name)) > maxGoBuildPathBytes-paths || entry.size > maxGoBuildBytes-bytes {
			return ErrExecutionGoBuildCustody
		}
		paths += uint64(len(name))
		bytes += entry.size
		rows = append(rows, inputTreeSourceEntry{entry: entry, root: source, relative: path, info: info})
		return nil
	})
	if err != nil {
		return nil, nil, ErrExecutionGoBuildCustody
	}
	slices.SortFunc(rows, func(a, b inputTreeSourceEntry) int { return strings.Compare(a.entry.name, b.entry.name) })
	return tree, rows, nil
}

func sendGoReferenceHeldInputs(ctx context.Context, wire *inputTreeSocket, root *os.File, expected LinuxInputTreeIdentity) (_ inputTreeCensus, retErr error) {
	return sendGoReferenceHeldInputsObserved(ctx, wire, root, expected, nil)
}

func sendGoReferenceHeldInputsObserved(ctx context.Context, wire *inputTreeSocket, root *os.File, expected LinuxInputTreeIdentity, observation *executionPreworkObservation) (_ inputTreeCensus, retErr error) {
	tree, rows, err := gatherGoReferenceHeldInputsObserved(ctx, root, observation)
	if err != nil {
		return inputTreeCensus{}, ErrExecutionGoBuildCustody
	}
	defer func() {
		if observation.close(tree.Close()) != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	var census inputTreeCensus
	buffer := make([]byte, treeWireChunk)
	for _, row := range rows {
		kind := byte(treeWireFile)
		if row.entry.mode == 0o700 {
			kind = treeWireDirectory
		}
		if census.add(row.entry) != nil || wire.send(ctx, kind, encodeInputTreeEntry(row.entry), nil) != nil {
			return inputTreeCensus{}, ErrExecutionGoBuildCustody
		}
		if kind == treeWireDirectory {
			continue
		}
		entry, err := sendInputTreeSourceFile(ctx, wire, row, buffer)
		if err != nil {
			return inputTreeCensus{}, ErrExecutionGoBuildCustody
		}
		census.rows[len(census.rows)-1] = entry
	}
	for _, row := range rows {
		current, err := tree.Lstat(row.relative)
		if ctx.Err() != nil || err != nil || !inputCustodySame(row.info, current) {
			return inputTreeCensus{}, ErrExecutionGoBuildCustody
		}
	}
	manifest := inputTreeManifest(census)
	if census.inventory.entries != expected.Entries || census.inventory.files != expected.Files || census.inventory.bytes != expected.Bytes ||
		census.inventory.pathBytes != expected.PathBytes || "sha256:"+hex.EncodeToString(manifest[32:]) != expected.ManifestSHA256 ||
		wire.send(ctx, treeWireEnd, manifest, nil) != nil {
		return inputTreeCensus{}, ErrExecutionGoBuildCustody
	}
	return census, nil
}
