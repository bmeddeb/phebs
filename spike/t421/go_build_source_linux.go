//go:build linux

package t421

import (
	"context"
	"os"
	"path/filepath"
)

// Reserve the loose-object construction envelope before Git may write. Unlike
// copying SDK/cache files, reconstructing the exact source also creates Git
// metadata; the raw source byte limit alone does not cover those writes.
func (staging *linuxGoBuildStaging) prepareSource(ctx context.Context, origin executionCheckoutInspector, commit, destination string) (executionReferenceSource, error) {
	refuse := func() (executionReferenceSource, error) { return executionReferenceSource{}, staging.refuse() }
	if ctx == nil || ctx.Err() != nil || staging.err != nil || destination != filepath.Join(staging.directory, "source") {
		return refuse()
	}
	raw, err := origin.run(ctx, maxCheckoutInventoryBytes, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return refuse()
	}
	entries, err := executionCheckoutEntries(raw)
	if err != nil {
		return refuse()
	}
	reservation, rawBytes := int64(8<<20), int64(0)
	pathReservation := 32 << 10
	directories := map[string]bool{}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(origin.root, entry.path))
		if ctx.Err() != nil || err != nil || !info.Mode().IsRegular() || !inputCustodyCopyOwned(info) ||
			info.Size() < 0 || info.Size() > maxInputCustodyFileBytes || len("inputs/source/"+entry.path) > maxInputCustodyPathBytes {
			return refuse()
		}
		cost := 2*info.Size() + 8*int64(len(entry.path)) + 512
		if cost > maxGoBuildBytes-reservation {
			return refuse()
		}
		reservation += cost
		rawBytes += info.Size()
		pathReservation += len(entry.path) + 80
		for directory := filepath.Dir(entry.path); directory != "."; directory = filepath.Dir(directory) {
			if !directories[directory] {
				directories[directory] = true
				pathReservation += len(directory) + 80
			}
			if 2*len(entries)+2*len(directories)+300 > maxGoBuildEntries-staging.inventory.Entries || pathReservation > maxGoBuildPathBytes-staging.pathBytes {
				return refuse()
			}
		}
	}
	if reservation > maxGoBuildBytes-staging.inventory.Bytes || 2*len(entries)+2*len(directories)+300 > maxGoBuildEntries-staging.inventory.Entries ||
		pathReservation > maxGoBuildPathBytes-staging.pathBytes {
		return refuse()
	}
	staging.inventory.Bytes += reservation
	reference, err := createExecutionReferenceSourceBounded(ctx, origin, commit, destination, rawBytes)
	if err != nil {
		return refuse()
	}
	var actual int64
	err = walkGoBuildTree(ctx, staging.tree, "source", func(path string, info os.FileInfo) error {
		if staging.addName(path) != nil {
			return staging.refuse()
		}
		staging.names[path] = info
		if info.IsDir() {
			return nil
		}
		if info.Size() < 0 || info.Size() > maxInputCustodyFileBytes || info.Size() > reservation-actual {
			return staging.refuse()
		}
		actual += info.Size()
		staging.inventory.Files++
		return nil
	})
	if err != nil {
		return refuse()
	}
	staging.inventory.Bytes -= reservation - actual
	return reference, nil
}
