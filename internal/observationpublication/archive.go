package observationpublication

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MaxArchiveEntries   = 10_000_000
	MaxArchiveBytes     = int64(1 << 40)
	MaxArchivePathBytes = 512
)

type ArchiveReport struct {
	Publications        int
	V1Publications      int
	V2Publications      int
	Files               int
	Bytes               int64
	Omitted             int
	OmittedPublications int
	OmittedArtifacts    int
	StaleMarkers        int
}

type archiveFile struct {
	path string
	name string
	size int64
}

// CreateArchive includes only fully validated current publications. A corrupt
// or marker-covered derived generation is omitted rather than blocking backup
// of precious state.
func CreateArchive(ctx context.Context, root, output string) (ArchiveReport, error) {
	var report ArchiveReport
	if !filepath.IsAbs(root) || !filepath.IsAbs(output) {
		return report, invalid("archive paths must be absolute")
	}
	entries, err := boundedDirectory(root, MaxLifecycleRepositories)
	if errors.Is(err, os.ErrNotExist) {
		entries = nil
	} else if errors.Is(err, ErrLimit) {
		report.OmittedArtifacts = MaxLifecycleRepositories + 1
		entries = nil
	} else if err != nil {
		return report, err
	}
	var files []archiveFile
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if !entry.IsDir() || len(entry.Name()) != 64 || !validLowerHex(entry.Name()) {
			report.OmittedArtifacts++
			continue
		}
		repositoryRoot := filepath.Join(root, entry.Name())
		pointerRaw, pointerErr := readBoundedRegular(filepath.Join(repositoryRoot, "current.json"), MaxManifestBytes)
		if pointerErr == nil {
			var pointer Pointer
			if decodeCanonical(pointerRaw, &pointer) != nil || repositoryHash(pointer.Repository) != entry.Name() {
				report.OmittedPublications++
			} else if _, err := Open(ctx, root, pointer.Repository); err != nil {
				report.OmittedPublications++
			} else {
				generationRoot := generationDirectory(root, pointer.Repository, pointer.GenerationDigest)
				publicationFiles, err := collectArchiveFiles(root, generationRoot)
				if err != nil {
					return report, err
				}
				files = append(files, archiveFile{path: filepath.Join(repositoryRoot, "current.json"), name: filepath.ToSlash(filepath.Join(entry.Name(), "current.json")), size: int64(len(pointerRaw))})
				files = append(files, publicationFiles...)
				report.Publications++
				report.V1Publications++
			}
		} else if !errors.Is(pointerErr, os.ErrNotExist) {
			report.OmittedPublications++
		}

		v2Files, v2Publications, v2OmittedPublications, v2OmittedArtifacts, staleMarkers, err :=
			collectInventoryArchiveFilesV2(ctx, root, entry.Name())
		if err != nil {
			return report, err
		}
		files = append(files, v2Files...)
		report.Publications += v2Publications
		report.V2Publications += v2Publications
		report.OmittedPublications += v2OmittedPublications
		report.OmittedArtifacts += v2OmittedArtifacts
		report.StaleMarkers += staleMarkers
	}
	report.Omitted = report.OmittedPublications + report.OmittedArtifacts
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	if len(files) > MaxArchiveEntries {
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
	for _, item := range files {
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
			return report, invalid("archive input changed")
		}
		source, err := os.Open(item.path)
		if err != nil {
			_ = writer.Close()
			return report, err
		}
		opened, err := source.Stat()
		if err != nil || !opened.Mode().IsRegular() || opened.Size() != item.size || !os.SameFile(before, opened) {
			_ = source.Close()
			_ = writer.Close()
			return report, invalid("archive input changed")
		}
		header := &tar.Header{Name: item.name, Mode: 0o600, Size: item.size, ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := writer.WriteHeader(header); err != nil {
			_ = source.Close()
			_ = writer.Close()
			return report, err
		}
		written, copyErr := io.CopyN(writer, source, item.size)
		after, statErr := source.Stat()
		closeErr := source.Close()
		current, currentErr := os.Lstat(item.path)
		if copyErr != nil || statErr != nil || closeErr != nil || currentErr != nil || written != item.size ||
			!os.SameFile(opened, after) || !os.SameFile(after, current) ||
			after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) ||
			current.Size() != after.Size() || !current.ModTime().Equal(after.ModTime()) {
			_ = writer.Close()
			var shortErr error
			if written != item.size {
				shortErr = io.ErrShortWrite
			}
			return report, errors.Join(copyErr, statErr, closeErr, currentErr, shortErr, invalid("archive input changed"))
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
		verified.V1Publications != report.V1Publications || verified.V2Publications != report.V2Publications ||
		verified.Files != report.Files || verified.Bytes != report.Bytes {
		return report, errors.Join(err, invalid("created archive verification"))
	}
	complete = true
	return report, nil
}

func collectArchiveFiles(root, generationRoot string) ([]archiveFile, error) {
	var files []archiveFile
	err := filepath.WalkDir(generationRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return invalid("archive artifact is special")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(relative, "..") {
			return invalid("archive artifact escapes root")
		}
		files = append(files, archiveFile{path: path, name: filepath.ToSlash(relative), size: info.Size()})
		if len(files) > MaxArchiveEntries {
			return ErrLimit
		}
		return nil
	})
	return files, err
}

