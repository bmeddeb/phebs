package main

import (
	"os"
	"os/exec"
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
	if !strings.Contains(workflow, "  go-test:\n    name: Go full test\n    runs-on: ubuntu-latest\n    timeout-minutes: 90\n") {
		t.Error("full Go job must retain setup and scheduling headroom beyond its 60-minute package allowance")
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range []struct {
		target        string
		prerequisites string
		command       string
	}{
		{"test", "verify-test-surreal verify-glossary", "\tgo test ./... -timeout=60m"},
		{"ci-go", "verify-go verify-surreal", "\tgo test ./... -count=1 -timeout=60m"},
	} {
		// Match only this target's recipe, allowing its comment and engine
		// guard without accidentally accepting a command in another target.
		recipe := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(gate.target+": "+gate.prerequisites) + `(?:[ \t]+#[^\n]*)?\n(?:\t[^\n]*\n)*`).FindString(string(makefile))
		if !strings.Contains(recipe, gate.command+"\n") {
			t.Errorf("Makefile target %s is missing full-suite allowance %q", gate.target, gate.command)
		}
	}
	if !strings.Contains(string(makefile), `[ -n "$${PHEBS_SKIP_SURREAL_TESTS:-}" ] && [ "$$PHEBS_SKIP_SURREAL_TESTS" != 1 ]`) {
		t.Error("make test must reject misspelled SurrealDB skip values")
	}
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
		"make release VERSION=v0.2.1",
		"make smoke-release VERSION=v0.2.1",
		`cmp "$first/$bundle/release-manifest.json" "$second/$bundle/release-manifest.json"`,
		`sha256sum "$bundle.tar.gz"`,
		"if-no-files-found: error",
		"runs-on: ubuntu-24.04",
		"mcr.microsoft.com/playwright:v1.62.1-noble@sha256:c091b21d9fae78c76e85cd4356431e9b018402f172a214fc7d7a5e9a7e29d8ac",
		"PHEBS_RECEIPTS_BROWSER: chromium",
		"apt-get update",
		"apt-get install --yes --no-install-recommends make",
		"shell: bash",
		"cleanup_server()",
		"ready_deadline=$((SECONDS + 600))",
		"curl -fsS --connect-timeout 2 --max-time 5 http://127.0.0.1:3073/api/version",
	} {
		if !strings.Contains(workflow, gate) {
			t.Errorf("workflow is missing gate %q", gate)
		}
	}
	if strings.Contains(workflow, `sha256sum "dist/release/`) {
		t.Error("release checksum records a CI-internal path instead of the adjacent archive basename")
	}
	for _, job := range []string{"go-test", "race", "screenshots", "release"} {
		jobBody := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(job) + `:\n(?:[ ]{4,}[^\n]*\n|\n)*`).FindString(workflow)
		if count := strings.Count(jobBody, `sh scripts/install-surreal-ci.sh "$RUNNER_TEMP"`); count != 1 {
			t.Errorf("job %s pinned SurrealDB installer calls = %d, want 1", job, count)
		}
		if job == "screenshots" {
			for _, required := range []string{"apt-get update", "apt-get install --yes --no-install-recommends make"} {
				if !strings.Contains(jobBody, required) {
					t.Errorf("screenshots job is missing %q", required)
				}
			}
		}
	}
	if count := strings.Count(workflow, `sh scripts/install-surreal-ci.sh "$RUNNER_TEMP"`); count != 4 {
		t.Errorf("pinned SurrealDB installer calls = %d, want 4", count)
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

func TestMakeTestGuardRequiresSurrealOnPath(t *testing.T) {
	root := filepath.Clean("..")
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Fatal(err)
	}
	trPath, err := exec.LookPath("tr")
	if err != nil {
		t.Fatal(err)
	}
	testBin := t.TempDir()
	if err := os.Symlink(trPath, filepath.Join(testBin, "tr")); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(testBin, "surreal-override")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	baseEnv := []string{
		"LC_ALL=C",
		"PATH=" + testBin,
		"PHEBS_SURREAL=" + fake,
	}

	command := exec.Command(makePath, "--no-print-directory", "verify-test-surreal")
	command.Dir = root
	command.Env = baseEnv
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "binary not found in PATH") {
		t.Fatalf("runtime-only override passed test guard: err=%v output=%s", err, output)
	}

	command = exec.Command(makePath, "--no-print-directory", "verify-test-surreal")
	command.Dir = root
	command.Env = append(baseEnv, "PHEBS_SKIP_SURREAL_TESTS=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("explicit skip was refused: %v: %s", err, output)
	}

	command = exec.Command(makePath, "--no-print-directory", "verify-test-surreal")
	command.Dir = root
	command.Env = append(baseEnv, "PHEBS_SKIP_SURREAL_TESTS=true")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "must be exactly 1") {
		t.Fatalf("invalid skip value was accepted: err=%v output=%s", err, output)
	}
}

// TestReceiptFixtureStagingContract pins the R3 hardening of the receipt
// fixture staging path: the workflow must check the staging output before
// eval (so a failed staging run fails the step instead of silently
// succeeding on empty output), must invoke the script via `sh` (so the
// step does not depend on the executable bit surviving the push), and must
// run the staging regression tests in the static job. The staging script
// itself must refuse symlinks/foreign owners at the shared fixture root
// and destinations, reuse identical bytes, never overwrite differing
// bytes in place, and publish new bundles with create-only hard links.
func TestReceiptFixtureStagingContract(t *testing.T) {
	root := filepath.Clean("..")

	workflowBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	if !strings.Contains(workflow, `receipt_env="$(sh scripts/stage-receipt-fixtures.sh --env)" || exit 1`) {
		t.Error("workflow must capture the staging output and check it before eval")
	}
	if !strings.Contains(workflow, "eval \"$receipt_env\"") {
		t.Error("workflow must eval the checked staging output")
	}
	if strings.Contains(workflow, `eval "$(scripts/stage-receipt-fixtures.sh --env)"`) {
		t.Error("workflow still contains the unchecked eval that masked staging failures")
	}
	if !strings.Contains(workflow, "sh scripts/test-stage-receipt-fixtures.sh") {
		t.Error("workflow must run the receipt fixture staging regression tests")
	}
	configBytes, err := os.ReadFile(filepath.Join(root, "phebs-ux-dev.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(workflow, "sourcegraph/zoekt") || strings.Contains(string(configBytes), "sourcegraph/zoekt") {
		t.Error("receipt cohort must not clone mutable sourcegraph/zoekt HEAD")
	}

	scriptBytes, err := os.ReadFile(filepath.Join(root, "scripts", "stage-receipt-fixtures.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptBytes)
	for _, required := range []string{
		`mktemp "$fixture_root/.stage-bundle-XXXXXX"`,
		`cmp -s "$src" "$dst"`,
		"is a symlink",
		"not owned by",
		"refusing to overwrite in place",
		`link "$tmp" "$dst"`,
		`mkdir -m 700 "$fixture_root"`,
		"must have mode 0700 without an ACL",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("staging script is missing safety property %q", required)
		}
	}
	if strings.Contains(script, "mkdir -p \"$fixture_root\"") {
		t.Error("staging script must not mkdir -p the shared fixture root (it would follow a planted symlink)")
	}

	if _, err := os.Stat(filepath.Join(root, "scripts", "test-stage-receipt-fixtures.sh")); err != nil {
		t.Errorf("staging regression test script is missing: %v", err)
	}
}
