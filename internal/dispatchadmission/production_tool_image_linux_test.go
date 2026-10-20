//go:build linux

package dispatchadmission

import (
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/executableidentity"
	"golang.org/x/sys/unix"
)

func selectedLinuxImageFixture(t *testing.T, mode uint32, sealed, elf bool) *os.File {
	t.Helper()
	fd, err := unix.MemfdCreate("selected-image-mechanical-test", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if err != nil {
		t.Fatal(err)
	}
	writer := os.NewFile(uintptr(fd), "selected-image-writer")
	t.Cleanup(func() { _ = writer.Close() })
	raw := make([]byte, 64)
	if elf {
		copy(raw, "\x7fELF")
		raw[4], raw[5], raw[6] = 2, 1, 1
		binary.LittleEndian.PutUint16(raw[18:20], 62)
	}
	if n, err := writer.Write(raw); err != nil || n != len(raw) || unix.Fchmod(fd, mode) != nil {
		t.Fatal("fixed mechanical image bytes/mode", err)
	}
	if sealed {
		if _, err := unix.FcntlInt(writer.Fd(), unix.F_ADD_SEALS, selectedLinuxToolSeals); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := os.Open("/proc/self/fd/" + strconv.Itoa(fd))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func TestSelectedLinuxImageKernelAndCanonicalRoute(t *testing.T) {
	image := selectedLinuxImageFixture(t, 0o500, true, true)
	path := "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strconv.FormatUint(uint64(image.Fd()), 10)
	// The private mechanical helper uses this test's own PID. Production always
	// supplies actual Getppid, covered by the full authenticated c.c child gate.
	opened, err := openLinuxSelectedToolImage(path, os.Getpid())
	if err != nil {
		t.Fatal("sealed native route", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	digest, err := executableidentity.DigestFile(image)
	if err != nil || executableidentity.VerifyFile(image, digest) != nil || executableidentity.Verify(path, digest) == nil {
		t.Fatal("strict path verifier changed or descriptor digest lost", err)
	}
	for _, value := range []string{
		strings.Replace(path, "/fd/", "/fd/0", 1), path + "/", path + "/image", "/proc/self/fd/" + strconv.FormatUint(uint64(image.Fd()), 10),
		"/proc/" + strconv.Itoa(os.Getpid()+1) + "/fd/3", "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/2",
	} {
		if file, err := openLinuxSelectedToolImage(value, os.Getpid()); err == nil || file != nil {
			t.Fatal("noncanonical/wrong-owner route admitted", value)
		}
	}
	if file, selected, err := OpenSelectedProductionToolImage("surreal", path); file != nil || selected || err != nil {
		t.Fatal("numeric path alone acquired selected authority", selected, err)
	}
}

func TestSelectedLinuxImageRefusesProtectionAndFormatDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mode   uint32
		sealed bool
		elf    bool
	}{
		{"unsealed", 0o500, false, true},
		{"non-executable", 0o400, true, true},
		{"not-ELF", 0o500, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := selectedLinuxImageFixture(t, test.mode, test.sealed, test.elf)
			if verifyLinuxSelectedToolImage(image) == nil {
				t.Fatal("unprotected or wrong-format image accepted")
			}
		})
	}
}

func TestSelectedLinuxImageRequiresLiveExactProductionBinding(t *testing.T) {
	image := selectedLinuxImageFixture(t, 0o500, true, true)
	path := "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strconv.FormatUint(uint64(image.Fd()), 10)
	controller, client, server := paired(t, testConfig())
	for _, test := range []struct {
		name, program, semantic, role, selectedPath string
		input                                       [32]byte
		selected                                    bool
	}{
		{"wrong-program", ProgramCorpusAuthor, ProductionSemanticV3, "surreal", path, [32]byte{1}, false},
		{"wrong-semantic", ProgramPhebs, "", "surreal", path, [32]byte{1}, false},
		{"wrong-role", ProgramPhebs, ProductionSemanticV3, "git", path, [32]byte{1}, false},
		{"unselected-route", ProgramPhebs, ProductionSemanticV3, "surreal", path + "-other", [32]byte{1}, false},
		{"missing-input", ProgramPhebs, ProductionSemanticV3, "surreal", path, [32]byte{}, true},
		{"self-is-not-parent", ProgramPhebs, ProductionSemanticV3, "surreal", path, [32]byte{1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			setPipedTestRuntime(t, &ProductionLifetime{program: test.program, semanticMode: test.semantic, inputSHA256: test.input, client: client,
				tools: map[string]ProductionToolBinding{test.role: {Role: test.role, Path: test.selectedPath}}})
			file, selected, err := OpenSelectedProductionToolImage(test.role, path)
			if file != nil || selected != test.selected || test.selected && err == nil || !test.selected && err != nil {
				t.Fatal("unissued/foreign route escaped its selected refusal", selected, err)
			}
		})
	}
	finishPair(t, controller, client, server)
}
