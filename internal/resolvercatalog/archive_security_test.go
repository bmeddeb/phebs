package resolvercatalog

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const boundedResolverSparseLogicalSize = int64(8 << 20)

func TestResolverArchiveHeaderRejectsSparseMetadataWithinOrdinaryBounds(t *testing.T) {
	name := "phebs-resolver-catalog-" + strings.Repeat("a", 64) + ".manifest.json"
	header := &tar.Header{
		Name: name, Mode: 0o600, Size: 1,
		ModTime:  time.Unix(0, 0).UTC(),
		Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		PAXRecords: map[string]string{
			"GNU.sparse.size":      "1",
			"GNU.sparse.numblocks": "1",
			"GNU.sparse.map":       "0,1",
		},
	}
	if err := validateResolverArchiveHeader(
		header, map[string]bool{}, 0, 1024,
	); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("sparse PAX header error = %v, want unsafe refusal", err)
	}
}

func TestCreateResolverArchiveDoesNotCreateMissingLiveRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "missing-resolver-catalogs")
	archivePath := filepath.Join(parent, "resolver-catalog.tar")
	report, err := CreateArchiveWithReport(root, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Publications != 0 || report.OmittedPublications != 0 ||
		report.OmittedArtifacts != 0 || report.StaleMarkers != 0 ||
		len(report.Details) != 0 || report.TruncatedDetails != 0 {
		t.Fatalf("empty archive report = %+v", report)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("backup created missing live root: %v", err)
	}
	assertEmptyTar(t, archivePath)
}

func TestCreateResolverArchiveRejectsSpecialLiveRoot(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "resolver-catalogs")
	if err := os.Symlink(realRoot, root); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(parent, "resolver-catalog.tar")
	if _, err := CreateArchiveWithReport(root, archivePath); err == nil {
		t.Fatal("CreateArchiveWithReport accepted a symlink live root")
	}
	if _, err := os.Lstat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("special-root refusal left archive output: %v", err)
	}
}

func TestRestoreResolverArchiveRejectsSparseHeadersBeforeCreatingOutput(t *testing.T) {
	name := "phebs-resolver-catalog-sparse-member"
	tests := []struct {
		name    string
		archive []byte
	}{
		{"pax sparse", resolverPAXSparseArchive(name)},
		{"old gnu sparse", resolverOldGNUSparseArchive(name)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "resolver-catalog.tar")
			if err := os.WriteFile(archivePath, test.archive, 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "resolver-catalogs")
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

func TestResolverArchiveCreationBoundsLogicalEntriesAndPhysicalBytes(t *testing.T) {
	entries := maxArchiveEntries - 1
	logical := maxArchiveBytes - 1
	if err := reserveResolverArchiveEntry(&entries, &logical, 1); err != nil {
		t.Fatalf("reserve at cap: %v", err)
	}
	if entries != maxArchiveEntries || logical != maxArchiveBytes {
		t.Fatalf("reserved totals = %d, %d", entries, logical)
	}
	if err := reserveResolverArchiveEntry(&entries, &logical, 0); !errors.Is(err, ErrLimit) {
		t.Fatalf("entry cap+1 = %v, want ErrLimit", err)
	}
	entries = 0
	logical = maxArchiveBytes
	if err := reserveResolverArchiveEntry(&entries, &logical, 1); !errors.Is(err, ErrLimit) {
		t.Fatalf("logical cap+1 = %v, want ErrLimit", err)
	}

	var destination bytes.Buffer
	output := &boundedArchiveOutput{destination: &destination, remaining: 3}
	if written, err := output.Write([]byte("abc")); err != nil || written != 3 {
		t.Fatalf("physical write at cap = %d, %v", written, err)
	}
	if written, err := output.Write([]byte("x")); !errors.Is(err, ErrLimit) || written != 0 {
		t.Fatalf("physical write cap+1 = %d, %v, want ErrLimit", written, err)
	}
}

func TestRestoreResolverArchiveRejectsSpecialOrOversizedSource(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		parent := t.TempDir()
		realArchive := filepath.Join(parent, "real.tar")
		if err := os.WriteFile(realArchive, make([]byte, 1024), 0o600); err != nil {
			t.Fatal(err)
		}
		archivePath := filepath.Join(parent, "resolver-catalog.tar")
		if err := os.Symlink(realArchive, archivePath); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(parent, "resolver-catalogs")
		if err := RestoreArchive(archivePath, target); err == nil {
			t.Fatal("RestoreArchive accepted a symlink archive")
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("special-source refusal created restore output: %v", err)
		}
	})
	t.Run("physical ceiling", func(t *testing.T) {
		parent := t.TempDir()
		archivePath := filepath.Join(parent, "resolver-catalog.tar")
		file, err := os.OpenFile(
			archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
		)
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
		target := filepath.Join(parent, "resolver-catalogs")
		if err := RestoreArchive(archivePath, target); err == nil {
			t.Fatal("RestoreArchive accepted an archive above its physical ceiling")
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("oversize refusal created restore output: %v", err)
		}
	})
}

