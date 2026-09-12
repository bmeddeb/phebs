package recovery

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// ReadArchiveTransitionManifestFile borrows a manifest descriptor opened by the
// selected caller's no-follow rooted custody checks. It neither opens paths nor
// changes the descriptor offset. Only this bounded content read is charged;
// descriptor metadata and the caller's directory checks are separate work.
func ReadArchiveTransitionManifestFile(ctx context.Context, file *os.File, backupCommandSHA256, restoreCommandSHA256 string) (ArchiveTransitionManifest, error) {
	if ctx == nil || file == nil || !validSHA256(backupCommandSHA256) || !validSHA256(restoreCommandSHA256) {
		return ArchiveTransitionManifest{}, errors.New("archive transition descriptor input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return ArchiveTransitionManifest{}, err
	}
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return ArchiveTransitionManifest{}, err
	}
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > maxManifestBytes {
		return ArchiveTransitionManifest{}, errors.New("archive transition manifest descriptor is invalid")
	}
	manifest, err := decodeArchiveManifest(io.NewSectionReader(file, 0, maxManifestBytes+1))
	if err != nil {
		return ArchiveTransitionManifest{}, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return ArchiveTransitionManifest{}, errors.New("archive transition manifest descriptor changed")
	}
	return projectArchiveTransitionManifest(ctx, manifest, backupCommandSHA256, restoreCommandSHA256)
}
