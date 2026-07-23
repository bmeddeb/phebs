package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCIContractPinsToolsAndNamedGates(t *testing.T) {
	root := filepath.Clean("..")
	pins := []struct {
		path string
		want string
	}{
		{path: ".go-version", want: "1.26.5"},
		{path: ".node-version", want: "24.18.0"},
		{path: ".golangci-lint-version", want: "v2.12.2"},
		{path: ".surrealdb-version", want: "3.2.0"},
		{
			path: ".surrealdb-linux-amd64.sha256",
			want: "9c0a9ae29444f3b144a1261fc923116b0e10a3cbadc478cabc9009b3beb9bb3a",
		},
	}
	for _, pin := range pins {
		t.Run(pin.path, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, pin.path))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(content)); got != pin.want {
				t.Fatalf("%s = %q, want %q", pin.path, got, pin.want)
			}
		})
	}

	workflowBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	for _, exact := range []string{
		"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
		"actions/setup-node@820762786026740c76f36085b0efc47a31fe5020",
		"golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a",
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
	} {
		if !strings.Contains(workflow, exact) {
			t.Errorf("workflow does not pin %q", exact)
		}
	}
	floatingAction := regexp.MustCompile(`(?m)^\s*-\s+uses:\s+\S+@(main|master|v[0-9]+)(?:\s+#.*)?$`)
	if match := floatingAction.FindString(workflow); match != "" {
		t.Errorf("workflow contains floating action ref %q", strings.TrimSpace(match))
	}
	for _, forbidden := range []string{
		"version: latest",
		"go-version-file: go.mod",
		"node-version: 24",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("workflow contains floating tool selector %q", forbidden)
		}
	}
	for _, gate := range []string{
		"name: Go static and compile",
		"name: Go full test",
		"name: Go concurrency race",
		"name: UI test, lint, and embedded build",
		"name: Release bundle and fresh-data smoke",
		"run: make ci-static",
		"run: make ci-go",
		"run: make ci-race",
		"run: make ci-ui",
		"make release VERSION=v0.1.0",
		"make smoke-release VERSION=v0.1.0",
		`cmp "$first/$bundle/release-manifest.json" "$second/$bundle/release-manifest.json"`,
		`sha256sum "dist/release/$bundle.tar.gz"`,
		"if-no-files-found: error",
	} {
		if !strings.Contains(workflow, gate) {
			t.Errorf("workflow is missing gate %q", gate)
		}
	}
	if count := strings.Count(workflow, `sh scripts/install-surreal-ci.sh "$RUNNER_TEMP"`); count != 3 {
		t.Errorf("pinned SurrealDB installer calls = %d, want 3", count)
	}
	installerBytes, err := os.ReadFile(filepath.Join(root, "scripts", "install-surreal-ci.sh"))
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerBytes)
	for _, required := range []string{
		"github.com/surrealdb/surrealdb/releases/download/v$version/",
		".surrealdb-linux-amd64.sha256",
		"sha256sum --check -",
		`"$bin_dir/surreal" version`,
	} {
		if !strings.Contains(installer, required) {
			t.Errorf("SurrealDB installer is missing %q", required)
		}
	}
}
