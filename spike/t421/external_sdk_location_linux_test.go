//go:build linux

package t421

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// syntheticGoSDK builds the measured Ubuntu GOROOT shape at fixture scale: eight
// real root directories, four root regular files, a bin holding exactly two
// native tools and a platform tool directory holding eight native tools, with the
// two source markers the recipe requires. The VERSION marker is padded to exactly
// 64 bytes so the canonical root encoding — and therefore RootSHA256 — stays
// stable across toolchain releases rather than moving with the length of the
// release tag. Every directory is created through the explicit-chmod helper, so no
// permission bit in the canonical encoding depends on the host umask. The image
// digest is taken before mutate runs, so a mutation can invalidate the tree and
// the bound digest independently.
func syntheticGoSDK(t *testing.T, mutate func(goroot string)) (string, string, string) {
	t.Helper()
	base := requireExternalDelegationBase(t)
	goroot := writeExternalDelegationDirectory(t, filepath.Join(base, "goroot"), 0o755)
	platform := runtime.GOOS + "_" + runtime.GOARCH
	tooldir := filepath.Join(goroot, "pkg", "tool", platform)
	for _, directory := range []string{"api", "bin", "doc", "lib", "misc", "pkg", "src", "test"} {
		writeExternalDelegationDirectory(t, filepath.Join(goroot, directory), 0o755)
	}
	writeExternalDelegationDirectory(t, filepath.Join(goroot, "pkg", "tool"), 0o755)
	writeExternalDelegationDirectory(t, tooldir, 0o755)
	writeExternalDelegationDirectory(t, filepath.Join(goroot, "src", "go"), 0o755)
	writeExternalDelegationDirectory(t, filepath.Join(goroot, "src", "runtime"), 0o755)
	writeExternalDelegationFile(t, filepath.Join(goroot, "LICENSE"), []byte("license\n"), 0o644)
	writeExternalDelegationFile(t, filepath.Join(goroot, "PATENTS"), []byte("patents\n"), 0o644)
	writeExternalDelegationFile(t, filepath.Join(goroot, "go.env"), []byte("GOFLAGS=\n"), 0o644)
	writeExternalDelegationFile(t, filepath.Join(goroot, "VERSION"), paddedGoSDKVersionMarker(t), 0o644)
	writeExternalDelegationFile(t, filepath.Join(goroot, "bin", "go"), externalDelegationELF(1), 0o755)
	writeExternalDelegationFile(t, filepath.Join(goroot, "bin", "gofmt"), externalDelegationELF(2), 0o755)
	for _, tool := range goSDKFixtureTools {
		writeExternalDelegationFile(t, filepath.Join(tooldir, tool), externalDelegationELF(3), 0o755)
	}
	digest, err := t4013.DigestHostExecutable(t.Context(), filepath.Join(goroot, "bin", "go"))
	if err != nil {
		t.Fatalf("synthetic SDK image digest failed: %v", err)
	}
	if mutate != nil {
		mutate(goroot)
	}
	return goroot, tooldir, digest
}

// goSDKFixtureTools is the platform tool name set this host's real GOTOOLDIR
// holds, reproduced at fixture scale.
var goSDKFixtureTools = []string{"asm", "cgo", "compile", "cover", "fix", "link", "preprofile", "vet"}

// paddedGoSDKVersionMarker returns a marker whose first line names the running
// toolchain and whose total length is exactly 64 bytes.
func paddedGoSDKVersionMarker(t *testing.T) []byte {
	t.Helper()
	release := runtime.Version()
	if len(release) > 62 {
		t.Fatalf("release tag too long for the padded marker: %d", len(release))
	}
	return []byte(release + "\n" + strings.Repeat("#", 62-len(release)) + "\n")
}

