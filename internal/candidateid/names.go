// Package candidateid owns the stable filesystem names shared by candidate
// publication code and its database pointer validator.
package candidateid

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func ArtifactBase(repository string) string {
	sum := sha256.Sum256([]byte(repository))
	return "phebs-candidate-" + hex.EncodeToString(sum[:])
}

func ManifestName(repository string) string {
	return ArtifactBase(repository) + ".manifest.json"
}

func PublishingName(repository string) string {
	return ArtifactBase(repository) + ".publishing"
}

func ArtifactPrefix(repository, generationDigest string) string {
	return ArtifactBase(repository) + "-" +
		strings.TrimPrefix(generationDigest, "sha256:") + "-"
}
