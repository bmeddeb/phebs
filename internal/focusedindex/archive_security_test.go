package focusedindex

import (
	"archive/tar"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const hugeSparseLogicalSize = int64(8 << 30)

func TestArchiveHeaderRejectsSparseMetadataWithinOrdinaryBounds(t *testing.T) {
	name := "phebs-focus-" + strings.Repeat("a", 64) + ".manifest.json"
	header := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     1,
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
		PAXRecords: map[string]string{
			"GNU.sparse.size":      "1",
			"GNU.sparse.numblocks": "1",
			"GNU.sparse.map":       "0,1",
		},
	}
	if err := validateArchiveHeader(
		header, map[string]bool{}, 0, 1024,
	); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("sparse PAX header error = %v, want unsafe refusal", err)
	}
}

func TestArchiveHeaderRejectsOversizedPAXName(t *testing.T) {
	name := "phebs-focus-" + strings.Repeat("a", maxArchiveNameBytes)
	header := &tar.Header{
		Name:     name,
		Mode:     0o600,
		Size:     1,
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
		PAXRecords: map[string]string{
			"path": name,
		},
	}
	if err := validateArchiveHeader(
		header, map[string]bool{}, 0, 1024,
	); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("oversized PAX path error = %v, want unsafe refusal", err)
	}
}

func TestRestoreRejectsSparseHeadersBeforeCreatingOutput(t *testing.T) {
	name := "phebs-focus-" + strings.Repeat("a", 64) + ".manifest.json"
	tests := []struct {
		name    string
		archive []byte
	}{
		{"pax sparse", paxSparseArchive(name)},
		{"old gnu sparse", oldGNUSparseArchive(name)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "focused-index.tar")
			if err := os.WriteFile(archivePath, test.archive, 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "index")
			err := RestoreArchive(archivePath, target)
			if err == nil || !strings.Contains(err.Error(), "unsafe") {
				t.Fatalf("RestoreArchive sparse error = %v, want unsafe refusal", err)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("sparse refusal created restore output: %v", err)
			}
		})
	}
}

func TestRestoreRejectsArchiveAbovePhysicalCeilingBeforeCreatingOutput(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "focused-index.tar")
	file, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxArchiveBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "index")
	if err := RestoreArchive(archivePath, target); err == nil {
		t.Fatal("RestoreArchive accepted an archive above its physical ceiling")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("oversize refusal created restore output: %v", err)
	}
}

func paxSparseArchive(name string) []byte {
	records := paxRecord("GNU.sparse.size", fmt.Sprint(hugeSparseLogicalSize)) +
		paxRecord("GNU.sparse.numblocks", "1") +
		paxRecord("GNU.sparse.map", "0,1")
	result := rawTarHeader("PaxHeaders.0/focused", tar.TypeXHeader, int64(len(records)), false)
	result = appendPadded(result, []byte(records))
	result = append(result, rawTarHeader(name, tar.TypeReg, 1, false)...)
	result = appendPadded(result, []byte("x"))
	return append(result, make([]byte, 1024)...)
}

func oldGNUSparseArchive(name string) []byte {
	header := rawTarHeader(name, tar.TypeGNUSparse, 1, true)
	putTarOctal(header[386:398], 0)
	putTarOctal(header[398:410], 1)
	putTarBase256(header[483:495], hugeSparseLogicalSize)
	setTarChecksum(header)
	result := append([]byte{}, header...)
	result = appendPadded(result, []byte("x"))
	return append(result, make([]byte, 1024)...)
}

func paxRecord(key, value string) string {
	body := " " + key + "=" + value + "\n"
	size := len(body) + 1
	for {
		record := fmt.Sprintf("%d%s", size, body)
		if len(record) == size {
			return record
		}
		size = len(record)
	}
}

func rawTarHeader(name string, typeflag byte, size int64, gnu bool) []byte {
	header := make([]byte, 512)
	copy(header[0:100], name)
	putTarOctal(header[100:108], 0o600)
	putTarOctal(header[108:116], 0)
	putTarOctal(header[116:124], 0)
	putTarOctal(header[124:136], size)
	putTarOctal(header[136:148], 0)
	header[156] = typeflag
	if gnu {
		copy(header[257:263], "ustar ")
		copy(header[263:265], " \x00")
	} else {
		copy(header[257:263], "ustar\x00")
		copy(header[263:265], "00")
	}
	setTarChecksum(header)
	return header
}

func putTarOctal(field []byte, value int64) {
	for index := range field {
		field[index] = 0
	}
	encoded := fmt.Sprintf("%0*o", len(field)-1, value)
	copy(field, encoded)
}

func putTarBase256(field []byte, value int64) {
	for index := len(field) - 1; index >= 0; index-- {
		field[index] = byte(value)
		value >>= 8
	}
	field[0] |= 0x80
}

func setTarChecksum(header []byte) {
	for index := 148; index < 156; index++ {
		header[index] = ' '
	}
	var sum int
	for _, value := range header {
		sum += int(value)
	}
	copy(header[148:154], fmt.Sprintf("%06o", sum))
	header[154] = 0
	header[155] = ' '
}

func appendPadded(destination, data []byte) []byte {
	destination = append(destination, data...)
	padding := -len(data) & 511
	return append(destination, make([]byte, padding)...)
}