// wantSyntheticGoSDKRootCanonical is the exact canonical encoding of the fixture
// GOROOT root, hand-derived from the row format in externalDelegationCanonical and
// byte-sorted: uppercase marker names precede every lowercase entry, and "doc"
// precedes "go.env" precedes "lib". Directory rows carry a zero size because
// classifyExternalDelegationRow records a size only for regular entries, and
// their permission string is the ten-character Perm() rendering rather than the
// drwxr-xr-x form, because FileMode.Perm masks out ModeDir.
const wantSyntheticGoSDKRootCanonical = "regular 7:LICENSE;4:text;1:8;10:-rw-r--r--;0:;\n" +
	"regular 7:PATENTS;4:text;1:8;10:-rw-r--r--;0:;\n" +
	"regular 7:VERSION;4:text;2:64;10:-rw-r--r--;0:;\n" +
	"directory 3:api;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"directory 3:bin;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"directory 3:doc;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"regular 6:go.env;4:text;1:9;10:-rw-r--r--;0:;\n" +
	"directory 3:lib;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"directory 4:misc;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"directory 3:pkg;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"directory 3:src;0:;1:0;10:-rwxr-xr-x;0:;\n" +
	"directory 4:test;0:;1:0;10:-rwxr-xr-x;0:;\n"

// wantSyntheticGoSDKToolCanonical is the exact canonical encoding of the fixture
// platform tool directory. No tool body is hashed, so the digest is over names,
// admitted class, size and permissions only.
const wantSyntheticGoSDKToolCanonical = "regular 3:asm;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"regular 3:cgo;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"regular 7:compile;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"regular 5:cover;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"regular 3:fix;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"regular 4:link;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"regular 10:preprofile;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
	"regular 3:vet;6:native;3:128;10:-rwxr-xr-x;0:;\n"

// wantSyntheticGoSDKRootDigest and wantSyntheticGoSDKToolDigest are the SHA-256
// values of the two canonical constants above, computed independently of this
// repository's digest helper. Both are pinned so the production digest function is
// itself under test: a change to the "sha256:" prefix or to the hash function
// fails the assertions in the census test rather than silently re-deriving the
// expectation from the code being checked.
const wantSyntheticGoSDKRootDigest = "sha256:7723ae0c8077411ac69e4d7fd0bfbb11cf06e1dfdeb839c9073d2a5a99adbb45"

const wantSyntheticGoSDKToolDigest = "sha256:7f88555343375bcb9fbad80d8db05e36da61aefb570145b350370f40ffc5a268"

// TestLinuxExternalGoSDKLocationCensusBindsAdmittedImage pins that one census of
// a fully admitted synthetic SDK reports the exact bounded shape, digests the root
// and tool directory canonically rather than by walking the tree, records the
// release the VERSION marker was required to name, and leaks no host path and no
// SDK entry name.
func TestLinuxExternalGoSDKLocationCensusBindsAdmittedImage(t *testing.T) {
	if derived := externalDelegationDigest(wantSyntheticGoSDKRootCanonical); derived != wantSyntheticGoSDKRootDigest {
		t.Fatalf("root digest helper = %s, independently computed %s", derived, wantSyntheticGoSDKRootDigest)
	}
	if derived := externalDelegationDigest(wantSyntheticGoSDKToolCanonical); derived != wantSyntheticGoSDKToolDigest {
		t.Fatalf("tool digest helper = %s, independently computed %s", derived, wantSyntheticGoSDKToolDigest)
	}
	goroot, tooldir, digest := syntheticGoSDK(t, nil)
	location, err := censusExternalGoSDKLocation(t.Context(), goroot, tooldir, digest)
	if err != nil {
		t.Fatalf("synthetic SDK census refused: %v", err)
	}
	want := ExecutionGoSDKLocation{
		Role:                 "go",
		ImageSHA256:          digest,
		Version:              runtime.Version(),
		RootEntries:          12,
		RootDirectories:      8,
		RootSHA256:           wantSyntheticGoSDKRootDigest,
		BinEntries:           2,
		ToolDirectoryEntries: 8,
		ToolDirectorySHA256:  wantSyntheticGoSDKToolDigest,
		Provenance:           "external-go-sdk-location-linux-amd64-v1",
	}
	if location != want {
		t.Fatalf("census =\n%#v\nwant\n%#v", location, want)
	}
	// "go", "linux" and "amd64" are deliberately absent from the forbidden list:
	// the role, the release tag and the provenance platform suffix all name them by
	// design, so the meaningful leaks here are the private directories and the
	// censused SDK entry names.
	assertExternalDelegationPathFree(t, location, goroot, tooldir, "VERSION", "gofmt", "preprofile")
	again, err := censusExternalGoSDKLocation(t.Context(), goroot, tooldir, digest)
	if err != nil {
		t.Fatalf("repeat synthetic SDK census refused: %v", err)
	}
	if again != location {
		t.Fatalf("census is not deterministic: %#v then %#v", location, again)
	}
}

