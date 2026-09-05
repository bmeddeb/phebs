//go:build darwin

package t421

import (
	"context"
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func TestProtectExecutionReferenceToolChecksProtectedImageAgainstRealBuild(t *testing.T) {
	requireExternalToolFrozenHost(t)
	// Reuse the real committed source/build helpers, but keep this fixture to one
	// tiny dependency-free command. This is not a full tool-inventory build.
	fixture := newExecutionCheckoutFixture(t)
	fixture.write(t, "go.mod", "module github.com/bmeddeb/phebs\n\ngo 1.26\n")
	fixture.write(t, "go.sum", "")
	fixture.write(t, ".gitignore", "/ignored/\n/ignored_test.go\n/cmd/phebs-focused-index/ignored.go\n")
	fixture.write(t, "cmd/phebs-focused-index/main.go", "package main\nvar message = \"exact\"\nfunc main() { println(message) }\n")
	fixture.command(t, "add", "go.mod", "go.sum", ".gitignore", "cmd/phebs-focused-index/main.go")
	fixture.source = fixture.commit(t, "neutral reference custody command")
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
	want := ExecutionToolIdentity{
		Role: request.Role, FileType: regularFileType, SHA256: cleanDigest,
		Version: "clean commit " + fixture.source, Provenance: "go-build-info-vcs-v1", BuildVCSRevision: fixture.source,
	}
	t.Run("verified protected copy executes", func(t *testing.T) {
		original := inputCustodyTestStat(t, request.Binary)
		tool, err := referenceCustodyTestProtect(t, t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		identity, path, err := tool.Check(t.Context(), request.Role)
		if err != nil || identity != want || path != filepath.Join(tool.Directory(), request.Role) || path == request.Binary {
			t.Fatalf("verified copy: %#v, %q, %v", identity, path, err)
		}
		if !inputCustodySame(original, inputCustodyTestStat(t, request.Binary)) ||
			os.SameFile(original, inputCustodyTestStat(t, path)) || !inputCustodyProtected(inputCustodyTestStat(t, path)) {
			t.Fatal("reference construction changed original custody or did not create a protected copy")
		}
		inputCustodyTestReadOnlyDescriptors(t, tool.input)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, path)
		command.Dir, command.Env = tool.Directory(), []string{"LC_ALL=C"}
		if output, err := command.CombinedOutput(); err != nil || string(output) != "exact\n" {
			t.Fatalf("protected native execution: %q, %v", output, err)
		}
		identity.Role = "caller-mutated"
		if got, _, err := tool.Check(t.Context(), request.Role); err != nil || got != want {
			t.Fatalf("caller changed internal verified identity: %#v, %v", got, err)
		}
		got, privatePath, err := tool.Check(t.Context(), "phebs")
		referenceCustodyTestRefusal(t, got, privatePath, err)
		got, privatePath, err = tool.Check(t.Context(), request.Role)
		referenceCustodyTestRefusal(t, got, privatePath, err)
	})
	for _, test := range []struct {
		name   string
		change func(*ReferenceToolRequest)
	}{
		{"wrong implemented role", func(request *ReferenceToolRequest) { request.Role = "phebs" }},
		{"wrong source commit", func(request *ReferenceToolRequest) { request.SourceCommit = fixture.integration }},
		{"missing source", func(request *ReferenceToolRequest) {
			request.RepositoryRoot = filepath.Join(workspace, "missing-source")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := request
			test.change(&selected)
			tool, err := referenceCustodyTestProtect(t, t.Context(), selected)
			referenceCustodyTestRetainedFailure(t, tool, selected.Role, err)
		})
	}
	// The ignored compiled file preserves the clean Go/VCS metadata. A digest
	// observed from that selected image is not independent source provenance.
	fixture.write(t, "cmd/phebs-focused-index/ignored.go", "package main\nfunc init() { message = \"injected\" }\n")
	request.Binary = filepath.Join(workspace, "supplied-ignored-input")
	buildReferenceToolFixture(t, request, workspace)
	if err := os.Remove(filepath.Join(fixture.root, "cmd/phebs-focused-index/ignored.go")); err != nil {
		t.Fatal(err)
	}
	changedInfo, err := buildinfo.ReadFile(request.Binary)
	if err != nil || !reflect.DeepEqual(changedInfo, cleanInfo) {
		t.Fatalf("changed fixture must retain identical BuildInfo: %v", err)
	}
	changedDigest, err := executableidentity.Digest(request.Binary)
	if err != nil || changedDigest == cleanDigest {
		t.Fatalf("changed fixture must differ in executable bytes: %v", err)
	}
	t.Run("metadata-identical changed image is not admitted", func(t *testing.T) {
		tool, err := referenceCustodyTestProtect(t, t.Context(), request)
		referenceCustodyTestRetainedFailure(t, tool, request.Role, err)
		path := filepath.Join(tool.Directory(), request.Role)
		if digest, err := executableidentity.Digest(path); err != nil || digest != changedDigest {
			t.Fatalf("verifier failure did not retain the actual selected copy: %s, %v", digest, err)
		}
	})
}

func TestProtectExecutionReferenceToolUnavailableBeforeCopy(t *testing.T) {
	requireExternalToolFrozenHost(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"", "unknown", "t422-execute", "git", "go", "surreal"} {
		t.Run("role "+role, func(t *testing.T) {
			tool, err := ProtectExecutionReferenceTool(t.Context(), parent, ReferenceToolRequest{Role: role, Binary: "/usr/bin/true"})
			if tool != nil && tool.input != nil {
				inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: role}})
			}
			if tool != nil || err != ErrExecutionToolCustody {
				t.Fatalf("unavailable role created custody: %v, %v", tool, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, candidate := range []context.Context{nil, ctx} {
		tool, err := ProtectExecutionReferenceTool(candidate, parent, ReferenceToolRequest{Role: "phebs-focused-index", Binary: "/usr/bin/true"})
		if tool != nil && tool.input != nil {
			inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: "phebs-focused-index"}})
		}
		if tool != nil || err != ErrExecutionToolCustody {
			t.Fatalf("unavailable context created custody: %v, %v", tool, err)
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("pre-copy refusal mutated parent: %d, %v", len(entries), err)
	}
}

func referenceCustodyTestProtect(t *testing.T, ctx context.Context, request ReferenceToolRequest) (*ExecutionToolCustody, error) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	tool, resultErr := ProtectExecutionReferenceTool(ctx, parent, request)
	if tool != nil && tool.input != nil {
		inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: request.Role}})
	}
	return tool, resultErr
}

