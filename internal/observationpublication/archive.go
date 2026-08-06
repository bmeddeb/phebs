package observationpublication

import (
	"archive/tar"
	"context"
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
		report.Omitted = MaxLifecycleRepositories + 1
		entries = nil
	} else if err != nil {
		return report, err
	}
	var files []archiveFile
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if !entry.IsDir() || len(entry.Name()) != 64 {
			report.Omitted++
			continue
		}
		repositoryRoot := filepath.Join(root, entry.Name())
		pointerRaw, err := readBoundedRegular(filepath.Join(repositoryRoot, "current.json"), MaxManifestBytes)
		if err != nil {
			report.Omitted++
			continue
		}
		var pointer Pointer
		if decodeCanonical(pointerRaw, &pointer) != nil {
			report.Omitted++
			continue
		}
		if _, err := Open(ctx, root, pointer.Repository); err != nil {
			report.Omitted++
			continue
		}
		generationRoot := generationDirectory(root, pointer.Repository, pointer.GenerationDigest)
		publicationFiles, err := collectArchiveFiles(root, generationRoot)
		if err != nil {
			return report, err
		}
		files = append(files, archiveFile{path: filepath.Join(repositoryRoot, "current.json"), name: filepath.ToSlash(filepath.Join(entry.Name(), "current.json")), size: int64(len(pointerRaw))})
		files = append(files, publicationFiles...)
		report.Publications++
	}
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
	if err != nil || verified.Publications != report.Publications {
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

func RestoreArchive(ctx context.Context, archivePath, root string) error {
	if !filepath.IsAbs(archivePath) || !filepath.IsAbs(root) {
		return invalid("restore paths must be absolute")
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) != 0 {
		return invalid("restore root is not empty")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage := root + ".restore"
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(stage)
		}
	}()
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
		destination := filepath.Join(stage, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			_ = file.Close()
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = file.Close()
			return err
		}
		written, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			_ = file.Close()
			var shortErr error
			if written != header.Size {
				shortErr = io.ErrShortWrite
			}
			return errors.Join(copyErr, closeErr, shortErr)
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
	repositories, err := boundedDirectory(stage, MaxLifecycleRepositories)
	if err != nil {
		return err
	}
	for _, repository := range repositories {
		if !repository.IsDir() {
			return invalid("restore root artifact")
		}
		raw, err := readBoundedRegular(filepath.Join(stage, repository.Name(), "current.json"), MaxManifestBytes)
		if err != nil {
			return err
		}
		var pointer Pointer
		if decodeCanonical(raw, &pointer) != nil {
			return invalid("restored pointer")
		}
		if _, err := Open(ctx, stage, pointer.Repository); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, root); err != nil {
		return err
	}
	complete = true
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
	report := ArchiveReport{Publications: len(entries)}
	return report, nil
}