func collectInventoryArchiveFilesV2(
	ctx context.Context, root, repositoryHashValue string,
) ([]archiveFile, int, int, int, int, error) {
	directory := filepath.Join(root, repositoryHashValue, InventoryPublicationDirectoryV2)
	entries, err := boundedDirectory(directory, MaxInventoryPublicationEntriesV2)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, 0, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, 0, 0, err
	}
	raw, err := readBoundedRegular(filepath.Join(directory, InventoryPublicationRootNameV2), MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		omittedArtifacts := len(entries)
		staleMarkers := 0
		for _, entry := range entries {
			if entry.Name() == InventoryPublicationMarkerNameV2 {
				staleMarkers++
			}
		}
		return nil, 0, 0, omittedArtifacts, staleMarkers, nil
	}
	if err != nil {
		return nil, 0, 1, 0, 0, nil
	}
	var publicationRoot InventoryPublicationRootV2
	if decodeCanonical(raw, &publicationRoot) != nil ||
		validateInventoryPublicationRootV2(publicationRoot, publicationRoot.Repository) != nil ||
		repositoryHash(publicationRoot.Repository) != repositoryHashValue {
		return nil, 0, 1, 0, 0, nil
	}
	refs := []InventoryGenerationRefV2{publicationRoot.Current}
	if publicationRoot.Prior != nil {
		refs = append(refs, *publicationRoot.Prior)
	}
	var files []archiveFile
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, 0, 0, err
		}
		generationDirectory := filepath.Join(directory, ref.Directory)
		transition := InventoryPublicationTransitionV2{
			Repository: publicationRoot.Repository, Directory: generationDirectory,
			SourceDirectory:    filepath.Join(generationDirectory, InventoryPublicationSourceNameV2),
			InventoryDirectory: filepath.Join(generationDirectory, InventoryPublicationInventoryNameV2),
		}
		validated, err := validateInventoryPublicationStageV2(ctx, transition)
		if err != nil || validated != ref {
			return nil, 0, len(refs), 0, 0, nil
		}
		generationFiles, err := collectArchiveFiles(root, generationDirectory)
		if err != nil {
			return nil, 0, 0, 0, 0, err
		}
		files = append(files, generationFiles...)
	}
	files = append(files, archiveFile{
		path: filepath.Join(directory, InventoryPublicationRootNameV2),
		name: filepath.ToSlash(filepath.Join(repositoryHashValue, InventoryPublicationDirectoryV2, InventoryPublicationRootNameV2)),
		size: int64(len(raw)),
	})
	omittedArtifacts := 0
	staleMarkers := 0
	rooted := map[string]bool{
		InventoryPublicationRootNameV2: true,
	}
	for _, ref := range refs {
		rooted[ref.Directory] = true
	}
	for _, entry := range entries {
		if rooted[entry.Name()] {
			continue
		}
		omittedArtifacts++
		if entry.Name() == InventoryPublicationMarkerNameV2 {
			staleMarkers++
		}
	}
	return files, len(refs), 0, omittedArtifacts, staleMarkers, nil
}

