//go:build linux

package t421

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLinuxGoReferenceSourceRequiresExactProtectedModes(t *testing.T) {
	reference := goReferenceProtectedSourceFixture(t)
	// The source's tree/index are unchanged 100644/100755. Historical checkout
	// inspection must continue to reject H2k's normalized executable 0500.
	if reference.verify(t.Context()) == nil {
		t.Fatal("historical execute-bit gate accepted protected 0500")
	}
	if err := verifyGoReferenceSource(t.Context(), reference); err != nil {
		t.Fatal("exact protected raw source/tree/index refused", err)
	}
}

func TestLinuxGoReferenceProtectedSourceRetainsEveryGate(t *testing.T) {
	for _, test := range []string{"executable-0755", "executable-0555", "executable-0400", "data-0500", "data-0600", "blob", "index", "config", "shallow", "tree", "untracked", "canceled"} {
		t.Run(test, func(t *testing.T) {
			reference := goReferenceProtectedSourceFixture(t)
			path := filepath.Join(reference.root.root, "tracked.txt")
			ctx := t.Context()
			var mutationErr error
			switch test {
			case "executable-0755":
				mutationErr = os.Chmod(filepath.Join(reference.root.root, "executable"), 0o755)
			case "executable-0555":
				mutationErr = os.Chmod(filepath.Join(reference.root.root, "executable"), 0o555)
			case "executable-0400":
				mutationErr = os.Chmod(filepath.Join(reference.root.root, "executable"), 0o400)
			case "data-0500":
				mutationErr = os.Chmod(path, 0o500)
			case "data-0600":
				mutationErr = os.Chmod(path, 0o600)
			case "blob":
				if os.Chmod(path, 0o600) != nil || os.WriteFile(path, []byte("wrong raw blob\n"), 0o600) != nil {
					t.Fatal("blob mutation fixture")
				}
				mutationErr = os.Chmod(path, 0o400)
			case "index":
				_, mutationErr = reference.root.run(t.Context(), 0, "update-index", "--assume-unchanged", "tracked.txt")
			case "config":
				mutationErr = os.WriteFile(filepath.Join(reference.root.root, ".git", "config"), []byte("[core]\n\tbare = true\n"), 0o600)
			case "shallow":
				mutationErr = os.WriteFile(filepath.Join(reference.root.root, ".git", "shallow"), []byte(strings.Repeat("0", 40)+"\n"), 0o600)
			case "tree":
				reference.tree = strings.Repeat("0", 40)
			case "untracked":
				mutationErr = os.WriteFile(filepath.Join(reference.root.root, "extra.go"), []byte("package extra\n"), 0o400)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if mutationErr != nil {
				t.Fatal("source mutation fixture", mutationErr)
			}
			if verifyGoReferenceSource(ctx, reference) == nil {
				t.Fatal("changed source mode/blob/index/control/tree admitted")
			}
		})
	}
}

func goReferenceProtectedSourceFixture(t *testing.T) executionReferenceSource {
	t.Helper()
	fixture := newExecutionCheckoutFixture(t)
	fixture.write(t, "executable", "#!/bin/sh\nexit 0\n")
	if os.Chmod(filepath.Join(fixture.root, "executable"), 0o755) != nil {
		t.Fatal("executable fixture")
	}
	fixture.command(t, "add", "executable")
	fixture.source = fixture.commit(t, "protected source mode fixture")
	reference := newExecutionReferenceSourceFixture(t, fixture)
	for _, entry := range reference.entries {
		mode := os.FileMode(0o400)
		if entry.mode == "100755" {
			mode = 0o500
		}
		if os.Chmod(filepath.Join(reference.root.root, entry.path), mode) != nil {
			t.Fatal("H2k normalized fixture modes")
		}
	}
	return reference
}

func TestLinuxGoReferenceOutputComparesCompleteBytes(t *testing.T) {
	for _, test := range []string{"valid", "first-byte", "last-byte", "length", "nonexecutable", "canceled"} {
		t.Run(test, func(t *testing.T) {
			selected := bytes.Repeat([]byte("candidate"), treeWireChunk/3)
			actual := bytes.Clone(selected)
			mode := os.FileMode(0o500)
			ctx := t.Context()
			switch test {
			case "first-byte":
				actual[0]++
			case "last-byte":
				actual[len(actual)-1]++
			case "length":
				actual = append(actual, 0)
			case "nonexecutable":
				mode = 0o400
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			directory := t.TempDir()
			for _, file := range []struct {
				name string
				raw  []byte
			}{{"candidate", selected}, {"output", actual}} {
				if os.WriteFile(filepath.Join(directory, file.name), file.raw, mode) != nil {
					t.Fatal("file fixture")
				}
			}
			candidate, err := os.Open(filepath.Join(directory, "candidate"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = candidate.Close() }()
			output, err := os.Open(filepath.Join(directory, "output"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = output.Close() }()
			digest, err := compareGoReferenceOutput(ctx, candidate, output)
			if test == "valid" {
				if err != nil || digest != SHA256(selected) {
					t.Fatal("equal complete bytes refused", err)
				}
			} else if err == nil || digest != "" {
				t.Fatal("different/unavailable bytes admitted", test)
			}
		})
	}
}

func TestLinuxGoReferenceCandidateRequiresSealedDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidate")
	if os.WriteFile(path, []byte("ordinary executable-shaped file"), 0o500) != nil {
		t.Fatal("candidate fixture")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	plan := goReferencePlanFixture()
	plan.CandidateSHA256 = SHA256([]byte("ordinary executable-shaped file"))
	if checkGoReferenceCandidate(t.Context(), file, plan) != ErrExecutionGoBuildCustody || checkGoReferenceCandidate(t.Context(), nil, plan) != ErrExecutionGoBuildCustody {
		t.Fatal("unsealed/absent candidate descriptor admitted")
	}
}

func TestLinuxGoReferenceCommandStderrIsBoundedPrivateAndExclusive(t *testing.T) {
	for _, size := range []int{0, maxGoReferenceCommandStderrBytes, maxGoReferenceCommandStderrBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			directory := t.TempDir()
			raw := bytes.Repeat([]byte{'x'}, size)
			err := retainGoReferenceCommandStderr(directory, raw)
			path := filepath.Join(directory, "command.stderr")
			if size > maxGoReferenceCommandStderrBytes {
				if err == nil {
					t.Fatal("over-bound diagnostic retained")
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("over-bound diagnostic created a file")
				}
				return
			}
			actual, readErr := os.ReadFile(path)
			info, statErr := os.Lstat(path)
			if err != nil || readErr != nil || statErr != nil || !bytes.Equal(actual, raw) || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatal("bounded private diagnostic differs")
			}
			if retainGoReferenceCommandStderr(directory, []byte("replacement")) == nil {
				t.Fatal("first diagnostic overwritten")
			}
			actual, err = os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, raw) {
				t.Fatal("exclusive diagnostic changed")
			}
		})
	}
	directory := t.TempDir()
	sentinel := filepath.Join(directory, "sentinel")
	if os.WriteFile(sentinel, []byte("unchanged"), 0o600) != nil || os.Symlink(sentinel, filepath.Join(directory, "command.stderr")) != nil {
		t.Fatal("symlink fixture")
	}
	if retainGoReferenceCommandStderr(directory, []byte("replacement")) == nil {
		t.Fatal("diagnostic followed existing symlink")
	}
	actual, err := os.ReadFile(sentinel)
	if err != nil || string(actual) != "unchanged" {
		t.Fatal("foreign sentinel changed")
	}
}
