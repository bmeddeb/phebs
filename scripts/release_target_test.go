package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// supportedReleaseTargets is the platform set fixed by the 2026-10-07 PLAN
// decision: 64-bit only. It is restated here so that widening the Makefile
// variable without a matching dated ADR fails a gate instead of passing
// silently.
var supportedReleaseTargets = "linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

func runValidateReleaseTarget(t *testing.T, goos, goarch string) (string, error) {
	t.Helper()
	command := exec.Command("make", "validate-release-target",
		"TARGET_GOOS="+goos, "TARGET_GOARCH="+goarch)
	command.Dir = filepath.Clean("..")
	out, err := command.CombinedOutput()
	return string(out), err
}

// TestValidateReleaseTargetRefusesUnsupportedPlatforms pins the refusal side of
// the policy: 32-bit and non-Linux/Darwin targets must be rejected before any
// build work, the refusal must name the supported set so it is actionable, and
// the 32-bit explanation must appear for genuine 32-bit GOARCH values only.
// Claiming 32-bit for an unsupported 64-bit target such as linux/riscv64 would
// be a misleading diagnostic, so its absence is asserted too.
func TestValidateReleaseTargetRefusesUnsupportedPlatforms(t *testing.T) {
	tests := []struct {
		goos     string
		goarch   string
		wantHint bool
	}{
		{"linux", "386", true},
		{"linux", "arm", true},
		{"linux", "mips", true},
		{"linux", "mipsle", true},
		{"linux", "ppc", true},
		{"linux", "s390", true},
		{"windows", "amd64", false},
		{"freebsd", "amd64", false},
		{"linux", "riscv64", false},
		{"linux", "loong64", false},
	}
	for _, test := range tests {
		target := test.goos + "/" + test.goarch
		t.Run(target, func(t *testing.T) {
			out, err := runValidateReleaseTarget(t, test.goos, test.goarch)
			if err == nil {
				t.Fatalf("unsupported release target %s was accepted:\n%s", target, out)
			}
			if !strings.Contains(out, "release target "+target+" is not supported") {
				t.Errorf("refusal does not name the target:\n%s", out)
			}
			if !strings.Contains(out, "supported release targets: "+supportedReleaseTargets) {
				t.Errorf("refusal does not name the supported set:\n%s", out)
			}
			if got := strings.Contains(out, "64-bit only"); got != test.wantHint {
				t.Errorf("32-bit explanation present=%v, want %v:\n%s", got, test.wantHint, out)
			}
		})
	}
}

// TestValidateReleaseTargetDoesNotCallSupportedArm64A32BitTarget guards the one
// pattern-matching hazard in the refusal: linux/arm64 must not be caught by a
// prefix rule written for linux/arm, or a supported target would be refused.
func TestValidateReleaseTargetDoesNotCallSupportedArm64A32BitTarget(t *testing.T) {
	for _, target := range []struct{ goos, goarch string }{{"linux", "arm64"}, {"darwin", "arm64"}} {
		out, _ := runValidateReleaseTarget(t, target.goos, target.goarch)
		if strings.Contains(out, "is not supported") {
			t.Errorf("%s/%s was refused as unsupported:\n%s", target.goos, target.goarch, out)
		}
		if strings.Contains(out, "64-bit only") {
			t.Errorf("%s/%s was misreported as 32-bit:\n%s", target.goos, target.goarch, out)
		}
	}
}

// TestValidateReleaseTargetAcceptsHostPlatform keeps the guard from blocking a
// legitimate release: whatever platform the suite runs on must not be refused
// as unsupported. It may still fail the separate, pre-existing requirement that
// the release target be executable on the smoke host, which is not this policy.
func TestValidateReleaseTargetAcceptsHostPlatform(t *testing.T) {
	goos := goEnv(t, "GOOS")
	goarch := goEnv(t, "GOARCH")
	out, err := runValidateReleaseTarget(t, goos, goarch)
	if strings.Contains(out, "is not supported") {
		t.Fatalf("host platform %s/%s was refused as unsupported:\n%s", goos, goarch, out)
	}
	if err != nil && !strings.Contains(out, "is not executable on this") {
		t.Fatalf("host platform %s/%s failed for an unexpected reason:\n%s", goos, goarch, out)
	}
}

// TestSupportedReleaseTargetsMatchTheRecordedDecision ties the Makefile variable
// to the documented matrix, so the policy cannot be widened or narrowed in one
// place only.
func TestSupportedReleaseTargetsMatchTheRecordedDecision(t *testing.T) {
	root := filepath.Clean("..")
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^SUPPORTED_RELEASE_TARGETS := (.+)$`).FindStringSubmatch(string(makefile))
	if match == nil {
		t.Fatal("Makefile does not declare SUPPORTED_RELEASE_TARGETS")
	}
	if got := strings.TrimSpace(match[1]); got != supportedReleaseTargets {
		t.Fatalf("SUPPORTED_RELEASE_TARGETS = %q, want %q; changing it needs a dated PLAN ADR", got, supportedReleaseTargets)
	}
	for _, doc := range []struct {
		path string
		want string
	}{
		{"docs/guides/GETTING_STARTED.md", "64-bit platforms only"},
		{"AGENTS.md", "Supported build platforms; 32-bit is out of scope"},
		{"PLAN.md", "Supported build platforms; 32-bit targets are out of scope"},
	} {
		content, err := os.ReadFile(filepath.Join(root, doc.path))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), doc.want) {
			t.Errorf("%s no longer records the platform decision %q", doc.path, doc.want)
		}
	}
}

func goEnv(t *testing.T, key string) string {
	t.Helper()
	out, err := exec.Command("go", "env", key).CombinedOutput()
	if err != nil {
		t.Fatalf("go env %s: %v\n%s", key, err, out)
	}
	return strings.TrimSpace(string(out))
}