func RestoreArchive(ctx context.Context, archivePath, root string) error {
	return RestoreArchiveWithStage(ctx, archivePath, root, root+".restore")
}

// RestoreArchiveWithStage restores through an explicit sibling stage. The
// recovery workflow uses a stage outside its data directory so interruption
// leaves that directory empty and a subsequent invocation can resume. The
// stage and destination must be disjoint.
func RestoreArchiveWithStage(ctx context.Context, archivePath, root, stage string) error {
	if !filepath.IsAbs(archivePath) || !filepath.IsAbs(root) || !filepath.IsAbs(stage) ||
		filepath.Clean(archivePath) != archivePath || filepath.Clean(root) != root ||
		filepath.Clean(stage) != stage || pathsOverlap(root, stage) {
		return invalid("restore paths must be absolute")
	}
	rootExists := false
	if entries, err := os.ReadDir(root); err == nil {
		if len(entries) != 0 {
			return invalid("restore root is not empty")
		}
		rootExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Lstat(stage); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(stage, 0o700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return invalid("restore stage is special")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	archiveInfo, err := file.Stat()
	if err != nil || !archiveInfo.Mode().IsRegular() || archiveInfo.Size() < 1 || archiveInfo.Size() > MaxArchiveBytes {
		_ = file.Close()
		return invalid("observation archive descriptor")
	}
	reader := tar.NewReader(io.LimitReader(file, MaxArchiveBytes+1))
	entries := 0
	var total int64
	seen := make(map[string]bool)
	seenDirectories := map[string]bool{".": true}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || header.Typeflag != tar.TypeReg || header.Size < 0 || len(header.Name) > MaxArchivePathBytes ||
			filepath.IsAbs(header.Name) || filepath.Clean(header.Name) != filepath.FromSlash(header.Name) ||
			strings.HasPrefix(header.Name, "../") || entries == MaxArchiveEntries || header.Size > MaxArchiveBytes-total {
			_ = file.Close()
			return invalid("unsafe observation archive")
		}
		for key := range header.PAXRecords {
			if key != "path" && key != "size" {
				_ = file.Close()
				return invalid("unsafe observation archive metadata")
			}
		}
		if value, ok := header.PAXRecords["path"]; ok && value != header.Name {
			_ = file.Close()
			return invalid("observation archive path metadata")
		}
		if value, ok := header.PAXRecords["size"]; ok && value != strconv.FormatInt(header.Size, 10) {
			_ = file.Close()
			return invalid("observation archive size metadata")
		}
		if seen[header.Name] {
			_ = file.Close()
			return invalid("duplicate observation archive path")
		}
		seen[header.Name] = true
		for parent := filepath.Dir(filepath.FromSlash(header.Name)); parent != "."; parent = filepath.Dir(parent) {
			seenDirectories[filepath.ToSlash(parent)] = true
		}
		destination := filepath.Join(stage, filepath.FromSlash(header.Name))
		if err := ensureRestoreParent(stage, filepath.Dir(destination)); err != nil {
			_ = file.Close()
			return err
		}
		if info, statErr := os.Lstat(destination); statErr == nil {
			if !info.Mode().IsRegular() || info.Size() != header.Size {
				_ = file.Close()
				return invalid("resumed observation archive entry changed")
			}
			existing, openErr := os.Open(destination)
			if openErr != nil {
				_ = file.Close()
				return openErr
			}
			existingHash := sha256.New()
			archiveHash := sha256.New()
			existingBytes, existingErr := io.Copy(existingHash, existing)
			closeErr := existing.Close()
			archiveBytes, archiveErr := io.CopyN(archiveHash, reader, header.Size)
			if existingErr != nil || closeErr != nil || archiveErr != nil ||
				existingBytes != header.Size || archiveBytes != header.Size ||
				!strings.EqualFold(hex.EncodeToString(existingHash.Sum(nil)), hex.EncodeToString(archiveHash.Sum(nil))) {
				_ = file.Close()
				return errors.Join(existingErr, closeErr, archiveErr, invalid("resumed observation archive entry differs"))
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			_ = file.Close()
			return statErr
		} else {
			part := destination + ".restore-part"
			if err := os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
				_ = file.Close()
				return err
			}
			output, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				_ = file.Close()
				return err
			}
			written, copyErr := io.CopyN(output, reader, header.Size)
			syncErr := output.Sync()
			closeErr := output.Close()
			if copyErr != nil || syncErr != nil || closeErr != nil || written != header.Size {
				_ = file.Close()
				var shortErr error
				if written != header.Size {
					shortErr = io.ErrShortWrite
				}
				return errors.Join(copyErr, syncErr, closeErr, shortErr)
			}
			if err := os.Rename(part, destination); err != nil {
				_ = file.Close()
				return err
			}
		}
		entries++
		total += header.Size
	}
	position, err := file.Seek(0, io.SeekCurrent)
	if err != nil || position != archiveInfo.Size() {
		_ = file.Close()
		return invalid("observation archive has trailing bytes")
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := validateRestoreStageInventory(stage, seen, seenDirectories); err != nil {
		return err
	}
	repositories, err := boundedDirectory(stage, MaxLifecycleRepositories)
	if err != nil {
		return err
	}
	for _, repository := range repositories {
		if !repository.IsDir() {
			return invalid("restore root artifact")
		}
		validAuthority := false
		raw, pointerErr := readBoundedRegular(filepath.Join(stage, repository.Name(), "current.json"), MaxManifestBytes)
		if pointerErr == nil {
			var pointer Pointer
			if decodeCanonical(raw, &pointer) != nil || repositoryHash(pointer.Repository) != repository.Name() {
				return invalid("restored pointer")
			}
			if _, err := Open(ctx, stage, pointer.Repository); err != nil {
				return err
			}
			validAuthority = true
		} else if !errors.Is(pointerErr, os.ErrNotExist) {
			return pointerErr
		}
		v2Raw, v2Err := readBoundedRegular(
			filepath.Join(stage, repository.Name(), InventoryPublicationDirectoryV2, InventoryPublicationRootNameV2),
			MaxManifestBytes,
		)
		if v2Err == nil {
			var publicationRoot InventoryPublicationRootV2
			if decodeCanonical(v2Raw, &publicationRoot) != nil ||
				validateInventoryPublicationRootV2(publicationRoot, publicationRoot.Repository) != nil ||
				repositoryHash(publicationRoot.Repository) != repository.Name() {
				return invalid("restored inventory v2 root")
			}
			refs := []InventoryGenerationRefV2{publicationRoot.Current}
			if publicationRoot.Prior != nil {
				refs = append(refs, *publicationRoot.Prior)
			}
			for _, ref := range refs {
				directory := filepath.Join(stage, repository.Name(), InventoryPublicationDirectoryV2, ref.Directory)
				validated, err := validateInventoryPublicationStageV2(ctx, InventoryPublicationTransitionV2{
					Repository: publicationRoot.Repository, Directory: directory,
					SourceDirectory:    filepath.Join(directory, InventoryPublicationSourceNameV2),
					InventoryDirectory: filepath.Join(directory, InventoryPublicationInventoryNameV2),
				})
				if err != nil || validated != ref {
					return errors.Join(err, invalid("restored inventory v2 generation"))
				}
			}
			validAuthority = true
		} else if !errors.Is(v2Err, os.ErrNotExist) {
			return v2Err
		}
		if !validAuthority {
			return invalid("restored repository has no publication authority")
		}
	}
	if rootExists {
		if err := os.Remove(root); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, root); err != nil {
		return err
	}
	return nil
}

func pathsOverlap(left, right string) bool {
	leftToRight, leftErr := filepath.Rel(left, right)
	rightToLeft, rightErr := filepath.Rel(right, left)
	return leftErr != nil || rightErr != nil || leftToRight == "." || rightToLeft == "." ||
		leftToRight != ".." && !strings.HasPrefix(leftToRight, ".."+string(filepath.Separator)) ||
		rightToLeft != ".." && !strings.HasPrefix(rightToLeft, ".."+string(filepath.Separator))
}

func validateRestoreStageInventory(stage string, files, directories map[string]bool) error {
	regular := 0
	err := filepath.WalkDir(stage, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(stage, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return invalid("restore stage artifact escapes stage")
		}
		name := filepath.ToSlash(relative)
		if entry.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 || !directories[name] {
				return invalid("restore stage contains archive-absent directory")
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || !files[name] {
			return errors.Join(err, invalid("restore stage contains archive-absent artifact"))
		}
		regular++
		if regular > MaxArchiveEntries {
			return ErrLimit
		}
		return nil
	})
	if err != nil {
		return err
	}
	if regular != len(files) {
		return invalid("restore stage inventory differs from archive")
	}
	return nil
}

func ensureRestoreParent(stage, parent string) error {
	relative, err := filepath.Rel(stage, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return invalid("restore parent escapes stage")
	}
	current := stage
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return invalid("restore parent component")
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return invalid("restore parent is special")
		}
	}
	return nil
}

