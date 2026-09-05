//go:build darwin

package t421

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestExecutionProductionSourceAdmission(t *testing.T) {
	requireExternalToolFrozenHost(t)
	fixture := newExecutionCheckoutFixture(t)
	parent, _ := inputCustodyTestFixture(t)
	git := gitCustodyTestProtect(t, t.Context(), parent, fixture.git)
	for _, name := range []string{"actual source", "actual packed source", "extra ref", "hook config", "symlink", "extra file", "oversized file", "changed pack"} {
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(parent, strings.ReplaceAll(name, " ", "-"))
			if name == "actual packed source" {
				source += " #?%"
			}
			gitCustodyTestRun(t, git, parent, nil, "init", "--bare", "--template=", "--initial-branch=main", source)
			if err := os.Chmod(source, 0o700); err != nil {
				t.Fatal(err)
			}
			args := []string{"-C", source}
			if name == "actual packed source" || name == "changed pack" {
				// A tiny fixture normally unpacks. This test-only control reaches
				// the real packed representation without changing the serve recipe.
				args = append(args, "-c", "fastimport.unpackLimit=0")
			}
			args = append(args, "fast-import", "--quiet", "--date-format=raw")
			gitCustodyTestRun(t, git, parent, strings.NewReader(gitCustodyTestImport("neutral searchable source\n", "")), args...)
			commit := gitCustodyTestRevision(t, git, parent, source)[0]
			switch name {
			case "extra ref":
				gitCustodyTestRun(t, git, parent, nil, "-C", source, "update-ref", "refs/heads/extra", commit)
			case "hook config":
				gitCustodyTestRun(t, git, parent, nil, "-C", source, "config", "core.hooksPath", "/private/not-permitted")
			case "symlink":
				if err := os.Symlink("config", filepath.Join(source, "extra")); err != nil {
					t.Fatal(err)
				}
			case "extra file":
				if err := os.WriteFile(filepath.Join(source, "extra"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "oversized file":
				if err := os.Truncate(filepath.Join(source, "config"), (2<<20)+1); err != nil {
					t.Fatal(err)
				}
			case "changed pack":
				packs, err := filepath.Glob(filepath.Join(source, "objects", "pack", "*.pack"))
				if err != nil || len(packs) != 1 {
					t.Fatalf("actual fast-import pack: %d, %v", len(packs), err)
				}
				if err := os.Chmod(packs[0], 0o600); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(packs[0], os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteAt([]byte("wrong"), 12)
				if closeErr := file.Close(); writeErr != nil || closeErr != nil {
					t.Fatalf("mutate exact fixture pack: %v, %v", writeErr, closeErr)
				}
			}
			custody := &ExecutionProductionCustody{request: ExecutionProductionRequest{Git: git, SourceRepository: source, SourceCommit: commit}}
			t.Cleanup(func() { _ = custody.Close() })
			err := custody.admitSource(t.Context())
			if (err == nil) != (name == "actual source" || name == "actual packed source") {
				t.Fatalf("small actual-source admission: %v", err)
			}
			if name == "actual packed source" && len(custody.controls) != 5 {
				t.Fatalf("source controls and native pack pair not retained: %d", len(custody.controls))
			}
			if name == "actual source" || name == "actual packed source" {
				mirror := source + "-mirror"
				gitCustodyTestRun(t, git, parent, nil, "clone", "--mirror", productionSourceURL(source), mirror)
				if got := gitCustodyTestRevision(t, git, parent, mirror)[0]; got != commit {
					t.Fatal("file transport changed the selected source commit")
				}
				for _, control := range custody.controls {
					current, err := os.Lstat(control.path)
					if err != nil || !inputCustodySame(control.info, current) {
						t.Fatal("ordinary mirror clone changed retained source control custody", err)
					}
				}
				err := filepath.WalkDir(filepath.Join(source, "objects"), func(path string, entry os.DirEntry, walkErr error) error {
					if walkErr != nil || entry.IsDir() {
						return walkErr
					}
					relative, err := filepath.Rel(source, path)
					if err != nil {
						return err
					}
					original, err := os.Lstat(path)
					if err != nil {
						return err
					}
					copied, err := os.Lstat(filepath.Join(mirror, relative))
					if os.IsNotExist(err) {
						return nil // Transport may choose another pack layout.
					}
					if err != nil {
						return err
					}
					if os.SameFile(original, copied) {
						return fmt.Errorf("source and mirror share a native object")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestExecutionProductionNativeSourceBoundsAndLease(t *testing.T) {
	parent, _ := inputCustodyTestFixture(t)
	lease, err := acquireProductionSourceLease(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if other, err := acquireProductionSourceLease(parent); err == nil || other != nil {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("second source mutation owner admitted")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err = acquireProductionSourceLease(parent)
	if err != nil {
		t.Fatal("released source lease did not permit its next explicit owner")
	}
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(source, "objects", "00")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 512; index++ {
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("%038x", index)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := inspectProductionSourceFiles(t.Context(), source); err == nil {
		t.Fatal("native source census exceeded its 512-entry bound")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspectProductionSourceFiles(canceled, source); err == nil {
		t.Fatal("canceled native source census admitted")
	}
}

func TestExecutionProductionClosedParsersAndLocalBudget(t *testing.T) {
	valid := "core.repositoryformatversion\n0\x00core.filemode\ntrue\x00core.bare\ntrue\x00"
	for _, test := range []struct {
		name, raw string
		want      bool
	}{
		{"exact", valid, true},
		{"native optional", valid + "core.precomposeunicode\ntrue\x00", true},
		{"empty", "", false},
		{"truncated", strings.TrimSuffix(valid, "\x00"), false},
		{"duplicate", valid + "core.bare\ntrue\x00", false},
		{"filter", valid + "filter.neutral.clean\n/private/not-permitted\x00", false},
		{"worktree", strings.Replace(valid, "core.bare\ntrue", "core.bare\nfalse", 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validProductionSourceConfig([]byte(test.raw)) != test.want {
				t.Fatal("closed source config parser changed")
			}
		})
	}
	blob := []byte("neutral\n")
	entries := []executionCheckoutEntry{{object: gitSHA1ObjectID("blob", blob)}}
	batch := []byte(fmt.Sprintf("%s blob %d\n%s\n", entries[0].object, len(blob), blob))
	for _, raw := range [][]byte{batch[:len(batch)-1], append(append([]byte(nil), batch...), 'x'), []byte(strings.Replace(string(batch), "blob", "tree", 1))} {
		if validateProductionSourceBlobs(entries, raw) == nil {
			t.Fatal("malformed source raw-object stream admitted")
		}
	}
	if validateProductionSourceBlobs(entries, batch) != nil {
		t.Fatal("valid source raw-object stream refused")
	}
	config := productionServeConfig([32]byte{1})
	if _, err := dispatchadmission.New(t.Context(), config); err != nil || config.Limits.WireBytes != 3_840_256 {
		t.Fatalf("small source-owned rehearsal bounds: %v", err)
	}
	var total uint64
	for _, role := range config.Phases[0].Roles {
		total += role.Attempts
	}
	if total != config.Limits.Attempts {
		t.Fatal("role ceilings do not exhaust the exact local attempt cap")
	}
}

func TestExecutionProductionRejectsNakedAndZeroValues(t *testing.T) {
	parent, _ := inputCustodyTestFixture(t)
	request := ExecutionProductionRequest{Git: &ExecutionGitCustody{}, Builds: &ExecutionGoBuildCustody{},
		Phebs: &ExecutionToolCustody{}, Zoekt: &ExecutionToolCustody{}, Surreal: &ExecutionToolCustody{},
		SourceCommit: strings.Repeat("a", 40), Listen: "127.0.0.1:12345"}
	if custody, err := PrepareExecutionProduction(t.Context(), parent, request); err != ErrExecutionProductionCustody || custody != nil {
		t.Fatal("caller-authored empty handles admitted")
	}
	for _, custody := range []*ExecutionProductionCustody{nil, {}} {
		if run, err := custody.StartServe(t.Context()); err != ErrExecutionProductionCustody || run != nil {
			t.Fatal("zero custody launched")
		}
	}
	for _, run := range []*ExecutionProductionRun{nil, {}} {
		if _, err := run.Stop(t.Context()); err != ErrExecutionProductionCustody {
			t.Fatal("zero run stopped successfully")
		}
		if _, err := run.Wait(t.Context()); err != ErrExecutionProductionCustody {
			t.Fatal("zero run joined successfully")
		}
	}
}
