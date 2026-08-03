package callerpublication

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

func TestRetentionNamesExcludeControlsAndAliases(t *testing.T) {
	repository := "github.com/acme/service"
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := callerpublicationid.ManifestName(digest, digest)
	leaf := callerleafid.ArtifactName(digest, digest)
	if !IsRetentionRepositoryDirectoryName(callerpublicationid.RepositoryDirectory(repository)) ||
		IsRetentionRepositoryDirectoryName("../"+callerpublicationid.RepositoryDirectory(repository)) {
		t.Fatal("caller retention repository-directory boundary is not exact")
	}
	for _, name := range []string{manifest, leaf} {
		if !IsRetentionArtifactName(name) {
			t.Fatalf("valid retention artifact %q rejected", name)
		}
	}
	for _, name := range []string{
		callerpublicationid.PublishingName,
		callerpublicationid.DeletingName,
		callerpublicationid.StagePrefix + strings.Repeat("b", 32),
		callerleafid.StagePrefix + strings.Repeat("c", 32),
	} {
		if !IsRetentionIgnoredControlName(name) || IsRetentionArtifactName(name) {
			t.Fatalf("caller control %q crossed retention artifact boundary", name)
		}
	}
	if IsRetentionArtifactName("../" + leaf) {
		t.Fatal("caller artifact path alias accepted")
	}
}
