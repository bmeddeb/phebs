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
	"strings"
	"time"
)

const (
	MaxOmissionDetails = 64
	maxArchiveEntries  = MaxDirectoryEntries
	maxArchiveBytes    = int64(1 << 40)
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
	if err := ensureRealDirectory(root); err != nil {
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
	writer := tar.NewWriter(file)
	archived := make(map[string]struct{})
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
	if err := file.Close(); err != nil {
		return report, err
	}
	success = true
	return report, nil
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

func appendStableTarFile(writer *tar.Writer, sourcePath, name string) error {
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

// RestoreArchive validates every header and publication in a private stage,
// then renames the complete filesystem set into an absent target.
func RestoreArchive(archivePath, target string) error {
	if _, err := os.Lstat(target); err == nil {
		return errors.New("resolver catalog restore target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
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
	source, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	reader := tar.NewReader(source)
	seen := make(map[string]struct{})
	var entries int
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		entries++
		total += header.Size
		entryLimit := MaxMemberContentBytes
		if strings.HasSuffix(header.Name, ".manifest.json") {
			entryLimit = int64(maxManifestBytes)
		}
		if entries > maxArchiveEntries || total > maxArchiveBytes ||
			header.Typeflag != tar.TypeReg ||
			header.Name == "" || filepath.Base(header.Name) != header.Name ||
			len(header.Name) > 512 ||
			!strings.HasPrefix(header.Name, "phebs-resolver-catalog-") ||
			header.Size < 0 || header.Size > entryLimit {
			return errors.New("resolver catalog archive has an invalid entry")
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return errors.New("resolver catalog archive has a duplicate entry")
		}
		seen[header.Name] = struct{}{}
		targetPath := filepath.Join(stage, header.Name)
		file, createErr := os.OpenFile(
			targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600,
		)
		if createErr != nil {
			return createErr
		}
		_, copyErr := io.CopyN(file, reader, header.Size)
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
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
	if len(referenced) != len(seen) {
		return errors.New("resolver catalog archive contains an unreferenced artifact")
	}
	for name := range seen {
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
