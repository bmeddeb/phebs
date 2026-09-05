package t421

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

const (
	maxReferenceModuleGraphBytes = 8 << 20
	maxReferenceModules          = 1024
	maxReferenceGoSumBytes       = 1 << 20
)

// The remaining fields are Go's non-authoritative module-list metadata. Only
// paths derived below and independently checked file content establish sums.
type referenceGraphModule struct {
	Path, Version, Query, Dir, GoMod, GoVersion, Sum, GoModSum, Deprecated string
	Main, Indirect, Reuse                                                  bool
	Versions, Retracted                                                    []string
	Replace, Error, Time, Update, Origin                                   json.RawMessage
}

// verifyExecutionModuleGraph checks actual cache bytes against the clean source
// go.sum, never the mutable cache's .ziphash or self-reported list sums. This is
// an observation; the caller retains build custody and repeats it after builds.
func verifyExecutionModuleGraph(
	ctx context.Context, sourceRoot, moduleCache string, raw []byte,
) (_ map[string]string, retErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > maxReferenceModuleGraphBytes {
		return nil, errors.New("reference module graph exceeds its byte bound")
	}
	for _, path := range []string{sourceRoot, moduleCache} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, errors.New("reference module roots must be canonical absolute paths")
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path {
			return nil, errors.New("reference module roots contain unresolved links")
		}
	}
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return nil, errors.New("reference module source is unavailable")
	}
	defer func() { retErr = errors.Join(retErr, source.Close()) }()
	cache, err := os.OpenRoot(moduleCache)
	if err != nil {
		return nil, errors.New("reference module cache is unavailable")
	}
	defer func() { retErr = errors.Join(retErr, cache.Close()) }()
	budget := referenceModuleBudget{}
	sumRaw, err := readReferenceModuleFile(ctx, source, "go.sum", maxReferenceGoSumBytes, &budget)
	if err != nil {
		return nil, err
	}
	sums, err := referenceModuleSums(sumRaw)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	seen := make(map[string]bool)
	verified := make(map[string]string)
	mainSeen := false
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var value referenceGraphModule
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		} else if err != nil || count >= maxReferenceModules {
			return nil, errors.New("reference module graph is malformed or over its module bound")
		}
		if seen[value.Path] || rawReferenceModuleValue(value.Replace) || rawReferenceModuleValue(value.Error) {
			return nil, errors.New("reference module graph repeats, replaces, or fails a module")
		}
		seen[value.Path] = true
		if value.Main {
			if mainSeen || count != 0 || value.Path != "github.com/bmeddeb/phebs" || value.Version != "" ||
				value.Dir != sourceRoot || value.GoMod != filepath.Join(sourceRoot, "go.mod") ||
				value.Sum != "" || value.GoModSum != "" {
				return nil, errors.New("reference module graph main authority differs")
			}
			mainSeen = true
			continue
		}
		if !mainSeen || module.Check(value.Path, value.Version) != nil {
			return nil, errors.New("reference module graph dependency identity is invalid")
		}
		escapedPath, pathErr := module.EscapePath(value.Path)
		escapedVersion, versionErr := module.EscapeVersion(value.Version)
		if pathErr != nil || versionErr != nil {
			return nil, errors.New("reference module graph dependency path is invalid")
		}
		key := value.Path + "@" + value.Version
		modPath := filepath.Join("cache", "download", filepath.FromSlash(escapedPath), "@v", escapedVersion+".mod")
		wantModSum := sums[key+"/go.mod"]
		if wantModSum == "" || value.GoMod != filepath.Join(moduleCache, modPath) ||
			value.GoModSum != "" && value.GoModSum != wantModSum {
			return nil, errors.New("reference cached module descriptor lacks exact source authority")
		}
		modRaw, err := readReferenceModuleFile(ctx, cache, modPath, maxReferenceGoSumBytes, &budget)
		if err != nil {
			return nil, err
		}
		modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(modRaw)), nil
		})
		if err != nil || modSum != wantModSum {
			return nil, errors.New("reference cached module descriptor differs from source checksum")
		}
		wantSum := sums[key]
		if value.Sum != "" && (wantSum == "" || value.Sum != wantSum) {
			return nil, errors.New("reference module graph sum differs from source checksum")
		}
		if value.Dir == "" {
			continue
		}
		directory := filepath.FromSlash(escapedPath + "@" + escapedVersion)
		if wantSum == "" || value.Dir != filepath.Join(moduleCache, directory) {
			return nil, errors.New("reference module directory lacks exact source authority")
		}
		actual, err := hashReferenceModuleDirectory(ctx, cache, directory, key, &budget)
		if err != nil {
			return nil, err
		}
		if actual != wantSum {
			return nil, errors.New("reference module directory differs from source checksum")
		}
		verified[key] = actual
	}
	if !mainSeen {
		return nil, errors.New("reference module graph omits its main module")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return verified, nil
}

