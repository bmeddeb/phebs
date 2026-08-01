package callerpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/reponame"
)

const deletionMarkerSchema = "phebs-caller-publication-deletion-v1"

type deletionMarker struct {
	Schema     string `json:"schema"`
	Repository string `json:"repository"`
}

func markRepositoryDeleting(
	ctx context.Context,
	root, repository string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reponame.Validate(repository); err != nil {
		return err
	}
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer authority.close()
	wanted := deletionMarker{Schema: deletionMarkerSchema, Repository: repository}
	if _, err := authority.root.Lstat(callerpublicationid.DeletingName); err == nil {
		observed, readErr := readDeletionMarkerAt(authority)
		if readErr != nil {
			return readErr
		}
		if observed != wanted {
			return fmt.Errorf("%w: deletion marker differs", ErrInvalidManifest)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := json.Marshal(wanted)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	file, err := authority.root.OpenFile(
		callerpublicationid.DeletingName,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	var shortWriteErr error
	if written != len(raw) {
		shortWriteErr = io.ErrShortWrite
	}
	if markerErr := errors.Join(writeErr, shortWriteErr, syncErr, closeErr); markerErr != nil {
		cleanupErr := removeDerivedMarkerAt(
			authority, callerpublicationid.DeletingName,
		)
		return errors.Join(markerErr, cleanupErr)
	}
	return syncRootDirectory(authority.root, ".")
}

func clearRepositoryDeleting(root, repository string) (bool, error) {
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer authority.close()
	observed, err := readDeletionMarkerAt(authority)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if observed.Repository != repository {
		return false, fmt.Errorf("%w: deletion marker differs", ErrInvalidManifest)
	}
	if err := removeDerivedMarkerAt(
		authority, callerpublicationid.DeletingName,
	); err != nil {
		return false, err
	}
	return true, nil
}

func readDeletionMarkerAt(
	authority *directoryAuthority,
) (deletionMarker, error) {
	raw, _, err := readStableRegularAt(
		authority, callerpublicationid.DeletingName, maxMarkerBytes,
	)
	if err != nil {
		return deletionMarker{}, err
	}
	var marker deletionMarker
	if err := decodeCanonical(raw, &marker); err != nil ||
		marker.Schema != deletionMarkerSchema ||
		reponame.Validate(marker.Repository) != nil {
		return deletionMarker{}, fmt.Errorf(
			"%w: invalid deletion marker", ErrInvalidManifest,
		)
	}
	return marker, nil
}

// ReconcileDeletionMarkers closes the active-lease deletion crash edge. A
// valid ineligible tombstone still owns its package directory and removes it
// on the next startup. Same-name recreation clears only that exact tombstone.
// A deterministically incomplete tombstone cannot select a repository and is
// itself removed; operational I/O remains a startup failure.
func ReconcileDeletionMarkers(
	ctx context.Context,
	root string,
	eligible func(context.Context, string) (bool, error),
) error {
	if eligible == nil {
		return errors.New("caller publication deletion eligibility is not configured")
	}
	if err := ensureRealDirectory(root, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return err
	}
	defer rootAuthority.close()
	repositories, err := readRootDirectory(
		rootAuthority.root, callerpublicationid.InstallationPublicationRepositories,
	)
	if err != nil {
		return err
	}
	for index, repositoryEntry := range repositories {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if !ValidRepositoryDirectoryName(repositoryEntry.Name()) ||
			!repositoryEntry.IsDir() || repositoryEntry.Type()&os.ModeSymlink != 0 {
			continue
		}
		authority, err := openChildDirectoryAuthority(
			rootAuthority, repositoryEntry.Name(), nil,
		)
		if err != nil {
			return err
		}
		marker, markerErr := readDeletionMarkerAt(authority)
		if errors.Is(markerErr, os.ErrNotExist) {
			authority.close()
			continue
		}
		if errors.Is(markerErr, ErrPublicationIO) {
			authority.close()
			return markerErr
		}
		if markerErr != nil || callerpublicationid.RepositoryDirectory(
			marker.Repository,
		) != repositoryEntry.Name() {
			// An incomplete tombstone has no trustworthy repository identity.
			// Remove only the descriptor-owned marker; ordinary publication
			// reconciliation below will preserve live authority or reclaim exact
			// ineligible residue without guessing from corrupt bytes.
			removeErr := removeDerivedMarkerAt(
				authority, callerpublicationid.DeletingName,
			)
			authority.close()
			if removeErr != nil {
				return removeErr
			}
			continue
		}
		authority.close()
		live, err := eligible(ctx, marker.Repository)
		if err != nil {
			return err
		}
		if live {
			if _, err := clearRepositoryDeleting(root, marker.Repository); err != nil {
				return err
			}
			continue
		}
		if err := callerleaf.RemoveRepository(ctx, root, marker.Repository); err != nil {
			return err
		}
	}
	return nil
}

func reconcileDeletionMarkers(
	ctx context.Context,
	root string,
	eligible func(context.Context, string) (bool, error),
) error {
	return ReconcileDeletionMarkers(ctx, root, eligible)
}