// TestLinuxExternalGoSDKLocationCensusRefusesUnadmittedShapes pins the fail-closed
// contract of the SDK recipe across every fence it owns, in the order the census
// applies them: the bounded VERSION marker, the root census and its marker set,
// the exact bin pair, the bin/go digest binding behind it, the flat native tool
// directory and the two source markers. Each case also pins that a refusal returns
// the zero location rather than a partial description of an unadmitted SDK.
func TestLinuxExternalGoSDKLocationCensusRefusesUnadmittedShapes(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		mutate func(t *testing.T, goroot string)
	}{
		{
			name: "version_marker_is_absent",
			want: "external Go SDK version marker is not a bounded regular file",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Remove(filepath.Join(goroot, "VERSION")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "version_marker_is_empty",
			want: "external Go SDK version marker is not a bounded regular file",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Truncate(filepath.Join(goroot, "VERSION"), 0); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// The marker bound is enforced against the Lstat size, so a sparse
			// truncate trips it without materialising four kibibytes of content.
			name: "version_marker_exceeds_its_bound",
			want: "external Go SDK version marker is not a bounded regular file",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Truncate(filepath.Join(goroot, "VERSION"), maxGoSDKVersionBytes+1); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "version_marker_is_a_symlink",
			want: "external Go SDK version marker is not a bounded regular file",
			mutate: func(t *testing.T, goroot string) {
				path := filepath.Join(goroot, "VERSION")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("LICENSE", path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "version_marker_names_another_release",
			want: "external Go SDK version marker does not name the verifier toolchain",
			mutate: func(t *testing.T, goroot string) {
				writeExternalDelegationFile(t, filepath.Join(goroot, "VERSION"), []byte("go1.0.0\n"), 0o644)
			},
		},
		{
			name: "root_holds_a_fifo",
			want: "external delegation entry is not an admitted file class",
			mutate: func(t *testing.T, goroot string) {
				if err := unix.Mkfifo(filepath.Join(goroot, "fifo-marker"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// os.Chmod needs the explicit FileMode setuid bit: the octal literal
			// 0o4644 is silently dropped by Go's syscallMode translation, so a
			// fixture written that way would not be setuid at all.
			name: "root_file_is_setuid",
			want: "external delegation entry is not an admitted file class",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Chmod(filepath.Join(goroot, "LICENSE"), os.ModeSetuid|0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "root_exceeds_its_entry_bound",
			want: "external delegation directory exceeds its entry bound",
			mutate: func(t *testing.T, goroot string) {
				for index := range maxGoSDKRootEntries - 11 {
					writeExternalDelegationFile(t, filepath.Join(goroot, fmt.Sprintf("extra-%03d", index)),
						[]byte("extra\n"), 0o644)
				}
			},
		},
		{
			name: "root_holds_a_symlink",
			want: "external Go SDK root holds an unadmitted entry class",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Symlink("LICENSE", filepath.Join(goroot, "README.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "root_missing_a_directory_marker",
			want: "external Go SDK root is missing an admitted directory marker",
			mutate: func(t *testing.T, goroot string) {
				if err := os.RemoveAll(filepath.Join(goroot, "lib")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// A marker replaced by a regular file still satisfies the census class
			// switch, so it is this marker-shape fence rather than the class fence
			// that refuses a substituted GOROOT layout.
			name: "root_directory_marker_is_a_file",
			want: "external Go SDK root is missing an admitted directory marker",
			mutate: func(t *testing.T, goroot string) {
				if err := os.RemoveAll(filepath.Join(goroot, "lib")); err != nil {
					t.Fatal(err)
				}
				writeExternalDelegationFile(t, filepath.Join(goroot, "lib"), []byte("not a directory\n"), 0o644)
			},
		},
		{
			name: "root_missing_a_file_marker",
			want: "external Go SDK root is missing an admitted file marker",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Remove(filepath.Join(goroot, "go.env")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "root_file_marker_is_a_directory",
			want: "external Go SDK root is missing an admitted file marker",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Remove(filepath.Join(goroot, "go.env")); err != nil {
					t.Fatal(err)
				}
				writeExternalDelegationDirectory(t, filepath.Join(goroot, "go.env"), 0o755)
			},
		},
		{
			name: "bin_exceeds_its_entry_bound",
			want: "external delegation directory exceeds its entry bound",
			mutate: func(t *testing.T, goroot string) {
				for index := range maxGoSDKBinEntries - 1 {
					writeExternalDelegationFile(t, filepath.Join(goroot, "bin", fmt.Sprintf("tool-%03d", index)),
						externalDelegationELF(4), 0o755)
				}
			},
		},
		{
			name: "bin_holds_an_extra_tool",
			want: "external Go SDK bin does not hold exactly its two admitted tools",
			mutate: func(t *testing.T, goroot string) {
				writeExternalDelegationFile(t, filepath.Join(goroot, "bin", "godoc"), externalDelegationELF(4), 0o755)
			},
		},
		{
			// Removing bin/go leaves one admitted row in bin, so the exact-pair fence
			// in front of the digest binding is what refuses: the census bounds the
			// bin rows before any bin/go body is read, which is the precedence the
			// recipe keeps. The digest fence itself is pinned by the case below.
			name: "bin_go_is_absent",
			want: "external Go SDK bin does not hold exactly its two admitted tools",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Remove(filepath.Join(goroot, "bin", "go")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bin_tool_is_a_script",
			want: "external Go SDK bin is missing an admitted native tool",
			mutate: func(t *testing.T, goroot string) {
				writeExternalDelegationFile(t, filepath.Join(goroot, "bin", "gofmt"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
			},
		},
		{
			name: "bin_tool_is_not_executable",
			want: "external Go SDK bin is missing an admitted native tool",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Chmod(filepath.Join(goroot, "bin", "gofmt"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bin_go_differs_from_the_admitted_image",
			want: "external Go GOROOT bin/go differs from the admitted image",
			mutate: func(t *testing.T, goroot string) {
				writeExternalDelegationFile(t, filepath.Join(goroot, "bin", "go"), externalDelegationELF(9), 0o755)
			},
		},
		{
			name: "tool_directory_is_absent",
			want: "external delegation directory cannot be opened as a census root",
			mutate: func(t *testing.T, goroot string) {
				if err := os.RemoveAll(filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tool_directory_is_empty",
			want: "external Go SDK tool directory is empty",
			mutate: func(t *testing.T, goroot string) {
				for _, tool := range goSDKFixtureTools {
					if err := os.Remove(filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, tool)); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "tool_directory_exceeds_its_entry_bound",
			want: "external delegation directory exceeds its entry bound",
			mutate: func(t *testing.T, goroot string) {
				tooldir := filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)
				for index := range maxGoSDKToolEntries - 7 {
					writeExternalDelegationFile(t, filepath.Join(tooldir, fmt.Sprintf("extra-%03d", index)),
						externalDelegationELF(4), 0o755)
				}
			},
		},
		{
			name: "tool_directory_holds_a_subdirectory",
			want: "external Go SDK tool directory holds an unadmitted entry",
			mutate: func(t *testing.T, goroot string) {
				writeExternalDelegationDirectory(t,
					filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, "nested"), 0o755)
			},
		},
		{
			name: "tool_directory_holds_a_symlink",
			want: "external Go SDK tool directory holds an unadmitted entry",
			mutate: func(t *testing.T, goroot string) {
				tooldir := filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)
				if err := os.Remove(filepath.Join(tooldir, "asm")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("vet", filepath.Join(tooldir, "asm")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tool_directory_helper_is_a_script",
			want: "external Go SDK tool directory holds an unadmitted entry",
			mutate: func(t *testing.T, goroot string) {
				writeExternalDelegationFile(t,
					filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, "vet"),
					[]byte("#!/bin/sh\nexit 0\n"), 0o755)
			},
		},
		{
			name: "tool_directory_tool_is_not_executable",
			want: "external Go SDK tool directory holds an unadmitted entry",
			mutate: func(t *testing.T, goroot string) {
				if err := os.Chmod(filepath.Join(goroot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, "vet"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "source_marker_is_absent",
			want: "external Go SDK source marker is not a real resolved directory",
			mutate: func(t *testing.T, goroot string) {
				if err := os.RemoveAll(filepath.Join(goroot, "src", "runtime")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "source_marker_is_a_file",
			want: "external Go SDK source marker is not a real resolved directory",
			mutate: func(t *testing.T, goroot string) {
				if err := os.RemoveAll(filepath.Join(goroot, "src", "go")); err != nil {
					t.Fatal(err)
				}
				writeExternalDelegationFile(t, filepath.Join(goroot, "src", "go"), []byte("not a directory\n"), 0o644)
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			goroot, tooldir, digest := syntheticGoSDK(t, func(fixture string) {
				testCase.mutate(t, fixture)
			})
			location, err := censusExternalGoSDKLocation(t.Context(), goroot, tooldir, digest)
			assertExternalDelegationRefusal(t, err, goroot)
			if location != (ExecutionGoSDKLocation{}) {
				t.Fatalf("refused census returned %#v", location)
			}
			// Exact equality rather than a substring: every message in this table is
			// the complete production string, so a fence reordering that lets one
			// rule refuse where another was expected fails here instead of passing
			// on a shared prefix.
			if err.Error() != testCase.want {
				t.Fatalf("refusal = %q, want %q", err.Error(), testCase.want)
			}
		})
	}
}

// TestLinuxExternalGoSDKDirectoryAdmission pins that the two reported locations
// are admitted as a matched pair bound to the selected image, so a one-line
// report, a relative or unresolved location, a non-directory, a tool directory
// outside its own GOROOT and a GOROOT that does not hold the admitted image all
// refuse before any census runs.
func TestLinuxExternalGoSDKDirectoryAdmission(t *testing.T) {
	base := requireExternalDelegationBase(t)
	goroot := writeExternalDelegationDirectory(t, filepath.Join(base, "sdk"), 0o755)
	platform := runtime.GOOS + "_" + runtime.GOARCH
	writeExternalDelegationDirectory(t, filepath.Join(goroot, "pkg"), 0o755)
	writeExternalDelegationDirectory(t, filepath.Join(goroot, "pkg", "tool"), 0o755)
	tooldir := writeExternalDelegationDirectory(t, filepath.Join(goroot, "pkg", "tool", platform), 0o755)
	image := writeExternalDelegationFile(t, filepath.Join(goroot, "bin", "go"), externalDelegationELF(1), 0o755)
	decoy := writeExternalDelegationFile(t, filepath.Join(base, "decoy-go"), externalDelegationELF(5), 0o755)
	// An unrelated tree that really does hold a platform tool directory, so the
	// pairing fence is exercised rather than the directory fence in front of it.
	outsideTool := writeExternalDelegationDirectory(t, filepath.Join(base, "outside", "pkg", "tool", platform), 0o755)
	linkRoot := filepath.Join(base, "link-sdk")
	if err := os.Symlink(goroot, linkRoot); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		output   string
		resolved string
		want     string
	}{
		{
			name:     "one_reported_location",
			output:   goroot,
			resolved: image,
			want:     "external Go SDK environment did not report exactly two locations",
		},
		{
			name:     "three_reported_locations",
			output:   goroot + "\n" + tooldir + "\n" + goroot,
			resolved: image,
			want:     "external Go SDK environment did not report exactly two locations",
		},
		{
			name:     "goroot_is_relative",
			output:   filepath.Base(goroot) + "\n" + tooldir,
			resolved: image,
			want:     "external delegation directory is invalid",
		},
		{
			name:     "goroot_is_not_fully_resolved",
			output:   linkRoot + "\n" + tooldir,
			resolved: image,
			want:     "external delegation directory is not fully resolved",
		},
		{
			name:     "goroot_is_not_a_directory",
			output:   image + "\n" + tooldir,
			resolved: image,
			want:     "external delegation directory is not a real directory",
		},
		{
			name:     "tool_directory_is_outside_its_goroot",
			output:   goroot + "\n" + outsideTool,
			resolved: image,
			want:     "external Go tool directory is not the platform directory inside its GOROOT",
		},
		{
			name:     "tool_directory_is_not_the_platform_directory",
			output:   goroot + "\n" + filepath.Join(goroot, "pkg", "tool"),
			resolved: image,
			want:     "external Go tool directory is not the platform directory inside its GOROOT",
		},
		{
			name:     "goroot_holds_another_image",
			output:   goroot + "\n" + tooldir,
			resolved: decoy,
			want:     "external Go GOROOT does not hold the admitted image as its own bin/go",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotRoot, gotTool, err := admitExternalGoSDKDirectories(testCase.output, testCase.resolved)
			assertExternalDelegationRefusal(t, err, base)
			if gotRoot != "" || gotTool != "" {
				t.Fatalf("refused admission returned %q, %q", gotRoot, gotTool)
			}
			// Exact equality rather than a substring, matching the census table
			// above: every message here is the complete production string.
			if err.Error() != testCase.want {
				t.Fatalf("refusal = %q, want %q", err.Error(), testCase.want)
			}
		})
	}
	t.Run("admitted_pair", func(t *testing.T) {
		gotRoot, gotTool, err := admitExternalGoSDKDirectories(goroot+"\n"+tooldir, image)
		if err != nil {
			t.Fatalf("admitted pair refused: %v", err)
		}
		if gotRoot != goroot || gotTool != tooldir {
			t.Fatalf("admitted pair = %q, %q, want %q, %q", gotRoot, gotTool, goroot, tooldir)
		}
	})
	t.Run("bin_go_is_absent", func(t *testing.T) {
		orphan := writeExternalDelegationDirectory(t, filepath.Join(base, "orphan"), 0o755)
		writeExternalDelegationDirectory(t, filepath.Join(orphan, "pkg", "tool", platform), 0o755)
		gotRoot, gotTool, err := admitExternalGoSDKDirectories(orphan+"\n"+filepath.Join(orphan, "pkg", "tool", platform), image)
		assertExternalDelegationRefusal(t, err, base)
		if gotRoot != "" || gotTool != "" {
			t.Fatalf("refused admission returned %q, %q", gotRoot, gotTool)
		}
		// A GOROOT with no bin/go at all and a GOROOT holding a different image
		// reach the same fence, so both assert the same complete message.
		const want = "external Go GOROOT does not hold the admitted image as its own bin/go"
		if err.Error() != want {
			t.Fatalf("refusal = %q, want %q", err.Error(), want)
		}
	})
}

// TestLinuxExternalGoSDKLocationObservation runs the recipe against this host's
// real selected Go image and re-derives every counter with an independent
// os.ReadDir walk plus an independent image digest, so the location is checked
// against the SDK rather than against itself. It pins that no SDK-wide walk is
// needed to produce the record.
func TestLinuxExternalGoSDKLocationObservation(t *testing.T) {
	parent := requireExternalDelegationProbeParent(t)
	binary := requireLinuxExternalTool(t, "go")
	location, err := ObserveExecutionGoSDKLocation(t.Context(), binary)
	if err != nil {
		t.Fatalf("real SDK observation refused: %v", err)
	}
	fields := strings.Split(linuxHostToolVersion(t, binary, "env", "GOROOT", "GOTOOLDIR"), "\n")
	if len(fields) != 2 {
		t.Fatalf("independent location probe reported %d fields", len(fields))
	}
	goroot, tooldir := fields[0], fields[1]
	wantImage, err := t4013.DigestHostExecutable(t.Context(), binary)
	if err != nil {
		t.Fatal(err)
	}
	want := ExecutionGoSDKLocation{
		Role:        "go",
		ImageSHA256: wantImage,
		Version:     runtime.Version(),
		Provenance:  "external-go-sdk-location-linux-amd64-v1",
	}
	if location.Role != want.Role || location.ImageSHA256 != want.ImageSHA256 ||
		location.Version != want.Version || location.Provenance != want.Provenance {
		t.Fatalf("location identity =\n%#v\nwant\n%#v", location, want)
	}
	for _, digest := range []string{location.RootSHA256, location.ToolDirectorySHA256} {
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
			t.Fatalf("malformed digest %q", digest)
		}
	}
	rootRows, rootDirectories := externalDelegationFixtureDirectoryShape(t, goroot)
	if location.RootEntries != rootRows || location.RootDirectories != rootDirectories {
		t.Fatalf("root shape = %d/%d, want %d/%d", location.RootEntries, location.RootDirectories, rootRows, rootDirectories)
	}
	binRows, _ := externalDelegationFixtureDirectoryShape(t, filepath.Join(goroot, "bin"))
	if location.BinEntries != binRows || binRows != 2 {
		t.Fatalf("bin entries = %d, want %d", location.BinEntries, binRows)
	}
	toolRows, _ := externalDelegationFixtureDirectoryShape(t, tooldir)
	if location.ToolDirectoryEntries != toolRows {
		t.Fatalf("tool entries = %d, want %d", location.ToolDirectoryEntries, toolRows)
	}
	// The three names are real entries of this host's GOROOT — the bounded root
	// marker, the second bin tool and one platform tool — so the assertion pins
	// that a bare entry name cannot reach the caller either, not merely that a
	// path separator cannot. The synthetic census test forbids the same names.
	assertExternalDelegationPathFree(t, location, binary, goroot, tooldir, "VERSION", "gofmt", "preprofile")
	again, err := ObserveExecutionGoSDKLocation(t.Context(), binary)
	if err != nil {
		t.Fatalf("repeat real SDK observation refused: %v", err)
	}
	if again != location {
		t.Fatalf("observation is not deterministic:\n%#v\nthen\n%#v", location, again)
	}
	assertExternalProbeParentEmpty(t, parent)
}

// externalDelegationFixtureDirectoryShape counts one directory's entries and its
// real subdirectories through an independent read, so the recipe's counters are
// not compared against the production census.
func externalDelegationFixtureDirectoryShape(t *testing.T, directory string) (entries, directories int) {
	t.Helper()
	rows, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		info, err := row.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			directories++
		}
	}
	return len(rows), directories
}

// TestLinuxExternalGoSDKLocationRefusesUnadmittedImages pins that the recipe
// performs no discovery, admits no script and no shim, and leaves no scratch
// behind on any refusal path.
func TestLinuxExternalGoSDKLocationRefusesUnadmittedImages(t *testing.T) {
	parent := requireExternalDelegationProbeParent(t)
	binary := requireLinuxExternalTool(t, "go")
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
			want: "external Go SDK location requires a context and the frozen Linux platform",
		},
		{
			name: "expired_context",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			path: func(t *testing.T) string { return binary },
			want: "external Go SDK observation canceled",
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
				return filepath.Join(requireExternalDelegationCaseDirectory(t, base), "absent-go")
			},
			want: "external delegation image cannot be resolved",
		},
		{
			// A delegating shim is refused by the native-image screen before any
			// probe runs, so it can never reach the location report it wraps.
			name: "script_substitute",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string {
				return writeExternalDelegationFile(t,
					filepath.Join(requireExternalDelegationCaseDirectory(t, base), "go"),
					[]byte("#!/bin/sh\nexec "+binary+" \"$@\"\n"), 0o755)
			},
			want: "external delegation image is not a bounded native executable",
		},
		{
			name: "degraded_elf32_substitute",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string {
				return writeExternalDelegationFile(t,
					filepath.Join(requireExternalDelegationCaseDirectory(t, base), "go"),
					degradedELF32Image(t, binary), 0o755)
			},
			want: "external delegation image is not a bounded native executable",
		},
		{
			// A real native image of the wrong role passes the screen and is
			// refused by the closed probe instead, which is the generic probe
			// refusal rather than a role-specific claim.
			name: "native_image_that_is_not_go",
			ctx:  func(t *testing.T) context.Context { return t.Context() },
			path: func(t *testing.T) string { return requireLinuxExternalTool(t, "git") },
			want: "external tool probe failed, expired, or exceeded output bound",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := testCase.path(t)
			location, err := ObserveExecutionGoSDKLocation(testCase.ctx(t), path)
			assertExternalDelegationRefusal(t, err, path, binary)
			if location != (ExecutionGoSDKLocation{}) {
				t.Fatalf("refused observation returned %#v", location)
			}
			if err.Error() != testCase.want {
				t.Fatalf("refusal = %q, want %q", err.Error(), testCase.want)
			}
			assertExternalProbeParentEmpty(t, parent)
		})
	}
}
