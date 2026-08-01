// Package callerpublicationid owns the stable filesystem and store identities
// for complete caller-generation publications.
package callerpublicationid

import (
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/callerleafid"
)

const (
	WriterSchema   = "phebs-caller-generation-publication-store-v1"
	ManifestPrefix = "complete-v1-"
	PublishingName = "complete-v1.publishing"
	DeletingName   = "complete-v1.deleting"
	StagePrefix    = ".phebs-caller-publication-stage-"

	// InstallationPublicationRepositories, InstallationPublicationRefs, and
	// InstallationCanonicalBytes are the
	// shared durable-writer/startup admission ceilings. Keeping them beside
	// the store/filesystem identity avoids either side accepting state that the
	// other must refuse after restart.
	InstallationPublicationRepositories = 65_536
	InstallationPublicationRefs         = 65_536
	InstallationCanonicalBytes          = int64(1 << 40)

	manifestSuffix = ".manifest.json"
)

// RepositoryDirectory derives the complete-publication owner from the same
// validated repository identity as its independently durable leaf artifacts.
func RepositoryDirectory(repository string) string {
	return callerleafid.RepositoryDirectory(repository)
}

// ManifestName binds the exact semantic generation and canonical manifest
// bytes. Neither decoded manifest content nor a store-selected path chooses a
// filesystem owner.
func ManifestName(generationDigest, manifestDigest string) string {
	return ManifestPrefix + digestHex(generationDigest) + "-" +
		digestHex(manifestDigest) + manifestSuffix
}

// ManifestDigests decodes only a canonical package-owned basename. Returned
// digests use the canonical sha256: form consumed by callerleaf identities.
func ManifestDigests(name string) (generationDigest, manifestDigest string, ok bool) {
	if filepath.Base(name) != name || !strings.HasPrefix(name, ManifestPrefix) ||
		!strings.HasSuffix(name, manifestSuffix) {
		return "", "", false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, ManifestPrefix), manifestSuffix)
	if len(raw) != 64+1+64 || raw[64] != '-' ||
		!validLowerHex(raw[:64]) || !validLowerHex(raw[65:]) {
		return "", "", false
	}
	return "sha256:" + raw[:64], "sha256:" + raw[65:], true
}

func ValidManifestName(name string) bool {
	_, _, ok := ManifestDigests(name)
	return ok
}

func ValidStageName(name string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, StagePrefix) &&
		len(name) == len(StagePrefix)+32 && validLowerHex(name[len(StagePrefix):])
}

func digestHex(value string) string {
	return strings.TrimPrefix(value, "sha256:")
}

func validLowerHex(value string) bool {
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
