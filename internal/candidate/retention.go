package candidate

import (
	"path/filepath"
	"strings"
)

const retentionArtifactPrefix = "phebs-candidate-"

// IsRetentionArtifactName reports whether name is a stable file in the
// candidate package namespace. Member grammar is intentionally generation-
// agnostic: lifecycle ownership retains historical and unmatched residue by
// ArtifactBase(repository)+"-", not only members produced by the current
// writer. Publication markers and dot-prefixed stages remain controls.
func IsRetentionArtifactName(name string) bool {
	if filepath.Base(name) != name || !strings.HasPrefix(name, retentionArtifactPrefix) {
		return false
	}
	tail := strings.TrimPrefix(name, retentionArtifactPrefix)
	if len(tail) < 64 || !retentionLowerHex(tail[:64]) {
		return false
	}
	tail = tail[64:]
	if tail == ".manifest.json" {
		return true
	}
	return len(tail) > 1 && tail[0] == '-'
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
