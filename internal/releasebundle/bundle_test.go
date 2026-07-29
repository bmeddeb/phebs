package releasebundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIsDeterministicAndVerifiable(t *testing.T) {
	root := t.TempDir()
	inputs := testInputs(t, root)
	var manifests [][]byte
	for _, name := range []string{"one", "two"} {
		output := filepath.Join(root, name)
		manifest, err := Build(BuildOptions{
			OutputDir: output,
			Version:   "v0.1.0",
			Commit:    strings.Repeat("a", 40),
			GoVersion: "1.26.5",
			GOOS:      "linux",
			GOARCH:    "amd64",
			Sources:   inputs,
		})
		if err != nil {
			t.Fatalf("Build(%s): %v", name, err)
		}
		if manifest.Schema != Schema || len(manifest.Files) != len(inputs) {
			t.Fatalf("manifest = %#v", manifest)
		}
		if _, err := Verify(output); err != nil {
			t.Fatalf("Verify(%s): %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(output, ManifestName))
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, data)
	}
	if !bytes.Equal(manifests[0], manifests[1]) {
		t.Fatalf("same inputs produced different manifests:\n%s\n%s", manifests[0], manifests[1])
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{
			name: "phebs executable bytes",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				appendTamper(t, filepath.Join(dir, "phebs"))
			},
		},
		{
			name: "zoekt executable bytes",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				appendTamper(t, filepath.Join(dir, "bin", "zoekt-git-index"))
			},
		},
		{
			name: "buf executable bytes",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				appendTamper(t, filepath.Join(dir, "bin", "buf"))
			},
		},
		{
			name: "mode",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Chmod(filepath.Join(dir, "bin", "buf"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra file",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "extra"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra directory",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(dir, "unmanifested"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing file",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "LICENSE")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("README.md", filepath.Join(dir, "LICENSE")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "noncanonical manifest",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				path := filepath.Join(dir, ManifestName)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = append([]byte(" "), data...)
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "bundle")
			if _, err := Build(BuildOptions{
				OutputDir: output, Version: "v0.1.0", Commit: strings.Repeat("a", 40),
				GoVersion: "1.26.5", GOOS: "linux", GOARCH: "amd64",
				Sources: testInputs(t, root),
			}); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, output)
			if _, err := Verify(output); !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("Verify() error = %v, want ErrInvalidBundle", err)
			}
		})
	}
}

func TestBuildFailsClosed(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "source")
	if err := os.WriteFile(regular, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		opts BuildOptions
	}{
		{name: "empty metadata", opts: BuildOptions{OutputDir: filepath.Join(root, "empty"), Sources: []Source{{Path: "phebs", SourcePath: regular, Executable: true}}}},
		{name: "unsafe path", opts: baseOptions(filepath.Join(root, "unsafe"), []Source{{Path: "../phebs", SourcePath: regular, Executable: true}})},
		{name: "duplicate path", opts: baseOptions(filepath.Join(root, "duplicate"), []Source{
			{Path: "phebs", SourcePath: regular, Executable: true},
			{Path: "phebs", SourcePath: regular, Executable: true},
		})},
	}
	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	tests = append(tests, struct {
		name string
		opts BuildOptions
	}{name: "existing output", opts: baseOptions(existing, []Source{{Path: "phebs", SourcePath: regular, Executable: true}})})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Build(test.opts); !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("Build() error = %v, want ErrInvalidBundle", err)
			}
		})
	}
}

func testInputs(t *testing.T, root string) []Source {
	t.Helper()
	write := func(name, content string, mode os.FileMode) string {
		t.Helper()
		path := filepath.Join(root, "input-"+strings.ReplaceAll(name, "/", "-"))
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return []Source{
		{Path: "phebs", SourcePath: write("phebs", "server", 0o755), Executable: true},
		{Path: "bin/zoekt-git-index", SourcePath: write("zoekt", "indexer", 0o755), Executable: true},
		{Path: "bin/phebs-focused-index", SourcePath: write("focused", "focused-indexer", 0o755), Executable: true},
		{Path: "bin/buf", SourcePath: write("buf", "compat", 0o755), Executable: true},
		{Path: "LICENSE", SourcePath: write("license", "license", 0o644)},
		{Path: "README.md", SourcePath: write("readme", "readme", 0o644)},
		{Path: "phebs-otel-demo.yaml", SourcePath: write("otel-demo", "connections: []\n", 0o644)},
	}
}

func baseOptions(output string, sources []Source) BuildOptions {
	return BuildOptions{
		OutputDir: output, Version: "v0.1.0", Commit: strings.Repeat("a", 40),
		GoVersion: "1.26.5", GOOS: "linux", GOARCH: "amd64", Sources: sources,
	}
}

func appendTamper(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
