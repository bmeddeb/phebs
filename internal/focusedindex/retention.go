package focusedindex

import (
	"path/filepath"
	"strings"
)

// IsRetentionArtifactName reports whether name belongs to the stable focused
// publication namespace. Whole-repository shards, publication markers, and
// build workspaces are intentionally outside this component.
func IsRetentionArtifactName(name string) bool {
	if filepath.Base(name) != name || !strings.HasPrefix(name, "phebs-focus-") {
		return false
	}
	identity := strings.TrimPrefix(name, "phebs-focus-")
	if len(identity) < 64 || !retentionLowerHex(identity[:64]) {
		return false
	}
	if identity[64:] == ".manifest.json" {
		return true
	}
	shard := strings.TrimSuffix(name, MemberSuffix)
	return managedHashShardName(shard, "phebs-focus-", '-')
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
