package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageCreatesOneIncompleteGenerationWithoutMovingCurrent(t *testing.T) {
	dataDirectory := t.TempDir()
	repository := "example/private-target"
	repositoryDigest := sha256.Sum256([]byte(repository))
	repositoryRoot := filepath.Join(
		dataDirectory, "observations", hex.EncodeToString(repositoryDigest[:]),
	)
	if err := os.MkdirAll(repositoryRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	current := pointer{
		Schema: "phebs-observation-pointer-v1", Repository: repository,
		GenerationDigest: "sha256:" + strings.Repeat("1", 64),
		ManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		ManifestName:     strings.Repeat("1", 64) + "/manifest.json",
	}
	raw, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(repositoryRoot, "current.json")
	if err := os.WriteFile(currentPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	generation, err := stage(dataDirectory, repository)
	if err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(currentPath); err != nil || string(after) != string(raw) {
		t.Fatalf("current pointer moved: bytes=%q err=%v", after, err)
	}
	markerRaw, err := os.ReadFile(filepath.Join(repositoryRoot, "publishing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var published marker
	if err := decodeCanonical(markerRaw, &published); err != nil {
		t.Fatal(err)
	}
	if published.Repository != repository || published.GenerationDigest != generation {
		t.Fatalf("marker = %+v generation=%q", published, generation)
	}
	generationRoot := filepath.Join(repositoryRoot, strings.TrimPrefix(generation, "sha256:"))
	if _, err := os.Stat(filepath.Join(generationRoot, "partial")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(generationRoot, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("incomplete generation unexpectedly has manifest: %v", err)
	}
	if _, err := stage(dataDirectory, repository); err == nil {
		t.Fatal("second interruption unexpectedly replaced the first marker")
	}
}
