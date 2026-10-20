//go:build linux

package t421

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type goReferenceCheckoutGit struct {
	core, digest, workspace string
}

func (git goReferenceCheckoutGit) checkGit(ctx context.Context, digest string) error {
	if ctx == nil || ctx.Err() != nil || !validExecutionSHA256(digest) || digest != git.digest || !executionGitAbsolutePath(git.core) {
		return ErrExecutionGitCustody
	}
	return nil
}

func (git goReferenceCheckoutGit) runInput(ctx context.Context, root string, input io.Reader, limit int64, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	argv := append([]string{"--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.commitGraph=false"}, args...)
	environment := executionGitEnvironment(git.core, filepath.Join(git.workspace, "home"), filepath.Join(git.workspace, "tmp"))
	return runGoReferenceCommand(ctx, git.workspace, root, git.core+"/git", environment, input, limit, "source-git", argv...)
}

func inspectGoReferenceSource(ctx context.Context, plan linuxGoReferencePlan, gitDigest, workspace string) (executionReferenceSource, error) {
	root, err := os.Getwd()
	if err != nil {
		return executionReferenceSource{}, ErrExecutionGoBuildCustody
	}
	core := filepath.Join(root, "inputs", "git", "core")
	for _, name := range executionGitImageNames() {
		if validateExternalToolNativeImage(filepath.Join(core, name)) != nil {
			return executionReferenceSource{}, ErrExecutionGoBuildCustody
		}
	}
	inspection := executionCheckoutInspector{root: filepath.Join(root, "inputs", "source"), digest: gitDigest,
		transport: goReferenceCheckoutGit{core: core, digest: gitDigest, workspace: workspace}}
	raw, err := inspection.run(ctx, maxCheckoutInventoryBytes, "ls-tree", "-rz", "--full-tree", plan.Source)
	if err != nil {
		return executionReferenceSource{}, ErrExecutionGoBuildCustody
	}
	entries, err := executionCheckoutEntries(raw)
	reference := executionReferenceSource{root: inspection, source: plan.Source, tree: plan.Tree, entries: entries, configSHA256: plan.ConfigSHA256}
	if err != nil || reference.verify(ctx) != nil {
		return executionReferenceSource{}, ErrExecutionGoBuildCustody
	}
	return reference, nil
}

