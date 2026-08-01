package callerpublication

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

func canonicalArchiveTestName() string {
	return callerpublicationid.RepositoryDirectory("github.com/acme/service") + "/" +
		callerpublicationid.ManifestName(
			"sha256:"+strings.Repeat("1", 64),
			"sha256:"+strings.Repeat("2", 64),
		)
}

func resetArchiveHooks(t *testing.T) {
	t.Helper()
	testAfterArchiveRepositoryMkdir = nil
	testAfterArchiveSourceAuthority = nil
	testAfterArchivePublicationCopy = nil
	testBeforeArchiveSourceCopy = nil
	testBeforeArchiveVerifyEntry = nil
	testBeforeArchiveStageRename = nil
	testAfterArchiveStageRename = nil
	t.Cleanup(func() {
		testAfterArchiveRepositoryMkdir = nil
		testAfterArchiveSourceAuthority = nil
		testAfterArchivePublicationCopy = nil
		testBeforeArchiveSourceCopy = nil
		testBeforeArchiveVerifyEntry = nil
		testBeforeArchiveStageRename = nil
		testAfterArchiveStageRename = nil
	})
}

func TestCallerArchiveHeaderRejectsUnsafePathsAndMetadata(t *testing.T) {
	canonical := canonicalArchiveTestName()
	tests := []struct {
		name   string
		mutate func(*tar.Header)
	}{
		{"parent path", func(header *tar.Header) { header.Name = "../" + canonical }},
		{"nested path", func(header *tar.Header) { header.Name += "/extra" }},
		{"foreign parent", func(header *tar.Header) { header.Name = "caller/" + filepath.Base(header.Name) }},
		{"directory", func(header *tar.Header) { header.Typeflag = tar.TypeDir }},
		{"symlink", func(header *tar.Header) { header.Typeflag, header.Linkname = tar.TypeSymlink, "target" }},
		{"hard link", func(header *tar.Header) { header.Typeflag, header.Linkname = tar.TypeLink, "target" }},
		{"device", func(header *tar.Header) { header.Typeflag = tar.TypeChar }},
		{"sparse pax", func(header *tar.Header) {
			header.PAXRecords = map[string]string{"GNU.sparse.size": "1"}
		}},
		{"unknown pax", func(header *tar.Header) {
			header.PAXRecords = map[string]string{"comment": "x"}
		}},
		{"mismatched pax path", func(header *tar.Header) {
			header.PAXRecords = map[string]string{"path": "different"}
		}},
		{"mismatched pax size", func(header *tar.Header) {
			header.PAXRecords = map[string]string{"size": "2"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := &tar.Header{
				Name: canonical, Mode: 0o600, Size: 1,
				ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg,
				Format: tar.FormatPAX,
			}
			test.mutate(header)
			if err := validateArchiveHeader(
				header, map[string]bool{}, 0, 1024,
			); err == nil || !strings.Contains(err.Error(), "unsafe") {
				t.Fatalf("validateArchiveHeader = %v, want unsafe refusal", err)
			}
		})
	}
	header := &tar.Header{
		Name: canonical, Mode: 0o600, Size: 1,
		ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg,
		Format: tar.FormatPAX,
	}
	if err := validateArchiveHeader(
		header, map[string]bool{canonical: true}, 0, 1024,
	); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("duplicate header error = %v, want unsafe refusal", err)
	}
}

func TestCreateCallerArchiveDoesNotCreateMissingLiveRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "missing-caller-leaves")
	archivePath := filepath.Join(parent, "caller-publication.tar")
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
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := tar.NewReader(file).Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty archive first entry = %v", err)
	}
}

func TestCreateCallerArchiveRejectsSpecialLiveRoot(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "caller-leaves")
	if err := os.Symlink(realRoot, root); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(parent, "caller-publication.tar")
	if _, err := CreateArchiveWithReport(root, archivePath); err == nil {
		t.Fatal("CreateArchiveWithReport accepted a symlink live root")
	}
	if _, err := os.Lstat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("special-root refusal left archive output: %v", err)
	}
}

