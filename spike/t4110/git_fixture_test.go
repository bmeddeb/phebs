package t4110

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t411"
)

func TestGitEnvironmentScrubsInheritedGitVariables(t *testing.T) {
	t.Setenv("GIT_DIR", "/attacker/repository")
	t.Setenv("GIT_OBJECT_DIRECTORY", "/attacker/objects")
	t.Setenv("GIT_TRACE", "/attacker/trace")
	for _, entry := range gitEnvironment() {
		if strings.HasPrefix(entry, "GIT_DIR=") ||
			strings.HasPrefix(entry, "GIT_OBJECT_DIRECTORY=") ||
			strings.HasPrefix(entry, "GIT_TRACE=") {
			t.Fatalf("inherited Git environment escaped: %q", entry)
		}
	}
}

func TestLiveAuthorRefusesAmbientGitVariables(t *testing.T) {
	t.Setenv("GIT_OBJECT_DIRECTORY", "/attacker/objects")
	if err := verifyNoAmbientGitEnvironment(); err == nil {
		t.Fatal("ambient Git environment was accepted")
	}
}

func TestWriteFastImportRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{
		"../escape.go", "line\nbreak.go", "nul\x00byte.go", `quoted"path.go`,
		`back\\slash.go`, "white space.go",
	} {
		var output bytes.Buffer
		err := writeFastImport(&output, []t411.FixtureFile{{
			Path: path, Content: []byte("fixture\n"),
		}})
		if err == nil || output.Len() != 0 {
			t.Fatalf("unsafe path %q: bytes=%d error=%v", path, output.Len(), err)
		}
	}
	var output bytes.Buffer
	err := writeFastImport(&output, []t411.FixtureFile{
		{Path: "a.go", Content: []byte("fixture\n")},
		{Path: "bad path.go", Content: []byte("fixture\n")},
	})
	if err == nil || output.Len() != 0 {
		t.Fatalf("late unsafe path: bytes=%d error=%v", output.Len(), err)
	}
}