func referenceCustodyTestRetainedFailure(t *testing.T, tool *ExecutionToolCustody, role string, err error) {
	t.Helper()
	if tool == nil || tool.input == nil || err != ErrExecutionToolCustody || tool.identity != (ExecutionToolIdentity{}) {
		t.Fatalf("verifier failure did not retain closed identity-free custody: %#v, %v", tool, err)
	}
	inputCustodyTestClosed(t, tool.input)
	for _, path := range []string{tool.Directory(), filepath.Join(tool.Directory(), role)} {
		if !inputCustodyProtected(inputCustodyTestStat(t, path)) {
			t.Fatal("verifier failure thawed or removed protected copies")
		}
	}
	identity, path, checkErr := tool.Check(t.Context(), role)
	referenceCustodyTestRefusal(t, identity, path, checkErr)
	if err := tool.Close(); err != ErrExecutionToolCustody {
		t.Fatalf("failed custody lost its terminal error: %v", err)
	}
}

func referenceCustodyTestRefusal(t *testing.T, identity ExecutionToolIdentity, path string, err error) {
	t.Helper()
	if identity != (ExecutionToolIdentity{}) || path != "" || err != ErrExecutionToolCustody {
		t.Fatalf("refusal exposed identity, private path or diagnostic: %#v, %q, %v", identity, path, err)
	}
}