func TestCreateCallerArchivePinsAdmittedRepositoryDirectory(t *testing.T) {
	resetArchiveHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	manifest := installArchivePublication(
		t, root, "github.com/acme/archive-source-swap",
		func(context.Context, State) error { return nil },
	)
	baseline := filepath.Join(t.TempDir(), "baseline.tar")
	if _, err := CreateArchiveWithReport(root, baseline); err != nil {
		t.Fatal(err)
	}

	repositoryPath := filepath.Join(
		root, callerpublicationid.RepositoryDirectory(manifest.Generation.Repository),
	)
	movedPath := repositoryPath + ".admitted"
	external := t.TempDir()
	for _, reference := range ArtifactRefs(manifest) {
		if err := os.WriteFile(
			filepath.Join(external, reference.Name),
			[]byte("operator secret that is not caller evidence\n"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	swapped := false
	restored := false
	testAfterArchiveSourceAuthority = func(path string) {
		if swapped {
			return
		}
		if path != repositoryPath {
			t.Fatalf("archive source hook path = %q, want %q", path, repositoryPath)
		}
		if err := os.Rename(repositoryPath, movedPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, repositoryPath); err != nil {
			t.Fatal(err)
		}
		swapped = true
	}
	testAfterArchivePublicationCopy = func(path string) {
		if !swapped || restored {
			return
		}
		if path != repositoryPath {
			t.Fatalf("archive copy hook path = %q, want %q", path, repositoryPath)
		}
		if err := os.Remove(repositoryPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(movedPath, repositoryPath); err != nil {
			t.Fatal(err)
		}
		restored = true
	}
	t.Cleanup(func() {
		if !restored {
			_ = os.Remove(repositoryPath)
			_ = os.Rename(movedPath, repositoryPath)
		}
	})

	observed := filepath.Join(t.TempDir(), "observed.tar")
	if _, err := CreateArchiveWithReport(root, observed); err != nil {
		t.Fatal(err)
	}
	if !swapped || !restored {
		t.Fatal("archive did not exercise the repository-directory swap")
	}
	want, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(observed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("repository path swap redirected caller archive source reads")
	}
}

func TestCreateCallerArchiveHonorsCancellationDuringCopy(t *testing.T) {
	resetArchiveHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	installArchivePublication(
		t, root, "github.com/acme/archive-cancel",
		func(context.Context, State) error { return nil },
	)
	ctx, cancel := context.WithCancel(t.Context())
	testBeforeArchiveSourceCopy = func(string) { cancel() }
	archivePath := filepath.Join(t.TempDir(), "caller-publication.tar")
	if _, err := CreateArchiveWithReportContext(
		ctx, root, archivePath,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateArchiveWithReportContext cancellation = %v", err)
	}
	if _, err := os.Lstat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled caller archive left output: %v", err)
	}
}

func TestVerifyCallerArchiveStreamsWithoutScratchTree(t *testing.T) {
	resetArchiveHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	for _, repository := range []string{
		"github.com/acme/archive-stream-a",
		"github.com/acme/archive-stream-b",
	} {
		installArchivePublication(
			t, root, repository,
			func(context.Context, State) error { return nil },
		)
	}
	archivePath := filepath.Join(t.TempDir(), "caller-publication.tar")
	if _, err := CreateArchiveWithReport(root, archivePath); err != nil {
		t.Fatal(err)
	}
	blockedTemp := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedTemp, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blockedTemp)
	report, err := VerifyArchiveWithReportContext(t.Context(), archivePath)
	if err != nil || report.Publications != 2 {
		t.Fatalf("streaming verify = %+v, %v", report, err)
	}
}

func TestVerifyAndRestoreCallerArchiveHonorCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	installArchivePublication(
		t, root, "github.com/acme/archive-cancel-verify",
		func(context.Context, State) error { return nil },
	)
	archivePath := filepath.Join(t.TempDir(), "caller-publication.tar")
	if _, err := CreateArchiveWithReport(root, archivePath); err != nil {
		t.Fatal(err)
	}

	t.Run("streaming preflight", func(t *testing.T) {
		resetArchiveHooks(t)
		ctx, cancel := context.WithCancel(t.Context())
		testBeforeArchiveVerifyEntry = func(string) { cancel() }
		if _, err := VerifyArchiveWithReportContext(
			ctx, archivePath,
		); !errors.Is(err, context.Canceled) {
			t.Fatalf("VerifyArchiveWithReportContext cancellation = %v", err)
		}
	})

	t.Run("staged extraction", func(t *testing.T) {
		resetArchiveHooks(t)
		ctx, cancel := context.WithCancel(t.Context())
		testAfterArchiveRepositoryMkdir = func(string) { cancel() }
		target := filepath.Join(t.TempDir(), "caller-leaves")
		if err := RestoreArchiveContext(
			ctx, archivePath, target,
		); !errors.Is(err, context.Canceled) {
			t.Fatalf("RestoreArchiveContext cancellation = %v", err)
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled restore published target: %v", err)
		}
	})
}

func TestCallerArchiveCreationBoundsLogicalEntriesAndPhysicalBytes(t *testing.T) {
	if MaxArchiveBytes != int64(4<<40) {
		t.Fatalf("archive byte cap = %d, want 4 TiB", MaxArchiveBytes)
	}

	entries := maxArchiveEntries - 1
	logical := MaxArchiveBytes - 1
	if err := reserveArchiveEntry(&entries, &logical, 1); err != nil {
		t.Fatalf("reserve at cap: %v", err)
	}
	if err := reserveArchiveEntry(&entries, &logical, 0); !errors.Is(err, callerleaf.ErrLimit) {
		t.Fatalf("entry cap+1 = %v, want ErrLimit", err)
	}
	entries = 0
	logical = MaxArchiveBytes
	if err := reserveArchiveEntry(&entries, &logical, 1); !errors.Is(err, callerleaf.ErrLimit) {
		t.Fatalf("logical cap+1 = %v, want ErrLimit", err)
	}

	var destination bytes.Buffer
	output := &boundedArchiveOutput{destination: &destination, remaining: 3}
	if written, err := output.Write([]byte("abc")); err != nil || written != 3 {
		t.Fatalf("physical write at cap = %d, %v", written, err)
	}
	if written, err := output.Write([]byte("x")); !errors.Is(err, callerleaf.ErrLimit) || written != 0 {
		t.Fatalf("physical cap+1 = %d, %v", written, err)
	}
}

func TestRestoreCallerArchiveRejectsSparseHeaderBeforeOutput(t *testing.T) {
	tests := []struct {
		name    string
		archive []byte
	}{
		{"pax sparse", callerPAXSparseArchive(canonicalArchiveTestName())},
		{"old gnu sparse", callerOldGNUSparseArchive(canonicalArchiveTestName())},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "caller-publication.tar")
			if err := os.WriteFile(archivePath, test.archive, 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "caller-leaves")
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

func TestRestoreCallerArchiveRejectsSpecialOversizedAndTrailingSources(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		parent := t.TempDir()
		realArchive := filepath.Join(parent, "real.tar")
		if err := os.WriteFile(realArchive, make([]byte, 1024), 0o600); err != nil {
			t.Fatal(err)
		}
		archivePath := filepath.Join(parent, "caller-publication.tar")
		if err := os.Symlink(realArchive, archivePath); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(parent, "caller-leaves")
		if err := RestoreArchive(archivePath, target); err == nil {
			t.Fatal("RestoreArchive accepted a symlink archive")
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("special-source refusal created restore output: %v", err)
		}
	})

	t.Run("physical ceiling", func(t *testing.T) {
		parent := t.TempDir()
		archivePath := filepath.Join(parent, "caller-publication.tar")
		file, err := os.OpenFile(
			archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(MaxArchiveBytes + 1); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(parent, "caller-leaves")
		if err := RestoreArchive(archivePath, target); err == nil {
			t.Fatal("RestoreArchive accepted an archive above its physical ceiling")
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("oversize refusal created restore output: %v", err)
		}
	})

	t.Run("trailing data", func(t *testing.T) {
		parent := t.TempDir()
		archivePath := filepath.Join(parent, "caller-publication.tar")
		if err := os.WriteFile(archivePath, append(make([]byte, 1024), 'x'), 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(parent, "caller-leaves")
		if err := RestoreArchive(archivePath, target); err == nil ||
			!strings.Contains(err.Error(), "trailing data") {
			t.Fatalf("RestoreArchive trailing error = %v", err)
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("trailing refusal created restore output: %v", err)
		}
	})
}

func TestRestoreCallerArchiveRejectsRepositoryDirectorySwapWithoutEscape(t *testing.T) {
	resetArchiveHooks(t)
	root := filepath.Join(t.TempDir(), "caller-leaves")
	installArchivePublication(
		t, root, "github.com/acme/restore-swap",
		func(context.Context, State) error { return nil },
	)
	archivePath := filepath.Join(t.TempDir(), "caller-publication.tar")
	if _, err := CreateArchiveWithReport(root, archivePath); err != nil {
		t.Fatal(err)
	}

	external := filepath.Join(t.TempDir(), "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	swapped := false
	testAfterArchiveRepositoryMkdir = func(repositoryPath string) {
		if swapped {
			return
		}
		swapped = true
		if err := os.Rename(repositoryPath, repositoryPath+".original"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, repositoryPath); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "caller-leaves")
	if err := RestoreArchive(archivePath, target); err == nil {
		t.Fatal("RestoreArchive accepted a swapped repository directory")
	}
	if !swapped {
		t.Fatal("restore did not exercise the repository-directory swap")
	}
	entries, err := os.ReadDir(external)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("restore escaped its private stage: %v", entries)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("failed restore published a target: %v", err)
	}
}

func TestRestoreCallerArchivePinsStageThroughInstallation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	manifest := installArchivePublication(
		t, root, "github.com/acme/restore-stage-swap",
		func(context.Context, State) error { return nil },
	)
	archivePath := filepath.Join(t.TempDir(), "caller-publication.tar")
	if _, err := CreateArchiveWithReport(root, archivePath); err != nil {
		t.Fatal(err)
	}

	t.Run("before rename", func(t *testing.T) {
		resetArchiveHooks(t)
		external := t.TempDir()
		testBeforeArchiveStageRename = func(stage string) {
			if err := os.Rename(stage, stage+".admitted"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, stage); err != nil {
				t.Fatal(err)
			}
		}
		target := filepath.Join(t.TempDir(), "caller-leaves")
		if err := RestoreArchive(archivePath, target); err == nil ||
			!strings.Contains(err.Error(), "stage changed before installation") {
			t.Fatalf("RestoreArchive pre-rename stage swap = %v", err)
		}
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pre-rename stage swap published target: %v", err)
		}
		entries, err := os.ReadDir(external)
		if err != nil || len(entries) != 0 {
			t.Fatalf("pre-rename stage swap escaped: %v, %v", entries, err)
		}
	})

	t.Run("after rename", func(t *testing.T) {
		resetArchiveHooks(t)
		external := t.TempDir()
		testAfterArchiveStageRename = func(target string) {
			if err := os.Rename(target, target+".admitted"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, target); err != nil {
				t.Fatal(err)
			}
		}
		target := filepath.Join(t.TempDir(), "caller-leaves")
		if err := RestoreArchive(archivePath, target); err == nil ||
			!strings.Contains(err.Error(), "target changed during installation") {
			t.Fatalf("RestoreArchive post-rename stage swap = %v", err)
		}
		entries, err := os.ReadDir(external)
		if err != nil || len(entries) != 0 {
			t.Fatalf("post-rename stage swap escaped: %v, %v", entries, err)
		}
	})

	t.Run("post-install content validation", func(t *testing.T) {
		resetArchiveHooks(t)
		leaf := ArtifactRefs(manifest)[1]
		testAfterArchiveStageRename = func(target string) {
			path := filepath.Join(target, leaf.RepositoryDirectory, leaf.Name)
			if err := os.WriteFile(path, []byte("corrupt\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		target := filepath.Join(t.TempDir(), "caller-leaves")
		if err := RestoreArchive(archivePath, target); err == nil ||
			!strings.Contains(err.Error(), "validate installed") {
			t.Fatalf("RestoreArchive post-install mutation = %v", err)
		}
	})

	t.Run("parent swap after rename", func(t *testing.T) {
		resetArchiveHooks(t)
		parent := t.TempDir()
		target := filepath.Join(parent, "caller-leaves")
		moved := parent + "-moved"
		external := t.TempDir()
		testAfterArchiveStageRename = func(string) {
			if err := os.Rename(parent, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, parent); err != nil {
				t.Fatal(err)
			}
		}
		err := RestoreArchive(archivePath, target)
		if removeErr := os.Remove(parent); removeErr != nil {
			t.Fatal(removeErr)
		}
		if renameErr := os.Rename(moved, parent); renameErr != nil {
			t.Fatal(renameErr)
		}
		if err == nil || !strings.Contains(err.Error(), "parent changed") {
			t.Fatalf("RestoreArchive parent swap = %v", err)
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("failed restore left an installed target: %v", statErr)
		}
	})
}

func callerPAXSparseArchive(name string) []byte {
	records := callerPAXRecord("GNU.sparse.size", "1")
	result := callerRawTarHeader(
		"PaxHeaders.0/caller", tar.TypeXHeader, int64(len(records)),
	)
	result = callerAppendPadded(result, []byte(records))
	result = append(result, callerRawTarHeader(name, tar.TypeReg, 1)...)
	result = callerAppendPadded(result, []byte("x"))
	return append(result, make([]byte, 1024)...)
}

func callerOldGNUSparseArchive(name string) []byte {
	header := callerRawTarHeader(name, tar.TypeGNUSparse, 1)
	copy(header[257:263], "ustar ")
	copy(header[263:265], " \x00")
	callerPutTarOctal(header[386:398], 0)
	callerPutTarOctal(header[398:410], 1)
	callerPutTarBase256(header[483:495], 8<<20)
	callerSetTarChecksum(header)
	result := append([]byte{}, header...)
	result = callerAppendPadded(result, []byte("x"))
	return append(result, make([]byte, 1024)...)
}

func callerPAXRecord(key, value string) string {
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

func callerRawTarHeader(name string, typeflag byte, size int64) []byte {
	header := make([]byte, 512)
	copy(header[0:100], name)
	callerPutTarOctal(header[100:108], 0o600)
	callerPutTarOctal(header[108:116], 0)
	callerPutTarOctal(header[116:124], 0)
	callerPutTarOctal(header[124:136], size)
	callerPutTarOctal(header[136:148], 0)
	header[156] = typeflag
	copy(header[257:263], "ustar\x00")
	copy(header[263:265], "00")
	callerSetTarChecksum(header)
	return header
}

func callerPutTarOctal(field []byte, value int64) {
	for index := range field {
		field[index] = 0
	}
	copy(field, fmt.Sprintf("%0*o", len(field)-1, value))
}

func callerPutTarBase256(field []byte, value int64) {
	for index := len(field) - 1; index >= 0; index-- {
		field[index] = byte(value)
		value >>= 8
	}
	field[0] |= 0x80
}

func callerSetTarChecksum(header []byte) {
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

func callerAppendPadded(destination, data []byte) []byte {
	destination = append(destination, data...)
	return append(destination, make([]byte, -len(data)&511)...)
}
