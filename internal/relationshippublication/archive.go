package relationshippublication

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
)

const (
	MaxArchiveEntries   = 20_000_000
	MaxArchiveBytes     = int64(1 << 40)
	MaxArchivePathBytes = 512
)

type ArchiveReport struct {
	Publications int
	Files        int
	Bytes        int64
	Omitted      int
}

type archiveFile struct {
	path string
	name string
	size int64
}

type archiveNamespace struct {
	base    string
	current func(context.Context, string, string) ([]archiveFile, error)
}

type archiveComponentAuthority struct {
	resolverGeneration string
	resolverRoot       string
	rpcGeneration      string
	rpcRoot            string
	kafkaGeneration    string
	kafkaRoot          string
}

var archiveRoots = []string{
	"relationships",
	"relationship-resolver-namespaces",
	"relationship-rpc-postings",
	"relationship-kafka-postings",
}

// CreateArchive preserves only fully validated current relationship roots and
// their exact resolver/RPC/Kafka component generations. Corrupt or in-flight
// derived state is visibly omitted and never blocks the precious DB export.
func CreateArchive(ctx context.Context, dataDir, output string) (ArchiveReport, error) {
	var report ArchiveReport
	if !filepath.IsAbs(dataDir) || !filepath.IsAbs(output) {
		return report, invalidLifecycle("archive paths")
	}
	files := make(map[string]archiveFile)
	var plannedBytes int64
	for _, namespace := range archiveNamespaces(dataDir) {
		entries, err := boundedLifecycleDirectory(namespace.base, MaxLifecycleRepositories)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if errors.Is(err, ErrLimit) {
			report.Omitted += MaxLifecycleRepositories + 1
			continue
		} else if err != nil {
			return report, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
				report.Omitted++
				continue
			}
			publicationFiles, err := namespace.current(ctx, dataDir, entry.Name())
			if err != nil {
				report.Omitted++
				continue
			}
			for _, item := range publicationFiles {
				plannedBytes, err = admitArchiveFile(
					files, item, plannedBytes, MaxArchiveEntries, MaxArchiveBytes,
				)
				if err != nil {
					return report, err
				}
			}
			report.Publications++
		}
	}
	ordered := make([]archiveFile, 0, len(files))
	for _, item := range files {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].name < ordered[j].name })
	if len(ordered) > MaxArchiveEntries {
		return report, ErrLimit
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return report, err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return report, err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(output)
		}
	}()
	writer := tar.NewWriter(file)
	for _, item := range ordered {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return report, err
		}
		if item.size > MaxArchiveBytes-report.Bytes {
			_ = writer.Close()
			return report, ErrLimit
		}
		before, err := os.Lstat(item.path)
		if err != nil || !before.Mode().IsRegular() || before.Size() != item.size {
			_ = writer.Close()
			return report, invalidLifecycle("archive input changed")
		}
		source, err := os.Open(item.path)
		if err != nil {
			_ = writer.Close()
			return report, err
		}
		header := &tar.Header{
			Name: item.name, Mode: 0o600, Size: item.size,
			ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			_ = source.Close()
			_ = writer.Close()
			return report, err
		}
		written, copyErr := io.CopyN(writer, source, item.size)
		after, statErr := source.Stat()
		closeErr := source.Close()
		current, currentErr := os.Lstat(item.path)
		if copyErr != nil || statErr != nil || closeErr != nil || currentErr != nil ||
			written != item.size || !os.SameFile(before, after) || !os.SameFile(after, current) ||
			before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			_ = writer.Close()
			return report, errors.Join(copyErr, statErr, closeErr, currentErr, invalidLifecycle("archive input changed"))
		}
		report.Files++
		report.Bytes += item.size
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
	verified, err := VerifyArchive(ctx, output)
	if err != nil || verified.Publications != report.Publications ||
		verified.Files != report.Files || verified.Bytes != report.Bytes {
		return report, errors.Join(err, invalidLifecycle("created archive verification"))
	}
	complete = true
	return report, nil
}

