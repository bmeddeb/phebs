package focusedindex

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
)

const (
	maxArchiveEntries    = 100_000
	maxArchiveBytes      = MaxRetainedSearchLogicalBytes + int64(1<<30)
	maxArchiveEntryBytes = int64(16 << 30)
	maxArchiveNameBytes  = 512
)

// ArchiveReport records derived search state that was safe to preserve and
// residue that was deliberately omitted. Omission never weakens restore:
// archives still contain only complete self-contained publications.
type ArchiveReport struct {
	Publications        int
	OmittedPublications int
	OmittedArtifacts    int
	StaleMarkers        int
}

// ArchiveSearchGeneration names one immutable generation that a durable
// runtime selector still exposes even when the mutable current root advances.
type ArchiveSearchGeneration struct {
	Repository       string
	GenerationDigest string
}

func VerifyArchive(archivePath string) error {
	_, err := VerifyArchiveWithReport(archivePath)
	return err
}

// VerifyArchiveWithReport is VerifyArchive with independently recovered
// publication accounting. Explicit verification deliberately retains the
// complete restore round trip; backup creation uses a cheaper construction
// proof instead.
func VerifyArchiveWithReport(archivePath string) (ArchiveReport, error) {
	root, err := os.MkdirTemp("", "phebs-focused-archive-")
	if err != nil {
		return ArchiveReport{}, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	indexDir := filepath.Join(root, "index")
	if err := RestoreArchive(archivePath, indexDir); err != nil {
		return ArchiveReport{}, err
	}
	var report ArchiveReport
	publications, _, err := validatedPublications(indexDir)
	if err != nil {
		return ArchiveReport{}, err
	}
	report.Publications = len(publications)
	return report, nil
}

// CreateArchive writes a deterministic inventory of every complete focused or
// repository-search v2 publication. Invalid or interrupted derived state is
// omitted so it cannot prevent backup of the precious database export.
func CreateArchive(indexDir, destination string) error {
	_, err := CreateArchiveWithReport(indexDir, destination)
	return err
}

// CreateArchiveWithReport is CreateArchive with omission accounting for the
// operator-facing backup diagnostic.
func CreateArchiveWithReport(
	indexDir, destination string,
) (ArchiveReport, error) {
	return CreateArchiveWithReportContext(
		context.Background(), indexDir, destination,
	)
}

// CreateArchiveWithReportContext is the cancellable backup boundary used by
// recovery while it holds the shared index backup lock.
func CreateArchiveWithReportContext(
	ctx context.Context,
	indexDir, destination string,
) (ArchiveReport, error) {
	return CreateArchiveWithSelections(
		ctx, indexDir, destination, nil,
	)
}

// CreateArchiveWithSelections additionally preserves exact immutable search
// generations named by durable runtime selectors. It does not preserve their
// mutable lifecycle pointers or unrelated rollback generations.
func CreateArchiveWithSelections(
	ctx context.Context,
	indexDir, destination string,
	selections []ArchiveSearchGeneration,
) (ArchiveReport, error) {
	expectations, report, err := archivablePublications(ctx, indexDir, selections)
	if err != nil {
		return report, err
	}
	files := make([]string, 0, len(expectations))
	for name := range expectations {
		files = append(files, name)
	}
	sort.Strings(files)
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
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return report, err
		}
		expectation := expectations[name]
		sourceName := name
		if expectation.source != "" {
			sourceName = expectation.source
		}
		if filepath.IsAbs(sourceName) || filepath.Clean(sourceName) != sourceName ||
			sourceName == "." || strings.HasPrefix(sourceName, ".."+string(filepath.Separator)) {
			_ = writer.Close()
			return report, fmt.Errorf("focused archive source %q is unsafe", sourceName)
		}
		path := filepath.Join(indexDir, sourceName)
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
		digest := sha256.New()
		written, copyErr := io.CopyN(
			io.MultiWriter(writer, digest), source, info.Size(),
		)
		after, afterErr := source.Stat()
		closeErr := source.Close()
		current, currentErr := os.Lstat(path)
		if copyErr != nil || written != info.Size() || afterErr != nil ||
			closeErr != nil || currentErr != nil {
			_ = writer.Close()
			return report, errors.Join(copyErr, afterErr, closeErr, currentErr)
		}
		if !sameArchiveFileIdentity(info, after) ||
			!sameArchiveFileIdentity(after, current) {
			_ = writer.Close()
			return report, fmt.Errorf(
				"focused archive input %q changed while it was copied", name,
			)
		}
		actualDigest := "sha256:" + hex.EncodeToString(digest.Sum(nil))
		if actualDigest != expectation.digest {
			_ = writer.Close()
			return report, fmt.Errorf(
				"focused archive input %q changed after validation", name,
			)
		}
		expectation.size = info.Size()
		expectations[name] = expectation
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
	if err := verifyCreatedArchive(destination, expectations); err != nil {
		return report, fmt.Errorf("verify created focused archive: %w", err)
	}
	complete = true
	return report, nil
}

