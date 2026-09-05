package t421

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

// ProtectReferenceTool copies a selected implemented Go image, then rebuilds it
// from this protected SDK/module/source custody using the exact reference recipe.
// No supplied image is executed. The mutex serializes the complete admission
// build and makes Close wait until all children have joined. This is never a
// production request path; no general-purpose runner or launch permission leaks.
// Scratch home/cache/tmp/output are fresh siblings under the same explicit
// parent as input custody and are removed only after the bounded runner joins.
func (custody *ExecutionGoBuildCustody) ProtectReferenceTool(ctx context.Context, parent, role, binary string) (*ExecutionToolCustody, error) {
	if _, _, _, _, _, err := referenceToolRole(role); err != nil || custody == nil ||
		ctx == nil || ctx.Err() != nil || parent != filepath.Dir(custody.directory) {
		return nil, ErrExecutionToolCustody
	}
	custody.mu.Lock()
	defer custody.mu.Unlock()
	if err := custody.check(ctx); err != nil {
		return nil, ErrExecutionToolCustody
	}
	tool, selection, err := protectExecutionToolCopy(ctx, parent, role, binary)
	if err != nil {
		return tool, err
	}
	identity, err := custody.verifyReferenceTool(ctx, parent, role, selection.Path)
	tool, err = finishExecutionToolCopy(ctx, tool, selection, identity, err)
	if err == nil {
		// Only this measured protected-input issuer binds a source/resource
		// lineage. A legacy observer or caller-authored identity cannot set it.
		tool.referenceInputs = custody
	}
	return tool, err
}

func (custody *ExecutionGoBuildCustody) verifyReferenceTool(ctx context.Context, parent, role, binary string) (identity ExecutionToolIdentity, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	packagePath, modulePath, moduleVersion, moduleSum, recipe, err := referenceToolRole(role)
	if err != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	workspace, err := os.MkdirTemp(parent, "t422-go-build-")
	if err != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	defer func() {
		if os.RemoveAll(workspace) != nil || retErr != nil {
			identity = ExecutionToolIdentity{}
			retErr = ErrExecutionGoBuildCustody
		}
	}()
	for _, name := range []string{"home", "tmp", "cache"} {
		if os.Mkdir(filepath.Join(workspace, name), 0o700) != nil {
			return identity, ErrExecutionGoBuildCustody
		}
	}
	request := ReferenceToolRequest{GoRoot: filepath.Join(custody.directory, "sdk"), ModuleCache: filepath.Join(custody.directory, "modules")}
	goBinary := filepath.Join(request.GoRoot, "bin", "go")
	images := []string{goBinary}
	for _, name := range []string{"asm", "cgo", "compile", "cover", "fix", "link", "preprofile", "vet"} {
		images = append(images, filepath.Join(request.GoRoot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH, name))
	}
	for _, path := range images {
		if validateExternalToolImage(path) != nil {
			return identity, ErrExecutionGoBuildCustody
		}
	}
	suppliedDigest, err := executableidentity.Digest(binary)
	if err != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	supplied, err := buildinfo.ReadFile(binary)
	if err != nil || validateReferenceBuildInfo(supplied, packagePath, custody.reference.source, modulePath, moduleVersion, moduleSum, nil) != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	environment := referenceBuildEnvironment(request, workspace)
	for index, value := range environment {
		if strings.HasPrefix(value, "PATH=") {
			environment[index] = "PATH=" + custody.git.Directory()
		}
	}
	environment = append(environment, "GIT_EXEC_PATH="+custody.git.Directory(), "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR="+os.DevNull)
	run := func(limit int64, args ...string) ([]byte, error) {
		if custody.check(ctx) != nil {
			return nil, ErrExecutionGoBuildCustody
		}
		output, runErr := runReferenceGo(ctx, custody.reference.root.root, goBinary, environment, limit, args...)
		if custody.check(ctx) != nil || runErr != nil {
			return nil, ErrExecutionGoBuildCustody
		}
		return output, nil
	}
	version, err := run(256, "version")
	if err != nil || string(version) != "go version "+runtime.Version()+" "+runtime.GOOS+"/"+runtime.GOARCH+"\n" {
		return identity, ErrExecutionGoBuildCustody
	}
	locations, err := run(2*maxInputCustodyPathBytes+2, "env", "GOROOT", "GOTOOLDIR")
	if err != nil || string(locations) != request.GoRoot+"\n"+filepath.Join(request.GoRoot, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)+"\n" {
		return identity, ErrExecutionGoBuildCustody
	}
	graph, err := run(maxReferenceModuleGraphBytes, "list", "-m", "-json", "all")
	if err != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	modules, err := verifyExecutionModuleGraph(ctx, custody.reference.root.root, request.ModuleCache, graph)
	if err != nil || validateReferenceBuildInfo(supplied, packagePath, custody.reference.source, modulePath, moduleVersion, moduleSum, modules) != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	if _, err := run(64<<10, "mod", "verify"); err != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	output := filepath.Join(workspace, "reference")
	if _, err := run(64<<10, "build", "-trimpath", "-pgo=off", "-buildvcs=true", "-p=1", "-o", output, packagePath); err != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	actual, err := buildinfo.ReadFile(output)
	if err != nil || validateReferenceBuildInfo(actual, packagePath, custody.reference.source, modulePath, moduleVersion, moduleSum, modules) != nil ||
		!reflect.DeepEqual(supplied, actual) || executableidentity.Verify(output, suppliedDigest) != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	afterGraph, err := run(maxReferenceModuleGraphBytes, "list", "-m", "-json", "all")
	if err != nil || !bytes.Equal(graph, afterGraph) {
		return identity, ErrExecutionGoBuildCustody
	}
	if _, err := verifyExecutionModuleGraph(ctx, custody.reference.root.root, request.ModuleCache, afterGraph); err != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	if _, err := run(64<<10, "mod", "verify"); err != nil || custody.reference.verify(ctx) != nil || custody.check(ctx) != nil ||
		executableidentity.Verify(binary, suppliedDigest) != nil {
		return identity, ErrExecutionGoBuildCustody
	}
	identity = ExecutionToolIdentity{Role: role, FileType: regularFileType, SHA256: suppliedDigest,
		Version: "clean commit " + custody.reference.source, Provenance: "go-build-info-vcs-v1", BuildVCSRevision: custody.reference.source}
	if modulePath != "" {
		identity.Version, identity.Provenance, identity.BuildVCSRevision = moduleVersion, "go-module-build-v1", ""
		identity.ModulePath, identity.ModuleVersion, identity.ModuleSum, identity.BuildRecipeSHA256 = modulePath, moduleVersion, moduleSum, recipe
	}
	return identity, nil
}
