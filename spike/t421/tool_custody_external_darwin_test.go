//go:build darwin

package t421

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestExecutionToolCustodyExternalOptionalRealSurreal(t *testing.T) {
	requireExternalToolFrozenHost(t)
	binary := toolCustodyExternalSurreal(t)
	parent, _ := inputCustodyTestFixture(t)
	before := inputCustodyTestStat(t, binary)
	alias := filepath.Join(filepath.Dir(parent), "selected-image")
	if err := os.Symlink(binary, alias); err != nil {
		t.Fatal(err)
	}
	tool := toolCustodyExternalProtect(t, parent, "surreal", alias)
	identity, path, err := tool.Check(t.Context(), "surreal")
	if err != nil || !strings.HasPrefix(identity.Version, "3.") || path != filepath.Join(tool.Directory(), "surreal") {
		t.Fatalf("protected SurrealDB observation: %#v, %v", identity, err)
	}
	assertExternalToolIdentity(t, identity, "surreal", path, identity.Version)
	if !inputCustodyProtected(inputCustodyTestStat(t, path)) ||
		os.SameFile(before, inputCustodyTestStat(t, path)) || !inputCustodySame(before, inputCustodyTestStat(t, binary)) {
		t.Fatal("copy custody changed the original or did not protect a distinct image")
	}
	inputCustodyTestReadOnlyDescriptors(t, tool.input)
	want := identity
	identity.Role, identity.SHA256 = "changed returned value", "changed returned value"
	again, againPath, err := tool.Check(t.Context(), "surreal")
	if err != nil || again != want || againPath != path {
		t.Fatalf("returned identity was not an independent value: %#v, %q, %v", again, againPath, err)
	}
}

func TestExecutionToolCustodyExternalOriginalWriterCannotChangeCopy(t *testing.T) {
	requireExternalToolFrozenHost(t)
	binary := toolCustodyExternalSurreal(t)
	parent, _ := inputCustodyTestFixture(t)
	// Mutate only this fixture-owned source image, never the selected host tool.
	original, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = original.Close() })
	source := filepath.Join(filepath.Dir(parent), "owned-surreal-source")
	writer, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if size, err := io.Copy(writer, io.LimitReader(original, maxInputCustodyFileBytes+1)); err != nil || size > maxInputCustodyFileBytes {
		t.Fatalf("bounded fixture image copy: %d, %v", size, err)
	}
	before := inputCustodyTestStat(t, source)
	tool := toolCustodyExternalProtect(t, parent, "surreal", source)
	identity, path, err := tool.Check(t.Context(), "surreal")
	if err != nil || !inputCustodySame(before, inputCustodyTestStat(t, source)) {
		t.Fatalf("source changed during custody construction: %v", err)
	}
	if _, err := writer.WriteAt([]byte("changed only the fixture source"), 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.Truncate(4); err != nil {
		t.Fatal(err)
	}
	after, afterPath, err := tool.Check(t.Context(), "surreal")
	if err != nil || after != identity || afterPath != path {
		t.Fatalf("original writer invalidated independent custody: %#v, %q, %v", after, afterPath, err)
	}
	digest, err := t4013.DigestHostExecutable(t.Context(), path)
	if err != nil || digest != identity.SHA256 {
		t.Fatalf("original writer changed protected bytes: %v", err)
	}
}

func TestExecutionToolCustodyExternalLifecycleRefusalsAreSticky(t *testing.T) {
	requireExternalToolFrozenHost(t)
	binary := toolCustodyExternalSurreal(t)
	for _, name := range []string{"wrong role", "canceled check", "nil context", "flag drift", "close"} {
		t.Run(name, func(t *testing.T) {
			parent, _ := inputCustodyTestFixture(t)
			tool := toolCustodyExternalProtect(t, parent, "surreal", binary)
			role, ctx := "surreal", t.Context()
			switch name {
			case "wrong role":
				role = "git"
			case "canceled check":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil context":
				ctx = nil
			case "flag drift":
				file := tool.input.inputs["surreal"].file
				if err := inputCustodyFlag(file, false); err != nil {
					t.Fatal(err)
				}
			case "close":
				if err := tool.Close(); err != nil {
					t.Fatal(err)
				}
				inputCustodyTestClosed(t, tool.input)
				if !inputCustodyProtected(inputCustodyTestStat(t, tool.Directory())) ||
					!inputCustodyProtected(inputCustodyTestStat(t, filepath.Join(tool.Directory(), "surreal"))) {
					t.Fatal("descriptor release thawed or removed retained custody")
				}
			}
			identity, path, err := tool.Check(ctx, role)
			toolCustodyExternalRefusal(t, identity, path, err)
			if name == "flag drift" {
				if err := inputCustodyFlag(tool.input.inputs["surreal"].file, true); err != nil {
					t.Fatal(err)
				}
			}
			identity, path, err = tool.Check(t.Context(), "surreal")
			toolCustodyExternalRefusal(t, identity, path, err)
			if err := tool.Close(); err != ErrExecutionToolCustody {
				t.Fatalf("closed failed custody lost its sticky failure: %v", err)
			}
			inputCustodyTestClosed(t, tool.input)
		})
	}
}