type archiveExpectation struct {
	digest string
	size   int64
	source string
}

// verifyCreatedArchive proves that the tar writer emitted exactly the safe,
// byte-identical inventory frozen by archivablePublications. It performs one
// bounded streaming pass and never materializes archive contents on disk.
func verifyCreatedArchive(
	archivePath string,
	expectations map[string]archiveExpectation,
) error {
	pathInfo, err := os.Lstat(archivePath)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return errors.New("created focused archive is missing or special")
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
			"created focused archive is missing, special, empty, or exceeds its limit",
		)
	}
	limited := &io.LimitedReader{R: archive, N: archiveInfo.Size()}
	reader := tar.NewReader(limited)
	seen := make(map[string]bool, len(expectations))
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			if limited.N != 0 {
				return errors.New(
					"created focused archive contains trailing data after its end marker",
				)
			}
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read created focused archive: %w", nextErr)
		}
		if err := validateArchiveHeader(
			header, seen, total, archiveInfo.Size(),
		); err != nil {
			return err
		}
		expectation, ok := expectations[header.Name]
		if !ok || expectation.size != header.Size {
			return fmt.Errorf(
				"created focused archive entry %q differs from its inventory",
				header.Name,
			)
		}
		if len(seen) == maxArchiveEntries {
			return errors.New("created focused archive contains too many entries")
		}
		seen[header.Name] = true
		total += header.Size
		digest := sha256.New()
		written, err := io.CopyN(digest, reader, header.Size)
		if err != nil || written != header.Size {
			return fmt.Errorf(
				"read created focused archive entry %q: %w",
				header.Name, err,
			)
		}
		actualDigest := "sha256:" + hex.EncodeToString(digest.Sum(nil))
		if actualDigest != expectation.digest {
			return fmt.Errorf(
				"created focused archive entry %q failed its content proof",
				header.Name,
			)
		}
	}
	if len(seen) != len(expectations) {
		return errors.New("created focused archive inventory is incomplete")
	}
	current, err := os.Lstat(archivePath)
	if err != nil || !sameArchiveFileIdentity(archiveInfo, current) {
		return errors.New("created focused archive changed while it was verified")
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
	materialized, err := materializeSelectedCurrentSearchGenerations(stage)
	if err != nil {
		return fmt.Errorf("materialize selected current search generation: %w", err)
	}
	_, declared, err := validatedPublications(stage)
	if err != nil {
		return fmt.Errorf("validate focused restore archive: %w", err)
	}
	selected, err := validatedSelectedSearchGenerations(stage)
	if err != nil {
		return fmt.Errorf("validate selected search restore archive: %w", err)
	}
	for name := range selected {
		if materialized[name] {
			continue
		}
		declared = append(declared, name)
	}
	sort.Strings(extracted)
	sort.Strings(declared)
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
	searchGenerations := filepath.Join(stage, searchGenerationDirectoryName)
	if _, err := os.Lstat(searchGenerations); err == nil {
		if _, err := os.Lstat(filepath.Join(indexDir, searchGenerationDirectoryName)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("focused restore target %q already exists", searchGenerationDirectoryName)
		}
		if err := syncSelectedSearchDirectories(stage, extracted); err != nil {
			return err
		}
		if err := os.Rename(
			searchGenerations, filepath.Join(indexDir, searchGenerationDirectoryName),
		); err != nil {
			return err
		}
		if err := syncDirectory(indexDir); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	manifests := make([]string, 0)
	for _, name := range extracted {
		if strings.HasPrefix(name, searchGenerationDirectoryName+string(filepath.Separator)) {
			continue
		}
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
			if err := os.MkdirAll(filepath.Dir(filepath.Join(destination, header.Name)), 0o700); err != nil {
				return nil, err
			}
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
		!safeArchiveName(name) ||
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
	ctx context.Context,
	indexDir string,
	selections []ArchiveSearchGeneration,
) (map[string]archiveExpectation, ArchiveReport, error) {
	var report ArchiveReport
	entries, err := os.ReadDir(indexDir)
	if errors.Is(err, os.ErrNotExist) {
		if len(selections) != 0 {
			return nil, report, errors.New("selected search archive root is absent")
		}
		return map[string]archiveExpectation{}, report, nil
	}
	if err != nil {
		return nil, report, err
	}
	publications := map[string]string{}
	files := map[string]archiveExpectation{}
	candidateManifests := map[string]bool{}
	selectedMarkers := map[string]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
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
		publicationFiles, err := archivePublicationExpectations(
			indexDir, manifest,
		)
		if err != nil {
			report.OmittedPublications++
			continue
		}
		publications[manifest.Repository] = manifest.Digest
		selectedMarkers[PublishingName(manifest.Repository)] = true
		for name, expectation := range publicationFiles {
			files[name] = expectation
		}
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		if !strings.HasPrefix(entry.Name(), "phebs-search-") ||
			!strings.HasSuffix(entry.Name(), ".manifest.json") {
			continue
		}
		candidateManifests[entry.Name()] = true
		var envelope repositoryindex.SearchManifest
		if err := readControlFile(
			filepath.Join(indexDir, entry.Name()), &envelope,
		); err != nil ||
			entry.Name() != repositoryindex.SearchManifestName(envelope.Repository) ||
			publications["search:"+envelope.Repository] != "" {
			report.OmittedPublications++
			continue
		}
		search, err := ValidateRepositorySearchGeneration(
			ctx, indexDir, envelope.Repository, envelope.Revisions,
		)
		if err != nil {
			report.OmittedPublications++
			continue
		}
		publicationFiles, err := archiveRepositorySearchExpectations(
			indexDir, search,
		)
		if err != nil {
			report.OmittedPublications++
			continue
		}
		publications["search:"+search.Repository] = search.Digest
		selectedMarkers[PublishingName(search.Repository)] = true
		for name, expectation := range publicationFiles {
			files[name] = expectation
		}
	}
	selected := make(map[string]bool, len(selections))
	for _, selection := range selections {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		key := selection.Repository + "\x00" + selection.GenerationDigest
		if selected[key] {
			continue
		}
		selected[key] = true
		var generationFiles map[string]archiveExpectation
		if publications["search:"+selection.Repository] == selection.GenerationDigest {
			receipt, ok := files[searchGenerationArchiveReceiptName(selection.Repository)]
			if !ok {
				return nil, report, errors.New("selected current search receipt is absent")
			}
			name := filepath.Join(
				searchGenerationDirectoryName, repositoryKey(selection.Repository),
				strings.TrimPrefix(selection.GenerationDigest, "sha256:"),
				searchGenerationReceiptName,
			)
			receipt.source = name
			generationFiles = map[string]archiveExpectation{name: receipt}
		} else {
			var err error
			generationFiles, err = archiveSelectedSearchGenerationExpectations(
				ctx, indexDir, selection,
			)
			if err != nil {
				return nil, report, fmt.Errorf(
					"archive selected search generation for %q: %w",
					selection.Repository, err,
				)
			}
		}
		for name, expectation := range generationFiles {
			files[name] = expectation
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if _, selected := files[name]; selected ||
			!isSearchArchiveArtifact(name) {
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
	report.Publications = len(publications)
	return files, report, nil
}

func archivePublicationExpectations(
	indexDir string,
	manifest Manifest,
) (map[string]archiveExpectation, error) {
	files := make(
		map[string]archiveExpectation,
		1+2*len(manifest.Members),
	)
	manifestName := ManifestName(manifest.Repository)
	var snapshot Manifest
	expectation, err := archiveControlExpectation(
		filepath.Join(indexDir, manifestName), &snapshot,
	)
	if err != nil || !reflect.DeepEqual(snapshot, manifest) {
		return nil, errors.New(
			"focused manifest changed after publication validation",
		)
	}
	files[manifestName] = expectation
	for _, member := range manifest.Members {
		files[member.Name] = archiveExpectation{
			digest: member.ContentDigest,
		}
		var sidecar ShardMember
		sidecarName := member.Name + MemberSuffix
		expectation, err := archiveControlExpectation(
			filepath.Join(indexDir, sidecarName), &sidecar,
		)
		if err != nil || sidecar != member {
			return nil, fmt.Errorf(
				"focused sidecar %q changed after publication validation",
				sidecarName,
			)
		}
		files[sidecarName] = expectation
	}
	return files, nil
}

func archiveRepositorySearchExpectations(
	indexDir string,
	search repositoryindex.SearchManifest,
) (map[string]archiveExpectation, error) {
	source, err := repositoryindex.ReadSourceManifest(indexDir, search.Repository)
	if err != nil {
		return nil, err
	}
	whole, err := ReadWholeManifest(indexDir, search.Repository, search.Revisions)
	if err != nil {
		return nil, err
	}
	files := make(map[string]archiveExpectation, 4+len(source.Members)+len(whole.Members))
	searchName := repositoryindex.SearchManifestName(search.Repository)
	var searchSnapshot repositoryindex.SearchManifest
	expectation, err := archiveControlExpectation(
		filepath.Join(indexDir, searchName), &searchSnapshot,
	)
	if err != nil || !reflect.DeepEqual(searchSnapshot, search) {
		return nil, errors.New("repository-search manifest changed after validation")
	}
	files[searchName] = expectation
	sourceName := repositoryindex.SourceManifestName(search.Repository)
	var sourceSnapshot repositoryindex.SourceManifest
	expectation, err = archiveControlExpectation(
		filepath.Join(indexDir, sourceName), &sourceSnapshot,
	)
	if err != nil || !reflect.DeepEqual(sourceSnapshot, source) {
		return nil, errors.New("repository-source manifest changed after validation")
	}
	files[sourceName] = expectation
	wholeName := WholeManifestName(search.Repository)
	var wholeSnapshot WholeManifest
	expectation, err = archiveControlExpectation(
		filepath.Join(indexDir, wholeName), &wholeSnapshot,
	)
	if err != nil || !reflect.DeepEqual(wholeSnapshot, whole) {
		return nil, errors.New("whole-search manifest changed after validation")
	}
	files[wholeName] = expectation
	for _, member := range source.Members {
		files[member.Name] = archiveExpectation{digest: member.Digest}
	}
	for _, member := range whole.Members {
		files[member.Name] = archiveExpectation{digest: member.ContentDigest}
	}
	root, rootErr := ReadSearchGenerationRoot(indexDir, search.Repository)
	if rootErr == nil {
		if root.Current.GenerationDigest != search.Digest ||
			!sameSearchRevisions(root.Current.Revisions, search.Revisions) {
			return nil, errors.New("search lifecycle root differs from selected flat publication")
		}
		if _, err := validateImmutableSearchGeneration(
			context.Background(), indexDir, search.Repository, search.Digest,
		); err != nil {
			return nil, err
		}
		receiptRelative := filepath.Join(
			searchGenerationDirectoryName, repositoryKey(search.Repository),
			root.Current.Directory, searchGenerationReceiptName,
		)
		var receipt SearchGenerationReceipt
		expectation, err := archiveControlExpectation(
			filepath.Join(indexDir, receiptRelative), &receipt,
		)
		if err != nil || receipt.Repository != search.Repository ||
			receipt.SearchDigest != search.Digest ||
			receipt.SourceDigest != source.Digest ||
			!sameSearchRevisions(receipt.Revisions, search.Revisions) {
			return nil, errors.New("search lifecycle receipt differs from selected flat publication")
		}
		expectation.source = receiptRelative
		files[searchGenerationArchiveReceiptName(search.Repository)] = expectation
	} else if !errors.Is(rootErr, os.ErrNotExist) {
		return nil, rootErr
	}
	return files, nil
}

func archiveSelectedSearchGenerationExpectations(
	ctx context.Context,
	indexDir string,
	selection ArchiveSearchGeneration,
) (map[string]archiveExpectation, error) {
	receipt, err := validateImmutableSearchGeneration(
		ctx, indexDir, selection.Repository, selection.GenerationDigest,
	)
	if err != nil {
		return nil, err
	}
	directory, err := searchGenerationDirectory(
		indexDir, selection.Repository, selection.GenerationDigest,
	)
	if err != nil {
		return nil, err
	}
	search, err := repositoryindex.ReadSearchManifest(directory, selection.Repository)
	if err != nil {
		return nil, err
	}
	flat, err := archiveRepositorySearchExpectations(directory, search)
	if err != nil {
		return nil, err
	}
	prefix := filepath.Join(
		searchGenerationDirectoryName, repositoryKey(selection.Repository),
		strings.TrimPrefix(selection.GenerationDigest, "sha256:"),
	)
	files := make(map[string]archiveExpectation, len(flat)+1)
	for name, expectation := range flat {
		expectation.source = filepath.Join(prefix, name)
		files[filepath.Join(prefix, name)] = expectation
	}
	var snapshot SearchGenerationReceipt
	expectation, err := archiveControlExpectation(
		filepath.Join(directory, searchGenerationReceiptName), &snapshot,
	)
	snapshot.AllocatedBytes = receipt.AllocatedBytes
	snapshot.AllocatedState = receipt.AllocatedState
	if err != nil || !reflect.DeepEqual(snapshot, receipt) {
		return nil, errors.New("selected search receipt changed after validation")
	}
	expectation.source = filepath.Join(prefix, searchGenerationReceiptName)
	files[filepath.Join(prefix, searchGenerationReceiptName)] = expectation
	return files, nil
}

func validatedSelectedSearchGenerations(
	indexDir string,
) (map[string]bool, error) {
	files := map[string]bool{}
	root := SearchGenerationRootDirectory(indexDir)
	repositories, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	if len(repositories) > MaxSearchLifecycleRepositories {
		return nil, errors.New("archived search repository inventory exceeds policy")
	}
	for _, repositoryEntry := range repositories {
		if !repositoryEntry.IsDir() || repositoryEntry.Type()&os.ModeSymlink != 0 ||
			!validArchiveHex(repositoryEntry.Name()) {
			return nil, errors.New("invalid archived search repository directory")
		}
		repositoryDirectory := filepath.Join(root, repositoryEntry.Name())
		generations, err := os.ReadDir(repositoryDirectory)
		if err != nil {
			return nil, err
		}
		if len(generations) < 1 || len(generations) > MaxSearchRepositoryGenerations {
			return nil, errors.New("archived search generation inventory exceeds policy")
		}
		for _, generationEntry := range generations {
			if !generationEntry.IsDir() || generationEntry.Type()&os.ModeSymlink != 0 ||
				!validArchiveHex(generationEntry.Name()) {
				return nil, errors.New("invalid archived search generation directory")
			}
			directory := filepath.Join(repositoryDirectory, generationEntry.Name())
			var envelope SearchGenerationReceipt
			if err := readControlFile(
				filepath.Join(directory, searchGenerationReceiptName), &envelope,
			); err != nil {
				return nil, err
			}
			digest := "sha256:" + generationEntry.Name()
			if repositoryKey(envelope.Repository) != repositoryEntry.Name() ||
				envelope.SearchDigest != digest {
				return nil, errors.New("archived search generation path identity mismatch")
			}
			receipt, err := validateImmutableSearchGeneration(
				context.Background(), indexDir, envelope.Repository, digest,
			)
			envelope.AllocatedBytes = receipt.AllocatedBytes
			envelope.AllocatedState = receipt.AllocatedState
			if err != nil || !reflect.DeepEqual(receipt, envelope) {
				return nil, errors.Join(err, errors.New("archived search generation is invalid"))
			}
			search, err := repositoryindex.ReadSearchManifest(directory, envelope.Repository)
			if err != nil {
				return nil, err
			}
			source, err := repositoryindex.ReadSourceManifest(directory, envelope.Repository)
			if err != nil {
				return nil, err
			}
			whole, err := ReadWholeManifest(directory, envelope.Repository, search.Revisions)
			if err != nil {
				return nil, err
			}
			expected := make(map[string]bool, receipt.FileCount+1)
			for _, name := range searchGenerationFiles(source, whole) {
				expected[name] = true
			}
			expected[searchGenerationReceiptName] = true
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != len(expected) {
				return nil, errors.Join(err, errors.New("archived search generation inventory mismatch"))
			}
			prefix := filepath.Join(
				searchGenerationDirectoryName, repositoryEntry.Name(), generationEntry.Name(),
			)
			for _, entry := range entries {
				if !expected[entry.Name()] || entry.IsDir() ||
					entry.Type()&os.ModeSymlink != 0 {
					return nil, errors.New("archived search generation contains an undeclared artifact")
				}
				info, err := entry.Info()
				if err != nil || !info.Mode().IsRegular() {
					return nil, errors.Join(err, errors.New("archived search generation contains a special artifact"))
				}
				files[filepath.Join(prefix, entry.Name())] = true
			}
		}
	}
	return files, nil
}

func materializeSelectedCurrentSearchGenerations(
	indexDir string,
) (map[string]bool, error) {
	materialized := map[string]bool{}
	root := SearchGenerationRootDirectory(indexDir)
	repositories, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return materialized, nil
	}
	if err != nil || len(repositories) > MaxSearchLifecycleRepositories {
		return nil, errors.Join(err, errors.New("selected search repository inventory exceeds policy"))
	}
	for _, repositoryEntry := range repositories {
		if !repositoryEntry.IsDir() || repositoryEntry.Type()&os.ModeSymlink != 0 ||
			!validArchiveHex(repositoryEntry.Name()) {
			return nil, errors.New("invalid selected search repository directory")
		}
		repositoryDirectory := filepath.Join(root, repositoryEntry.Name())
		generations, err := os.ReadDir(repositoryDirectory)
		if err != nil || len(generations) > MaxSearchRepositoryGenerations {
			return nil, errors.Join(err, errors.New("selected search generation inventory exceeds policy"))
		}
		for _, generationEntry := range generations {
			directory := filepath.Join(repositoryDirectory, generationEntry.Name())
			entries, err := os.ReadDir(directory)
			if err != nil || !generationEntry.IsDir() ||
				generationEntry.Type()&os.ModeSymlink != 0 ||
				!validArchiveHex(generationEntry.Name()) {
				return nil, errors.Join(err, errors.New("invalid selected search generation directory"))
			}
			if len(entries) != 1 || entries[0].Name() != searchGenerationReceiptName {
				continue
			}
			var envelope SearchGenerationReceipt
			if err := readControlFile(
				filepath.Join(directory, searchGenerationReceiptName), &envelope,
			); err != nil {
				return nil, err
			}
			receipt, err := readSearchGenerationReceipt(directory, envelope.Repository)
			if err != nil || repositoryKey(receipt.Repository) != repositoryEntry.Name() ||
				strings.TrimPrefix(receipt.SearchDigest, "sha256:") != generationEntry.Name() {
				return nil, errors.Join(err, errors.New("selected search receipt path identity mismatch"))
			}
			search, err := repositoryindex.ReadSearchManifest(indexDir, receipt.Repository)
			if err != nil || search.Digest != receipt.SearchDigest {
				return nil, errors.Join(err, errors.New("selected receipt-only generation is not current"))
			}
			if _, err := validateFlatSearchGenerationReceipt(
				indexDir, receipt.Repository, search,
			); err != nil {
				return nil, err
			}
			nestedReceipt, err := os.ReadFile(filepath.Join(directory, searchGenerationReceiptName))
			if err != nil {
				return nil, err
			}
			flatReceipt, err := os.ReadFile(filepath.Join(
				indexDir, searchGenerationArchiveReceiptName(receipt.Repository),
			))
			if err != nil || !bytes.Equal(nestedReceipt, flatReceipt) {
				return nil, errors.Join(err, errors.New("selected current receipt bytes differ"))
			}
			source, err := repositoryindex.ReadSourceManifest(indexDir, receipt.Repository)
			if err != nil {
				return nil, err
			}
			whole, err := ReadWholeManifest(indexDir, receipt.Repository, search.Revisions)
			if err != nil {
				return nil, err
			}
			prefix := filepath.Join(
				searchGenerationDirectoryName, repositoryEntry.Name(), generationEntry.Name(),
			)
			for _, name := range searchGenerationFiles(source, whole) {
				if err := os.Link(filepath.Join(indexDir, name), filepath.Join(directory, name)); err != nil {
					return nil, err
				}
				materialized[filepath.Join(prefix, name)] = true
			}
			if err := syncDirectory(directory); err != nil {
				return nil, err
			}
		}
	}
	return materialized, nil
}

func validArchiveHex(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func syncSelectedSearchDirectories(stage string, names []string) error {
	directories := map[string]bool{}
	prefix := searchGenerationDirectoryName + string(filepath.Separator)
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		for directory := filepath.Dir(filepath.Join(stage, name)); directory != stage; directory = filepath.Dir(directory) {
			directories[directory] = true
		}
	}
	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Count(ordered[i], string(filepath.Separator)) >
			strings.Count(ordered[j], string(filepath.Separator))
	})
	for _, directory := range ordered {
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func archiveControlExpectation(
	path string,
	destination any,
) (archiveExpectation, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() ||
		before.Size() < 0 || before.Size() > maxControlBytes {
		return archiveExpectation{}, errors.New(
			"focused archive control file is missing, special, or exceeds its limit",
		)
	}
	file, err := os.Open(path)
	if err != nil {
		return archiveExpectation{}, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !sameControlFileIdentity(before, opened) {
		_ = file.Close()
		return archiveExpectation{}, errors.New(
			"focused archive control file changed while opening",
		)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maxControlBytes+1))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if readErr != nil || afterErr != nil || closeErr != nil || currentErr != nil {
		return archiveExpectation{}, errors.Join(
			readErr, afterErr, closeErr, currentErr,
		)
	}
	if len(raw) > maxControlBytes || int64(len(raw)) != before.Size() ||
		!sameControlFileIdentity(before, after) ||
		!sameControlFileIdentity(after, current) {
		return archiveExpectation{}, errors.New(
			"focused archive control file changed while reading",
		)
	}
	if err := decodeJSONStrict(raw, destination); err != nil {
		return archiveExpectation{}, err
	}
	sum := sha256.Sum256(raw)
	return archiveExpectation{
		digest: "sha256:" + hex.EncodeToString(sum[:]),
		size:   int64(len(raw)),
	}, nil
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
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "phebs-search-") ||
			!strings.HasSuffix(entry.Name(), ".manifest.json") {
			continue
		}
		var envelope repositoryindex.SearchManifest
		if err := readControlFile(filepath.Join(indexDir, entry.Name()), &envelope); err != nil {
			return nil, nil, err
		}
		key := "search:" + envelope.Repository
		if entry.Name() != repositoryindex.SearchManifestName(envelope.Repository) ||
			publications[key] != "" {
			return nil, nil, errors.New(
				"repository-search manifest filename or repository is ambiguous",
			)
		}
		search, err := ValidateRepositorySearchGeneration(
			context.Background(), indexDir, envelope.Repository, envelope.Revisions,
		)
		if err != nil {
			return nil, nil, err
		}
		source, err := repositoryindex.ReadSourceManifest(indexDir, search.Repository)
		if err != nil {
			return nil, nil, err
		}
		whole, err := ReadWholeManifest(indexDir, search.Repository, search.Revisions)
		if err != nil {
			return nil, nil, err
		}
		publications[key] = search.Digest
		files[entry.Name()] = true
		files[repositoryindex.SourceManifestName(search.Repository)] = true
		files[WholeManifestName(search.Repository)] = true
		for _, member := range source.Members {
			files[member.Name] = true
		}
		for _, member := range whole.Members {
			files[member.Name] = true
		}
		receiptName := searchGenerationArchiveReceiptName(search.Repository)
		if _, statErr := os.Lstat(filepath.Join(indexDir, receiptName)); statErr == nil {
			if _, err := validateFlatSearchGenerationReceipt(
				indexDir, search.Repository, search,
			); err != nil {
				return nil, nil, err
			}
			files[receiptName] = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, nil, statErr
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	for _, entry := range entries {
		if isSearchArchiveArtifact(entry.Name()) && !files[entry.Name()] {
			return nil, nil, fmt.Errorf(
				"focused archive contains undeclared artifact %q", entry.Name(),
			)
		}
	}
	sort.Strings(names)
	return publications, names, nil
}

func safeArchiveName(name string) bool {
	if filepath.Base(name) != name {
		return safeSelectedSearchArchiveName(name)
	}
	return strings.HasPrefix(name, "phebs-focus-") ||
		strings.HasPrefix(name, "phebs-source-") ||
		strings.HasPrefix(name, "phebs-search-") ||
		strings.HasPrefix(name, "phebs-whole-")
}

func safeSelectedSearchArchiveName(name string) bool {
	parts := strings.Split(filepath.ToSlash(name), "/")
	if len(parts) != 4 || parts[0] != searchGenerationDirectoryName ||
		len(parts[1]) != sha256.Size*2 || len(parts[2]) != sha256.Size*2 ||
		strings.ToLower(parts[1]) != parts[1] || strings.ToLower(parts[2]) != parts[2] {
		return false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return false
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return false
	}
	return parts[3] == searchGenerationReceiptName ||
		(filepath.Base(parts[3]) == parts[3] && safeArchiveName(parts[3]))
}

func isSearchArchiveArtifact(name string) bool {
	// Search-generation lifecycle controls and immutable rollback directories
	// are live derived state, not backup pins. The archive remains a byte-exact
	// snapshot of the selected current publication and restores into the
	// backward-compatible flat layout; the next replacement adopts it.
	if strings.HasPrefix(name, "phebs-search-lifecycle-") ||
		strings.HasPrefix(name, "phebs-search-transition-") ||
		name == searchGenerationDirectoryName {
		return false
	}
	return safeArchiveName(name)
}
