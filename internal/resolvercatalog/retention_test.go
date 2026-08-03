package resolvercatalog

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
)

func TestRetentionArtifactAndStageNames(t *testing.T) {
	repository := "github.com/acme/service"
	generation := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name string
		want bool
	}{
		{resolvercatalogid.ManifestName(repository), true},
		{resolvercatalogid.ArtifactPrefix(repository, generation) + "go.ndjson", true},
		{resolvercatalogid.PublishingName(repository), false},
		{"../" + resolvercatalogid.ManifestName(repository), false},
	}
	for _, test := range tests {
		if got := IsRetentionRootArtifactName(test.name); got != test.want {
			t.Errorf("IsRetentionRootArtifactName(%q) = %v, want %v", test.name, got, test.want)
		}
	}
	if !IsRetentionStageName(stageDirectoryPrefix+"abc123") ||
		IsRetentionStageName(stageDirectoryPrefix) ||
		IsRetentionStageName("../"+stageDirectoryPrefix+"abc123") {
		t.Fatal("resolver retention stage-name boundary is not exact")
	}
}
