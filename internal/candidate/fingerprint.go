package candidate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ControlFingerprint is a process-local observation of one persisted
// publication's control bytes and member file identities. It is deliberately
// not a content-validation receipt: CaptureControlFingerprintContext proves
// the manifest digest and member envelopes, while MatchesContext detects
// subsequent path-identity, mode, size, or modification-time changes. A
// strict Open still proves every member digest before consumption.
type ControlFingerprint struct {
	state State
	files []controlFileFingerprint
}

type controlFileFingerprint struct {
	name string
	info os.FileInfo
}

// CaptureControlFingerprintContext establishes the cheap steady-state
// fingerprint for a persisted publication without reading member contents.
// It may run while a publication marker is installed after the caller has
// already strictly validated those bytes during publication or recovery.
func CaptureControlFingerprintContext(
	ctx context.Context,
	root string,
	state State,
) (*ControlFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}
	if err := ensureRealDirectory(root); err != nil {
		return nil, err
	}

	manifestPath := filepath.Join(root, state.Manifest)
	before, err := os.Lstat(manifestPath)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: inspect candidate manifest control file: %w",
			ErrInvalidManifest, err,
		)
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	after, err := os.Lstat(manifestPath)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: inspect candidate manifest control file after read: %w",
			ErrInvalidManifest, err,
		)
	}
	if !sameRegularFileIdentity(before, after) {
		return nil, fmt.Errorf(
			"%w: candidate manifest control file changed while fingerprinting",
			ErrInvalidManifest,
		)
	}
	if err := validateManifestIdentity(
		manifest, Expected{}, &state,
	); err != nil {
		return nil, err
	}

	fingerprint := &ControlFingerprint{
		state: state,
		files: []controlFileFingerprint{{
			name: state.Manifest,
			info: after,
		}},
	}
	seen := map[string]struct{}{state.Manifest: {}}
	addArtifact := func(member Artifact, ordinal int, expectedName string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateArtifactEnvelope(
			member, ordinal, expectedName,
		); err != nil {
			return err
		}
		if _, exists := seen[member.Name]; exists {
			return fmt.Errorf(
				"%w: duplicate candidate member name",
				ErrInvalidManifest,
			)
		}
		info, err := os.Lstat(filepath.Join(root, member.Name))
		if err != nil {
			return fmt.Errorf(
				"%w: inspect candidate member %q: %w",
				ErrInvalidManifest, member.Name, err,
			)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			info.Size() != member.ContentBytes {
			return fmt.Errorf(
				"%w: candidate member %q is special or has the wrong size",
				ErrInvalidManifest, member.Name,
			)
		}
		seen[member.Name] = struct{}{}
		fingerprint.files = append(
			fingerprint.files,
			controlFileFingerprint{name: member.Name, info: info},
		)
		return nil
	}

	prefix := ArtifactPrefix(manifest.Repository, manifest.GenerationDigest)
	for ordinal, member := range manifest.RepositoryMembers {
		if err := addArtifact(
			member, ordinal,
			prefix+fmt.Sprintf("repository-%06d.ndjson", ordinal),
		); err != nil {
			return nil, err
		}
	}
	var previousLeaf string
	for ordinal, leaf := range manifest.CallerLeaves {
		if err := validateCallerLeaf(leaf, previousLeaf); err != nil {
			return nil, err
		}
		if previousLeaf != "" && strings.HasPrefix(leaf.Prefix, previousLeaf) {
			return nil, fmt.Errorf(
				"%w: caller leaves overlap", ErrInvalidManifest,
			)
		}
		previousLeaf = leaf.Prefix
		if err := addArtifact(
			leaf.Artifact, ordinal,
			prefix+fmt.Sprintf("caller-%06d.ndjson", ordinal),
		); err != nil {
			return nil, err
		}
	}
	stable, err := fingerprint.MatchesContext(ctx, root, state)
	if err != nil {
		return nil, err
	}
	if !stable {
		return nil, fmt.Errorf(
			"%w: candidate controls changed while fingerprinting",
			ErrInvalidManifest,
		)
	}
	return fingerprint, nil
}

// MatchesContext reports whether every observed control/member file retains
// the identity captured for the same persisted state. False is an
// invalidation signal, not proof of corruption; callers should strictly open
// the publication before deciding whether to reuse or rebuild it.
func (fingerprint *ControlFingerprint) MatchesContext(
	ctx context.Context,
	root string,
	state State,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if fingerprint == nil {
		return false, errors.New("candidate control fingerprint is nil")
	}
	if err := state.Validate(); err != nil {
		return false, err
	}
	if fingerprint.state != state {
		return false, nil
	}
	if err := ensureRealDirectory(root); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	for _, file := range fingerprint.files {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		current, err := os.Lstat(filepath.Join(root, file.name))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf(
				"inspect candidate control fingerprint %q: %w",
				file.name, err,
			)
		}
		if !sameRegularFileIdentity(file.info, current) {
			return false, nil
		}
	}
	return true, nil
}
