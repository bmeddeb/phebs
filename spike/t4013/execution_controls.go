package t4013

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t401"
)

const (
	executionControlsSchema   = "t4013-execution-controls-v1"
	executionControlsFilename = ".t4013-execution-controls.json"
	maxExecutionControlsBytes = 4 << 10
)

type executionControls struct {
	Schema      string `json:"schema"`
	Workspace   string `json:"workspace"`
	Home        string `json:"home"`
	Temp        string `json:"temp"`
	ModuleCache string `json:"module_cache"`
	BuildCache  string `json:"build_cache"`
	GitExecPath string `json:"git_exec_path"`
}

func executionControlsFor(workspace, gitExecPath string) executionControls {
	root := filepath.Join(workspace, ".t4013-execution")
	return executionControls{
		Schema: executionControlsSchema, Workspace: workspace,
		Home: filepath.Join(root, "home"), Temp: filepath.Join(root, "tmp"),
		ModuleCache: filepath.Join(root, "go-mod"), BuildCache: filepath.Join(root, "go-build"),
		GitExecPath: gitExecPath,
	}
}

func createExecutionControls(
	workspace string, hostTools hostToolchainBinding,
) (executionControls, string, error) {
	if !filepath.IsAbs(workspace) || !filepath.IsAbs(hostTools.gitCore.path) {
		return executionControls{}, "", errors.New("T40.13 execution control scope is invalid")
	}
	controls := executionControlsFor(workspace, filepath.Dir(hostTools.gitCore.path))
	root := filepath.Dir(controls.Home)
	if err := os.Mkdir(root, 0o700); err != nil {
		return executionControls{}, "", errors.New("T40.13 execution controls are not new")
	}
	for _, path := range []string{controls.Home, controls.Temp} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return executionControls{}, "", errors.New("T40.13 execution control directory is not new")
		}
	}
	raw, err := t401.MarshalCanonical(controls)
	if err != nil || len(raw) > maxExecutionControlsBytes {
		return executionControls{}, "", errors.Join(err, errors.New("T40.13 execution controls exceed their bound"))
	}
	if err := writePrivateNew(filepath.Join(workspace, executionControlsFilename), raw); err != nil {
		return executionControls{}, "", fmt.Errorf("write T40.13 execution controls: %w", err)
	}
	if err := errors.Join(syncDirectory(root), syncDirectory(workspace)); err != nil {
		return executionControls{}, "", fmt.Errorf("persist T40.13 execution controls: %w", err)
	}
	if err := validateExecutionControlPaths(controls, true); err != nil {
		return executionControls{}, "", err
	}
	return controls, digest(raw), nil
}

func openExecutionControls(
	workspace, expectedDigest string, hostTools hostToolchainBinding, cachesAbsent bool,
) (executionControls, error) {
	if !digestIdentity(expectedDigest) || !filepath.IsAbs(workspace) || !filepath.IsAbs(hostTools.gitCore.path) {
		return executionControls{}, errors.New("T40.13 execution control identity is invalid")
	}
	path := filepath.Join(workspace, executionControlsFilename)
	raw, err := readAtomicRegular(path, maxExecutionControlsBytes)
	if err != nil || len(raw) == 0 || digest(raw) != expectedDigest {
		return executionControls{}, errors.Join(err, errors.New("T40.13 execution control manifest changed"))
	}
	controls, err := t401.DecodeStrict[executionControls](raw)
	want := executionControlsFor(workspace, filepath.Dir(hostTools.gitCore.path))
	if err != nil || controls != want {
		return executionControls{}, errors.Join(err, errors.New("T40.13 execution controls differ from custody"))
	}
	canonical, err := t401.MarshalCanonical(controls)
	if err != nil || !bytes.Equal(canonical, raw) {
		return executionControls{}, errors.Join(err, errors.New("T40.13 execution controls are not canonical"))
	}
	if err := validateExecutionControlPaths(controls, cachesAbsent); err != nil {
		return executionControls{}, err
	}
	return controls, nil
}

func validateExecutionControlPaths(controls executionControls, cachesAbsent bool) error {
	root := filepath.Dir(controls.Home)
	if controls.Schema != executionControlsSchema || !filepath.IsAbs(controls.Workspace) ||
		root != filepath.Join(controls.Workspace, ".t4013-execution") ||
		controls.Temp != filepath.Join(root, "tmp") || controls.ModuleCache != filepath.Join(root, "go-mod") ||
		controls.BuildCache != filepath.Join(root, "go-build") || !filepath.IsAbs(controls.GitExecPath) ||
		!isWithin(root, controls.Workspace) {
		return errors.New("T40.13 execution control paths are invalid")
	}
	for _, path := range []string{root, controls.Home, controls.Temp, controls.GitExecPath} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.Join(err, errors.New("T40.13 execution control directory is invalid"))
		}
	}
	if cachesAbsent {
		for _, path := range []string{controls.ModuleCache, controls.BuildCache} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return errors.Join(err, errors.New("T40.13 private Go cache is not explicitly absent"))
			}
		}
	}
	return nil
}

func privateCacheDigest(ctx context.Context, path string) (string, error) {
	value, err := hostTreeDigest(ctx, path, ".")
	if err != nil {
		return "", fmt.Errorf("digest bounded T40.13 private module cache: %w", err)
	}
	return value, nil
}

func removePrivateGoCache(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, errors.New("T40.13 private Go cache is invalid"))
	}
	if err := filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
