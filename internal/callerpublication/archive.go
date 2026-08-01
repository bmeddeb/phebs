package callerpublication

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

const (
	MaxOmissionDetails = 64
	maxArchiveEntries  = 65_536
	// The live installation cap accounts for leaf bytes. Keep independent
	// headroom for complete manifests, tar headers, and padding.
	MaxArchiveBytes     = int64(4 << 40)
	maxArchiveNameBytes = 512
)

type Omission struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ArchiveReport is a bounded receipt for derived state observed during
// backup. Counts remain exact; Details retains only the first fixed set.
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

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(raw []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(raw)
}

type archiveSource struct {
	reference      ArtifactRef
	expectedInfo   os.FileInfo
	expectedSize   int64
	expectedDigest string
}

func (output *boundedArchiveOutput) Write(raw []byte) (int, error) {
	if int64(len(raw)) > output.remaining {
		return 0, callerleaf.ErrLimit
	}
	written, err := output.destination.Write(raw)
	output.remaining -= int64(written)
	output.written += int64(written)
	if err == nil && written != len(raw) {
		err = io.ErrShortWrite
	}
	return written, err
}

func reserveArchiveEntry(entries *int, logicalBytes *int64, size int64) error {
	if entries == nil || logicalBytes == nil || size < 0 ||
		*entries >= maxArchiveEntries ||
		*logicalBytes > MaxArchiveBytes-size {
		return callerleaf.ErrLimit
	}
	(*entries)++
	*logicalBytes += size
	return nil
}

func archiveEntryLimit(name string) int64 {
	if strings.HasSuffix(name, ".manifest.json") {
		return int64(MaxManifestBytes)
	}
	return callerleaf.MaxCanonicalBytesPerPair
}

