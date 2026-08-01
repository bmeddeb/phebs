package callerleafid

import "testing"

func TestNamesAreStableAndRepositoryScoped(t *testing.T) {
	left := RepositoryDirectory("github.com/acme/one")
	right := RepositoryDirectory("github.com/acme/two")
	if left == right || len(left) != len("phebs-caller-overlay-")+64 {
		t.Fatalf("repository directories = %q %q", left, right)
	}
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	content := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if got, want := ArtifactName(digest, content),
		"leaf-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.ndjson"; got != want {
		t.Fatalf("ArtifactName = %q, want %q", got, want)
	}
}
