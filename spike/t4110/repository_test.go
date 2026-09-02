package t4110

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestT4110VerifyCleanCommitRejectsAnyCheckoutChange(t *testing.T) {
	root := t.TempDir()
	commands := [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "Neutral Gate"},
		{"config", "user.email", "gate@neutral.invalid"},
	}
	for _, arguments := range commands {
		if _, err := runCommand(t.Context(), root, "git", arguments...); err != nil {
			t.Fatal(err)
		}
	}
	tracked := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t.Context(), root, "git", "commit", "--quiet", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	commit, err := VerifyCleanCommit(t.Context(), root)
	if err != nil || !validCommit(commit) {
		t.Fatalf("clean commit = %q, %v", commit, err)
	}
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		if _, err := runCommand(t.Context(), root, "git", "update-index", flag, "tracked.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyCleanCommit(t.Context(), root); err == nil {
			t.Fatalf("hidden authoring checkout state %s was accepted", flag)
		}
		if _, err := runCommand(
			t.Context(), root, "git", "update-index",
			strings.Replace(flag, "--", "--no-", 1), "tracked.txt",
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCleanCommit(t.Context(), root); err == nil {
		t.Fatal("dirty authoring checkout was accepted")
	}
}

func TestT4110BuildFenceRejectsEveryModuleReplacement(t *testing.T) {
	replacement := &debug.Module{Path: "replacement.invalid/module", Version: "v1.0.0"}
	for _, info := range []*debug.BuildInfo{
		{Main: debug.Module{Path: "example.invalid/main", Replace: replacement}},
		{Deps: []*debug.Module{{Path: "example.invalid/dependency", Replace: replacement}}},
	} {
		if err := rejectGoModuleReplacements(info); err == nil {
			t.Fatal("Go module replacement was admitted")
		}
	}
	if err := rejectGoModuleReplacements(&debug.BuildInfo{
		Main: debug.Module{Path: "example.invalid/main"},
		Deps: []*debug.Module{{Path: "example.invalid/dependency", Version: "v1.0.0"}},
	}); err != nil {
		t.Fatalf("unreplaced Go build identity = %v", err)
	}
}

func TestT4110PhebsBinaryFenceRejectsNonGoBinary(t *testing.T) {
	if _, err := verifyPhebsBinaryCommit(
		"/bin/sh",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	); err == nil {
		t.Fatal("non-Go Phebs binary was accepted")
	}
}

func TestT4110BuildFenceRequiresDefaultArchitecture(t *testing.T) {
	settings := map[string]string{}
	switch runtime.GOARCH {
	case "386":
		settings["GO386"] = "sse2"
	case "amd64":
		settings["GOAMD64"] = "v1"
	case "arm":
		settings["GOARM"] = "7"
	case "arm64":
		settings["GOARM64"] = "v8.0"
	default:
		t.Skip("architecture has no frozen feature setting")
	}
	if !defaultArchitectureSetting(settings) {
		t.Fatal("default architecture setting was refused")
	}
	for key := range settings {
		settings[key] = "drift"
	}
	if defaultArchitectureSetting(settings) {
		t.Fatal("drifted architecture setting was admitted")
	}
}

func TestT4110ClosedGoSettingsRejectFIPSLatestAndRequireDefaultArchitecture(t *testing.T) {
	settings := map[string]string{
		"-trimpath":    "true",
		"CGO_ENABLED":  "0",
		"GOOS":         runtime.GOOS,
		"GOARCH":       runtime.GOARCH,
		"GOEXPERIMENT": "",
		"GOFIPS140":    "off",
	}
	architectureKey := ""
	switch runtime.GOARCH {
	case "386":
		architectureKey, settings["GO386"] = "GO386", "sse2"
	case "amd64":
		architectureKey, settings["GOAMD64"] = "GOAMD64", "v1"
	case "arm":
		architectureKey, settings["GOARM"] = "GOARM", "7"
	case "arm64":
		architectureKey, settings["GOARM64"] = "GOARM64", "v8.0"
	default:
		t.Skip("architecture has no frozen feature setting")
	}
	if !closedGoSettings(settings) {
		t.Fatal("closed default Go settings were refused")
	}
	settings["GOFIPS140"] = "latest"
	if closedGoSettings(settings) {
		t.Fatal("GOFIPS140=latest was admitted")
	}
	settings["GOFIPS140"] = "off"
	settings[architectureKey] = "drift"
	if closedGoSettings(settings) {
		t.Fatal("non-default architecture setting was admitted")
	}
}