func rawReferenceModuleValue(value json.RawMessage) bool {
	return len(value) != 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func referenceModuleSums(raw []byte) (map[string]string, error) {
	result := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 4096)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 || module.Check(parts[0], strings.TrimSuffix(parts[1], "/go.mod")) != nil ||
			!validReferenceModuleSum(parts[2]) {
			return nil, errors.New("reference source go.sum is malformed")
		}
		key := parts[0] + "@" + parts[1]
		if _, found := result[key]; found {
			return nil, errors.New("reference source go.sum repeats a checksum")
		}
		result[key] = parts[2]
	}
	if scanner.Err() != nil {
		return nil, errors.New("reference source go.sum line exceeds its bound")
	}
	return result, nil
}

func validReferenceModuleSum(value string) bool {
	if !strings.HasPrefix(value, "h1:") {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(value[3:])
	return err == nil && len(raw) == 32 && "h1:"+base64.StdEncoding.EncodeToString(raw) == value
}

type referenceModuleBudget struct {
	entries int
	bytes   int64
}

func (budget *referenceModuleBudget) reserve(info fs.FileInfo) error {
	budget.entries++
	if budget.entries > maxCheckoutEntries || info.Size() < 0 || !info.IsDir() && !info.Mode().IsRegular() {
		return errors.New("reference module entry inventory is unsupported or over bound")
	}
	if info.Mode().IsRegular() {
		if info.Size() > maxCheckoutFileBytes || info.Size() > maxCheckoutBytes-budget.bytes {
			return errors.New("reference module file bytes exceed their bound")
		}
		budget.bytes += info.Size()
	}
	return nil
}

func referenceModuleFileInfo(root *os.Root, path string) (fs.FileInfo, error) {
	for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
		info, err := root.Lstat(parent)
		if err != nil || !info.IsDir() {
			return nil, errors.New("reference module path has an unavailable or linked ancestor")
		}
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, errors.New("reference module file is unavailable")
	}
	return info, nil
}

func readReferenceModuleFile(
	ctx context.Context, root *os.Root, path string, maximum int64, budget *referenceModuleBudget,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := referenceModuleFileInfo(root, path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximum {
		return nil, errors.New("reference module control file is irregular or over bound")
	}
	if err := budget.reserve(info); err != nil {
		return nil, err
	}
	reader, err := openReferenceModuleFile(ctx, root, path, info)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(reader)
	if err := errors.Join(readErr, reader.Close()); err != nil {
		return nil, err
	}
	return raw, nil
}

type referenceModuleFile struct {
	ctx      context.Context
	root     *os.Root
	file     *os.File
	path     string
	before   fs.FileInfo
	written  int64
	closeErr error
	closed   bool
}

func openReferenceModuleFile(
	ctx context.Context, root *os.Root, path string, before fs.FileInfo,
) (*referenceModuleFile, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, errors.New("reference module file cannot be opened")
	}
	opened, err := file.Stat()
	if err != nil || !sameCheckoutFile(before, opened) {
		_ = file.Close()
		return nil, errors.New("reference module file changed before reading")
	}
	return &referenceModuleFile{ctx: ctx, root: root, file: file, path: path, before: before}, nil
}

func (reader *referenceModuleFile) Read(raw []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	remaining := reader.before.Size() - reader.written + 1
	if remaining <= 0 {
		return 0, errors.New("reference module file grew while reading")
	}
	if int64(len(raw)) > remaining {
		raw = raw[:remaining]
	}
	n, err := reader.file.Read(raw)
	reader.written += int64(n)
	if reader.written > reader.before.Size() {
		return n, errors.New("reference module file grew while reading")
	}
	return n, err
}

