package resolvercatalog

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	MaxOmissionDetails  = 64
	maxArchiveEntries   = MaxDirectoryEntries
	maxArchiveBytes     = int64(1 << 40)
	maxArchiveNameBytes = 512
)

type Omission struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ArchiveReport is bounded even if an artifact directory is adversarially
// large. Counts remain exact; Details retains only the first fixed set.
type ArchiveReport struct {
	Publications        int        `json:"publications"`
	OmittedPublications int        `json:"omitted_publications"`
	OmittedArtifacts    int        `json:"omitted_artifacts"`
	StaleMarkers        int        `json:"stale_markers"`
	Details             []Omission `json:"details,omitempty"`
	TruncatedDetails    int        `json:"truncated_details"`
}

type boundedArchiveOutput struct {
	destination io.Writer
	remaining   int64
	written     int64
}

func (output *boundedArchiveOutput) Write(raw []byte) (int, error) {
	if int64(len(raw)) > output.remaining {
		return 0, ErrLimit
	}
	written, err := output.destination.Write(raw)
	output.remaining -= int64(written)
	output.written += int64(written)
	if err == nil && written != len(raw) {
		err = io.ErrShortWrite
	}
	return written, err
}

func (report *ArchiveReport) omit(name, reason string, publication bool) {
	if publication {
		report.OmittedPublications++
	} else {
		report.OmittedArtifacts++
	}
	if len(report.Details) < MaxOmissionDetails {
		report.Details = append(report.Details, Omission{Name: name, Reason: reason})
	} else {
		report.TruncatedDetails++
	}
}

// CreateArchiveWithReport writes a deterministic archive containing every and
// only strictly valid, marker-free catalog publication.
func CreateArchiveWithReport(root, output string) (ArchiveReport, error) {
	var report ArchiveReport
	if err := validateOptionalArchiveRoot(root); err != nil {
		return report, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return report, err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return report, err
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(output)
		}
	}()
	boundedOutput := &boundedArchiveOutput{
		destination: file, remaining: maxArchiveBytes,
	}
	writer := tar.NewWriter(boundedOutput)
	archived := make(map[string]struct{})
	archiveEntries := 0
	var logicalBytes int64
	publications, scanReport, err := discoverPublications(root)
	report = scanReport
	if err != nil {
		_ = writer.Close()
		return report, err
	}
	for _, publication := range publications {
		names := publicationArtifactNames(publication.manifest)
		for _, name := range names {
			if err := appendStableTarFile(
				writer, filepath.Join(root, name), name,
				&archiveEntries, &logicalBytes,
			); err != nil {
				_ = writer.Close()
				return report, fmt.Errorf("archive resolver catalog %q: %w", name, err)
			}
			archived[name] = struct{}{}
		}
		if !publication.Current() {
			_ = writer.Close()
			return report, errors.New("resolver catalog changed during backup")
		}
		report.Publications++
	}
	if err := countUnarchivedArtifacts(root, archived, &report); err != nil {
		_ = writer.Close()
		return report, err
	}
	if err := writer.Close(); err != nil {
		return report, err
	}
	if err := file.Sync(); err != nil {
		return report, err
	}
	archiveInfo, err := file.Stat()
	if err != nil || archiveInfo.Size() != boundedOutput.written ||
		archiveInfo.Size() > maxArchiveBytes {
		return report, errors.New("resolver catalog archive exceeds its physical limit")
	}
	if err := file.Close(); err != nil {
		return report, err
	}
	success = true
	return report, nil
}

func validateOptionalArchiveRoot(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("resolver catalog archive root is not a real directory")
	}
	return nil
}

func discoverPublications(root string) ([]*Publication, ArchiveReport, error) {
	var report ArchiveReport
	entries, err := readBoundedDirectory(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, report, nil
	}
	if err != nil {
		return nil, report, err
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	publications := make([]*Publication, 0)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".publishing") &&
			strings.HasPrefix(name, "phebs-resolver-catalog-") {
			report.StaleMarkers++
			continue
		}
		if !strings.HasSuffix(name, ".manifest.json") ||
			!strings.HasPrefix(name, "phebs-resolver-catalog-") {
			continue
		}
		raw, _, readErr := readStableRegular(
			filepath.Join(root, name), maxManifestBytes,
		)
		if readErr != nil {
			report.omit(name, "invalid_manifest", true)
			continue
		}
		var manifest Manifest
		if decodeErr := decodeCanonical(raw, &manifest); decodeErr != nil ||
			validateManifest(manifest) != nil ||
			manifest.State().Manifest != name {
			report.omit(name, "invalid_manifest", true)
			continue
		}
		if IsPublishing(root, manifest.Identity.Repository) {
			report.omit(name, "publication_marker", true)
			continue
		}
		publication, openErr := Open(
			context.Background(), root, manifest.State(),
		)
		if openErr != nil {
			report.omit(name, "invalid_publication", true)
			continue
		}
		publications = append(publications, publication)
	}
	return publications, report, nil
}

func publicationArtifactNames(manifest Manifest) []string {
	names := make([]string, 0, len(manifest.Members)+1)
	names = append(names, manifest.State().Manifest)
	for _, member := range manifest.Members {
		names = append(names, memberArtifactName(manifest.Identity, member.Name))
	}
	slices.Sort(names)
	return names
}

func countUnarchivedArtifacts(
	root string,
	archived map[string]struct{},
	report *ArchiveReport,
) error {
	entries, err := readBoundedDirectory(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := archived[name]; ok ||
			strings.HasPrefix(name, stageDirectoryPrefix) ||
			strings.HasSuffix(name, ".publishing") {
			continue
		}
		if strings.HasPrefix(name, "phebs-resolver-catalog-") {
			report.omit(name, "unreferenced_artifact", false)
		}
	}
	return nil
}

func appendStableTarFile(
	writer *tar.Writer,
	sourcePath, name string,
	archiveEntries *int,
	logicalBytes *int64,
) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	limit := MaxMemberContentBytes
	if strings.HasSuffix(name, ".manifest.json") {
		limit = int64(maxManifestBytes)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > limit {
		return errors.New("archive source is not a bounded regular file")
	}
	if err := reserveResolverArchiveEntry(
		archiveEntries, logicalBytes, info.Size(),
	); err != nil {
		return err
	}
	file, fingerprint, err := openStableRegular(sourcePath, info.Size())
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	header := &tar.Header{
		Name: name, Mode: 0o600, Size: info.Size(),
		ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg,
		Format: tar.FormatPAX,
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	if _, err := io.CopyN(writer, file, info.Size()); err != nil {
		return err
	}
	return verifyFingerprint(file, fingerprint)
}

func reserveResolverArchiveEntry(
	entries *int,
	logicalBytes *int64,
	size int64,
) error {
	if entries == nil || logicalBytes == nil || size < 0 ||
		*entries >= maxArchiveEntries ||
		*logicalBytes > maxArchiveBytes-size {
		return ErrLimit
	}
	(*entries)++
	*logicalBytes += size
	return nil
}

// RestoreArchive validates every header and publication in a private stage,
// then renames the complete filesystem set into an absent target.
func RestoreArchive(archivePath, target string) error {
	if _, err := os.Lstat(target); err == nil {
		return errors.New("resolver catalog restore target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	pathInfo, err := os.Lstat(archivePath)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return errors.New("resolver catalog archive is missing or special")
	}
	source, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	archiveInfo, err := source.Stat()
	if err != nil || !archiveInfo.Mode().IsRegular() ||
		!os.SameFile(pathInfo, archiveInfo) ||
		archiveInfo.Size() <= 0 || archiveInfo.Size() > maxArchiveBytes {
		return errors.New(
			"resolver catalog archive is missing, special, empty, or exceeds its limit",
		)
	}
	preflight, err := scanResolverArchive(
		source, archiveInfo.Size(), "",
	)
	if err != nil {
		return err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".phebs-resolver-catalog-restore-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	extracted, err := scanResolverArchive(
		source, archiveInfo.Size(), stage,
	)
	if err != nil {
		return err
	}
	if !slices.Equal(preflight, extracted) {
		return errors.New("resolver catalog archive changed between validation passes")
	}
	current, err := os.Lstat(archivePath)
	if err != nil || !sameArchiveFileIdentity(archiveInfo, current) {
		return errors.New("resolver catalog archive changed while it was restored")
	}
	publications, report, err := discoverPublications(stage)
	if err != nil {
		return err
	}
	if report.OmittedPublications != 0 || report.StaleMarkers != 0 {
		return errors.New("resolver catalog archive contains an invalid publication")
	}
	referenced := make(map[string]struct{})
	for _, publication := range publications {
		for _, name := range publicationArtifactNames(publication.manifest) {
			referenced[name] = struct{}{}
		}
	}
	if len(referenced) != len(extracted) {
		return errors.New("resolver catalog archive contains an unreferenced artifact")
	}
	for _, name := range extracted {
		if _, ok := referenced[name]; !ok {
			return errors.New("resolver catalog archive contains an unreferenced artifact")
		}
	}
	if err := syncDirectory(stage); err != nil {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		return err
	}
	published = true
	return syncDirectory(filepath.Dir(target))
}

func scanResolverArchive(
	source *os.File,
	physicalSize int64,
	destination string,
) ([]string, error) {
	limited := &io.LimitedReader{R: source, N: physicalSize}
	reader := tar.NewReader(limited)
	extracted := make([]string, 0)
	seen := make(map[string]bool)
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			if limited.N != 0 {
				return nil, errors.New(
					"resolver catalog archive contains trailing data after its end marker",
				)
			}
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read resolver catalog archive: %w", nextErr)
		}
		if err := validateResolverArchiveHeader(
			header, seen, total, physicalSize,
		); err != nil {
			return nil, err
		}
		if len(extracted) == maxArchiveEntries {
			return nil, errors.New("resolver catalog archive contains too many entries")
		}
		seen[header.Name] = true
		total += header.Size
		if destination == "" {
			if _, err := io.CopyN(io.Discard, reader, header.Size); err != nil {
				return nil, fmt.Errorf(
					"read resolver catalog archive entry %q: %w",
					header.Name, err,
				)
			}
		} else {
			output, err := os.OpenFile(
				filepath.Join(destination, header.Name),
				os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
			)
			if err != nil {
				return nil, err
			}
			written, copyErr := io.CopyN(output, reader, header.Size)
			syncErr := output.Sync()
			closeErr := output.Close()
			if copyErr != nil || written != header.Size ||
				syncErr != nil || closeErr != nil {
				return nil, errors.Join(copyErr, syncErr, closeErr)
			}
		}
		extracted = append(extracted, header.Name)
	}
	return extracted, nil
}

func validateResolverArchiveHeader(
	header *tar.Header,
	seen map[string]bool,
	total, physicalSize int64,
) error {
	name := header.Name
	unsafe := func() error {
		return fmt.Errorf("resolver catalog archive entry %q is unsafe", name)
	}
	entryLimit := MaxMemberContentBytes
	if strings.HasSuffix(name, ".manifest.json") {
		entryLimit = int64(maxManifestBytes)
	}
	if header.Typeflag != tar.TypeReg ||
		(header.Format != tar.FormatUSTAR && header.Format != tar.FormatPAX) ||
		name == "" || len(name) > maxArchiveNameBytes ||
		filepath.Base(name) != name ||
		!strings.HasPrefix(name, "phebs-resolver-catalog-") ||
		seen[name] || header.Linkname != "" ||
		header.Mode != 0o600 ||
		header.Uid != 0 || header.Gid != 0 ||
		header.Uname != "" || header.Gname != "" ||
		header.Devmajor != 0 || header.Devminor != 0 ||
		!header.ModTime.Equal(time.Unix(0, 0)) ||
		!header.AccessTime.IsZero() || !header.ChangeTime.IsZero() ||
		header.Size < 0 || header.Size > entryLimit ||
		total > maxArchiveBytes-header.Size ||
		total > physicalSize-header.Size {
		return unsafe()
	}
	for key, value := range header.PAXRecords {
		switch key {
		case "path":
			if value != name {
				return unsafe()
			}
		case "size":
			if value != strconv.FormatInt(header.Size, 10) {
				return unsafe()
			}
		default:
			return unsafe()
		}
	}
	return nil
}

func sameArchiveFileIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil &&
		left.Mode().IsRegular() && right.Mode().IsRegular() &&
		os.SameFile(left, right) &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

// VerifyArchiveWithReport runs the exact restore validator in a disposable
// directory and returns the independently observed publication count.
func VerifyArchiveWithReport(archivePath string) (ArchiveReport, error) {
	parent, err := os.MkdirTemp("", "phebs-resolver-catalog-verify-parent-")
	if err != nil {
		return ArchiveReport{}, err
	}
	defer func() { _ = os.RemoveAll(parent) }()
	target := filepath.Join(parent, "catalogs")
	if err := RestoreArchive(archivePath, target); err != nil {
		return ArchiveReport{}, err
	}
	publications, report, err := discoverPublications(target)
	if err != nil {
		return ArchiveReport{}, err
	}
	if report.OmittedPublications != 0 || report.OmittedArtifacts != 0 ||
		report.StaleMarkers != 0 {
		return ArchiveReport{}, errors.New("restored resolver catalog archive is not exact")
	}
	report.Publications = len(publications)
	return report, nil
}
