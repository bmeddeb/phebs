package t421

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// ReferenceToolRequest names private inputs, never caller-authored provenance.
// Use a dedicated clean source checkout and an already populated offline module
// cache. This build verification is not full execution/toolchain admission.
type ReferenceToolRequest struct {
	RepositoryRoot       string
	GitBinary            string
	GoRoot               string
	ModuleCache          string
	Binary               string
	PlanSourceCommit     string
	IntegratedMainCommit string
	SourceCommit         string
	Role                 string
}

// VerifyExecutionReferenceTool rebuilds one implemented Go tool from a private
// exact source snapshot and compares the complete binary, not just its metadata.
// It neither runs the supplied binary nor issues a CheckoutAdmissionBinding.
// The executor role remains unavailable until its real command exists.
func VerifyExecutionReferenceTool(ctx context.Context, request ReferenceToolRequest) (identity ExecutionToolIdentity, retErr error) {
	packagePath, modulePath, moduleVersion, moduleSum, recipe, err := referenceToolRole(request.Role)
	if err != nil {
		return identity, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	for _, path := range []*string{&request.RepositoryRoot, &request.GitBinary, &request.GoRoot, &request.ModuleCache, &request.Binary} {
		if !filepath.IsAbs(*path) {
			return identity, errors.New("reference build requires absolute input paths")
		}
		*path, err = filepath.EvalSymlinks(*path)
		if err != nil {
			return identity, errors.New("reference build input cannot be resolved")
		}
	}
	commits, err := InspectExecutionCheckout(ctx, request.RepositoryRoot, request.GitBinary,
		request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit)
	if err != nil {
		return identity, err
	}
	gitDigest, err := executableidentity.Digest(request.GitBinary)
	if err != nil {
		return identity, errors.New("reference build Git image is unavailable")
	}
	origin := executionCheckoutInspector{root: request.RepositoryRoot, git: request.GitBinary, digest: gitDigest}
	gitExec, err := origin.run(ctx, 4096, "--exec-path")
	if err != nil {
		return identity, err
	}
	// Refuse a platform shim that delegates to a different Git executable image.
	core := filepath.Join(strings.TrimSuffix(string(gitExec), "\n"), "git")
	core, err = filepath.EvalSymlinks(core)
	if err != nil || !filepath.IsAbs(core) || executableidentity.Verify(core, gitDigest) != nil {
		return identity, errors.New("reference build requires the actual Git core image")
	}
	goBinary := filepath.Join(request.GoRoot, "bin", "go")
	goDigest, err := executableidentity.Digest(goBinary)
	if err != nil {
		return identity, errors.New("reference build Go driver is unavailable")
	}
	suppliedDigest, err := executableidentity.Digest(request.Binary)
	if err != nil {
		return identity, errors.New("reference build supplied image is unavailable")
	}
	supplied, err := buildinfo.ReadFile(request.Binary)
	if err != nil || validateReferenceBuildInfo(supplied, packagePath, request.SourceCommit, modulePath, moduleVersion, moduleSum, nil) != nil {
		return identity, errors.New("reference build supplied Go identity is invalid")
	}
	sdkDigest, err := referenceSDKDigest(ctx, request.GoRoot)
	if err != nil {
		return identity, err
	}
	workspace, err := os.MkdirTemp("", "phebs-t422-reference-")
	if err != nil {
		return identity, errors.New("reference build cannot create private custody")
	}
	defer func() {
		if err := os.RemoveAll(workspace); err != nil {
			retErr = errors.Join(retErr, errors.New("reference build private custody cleanup failed"))
		}
		if retErr != nil {
			identity = ExecutionToolIdentity{}
		}
	}()
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return identity, errors.New("reference build cannot resolve private custody")
	}
	workspace = resolvedWorkspace
	for _, name := range []string{"home", "tmp", "cache", "bin"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			return identity, errors.New("reference build private directories are unavailable")
		}
	}
	if err := os.Symlink(request.GitBinary, filepath.Join(workspace, "bin", "git")); err != nil {
		return identity, errors.New("reference build cannot bind private Git path")
	}
	reference, err := createExecutionReferenceSource(ctx, origin, request.SourceCommit, filepath.Join(workspace, "source"))
	if err != nil {
		return identity, err
	}
	environment := referenceBuildEnvironment(request, workspace)
	run := func(limit int64, args ...string) ([]byte, error) {
		if executableidentity.Verify(goBinary, goDigest) != nil || executableidentity.Verify(request.GitBinary, gitDigest) != nil {
			return nil, errors.New("reference build tool image changed before launch")
		}
		return runReferenceGo(ctx, reference.root.root, goBinary, environment, limit, args...)
	}
	version, err := run(256, "version")
	if err != nil || string(version) != "go version "+runtime.Version()+" "+runtime.GOOS+"/"+runtime.GOARCH+"\n" {
		return identity, errors.New("reference build Go driver version differs")
	}
	graph, err := run(8<<20, "list", "-m", "-json", "all")
	if err != nil {
		return identity, err
	}
	modules, err := verifyExecutionModuleGraph(ctx, reference.root.root, request.ModuleCache, graph)
	if err != nil {
		return identity, err
	}
	if err := validateReferenceBuildInfo(supplied, packagePath, request.SourceCommit, modulePath, moduleVersion, moduleSum, modules); err != nil {
		return identity, err
	}
	if _, err := run(64<<10, "mod", "verify"); err != nil {
		return identity, err
	}
	output := filepath.Join(workspace, "reference")
	if _, err := run(64<<10, "build", "-trimpath", "-pgo=off", "-buildvcs=true", "-p=1", "-o", output, packagePath); err != nil {
		return identity, err
	}
	actual, err := buildinfo.ReadFile(output)
	if err != nil || validateReferenceBuildInfo(actual, packagePath, request.SourceCommit, modulePath, moduleVersion, moduleSum, modules) != nil ||
		!reflect.DeepEqual(supplied, actual) || executableidentity.Verify(output, suppliedDigest) != nil {
		return identity, errors.New("supplied executable differs from its exact reference build")
	}
	afterGraph, err := run(8<<20, "list", "-m", "-json", "all")
	if err != nil || !bytes.Equal(graph, afterGraph) {
		return identity, errors.New("reference build module graph changed")
	}
	if _, err := verifyExecutionModuleGraph(ctx, reference.root.root, request.ModuleCache, afterGraph); err != nil {
		return identity, err
	}
	if _, err := run(64<<10, "mod", "verify"); err != nil {
		return identity, err
	}
	if err := reference.verify(ctx); err != nil {
		return identity, err
	}
	after, err := InspectExecutionCheckout(ctx, request.RepositoryRoot, request.GitBinary,
		request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit)
	if err != nil || after != commits {
		return identity, errors.New("reference build original checkout changed")
	}
	afterSDK, err := referenceSDKDigest(ctx, request.GoRoot)
	if err != nil || afterSDK != sdkDigest || executableidentity.Verify(goBinary, goDigest) != nil ||
		executableidentity.Verify(request.GitBinary, gitDigest) != nil || executableidentity.Verify(request.Binary, suppliedDigest) != nil || ctx.Err() != nil {
		return identity, errors.New("reference build inputs changed or verification expired")
	}
	identity = ExecutionToolIdentity{Role: request.Role, FileType: regularFileType, SHA256: suppliedDigest,
		Version: "clean commit " + request.SourceCommit, Provenance: "go-build-info-vcs-v1", BuildVCSRevision: request.SourceCommit}
	if modulePath != "" {
		identity.Version, identity.Provenance, identity.BuildVCSRevision = moduleVersion, "go-module-build-v1", ""
		identity.ModulePath, identity.ModuleVersion, identity.ModuleSum, identity.BuildRecipeSHA256 = modulePath, moduleVersion, moduleSum, recipe
	}
	return identity, nil
}

