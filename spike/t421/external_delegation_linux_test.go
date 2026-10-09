//go:build linux

package t421

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/executableidentity"
	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

// The shared delegation primitives are exercised directly rather than only
// through the two public recipes, because a refusal raised inside a census is
// indistinguishable from one raised by an image admission once it surfaces as a
// manifest error. Each test below pins one refusal class to its own message.

// assertExternalDelegationRefusal requires an error and proves no private
// absolute path survived into its message.
func assertExternalDelegationRefusal(t *testing.T, err error, private ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a delegation refusal")
	}
	for _, value := range private {
		if filepath.IsAbs(value) && strings.Contains(err.Error(), value) {
			t.Fatalf("delegation refusal leaked private input: %v", err)
		}
	}
}

// assertExternalDelegationPathFree proves an observation is structural: it
// carries no path separator at all, so neither a host location nor an entry name
// containing one can escape, and it repeats none of the forbidden values the
// caller names.
func assertExternalDelegationPathFree(t *testing.T, observation any, forbidden ...string) {
	t.Helper()
	encoded := fmt.Sprintf("%#v", observation)
	if strings.Contains(encoded, "/") {
		t.Fatalf("delegation observation carried a path separator: %s", encoded)
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(encoded, value) {
			t.Fatalf("delegation observation leaked %q: %s", value, encoded)
		}
	}
}

// writeExternalDelegationFile creates one fixture with an exact permission set.
// The mode is applied by an explicit Chmod rather than through WriteFile's
// argument, because the umask would otherwise make every permission string in a
// canonical digest host-dependent.
func writeExternalDelegationFile(t *testing.T, path string, content []byte, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeExternalDelegationDirectory creates one fixture directory with an exact
// permission set, for the same umask reason.
func writeExternalDelegationDirectory(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// externalDelegationELF builds a 128-byte stub whose header satisfies the
// 64-byte helper screen. It is a shape fixture only: it is never a valid
// linux/amd64 image, so admitExternalDelegationImage always refuses it and it
// never stands in for a real selected tool.
func externalDelegationELF(marker byte) []byte {
	content := make([]byte, 128)
	copy(content, "\x7fELF\x02\x01\x01\x00")
	content[16] = marker
	return content
}

// requireExternalDelegationProbeParent pins probe scratch inside a private
// resolved directory and requires that nothing survives the observation.
func requireExternalDelegationProbeParent(t *testing.T) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", parent)
	t.Cleanup(func() { assertExternalProbeParentEmpty(t, parent) })
	return parent
}

// requireExternalDelegationBase returns one fully resolved private directory that
// fixture paths are built under, so an unresolved base never masquerades as a
// refusal cause.
func requireExternalDelegationBase(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return base
}

// requireExternalDelegationFixtureBase returns one fully resolved private
// directory that is a sibling of an active probe parent rather than a child of
// it. requireExternalDelegationBase derives its root from TMPDIR, and
// requireExternalDelegationProbeParent repoints TMPDIR at the very directory the
// probe-leak assertion inspects, so a fixture built through it afterwards would
// be reported as retained probe workspace. Probe scratch is routed by TMPDIR
// rather than by withExecutionPreparationParent on purpose: an explicit
// preparation parent makes the probe retain its workspace for its owner, so the
// leak assertion only means something when the probe chose its own directory.
func requireExternalDelegationFixtureBase(t *testing.T, parent string) string {
	t.Helper()
	created, err := os.MkdirTemp(filepath.Dir(parent), "t421-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(created); err != nil {
			t.Fatal(err)
		}
	})
	base, err := filepath.EvalSymlinks(created)
	if err != nil {
		t.Fatal(err)
	}
	return base
}

