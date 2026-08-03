package focusedindex

import (
	"strings"
	"testing"
)

func TestRetentionArtifactNamesExcludeWholeAndControls(t *testing.T) {
	repository := "github.com/acme/service"
	shard := ShardPrefix(repository, "sha256:"+strings.Repeat("a", 64)) + "_v16.00000.zoekt"
	tests := []struct {
		name string
		want bool
	}{
		{ManifestName(repository), true},
		{shard, true},
		{shard + MemberSuffix, true},
		{PublishingName(repository), false},
		{WholeManifestName(repository), false},
		{WholeShardName(repository, 16, 0), false},
		{"../" + shard, false},
	}
	for _, test := range tests {
		if got := IsRetentionArtifactName(test.name); got != test.want {
			t.Errorf("IsRetentionArtifactName(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}
