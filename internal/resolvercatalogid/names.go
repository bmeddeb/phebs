// Package resolvercatalogid owns the stable filesystem names shared by the
// resolver-catalog lifecycle and its database pointer validator.
package resolvercatalogid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const artifactNamespace = "v1"

const (
	SourceLanePolicy = "candidate-source-lane-base-v1"
	WriterSchema     = "phebs-resolver-catalog-store-v1"
)

func ArtifactBase(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return "phebs-resolver-catalog-" + hex.EncodeToString(sum[:])
}

func ManifestName(repository string) string {
	return ArtifactBase(repository) + ".manifest.json"
}

func PublishingName(repository string) string {
	return ArtifactBase(repository) + ".publishing"
}

func ArtifactPrefix(repository, generationDigest string) string {
	return OwnedArtifactPrefix(repository) +
		strings.TrimPrefix(generationDigest, "sha256:") + "-"
}

func OwnedArtifactPrefix(repository string) string {
	return ArtifactBase(repository) + "-" + artifactNamespace + "-"
}
