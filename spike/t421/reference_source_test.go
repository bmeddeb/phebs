package t421

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func TestExecutionReferenceSourceRetainsOnlyExactSelectedCommitAndTree(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	for _, path := range []string{"space name.txt", "quoted\"name.txt", "tab\tname.txt", "nonascii-é.txt"} {
		fixture.write(t, path, "exact\x00bytes\n")
		fixture.command(t, "add", path)
	}
	fixture.write(t, "executable", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(fixture.root, "executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.command(t, "add", "executable")
	fixture.source = fixture.commit(t, "raw source inputs")
	reference := newExecutionReferenceSourceFixture(t, fixture)
	if reference.source != fixture.source || reference.tree != fixture.command(t, "rev-parse", "HEAD^{tree}") {
		t.Fatalf("private reference authority = %s/%s", reference.source, reference.tree)
	}
	for _, args := range [][]string{{"cat-file", "commit", fixture.source}, {"ls-tree", "-rz", "--full-tree", fixture.source}, {"ls-files", "-v", "--stage", "-z"}} {
		origin := executionCheckoutInspector{root: fixture.root, git: reference.root.git, digest: reference.root.digest}
		want, err := origin.run(t.Context(), maxCheckoutInventoryBytes, args...)
		if err != nil {
			t.Fatal(err)
		}
		got, err := reference.root.run(t.Context(), maxCheckoutInventoryBytes, args...)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("private reference Git %q differs: %v", args, err)
		}
	}
	if value, err := reference.root.run(t.Context(), 64, "rev-list", "--count", "HEAD"); err != nil || string(value) != "1\n" {
		t.Fatalf("private history boundary = %q, %v", value, err)
	}
	if _, err := reference.root.run(t.Context(), 0, "cat-file", "-e", fixture.integration); err == nil {
		t.Fatal("private source imported an ancestor commit")
	}
	if _, err := reference.root.run(t.Context(), 4096, "config", "--local", "--get", "user.name"); err == nil {
		t.Fatal("private source imported origin Git configuration")
	}
	if err := reference.verify(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionReferenceSourceDoesNotImportOrExecuteLocalFilters(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	fixture.write(t, ".gitattributes", "tracked.txt filter=reference-probe\n")
	fixture.command(t, "add", ".gitattributes")
	fixture.source = fixture.commit(t, "filter attribute without local authority")
	helperRoot := t.TempDir()
	marker := filepath.Join(helperRoot, "called")
	helper := filepath.Join(helperRoot, "filter")
	if err := os.WriteFile(helper, []byte(fmt.Sprintf("#!/bin/sh\nprintf called > %q\nprintf 'source\\n'\n", marker)), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.command(t, "config", "filter.reference-probe.clean", helper)
	fixture.command(t, "config", "filter.reference-probe.smudge", helper)
	fixture.command(t, "config", "filter.reference-probe.required", "true")
	fixture.command(t, "config", "core.fsmonitor", helper)
	reference := newExecutionReferenceSourceFixture(t, fixture)
	if status, err := reference.root.run(t.Context(), 0, "status", "--porcelain=v1", "--untracked-files=all"); err != nil || len(status) != 0 {
		t.Fatalf("private source is not clean for Go VCS stamping: %q, %v", status, err)
	}
	if err := reference.verify(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("reference preparation executed origin local helper: %v", err)
	}
}

func TestExecutionReferenceSourceRefusesMutations(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, executionReferenceSource)
	}{
		{"raw file", func(t *testing.T, r executionReferenceSource) {
			writeReferenceFixtureFile(t, r.root.root, "tracked.txt", "change\n")
		}},
		{"hidden index", func(t *testing.T, r executionReferenceSource) {
			if _, err := r.root.run(t.Context(), 0, "update-index", "--assume-unchanged", "tracked.txt"); err != nil {
				t.Fatal(err)
			}
		}},
		{"ignored input", func(t *testing.T, r executionReferenceSource) {
			writeReferenceFixtureFile(t, r.root.root, "ignored_test.go", "package extra\n")
		}},
		{"Git config", func(t *testing.T, r executionReferenceSource) {
			if _, err := r.root.run(t.Context(), 0, "config", "filter.changed.clean", "/must-not-run"); err != nil {
				t.Fatal(err)
			}
		}},
		{"shallow marker", func(t *testing.T, r executionReferenceSource) {
			writeReferenceFixtureFile(t, r.root.root, ".git/shallow", strings.Repeat("0", 40)+"\n")
		}},
		{"HEAD", func(t *testing.T, r executionReferenceSource) {
			writeReferenceFixtureFile(t, r.root.root, ".git/HEAD", strings.Repeat("0", 40)+"\n")
		}},
		{"attributes", func(t *testing.T, r executionReferenceSource) {
			writeReferenceFixtureFile(t, r.root.root, ".git/info/attributes", "* filter=changed\n")
		}},
		{"alternates", func(t *testing.T, r executionReferenceSource) {
			writeReferenceFixtureFile(t, r.root.root, ".git/objects/info/alternates", "/not-admitted\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reference := newExecutionReferenceSourceFixture(t, newExecutionCheckoutFixture(t))
			test.mutate(t, reference)
			if err := reference.verify(t.Context()); err == nil {
				t.Fatal("mutated private reference source was admitted")
			}
		})
	}
}

func TestExecutionReferenceSourceRefusesChangedOriginAndInvalidDestination(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *executionCheckoutFixture, *string)
	}{
		{"changed origin", func(t *testing.T, f *executionCheckoutFixture, _ *string) { f.write(t, "tracked.txt", "change\n") }},
		{"existing destination", func(t *testing.T, _ *executionCheckoutFixture, destination *string) {
			if err := os.Mkdir(*destination, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"inside origin", func(_ *testing.T, f *executionCheckoutFixture, destination *string) {
			*destination = filepath.Join(f.root, "reference")
		}},
		{"nonprivate parent", func(t *testing.T, _ *executionCheckoutFixture, destination *string) {
			if err := os.Chmod(filepath.Dir(*destination), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExecutionCheckoutFixture(t)
			if _, err := fixture.inspect(t); err != nil {
				t.Fatal(err)
			}
			origin := referenceFixtureInspector(t, fixture)
			destination := newReferenceFixtureDestination(t)
			test.mutate(t, &fixture, &destination)
			if _, err := createExecutionReferenceSource(t.Context(), origin, fixture.source, destination); err == nil {
				t.Fatal("unadmitted reference source inputs were accepted")
			}
		})
	}
}

func TestExecutionReferenceSourceRefusesCanceledCreation(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	origin := referenceFixtureInspector(t, fixture)
	destination := newReferenceFixtureDestination(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := createExecutionReferenceSource(ctx, origin, fixture.source, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled private source creation = %v", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("canceled private source created destination: %v", err)
	}
}

func newExecutionReferenceSourceFixture(t *testing.T, fixture executionCheckoutFixture) executionReferenceSource {
	t.Helper()
	if _, err := fixture.inspect(t); err != nil {
		t.Fatal(err)
	}
	reference, err := createExecutionReferenceSource(t.Context(), referenceFixtureInspector(t, fixture), fixture.source, newReferenceFixtureDestination(t))
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func referenceFixtureInspector(t *testing.T, fixture executionCheckoutFixture) executionCheckoutInspector {
	t.Helper()
	digest, err := executableidentity.Digest(fixture.git)
	if err != nil {
		t.Fatal(err)
	}
	return executionCheckoutInspector{root: fixture.root, git: fixture.git, digest: digest}
}

func newReferenceFixtureDestination(t *testing.T) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, "reference")
}

func writeReferenceFixtureFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