func referenceToolRole(role string) (packagePath, modulePath, version, sum, recipe string, err error) {
	policy := frozenToolPolicy()
	switch role {
	case "phebs", "phebs-focused-index":
		packagePath = "github.com/bmeddeb/phebs/cmd/" + role
	case "t422-author":
		packagePath = "github.com/bmeddeb/phebs/spike/t422/cmd/author"
	case "buf":
		modulePath, version, sum = policy.BufModulePath, policy.BufModuleVersion, policy.BufModuleSum
		packagePath = modulePath + "/cmd/buf"
		recipe = recipeDigest("t422-buf-build-recipe-v1", modulePath, version, sum, policy.BufBuildRecipe)
	case "zoekt-git-index":
		modulePath, version, sum = policy.ZoektModulePath, policy.ZoektModuleVersion, policy.ZoektModuleSum
		packagePath = modulePath + "/cmd/zoekt-git-index"
		recipe = recipeDigest("t422-zoekt-build-recipe-v1", modulePath, version, sum, policy.ZoektBuildRecipe)
	default:
		err = errors.New("reference build role is not an implemented Go tool")
	}
	return
}

func validateReferenceBuildInfo(info *debug.BuildInfo, packagePath, commit, modulePath, version, sum string, modules map[string]string) error {
	if info == nil || info.Path != packagePath || info.GoVersion != runtime.Version() || info.Main.Replace != nil {
		return errors.New("reference build package, toolchain, or main identity differs")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		if _, duplicate := settings[setting.Key]; duplicate {
			return errors.New("reference build has duplicate settings")
		}
		settings[setting.Key] = setting.Value
	}
	if settings["CGO_ENABLED"] != "0" || settings["-trimpath"] != "true" || settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return errors.New("reference build is not closed and host native")
	}
	if modulePath == "" {
		if !validCommit(commit) || info.Main.Path != "github.com/bmeddeb/phebs" || settings["vcs"] != "git" ||
			settings["vcs.revision"] != commit || settings["vcs.modified"] != "false" {
			return errors.New("reference build does not bind the exact clean source")
		}
	} else if info.Main.Path != modulePath || info.Main.Version != version || info.Main.Sum != sum || settings["vcs.revision"] != "" ||
		(modules != nil && modules[modulePath+"@"+version] != sum) {
		return errors.New("reference build does not bind the frozen module")
	}
	for _, dependency := range info.Deps {
		if dependency == nil || dependency.Replace != nil {
			return errors.New("reference build dependency is replaced or not independently verified")
		}
		if sum, present := modules[dependency.Path+"@"+dependency.Version]; modules != nil && (!present || sum != dependency.Sum) {
			return errors.New("reference build dependency is not independently verified")
		}
	}
	return nil
}

