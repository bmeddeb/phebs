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
	"strings"
	"time"
)

const (
	maxArchiveEntries = 100_000
	maxArchiveBytes   = int64(1 << 40)
)

func VerifyArchive(archivePath string) error {
	root, err := os.MkdirTemp("", "phebs-focused-archive-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	return RestoreArchive(archivePath, filepath.Join(root, "index"))
}

// CreateArchive writes a deterministic inventory of every valid focused
// publication. Contained manifest, sidecar, and shard bytes are copied exactly.
func CreateArchive(indexDir, destination string) error {
	publications, files, err := validatedPublications(indexDir)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(file)
	for _, name := range files {
		path := filepath.Join(indexDir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxArchiveBytes {
			_ = writer.Close()
			_ = file.Close()
			return fmt.Errorf("focused archive input %q is missing, special, or too large", name)
		}
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
			_ = file.Close()
			return err
		}
		source, err := os.Open(path)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return err
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		if copyErr != nil || closeErr != nil {
			_ = writer.Close()
			_ = file.Close()
			return errors.Join(copyErr, closeErr)
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	for repository, digest := range publications {
		manifest, err := ValidateSelfContained(indexDir, repository)
		if err != nil || manifest.Digest != digest {
			return errors.New("focused publication changed while backup was being created")
		}
	}
	return nil
}

// RestoreArchive verifies all archived bytes in a private staging directory,
// then installs shards and sidecars before manifests. It is intended for an
// offline, newly created recovery data directory.
func RestoreArchive(archivePath, indexDir string) error {
	archiveInfo, err := os.Lstat(archivePath)
	if err != nil || !archiveInfo.Mode().IsRegular() ||
		archiveInfo.Size() <= 0 || archiveInfo.Size() > maxArchiveBytes {
		return errors.New("focused archive is missing, special, empty, or exceeds its limit")
	}
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		return err
	}
	if err := ensureRealDirectory(indexDir); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(indexDir, ".phebs-restore-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	reader := tar.NewReader(io.LimitReader(archive, maxArchiveBytes+1))
	extracted := make([]string, 0)
	seen := map[string]bool{}
	var total int64
	for len(extracted) <= maxArchiveEntries {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = archive.Close()
			return fmt.Errorf("read focused archive: %w", nextErr)
		}
		name := header.Name
		if header.Typeflag != tar.TypeReg || filepath.Base(name) != name ||
			!strings.HasPrefix(name, "phebs-focus-") || seen[name] ||
			header.Size < 0 || header.Size > maxArchiveBytes-total {
			_ = archive.Close()
			return fmt.Errorf("focused archive entry %q is unsafe", name)
		}
		seen[name] = true
		total += header.Size
		destination := filepath.Join(stage, name)
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = archive.Close()
			return err
		}
		written, copyErr := io.CopyN(output, reader, header.Size)
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil || written != header.Size || syncErr != nil || closeErr != nil {
			_ = archive.Close()
			return errors.Join(copyErr, syncErr, closeErr)
		}
		extracted = append(extracted, name)
	}
	if len(extracted) > maxArchiveEntries {
		_ = archive.Close()
		return errors.New("focused archive contains too many entries")
	}
	if err := archive.Close(); err != nil {
		return err
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
	manifests := make([]string, 0)
	for _, name := range extracted {
		if strings.HasSuffix(name, ".manifest.json") {
			manifests = append(manifests, name)
			continue
		}
		if _, err := os.Lstat(filepath.Join(indexDir, name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("focused restore target %q already exists", name)
		}
		if err := moveRegular(
			filepath.Join(stage, name), filepath.Join(indexDir, name),
		); err != nil {
			return err
		}
	}
	for _, name := range manifests {
		if _, err := os.Lstat(filepath.Join(indexDir, name)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("focused restore target %q already exists", name)
		}
		if err := moveRegular(
			filepath.Join(stage, name), filepath.Join(indexDir, name),
		); err != nil {
			return err
		}
	}
	return syncDirectory(indexDir)
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
			return nil, nil, errors.New("focused manifest filename or repository is ambiguous")
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
