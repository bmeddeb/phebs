//go:build linux

package t421

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// syntheticGitExecPath builds the measured Ubuntu exec-path shape at fixture
// scale: two native helpers, three symlinks that collapse onto them, one sourced
// text helper without an executable bit, one script helper and one flat
// mergetools directory of two sourced text files. The core digest is taken before
// mutate runs, so a mutation can invalidate the directory and the bound digest
// independently.
func syntheticGitExecPath(t *testing.T, mutate func(directory string)) (string, string) {
	t.Helper()
	base := requireExternalDelegationBase(t)
	directory := writeExternalDelegationDirectory(t, filepath.Join(base, "exec-path"), 0o755)
	writeExternalDelegationFile(t, filepath.Join(directory, "git"), externalDelegationELF(1), 0o755)
	writeExternalDelegationFile(t, filepath.Join(directory, "git-remote-http"), externalDelegationELF(2), 0o755)
	writeExternalDelegationFile(t, filepath.Join(directory, "git-sh-setup"), []byte("# sourced\n"), 0o644)
	writeExternalDelegationFile(t, filepath.Join(directory, "git-submodule"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	for _, link := range []struct{ name, target string }{
		{"git-http-push", "git-remote-http"},
		{"git-rev-parse", "git"},
		{"git-status", "git"},
	} {
		if err := os.Symlink(link.target, filepath.Join(directory, link.name)); err != nil {
			t.Fatal(err)
		}
	}
	tools := writeExternalDelegationDirectory(t, filepath.Join(directory, "mergetools"), 0o755)
	writeExternalDelegationFile(t, filepath.Join(tools, "diffuse"), []byte("# sourced\n"), 0o644)
	writeExternalDelegationFile(t, filepath.Join(tools, "meld"), []byte("# sourced\n"), 0o644)
	digest, err := t4013.DigestHostExecutable(t.Context(), filepath.Join(directory, "git"))
	if err != nil {
		t.Fatalf("synthetic core digest failed: %v", err)
	}
	if mutate != nil {
		mutate(directory)
	}
	return directory, digest
}

// replaceExecPathSymlink repoints one fixture link without disturbing the rest of
// the census, so a single delegation shape can be varied per case.
func replaceExecPathSymlink(t *testing.T, directory, name, target string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// wantSyntheticExecPathCanonical is the exact canonical encoding of the
// unmutated fixture: the sorted root rows, then one length-prefixed subdirectory
// header, then the sorted child rows. It is derived independently of the encoder
// so a change to the field order, the length prefixes or the permission masking
// fails here rather than silently re-digesting.
const wantSyntheticExecPathCanonical = "regular 3:git;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"symlink 13:git-http-push;0:;1:0;0:;15:git-remote-http;\n" +
	"regular 15:git-remote-http;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"symlink 13:git-rev-parse;0:;1:0;0:;3:git;\n" +
	"regular 12:git-sh-setup;4:text;2:10;10:-rw-r--r--;0:;\n" +
	"symlink 10:git-status;0:;1:0;0:;3:git;\n" +
	"regular 13:git-submodule;6:script;2:17;10:-rwxr-xr-x;0:;\n" +
	"directory 10:mergetools;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"subdirectory 10:mergetools;\n" +
	"regular 7:diffuse;4:text;2:10;10:-rw-r--r--;0:;\n" +
	"regular 4:meld;4:text;2:10;10:-rw-r--r--;0:;\n"

// wantSyntheticExecPathDigest is the SHA-256 of wantSyntheticExecPathCanonical,
// computed independently of this repository's digest helper. Both constants are
// pinned so the production digest function is itself under test: a change to the
// "sha256:" prefix or to the hash function fails the assertion below rather than
// silently re-deriving the expectation from the code being checked.
const wantSyntheticExecPathDigest = "sha256:cebe6dcdb4bab45750938314d849c878929025033a2ce6cc26d1b7c41e78551b"

// TestLinuxExternalGitExecPathCensusBindsCoreImage pins the whole manifest shape
// of one admitted exec-path: every counter, the bound core digest, the canonical
// encoding and the provenance string, plus the fact that a second census of the
// same directory is byte-identical.
func TestLinuxExternalGitExecPathCensusBindsCoreImage(t *testing.T) {
	if derived := externalDelegationDigest(wantSyntheticExecPathCanonical); derived != wantSyntheticExecPathDigest {
		t.Fatalf("digest helper = %s, independently computed %s", derived, wantSyntheticExecPathDigest)
	}
	directory, digest := syntheticGitExecPath(t, nil)
	manifest, err := censusExternalGitExecPath(t.Context(), directory, digest)
	if err != nil {
		t.Fatalf("synthetic exec-path census refused: %v", err)
	}
	want := ExecutionGitExecPathManifest{
		Role:                "git",
		CoreSHA256:          digest,
		ManifestSHA256:      wantSyntheticExecPathDigest,
		Entries:             8,
		RegularFiles:        4,
		Symlinks:            3,
		Directories:         1,
		CoreImageSymlinks:   2,
		SymlinkTargets:      2,
		NativeHelpers:       2,
		ScriptHelpers:       1,
		TextHelpers:         3,
		SubdirectoryEntries: 2,
		SubdirectoryBytes:   20,
		Provenance:          "external-exec-path-manifest-linux-amd64-v1",
	}
	if manifest != want {
		t.Fatalf("manifest =\n%#v\nwant\n%#v", manifest, want)
	}
	// "exec-path" is deliberately absent from the forbidden list: the manifest's
	// own provenance tag names the recipe, so the meaningful leaks here are the
	// private directory and the two censused entry names.
	assertExternalDelegationPathFree(t, manifest, directory, "mergetools", "git-remote-http")
	repeat, err := censusExternalGitExecPath(t.Context(), directory, digest)
	if err != nil || repeat != manifest {
		t.Fatalf("second census = %#v, %v", repeat, err)
	}
}

// TestLinuxExternalGitExecPathCensusRefusesUnadmittedShapes pins that the recipe
// is structural rather than nominal. Each mutation breaks exactly one admission
// rule, and each refusal names that rule rather than leaking the fixture path.
func TestLinuxExternalGitExecPathCensusRefusesUnadmittedShapes(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		mutate func(t *testing.T, directory string)
	}{
		{
			name: "dangling_core_symlink",
			want: "external Git exec-path symlink does not resolve to a regular sibling helper",
			mutate: func(t *testing.T, directory string) {
				if err := os.Remove(filepath.Join(directory, "git")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "core_absent_without_delegation",
			want: "external Git exec-path does not delegate to a native core helper",
			mutate: func(t *testing.T, directory string) {
				for _, name := range []string{"git", "git-rev-parse", "git-status"} {
					if err := os.Remove(filepath.Join(directory, name)); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			// A symlinked core is refused by the sibling rule before the core-entry
			// check can see it: keeping targets["git"] non-zero requires a link whose
			// bare target is "git", and that link cannot resolve to a regular sibling
			// once "git" is itself a link. The structural rule therefore strictly
			// dominates the nominal one, which is what this case pins.
			name: "core_replaced_by_a_symlink",
			want: "external Git exec-path symlink does not resolve to a regular sibling helper",
			mutate: func(t *testing.T, directory string) {
				if err := os.Remove(filepath.Join(directory, "git")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("git-remote-http", filepath.Join(directory, "git")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "core_is_a_script",
			want: "external Git exec-path does not delegate to a native core helper",
			mutate: func(t *testing.T, directory string) {
				writeExternalDelegationFile(t, filepath.Join(directory, "git"),
					[]byte("#!/bin/sh\nexit 0\n"), 0o755)
			},
		},
		{
			name: "core_content_differs",
			want: "external Git exec-path core helper differs from the selected image",
			mutate: func(t *testing.T, directory string) {
				writeExternalDelegationFile(t, filepath.Join(directory, "git"),
					externalDelegationELF(9), 0o755)
			},
		},
		{
			name: "no_symlink_delegates_to_the_core",
			want: "external Git exec-path does not delegate to a native core helper",
			mutate: func(t *testing.T, directory string) {
				for _, name := range []string{"git-rev-parse", "git-status"} {
					if err := os.Remove(filepath.Join(directory, name)); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "symlink_escapes_the_directory",
			want: "external Git exec-path symlink does not name a bare sibling helper",
			mutate: func(t *testing.T, directory string) {
				replaceExecPathSymlink(t, directory, "git-status", "../escape")
			},
		},
		{
			name: "symlink_names_itself",
			want: "external Git exec-path symlink does not name a bare sibling helper",
			mutate: func(t *testing.T, directory string) {
				replaceExecPathSymlink(t, directory, "git-status", "git-status")
			},
		},
		{
			name: "symlink_names_the_directory_itself",
			want: "external Git exec-path symlink does not name a bare sibling helper",
			mutate: func(t *testing.T, directory string) {
				replaceExecPathSymlink(t, directory, "git-status", ".")
			},
		},
		{
			name: "symlink_names_a_subdirectory",
			want: "external Git exec-path symlink does not resolve to a regular sibling helper",
			mutate: func(t *testing.T, directory string) {
				replaceExecPathSymlink(t, directory, "git-status", "mergetools")
			},
		},
		{
			name: "symlink_chains_to_another_symlink",
			want: "external Git exec-path symlink does not resolve to a regular sibling helper",
			mutate: func(t *testing.T, directory string) {
				replaceExecPathSymlink(t, directory, "git-status", "git-rev-parse")
			},
		},
		{
			name: "root_holds_a_fifo",
			want: "external delegation entry is not an admitted file class",
			mutate: func(t *testing.T, directory string) {
				if err := unix.Mkfifo(filepath.Join(directory, "fifo-helper"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "root_helper_is_setuid",
			want: "external delegation entry is not an admitted file class",
			mutate: func(t *testing.T, directory string) {
				if err := os.Chmod(filepath.Join(directory, "git-remote-http"), os.ModeSetuid|0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "too_many_subdirectories",
			want: "external Git exec-path exceeds its directory or symlink-target bound",
			mutate: func(t *testing.T, directory string) {
				for _, name := range []string{"extra-a", "extra-b", "extra-c", "extra-d"} {
					writeExternalDelegationDirectory(t, filepath.Join(directory, name), 0o755)
				}
			},
		},
		{
			name: "too_many_symlink_targets",
			want: "external Git exec-path exceeds its directory or symlink-target bound",
			mutate: func(t *testing.T, directory string) {
				for index := range 7 {
					name := "extra-" + string(rune('a'+index))
					writeExternalDelegationFile(t, filepath.Join(directory, name),
						externalDelegationELF(byte(3+index)), 0o755)
					if err := os.Symlink(name, filepath.Join(directory, "link-"+name)); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "subdirectory_holds_a_symlink",
			want: "external Git exec-path subdirectory holds an unadmitted entry",
			mutate: func(t *testing.T, directory string) {
				tools := filepath.Join(directory, "mergetools")
				if err := os.Symlink("meld", filepath.Join(tools, "diffuse-link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "subdirectory_is_empty",
			want: "external Git exec-path subdirectory is empty or exceeds its byte bound",
			mutate: func(t *testing.T, directory string) {
				tools := filepath.Join(directory, "mergetools")
				for _, name := range []string{"diffuse", "meld"} {
					if err := os.Remove(filepath.Join(tools, name)); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "subdirectory_exceeds_its_entry_bound",
			want: "external delegation directory exceeds its entry bound",
			mutate: func(t *testing.T, directory string) {
				tools := filepath.Join(directory, "mergetools")
				for index := range maxGitExecPathSubdirectoryEntries + 1 {
					writeExternalDelegationFile(t, filepath.Join(tools, fmt.Sprintf("tool-%03d", index)),
						[]byte("# sourced\n"), 0o644)
				}
			},
		},
		{
			name: "subdirectory_exceeds_its_byte_bound",
			want: "external Git exec-path subdirectory is empty or exceeds its byte bound",
			mutate: func(t *testing.T, directory string) {
				// A sparse truncate reports the size the bound is written against
				// without materialising 64 MiB of helper bytes.
				if err := os.Truncate(filepath.Join(directory, "mergetools", "diffuse"),
					maxGitExecPathSubdirectoryBytes); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			directory, digest := syntheticGitExecPath(t, func(fixture string) {
				testCase.mutate(t, fixture)
			})
			manifest, err := censusExternalGitExecPath(t.Context(), directory, digest)
			assertExternalDelegationRefusal(t, err, directory)
			if manifest != (ExecutionGitExecPathManifest{}) {
				t.Fatalf("refused census returned %#v", manifest)
			}
			// Exact equality rather than a substring: every message in this table is
			// the complete production string, so a reordering that swaps one rule for
			// another fails here instead of passing on a shared prefix.
			if err.Error() != testCase.want {
				t.Fatalf("refusal = %q, want %q", err.Error(), testCase.want)
			}
		})
	}
	t.Run("directory_is_absent", func(t *testing.T) {
		base := requireExternalDelegationBase(t)
		absent := filepath.Join(base, "absent")
		manifest, err := censusExternalGitExecPath(t.Context(), absent, "sha256:"+strings.Repeat("0", 64))
		assertExternalDelegationRefusal(t, err, absent)
		if err.Error() != "external Git exec-path directory cannot be opened as a census root" {
			t.Fatalf("refusal = %q", err.Error())
		}
		if manifest != (ExecutionGitExecPathManifest{}) {
			t.Fatalf("refused census returned %#v", manifest)
		}
	})
	t.Run("context_already_expired", func(t *testing.T) {
		directory, digest := syntheticGitExecPath(t, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		manifest, err := censusExternalGitExecPath(ctx, directory, digest)
		assertExternalDelegationRefusal(t, err, directory)
		if err.Error() != "external delegation census is unavailable" {
			t.Fatalf("refusal = %q", err.Error())
		}
		if manifest != (ExecutionGitExecPathManifest{}) {
			t.Fatalf("refused census returned %#v", manifest)
		}
	})
}

// TestLinuxExternalGitExecPathManifestObservation runs the recipe against this
// host's real selected Git image and re-derives every counter with an independent
// os.ReadDir walk, so the manifest is checked against the directory rather than
// against itself.
func TestLinuxExternalGitExecPathManifestObservation(t *testing.T) {
	requireExternalDelegationProbeParent(t)
	binary := requireLinuxExternalTool(t, "git")
	manifest, err := ObserveExecutionGitExecPathManifest(t.Context(), binary)
	if err != nil {
		t.Fatalf("real exec-path observation refused: %v", err)
	}
	directory := linuxHostToolVersion(t, binary, "--exec-path")
	rows, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var regular, symlinks, directories, coreLinks, subdirectoryEntries int
	targets := make(map[string]int)
	classes := map[string]int{}
	for _, row := range rows {
		info, err := row.Info()
		if err != nil {
			t.Fatal(err)
		}
		switch mode := info.Mode(); {
		case mode&os.ModeSymlink != 0:
			symlinks++
			target, err := os.Readlink(filepath.Join(directory, row.Name()))
			if err != nil {
				t.Fatal(err)
			}
			targets[target]++
			if target == "git" {
				coreLinks++
			}
		case mode.IsDir():
			directories++
			children, err := os.ReadDir(filepath.Join(directory, row.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var total int64
			for _, child := range children {
				childInfo, err := child.Info()
				if err != nil {
					t.Fatal(err)
				}
				if !childInfo.Mode().IsRegular() || childInfo.Size() == 0 {
					t.Fatalf("subdirectory %q holds an unadmitted entry %q", row.Name(), child.Name())
				}
				total += childInfo.Size()
				classes[externalDelegationFixtureClass(t, filepath.Join(directory, row.Name(), child.Name()))]++
			}
			subdirectoryEntries += len(children)
			if total != manifest.SubdirectoryBytes {
				t.Fatalf("subdirectory bytes = %d, independent walk %d", manifest.SubdirectoryBytes, total)
			}
		case mode.IsRegular():
			regular++
			classes[externalDelegationFixtureClass(t, filepath.Join(directory, row.Name()))]++
		default:
			t.Fatalf("exec-path holds an unadmitted entry %q (%v)", row.Name(), mode)
		}
	}
	if manifest.Entries != len(rows) || manifest.RegularFiles != regular || manifest.Symlinks != symlinks ||
		manifest.Directories != directories || manifest.CoreImageSymlinks != coreLinks ||
		manifest.SymlinkTargets != len(targets) || manifest.SubdirectoryEntries != subdirectoryEntries {
		t.Fatalf("manifest %#v disagrees with the independent walk: entries %d regular %d symlink %d directory %d core %d targets %d subdirectory %d",
			manifest, len(rows), regular, symlinks, directories, coreLinks, len(targets), subdirectoryEntries)
	}
	if manifest.NativeHelpers != classes[externalDelegationNative] ||
		manifest.ScriptHelpers != classes[externalDelegationScript] ||
		manifest.TextHelpers != classes[externalDelegationText] {
		t.Fatalf("helper classes %#v disagree with the independent walk %#v", manifest, classes)
	}
	if manifest.Role != "git" || manifest.Provenance != "external-exec-path-manifest-linux-amd64-v1" {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	want, err := t4013.DigestHostExecutable(t.Context(), binary)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CoreSHA256 != want {
		t.Fatalf("bound core digest %s, independently %s", manifest.CoreSHA256, want)
	}
	// Both names are real entries of this host's exec-path — the one subdirectory
	// and the most-delegated non-core helper — so the assertion pins that a bare
	// entry name cannot reach the caller either, not merely that a path separator
	// cannot. The synthetic census test forbids the same names.
	assertExternalDelegationPathFree(t, manifest, binary, directory, "mergetools", "git-remote-http")
	repeat, err := ObserveExecutionGitExecPathManifest(t.Context(), binary)
	if err != nil {
		t.Fatalf("second real observation refused: %v", err)
	}
	if repeat != manifest {
		t.Fatalf("observation is not deterministic:\n%#v\n%#v", manifest, repeat)
	}
}

// externalDelegationFixtureClass applies the same 64-byte header screen the
// census uses, through an independent read, so the real-host counters are not
// checked against the production classifier.
func externalDelegationFixtureClass(t *testing.T, path string) string {
	t.Helper()
	image, err := t4013.OpenHostImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()
	header := make([]byte, maxExternalDelegationHeaderBytes)
	read, err := image.Read(header)
	if err != nil && read == 0 {
		t.Fatal(err)
	}
	switch {
	case bytes.HasPrefix(header[:read], []byte("\x7fELF")):
		return externalDelegationNative
	case bytes.HasPrefix(header[:read], []byte("#!")):
		return externalDelegationScript
	default:
		return externalDelegationText
	}
}

// TestLinuxExternalGitExecPathManifestRefusesUnadmittedImages pins that the
// recipe performs no discovery, admits no script and no shim, and leaves no
// scratch behind on any refusal path.
func TestLinuxExternalGitExecPathManifestRefusesUnadmittedImages(t *testing.T) {
	parent := requireExternalDelegationProbeParent(t)
	binary := requireLinuxExternalTool(t, "git")
	base := requireExternalDelegationFixtureBase(t, parent)
	cases := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		path func(t *testing.T) string
		// Each refusal message is pinned exactly rather than merely observed to
		// exist, so the fence that refuses a bad image is named: a regression
		// that admits the image and then fails later in the recipe changes this
		// string and fails the case.
		want string
	}{
		{
			name: "nil_context",
			ctx:  func(*testing.T) context.Context { return nil },
			path: func(t *testing.T) string { return binary },
			want: "external Git exec-path manifest requires a context and the frozen Linux platform",
		},
		{
			name: "expired_context",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			path: func(t *testing.T) string { return binary },
			want: "external Git exec-path observation canceled",
		},
		{
			name: "relative_path",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string { return filepath.Base(binary) },
			want: "external delegation image requires an explicit absolute path",
		},
		{
			name: "empty_path",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(*testing.T) string { return "" },
			want: "external delegation image requires an explicit absolute path",
		},
		{
			name: "absent_image",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string {
				return filepath.Join(requireExternalDelegationCaseDirectory(t, base), "absent-git")
			},
			want: "external delegation image cannot be resolved",
		},
		{
			// A delegating shim is refused by the native-image screen before any
			// probe runs, so it can never reach the manifest it would delegate for.
			name: "script_substitute",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string {
				return writeExternalDelegationFile(t,
					filepath.Join(requireExternalDelegationCaseDirectory(t, base), "git"),
					[]byte("#!/bin/sh\nexec "+binary+" \"$@\"\n"), 0o755)
			},
			want: "external delegation image is not a bounded native executable",
		},
		{
			name: "degraded_elf32_substitute",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string {
				return writeExternalDelegationFile(t,
					filepath.Join(requireExternalDelegationCaseDirectory(t, base), "git"),
					degradedELF32Image(t, binary), 0o755)
			},
			want: "external delegation image is not a bounded native executable",
		},
		{
			// A real native image of the wrong role passes the screen and is
			// refused by the closed probe instead, which is the generic probe
			// refusal rather than a role-specific claim.
			name: "native_image_that_is_not_git",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string { return requireLinuxExternalTool(t, "sh") },
			want: "external tool probe failed, expired, or exceeded output bound",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := testCase.path(t)
			manifest, err := ObserveExecutionGitExecPathManifest(testCase.ctx(t), path)
			assertExternalDelegationRefusal(t, err, path, binary)
			if manifest != (ExecutionGitExecPathManifest{}) {
				t.Fatalf("refused observation returned %#v", manifest)
			}
			if err.Error() != testCase.want {
				t.Fatalf("refusal = %q, want %q", err.Error(), testCase.want)
			}
			assertExternalProbeParentEmpty(t, parent)
		})
	}
}
