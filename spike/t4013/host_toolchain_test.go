package t4013

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostToolchainMismatchNamesOnlyTheClosedTool(t *testing.T) {
	expected := fakeHostToolchain()
	for index := range expected {
		observed := append([]HostToolObservation(nil), expected...)
		observed[index].SHA256 = "sha256:" + strings.Repeat("f", 64)
		err := compareHostToolchain(expected, observed)
		want := "T40.13 host toolchain differs from the frozen plan: " + expected[index].Name
		if err == nil || err.Error() != want ||
			strings.Contains(err.Error(), observed[index].Version) ||
			strings.Contains(err.Error(), observed[index].SHA256) {
			t.Fatalf("mismatch %d = %v, want %q", index, err, want)
		}
	}

	short := append([]HostToolObservation(nil), expected[:len(expected)-1]...)
	if err := compareHostToolchain(expected, short); err == nil ||
		err.Error() != "T40.13 host toolchain inventory differs from the frozen plan" {
		t.Fatalf("short inventory mismatch = %v", err)
	}
}

func TestBoundExecutableRefusesReplacementBeforeLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sha256, err := executableDigestContext(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	bound := boundExecutable{name: "tool", path: path, sha256: sha256}
	if _, err := bound.pathForLaunch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := bound.pathForLaunch(t.Context()); err == nil {
		t.Fatal("replacement executable passed its bound launch identity")
	}
}

func TestPrebindRefusesHostReplacementBeforeVersionLaunch(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "replacement-ran")
	paths := map[string]string{}
	for _, name := range []string{"go", "git", "surreal"} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	goSHA256, err := executableDigestContext(t.Context(), paths["go"])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths["go"], []byte("#!/bin/sh\n: > \"$T4013_MARKER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("T4013_MARKER", marker)
	expected := []HostToolObservation{
		{Name: "go", SHA256: goSHA256, PathSHA256: hostPathDigest(paths["go"])},
		{Name: "git", SHA256: digest([]byte("unused")), PathSHA256: hostPathDigest(paths["git"])},
		{Name: "surreal", SHA256: digest([]byte("unused")), PathSHA256: hostPathDigest(paths["surreal"])},
	}
	if _, err := prebindHostToolchain(t.Context(), expected); err == nil {
		t.Fatal("replacement host tool passed prebinding")
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement host tool ran before refusal: %v", err)
	}
}

func TestHostToolchainPathIdentityDetectsPathSearchDrift(t *testing.T) {
	expected := fakeHostToolchainV25()
	if err := validateHostToolchain(expected, true); err != nil {
		for index, value := range expected {
			if !digestIdentity(value.PathSHA256) {
				t.Fatalf("fake path identity %d (%s) = %q", index, value.Name, value.PathSHA256)
			}
		}
		t.Fatal(err)
	}
	observed := append([]HostToolObservation(nil), expected...)
	observed[0].PathSHA256 = hostPathDigest("/different/go")
	if err := compareHostToolchain(expected, observed); err == nil ||
		err.Error() != "T40.13 host toolchain differs from the frozen plan: go" {
		t.Fatalf("path drift = %v", err)
	}
}

func TestLaterTerminalChecksUseOnlyRetainedExecutableBindings(t *testing.T) {
	root := t.TempDir()
	bindings := make([]boundExecutable, 4)
	for index, name := range []string{"go", "git", "git-core", "surreal"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte{byte(index + 1)}, 0o700); err != nil {
			t.Fatal(err)
		}
		sha256, err := executableDigestContext(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		bindings[index] = boundExecutable{name: name, path: path, sha256: sha256}
	}
	run := &execution{
		plan: Plan{Schema: PlanSchemaV25}, hostTerminalVerified: true,
		hostTools: hostToolchainBinding{
			goDriver: bindings[0], git: bindings[1], gitCore: bindings[2], surreal: bindings[3],
		},
	}
	if err := run.verifyFrozenHostToolchain(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bindings[2].path, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run.verifyFrozenHostToolchain(); err == nil || !strings.Contains(err.Error(), "git-core") {
		t.Fatalf("later exact executable check = %v", err)
	}
}

func TestHostTreeDigestChangesWithBuildInput(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"src", filepath.Join("pkg", "include")} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "src", "input.go")
	if err := os.WriteFile(path, []byte("package input\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := hostTreeDigest(t.Context(), root, "src", filepath.Join("pkg", "include"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := hostTreeDigest(t.Context(), root, "src", filepath.Join("pkg", "include"))
	if err != nil || before == after {
		t.Fatalf("host tree digest did not change: %q %q, %v", before, after, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := hostTreeDigest(canceled, root, "src"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled host tree digest = %v", err)
	}
}

func TestClosedHostToolchainIgnoresAmbientSurrealOverride(t *testing.T) {
	bin := t.TempDir()
	pathSurreal := filepath.Join(bin, "surreal")
	overrideSurreal := filepath.Join(t.TempDir(), "surreal")
	for path, version := range map[string]string{pathSurreal: "3.0.0", overrideSurreal: "3.1.0"} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '"+version+"\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PHEBS_SURREAL", overrideSurreal)

	for _, test := range []struct {
		closed bool
		want   string
	}{
		{closed: false, want: "3.1.0"},
		{closed: true, want: "3.0.0"},
	} {
		var observed []HostToolObservation
		var err error
		if test.closed {
			observed, err = observeHostToolchain(t.Context(), true)
		} else {
			observed, err = ObserveHostToolchain(t.Context())
		}
		if err != nil {
			t.Fatal(err)
		}
		var got HostToolObservation
		for _, value := range observed {
			if value.Name == "surreal" {
				got = value
				break
			}
		}
		if got.Name != "surreal" || got.Version != test.want {
			t.Fatalf("closed=%t surreal = %+v, want version %s", test.closed, got, test.want)
		}
	}
}

func TestHostToolchainVerifierSelection(t *testing.T) {
	want := "T40.13 host toolchain identity inventory is incomplete"
	for name, verify := range map[string]func() error{
		"public legacy": func() error {
			return VerifyHostToolchain(t.Context(), fakeHostToolchainV25())
		},
		"v24": func() error {
			return verifyHostToolchainForPlan(t.Context(), Plan{
				Schema: PlanSchemaV24, HostToolchain: fakeHostToolchainV25(),
			})
		},
		"v25": func() error {
			return verifyHostToolchainForPlan(t.Context(), Plan{
				Schema: PlanSchemaV25, HostToolchain: fakeHostToolchain(),
			})
		},
	} {
		if err := verify(); err == nil || err.Error() != want {
			t.Fatalf("%s verifier = %v, want %q", name, err, want)
		}
	}
}

func TestHostToolchainEnvironmentChangesOnlyAtV25(t *testing.T) {
	t.Setenv("GOEXPERIMENT", "historical-ambient")
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	command := `printf '%s' "${GOEXPERIMENT:-closed}"`
	for _, test := range []struct {
		schema string
		want   string
	}{
		{PlanSchemaV20, "historical-ambient"},
		{PlanSchemaV21, "historical-ambient"},
		{PlanSchemaV23, "historical-ambient"},
		{PlanSchemaV24, "historical-ambient"},
		{PlanSchemaV25, "closed"},
	} {
		got, err := boundedCommand(
			t.Context(), planSchemaVersion(test.schema) >= 25,
			shell, "-c", command,
		)
		if err != nil || got != test.want {
			t.Fatalf("host environment for %s = %q, %v; want %q", test.schema, got, err, test.want)
		}
	}
}