// requireExternalDelegationCaseDirectory returns one fresh resolved directory
// under an existing fixture base, so table cases never share fixture state.
func requireExternalDelegationCaseDirectory(t *testing.T, base string) string {
	t.Helper()
	created, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(created)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// readHostImageBytes copies one admitted host image so a fixture can be mutated
// without touching the selected tool.
func readHostImageBytes(t *testing.T, binary string) []byte {
	t.Helper()
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// degradedELF32Image truncates a real image and clears its ELF class byte, so the
// native screen refuses it while every other header field stays real.
func degradedELF32Image(t *testing.T, binary string) []byte {
	t.Helper()
	content := readHostImageBytes(t, binary)
	if len(content) < 256 {
		t.Fatalf("host image is too short to degrade: %d", len(content))
	}
	degraded := make([]byte, 256)
	copy(degraded, content)
	degraded[4] = 1
	return degraded
}

// TestLinuxExternalDelegationImageAdmission pins the delegation recipes' own
// copy of the image admission sequence. It duplicates rather than shares the
// observer's hot path, so it must refuse the same shapes with its own messages.
func TestLinuxExternalDelegationImageAdmission(t *testing.T) {
	gitBinary := requireLinuxExternalTool(t, "git")
	base := requireExternalDelegationBase(t)
	missing := filepath.Join(base, "absent-core")
	script := writeExternalToolScript(t, "printf 'not-an-image\\n'\n")
	executableCopy := writeExternalDelegationFile(t, filepath.Join(base, "copied-core"),
		readHostImageBytes(t, gitBinary), 0o755)
	nonExecutableCopy := writeExternalDelegationFile(t, filepath.Join(base, "sealed-core"),
		readHostImageBytes(t, gitBinary), 0o600)
	elf32Copy := writeExternalDelegationFile(t, filepath.Join(base, "elf32-core"),
		degradedELF32Image(t, gitBinary), 0o755)
	link := filepath.Join(base, "linked-core")
	if err := os.Symlink(gitBinary, link); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, binary, message string
	}{
		{"empty", "", "external delegation image requires an explicit absolute path"},
		{"relative", "git", "external delegation image requires an explicit absolute path"},
		{"unclean", "/usr/bin//git", "external delegation image requires an explicit absolute path"},
		{"padded", gitBinary + " ", "external delegation image requires an explicit absolute path"},
		{"newline", gitBinary + "\n", "external delegation image requires an explicit absolute path"},
		{"nul", gitBinary + "\x00", "external delegation image requires an explicit absolute path"},
		{"missing", missing, "external delegation image cannot be resolved"},
		{"directory", base, "external delegation image is not a bounded native executable"},
		{"script", script, "external delegation image is not a bounded native executable"},
		{"non-executable", nonExecutableCopy, "external delegation image is not a bounded native executable"},
		{"elf32", elf32Copy, "external delegation image is not a bounded native executable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, digest, err := admitExternalDelegationImage(t.Context(), test.binary)
			if resolved != "" || digest != "" {
				t.Fatalf("unadmitted delegation image = %q, %q", resolved, digest)
			}
			assertExternalDelegationRefusal(t, err, test.binary)
			if err.Error() != test.message {
				t.Fatalf("delegation image refusal = %q, want %q", err, test.message)
			}
		})
	}
	for _, test := range []struct{ name, binary, want string }{
		{"symlink resolves to the admitted image", link, gitBinary},
		{"direct image is admitted unchanged", executableCopy, executableCopy},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, digest, err := admitExternalDelegationImage(t.Context(), test.binary)
			if err != nil {
				t.Fatal(err)
			}
			want, err := executableidentity.Digest(test.want)
			if err != nil {
				t.Fatal(err)
			}
			if resolved != test.want || digest != want {
				t.Fatalf("delegation image = %q, %q, want %q, %q", resolved, digest, test.want, want)
			}
		})
	}
}

// TestLinuxExternalDelegationDirectoryAdmission pins the location admission both
// recipes share. validPublicToolVersion deliberately does not apply here: probe
// output is a path, not a version string.
func TestLinuxExternalDelegationDirectoryAdmission(t *testing.T) {
	base := requireExternalDelegationBase(t)
	directory := writeExternalDelegationDirectory(t, filepath.Join(base, "reported-core"), 0o755)
	regular := writeExternalDelegationFile(t, filepath.Join(base, "regular"), []byte("x\n"), 0o644)
	linked := filepath.Join(base, "linked-directory")
	if err := os.Symlink(directory, linked); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, output, message string
	}{
		{"empty", "", "external delegation directory is invalid"},
		{"relative", "git-core", "external delegation directory is invalid"},
		{"unclean", directory + "/.", "external delegation directory is invalid"},
		{"padded", directory + " ", "external delegation directory is invalid"},
		{"newline", directory + "\n", "external delegation directory is invalid"},
		{"nul", directory + "\x00", "external delegation directory is invalid"},
		{"unbounded", "/" + strings.Repeat("a", maxInputCustodyPathBytes), "external delegation directory is invalid"},
		{"missing", filepath.Join(base, "absent-core"), "external delegation directory is not fully resolved"},
		{"symlinked", linked, "external delegation directory is not fully resolved"},
		{"regular file", regular, "external delegation directory is not a real directory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			admitted, err := admitExternalDelegationDirectory(test.output)
			if admitted != "" {
				t.Fatalf("unadmitted delegation directory = %q", admitted)
			}
			assertExternalDelegationRefusal(t, err, test.output)
			if err.Error() != test.message {
				t.Fatalf("delegation directory refusal = %q, want %q", err, test.message)
			}
		})
	}
	if admitted, err := admitExternalDelegationDirectory(directory); err != nil || admitted != directory {
		t.Fatalf("delegation directory = %q, %v, want %q", admitted, err, directory)
	}
}

