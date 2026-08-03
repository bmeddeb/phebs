package resolvercatalog

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

// IsRetentionRootArtifactName reports whether name is a stable installed
// resolver-catalog manifest or member. Markers and temporary controls are not
// retained publication identities.
func IsRetentionRootArtifactName(name string) bool {
	const prefix = "phebs-resolver-catalog-"
	if filepath.Base(name) != name || !strings.HasPrefix(name, prefix) {
		return false
	}
	tail := strings.TrimPrefix(name, prefix)
	if len(tail) < 64 || !retentionLowerHex(tail[:64]) {
		return false
	}
	tail = tail[64:]
	if tail == ".manifest.json" {
		return true
	}
	if !strings.HasPrefix(tail, "-v1-") {
		return false
	}
	tail = strings.TrimPrefix(tail, "-v1-")
	return len(tail) > 65 && retentionLowerHex(tail[:64]) && tail[64] == '-' &&
		validMemberName(tail[65:])
}

// IsRetentionStageName recognizes the private MkdirTemp namespace used by a
// resolver publication before installation.
func IsRetentionStageName(name string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, stageDirectoryPrefix) &&
		len(name) > len(stageDirectoryPrefix)
}

// ParseRetentionManifest validates canonical manifest metadata without
// opening or hashing any member payload and returns its committed receipt.
func ParseRetentionManifest(raw []byte) (State, int64, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes {
		return State{}, 0, fmt.Errorf("%w: manifest metadata exceeds bound", ErrInvalidManifest)
	}
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil {
		return State{}, 0, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := validateManifest(manifest); err != nil {
		return State{}, 0, err
	}
	var canonical int64
	for _, member := range manifest.Members {
		if member.ContentBytes > math.MaxInt64-canonical {
			return State{}, 0, fmt.Errorf("%w: canonical byte overflow", ErrInvalidManifest)
		}
		canonical += member.ContentBytes
	}
	return manifest.State(), canonical, nil
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
