package t4013

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	maxHostExecutableBytes = 256 << 20
	maxHostVersionBytes    = 4 << 10
	maxHostTreeEntries     = 100_000
	maxHostTreeBytes       = int64(2 << 30)
	hostObservationTimeout = 30 * time.Second
)

type boundExecutable struct {
	name   string
	path   string
	sha256 string
}

type hostToolchainBinding struct {
	goDriver boundExecutable
	git      boundExecutable
	gitCore  boundExecutable
	surreal  boundExecutable
}

func (binding hostToolchainBinding) verifyExecutables(ctx context.Context) error {
	for _, executable := range []boundExecutable{
		binding.goDriver, binding.git, binding.gitCore, binding.surreal,
	} {
		if _, err := executable.pathForLaunch(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (executable boundExecutable) pathForLaunch(ctx context.Context) (string, error) {
	if ctx == nil || executable.name == "" || !filepath.IsAbs(executable.path) ||
		!digestIdentity(executable.sha256) {
		return "", errors.New("T40.13 bound executable identity is invalid")
	}
	info, err := os.Lstat(executable.path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode()&0o111 == 0 {
		return "", errors.Join(err, fmt.Errorf("T40.13 bound executable changed: %s", executable.name))
	}
	observed, err := executableDigestContext(ctx, executable.path)
	if err != nil || observed != executable.sha256 {
		return "", errors.Join(err, fmt.Errorf("T40.13 bound executable changed: %s", executable.name))
	}
	return executable.path, nil
}

// ObserveHostToolchain returns the committed source-free identities used by
// the Go execution core. The shell driver's fixed utilities remain part of the
// dedicated-host TCB and are not claimed by this inventory.
func ObserveHostToolchain(ctx context.Context) ([]HostToolObservation, error) {
	return observeHostToolchain(ctx, false)
}

func observeHostToolchain(ctx context.Context, closedEnvironment bool) ([]HostToolObservation, error) {
	if ctx == nil {
		return nil, errors.New("T40.13 host toolchain context is nil")
	}
	if !closedEnvironment {
		return observeHostToolchainNow(ctx, false, nil)
	}
	observationContext, cancel := context.WithTimeout(ctx, hostObservationTimeout)
	defer cancel()
	var binding *hostToolchainBinding
	bound, ok, err := closedHostToolchainBinding(observationContext, nil)
	if err != nil {
		return nil, err
	}
	if ok {
		binding = &bound
	}
	values, err := observeHostToolchainNow(observationContext, closedEnvironment, binding)
	if err != nil && observationContext.Err() != nil {
		return nil, fmt.Errorf(
			"T40.13 host toolchain observation exceeded its deadline: %w", observationContext.Err(),
		)
	}
	return values, err
}

func observeHostToolchainNow(
	ctx context.Context, closedEnvironment bool, binding *hostToolchainBinding,
) ([]HostToolObservation, error) {
	digestExecutable := func(path string) (string, error) {
		if closedEnvironment {
			return executableDigestContext(ctx, path)
		}
		return executableDigest(path)
	}
	var goPath string
	var err error
	if binding != nil {
		goPath = binding.goDriver.path
	} else {
		goPath, err = resolveExecutable("go")
		if err != nil {
			return nil, err
		}
	}
	goDigestBefore, err := digestExecutable(goPath)
	if err != nil {
		return nil, fmt.Errorf("digest Go driver before observation: %w", err)
	}
	goVersion, err := boundedCommand(ctx, closedEnvironment, goPath, "version")
	if err != nil {
		return nil, fmt.Errorf("observe Go driver version: %w", err)
	}
	toolDirectory, err := boundedCommand(ctx, closedEnvironment, goPath, "env", "GOTOOLDIR")
	if err != nil {
		return nil, fmt.Errorf("observe Go tool directory: %w", err)
	}
	if !filepath.IsAbs(toolDirectory) {
		return nil, errors.New("T40.13 Go tool directory is not absolute")
	}
	var goRoot string
	var goToolsDigest, goRootDigest string
	if closedEnvironment {
		goRoot, err = boundedCommand(ctx, true, goPath, "env", "GOROOT")
		if err != nil || !filepath.IsAbs(goRoot) {
			return nil, errors.Join(err, errors.New("T40.13 Go root directory is invalid"))
		}
		goToolsDigest, err = hostTreeDigest(ctx, toolDirectory, ".")
		if err != nil {
			return nil, fmt.Errorf("digest Go tool directory: %w", err)
		}
		goRootDigest, err = hostTreeDigest(ctx, goRoot, "src", filepath.Join("pkg", "include"))
		if err != nil {
			return nil, fmt.Errorf("digest Go build inputs: %w", err)
		}
	}
	goDigestAfter, err := digestExecutable(goPath)
	if err != nil || goDigestAfter != goDigestBefore {
		return nil, errors.New("T40.13 Go driver changed while it was observed")
	}
	compilePath, err := resolveExactExecutable(filepath.Join(toolDirectory, "compile"))
	if err != nil {
		return nil, err
	}
	linkPath, err := resolveExactExecutable(filepath.Join(toolDirectory, "link"))
	if err != nil {
		return nil, err
	}
	compileIdentity, err := observeExecutable(ctx, closedEnvironment, "go-compile", compilePath, "-V=full")
	if err != nil {
		return nil, fmt.Errorf("observe Go compiler version: %w", err)
	}
	linkIdentity, err := observeExecutable(ctx, closedEnvironment, "go-link", linkPath, "-V=full")
	if err != nil {
		return nil, fmt.Errorf("observe Go linker version: %w", err)
	}
	var asmIdentity HostToolObservation
	var asmPath string
	if closedEnvironment {
		var resolveErr error
		asmPath, resolveErr = resolveExactExecutable(filepath.Join(toolDirectory, "asm"))
		if resolveErr != nil {
			return nil, resolveErr
		}
		asmIdentity, err = observeExecutable(ctx, true, "go-asm", asmPath, "-V=full")
		if err != nil {
			return nil, fmt.Errorf("observe Go assembler version: %w", err)
		}
	}
	var gitPath string
	if binding != nil {
		gitPath = binding.git.path
	} else {
		gitPath, err = resolveExecutable("git")
		if err != nil {
			return nil, err
		}
	}
	gitIdentity, err := observeExecutable(ctx, closedEnvironment, "git", gitPath, "--version")
	if err != nil {
		return nil, fmt.Errorf("observe Git version: %w", err)
	}
	var gitCoreIdentity HostToolObservation
	var gitCorePath string
	var gitExecPath, gitToolsDigest string
	if closedEnvironment {
		var resolveErr error
		if binding != nil {
			gitCorePath = binding.gitCore.path
		} else {
			gitCorePath, resolveErr = resolveGitCoreExecutableFor(ctx, gitPath)
			if resolveErr != nil {
				return nil, resolveErr
			}
		}
		gitCoreIdentity, err = observeExecutable(ctx, true, "git-core", gitCorePath, "--version")
		if err != nil {
			return nil, fmt.Errorf("observe Git core version: %w", err)
		}
		gitExecPath, err = resolveGitExecPathFor(ctx, gitPath)
		if err != nil {
			return nil, err
		}
		gitToolsDigest, err = hostTreeDigest(ctx, gitExecPath, ".")
		if err != nil {
			return nil, fmt.Errorf("digest Git tool directory: %w", err)
		}
	}
	var systemIdentities []HostToolObservation
	var systemPaths []string
	if closedEnvironment {
		for _, tool := range []struct {
			name string
			path string
		}{
			{"sandbox-exec", "/usr/bin/sandbox-exec"},
			{"sh", "/bin/sh"},
			{"du", "/usr/bin/du"},
			{"ps", "/bin/ps"},
			{"pgrep", "/usr/bin/pgrep"},
			{"sysctl", "/usr/sbin/sysctl"},
		} {
			path := tool.path
			var resolveErr error
			if !filepath.IsAbs(path) {
				path, resolveErr = resolveExecutable(path)
			} else {
				path, resolveErr = resolveExactExecutable(path)
			}
			if resolveErr != nil {
				return nil, resolveErr
			}
			identity, observeErr := observeBoundExecutable(ctx, tool.name, path)
			if observeErr != nil {
				return nil, fmt.Errorf("observe %s executable: %w", tool.name, observeErr)
			}
			systemIdentities = append(systemIdentities, identity)
			systemPaths = append(systemPaths, path)
		}
	}
	var surreal store.SurrealIdentity
	if closedEnvironment {
		var surrealPath string
		if binding != nil {
			surrealPath = binding.surreal.path
		} else {
			var resolveErr error
			surrealPath, resolveErr = resolveExecutable("surreal")
			if resolveErr != nil {
				return nil, resolveErr
			}
		}
		surreal, err = store.InspectSurrealBinaryContext(ctx, surrealPath)
		if err == nil {
			var version string
			version, err = boundedCommand(ctx, true, surreal.Path, "version")
			fields := strings.Fields(version)
			if err == nil && (len(fields) == 0 || fields[0] != surreal.Version) {
				err = errors.New("T40.13 SurrealDB version differs in the closed environment")
			}
		}
	} else {
		surreal, err = store.FindSurrealBinary()
	}
	if err != nil {
		return nil, fmt.Errorf("observe SurrealDB executable: %w", err)
	}
	surrealDigestAfter, err := digestExecutable(surreal.Path)
	if err != nil || surrealDigestAfter != surreal.SHA256 {
		return nil, errors.New("T40.13 SurrealDB executable changed while it was observed")
	}
	values := []HostToolObservation{
		{Name: "go", Version: goVersion, SHA256: goDigestBefore},
		compileIdentity,
		linkIdentity,
		gitIdentity,
		{Name: "surreal", Version: surreal.Version, SHA256: surreal.SHA256},
	}
	paths := []string{goPath, compilePath, linkPath, gitPath, surreal.Path}
	if closedEnvironment {
		values = append(values,
			asmIdentity,
			gitCoreIdentity,
			HostToolObservation{Name: "git-tools", Version: gitIdentity.Version, SHA256: gitToolsDigest},
			HostToolObservation{Name: "go-tools", Version: goVersion, SHA256: goToolsDigest},
			HostToolObservation{Name: "go-root", Version: goVersion, SHA256: goRootDigest},
		)
		values = append(values, systemIdentities...)
		paths = append(paths, asmPath, gitCorePath, gitExecPath, toolDirectory, goRoot)
		paths = append(paths, systemPaths...)
	}
	if closedEnvironment {
		for index, path := range paths {
			values[index].PathSHA256 = hostPathDigest(path)
			switch values[index].Name {
			case "git-tools", "go-tools", "go-root":
				continue
			}
			digest, digestErr := executableDigestContext(ctx, path)
			if digestErr != nil || digest != values[index].SHA256 {
				return nil, errors.New("T40.13 host executable changed during the complete observation")
			}
		}
	}
	if closedEnvironment {
		toolsAfter, toolsErr := hostTreeDigest(ctx, toolDirectory, ".")
		rootAfter, rootErr := hostTreeDigest(ctx, goRoot, "src", filepath.Join("pkg", "include"))
		gitAfter, gitErr := hostTreeDigest(ctx, gitExecPath, ".")
		if toolsErr != nil || rootErr != nil || gitErr != nil || toolsAfter != goToolsDigest ||
			rootAfter != goRootDigest || gitAfter != gitToolsDigest {
			return nil, errors.New("T40.13 Go build inputs changed during the complete observation")
		}
	}
	if err := validateHostToolchain(values, closedEnvironment); err != nil {
		return nil, err
	}
	return values, nil
}

func resolveGitCoreExecutable(ctx context.Context) (string, error) {
	gitPath, err := resolveExecutable("git")
	if err != nil {
		return "", err
	}
	return resolveGitCoreExecutableFor(ctx, gitPath)
}

func resolveGitCoreExecutableFor(ctx context.Context, gitPath string) (string, error) {
	execPath, err := resolveGitExecPathFor(ctx, gitPath)
	if err != nil {
		return "", err
	}
	return resolveExactExecutable(filepath.Join(execPath, "git"))
}

func resolveGitExecPathFor(ctx context.Context, gitPath string) (string, error) {
	execPath, err := boundedCommand(ctx, true, gitPath, "--exec-path")
	if err != nil || !filepath.IsAbs(execPath) {
		return "", errors.Join(err, errors.New("T40.13 Git executable directory is invalid"))
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", errors.Join(err, errors.New("T40.13 Git executable directory is invalid"))
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.Join(err, errors.New("T40.13 Git executable directory is invalid"))
	}
	return resolved, nil
}

func observeExecutable(
	ctx context.Context, closedEnvironment bool, name, path string, arguments ...string,
) (HostToolObservation, error) {
	digestExecutable := executableDigest
	if closedEnvironment {
		digestExecutable = func(path string) (string, error) {
			return executableDigestContext(ctx, path)
		}
	}
	before, err := digestExecutable(path)
	if err != nil {
		return HostToolObservation{}, err
	}
	version, err := boundedCommand(ctx, closedEnvironment, path, arguments...)
	if err != nil {
		return HostToolObservation{}, err
	}
	after, err := digestExecutable(path)
	if err != nil || after != before {
		return HostToolObservation{}, errors.New("host executable changed while it was observed")
	}
	return HostToolObservation{Name: name, Version: version, SHA256: before}, nil
}

func observeBoundExecutable(ctx context.Context, name, path string) (HostToolObservation, error) {
	digest, err := executableDigestContext(ctx, path)
	if err != nil {
		return HostToolObservation{}, err
	}
	return HostToolObservation{Name: name, Version: "bound executable", SHA256: digest}, nil
}

// DigestBuildInputTrees reuses the bounded host-tree identity reader for later
// reference builds. It observes bytes and modes, not exclusive host custody.
func DigestBuildInputTrees(ctx context.Context, root string, trees ...string) (string, error) {
	return hostTreeDigest(ctx, root, trees...)
}

func hostTreeDigest(ctx context.Context, root string, trees ...string) (string, error) {
	if ctx == nil {
		return "", errors.New("host tree context is nil")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(resolved) {
		return "", errors.Join(err, errors.New("host tree root is invalid"))
	}
	rootInfo, err := os.Lstat(resolved)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.Join(err, errors.New("host tree root is invalid"))
	}
	hash := sha256.New()
	entries := 0
	var total int64
	symlinkTargets := map[string]string{}
	for _, tree := range trees {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		tree = filepath.Clean(tree)
		if tree == ".." || filepath.IsAbs(tree) || strings.HasPrefix(tree, ".."+string(filepath.Separator)) {
			return "", errors.New("host tree selection is invalid")
		}
		if err := filepath.WalkDir(filepath.Join(resolved, tree), func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			entries++
			if entries > maxHostTreeEntries {
				return errors.New("host tree exceeds its entry bound")
			}
			info, err := entry.Info()
			if err != nil || !info.IsDir() && !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return errors.Join(err, errors.New("host tree contains an invalid entry"))
			}
			relative, err := filepath.Rel(resolved, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return errors.Join(err, errors.New("host tree entry escaped its root"))
			}
			if relative == "." {
				if !info.IsDir() {
					return errors.New("host tree root is not a directory")
				}
				return nil
			}
			kind := "d"
			if info.Mode().IsRegular() {
				kind = "f"
			} else if info.Mode()&os.ModeSymlink != 0 {
				kind = "l"
			}
			_, _ = io.WriteString(hash, kind+"\x00"+filepath.ToSlash(relative)+"\x00"+
				strconv.FormatUint(uint64(info.Mode()), 10)+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00")
			if info.IsDir() {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(path)
				if err != nil || target == "" || len(target) > 4<<10 {
					return errors.Join(err, errors.New("host tree symlink is invalid"))
				}
				resolvedTarget, err := filepath.EvalSymlinks(path)
				if err != nil {
					return err
				}
				targetInfo, err := os.Lstat(resolvedTarget)
				if err != nil || !targetInfo.Mode().IsRegular() || targetInfo.Size() <= 0 {
					return errors.Join(err, errors.New("host tree symlink target is invalid"))
				}
				digest, ok := symlinkTargets[resolvedTarget]
				if !ok {
					if total > maxHostTreeBytes-targetInfo.Size() {
						return errors.New("host tree exceeds its byte bound")
					}
					total += targetInfo.Size()
					digest, err = regularFileDigestContext(ctx, resolvedTarget, maxHostTreeBytes)
					if err != nil {
						return err
					}
					symlinkTargets[resolvedTarget] = digest
				}
				_, _ = io.WriteString(hash, target+"\x00"+digest+"\x00")
				return nil
			}
			if info.Size() < 0 || total > maxHostTreeBytes-info.Size() {
				return errors.New("host tree exceeds its byte bound")
			}
			total += info.Size()
			file, err := openNoFollowRegular(path)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(hash, io.LimitReader(contextReader{ctx, file}, info.Size()+1))
			after, statErr := file.Stat()
			afterPath, pathErr := os.Lstat(path)
			closeErr := file.Close()
			if copyErr != nil || statErr != nil || pathErr != nil || closeErr != nil || written != info.Size() ||
				!sameFileSnapshot(info, after) || !sameFileSnapshot(after, afterPath) {
				return errors.Join(copyErr, statErr, pathErr, closeErr, errors.New("host tree entry changed while hashing"))
			}
			_, _ = hash.Write([]byte{0})
			return nil
		}); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// VerifyHostToolchain re-observes the host tools and compares every ordered
// version and executable digest with the independently reviewed v2 plan.
func VerifyHostToolchain(ctx context.Context, expected []HostToolObservation) error {
	return verifyHostToolchain(ctx, expected, false)
}

func verifyHostToolchainForPlan(ctx context.Context, plan Plan) error {
	if planSchemaVersion(plan.Schema) >= 25 {
		_, err := bindHostToolchainForPlan(ctx, plan)
		return err
	}
	return verifyHostToolchain(ctx, plan.HostToolchain, planSchemaVersion(plan.Schema) >= 25)
}

func bindHostToolchainForPlan(ctx context.Context, plan Plan) (hostToolchainBinding, error) {
	if planSchemaVersion(plan.Schema) < 25 {
		return hostToolchainBinding{}, verifyHostToolchainForPlan(ctx, plan)
	}
	if err := validateHostToolchain(plan.HostToolchain, true); err != nil {
		return hostToolchainBinding{}, err
	}
	binding, err := prebindHostToolchain(ctx, plan.HostToolchain)
	if err != nil {
		return hostToolchainBinding{}, err
	}
	observationContext, cancel := context.WithTimeout(ctx, hostObservationTimeout)
	defer cancel()
	observed, err := observeHostToolchainNow(observationContext, true, &binding)
	if err != nil {
		if observationContext.Err() != nil {
			return hostToolchainBinding{}, fmt.Errorf(
				"T40.13 host toolchain observation exceeded its deadline: %w", observationContext.Err(),
			)
		}
		return hostToolchainBinding{}, err
	}
	if err := compareHostToolchain(plan.HostToolchain, observed); err != nil {
		return hostToolchainBinding{}, err
	}
	if err := binding.verifyExecutables(ctx); err != nil {
		return hostToolchainBinding{}, err
	}
	return binding, nil
}

func prebindHostToolchain(
	ctx context.Context, expected []HostToolObservation,
) (hostToolchainBinding, error) {
	if binding, ok, err := closedHostToolchainBinding(ctx, expected); ok || err != nil {
		return binding, err
	}
	goPath, err := resolveExecutable("go")
	if err != nil {
		return hostToolchainBinding{}, err
	}
	gitPath, err := resolveExecutable("git")
	if err != nil {
		return hostToolchainBinding{}, err
	}
	surrealPath, err := resolveExecutable("surreal")
	if err != nil {
		return hostToolchainBinding{}, err
	}
	binding := hostToolchainBinding{
		goDriver: bindObservedExecutable(expected, "go", goPath),
		git:      bindObservedExecutable(expected, "git", gitPath),
		surreal:  bindObservedExecutable(expected, "surreal", surrealPath),
	}
	for _, executable := range []boundExecutable{binding.goDriver, binding.git, binding.surreal} {
		if _, err := executable.pathForLaunch(ctx); err != nil {
			return hostToolchainBinding{}, err
		}
	}
	gitCorePath, err := resolveGitCoreExecutableFor(ctx, binding.git.path)
	if err != nil {
		return hostToolchainBinding{}, err
	}
	binding.gitCore = bindObservedExecutable(expected, "git-core", gitCorePath)
	if _, err := binding.gitCore.pathForLaunch(ctx); err != nil {
		return hostToolchainBinding{}, err
	}
	return binding, nil
}

func closedHostToolchainBinding(
	ctx context.Context, expected []HostToolObservation,
) (hostToolchainBinding, bool, error) {
	paths := []string{
		os.Getenv("CLOSED_GO_PATH"),
		os.Getenv("CLOSED_GIT_PATH"),
		os.Getenv("CLOSED_GIT_CORE_PATH"),
		os.Getenv("CLOSED_SURREAL_PATH"),
	}
	present := 0
	for _, path := range paths {
		if path != "" {
			present++
		}
	}
	if present == 0 {
		return hostToolchainBinding{}, false, nil
	}
	if present != len(paths) {
		return hostToolchainBinding{}, true, errors.New("T40.13 closed host tool paths are incomplete")
	}
	for index, path := range paths {
		resolved, err := resolveExactExecutable(path)
		if err != nil || resolved != path {
			return hostToolchainBinding{}, true, errors.Join(
				err, errors.New("T40.13 closed host tool path is not exact"),
			)
		}
		paths[index] = resolved
	}
	names := []string{"go", "git", "git-core", "surreal"}
	executables := make([]boundExecutable, len(paths))
	for index, path := range paths {
		if expected == nil {
			digest, err := executableDigestContext(ctx, path)
			if err != nil {
				return hostToolchainBinding{}, true, err
			}
			executables[index] = boundExecutable{name: names[index], path: path, sha256: digest}
		} else {
			executables[index] = bindObservedExecutable(expected, names[index], path)
		}
		if _, err := executables[index].pathForLaunch(ctx); err != nil {
			return hostToolchainBinding{}, true, err
		}
	}
	gitCore, err := resolveGitCoreExecutableFor(ctx, executables[1].path)
	if err != nil || gitCore != executables[2].path {
		return hostToolchainBinding{}, true, errors.Join(
			err, errors.New("T40.13 closed Git core path differs from Git authority"),
		)
	}
	return hostToolchainBinding{
		goDriver: executables[0], git: executables[1],
		gitCore: executables[2], surreal: executables[3],
	}, true, nil
}

func bindObservedExecutable(values []HostToolObservation, name, path string) boundExecutable {
	for _, value := range values {
		if value.Name == name && value.PathSHA256 == hostPathDigest(path) {
			return boundExecutable{name: name, path: path, sha256: value.SHA256}
		}
	}
	return boundExecutable{}
}

func hostPathDigest(path string) string {
	return digest([]byte("host-executable-path\x00" + path))
}

func verifyHostToolchain(
	ctx context.Context, expected []HostToolObservation, closedEnvironment bool,
) error {
	if err := validateHostToolchain(expected, closedEnvironment); err != nil {
		return err
	}
	observed, err := observeHostToolchain(ctx, closedEnvironment)
	if err != nil {
		return err
	}
	if err := compareHostToolchain(expected, observed); err != nil {
		return err
	}
	return nil
}

func compareHostToolchain(expected, observed []HostToolObservation) error {
	if len(expected) != len(observed) {
		return errors.New("T40.13 host toolchain inventory differs from the frozen plan")
	}
	for index := range expected {
		if expected[index] != observed[index] {
			name := expected[index].Name
			if name == "" || observed[index].Name != name {
				return errors.New("T40.13 host toolchain inventory differs from the frozen plan")
			}
			return fmt.Errorf("T40.13 host toolchain differs from the frozen plan: %s", name)
		}
	}
	return nil
}

func resolveExecutable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve host tool %s: %w", name, err)
	}
	return resolveExactExecutable(path)
}

func resolveExactExecutable(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve host executable: %w", err)
	}
	if !filepath.IsAbs(resolved) {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("make host executable absolute: %w", err)
		}
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect host executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Size() <= 0 || info.Size() > maxHostExecutableBytes {
		return "", errors.New("T40.13 host tool is not a bounded executable regular file")
	}
	return resolved, nil
}

func executableDigest(path string) (string, error) {
	return regularFileDigestContext(context.Background(), path, maxHostExecutableBytes)
}

func executableDigestContext(ctx context.Context, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&0o111 == 0 {
		return "", errors.Join(err, errors.New("host executable changed or is not executable"))
	}
	return regularFileDigestContext(ctx, path, maxHostExecutableBytes)
}

func regularFileDigestContext(ctx context.Context, path string, maximumBytes int64) (string, error) {
	if ctx == nil {
		return "", errors.New("host digest context is nil")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() <= 0 || before.Size() > maximumBytes {
		return "", errors.Join(err, errors.New("host executable changed or exceeded its byte bound"))
	}
	file, err := openNoFollowRegular(path)
	if err != nil {
		return "", err
	}
	opened, err := file.Stat()
	if err != nil || !sameFileSnapshot(before, opened) {
		_ = file.Close()
		return "", errors.Join(err, errors.New("host executable changed before hashing"))
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(contextReader{ctx, file}, maximumBytes+1))
	if err != nil {
		_ = file.Close()
		return "", err
	}
	afterOpen, openStatErr := file.Stat()
	closeErr := file.Close()
	afterPath, pathStatErr := os.Lstat(path)
	if openStatErr != nil || closeErr != nil || pathStatErr != nil ||
		!sameFileSnapshot(opened, afterOpen) || !sameFileSnapshot(afterOpen, afterPath) ||
		afterPath.Mode()&os.ModeSymlink != 0 || written != before.Size() || written > maximumBytes {
		return "", errors.New("host executable changed while hashing")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	context.Context
	io.Reader
}

func (reader contextReader) Read(value []byte) (int, error) {
	if err := reader.Err(); err != nil {
		return 0, err
	}
	return reader.Reader.Read(value)
}

func boundedCommand(
	ctx context.Context, closedEnvironment bool, path string, arguments ...string,
) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stdout := &boundedBuffer{remaining: maxHostVersionBytes}
	stderr := &boundedBuffer{remaining: maxHostVersionBytes}
	command := exec.CommandContext(commandContext, path, arguments...)
	if closedEnvironment {
		command.Env = scrubExecutionEnvironment()
	}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return "", err
	}
	value := strings.TrimSpace(stdout.String())
	if value == "" {
		value = strings.TrimSpace(stderr.String())
	}
	if !boundedVersion(value) {
		return "", errors.New("host tool returned an invalid bounded version")
	}
	return value, nil
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	remaining int
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if len(value) > buffer.remaining {
		written := 0
		if buffer.remaining > 0 {
			written, _ = buffer.buffer.Write(value[:buffer.remaining])
			buffer.remaining = 0
		}
		return written, errors.New("host tool version exceeds its byte bound")
	}
	written, err := buffer.buffer.Write(value)
	buffer.remaining -= written
	return written, err
}

func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }
