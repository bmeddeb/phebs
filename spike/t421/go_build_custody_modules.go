package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func (custody *ExecutionGoBuildCustody) prepareModules(ctx context.Context, original string) (retErr error) {
	if err := custody.makeDirectory("modules"); err != nil {
		return err
	}
	cache, err := os.OpenRoot(original)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	defer func() {
		if cache.Close() != nil {
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	budget := referenceModuleBudget{}
	rawMod, err := readReferenceModuleFile(ctx, custody.tree, "source/go.mod", maxReferenceGoSumBytes, &budget)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	parsed, err := modfile.Parse("go.mod", rawMod, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != "github.com/bmeddeb/phebs" || len(parsed.Replace) != 0 {
		return ErrExecutionGoBuildCustody
	}
	raw, err := readReferenceModuleFile(ctx, custody.tree, "source/go.sum", maxReferenceGoSumBytes, &budget)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	sums, err := referenceModuleSums(raw)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	pairs := make(map[string]bool)
	for key := range sums {
		pairs[strings.TrimSuffix(key, "/go.mod")] = true
		if len(pairs) > maxReferenceModules {
			return ErrExecutionGoBuildCustody
		}
	}
	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		path, version, ok := strings.Cut(key, "@")
		escapedPath, pathErr := module.EscapePath(path)
		escapedVersion, versionErr := module.EscapeVersion(version)
		if !ok || pathErr != nil || versionErr != nil || ctx.Err() != nil {
			return ErrExecutionGoBuildCustody
		}
		base := filepath.Join("cache", "download", filepath.FromSlash(escapedPath), "@v", escapedVersion)
		modPath := base + ".mod"
		if info, err := cache.Lstat(modPath); errors.Is(err, fs.ErrNotExist) {
			// No network or cache hydration: if selected by the actual graph,
			// an absent descriptor will later fail the offline Go command.
			continue
		} else if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxReferenceGoSumBytes || sums[key+"/go.mod"] == "" {
			return ErrExecutionGoBuildCustody
		}
		if err := custody.copyTree(ctx, original, modPath, filepath.Join("modules", modPath)); err != nil {
			return err
		}
		modRaw, err := readReferenceModuleFile(ctx, custody.tree, filepath.Join("modules", modPath), maxReferenceGoSumBytes, &budget)
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		modSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(modRaw)), nil
		})
		if err != nil || modSum != sums[key+"/go.mod"] {
			return ErrExecutionGoBuildCustody
		}
		if _, err := cache.Lstat(base + ".info"); !errors.Is(err, fs.ErrNotExist) {
			if err != nil {
				return ErrExecutionGoBuildCustody
			}
			infoRaw, err := readReferenceModuleFile(ctx, cache, base+".info", maxReferenceGoSumBytes, &budget)
			if err != nil {
				return ErrExecutionGoBuildCustody
			}
			normalized, err := normalizeGoBuildModuleInfo(infoRaw, version)
			if err != nil || custody.writeControl(ctx, filepath.Join("modules", base+".info"), normalized) != nil {
				return ErrExecutionGoBuildCustody
			}
		}
		directory := filepath.FromSlash(escapedPath + "@" + escapedVersion)
		if _, err := cache.Lstat(directory); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil || sums[key] == "" {
			return ErrExecutionGoBuildCustody
		}
		if err := custody.copyTree(ctx, original, directory, filepath.Join("modules", directory)); err != nil {
			return err
		}
		actual, err := hashReferenceModuleDirectory(ctx, custody.tree, filepath.Join("modules", directory), key, &budget)
		if err != nil || actual != sums[key] {
			return ErrExecutionGoBuildCustody
		}
		// DownloadDir requires this marker even with no zip. Never import the
		// cache's claimed checksum: only the independently verified h1 is used.
		if err := custody.writeControl(ctx, filepath.Join("modules", base+".ziphash"), []byte(actual)); err != nil {
			return err
		}
	}
	return nil
}

// .info is cache lookup metadata, not checksum authority. Keep only the exact
// selected version and observed timestamp; discard optional origin metadata.
func normalizeGoBuildModuleInfo(raw []byte, version string) ([]byte, error) {
	var info struct {
		Version string
		Time    time.Time
		Origin  json.RawMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > maxReferenceGoSumBytes || decoder.Decode(&info) != nil || info.Version != version || info.Time.IsZero() ||
		!errors.Is(decoder.Decode(new(any)), io.EOF) {
		return nil, ErrExecutionGoBuildCustody
	}
	result, err := json.Marshal(struct {
		Version string
		Time    time.Time
	}{info.Version, info.Time})
	if err != nil {
		return nil, ErrExecutionGoBuildCustody
	}
	return result, nil
}

func (custody *ExecutionGoBuildCustody) writeControl(ctx context.Context, path string, raw []byte) error {
	if len(raw) > maxReferenceGoSumBytes || custody.makeDirectory(filepath.Dir(path)) != nil || custody.addName(path) != nil ||
		custody.reserveFile(int64(len(raw))) != nil || ctx.Err() != nil {
		return ErrExecutionGoBuildCustody
	}
	writer, err := custody.tree.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	n, writeErr := writer.Write(raw)
	modeErr, syncErr := writer.Chmod(0o400), writer.Sync()
	before, statErr := writer.Stat()
	if closeErr := writer.Close(); writeErr != nil || n != len(raw) || modeErr != nil || syncErr != nil || statErr != nil || closeErr != nil {
		return ErrExecutionGoBuildCustody
	}
	file, err := custody.tree.Open(path)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	after, err := file.Stat()
	if err != nil || !inputCustodySame(before, after) {
		_ = file.Close()
		return ErrExecutionGoBuildCustody
	}
	return custody.protectFile(ctx, len(custody.entries)-1, file)
}