// TestLinuxExternalDelegationCensusSortsAndEncodesRows pins two properties every
// digest depends on: rows reach the canonical encoding in sorted name order even
// though the kernel returns directory order, and the length-prefixed encoding is
// byte-exact. The fixture is written in reverse order on purpose.
func TestLinuxExternalDelegationCensusSortsAndEncodesRows(t *testing.T) {
	base := requireExternalDelegationBase(t)
	directory := writeExternalDelegationDirectory(t, filepath.Join(base, "census"), 0o755)
	writeExternalDelegationFile(t, filepath.Join(directory, "zeta"), []byte("# sourced\n"), 0o644)
	writeExternalDelegationFile(t, filepath.Join(directory, "yankee"), externalDelegationELF(1), 0o755)
	if err := os.Symlink("zeta", filepath.Join(directory, "mid-link")); err != nil {
		t.Fatal(err)
	}
	writeExternalDelegationDirectory(t, filepath.Join(directory, "alpha"), 0o755)
	entries, err := censusExternalDelegationRoot(t.Context(), directory, maxGitExecPathEntries)
	if err != nil {
		t.Fatal(err)
	}
	// A directory row records mode.Perm().String(), which masks off ModeDir, so
	// the leading character is '-' and not 'd'.
	want := "directory 5:alpha;0:;1:0;10:-rwxr-xr-x;0:;\n" +
		"symlink 8:mid-link;0:;1:0;0:;4:zeta;\n" +
		"regular 6:yankee;6:native;3:128;10:-rwxr-xr-x;0:;\n" +
		"regular 4:zeta;4:text;2:10;10:-rw-r--r--;0:;\n"
	if got := externalDelegationCanonical(entries); got != want {
		t.Fatalf("canonical encoding = %q, want %q", got, want)
	}
	if len(entries) != 4 {
		t.Fatalf("census = %#v", entries)
	}
	for index, name := range []string{"alpha", "mid-link", "yankee", "zeta"} {
		if entries[index].name != name {
			t.Fatalf("census order = %#v, want %q at %d", entries, name, index)
		}
	}
	if entries[3].executable || !entries[2].executable {
		t.Fatalf("executable bit misclassified: %#v, %#v", entries[2], entries[3])
	}
	named, err := externalDelegationNamed(entries)
	if err != nil || len(named) != 4 {
		t.Fatalf("named index = %d, %v", len(named), err)
	}
	if _, err := externalDelegationNamed(append(entries, entries[0])); err == nil ||
		err.Error() != "external delegation entry is duplicated" {
		t.Fatalf("duplicate refusal = %v", err)
	}
	if got := externalDelegationSubdirectoryHeader("mergetools"); got != "subdirectory 10:mergetools;\n" {
		t.Fatalf("subdirectory header = %q", got)
	}
}