func checkGoReferenceCandidate(ctx context.Context, candidate *os.File, plan linuxGoReferencePlan) error {
	if candidate == nil || !plan.valid() {
		return ErrExecutionGoBuildCustody
	}
	seals, sealErr := unix.FcntlInt(candidate.Fd(), unix.F_GET_SEALS, 0)
	flags, flagErr := unix.FcntlInt(candidate.Fd(), unix.F_GETFL, 0)
	fdFlags, fdErr := unix.FcntlInt(candidate.Fd(), unix.F_GETFD, 0)
	if sealErr != nil || seals&linuxInputSeals != linuxInputSeals || flagErr != nil || flags&unix.O_ACCMODE != unix.O_RDONLY ||
		fdErr != nil || fdFlags&unix.FD_CLOEXEC == 0 {
		return ErrExecutionGoBuildCustody
	}
	digest, err := digestExecutionReferenceFD(ctx, candidate)
	if err != nil || digest != plan.CandidateSHA256 {
		return ErrExecutionGoBuildCustody
	}
	info, err := buildinfo.Read(candidate)
	packagePath, modulePath, version, sum, _, roleErr := referenceToolRoleForSchema(plan.Role, plan.Schema, plan.Source)
	if err != nil || roleErr != nil || validateReferenceBuildInfoForSchema(info, plan.Role, plan.Schema, packagePath, plan.Source, modulePath, version, sum, nil) != nil {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

func buildGoReference(ctx context.Context, plan linuxGoReferencePlan, workspace string, reference executionReferenceSource, candidate *os.File) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	root, err := os.Getwd()
	if err != nil || !plan.valid() {
		return goReferenceBuildRefusal("build-inputs")
	}
	if checkGoReferenceCandidate(ctx, candidate, plan) != nil {
		return goReferenceBuildRefusal("candidate-before")
	}
	if reference.verify(ctx) != nil {
		return goReferenceBuildRefusal("source-before")
	}
	inputs := filepath.Join(root, "inputs")
	request := ReferenceToolRequest{GoRoot: filepath.Join(inputs, "sdk"), ModuleCache: filepath.Join(inputs, "modules")}
	goBinary := filepath.Join(request.GoRoot, "bin", "go")
	images := []string{goBinary}
	for _, name := range []string{"asm", "cgo", "compile", "cover", "fix", "link", "preprofile", "vet"} {
		images = append(images, filepath.Join(request.GoRoot, "pkg", "tool", "linux_amd64", name))
	}
	for _, path := range images {
		if validateExternalToolNativeImage(path) != nil {
			return goReferenceBuildRefusal("sdk-images")
		}
	}
	packagePath, modulePath, version, sum, _, err := referenceToolRoleForSchema(plan.Role, plan.Schema, plan.Source)
	if err != nil {
		return goReferenceBuildRefusal("role")
	}
	supplied, err := buildinfo.Read(candidate)
	if err != nil {
		return goReferenceBuildRefusal("candidate-info")
	}
	environment := referenceBuildEnvironment(request, workspace)
	gitCore := filepath.Join(inputs, "git", "core")
	for i, binding := range environment {
		if strings.HasPrefix(binding, "PATH=") {
			environment[i] = "PATH=" + gitCore
		}
	}
	environment = append(environment, "GIT_EXEC_PATH="+gitCore, "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR="+os.DevNull)
	runFrom := func(cwd, phase string, limit int64, args ...string) ([]byte, error) {
		commandCtx, commandCancel := context.WithTimeout(ctx, 10*time.Minute)
		defer commandCancel()
		return runGoReferenceCommand(commandCtx, workspace, cwd, goBinary, environment, nil, limit, phase, args...)
	}
	run := func(phase string, limit int64, args ...string) ([]byte, error) {
		return runFrom(reference.root.root, phase, limit, args...)
	}
	versionOutput, err := run("version", 256, "version")
	if err != nil || string(versionOutput) != "go version "+runtime.Version()+" linux/amd64\n" {
		return goReferenceBuildRefusal("version")
	}
	locations, err := run("locations", 2*maxInputCustodyPathBytes+2, "env", "GOROOT", "GOTOOLDIR")
	if err != nil || string(locations) != request.GoRoot+"\n"+filepath.Join(request.GoRoot, "pkg", "tool", "linux_amd64")+"\n" {
		return goReferenceBuildRefusal("locations")
	}
	graph, err := run("module-graph", maxReferenceModuleGraphBytes, "list", "-m", "-json", "all")
	if err != nil {
		return goReferenceBuildRefusal("graph")
	}
	modules, err := verifyExecutionModuleGraph(ctx, reference.root.root, request.ModuleCache, graph)
	if err != nil || validateReferenceBuildInfoForSchema(supplied, plan.Role, plan.Schema, packagePath, plan.Source, modulePath, version, sum, modules) != nil {
		return goReferenceBuildRefusal("graph-checksums-candidate")
	}
	if _, err := run("module-verify", 64<<10, "mod", "verify"); err != nil {
		return goReferenceBuildRefusal("module-verify")
	}
	output := filepath.Join(workspace, "reference")
	buildRoot, buildArgs, checkOverlay, err := referenceToolBuildArgs(ctx, plan.Role, plan.Schema, reference.root.root, request.ModuleCache, workspace, output, packagePath)
	if err != nil {
		return goReferenceBuildRefusal("overlay-prepare")
	}
	checkGraph := func() error {
		if buildRoot == reference.root.root {
			return nil
		}
		overlayGraph, err := runFrom(buildRoot, "overlay-graph", maxReferenceModuleGraphBytes, "list", "-modfile=v3.mod", "-m", "-json", "all")
		if err != nil {
			return ErrExecutionGoBuildCustody
		}
		return verifyZoektOfferGraph(graph, overlayGraph, reference.root.root, buildRoot)
	}
	if checkGraph() != nil {
		return goReferenceBuildRefusal("overlay-graph-before")
	}
	if _, err := runFrom(buildRoot, "build", 64<<10, buildArgs...); err != nil {
		return goReferenceBuildRefusal("build")
	}
	if checkOverlay() != nil {
		return goReferenceBuildRefusal("overlay-after")
	}
	if checkGraph() != nil {
		return goReferenceBuildRefusal("overlay-graph-after")
	}
	actual, err := buildinfo.ReadFile(output)
	if err != nil || validateReferenceBuildInfoForSchema(actual, plan.Role, plan.Schema, packagePath, plan.Source, modulePath, version, sum, modules) != nil || !reflect.DeepEqual(supplied, actual) {
		return goReferenceBuildRefusal("output-info")
	}
	file, err := os.Open(output)
	if err != nil {
		return goReferenceBuildRefusal("output-open")
	}
	digest, compareErr := compareGoReferenceOutput(ctx, candidate, file)
	closeErr := file.Close()
	if compareErr != nil || closeErr != nil || digest != plan.CandidateSHA256 {
		return goReferenceBuildRefusal("candidate-compare")
	}
	afterGraph, err := run("module-graph-after", maxReferenceModuleGraphBytes, "list", "-m", "-json", "all")
	if err != nil || !bytes.Equal(graph, afterGraph) {
		return goReferenceBuildRefusal("after-graph")
	}
	if _, err := verifyExecutionModuleGraph(ctx, reference.root.root, request.ModuleCache, afterGraph); err != nil {
		return goReferenceBuildRefusal("after-graph-checksums")
	}
	if _, err := run("module-verify-after", 64<<10, "mod", "verify"); err != nil {
		return goReferenceBuildRefusal("module-verify-after")
	}
	if reference.verify(ctx) != nil {
		return goReferenceBuildRefusal("source-after")
	}
	if checkGoReferenceCandidate(ctx, candidate, plan) != nil {
		return goReferenceBuildRefusal("candidate-after")
	}
	return digest, nil
}

func goReferenceBuildRefusal(step string) (string, error) {
	_, _ = fmt.Fprintf(os.Stderr, "reference build refused step=%s\n", step)
	return "", ErrExecutionGoBuildCustody
}

const maxGoReferenceCommandStderrBytes = 64 << 10

func runGoReferenceCommand(ctx context.Context, workspace, cwd, binary string, environment []string, input io.Reader, limit int64, phase string, args ...string) ([]byte, error) {
	if ctx == nil || ctx.Err() != nil || limit < 0 || limit > maxCheckoutInventoryBytes {
		return nil, ErrExecutionGoBuildCustody
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env, command.Stdin = cwd, environment, input
	stdout, stderr := checkoutCommandOutput{remaining: limit, cancel: cancel}, checkoutCommandOutput{remaining: maxGoReferenceCommandStderrBytes, cancel: cancel}
	command.Stdout, command.Stderr, command.WaitDelay = &stdout, &stderr, time.Second
	if prepareReferenceCommand(command) != nil || command.Start() != nil {
		retained := retainGoReferenceCommandStderr(workspace, stderr.buffer.Bytes()) == nil
		_, _ = fmt.Fprintf(os.Stderr, "reference command refused phase=%s boundary=start root_waited=false command_stderr_retained=%t\n", phase, retained)
		return nil, ErrExecutionGoBuildCustody
	}
	waitErr := command.Wait()
	orphanErr := reapGoBuildNamespaceOrphans(ctx)
	if waitErr != nil || orphanErr != nil || stdout.err != nil || stderr.err != nil || ctx.Err() != nil {
		retained := retainGoReferenceCommandStderr(workspace, stderr.buffer.Bytes()) == nil
		exitCode := -1
		if command.ProcessState != nil {
			exitCode = command.ProcessState.ExitCode()
		}
		_, _ = fmt.Fprintf(os.Stderr, "reference command refused phase=%s boundary=wait-drain wait_returned=true process_state=%t exit_code=%d orphan_drained=%t command_stderr_retained=%t\n",
			phase, command.ProcessState != nil, exitCode, orphanErr == nil, retained)
		return nil, ErrExecutionGoBuildCustody
	}
	return stdout.buffer.Bytes(), nil
}

// The first failed fixed command stops this recipe. Keep only its bounded raw
// stderr in the private work directory; public errors never carry its content.
func retainGoReferenceCommandStderr(workspace string, raw []byte) error {
	if !executionGitAbsolutePath(workspace) || len(raw) > maxGoReferenceCommandStderrBytes {
		return ErrExecutionGoBuildCustody
	}
	file, err := os.OpenFile(filepath.Join(workspace, "command.stderr"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrExecutionGoBuildCustody
	}
	n, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil || n != len(raw) || closeErr != nil {
		return ErrExecutionGoBuildCustody
	}
	return nil
}

func compareGoReferenceOutput(ctx context.Context, candidate, output *os.File) (string, error) {
	before, err := output.Stat()
	selected, selectedErr := candidate.Stat()
	if err != nil || selectedErr != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o111 == 0 ||
		before.Size() <= 0 || before.Size() > maxInputCustodyFileBytes || before.Size() != selected.Size() {
		return "", ErrExecutionGoBuildCustody
	}
	left, right := make([]byte, treeWireChunk), make([]byte, treeWireChunk)
	for offset := int64(0); offset < before.Size(); {
		count := min(int64(len(left)), before.Size()-offset)
		if ctx.Err() != nil {
			return "", ErrExecutionGoBuildCustody
		}
		n, leftErr := candidate.ReadAt(left[:count], offset)
		m, rightErr := output.ReadAt(right[:count], offset)
		if leftErr != nil || rightErr != nil || int64(n) != count || n != m || !bytes.Equal(left[:n], right[:m]) {
			return "", ErrExecutionGoBuildCustody
		}
		offset += count
	}
	after, err := output.Stat()
	if err != nil || !sameCheckoutFile(before, after) {
		return "", ErrExecutionGoBuildCustody
	}
	return digestExecutionReferenceFD(ctx, output)
}
