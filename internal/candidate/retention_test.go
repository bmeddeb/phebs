package candidate

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidateid"
)

func TestRetentionArtifactNamesFollowDurablePackageOwnership(t *testing.T) {
	repository := "github.com/acme/service"
	base := candidateid.ArtifactBase(repository)
	generation := strings.Repeat("a", 64)
	tests := []struct {
		name string
		want bool
	}{
		{candidateid.ManifestName(repository), true},
		{base + "-v4-" + generation + "-repository-000001.ndjson", true},
		{base + "-v4-" + generation + "-caller-000001.ndjson", true},
		{base + "-v4-" + generation + "-local-001-000001.ndjson", true},
		// Pre-v4 writers placed the generation digest directly after the base.
		{base + "-" + generation + "-repository-000001.ndjson", true},
		// Unmatched package residue remains lifecycle-owned and must be counted.
		{base + "-v4-" + generation + "-repository-extra-000001.ndjson", true},
		{base + "-future-residue", true},
		{base + "-", false},
		{candidateid.PublishingName(repository), false},
		{base + ".manifest.json.bak", false},
		{".phebs-candidate-stage", false},
		{"../" + candidateid.ManifestName(repository), false},
	}
	for _, test := range tests {
		if got := IsRetentionArtifactName(test.name); got != test.want {
			t.Errorf("IsRetentionArtifactName(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}