// TestLinuxExternalDelegationCensusRefusesUnadmittedRows pins the closed
// structural vocabulary: a setuid helper, a FIFO, a socket and a zero-byte
// regular file each refuse, and a directory over its entry bound refuses instead
// of truncating.
func TestLinuxExternalDelegationCensusRefusesUnadmittedRows(t *testing.T) {
	base := requireExternalDelegationBase(t)
	setuidDirectory := writeExternalDelegationDirectory(t, filepath.Join(base, "setuid"), 0o755)
	setuid := writeExternalDelegationFile(t, filepath.Join(setuidDirectory, "setuid-helper"),
		externalDelegationELF(1), 0o755)
	// os.Chmod takes a FileMode, not an octal literal: syscallMode maps only the
	// ModeSetuid/ModeSetgid/ModeSticky bits and Perm() masks to 0777, so a
	// hand-written 0o4755 would silently drop the setuid bit.
	if err := os.Chmod(setuid, os.ModeSetuid|0o755); err != nil {
		t.Fatal(err)
	}
	setgidDirectory := writeExternalDelegationDirectory(t, filepath.Join(base, "setgid"), 0o755)
	setgid := writeExternalDelegationFile(t, filepath.Join(setgidDirectory, "setgid-helper"),
		externalDelegationELF(2), 0o755)
	if err := os.Chmod(setgid, os.ModeSetgid|0o755); err != nil {
		t.Fatal(err)
	}
	fifoDirectory := writeExternalDelegationDirectory(t, filepath.Join(base, "fifo"), 0o755)
	if err := unix.Mkfifo(filepath.Join(fifoDirectory, "fifo-helper"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A socket is created rather than mknod-ed, because mknod for a non-regular,
	// non-FIFO node needs CAP_MKNOD. The listener stays open for the whole test:
	// closing it would unlink the fixture being censused. Its directory is a short
	// system temporary path because a unix socket address is bounded to 108 bytes.
	socketDirectory, err := os.MkdirTemp("", "t421sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	listener, err := net.Listen("unix", filepath.Join(socketDirectory, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	emptyDirectory := writeExternalDelegationDirectory(t, filepath.Join(base, "empty"), 0o755)
	writeExternalDelegationFile(t, filepath.Join(emptyDirectory, "empty-helper"), nil, 0o755)
	overDirectory := writeExternalDelegationDirectory(t, filepath.Join(base, "over"), 0o755)
	for _, name := range []string{"aaa", "bbb", "ccc"} {
		writeExternalDelegationFile(t, filepath.Join(overDirectory, name), []byte("# sourced\n"), 0o644)
	}
	for _, test := range []struct {
		name, directory, message string
		limit                    int
	}{
		{"setuid helper", setuidDirectory, "external delegation entry is not an admitted file class", maxGitExecPathEntries},
		{"setgid helper", setgidDirectory, "external delegation entry is not an admitted file class", maxGitExecPathEntries},
		{"fifo", fifoDirectory, "external delegation entry is not an admitted file class", maxGitExecPathEntries},
		{"socket", socketDirectory, "external delegation entry is not an admitted file class", maxGitExecPathEntries},
		{"empty helper", emptyDirectory, "external delegation helper is empty or exceeds its byte bound", maxGitExecPathEntries},
		{"entry bound", overDirectory, "external delegation directory exceeds its entry bound", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries, err := censusExternalDelegationRoot(t.Context(), test.directory, test.limit)
			if entries != nil {
				t.Fatalf("census returned %#v", entries)
			}
			assertExternalDelegationRefusal(t, err, test.directory)
			if err.Error() != test.message {
				t.Fatalf("census refusal = %q, want %q", err, test.message)
			}
		})
	}
	t.Run("non-positive bound", func(t *testing.T) {
		entries, err := censusExternalDelegationRoot(t.Context(), overDirectory, 0)
		if entries != nil || err == nil || err.Error() != "external delegation census is unavailable" {
			t.Fatalf("census = %#v, %v", entries, err)
		}
	})
	t.Run("missing directory", func(t *testing.T) {
		_, err := censusExternalDelegationRoot(t.Context(), filepath.Join(base, "absent"), 8)
		assertExternalDelegationRefusal(t, err)
		if err.Error() != "external delegation directory cannot be opened as a census root" {
			t.Fatalf("missing-directory refusal = %q", err)
		}
	})
	t.Run("expired context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := censusExternalDelegationRoot(ctx, overDirectory, 8); err == nil ||
			err.Error() != "external delegation census is unavailable" {
			t.Fatalf("expired-context refusal = %v", err)
		}
	})
}

// TestLinuxExternalDelegationProbeRefusesBeforeLaunching proves the probe
// re-hashes the selected image on both sides and leaves no surviving scratch on
// either refusal path.
func TestLinuxExternalDelegationProbeRefusesBeforeLaunching(t *testing.T) {
	gitBinary := requireLinuxExternalTool(t, "git")
	probeParent := requireExternalDelegationProbeParent(t)
	unrelated := "sha256:" + strings.Repeat("0", 64)
	t.Run("substituted digest", func(t *testing.T) {
		output, err := runExternalDelegationProbe(t.Context(), gitBinary, unrelated, "--version")
		if output != "" {
			t.Fatalf("probe output = %q", output)
		}
		assertExternalDelegationRefusal(t, err, gitBinary)
		if err.Error() != "external delegation image changed before its probe" {
			t.Fatalf("probe refusal = %q", err)
		}
		assertExternalProbeParentEmpty(t, probeParent)
	})
	t.Run("expired context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		output, err := runExternalDelegationProbe(ctx, gitBinary, unrelated, "--version")
		if output != "" || err == nil || err.Error() != "external delegation probe canceled" {
			t.Fatalf("probe = %q, %v", output, err)
		}
		assertExternalProbeParentEmpty(t, probeParent)
	})
	t.Run("admitted image reports its own version", func(t *testing.T) {
		digest, err := t4013.DigestHostExecutable(t.Context(), gitBinary)
		if err != nil {
			t.Fatal(err)
		}
		output, err := runExternalDelegationProbe(t.Context(), gitBinary, digest, "--version")
		if err != nil {
			t.Fatal(err)
		}
		if want := linuxHostToolVersion(t, gitBinary, "--version"); output != want {
			t.Fatalf("probe output = %q, want %q", output, want)
		}
		assertExternalProbeParentEmpty(t, probeParent)
	})
}
