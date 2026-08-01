package callerpublicationid

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleafid"
)

func TestNamesAreCanonicalAndRepositoryScoped(t *testing.T) {
	generation := "sha256:" + strings.Repeat("1", 64)
	manifest := "sha256:" + strings.Repeat("a", 64)
	name := ManifestName(generation, manifest)
	want := "complete-v1-" + strings.Repeat("1", 64) + "-" +
		strings.Repeat("a", 64) + ".manifest.json"
	if name != want || !ValidManifestName(name) {
		t.Fatalf("ManifestName = %q, valid=%v", name, ValidManifestName(name))
	}
	gotGeneration, gotManifest, ok := ManifestDigests(name)
	if !ok || gotGeneration != generation || gotManifest != manifest {
		t.Fatalf("ManifestDigests = %q, %q, %v", gotGeneration, gotManifest, ok)
	}
	const repository = "github.com/acme/service"
	if got := RepositoryDirectory(repository); got != callerleafid.RepositoryDirectory(repository) {
		t.Fatalf("RepositoryDirectory = %q", got)
	}
}

func TestNamesRejectAliasesAndPaths(t *testing.T) {
	valid := ManifestName(
		"sha256:"+strings.Repeat("1", 64),
		"sha256:"+strings.Repeat("2", 64),
	)
	for _, name := range []string{
		"../" + valid,
		"nested/" + valid,
		strings.Replace(valid, "1", "A", 1),
		strings.TrimSuffix(valid, ".json"),
		valid + ".json",
	} {
		if ValidManifestName(name) {
			t.Fatalf("accepted manifest alias %q", name)
		}
	}
	stage := StagePrefix + strings.Repeat("b", 32)
	if !ValidStageName(stage) || ValidStageName("../"+stage) ||
		ValidStageName(StagePrefix+strings.Repeat("b", 31)) {
		t.Fatalf("stage validation differs for %q", stage)
	}
}