func resolverPAXSparseArchive(name string) []byte {
	records := resolverPAXRecord(
		"GNU.sparse.size", fmt.Sprint(boundedResolverSparseLogicalSize),
	) + resolverPAXRecord("GNU.sparse.numblocks", "1") +
		resolverPAXRecord("GNU.sparse.map", "0,1")
	result := resolverRawTarHeader(
		"PaxHeaders.0/resolver", tar.TypeXHeader, int64(len(records)), false,
	)
	result = resolverAppendPadded(result, []byte(records))
	result = append(result, resolverRawTarHeader(name, tar.TypeReg, 1, false)...)
	result = resolverAppendPadded(result, []byte("x"))
	return append(result, make([]byte, 1024)...)
}

func resolverOldGNUSparseArchive(name string) []byte {
	header := resolverRawTarHeader(name, tar.TypeGNUSparse, 1, true)
	resolverPutTarOctal(header[386:398], 0)
	resolverPutTarOctal(header[398:410], 1)
	resolverPutTarBase256(header[483:495], boundedResolverSparseLogicalSize)
	resolverSetTarChecksum(header)
	result := append([]byte{}, header...)
	result = resolverAppendPadded(result, []byte("x"))
	return append(result, make([]byte, 1024)...)
}

func resolverPAXRecord(key, value string) string {
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

func resolverRawTarHeader(name string, typeflag byte, size int64, gnu bool) []byte {
	header := make([]byte, 512)
	copy(header[0:100], name)
	resolverPutTarOctal(header[100:108], 0o600)
	resolverPutTarOctal(header[108:116], 0)
	resolverPutTarOctal(header[116:124], 0)
	resolverPutTarOctal(header[124:136], size)
	resolverPutTarOctal(header[136:148], 0)
	header[156] = typeflag
	if gnu {
		copy(header[257:263], "ustar ")
		copy(header[263:265], " \x00")
	} else {
		copy(header[257:263], "ustar\x00")
		copy(header[263:265], "00")
	}
	resolverSetTarChecksum(header)
	return header
}

func resolverPutTarOctal(field []byte, value int64) {
	for index := range field {
		field[index] = 0
	}
	encoded := fmt.Sprintf("%0*o", len(field)-1, value)
	copy(field, encoded)
}

func resolverPutTarBase256(field []byte, value int64) {
	for index := len(field) - 1; index >= 0; index-- {
		field[index] = byte(value)
		value >>= 8
	}
	field[0] |= 0x80
}

func resolverSetTarChecksum(header []byte) {
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

func resolverAppendPadded(destination, data []byte) []byte {
	destination = append(destination, data...)
	padding := -len(data) & 511
	return append(destination, make([]byte, padding)...)
}
