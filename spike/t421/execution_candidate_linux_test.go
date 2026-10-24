//go:build linux

package t421

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func linuxExecutorCandidateFixture(t *testing.T, sealed, readonly, executable bool, size int64) *os.File {
	t.Helper()
	fd, err := unix.MemfdCreate("phebs-sealed-input", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if err != nil {
		t.Fatal(err)
	}
	writer := os.NewFile(uintptr(fd), "candidate-writer")
	t.Cleanup(func() { _ = writer.Close() })
	bytes := make([]byte, 120)
	if executable {
		copy(bytes, "\x7fELF\x02\x01\x01")
		binary.LittleEndian.PutUint16(bytes[16:], 2)
		binary.LittleEndian.PutUint16(bytes[18:], 62)
		binary.LittleEndian.PutUint32(bytes[20:], 1)
		binary.LittleEndian.PutUint64(bytes[32:], 64)
		binary.LittleEndian.PutUint16(bytes[52:], 64)
		binary.LittleEndian.PutUint16(bytes[54:], 56)
		binary.LittleEndian.PutUint16(bytes[56:], 1)
	}
	if _, err := writer.Write(bytes); err != nil || unix.Ftruncate(fd, size) != nil || unix.Fchmod(fd, 0o500) != nil {
		t.Fatal("candidate construction", err)
	}
	if sealed {
		if _, err := unix.FcntlInt(writer.Fd(), unix.F_ADD_SEALS, linuxInputSeals); err != nil {
			t.Fatal(err)
		}
	}
	if !readonly {
		return writer
	}
	reader, err := openLinuxInputReadOnly(writer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func TestLinuxExecutorCandidateSameInodeNoPrematureIdentity(t *testing.T) {
	file := linuxExecutorCandidateFixture(t, true, true, true, 120)
	if _, err := file.Seek(17, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	before := linuxInputFDCount(t)
	tool, err := protectLinuxExecutorCandidateFD(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tool.Close() })
	if tool.identity != (ExecutionToolIdentity{}) || tool.referenceInputs != nil || linuxInputFDCount(t) != before+1 {
		t.Fatal("byte adoption issued reference identity or leaked descriptors")
	}
	entry := tool.input.inputs["t422-execute"]
	left, leftErr := file.Stat()
	right, rightErr := entry.file.Stat()
	position, seekErr := file.Seek(0, io.SeekCurrent)
	if leftErr != nil || rightErr != nil || !os.SameFile(left, right) || seekErr != nil || position != 17 || !validExecutionSHA256(entry.identity.SHA256) {
		t.Fatal("candidate was copied, source offset changed or bytes unmeasured")
	}
	if tool.Close() != nil || linuxInputFDCount(t) != before {
		t.Fatal("candidate keeper not released")
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("candidate Close closed the original borrowed image", err)
	}
	// The old public path route continues refusing deleted memfd aliases.
	if candidate, err := protectLinuxExecutionTool(t.Context(), "t422-execute", filepath.Join("/proc/self/fd", strconv.FormatUint(uint64(file.Fd()), 10))); err == nil || candidate != nil {
		t.Fatal("public canonical path boundary was relaxed")
	}
}

func TestLinuxExecutorCandidateRefusals(t *testing.T) {
	if tool, err := protectLinuxExecutorCandidateFD(t.Context(), nil); err == nil || tool != nil {
		t.Fatal("nil image accepted")
	}
	for _, test := range []struct {
		name                         string
		sealed, readonly, executable bool
		size                         int64
	}{
		{"unsealed", false, true, true, 120},
		{"writable", true, false, true, 120},
		{"non-elf", true, true, false, 120},
		{"oversize", true, true, true, maxInputCustodyFileBytes + 1},
		{"short", true, true, true, 63},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := linuxExecutorCandidateFixture(t, test.sealed, test.readonly, test.executable, test.size)
			before := linuxInputFDCount(t)
			if tool, err := protectLinuxExecutorCandidateFD(t.Context(), file); err == nil || tool != nil || linuxInputFDCount(t) != before {
				t.Fatal("invalid candidate accepted or duplicate retained")
			}
		})
	}
	metadata := linuxExecutorCandidateFixture(t, false, true, true, 120)
	if unix.Fchmod(int(metadata.Fd()), 0o400) != nil {
		t.Fatal("metadata fixture chmod")
	}
	if tool, err := protectLinuxExecutorCandidateFD(t.Context(), metadata); err == nil || tool != nil {
		t.Fatal("non-executable metadata accepted")
	}
	file := linuxExecutorCandidateFixture(t, true, true, true, 120)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if tool, err := protectLinuxExecutorCandidateFD(ctx, file); err == nil || tool != nil {
			t.Fatal("invalid context accepted")
		}
	}
	if file.Close() != nil {
		t.Fatal("fixture close")
	}
	if tool, err := protectLinuxExecutorCandidateFD(t.Context(), file); err == nil || tool != nil {
		t.Fatal("closed image accepted")
	}
}