func TestExecutionToolCustodyExternalRefusesBeforeCreatingCopies(t *testing.T) {
	requireExternalToolFrozenHost(t)
	parent, _ := inputCustodyTestFixture(t)
	binary := toolCustodyExternalGo(t)
	before := inputCustodyTestStat(t, binary)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name, role, path string
		ctx              context.Context
	}{
		{"fixed shell", "sh", "/bin/sh", t.Context()},
		{"fixed hdiutil", "hdiutil", "/usr/bin/hdiutil", t.Context()},
		{"fixed ssh-keygen", "ssh-keygen", "/usr/bin/ssh-keygen", t.Context()},
		{"Git helper recipe unavailable", "git", binary, t.Context()},
		{"Go SDK recipe unavailable", "go", binary, t.Context()},
		{"missing author", "t422-author", binary, t.Context()},
		{"missing executor", "t422-execute", binary, t.Context()},
		{"reference role", "phebs", binary, t.Context()},
		{"unknown role", "unknown", binary, t.Context()},
		{"empty role", "", binary, t.Context()},
		{"relative path", "surreal", "surreal", t.Context()},
		{"missing path", "surreal", filepath.Join(parent, "absent"), t.Context()},
		{"nil context", "surreal", binary, nil},
		{"canceled context", "surreal", binary, canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool, err := ProtectExecutionExternalTool(test.ctx, parent, test.role, test.path)
			if tool != nil || err != ErrExecutionToolCustody {
				if tool != nil && tool.input != nil {
					inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: test.role}})
				}
				t.Fatalf("invalid request created custody or leaked failure: %#v, %v", tool, err)
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatalf("invalid request changed private parent: %v", err)
			}
		})
	}
	if !inputCustodySame(before, inputCustodyTestStat(t, binary)) {
		t.Fatal("rejected requests changed the original tool")
	}
	for _, tool := range []*ExecutionToolCustody{nil, {}} {
		identity, path, err := tool.Check(t.Context(), "surreal")
		toolCustodyExternalRefusal(t, identity, path, err)
		if tool.Directory() != "" || tool.Close() != nil {
			t.Fatal("zero or absent wrapper fabricated custody")
		}
	}
}

func TestExecutionToolCustodyExternalVerifierFailureRetainsClosedCopy(t *testing.T) {
	requireExternalToolFrozenHost(t)
	for _, test := range []struct{ name, role, binary string }{
		{"unusable native image", "surreal", toolCustodyExternalGo(t)},
		{"non-native image", "surreal", writeExternalToolScript(t, "exit 91\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent, _ := inputCustodyTestFixture(t)
			tool, err := ProtectExecutionExternalTool(t.Context(), parent, test.role, test.binary)
			if tool != nil && tool.input != nil {
				inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: test.role}})
			}
			if err != ErrExecutionToolCustody || tool == nil || tool.input == nil ||
				tool.Directory() == "" || tool.identity != (ExecutionToolIdentity{}) {
				t.Fatalf("failed real observation lost cleanup custody or retained identity: %#v, %v", tool, err)
			}
			inputCustodyTestClosed(t, tool.input)
			if !inputCustodyProtected(inputCustodyTestStat(t, tool.Directory())) ||
				!inputCustodyProtected(inputCustodyTestStat(t, filepath.Join(tool.Directory(), test.role))) {
				t.Fatal("failed observation removed or thawed its retained copy")
			}
			identity, path, err := tool.Check(t.Context(), test.role)
			toolCustodyExternalRefusal(t, identity, path, err)
			if err := tool.Close(); !errors.Is(err, ErrExecutionToolCustody) {
				t.Fatalf("failed custody close: %v", err)
			}
		})
	}
}

func TestExecutionToolCustodyExternalRejectsUnownedParentShape(t *testing.T) {
	requireExternalToolFrozenHost(t)
	binary := toolCustodyExternalGo(t)
	for _, name := range []string{"relative", "unclean", "symlink", "mode", "regular file"} {
		t.Run(name, func(t *testing.T) {
			parent, input := inputCustodyTestFixture(t)
			selected := parent
			switch name {
			case "relative":
				selected = "parent"
			case "unclean":
				selected += "/."
			case "symlink":
				selected = filepath.Join(filepath.Dir(parent), "parent-alias")
				if err := os.Symlink(parent, selected); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(parent, 0o755); err != nil {
					t.Fatal(err)
				}
			case "regular file":
				selected = input.Path
			}
			tool, err := ProtectExecutionExternalTool(t.Context(), selected, "surreal", binary)
			if tool != nil && tool.input != nil {
				inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: "surreal"}})
			}
			if tool != nil || err != ErrExecutionToolCustody {
				t.Fatalf("unadmitted parent created custody: %#v, %v", tool, err)
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatalf("unadmitted parent was mutated: %v", err)
			}
		})
	}
}

func toolCustodyExternalGo(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func toolCustodyExternalSurreal(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("PHEBS_T422_EXTERNAL_SURREAL")
	if binary == "" {
		t.Skip("set PHEBS_T422_EXTERNAL_SURREAL to an explicit supported native SurrealDB image")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("SurrealDB fixture selection must be absolute")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func toolCustodyExternalProtect(t *testing.T, parent, role, binary string) *ExecutionToolCustody {
	t.Helper()
	tool, err := ProtectExecutionExternalTool(t.Context(), parent, role, binary)
	if tool != nil && tool.input != nil {
		inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: role}})
	}
	if err != nil || tool == nil {
		t.Fatalf("real protected external image: %v", err)
	}
	return tool
}

func toolCustodyExternalRefusal(t *testing.T, identity ExecutionToolIdentity, path string, err error) {
	t.Helper()
	if identity != (ExecutionToolIdentity{}) || path != "" || err != ErrExecutionToolCustody {
		t.Fatalf("refusal leaked identity, path or a nonclosed error: %#v, %q, %v", identity, path, err)
	}
}
