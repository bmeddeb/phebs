//go:build linux

package t421

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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