func referenceSDKDigest(ctx context.Context, root string) (string, error) {
	digest, err := t4013.DigestBuildInputTrees(ctx, root, "src", "pkg", "lib", "go.env", "VERSION")
	if err != nil {
		return "", errors.New("reference build SDK inputs are unavailable or over bound")
	}
	return digest, nil
}

func referenceBuildEnvironment(request ReferenceToolRequest, workspace string) []string {
	return []string{
		"CGO_ENABLED=0", "GOENV=off", "GOWORK=off", "GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local",
		"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH, "GO386=sse2", "GOAMD64=v1", "GOARM=7", "GOARM64=v8.0",
		"GOEXPERIMENT=", "GOFIPS140=off", "GOCACHEPROG=", "GOPROXY=off", "GOSUMDB=off", "GOTELEMETRY=off",
		"GOROOT=" + request.GoRoot, "GOMODCACHE=" + request.ModuleCache, "GOCACHE=" + filepath.Join(workspace, "cache"),
		"GOPATH=" + filepath.Join(workspace, "home", "go"), "GOMAXPROCS=2",
		"HOME=" + filepath.Join(workspace, "home"), "TMPDIR=" + filepath.Join(workspace, "tmp"),
		"TMP=" + filepath.Join(workspace, "tmp"), "TEMP=" + filepath.Join(workspace, "tmp"),
		"PATH=" + filepath.Join(workspace, "bin"), "LANG=C", "LC_ALL=C", "TZ=UTC",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ATTR_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=4", "GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2=" + os.DevNull,
		"GIT_CONFIG_KEY_3=core.commitGraph", "GIT_CONFIG_VALUE_3=false",
	}
}

func runReferenceGo(ctx context.Context, root, binary string, environment []string, limit int64, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("reference build canceled: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	stdout, stderr := checkoutCommandOutput{remaining: limit, cancel: cancel}, checkoutCommandOutput{remaining: 64 << 10, cancel: cancel}
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = root, environment
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = time.Second
	if err := prepareReferenceCommand(command); err != nil {
		return nil, err
	}
	if err := command.Run(); err != nil || ctx.Err() != nil {
		return nil, errors.New("reference build Go command failed, expired, or exceeded output bound")
	}
	return stdout.buffer.Bytes(), nil
}
