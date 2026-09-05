//go:build darwin || linux

package t421

import (
	"context"
	"debug/buildinfo"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func TestVerifyExecutionReferenceToolComparesExactBinaryBytes(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	fixture.write(t, "go.mod", "module github.com/bmeddeb/phebs\n\ngo 1.26\n")
	fixture.write(t, "go.sum", "")
	fixture.write(t, ".gitignore", "/ignored/\n/ignored_test.go\n/cmd/phebs-focused-index/ignored.go\n")
	fixture.write(t, "cmd/phebs-focused-index/main.go", "package main\nvar message = \"exact\"\nfunc main() { println(message) }\n")
	fixture.command(t, "add", "go.mod", "go.sum", ".gitignore", "cmd/phebs-focused-index/main.go")
	fixture.source = fixture.commit(t, "implemented neutral command")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goCommand := exec.CommandContext(t.Context(), goBinary, "env", "GOROOT")
	goCommand.Dir = t.TempDir()
	goCommand.Env = []string{"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "PATH=/usr/bin:/bin", "LC_ALL=C"}
	goRootRaw, err := goCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	goRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(goRootRaw)))
	if err != nil {
		t.Fatal(err)
	}
	moduleCache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := ReferenceToolRequest{
		RepositoryRoot: fixture.root, GitBinary: fixture.git, GoRoot: goRoot, ModuleCache: moduleCache,
		PlanSourceCommit: fixture.plan, IntegratedMainCommit: fixture.integration, SourceCommit: fixture.source,
		Role: "phebs-focused-index",
	}
	workspace := newReferenceToolBuildWorkspace(t, request)
	request.Binary = filepath.Join(workspace, "supplied-clean")
	buildReferenceToolFixture(t, request, workspace)
	cleanInfo, err := buildinfo.ReadFile(request.Binary)
	if err != nil {
		t.Fatal(err)
	}
	cleanDigest, err := executableidentity.Digest(request.Binary)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"GOFLAGS": "-this-ambient-flag-must-not-be-read", "GOENV": filepath.Join(workspace, "missing-go-env"),
		"GOWORK": filepath.Join(workspace, "missing-workspace"), "GIT_DIR": filepath.Join(workspace, "missing-git"),
	} {
		t.Setenv(name, value)
	}
	identity, err := VerifyExecutionReferenceTool(t.Context(), request)
	if err != nil {
		t.Fatalf("exact independently rebuilt tool refused: %v", err)
	}
	want := ExecutionToolIdentity{
		Role: request.Role, FileType: regularFileType, SHA256: cleanDigest,
		Version: "clean commit " + fixture.source, Provenance: "go-build-info-vcs-v1", BuildVCSRevision: fixture.source,
	}
	if identity != want {
		t.Fatalf("exact reference identity = %#v, want %#v", identity, want)
	}

	// Ignored Go files are compiled but do not make Go's VCS stamp dirty. The
	// complete reference image comparison must reject this metadata-identical lie.
	fixture.write(t, "cmd/phebs-focused-index/ignored.go", "package main\nfunc init() { message = \"injected\" }\n")
	request.Binary = filepath.Join(workspace, "supplied-ignored-input")
	buildReferenceToolFixture(t, request, workspace)
	if err := os.Remove(filepath.Join(fixture.root, "cmd/phebs-focused-index/ignored.go")); err != nil {
		t.Fatal(err)
	}
	changedInfo, err := buildinfo.ReadFile(request.Binary)
	if err != nil || !reflect.DeepEqual(changedInfo, cleanInfo) {
		t.Fatalf("ignored-source test did not preserve identical build metadata: %v", err)
	}
	changedDigest, err := executableidentity.Digest(request.Binary)
	if err != nil || changedDigest == cleanDigest {
		t.Fatalf("ignored-source test did not alter executable bytes: %v", err)
	}
	if identity, err := VerifyExecutionReferenceTool(t.Context(), request); err == nil || identity != (ExecutionToolIdentity{}) {
		t.Fatalf("metadata-identical changed executable admitted: %#v, %v", identity, err)
	}
}

