package callerpublication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/downstreamauthority/authorityvalidate"
)

// RestoreContinuitySHA256 commits to every field of this already validated,
// leased manifest except its self digest and upstream extraction-run provenance.
// It changes no publication identity and opens no file. The opt-in ceremony F
// pays one bounded manifest copy/encoding/hash; ordinary readers never call it.
func (publication *Publication) RestoreContinuitySHA256() (string, error) {
	if publication == nil || publication.manifest.Schema != ManifestSchema ||
		publication.manifest.Generation.Schema != callerleaf.GenerationSchemaV2 ||
		!validDigest(publication.manifest.Digest) {
		return "", ErrInvalidManifest
	}
	manifest := cloneManifest(publication.manifest)
	upstream, err := authorityvalidate.WithoutRunProvenance(manifest.Generation.Upstream)
	if err != nil {
		return "", err
	}
	manifest.Generation.Upstream = upstream
	manifest.Digest = ""
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if len(raw) > MaxManifestBytes {
		return "", ErrInvalidManifest
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("t422-caller-restore-continuity-manifest-v1\x00"))
	_, _ = hash.Write(raw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