func validateArchiveHeader(
	header *tar.Header,
	seen map[string]bool,
	total, physicalSize int64,
) error {
	name := header.Name
	unsafe := func() error {
		return fmt.Errorf("caller publication archive entry %q is unsafe", name)
	}
	components := strings.Split(name, "/")
	if len(components) != 2 ||
		!ValidRepositoryDirectoryName(components[0]) ||
		(!callerpublicationid.ValidManifestName(components[1]) &&
			!ValidLeafArtifactName(components[1])) ||
		header.Typeflag != tar.TypeReg ||
		(header.Format != tar.FormatUSTAR && header.Format != tar.FormatPAX) ||
		name == "" || len(name) > maxArchiveNameBytes || path.Clean(name) != name ||
		seen[name] || header.Linkname != "" || header.Mode != 0o600 ||
		header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" ||
		header.Devmajor != 0 || header.Devminor != 0 ||
		!header.ModTime.Equal(time.Unix(0, 0)) ||
		!header.AccessTime.IsZero() || !header.ChangeTime.IsZero() ||
		header.Size < 0 || header.Size > archiveEntryLimit(name) ||
		total > MaxArchiveBytes-header.Size || total > physicalSize-header.Size {
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

func scanArchive(
	ctx context.Context,
	source *os.File,
	physicalSize int64,
	destination *directoryAuthority,
	destinationPath string,
	visit func(*tar.Header, io.Reader) error,
) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("caller publication archive context is required")
	}
	if destination != nil && destination.root == nil {
		return nil, errors.New("caller publication restore stage is unavailable")
	}
	if destination != nil && visit != nil {
		return nil, errors.New("caller publication archive scan mode is invalid")
	}
	limited := &io.LimitedReader{
		R: contextReader{ctx: ctx, reader: source}, N: physicalSize,
	}
	reader := tar.NewReader(limited)
	extracted := make([]string, 0)
	seen := make(map[string]bool)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			if limited.N != 0 {
				return nil, errors.New("caller publication archive contains trailing data after its end marker")
			}
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read caller publication archive: %w", nextErr)
		}
		if err := validateArchiveHeader(header, seen, total, physicalSize); err != nil {
			return nil, err
		}
		if len(extracted) == maxArchiveEntries {
			return nil, errors.New("caller publication archive contains too many entries")
		}
		seen[header.Name] = true
		total += header.Size
		if destination == nil {
			entry := &io.LimitedReader{R: reader, N: header.Size}
			var entryErr error
			if visit == nil {
				_, entryErr = io.Copy(io.Discard, entry)
			} else {
				entryErr = visit(header, entry)
			}
			if entryErr != nil {
				return nil, fmt.Errorf(
					"verify caller publication archive entry %q: %w",
					header.Name, entryErr,
				)
			}
			if entry.N != 0 {
				return nil, fmt.Errorf(
					"read caller publication archive entry %q: truncated content",
					header.Name,
				)
			}
		} else {
			components := strings.Split(header.Name, "/")
			if err := destination.root.Mkdir(components[0], 0o700); err != nil &&
				!errors.Is(err, os.ErrExist) {
				return nil, err
			}
			if testAfterArchiveRepositoryMkdir != nil {
				testAfterArchiveRepositoryMkdir(
					filepath.Join(destinationPath, components[0]),
				)
			}
			directory, err := openChildDirectoryAuthority(
				destination, components[0], nil,
			)
			if err != nil {
				return nil, err
			}
			output, err := directory.root.OpenFile(
				components[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
			)
			if err != nil {
				directory.close()
				return nil, err
			}
			written, copyErr := io.CopyN(output, reader, header.Size)
			syncErr := output.Sync()
			closeErr := output.Close()
			rootCloseErr := directory.root.Close()
			if copyErr != nil || written != header.Size || syncErr != nil ||
				closeErr != nil || rootCloseErr != nil {
				return nil, errors.Join(copyErr, syncErr, closeErr, rootCloseErr)
			}
		}
		extracted = append(extracted, header.Name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.Sort(extracted)
	return extracted, nil
}

type archiveStreamPublication struct {
	directory string
	manifest  Manifest
	pairs     map[string]PairReceipt
	lastName  string
}

type archiveStreamVerifier struct {
	ctx             context.Context
	lastRepository  string
	current         *archiveStreamPublication
	publications    int
	publicationRefs int
	canonicalBytes  int64
}

func (verifier *archiveStreamVerifier) finishPublication() error {
	if verifier.current == nil {
		return nil
	}
	if len(verifier.current.pairs) != 0 {
		return errors.New("caller publication archive omits referenced leaf artifacts")
	}
	verifier.current = nil
	return nil
}

func (verifier *archiveStreamVerifier) visit(
	header *tar.Header,
	reader io.Reader,
) error {
	if err := verifier.ctx.Err(); err != nil {
		return err
	}
	if header == nil || reader == nil {
		return errors.New("caller publication archive entries are not canonical")
	}
	if testBeforeArchiveVerifyEntry != nil {
		testBeforeArchiveVerifyEntry(header.Name)
	}
	if err := verifier.ctx.Err(); err != nil {
		return err
	}
	components := strings.Split(header.Name, "/")
	repositoryDirectory, name := components[0], components[1]
	if callerpublicationid.ValidManifestName(name) {
		if err := verifier.finishPublication(); err != nil {
			return err
		}
		raw, err := io.ReadAll(reader)
		if err != nil || int64(len(raw)) != header.Size {
			return errors.New("caller publication archive manifest is truncated")
		}
		manifest, err := decodeManifest(raw)
		if err != nil {
			return fmt.Errorf("decode caller publication archive manifest: %w", err)
		}
		if callerpublicationid.RepositoryDirectory(
			manifest.Generation.Repository,
		) != repositoryDirectory || manifest.State().Manifest != name {
			return errors.New("caller publication archive manifest owner differs")
		}
		if verifier.lastRepository != "" &&
			manifest.Generation.Repository <= verifier.lastRepository {
			return errors.New("caller publication archive repository order is not canonical")
		}
		publicationRefs := len(manifest.Pairs) + 1
		if publicationRefs >
			callerpublicationid.InstallationPublicationRefs-verifier.publicationRefs ||
			manifest.Aggregate.CanonicalBytes >
				callerpublicationid.InstallationCanonicalBytes-verifier.canonicalBytes ||
			verifier.publications >=
				callerpublicationid.InstallationPublicationRepositories {
			return callerleaf.ErrLimit
		}
		pairs := make(map[string]PairReceipt, len(manifest.Pairs))
		for index, pair := range manifest.Pairs {
			if index%256 == 0 {
				if err := verifier.ctx.Err(); err != nil {
					return err
				}
			}
			if _, duplicate := pairs[pair.Receipt.Name]; duplicate {
				return errors.New("caller publication archive manifest repeats a leaf")
			}
			pairs[pair.Receipt.Name] = pair
		}
		verifier.lastRepository = manifest.Generation.Repository
		verifier.current = &archiveStreamPublication{
			directory: repositoryDirectory, manifest: manifest, pairs: pairs,
			lastName: name,
		}
		verifier.publications++
		verifier.publicationRefs += publicationRefs
		verifier.canonicalBytes += manifest.Aggregate.CanonicalBytes
		return nil
	}
	if verifier.current == nil || verifier.current.directory != repositoryDirectory {
		return errors.New("caller publication archive leaf precedes its manifest")
	}
	if name <= verifier.current.lastName {
		return errors.New("caller publication archive member order is not canonical")
	}
	pair, ok := verifier.current.pairs[name]
	if !ok || header.Size != pair.Receipt.ContentBytes {
		return errors.New("caller publication archive leaf is unreferenced")
	}
	if err := callerleaf.VerifyReader(
		verifier.ctx, reader, verifier.current.manifest.Generation,
		pair.Pair, pair.Receipt, nil,
	); err != nil {
		return err
	}
	delete(verifier.current.pairs, name)
	verifier.current.lastName = name
	return nil
}

func verifyArchiveStream(
	ctx context.Context,
	source *os.File,
	physicalSize int64,
) ([]string, ArchiveReport, error) {
	verifier := archiveStreamVerifier{ctx: ctx}
	names, err := scanArchive(
		ctx, source, physicalSize, nil, "", verifier.visit,
	)
	if err != nil {
		return nil, ArchiveReport{}, err
	}
	if err := verifier.finishPublication(); err != nil {
		return nil, ArchiveReport{}, err
	}
	return names, ArchiveReport{Publications: verifier.publications}, nil
}

// CreateArchiveWithReport writes a deterministic archive containing every and
// only strictly valid, marker-free complete caller publication.
func CreateArchiveWithReport(root, output string) (ArchiveReport, error) {
	return CreateArchiveWithReportContext(context.Background(), root, output)
}

// CreateArchiveWithReportContext is the cancellable backup boundary used by
// recovery. Every source read remains beneath the exact repository directory
// descriptor admitted by Discover; a path replacement can invalidate the
// backup, but cannot redirect its reads.
func CreateArchiveWithReportContext(
	ctx context.Context,
	root, output string,
) (ArchiveReport, error) {
	var report ArchiveReport
	if ctx == nil {
		return report, errors.New("caller publication archive context is required")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
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
	boundedOutput := &boundedArchiveOutput{destination: file, remaining: MaxArchiveBytes}
	writer := tar.NewWriter(boundedOutput)
	publications, observed, err := Discover(ctx, root)
	report.OmittedPublications = observed.OmittedPublications
	report.OmittedArtifacts = observed.OmittedArtifacts
	report.StaleMarkers = observed.StaleMarkers
	report.Details = slices.Clone(observed.Details)
	report.TruncatedDetails = observed.TruncatedDetails
	if err != nil {
		_ = writer.Close()
		return report, err
	}
	archived := make(map[string]struct{})
	entries := 0
	var logicalBytes int64
	for _, publication := range publications {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return report, err
		}
		if publication == nil {
			_ = writer.Close()
			return report, errors.New("caller publication discovery returned nil")
		}
		if err := appendStablePublication(
			ctx, writer, root, publication, archived,
			&entries, &logicalBytes,
		); err != nil {
			_ = writer.Close()
			return report, err
		}
		report.Publications++
	}
	if err := ctx.Err(); err != nil {
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
		archiveInfo.Size() > MaxArchiveBytes {
		return report, errors.New("caller publication archive exceeds its physical limit")
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
		return errors.New("caller publication archive root is not a real directory")
	}
	return nil
}

func appendStablePublication(
	ctx context.Context,
	writer *tar.Writer,
	root string,
	publication *Publication,
	archived map[string]struct{},
	entries *int,
	logicalBytes *int64,
) error {
	if publication == nil || archived == nil {
		return errors.New("caller publication archive source is invalid")
	}
	repository := publication.state.Generation.Repository
	repositoryDirectory := callerpublicationid.RepositoryDirectory(repository)
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if err != nil {
		return fmt.Errorf("open caller publication archive source: %w", err)
	}
	defer authority.close()
	if !sameDirectory(publication.directoryInfo, authority.info) {
		return errors.New("caller publication archive source directory changed")
	}
	if testAfterArchiveSourceAuthority != nil {
		testAfterArchiveSourceAuthority(filepath.Join(root, repositoryDirectory))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkPublishingAt(
		authority, markerForState(publication.state), false,
	); err != nil {
		return err
	}
	sources, err := publicationArchiveSources(publication)
	if err != nil {
		return err
	}
	copied := make(map[string]os.FileInfo, len(sources))
	for _, source := range sources {
		name := source.reference.RelativePath()
		if _, duplicate := archived[name]; duplicate {
			continue
		}
		info, appendErr := appendStableTarFileAt(
			ctx, writer, authority, source, entries, logicalBytes,
		)
		if appendErr != nil {
			return fmt.Errorf("archive caller publication %q: %w", name, appendErr)
		}
		copied[source.reference.Name] = info
		archived[name] = struct{}{}
	}
	if testAfterArchivePublicationCopy != nil {
		testAfterArchivePublicationCopy(filepath.Join(root, repositoryDirectory))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkPublishingAt(
		authority, markerForState(publication.state), false,
	); err != nil {
		return err
	}
	for name, expected := range copied {
		current, statErr := authority.root.Lstat(name)
		if statErr != nil || !sameFile(expected, current) {
			return errors.New("caller publication archive source changed after copy")
		}
	}
	if !publication.Current() {
		return errors.New("caller publication changed during backup")
	}
	return nil
}

func publicationArchiveSources(publication *Publication) ([]archiveSource, error) {
	if publication == nil || ValidateManifest(publication.manifest) != nil {
		return nil, errors.New("caller publication archive source is invalid")
	}
	repositoryDirectory := callerpublicationid.RepositoryDirectory(
		publication.state.Generation.Repository,
	)
	receipts := make(map[string]callerleaf.Receipt, len(publication.manifest.Pairs))
	for _, pair := range publication.manifest.Pairs {
		if _, duplicate := receipts[pair.Receipt.Name]; duplicate {
			return nil, errors.New("caller publication archive source is duplicated")
		}
		receipts[pair.Receipt.Name] = pair.Receipt
	}
	references := publication.ArtifactRefs()
	sources := make([]archiveSource, len(references))
	for index, reference := range references {
		source := archiveSource{reference: reference}
		switch reference.Name {
		case publication.state.Manifest:
			source.expectedInfo = publication.manifestInfo
			if source.expectedInfo == nil {
				return nil, errors.New("caller publication archive manifest identity is missing")
			}
			source.expectedSize = source.expectedInfo.Size()
		default:
			receipt, ok := receipts[reference.Name]
			if !ok {
				return nil, errors.New("caller publication archive leaf identity is missing")
			}
			source.expectedSize = receipt.ContentBytes
			source.expectedDigest = receipt.ContentDigest
		}
		if reference.RepositoryDirectory != repositoryDirectory {
			return nil, errors.New("caller publication archive owner is not canonical")
		}
		sources[index] = source
	}
	return sources, nil
}

func appendStableTarFileAt(
	ctx context.Context,
	writer *tar.Writer,
	authority *directoryAuthority,
	sourceRef archiveSource,
	entries *int,
	logicalBytes *int64,
) (os.FileInfo, error) {
	ref := sourceRef.reference
	name := ref.RelativePath()
	components := strings.Split(name, "/")
	if len(components) != 2 || components[0] != ref.RepositoryDirectory ||
		components[1] != ref.Name {
		return nil, errors.New("caller publication artifact reference is not canonical")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := authority.root.Lstat(ref.Name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > archiveEntryLimit(name) ||
		info.Size() != sourceRef.expectedSize ||
		sourceRef.expectedInfo != nil && !sameFile(sourceRef.expectedInfo, info) {
		return nil, errors.New("archive source is not the admitted bounded regular file")
	}
	if err := reserveArchiveEntry(entries, logicalBytes, info.Size()); err != nil {
		return nil, err
	}
	source, err := authority.root.Open(ref.Name)
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = source.Close()
		}
	}()
	opened, err := source.Stat()
	if err != nil || !sameArchiveFileIdentity(info, opened) {
		return nil, errors.New("caller publication archive source changed while opening")
	}
	header := &tar.Header{
		Name: name, Mode: 0o600, Size: info.Size(),
		ModTime: time.Unix(0, 0).UTC(), Typeflag: tar.TypeReg,
		Format: tar.FormatPAX,
	}
	if err := writer.WriteHeader(header); err != nil {
		return nil, err
	}
	if testBeforeArchiveSourceCopy != nil {
		testBeforeArchiveSourceCopy(name)
	}
	reader := io.Reader(contextReader{ctx: ctx, reader: source})
	var hash = sha256.New()
	if sourceRef.expectedDigest != "" {
		reader = io.TeeReader(reader, hash)
	}
	if _, err := io.CopyN(writer, reader, info.Size()); err != nil {
		return nil, err
	}
	if sourceRef.expectedDigest != "" && "sha256:"+
		hex.EncodeToString(hash.Sum(nil)) != sourceRef.expectedDigest {
		return nil, errors.New("caller publication archive source digest differs")
	}
	after, statErr := source.Stat()
	current, lstatErr := authority.root.Lstat(ref.Name)
	if statErr != nil || lstatErr != nil ||
		!sameArchiveFileIdentity(opened, after) ||
		!sameArchiveFileIdentity(after, current) {
		return nil, errors.New("caller publication archive source changed while reading")
	}
	closeErr := source.Close()
	closed = true
	if closeErr != nil {
		return nil, closeErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return current, nil
}

func openArchiveSource(archivePath string) (*os.File, os.FileInfo, error) {
	pathInfo, err := os.Lstat(archivePath)
	if err != nil || !pathInfo.Mode().IsRegular() ||
		pathInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("caller publication archive is missing or special")
	}
	source, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, err
	}
	archiveInfo, err := source.Stat()
	if err != nil || !archiveInfo.Mode().IsRegular() ||
		!sameArchiveFileIdentity(pathInfo, archiveInfo) ||
		archiveInfo.Size() <= 0 || archiveInfo.Size() > MaxArchiveBytes {
		_ = source.Close()
		return nil, nil, errors.New(
			"caller publication archive is missing, special, empty, or exceeds its limit",
		)
	}
	return source, archiveInfo, nil
}

// RestoreArchive validates every header and complete publication in a private
// stage, then renames the complete filesystem set into an absent target.
func RestoreArchive(archivePath, target string) error {
	return RestoreArchiveContext(context.Background(), archivePath, target)
}

// RestoreArchiveContext is the cancellable restore boundary. Cancellation can
// stop both the streaming semantic preflight and final staged extraction.
func RestoreArchiveContext(ctx context.Context, archivePath, target string) error {
	if ctx == nil {
		return errors.New("caller publication restore context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return errors.New("caller publication restore target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source, archiveInfo, err := openArchiveSource(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	preflight, _, err := verifyArchiveStream(ctx, source, archiveInfo.Size())
	if err != nil {
		return err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	parentPath := filepath.Dir(target)
	parentAuthority, err := openStableDirectoryRoot(parentPath)
	if err != nil {
		return fmt.Errorf("open caller publication restore parent: %w", err)
	}
	defer parentAuthority.close()
	targetName := filepath.Base(target)
	if _, err := parentAuthority.root.Lstat(targetName); err == nil {
		return errors.New("caller publication restore target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(parentPath, ".phebs-caller-publication-restore-")
	if err != nil {
		return err
	}
	stageName := filepath.Base(stage)
	stageInfo, err := parentAuthority.root.Lstat(stageName)
	if err != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		_ = os.RemoveAll(stage)
		return errors.New("caller publication restore stage changed during creation")
	}
	stageAuthority, err := openChildDirectoryAuthority(
		parentAuthority, stageName, stageInfo,
	)
	if err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("open caller publication restore stage: %w", err)
	}
	defer stageAuthority.close()
	published := false
	cleanupName := stageName
	defer func() {
		if !published {
			current, currentErr := parentAuthority.root.Lstat(cleanupName)
			if currentErr == nil && sameDirectory(stageInfo, current) {
				_ = parentAuthority.root.RemoveAll(cleanupName)
				_ = syncRootDirectory(parentAuthority.root, ".")
			}
		}
	}()
	extracted, err := scanArchive(
		ctx, source, archiveInfo.Size(), stageAuthority, stage, nil,
	)
	if err != nil {
		return err
	}
	if !slices.Equal(preflight, extracted) {
		return errors.New("caller publication archive changed between validation passes")
	}
	current, err := os.Lstat(archivePath)
	if err != nil || !sameArchiveFileIdentity(archiveInfo, current) {
		return errors.New("caller publication archive changed while it was restored")
	}
	if err := validateRestoredArchive(ctx, stage, extracted); err != nil {
		return err
	}
	if err := syncExtractedDirectories(ctx, stageAuthority, extracted); err != nil {
		return err
	}
	if testBeforeArchiveStageRename != nil {
		testBeforeArchiveStageRename(stage)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	currentStage, err := parentAuthority.root.Lstat(stageName)
	if err != nil || !sameDirectory(stageInfo, currentStage) {
		return errors.New("caller publication restore stage changed before installation")
	}
	if _, err := parentAuthority.root.Lstat(targetName); err == nil {
		return errors.New("caller publication restore target appeared before installation")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := parentAuthority.root.Rename(stageName, targetName); err != nil {
		return err
	}
	cleanupName = targetName
	if err := syncRootDirectory(parentAuthority.root, "."); err != nil {
		return err
	}
	if testAfterArchiveStageRename != nil {
		testAfterArchiveStageRename(target)
	}
	installed, err := parentAuthority.root.Lstat(targetName)
	if err != nil || !sameDirectory(stageInfo, installed) {
		return errors.New("caller publication restore target changed during installation")
	}
	currentParent, err := os.Lstat(parentPath)
	if err != nil || !sameDirectory(parentAuthority.info, currentParent) {
		return errors.New("caller publication restore parent changed during installation")
	}
	if err := validateRestoredArchive(ctx, target, extracted); err != nil {
		return fmt.Errorf("validate installed caller publication archive: %w", err)
	}
	currentParent, err = os.Lstat(parentPath)
	if err != nil || !sameDirectory(parentAuthority.info, currentParent) {
		return errors.New("caller publication restore parent changed during validation")
	}
	installed, err = parentAuthority.root.Lstat(targetName)
	if err != nil || !sameDirectory(stageInfo, installed) {
		return errors.New("caller publication restore target changed after validation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	published = true
	return nil
}

func validateRestoredArchive(
	ctx context.Context,
	root string,
	extracted []string,
) error {
	publications, report, err := Discover(ctx, root)
	if err != nil {
		return err
	}
	if report.OmittedPublications != 0 || report.OmittedArtifacts != 0 ||
		report.StaleMarkers != 0 {
		return errors.New("caller publication archive contains invalid or unreferenced state")
	}
	referenced := make(map[string]struct{})
	for _, publication := range publications {
		for _, ref := range publication.ArtifactRefs() {
			referenced[ref.RelativePath()] = struct{}{}
		}
	}
	if len(referenced) != len(extracted) {
		return errors.New("caller publication archive contains an unreferenced artifact")
	}
	for _, name := range extracted {
		if _, ok := referenced[name]; !ok {
			return errors.New("caller publication archive contains an unreferenced artifact")
		}
	}
	return nil
}

func syncExtractedDirectories(
	ctx context.Context,
	authority *directoryAuthority,
	names []string,
) error {
	if ctx == nil {
		return errors.New("caller publication restore context is required")
	}
	if authority == nil || authority.root == nil {
		return errors.New("caller publication restore stage is unavailable")
	}
	seen := make(map[string]struct{})
	for index, name := range names {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		parent := strings.Split(name, "/")[0]
		if _, duplicate := seen[parent]; duplicate {
			continue
		}
		seen[parent] = struct{}{}
		directory, err := openChildDirectoryAuthority(authority, parent, nil)
		if err != nil {
			return err
		}
		err = syncRootDirectory(directory.root, ".")
		directory.close()
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func sameArchiveFileIdentity(left, right os.FileInfo) bool {
	return left != nil && right != nil &&
		left.Mode().IsRegular() && right.Mode().IsRegular() &&
		left.Mode() == right.Mode() && os.SameFile(left, right) &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

var (
	testAfterArchiveRepositoryMkdir func(string)
	testAfterArchiveSourceAuthority func(string)
	testAfterArchivePublicationCopy func(string)
	testBeforeArchiveSourceCopy     func(string)
	testBeforeArchiveVerifyEntry    func(string)
	testBeforeArchiveStageRename    func(string)
	testAfterArchiveStageRename     func(string)
)

// VerifyArchiveWithReport streams the archive through the exact manifest and
// leaf validators without materializing a second publication tree.
func VerifyArchiveWithReport(archivePath string) (ArchiveReport, error) {
	return VerifyArchiveWithReportContext(context.Background(), archivePath)
}

// VerifyArchiveWithReportContext is the cancellable, O(one publication)
// memory and zero-scratch-disk archive verification boundary.
func VerifyArchiveWithReportContext(
	ctx context.Context,
	archivePath string,
) (ArchiveReport, error) {
	if ctx == nil {
		return ArchiveReport{}, errors.New("caller publication verify context is required")
	}
	if err := ctx.Err(); err != nil {
		return ArchiveReport{}, err
	}
	source, archiveInfo, err := openArchiveSource(archivePath)
	if err != nil {
		return ArchiveReport{}, err
	}
	defer func() { _ = source.Close() }()
	_, report, err := verifyArchiveStream(ctx, source, archiveInfo.Size())
	if err != nil {
		return ArchiveReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArchiveReport{}, err
	}
	current, err := os.Lstat(archivePath)
	if err != nil || !sameArchiveFileIdentity(archiveInfo, current) {
		return ArchiveReport{}, errors.New(
			"caller publication archive changed while it was verified",
		)
	}
	if err := ctx.Err(); err != nil {
		return ArchiveReport{}, err
	}
	return report, nil
}