func admitArchiveFile(
	files map[string]archiveFile,
	item archiveFile,
	totalBytes int64,
	maxEntries int,
	maxBytes int64,
) (int64, error) {
	if prior, duplicate := files[item.name]; duplicate {
		if prior.path != item.path || prior.size != item.size {
			return totalBytes, invalidLifecycle("archive path collision")
		}
		return totalBytes, nil
	}
	if maxEntries < 1 || len(files) >= maxEntries || item.size < 0 || maxBytes < 0 ||
		totalBytes < 0 || totalBytes > maxBytes || item.size > maxBytes-totalBytes {
		return totalBytes, ErrLimit
	}
	files[item.name] = item
	return totalBytes + item.size, nil
}

func archiveCurrentPublication(
	ctx context.Context, dataDir, repositoryHashValue string,
) ([]archiveFile, error) {
	root := filepath.Join(dataDir, "relationships")
	repositoryDirectory := filepath.Join(root, "relationship-publications", repositoryHashValue)
	if _, err := os.Lstat(filepath.Join(repositoryDirectory, UnavailableName)); err == nil {
		return nil, ErrNotFound
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(repositoryDirectory, "publishing.json")); err == nil {
		return nil, ErrPublishing
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pointerRaw, err := readRegular(filepath.Join(repositoryDirectory, "current.json"), MaxRootBytes)
	if err != nil {
		return nil, err
	}
	var pointer Pointer
	if decodeExact(pointerRaw, MaxRootBytes, &pointer) != nil || validatePointer(pointer) != nil ||
		repositoryHash(pointer.Repository) != repositoryHashValue {
		return nil, invalidLifecycle("archive relationship pointer")
	}
	publication, err := OpenCurrent(ctx, root, pointer.Repository)
	if err != nil {
		return nil, err
	}
	if _, err := openDirectoryComplete(ctx, publication.directory, publication.rootValue); err != nil {
		return nil, err
	}
	authority := publication.rootValue.Authority
	components := archiveComponentAuthority{
		resolverGeneration: authority.ResolverGenerationDigest,
		resolverRoot:       authority.ResolverRootDigest,
		rpcGeneration:      authority.RPCGenerationDigest,
		rpcRoot:            authority.RPCRootDigest,
		kafkaGeneration:    authority.KafkaGenerationDigest,
		kafkaRoot:          authority.KafkaRootDigest,
	}
	resolverRoot, resolverPointer, err := openArchivedResolver(
		ctx, dataDir, pointer.Repository, components,
	)
	if err != nil {
		return nil, err
	}
	if resolverRoot.Digest != components.resolverRoot {
		return nil, invalidLifecycle("archive resolver authority")
	}
	rpcRoot, err := openArchivedRPC(ctx, dataDir, pointer.Repository, components)
	if err != nil {
		return nil, err
	}
	kafkaRoot, err := openArchivedKafka(ctx, dataDir, pointer.Repository, components)
	if err != nil {
		return nil, err
	}
	var files []archiveFile
	files = append(files, archiveFile{
		path: filepath.Join(repositoryDirectory, "current.json"),
		name: filepath.ToSlash(filepath.Join("relationships", "relationship-publications", repositoryHashValue, "current.json")),
		size: int64(len(pointerRaw)),
	})
	for _, item := range []struct {
		root      string
		directory string
	}{
		{dataDir, publication.directory},
		{dataDir, resolverGenerationDirectory(dataDir, pointer.Repository, resolverRoot.GenerationDigest)},
		{dataDir, rpcGenerationDirectory(dataDir, pointer.Repository, rpcRoot.GenerationDigest)},
		{dataDir, kafkaGenerationDirectory(dataDir, pointer.Repository, kafkaRoot.GenerationDigest)},
	} {
		values, err := collectArchiveTree(item.root, item.directory)
		if err != nil {
			return nil, err
		}
		files = append(files, values...)
	}
	resolverCurrentPath := filepath.Join(
		dataDir, "relationship-resolver-namespaces", "resolver-namespaces",
		repositoryHashValue, "current.json",
	)
	files = append(files, archiveFile{
		path: resolverCurrentPath,
		name: filepath.ToSlash(filepath.Join(
			"relationship-resolver-namespaces", "resolver-namespaces",
			repositoryHashValue, "current.json",
		)),
		size: int64(len(resolverPointer)),
	})
	return files, nil
}

func archiveCurrentPublicationV3(
	ctx context.Context, dataDir, repositoryHashValue string,
) ([]archiveFile, error) {
	root := filepath.Join(dataDir, "relationships")
	repositoryDirectory := filepath.Join(ShadowBase(root), repositoryHashValue)
	if _, err := os.Lstat(filepath.Join(repositoryDirectory, "publishing.json")); err == nil {
		return nil, ErrPublishing
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pointerPath := filepath.Join(repositoryDirectory, "current.json")
	pointerRaw, err := readRegular(pointerPath, MaxRootBytesV3)
	if err != nil {
		return nil, err
	}
	var decoded PointerV3
	if decodeExact(pointerRaw, MaxRootBytesV3, &decoded) != nil ||
		repositoryHash(decoded.Repository) != repositoryHashValue {
		return nil, invalidLifecycle("archive relationship v3 pointer")
	}
	pointer, err := ReadPointerV3(ctx, root, decoded.Repository)
	if err != nil || pointer != decoded {
		return nil, errors.Join(err, invalidLifecycle("archive relationship v3 pointer"))
	}
	publication, err := ValidateGenerationV3(
		ctx, root, pointer.Repository, pointer.GenerationDigest, pointer.RootDigest,
	)
	if err != nil {
		return nil, err
	}
	rootValue := publication.Root()
	directory, err := GenerationPathV3(
		root, pointer.Repository, pointer.GenerationDigest,
	)
	if err != nil {
		return nil, err
	}
	names, err := GenerationFilesV3(rootValue)
	if err != nil {
		return nil, err
	}
	files := []archiveFile{{
		path: pointerPath,
		name: filepath.ToSlash(filepath.Join(
			"relationships", RelationshipPublicationsV3Shadow,
			repositoryHashValue, "current.json",
		)),
		size: int64(len(pointerRaw)),
	}}
	for _, name := range names {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.Join(err, invalidLifecycle("archive relationship v3 member"))
		}
		relative, err := filepath.Rel(dataDir, path)
		if err != nil || strings.HasPrefix(relative, "..") ||
			len(relative) > MaxArchivePathBytes {
			return nil, invalidLifecycle("archive relationship v3 path")
		}
		files = append(files, archiveFile{
			path: path, name: filepath.ToSlash(relative), size: info.Size(),
		})
	}
	authority := rootValue.Authority
	components := archiveComponentAuthority{
		resolverGeneration: authority.ResolverGenerationDigest,
		resolverRoot:       authority.ResolverRootDigest,
		rpcGeneration:      authority.RPCGenerationDigest,
		rpcRoot:            authority.RPCRootDigest,
		kafkaGeneration:    authority.KafkaGenerationDigest,
		kafkaRoot:          authority.KafkaRootDigest,
	}
	resolverRoot, err := openArchivedResolverGeneration(
		ctx, dataDir, pointer.Repository, components,
	)
	if err != nil || resolverRoot.Digest != components.resolverRoot {
		return nil, errors.Join(err, invalidLifecycle("archive v3 resolver authority"))
	}
	rpcRoot, err := openArchivedRPC(ctx, dataDir, pointer.Repository, components)
	if err != nil {
		return nil, err
	}
	kafkaRoot, err := openArchivedKafka(ctx, dataDir, pointer.Repository, components)
	if err != nil {
		return nil, err
	}
	for _, directory := range []string{
		resolverGenerationDirectory(dataDir, pointer.Repository, resolverRoot.GenerationDigest),
		rpcGenerationDirectory(dataDir, pointer.Repository, rpcRoot.GenerationDigest),
		kafkaGenerationDirectory(dataDir, pointer.Repository, kafkaRoot.GenerationDigest),
	} {
		values, err := collectArchiveTree(dataDir, directory)
		if err != nil {
			return nil, err
		}
		files = append(files, values...)
	}
	return files, nil
}

func openArchivedResolverGeneration(
	ctx context.Context, dataDir, repository string, authority archiveComponentAuthority,
) (resolvernamespace.Root, error) {
	root := filepath.Join(dataDir, "relationship-resolver-namespaces")
	directory := resolverGenerationDirectory(
		dataDir, repository, authority.resolverGeneration,
	)
	value, err := readComponentRoot[resolvernamespace.Root](
		filepath.Join(directory, "root.json"), resolvernamespace.ValidateRoot,
	)
	if err != nil || value.GenerationDigest != authority.resolverGeneration ||
		value.Digest != authority.resolverRoot {
		return value, errors.Join(err, invalidLifecycle("archive resolver authority"))
	}
	if _, err := resolvernamespace.Open(ctx, root, value); err != nil {
		return value, err
	}
	return value, nil
}

func openArchivedResolver(
	ctx context.Context, dataDir, repository string, authority archiveComponentAuthority,
) (resolvernamespace.Root, []byte, error) {
	root := filepath.Join(dataDir, "relationship-resolver-namespaces")
	base := filepath.Join(root, "resolver-namespaces", repositoryHash(repository))
	if _, err := os.Lstat(filepath.Join(base, "publishing.json")); err == nil {
		return resolvernamespace.Root{}, nil, ErrPublishing
	} else if !errors.Is(err, os.ErrNotExist) {
		return resolvernamespace.Root{}, nil, err
	}
	pointerRaw, err := readRegular(filepath.Join(base, "current.json"), MaxRootBytes)
	if err != nil {
		return resolvernamespace.Root{}, nil, err
	}
	current, err := resolvernamespace.OpenCurrent(ctx, root, repository)
	if err != nil {
		return resolvernamespace.Root{}, nil, err
	}
	value := current.Root()
	if value.GenerationDigest != authority.resolverGeneration ||
		value.Digest != authority.resolverRoot {
		return resolvernamespace.Root{}, nil, invalidLifecycle("archive resolver pointer")
	}
	if _, err := resolvernamespace.Open(ctx, root, value); err != nil {
		return resolvernamespace.Root{}, nil, err
	}
	return value, pointerRaw, nil
}

func openArchivedRPC(
	ctx context.Context, dataDir, repository string, authority archiveComponentAuthority,
) (rpccallerposting.Root, error) {
	root := filepath.Join(dataDir, "relationship-rpc-postings")
	value, err := readComponentRoot[rpccallerposting.Root](
		filepath.Join(rpcGenerationDirectory(dataDir, repository, authority.rpcGeneration), "root.json"),
		rpccallerposting.ValidateRoot,
	)
	if err != nil || value.GenerationDigest != authority.rpcGeneration ||
		value.Digest != authority.rpcRoot {
		return value, errors.Join(err, invalidLifecycle("archive RPC authority"))
	}
	publication, err := rpccallerposting.Open(ctx, root, value)
	if err != nil {
		return value, err
	}
	return value, publication.WalkPostings(ctx, func(rpccallerposting.Posting) error { return nil })
}

func openArchivedKafka(
	ctx context.Context, dataDir, repository string, authority archiveComponentAuthority,
) (kafkatopicposting.Root, error) {
	root := filepath.Join(dataDir, "relationship-kafka-postings")
	value, err := readComponentRoot[kafkatopicposting.Root](
		filepath.Join(kafkaGenerationDirectory(dataDir, repository, authority.kafkaGeneration), "root.json"),
		kafkatopicposting.ValidateRoot,
	)
	if err != nil || value.GenerationDigest != authority.kafkaGeneration ||
		value.Digest != authority.kafkaRoot {
		return value, errors.Join(err, invalidLifecycle("archive Kafka authority"))
	}
	publication, err := kafkatopicposting.Open(ctx, root, value)
	if err != nil {
		return value, err
	}
	return value, publication.WalkPostings(ctx, func(kafkatopicposting.Posting) error { return nil })
}

func readComponentRoot[T any](path string, validate func(T) error) (T, error) {
	var value T
	raw, err := readRegular(path, MaxRootBytes)
	if err != nil {
		return value, err
	}
	if decodeExact(raw, MaxRootBytes, &value) != nil || validate(value) != nil {
		return value, invalidLifecycle("archive component root")
	}
	canonical, err := jsonMarshal(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return value, invalidLifecycle("archive component root bytes")
	}
	return value, nil
}

func collectArchiveTree(root, directory string) ([]archiveFile, error) {
	var files []archiveFile
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 {
				return invalidLifecycle("archive symlink directory")
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return invalidLifecycle("archive special file")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(relative, "..") || len(relative) > MaxArchivePathBytes {
			return invalidLifecycle("archive path")
		}
		files = append(files, archiveFile{
			path: path, name: filepath.ToSlash(relative), size: info.Size(),
		})
		if len(files) > MaxArchiveEntries {
			return ErrLimit
		}
		return nil
	})
	return files, err
}