func TestReferenceBuildInfoRefusesMissingReplacedOrWrongAuthority(t *testing.T) {
	commit := strings.Repeat("a", 40)
	packagePath := "github.com/bmeddeb/phebs/cmd/phebs-focused-index"
	baseline := func() *debug.BuildInfo {
		return &debug.BuildInfo{
			GoVersion: runtime.Version(), Path: packagePath, Main: debug.Module{Path: "github.com/bmeddeb/phebs", Version: "(devel)"},
			Settings: []debug.BuildSetting{
				{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"},
				{Key: "GOOS", Value: runtime.GOOS}, {Key: "GOARCH", Value: runtime.GOARCH},
				{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: "false"},
			},
		}
	}
	if err := validateReferenceBuildInfo(baseline(), packagePath, commit, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(**debug.BuildInfo)
	}{
		{"missing", func(info **debug.BuildInfo) { *info = nil }},
		{"wrong role", func(info **debug.BuildInfo) { (*info).Path = "github.com/bmeddeb/phebs/cmd/phebs" }},
		{"wrong toolchain", func(info **debug.BuildInfo) { (*info).GoVersion = "go0.0.0" }},
		{"wrong main module", func(info **debug.BuildInfo) { (*info).Main.Path = "example.invalid/other" }},
		{"main replacement", func(info **debug.BuildInfo) { (*info).Main.Replace = &debug.Module{Path: "example.invalid/replaced"} }},
		{"missing dependency", func(info **debug.BuildInfo) { (*info).Deps = []*debug.Module{nil} }},
		{"dependency replacement", func(info **debug.BuildInfo) {
			(*info).Deps = []*debug.Module{{Path: "example.invalid/dependency", Replace: &debug.Module{Path: "example.invalid/replaced"}}}
		}},
		{"missing settings", func(info **debug.BuildInfo) { (*info).Settings = nil }},
		{"wrong source", func(info **debug.BuildInfo) { (*info).Settings[5].Value = strings.Repeat("b", 40) }},
		{"dirty source", func(info **debug.BuildInfo) { (*info).Settings[6].Value = "true" }},
		{"CGO", func(info **debug.BuildInfo) { (*info).Settings[0].Value = "1" }},
		{"untrimmed path", func(info **debug.BuildInfo) { (*info).Settings[1].Value = "false" }},
		{"wrong host", func(info **debug.BuildInfo) { (*info).Settings[2].Value = "other" }},
		{"duplicate setting", func(info **debug.BuildInfo) { (*info).Settings = append((*info).Settings, (*info).Settings[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := baseline()
			test.mutate(&info)
			if err := validateReferenceBuildInfo(info, packagePath, commit, "", "", "", nil); err == nil {
				t.Fatal("invalid repository build authority was admitted")
			}
		})
	}
	packagePath, modulePath, version, sum, _, err := referenceToolRole("buf")
	if err != nil {
		t.Fatal(err)
	}
	moduleInfo := func() *debug.BuildInfo {
		info := baseline()
		info.Path = packagePath
		info.Main = debug.Module{Path: modulePath, Version: version, Sum: sum}
		info.Settings = info.Settings[:4]
		return info
	}
	modules := map[string]string{modulePath + "@" + version: sum}
	if err := validateReferenceBuildInfo(moduleInfo(), packagePath, commit, modulePath, version, sum, modules); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*debug.BuildInfo)
	}{
		{"module path", func(info *debug.BuildInfo) { info.Main.Path = "example.invalid/other" }},
		{"module version", func(info *debug.BuildInfo) { info.Main.Version = "v0.0.0" }},
		{"module sum", func(info *debug.BuildInfo) { info.Main.Sum = "h1:wrong" }},
		{"module VCS authority", func(info *debug.BuildInfo) {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "vcs.revision", Value: commit})
		}},
		{"unverified dependency", func(info *debug.BuildInfo) {
			info.Deps = []*debug.Module{{Path: "example.invalid/dependency", Version: "v1.0.0", Sum: "h1:unverified"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := moduleInfo()
			test.mutate(info)
			if err := validateReferenceBuildInfo(info, packagePath, commit, modulePath, version, sum, modules); err == nil {
				t.Fatal("invalid frozen module authority was admitted")
			}
		})
	}
	if err := validateReferenceBuildInfo(moduleInfo(), packagePath, commit, modulePath, version, sum, map[string]string{}); err == nil {
		t.Fatal("self-reported module pin replaced independent verification")
	}
}

func TestVerifyExecutionReferenceToolRefusesUnavailableRolesBeforeWork(t *testing.T) {
	for _, role := range []string{"t422-execute", "git", "go", "surreal", ""} {
		t.Run(role, func(t *testing.T) {
			if identity, err := VerifyExecutionReferenceTool(t.Context(), ReferenceToolRequest{Role: role}); err == nil || identity != (ExecutionToolIdentity{}) {
				t.Fatalf("unimplemented/non-Go role admitted: %#v, %v", identity, err)
			}
		})
	}
}

func TestReferenceAuthorRoleUsesExactSourceAuthority(t *testing.T) {
	path, module, version, sum, recipe, err := referenceToolRole("t422-author")
	if err != nil || path != "github.com/bmeddeb/phebs/spike/t422/cmd/author" || module != "" || version != "" || sum != "" || recipe != "" {
		t.Fatalf("author role lost exact checkout authority: %q, %q, %q, %q, %q, %v", path, module, version, sum, recipe, err)
	}
	commit := strings.Repeat("a", 40)
	info := &debug.BuildInfo{GoVersion: runtime.Version(), Path: path, Main: debug.Module{Path: "github.com/bmeddeb/phebs", Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"},
			{Key: "GOOS", Value: runtime.GOOS}, {Key: "GOARCH", Value: runtime.GOARCH},
			{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: "false"},
		}}
	if err := validateReferenceBuildInfo(info, path, commit, module, version, sum, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	info.Path = "github.com/bmeddeb/phebs/cmd/phebs"
	if err := validateReferenceBuildInfo(info, path, commit, module, version, sum, nil); err == nil {
		t.Fatal("another actual command was admitted as author")
	}
}

func TestRunReferenceGoCancellationKillsOwnedDescendant(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "child-pid")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := runReferenceGo(ctx, root, "/bin/sh", []string{"PATH=/usr/bin:/bin"}, 64,
		"-c", "sleep 30 & child=$!; printf '%s\\n' \"$child\" > \"$1\"; wait", "reference-cancellation", pidPath)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("owned child cancellation was not bounded: %v", err)
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Fatalf("owned child identity is invalid: %q", raw)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		} else if err != nil {
			t.Fatalf("owned descendant liveness unavailable: %v", err)
		}
		select {
		case <-deadline.C:
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("owned sleep descendant survived canceled reference command")
		case <-tick.C:
		}
	}
}

func newReferenceToolBuildWorkspace(t *testing.T, request ReferenceToolRequest) string {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "cache", "tmp", "bin"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(request.GitBinary, filepath.Join(workspace, "bin", "git")); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func buildReferenceToolFixture(t *testing.T, request ReferenceToolRequest, workspace string) {
	t.Helper()
	if _, err := runReferenceGo(t.Context(), request.RepositoryRoot, filepath.Join(request.GoRoot, "bin", "go"),
		referenceBuildEnvironment(request, workspace), 64<<10,
		"build", "-trimpath", "-pgo=off", "-buildvcs=true", "-p=1", "-o", request.Binary,
		"github.com/bmeddeb/phebs/cmd/phebs-focused-index"); err != nil {
		t.Fatal(err)
	}
}
