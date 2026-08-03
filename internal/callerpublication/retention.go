package callerpublication

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

// IsRetentionRepositoryDirectoryName recognizes the cryptographic directory
// namespace shared by complete manifests and independently durable leaves.
func IsRetentionRepositoryDirectoryName(name string) bool {
	const prefix = "phebs-caller-overlay-"
	return filepath.Base(name) == name && strings.HasPrefix(name, prefix) &&
		len(name) == len(prefix)+64 && retentionLowerHex(name[len(prefix):])
}

// IsRetentionArtifactName reports whether name is a complete-generation
// manifest or an immutable caller-leaf artifact.
func IsRetentionArtifactName(name string) bool {
	return callerpublicationid.ValidManifestName(name) || validRetentionLeafName(name)
}

// IsRetentionIgnoredControlName recognizes lifecycle-owned stages and markers
// which the retained-artifact component deliberately does not count.
func IsRetentionIgnoredControlName(name string) bool {
	return name == callerpublicationid.PublishingName ||
		name == callerpublicationid.DeletingName ||
		callerpublicationid.ValidStageName(name) || validRetentionLeafStageName(name)
}

// ParseRetentionManifest validates canonical manifest metadata without
// opening or hashing a leaf payload.
func ParseRetentionManifest(raw []byte) (State, int64, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return State{}, 0, fmt.Errorf("%w: manifest metadata exceeds bound", ErrInvalidManifest)
	}
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil {
		return State{}, 0, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return State{}, 0, err
	}
	return manifest.State(), manifest.Aggregate.CanonicalBytes, nil
}

func validRetentionLeafName(name string) bool {
	const prefix = "leaf-v1-"
	const suffix = ".ndjson"
	if filepath.Base(name) != name || !strings.HasPrefix(name, prefix) ||
		!strings.HasSuffix(name, suffix) || len(name) != len(prefix)+64+1+64+len(suffix) {
		return false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	return len(hexPart) == 129 && hexPart[64] == '-' &&
		retentionLowerHex(hexPart[:64]) && retentionLowerHex(hexPart[65:])
}

func validRetentionLeafStageName(name string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, callerleafid.StagePrefix) &&
		len(name) == len(callerleafid.StagePrefix)+32 &&
		retentionLowerHex(name[len(callerleafid.StagePrefix):])
}

func retentionLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}