func (reader *referenceModuleFile) Close() error {
	if !reader.closed {
		reader.closed = true
		after, statErr := reader.file.Stat()
		current, pathErr := reader.root.Lstat(reader.path)
		closeErr := reader.file.Close()
		if statErr != nil || pathErr != nil || closeErr != nil || reader.written != reader.before.Size() ||
			!sameCheckoutFile(reader.before, after) || !sameCheckoutFile(reader.before, current) {
			reader.closeErr = errors.New("reference module file changed while reading")
		}
	}
	return reader.closeErr
}

func hashReferenceModuleDirectory(
	ctx context.Context, root *os.Root, directory, prefix string, budget *referenceModuleBudget,
) (string, error) {
	type entry struct {
		path string
		info fs.FileInfo
	}
	info, err := referenceModuleFileInfo(root, directory)
	if err != nil || !info.IsDir() {
		return "", errors.New("reference module directory is unavailable or linked")
	}
	if err := budget.reserve(info); err != nil {
		return "", err
	}
	directories := []entry{{path: directory, info: info}}
	files := make(map[string]entry)
	names := make([]string, 0)
	for index := 0; index < len(directories); index++ {
		current := directories[index]
		if err := ctx.Err(); err != nil {
			return "", err
		}
		file, err := root.Open(current.path)
		if err != nil {
			return "", errors.New("reference module directory cannot be opened")
		}
		opened, statErr := file.Stat()
		if statErr != nil || !sameCheckoutFile(current.info, opened) {
			_ = file.Close()
			return "", errors.New("reference module directory changed before inventory")
		}
		readErr := func() error {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				children, err := file.ReadDir(128)
				if err != nil && !errors.Is(err, io.EOF) {
					return errors.New("reference module directory read failed")
				}
				for _, child := range children {
					name := child.Name()
					if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "/\\\r\n") {
						return errors.New("reference module contains an unsupported file name")
					}
					path := filepath.Join(current.path, name)
					info, err := root.Lstat(path)
					if err != nil {
						return errors.New("reference module directory entry is unavailable")
					}
					if err := budget.reserve(info); err != nil {
						return err
					}
					value := entry{path: path, info: info}
					if info.IsDir() {
						directories = append(directories, value)
					} else {
						relative := strings.TrimPrefix(path, directory+string(filepath.Separator))
						hashName := prefix + "/" + filepath.ToSlash(relative)
						if _, present := files[hashName]; present {
							return errors.New("reference module inventory repeats a file")
						}
						files[hashName] = value
						names = append(names, hashName)
					}
				}
				if errors.Is(err, io.EOF) {
					return nil
				}
			}
		}()
		after, statErr := file.Stat()
		closeErr := file.Close()
		if readErr != nil || statErr != nil || closeErr != nil || !sameCheckoutFile(current.info, after) {
			return "", errors.Join(readErr, errors.New("reference module directory changed during inventory"))
		}
	}
	var closedErr error
	actual, err := dirhash.Hash1(names, func(name string) (io.ReadCloser, error) {
		value, present := files[name]
		if !present {
			return nil, errors.New("reference module hash requested an unknown file")
		}
		reader, err := openReferenceModuleFile(ctx, root, value.path, value.info)
		if err != nil {
			return nil, err
		}
		return &referenceModuleHashReader{referenceModuleFile: reader, closedErr: &closedErr}, nil
	})
	if err != nil || closedErr != nil {
		return "", errors.Join(err, closedErr)
	}
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		current, err := root.Lstat(directory.path)
		if err != nil || !sameCheckoutFile(directory.info, current) {
			return "", errors.New("reference module directory changed after inventory")
		}
	}
	return actual, nil
}

// Hash1 intentionally discards Close errors, so retain them outside its callback.
type referenceModuleHashReader struct {
	*referenceModuleFile
	closedErr *error
}

func (reader *referenceModuleHashReader) Close() error {
	err := reader.referenceModuleFile.Close()
	*reader.closedErr = errors.Join(*reader.closedErr, err)
	return err
}
