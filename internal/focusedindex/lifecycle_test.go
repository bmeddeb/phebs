package focusedindex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleCleanupPreservesCurrentProcessAndReclaimsResidue(t *testing.T) {
	indexDir := t.TempDir()
	currentBuild, _, err := NewBuildWorkspace(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := strings.Repeat("0", 32)
	if oldToken == lifecycleOwner {
		oldToken = strings.Repeat("1", 32)
	}
	oldWorkspaces := []string{
		buildWorkspacePrefix + oldToken + "-build",
		restoreWorkspacePrefix + oldToken + "-restore",
	}
	for _, name := range oldWorkspaces {
		if err := os.Mkdir(filepath.Join(indexDir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	repository := "example.com/team/repo"
	currentTemp := "." + PublishingName(repository) + "." + lifecycleOwner + ".current"
	oldTemp := "." + PublishingName(repository) + "." + oldToken + ".old"
	for _, name := range []string{currentTemp, oldTemp} {
		if err := os.WriteFile(filepath.Join(indexDir, name), []byte("marker"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	report, err := CleanupAbandonedLifecycle(indexDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Workspaces != len(oldWorkspaces) || report.TemporaryMarkers != 1 {
		t.Fatalf("lifecycle cleanup report = %+v", report)
	}
	for _, path := range []string{
		currentBuild,
		filepath.Join(indexDir, currentTemp),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("current-process lifecycle path %q was removed: %v", path, err)
		}
	}
	for _, name := range append(oldWorkspaces, oldTemp) {
		if _, err := os.Lstat(filepath.Join(indexDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("abandoned lifecycle path %q survived: %v", name, err)
		}
	}
}

func TestPublicationMarkerCarriesCurrentProcessOwnership(t *testing.T) {
	indexDir := t.TempDir()
	repository := "example.com/team/repo"
	if err := startPublication(indexDir, repository); err != nil {
		t.Fatal(err)
	}
	if !PublicationMarkerOwnedByCurrentProcess(indexDir, repository) {
		t.Fatal("new publication marker does not carry current process ownership")
	}
	if err := os.WriteFile(
		filepath.Join(indexDir, PublishingName(repository)),
		[]byte(repository+"\nprior-process\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if PublicationMarkerOwnedByCurrentProcess(indexDir, repository) {
		t.Fatal("prior-process publication marker was treated as active")
	}
}
