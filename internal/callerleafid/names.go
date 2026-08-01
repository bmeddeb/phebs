// Package callerleafid owns stable caller-leaf filesystem and store identities.
package callerleafid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	ArtifactNamespace = "v1"
	WriterSchema      = "phebs-caller-leaf-store-v1"
	SourceLanePolicy  = "candidate-source-lane-base-v1"
	StagePrefix       = ".phebs-caller-leaf-stage-"
)

// RepositoryDirectory is derived only from the already validated repository
// name. Decoded artifact content never selects a filesystem owner.
func RepositoryDirectory(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return "phebs-caller-overlay-" + hex.EncodeToString(sum[:])
}

// ArtifactName binds both the exact pair and its canonical bytes. A retry can
// therefore identify a byte-identical install without overwriting another
// result for the same immutable pair.
func ArtifactName(pairDigest, contentDigest string) string {
	return PairArtifactPrefix(pairDigest) +
		digestHex(contentDigest) + ".ndjson"
}

// PairArtifactPrefix identifies every possible content-addressed result for
// one exact generation/domain/leaf pair. Callers still validate a complete
// name before treating it as owned bytes.
func PairArtifactPrefix(pairDigest string) string {
	return "leaf-" + ArtifactNamespace + "-" + digestHex(pairDigest) + "-"
}

func digestHex(value string) string {
	return strings.TrimPrefix(value, "sha256:")
}