func resolverGenerationDirectory(dataDir, repository, generation string) string {
	return filepath.Join(
		dataDir, "relationship-resolver-namespaces", "resolver-namespaces",
		repositoryHash(repository), "generation-"+strings.TrimPrefix(generation, "sha256:"),
	)
}

func rpcGenerationDirectory(dataDir, repository, generation string) string {
	return filepath.Join(
		dataDir, "relationship-rpc-postings", "rpc-caller-postings",
		repositoryHash(repository), strings.TrimPrefix(generation, "sha256:"),
	)
}

func kafkaGenerationDirectory(dataDir, repository, generation string) string {
	return filepath.Join(
		dataDir, "relationship-kafka-postings", "kafka-topic-postings",
		repositoryHash(repository), strings.TrimPrefix(generation, "sha256:"),
	)
}

func VerifyArchive(ctx context.Context, archivePath string) (ArchiveReport, error) {
	var report ArchiveReport
	stage, err := os.MkdirTemp(filepath.Dir(archivePath), ".relationship-verify-")
	if err != nil {
		return report, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if report, err = extractArchive(ctx, archivePath, stage); err != nil {
		return report, err
	}
	publications, err := validateArchiveTree(ctx, stage)
	report.Publications = publications
	return report, err
}

func RestoreArchive(ctx context.Context, archivePath, dataDir string) error {
	if !filepath.IsAbs(archivePath) || !filepath.IsAbs(dataDir) {
		return invalidLifecycle("restore paths")
	}
	stage, err := os.MkdirTemp(filepath.Dir(dataDir), ".relationship-restore-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if _, err := extractArchive(ctx, archivePath, stage); err != nil {
		return err
	}
	if _, err := validateArchiveTree(ctx, stage); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	for _, name := range archiveRoots {
		source := filepath.Join(stage, name)
		if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		destination := filepath.Join(dataDir, name)
		if _, err := os.Lstat(destination); err == nil {
			return invalidLifecycle("restore destination exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(source, destination); err != nil {
			return err
		}
	}
	return syncDirectory(dataDir)
}

func extractArchive(ctx context.Context, archivePath, root string) (ArchiveReport, error) {
	var report ArchiveReport
	file, err := os.Open(archivePath)
	if err != nil {
		return report, err
	}
	defer func() { _ = file.Close() }()
	reader := tar.NewReader(file)
	prior := ""
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return report, err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		parts := strings.Split(filepath.ToSlash(clean), "/")
		if header.Typeflag != tar.TypeReg || header.Size < 0 ||
			len(header.Name) > MaxArchivePathBytes || header.Linkname != "" ||
			filepath.ToSlash(clean) != header.Name || archiveHeaderSparse(header) ||
			clean == "." || filepath.IsAbs(clean) ||
			strings.HasPrefix(clean, "..") || len(parts) < 2 ||
			!slices.Contains(archiveRoots, parts[0]) || header.Name <= prior ||
			header.Size > MaxArchiveBytes-report.Bytes || report.Files >= MaxArchiveEntries {
			return report, invalidLifecycle("archive entry")
		}
		prior = header.Name
		destination := filepath.Join(root, clean)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return report, err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return report, err
		}
		written, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return report, errors.Join(copyErr, closeErr, io.ErrShortWrite)
		}
		report.Files++
		report.Bytes += header.Size
	}
	return report, nil
}

func archiveHeaderSparse(header *tar.Header) bool {
	if header == nil {
		return true
	}
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") || key == "SCHILY.realsize" {
			return true
		}
	}
	return false
}

func validateArchiveTree(ctx context.Context, dataDir string) (int, error) {
	publications := 0
	for _, namespace := range archiveNamespaces(dataDir) {
		entries, err := boundedLifecycleDirectory(namespace.base, MaxLifecycleRepositories)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
				return 0, invalidLifecycle("archive repository inventory")
			}
			if _, err := namespace.current(ctx, dataDir, entry.Name()); err != nil {
				return 0, err
			}
			publications++
		}
	}
	return publications, nil
}

func archiveNamespaces(dataDir string) []archiveNamespace {
	root := filepath.Join(dataDir, "relationships")
	return []archiveNamespace{
		{
			base:    filepath.Join(root, "relationship-publications"),
			current: archiveCurrentPublication,
		},
		{base: ShadowBase(root), current: archiveCurrentPublicationV3},
	}
}
