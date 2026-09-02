package t4110

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

type admittedExecutable struct {
	path   string
	sha256 string
}

// VerifyCleanCommit returns the exact lowercase HEAD only when the authoring
// checkout has no tracked, non-ignored untracked, staged, unstaged, or hidden
// tracked change. Ignored local files are outside the composed HEAD tree.
func VerifyCleanCommit(ctx context.Context, repositoryRoot string) (string, error) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("resolve Git executable: %w", err)
	}
	return verifyCleanCommitWithGit(ctx, repositoryRoot, gitBinary)
}

func verifyCleanCommitWithGit(
	ctx context.Context,
	repositoryRoot, gitBinary string,
) (string, error) {
	head, err := runCommand(ctx, repositoryRoot, gitBinary, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve authoring HEAD: %w", err)
	}
	head = strings.TrimSpace(head)
	if !validCommit(head) {
		return "", errors.New("authoring HEAD is not one lowercase full commit")
	}
	flags, err := runCommand(ctx, repositoryRoot, gitBinary, "ls-files", "-v", "-z")
	if err != nil {
		return "", fmt.Errorf("inspect authoring index flags: %w", err)
	}
	for _, record := range strings.Split(flags, "\x00") {
		if record == "" {
			continue
		}
		if len(record) < 3 || record[1] != ' ' || record[0] == 'S' ||
			record[0] >= 'a' && record[0] <= 'z' {
			return "", errors.New("authoring checkout has hidden tracked state")
		}
	}
	status, err := runCommand(
		ctx,
		repositoryRoot,
		gitBinary,
		"status",
		"--porcelain=v1",
		"--untracked-files=all",
	)
	if err != nil {
		return "", fmt.Errorf("inspect authoring checkout: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return "", errors.New("authoring checkout is not clean")
	}
	return head, nil
}

func commandVersion(ctx context.Context, binary string) (string, error) {
	command := exec.CommandContext(ctx, binary, "version")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", errors.Join(err, boundedCommandError(string(output)))
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", errors.New("phebs version output is empty")
	}
	return fields[len(fields)-1], nil
}

func verifyPhebsBinaryCommit(binary, commit string) (string, error) {
	return verifyGoBinaryCommit(
		binary,
		commit,
		"github.com/bmeddeb/phebs/cmd/phebs",
		true,
	)
}

func verifyAuthorBinaryCommit(binary, commit string) (string, error) {
	return verifyGoBinaryCommit(
		binary,
		commit,
		"github.com/bmeddeb/phebs/spike/t4110/cmd/author",
		true,
	)
}

func verifyGoBinaryCommit(
	binary, commit, packagePath string,
	closedBuild bool,
) (string, error) {
	if !validCommit(commit) {
		return "", errors.New("go binary commit fence is invalid")
	}
	resolved, err := filepath.Abs(binary)
	if err != nil {
		return "", fmt.Errorf("resolve Phebs binary: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve Phebs binary links: %w", err)
	}
	info, err := buildinfo.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read Go binary build identity: %w", err)
	}
	if err := rejectGoModuleReplacements(info); err != nil {
		return "", err
	}
	if info.Path != packagePath {
		return "", fmt.Errorf("go binary package identity is %q", info.Path)
	}
	if info.GoVersion != runtime.Version() {
		return "", fmt.Errorf("go binary toolchain is %q, want %q", info.GoVersion, runtime.Version())
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["vcs.revision"] != commit || settings["vcs.modified"] != "false" {
		return "", errors.New("go binary is not an exact clean-HEAD build")
	}
	if closedBuild {
		for _, key := range []string{
			"-asan", "-asmflags", "-cover", "-covermode", "-coverpkg", "-gcflags",
			"-installsuffix", "-ldflags", "-modfile", "-msan", "-overlay", "-pgo",
			"-pkgdir", "-race", "-tags", "-toolexec",
		} {
			if value := settings[key]; value != "" {
				return "", fmt.Errorf("author binary has controlling build setting %q", key)
			}
		}
		if value := settings["-buildmode"]; value != "" && value != "exe" {
			return "", fmt.Errorf("author binary has controlling build setting %q", "-buildmode")
		}
		if compiler := settings["-compiler"]; compiler != "" && compiler != "gc" {
			return "", fmt.Errorf("go binary compiler is %q", compiler)
		}
		if !closedGoSettings(settings) {
			return "", errors.New("go binary does not use the required T41.10 build settings")
		}
	}
	return resolved, nil
}

func rejectGoModuleReplacements(info *debug.BuildInfo) error {
	if info == nil {
		return errors.New("go binary build identity is absent")
	}
	if info.Main.Replace != nil {
		return errors.New("go binary main module has a replacement")
	}
	for _, dependency := range info.Deps {
		if dependency == nil || dependency.Replace != nil {
			return errors.New("go binary dependency identity is invalid or replaced")
		}
	}
	return nil
}

func compareGoBinaryBuildIdentity(supplied, reference string) error {
	suppliedInfo, err := buildinfo.ReadFile(supplied)
	if err != nil {
		return fmt.Errorf("read supplied Go binary identity: %w", err)
	}
	referenceInfo, err := buildinfo.ReadFile(reference)
	if err != nil {
		return fmt.Errorf("read reference Go binary identity: %w", err)
	}
	if err := errors.Join(
		rejectGoModuleReplacements(suppliedInfo),
		rejectGoModuleReplacements(referenceInfo),
	); err != nil {
		return err
	}
	if !reflect.DeepEqual(suppliedInfo, referenceInfo) {
		return errors.New("supplied and exact-reference Go build identities differ")
	}
	return nil
}

func closedGoSettings(settings map[string]string) bool {
	return settings["-trimpath"] == "true" && settings["CGO_ENABLED"] == "0" &&
		settings["GOOS"] == runtime.GOOS && settings["GOARCH"] == runtime.GOARCH &&
		settings["GOEXPERIMENT"] == "" &&
		(settings["GOFIPS140"] == "" || settings["GOFIPS140"] == "off") &&
		defaultArchitectureSetting(settings)
}

func defaultArchitectureSetting(settings map[string]string) bool {
	switch runtime.GOARCH {
	case "386":
		return settings["GO386"] == "sse2"
	case "amd64":
		return settings["GOAMD64"] == "v1"
	case "arm":
		return settings["GOARM"] == "7"
	case "arm64":
		return settings["GOARM64"] == "v8.0"
	default:
		return false
	}
}

func admitExecutable(path string) (admittedExecutable, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return admittedExecutable{}, err
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return admittedExecutable{}, err
	}
	digest, err := executableidentity.Digest(resolved)
	if err != nil {
		return admittedExecutable{}, err
	}
	return admittedExecutable{path: resolved, sha256: digest}, nil
}

func admitNamedExecutable(name string) (admittedExecutable, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return admittedExecutable{}, err
	}
	return admitExecutable(path)
}

func (executable admittedExecutable) verify() error {
	return executableidentity.Verify(executable.path, executable.sha256)
}
