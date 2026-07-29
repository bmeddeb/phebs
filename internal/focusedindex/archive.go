package focusedindex

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxArchiveEntries    = 100_000
	maxArchiveBytes      = int64(64 << 30)
	maxArchiveEntryBytes = int64(16 << 30)
	maxArchiveNameBytes  = 255
)

// ArchiveReport records derived focused state that was safe to preserve and
// residue that was deliberately omitted. Omission never weakens restore:
// archives still contain only complete self-contained publications.
type ArchiveReport struct {
	Publications        int
	OmittedPublications int
	OmittedArtifacts    int
	StaleMarkers        int
}

func VerifyArchive(archivePath string) error {
	root, err := os.MkdirTemp("", "phebs-focused-archive-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	return RestoreArchive(archivePath, filepath.Join(root, "index"))
}

// CreateArchive writes a deterministic inventory of every complete focused
// publication. Invalid or interrupted derived state is omitted so it cannot
// prevent backup of the precious database export.
func CreateArchive(indexDir, destination string) error {
	_, err := CreateArchiveWithReport(indexDir, destination)
	return err
}

// CreateArchiveWithReport is CreateArchive with omission accounting for the
// operator-facing backup diagnostic.
func CreateArchiveWithReport(
	indexDir, destination string,
) (ArchiveReport, error) {
	publications, files, report, err := archivablePublications(indexDir)
	if err != nil {
		return report, err
	}
	if len(files) > maxArchiveEntries {
		return report, errors.New("focused archive contains too many entries")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return report, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = file.Close()
			_ = os.Remove(destination)
		}
	}()
	writer := tar.NewWriter(file)
	var total int64
	for _, name := range files {
		path := filepath.Join(indexDir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() ||
			info.Size() < 0 || info.Size() > maxArchiveEntryBytes ||
			total > maxArchiveBytes-info.Size() {
			_ = writer.Close()
			return report, fmt.Errorf(
				"focused archive input %q is missing, special, or exceeds its limit",
				name,
			)
		}
		total += info.Size()
		header := &tar.Header{
			Name:       name,
			Mode:       0o600,
			Size:       info.Size(),
			ModTime:    time.Unix(0, 0).UTC(),
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			_ = writer.Close()
			return report, err
		}
		source, err := os.Open(path)
		if err != nil {
			_ = writer.Close()
			return report, err
		}
		openedInfo, statErr := source.Stat()
		if statErr != nil || !openedInfo.Mode().IsRegular() ||
			!os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
			_ = source.Close()
			_ = writer.Close()
			return report, fmt.Errorf(
				"focused archive input %q changed before it was copied", name,
			)
		}
		written, copyErr := io.CopyN(writer, source, info.Size())
		closeErr := source.Close()
		if copyErr != nil || written != info.Size() || closeErr != nil {
			_ = writer.Close()
			return report, errors.Join(copyErr, closeErr)
		}
	}
	if err := writer.Close(); err != nil {
		return report, err
	}
	archiveInfo, err := file.Stat()
	if err != nil || !archiveInfo.Mode().IsRegular() ||
		archiveInfo.Size() <= 0 || archiveInfo.Size() > maxArchiveBytes {
		return report, errors.New(
			"created focused archive is special, empty, or exceeds its physical limit",
		)
	}
	if err := file.Sync(); err != nil {
		return report, err
	}
	if err := file.Close(); err != nil {
		return report, err
	}
	for repository, digest := range publications {
		manifest, err := validateSelfContainedIgnoringMarker(indexDir, repository)
		if err != nil || manifest.Digest != digest {
			return report, errors.New(
				"focused publication changed while backup was being created",
			)
		}
	}
	if err := VerifyArchive(destination); err != nil {
		return report, fmt.Errorf("verify created focused archive: %w", err)
	}
	complete = true
	return report, nil
}

// RestoreArchive performs a complete structural pass before creating the
// target or staging output. It then verifies all extracted bytes in a private
// process-owned directory and installs shards and sidecars before manifests.
func RestoreArchive(archivePath, indexDir string) error {
	pathInfo, err := os.Lstat(archivePath)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return errors.New("focused archive is missing or special")
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = archive.Close() }()
	archiveInfo, err := archive.Stat()
	if err != nil || !archiveInfo.Mode().IsRegular() ||
		!os.SameFile(pathInfo, archiveInfo) ||
		archiveInfo.Size() <= 0 || archiveInfo.Size() > maxArchiveBytes {
		return errors.New(
			"focused archive is missing, special, empty, or exceeds its limit",
		)
	}

	preflight, err := scanArchive(archive, archiveInfo.Size(), "")
	if err != nil {
		return err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		return err
	}
	if err := ensureRealDirectory(indexDir); err != nil {
		return err
	}
	stage, err := newRestoreWorkspace(indexDir)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	extracted, err := scanArchive(archive, archiveInfo.Size(), stage)
	if err != nil {
		return err
	}
	if !slices.Equal(preflight, extracted) {
		return errors.New("focused archive changed between validation passes")
	}
	_, declared, err := validatedPublications(stage)
	if err != nil {
		return fmt.Errorf("validate focused restore archive: %w", err)
	}
	sort.Strings(extracted)
	if !slices.Equal(extracted, declared) {
		return fmt.Errorf(
			"focused restore archive inventory mismatch: actual=%v declared=%v",
			extracted, declared,
		)
	}
	for _, name := range extracted {
		if _, err := os.Lstat(filepath.Join(indexDir, name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("focused restore target %q already exists", name)
		}
	}
	manifests := make([]string, 0)
	for _, name := range extracted {
		if strings.HasSuffix(name, ".manifest.json") {
			manifests = append(manifests, name)
			continue
		}
		if err := moveRegular(
			filepath.Join(stage, name), filepath.Join(indexDir, name),
		); err != nil {
			return err
		}
	}
	for _, name := range manifests {
		if err := moveRegular(
			filepath.Join(stage, name), filepath.Join(indexDir, name),
		); err != nil {
			return err
		}
	}
	return syncDirectory(indexDir)
}

func scanArchive(
	archive *os.File,
	physicalSize int64,
	destination string,
) ([]string, error) {
	limited := &io.LimitedReader{R: archive, N: physicalSize}
	reader := tar.NewReader(limited)
	extracted := make([]string, 0)
	seen := map[string]bool{}
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			if limited.N != 0 {
				return nil, errors.New(
					"focused archive contains trailing data after its end marker",
				)
			}
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read focused archive: %w", nextErr)
		}
		if err := validateArchiveHeader(
			header, seen, total, physicalSize,
		); err != nil {
			return nil, err
		}
		if len(extracted) == maxArchiveEntries {
			return nil, errors.New("focused archive contains too many entries")
		}
		seen[header.Name] = true
		total += header.Size
		if destination == "" {
			if _, err := io.CopyN(io.Discard, reader, header.Size); err != nil {
				return nil, fmt.Errorf(
					"read focused archive entry %q: %w", header.Name, err,
				)
			}
		} else {
			output, err := os.OpenFile(
				filepath.Join(destination, header.Name),
				os.O_WRONLY|os.O_CREATE|os.O_EXCL,
				0o600,
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

func validateArchiveHeader(
	header *tar.Header,
	seen map[string]bool,
	total, physicalSize int64,
) error {
	name := header.Name
	unsafe := func() error {
		return fmt.Errorf("focused archive entry %q is unsafe", name)
	}
	if header.Typeflag != tar.TypeReg ||
		(header.Format != tar.FormatUSTAR && header.Format != tar.FormatPAX) ||
		len(name) == 0 || len(name) > maxArchiveNameBytes ||
		filepath.Base(name) != name ||
		!strings.HasPrefix(name, "phebs-focus-") ||
		seen[name] ||
		header.Linkname != "" ||
		header.Mode != 0o600 ||
		header.Uid != 0 || header.Gid != 0 ||
		header.Uname != "" || header.Gname != "" ||
		header.Devmajor != 0 || header.Devminor != 0 ||
		!header.ModTime.Equal(time.Unix(0, 0)) ||
		!header.AccessTime.IsZero() || !header.ChangeTime.IsZero() ||
		header.Size < 0 || header.Size > maxArchiveEntryBytes ||
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

func archivablePublications(
	indexDir string,
) (map[string]string, []string, ArchiveReport, error) {
	var report ArchiveReport
	entries, err := os.ReadDir(indexDir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, []string{}, report, nil
	}
	if err != nil {
		return nil, nil, report, err
	}
	publications := map[string]string{}
	files := map[string]bool{}
	candidateManifests := map[string]bool{}
	selectedMarkers := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "phebs-focus-") ||
			!strings.HasSuffix(entry.Name(), ".manifest.json") {
			continue
		}
		candidateManifests[entry.Name()] = true
		var envelope Manifest
		if err := readControlFile(
			filepath.Join(indexDir, entry.Name()), &envelope,
		); err != nil ||
			entry.Name() != ManifestName(envelope.Repository) ||
			publications[envelope.Repository] != "" {
			report.OmittedPublications++
			continue
		}
		manifest, err := validateSelfContainedIgnoringMarker(
			indexDir, envelope.Repository,
		)
		if err != nil {
			report.OmittedPublications++
			continue
		}
		publications[manifest.Repository] = manifest.Digest
		selectedMarkers[PublishingName(manifest.Repository)] = true
		files[entry.Name()] = true
		for _, member := range manifest.Members {
			files[member.Name] = true
			files[member.Name+MemberSuffix] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if files[name] || !strings.HasPrefix(name, "phebs-focus-") {
			continue
		}
		if selectedMarkers[name] {
			report.StaleMarkers++
			continue
		}
		if candidateManifests[name] {
			continue
		}
		report.OmittedArtifacts++
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	report.Publications = len(publications)
	return publications, names, report, nil
}

func validatedPublications(indexDir string) (map[string]string, []string, error) {
	entries, err := os.ReadDir(indexDir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, []string{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	publications := map[string]string{}
	files := map[string]bool{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".publishing") &&
			strings.HasPrefix(entry.Name(), "phebs-focus-") {
			return nil, nil, errors.New("focused publication is in progress")
		}
		if !strings.HasPrefix(entry.Name(), "phebs-focus-") ||
			!strings.HasSuffix(entry.Name(), ".manifest.json") {
			continue
		}
		var envelope Manifest
		if err := readControlFile(filepath.Join(indexDir, entry.Name()), &envelope); err != nil {
			return nil, nil, err
		}
		if entry.Name() != ManifestName(envelope.Repository) ||
			publications[envelope.Repository] != "" {
			return nil, nil, errors.New(
				"focused manifest filename or repository is ambiguous",
			)
		}
		manifest, err := ValidateSelfContained(indexDir, envelope.Repository)
		if err != nil {
			return nil, nil, err
		}
		publications[manifest.Repository] = manifest.Digest
		files[entry.Name()] = true
		for _, member := range manifest.Members {
			files[member.Name] = true
			files[member.Name+MemberSuffix] = true
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "phebs-focus-") && !files[entry.Name()] {
			return nil, nil, fmt.Errorf(
				"focused archive contains undeclared artifact %q", entry.Name(),
			)
		}
	}
	sort.Strings(names)
	return publications, names, nil
}
