package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"golang.org/x/mod/modfile"
)

// A version-qualified local replacement makes Go's overlay target legal:
// overlays cannot target GOMODCACHE. Only this fresh build root is modified.
// Original module content (including go.mod's language version and notices)
// stays exact; the separate overlay carries the single reviewed source edit.
func prepareZoektOfferBuild(ctx context.Context, sourceRoot, moduleCache, workspace string) (directory, overlay string, check func() error, retErr error) {
	policy := frozenToolPolicy()
	key := policy.ZoektModulePath + "@" + policy.ZoektModuleVersion
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return "", "", nil, err
	}
	defer func() { retErr = errors.Join(retErr, source.Close()) }()
	budget := referenceModuleBudget{}
	mod, err := readReferenceModuleFile(ctx, source, "go.mod", maxReferenceGoSumBytes, &budget)
	if err != nil {
		return "", "", nil, err
	}
	sum, err := readReferenceModuleFile(ctx, source, "go.sum", maxReferenceGoSumBytes, &budget)
	if err != nil {
		return "", "", nil, err
	}
	sums, err := referenceModuleSums(sum)
	if err != nil || sums[key] != policy.ZoektModuleSum {
		return "", "", nil, errors.New("private overlay source module sum differs")
	}
	descriptor, err := modfile.Parse("go.mod", mod, nil)
	if err != nil || len(descriptor.Replace) != 0 {
		return "", "", nil, errors.New("private overlay source descriptor is replaced or invalid")
	}
	selected := false
	for _, require := range descriptor.Require {
		if require.Mod.Path == policy.ZoektModulePath {
			selected = require.Mod.Version == policy.ZoektModuleVersion
		}
	}
	if !selected {
		return "", "", nil, errors.New("private overlay source module pin differs")
	}
	if err := descriptor.AddReplace(policy.ZoektModulePath, policy.ZoektModuleVersion, "./zoekt", ""); err != nil {
		return "", "", nil, err
	}
	derived, err := descriptor.Format()
	if err != nil {
		return "", "", nil, err
	}
	directory, err = os.MkdirTemp(workspace, "zoekt-build-")
	if err != nil {
		return "", "", nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", "", nil, err
	}
	defer func() { retErr = errors.Join(retErr, root.Close()) }()
	cache, err := os.OpenRoot(moduleCache)
	if err != nil {
		return "", "", nil, err
	}
	defer func() { retErr = errors.Join(retErr, cache.Close()) }()
	hashBudget := referenceModuleBudget{}
	digest, err := hashReferenceModuleDirectory(ctx, cache, key, key, &hashBudget)
	if err != nil || digest != policy.ZoektModuleSum {
		return "", "", nil, errors.New("private overlay cache module differs")
	}
	var copied int64
	err = walkGoBuildTree(ctx, cache, key, func(path string, info os.FileInfo) error {
		relative, err := filepath.Rel(key, path)
		if err != nil {
			return err
		}
		name := filepath.Join("zoekt", relative)
		if info.IsDir() {
			return root.Mkdir(name, 0o700)
		}
		if info.Size() > maxGoBuildBytes-copied {
			return ErrExecutionGoBuildCustody
		}
		file, size, err := copyExecutionInput(ctx, root, ExecutionInputCopy{Name: name, Path: filepath.Join(moduleCache, path), Executable: info.Mode().Perm()&0o111 != 0}, maxGoBuildBytes-copied)
		if err != nil {
			return err
		}
		copied += size
		return file.Close()
	})
	if err != nil {
		return "", "", nil, err
	}
	for name, raw := range map[string][]byte{"go.mod": mod, "go.sum": sum, "v3.mod": derived, "v3.sum": sum} {
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
		if err != nil {
			return "", "", nil, err
		}
		n, writeErr := file.Write(raw)
		closeErr := errors.Join(file.Sync(), file.Close())
		if n != len(raw) || writeErr != nil || closeErr != nil {
			return "", "", nil, errors.New("private overlay descriptor copy failed")
		}
	}
	overlay, check, err = prepareZoektOfferOverlay(filepath.Join(directory, "zoekt"), directory)
	if err != nil {
		return "", "", nil, err
	}
	overlayCheck := check
	check = func() (err error) {
		if err := overlayCheck(); err != nil {
			return err
		}
		src, err := os.OpenRoot(sourceRoot)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, src.Close()) }()
		dst, err := os.OpenRoot(directory)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, dst.Close()) }()
		budget := referenceModuleBudget{}
		for _, value := range []struct {
			root *os.Root
			name string
			raw  []byte
		}{
			{src, "go.mod", mod}, {src, "go.sum", sum}, {dst, "go.mod", mod}, {dst, "go.sum", sum}, {dst, "v3.mod", derived}, {dst, "v3.sum", sum},
		} {
			actual, err := readReferenceModuleFile(ctx, value.root, value.name, maxReferenceGoSumBytes, &budget)
			if err != nil || !bytes.Equal(actual, value.raw) {
				return errors.New("private overlay descriptor changed")
			}
		}
		budget = referenceModuleBudget{}
		digest, err := hashReferenceModuleDirectory(ctx, dst, "zoekt", key, &budget)
		if err != nil || digest != policy.ZoektModuleSum {
			return errors.New("private overlay module copy changed")
		}
		return nil
	}
	return directory, overlay, check, check()
}