func VerifyArchive(ctx context.Context, archivePath string) (ArchiveReport, error) {
	root, err := os.MkdirTemp("", "phebs-observation-verify-")
	if err != nil {
		return ArchiveReport{}, err
	}
	if err := os.Remove(root); err != nil {
		return ArchiveReport{}, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := RestoreArchive(ctx, archivePath, root); err != nil {
		return ArchiveReport{}, err
	}
	entries, err := boundedDirectory(root, MaxLifecycleRepositories)
	if err != nil {
		return ArchiveReport{}, err
	}
	var report ArchiveReport
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 64 {
			return ArchiveReport{}, invalid("verified archive repository")
		}
		if _, err := os.Lstat(filepath.Join(root, entry.Name(), "current.json")); err == nil {
			report.V1Publications++
			report.Publications++
		} else if !errors.Is(err, os.ErrNotExist) {
			return ArchiveReport{}, err
		}
		v2Raw, err := readBoundedRegular(
			filepath.Join(root, entry.Name(), InventoryPublicationDirectoryV2, InventoryPublicationRootNameV2),
			MaxManifestBytes,
		)
		if err == nil {
			var publicationRoot InventoryPublicationRootV2
			if decodeCanonical(v2Raw, &publicationRoot) != nil ||
				validateInventoryPublicationRootV2(publicationRoot, publicationRoot.Repository) != nil {
				return ArchiveReport{}, invalid("verified inventory v2 root")
			}
			count := 1
			if publicationRoot.Prior != nil {
				count++
			}
			report.V2Publications += count
			report.Publications += count
		} else if !errors.Is(err, os.ErrNotExist) {
			return ArchiveReport{}, err
		}
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.Join(err, invalid("verified archive artifact"))
		}
		report.Files++
		report.Bytes += info.Size()
		if report.Files > MaxArchiveEntries || report.Bytes > MaxArchiveBytes {
			return ErrLimit
		}
		return nil
	})
	if err != nil {
		return ArchiveReport{}, err
	}
	return report, nil
}