// Compare the actual native graphs, allowing only locations of the fresh main
// descriptor and the independently hashed exact-version local Zoekt copy.
// The original graph is still verified by verifyExecutionModuleGraph.
func verifyZoektOfferGraph(baseline, actual []byte, sourceRoot, buildRoot string) error {
	decode := func(raw []byte) ([]referenceGraphModule, error) {
		if len(raw) == 0 || len(raw) > maxReferenceModuleGraphBytes {
			return nil, ErrExecutionGoBuildCustody
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var result []referenceGraphModule
		for {
			var value referenceGraphModule
			err := decoder.Decode(&value)
			if errors.Is(err, io.EOF) {
				return result, nil
			}
			if err != nil || len(result) >= maxReferenceModules {
				return nil, ErrExecutionGoBuildCustody
			}
			result = append(result, value)
		}
	}
	before, err := decode(baseline)
	if err != nil {
		return err
	}
	after, err := decode(actual)
	if err != nil || len(before) != len(after) {
		return ErrExecutionGoBuildCustody
	}
	policy := frozenToolPolicy()
	found := false
	for index := range before {
		want, got := before[index], after[index]
		if rawReferenceModuleValue(want.Replace) || rawReferenceModuleValue(want.Error) || rawReferenceModuleValue(got.Error) {
			return ErrExecutionGoBuildCustody
		}
		if want.Main {
			if index != 0 || want.Dir != sourceRoot || want.GoMod != filepath.Join(sourceRoot, "go.mod") || got.Dir != buildRoot || got.GoMod != "v3.mod" {
				return ErrExecutionGoBuildCustody
			}
			got.Dir, got.GoMod = want.Dir, want.GoMod
		} else if want.Path == policy.ZoektModulePath {
			if found || want.Version != policy.ZoektModuleVersion || want.Sum != policy.ZoektModuleSum || got.Path != want.Path || got.Version != want.Version ||
				got.Dir != filepath.Join(buildRoot, "zoekt") || got.GoMod != filepath.Join(buildRoot, "zoekt", "go.mod") || got.Sum != "" || got.GoModSum != "" || rawReferenceModuleValue(got.Time) {
				return ErrExecutionGoBuildCustody
			}
			var replacement referenceGraphModule
			if strictZoektReplacement(got.Replace, &replacement) != nil ||
				replacement.Path != "./zoekt" || replacement.Version != "" || replacement.Dir != got.Dir || replacement.GoMod != got.GoMod || replacement.GoVersion != want.GoVersion {
				return ErrExecutionGoBuildCustody
			}
			expected := referenceGraphModule{Path: "./zoekt", Dir: got.Dir, GoMod: got.GoMod, GoVersion: want.GoVersion}
			if !reflect.DeepEqual(replacement, expected) {
				return ErrExecutionGoBuildCustody
			}
			got.Dir, got.GoMod, got.Sum, got.GoModSum, got.Replace = want.Dir, want.GoMod, want.Sum, want.GoModSum, nil
			// Native go list omits the remote version's Time for this local
			// replacement. The original graph retains its unchanged value.
			got.Time = want.Time
			found = true
		}
		if !reflect.DeepEqual(want, got) {
			return ErrExecutionGoBuildCustody
		}
	}
	if !found {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

func strictZoektReplacement(raw []byte, value *referenceGraphModule) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrExecutionGoBuildCustody
	}
	return nil
}
