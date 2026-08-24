package t4013

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	privateToolchainSchema   = "t4013-private-toolchain-v1"
	processProbeTimeout      = 2 * time.Second
	maxProcessProbeBytes     = 128 << 10
	maxProcessSnapshotRows   = 8192
	maxProcessDescendants    = 128
	maxProcessChildLifetimes = 8192
	maxProcessSampleAttempts = 3
	processSampleRetryDelay  = 25 * time.Millisecond
)

var errPrivateServerShutdownUnproven = errors.New("T40.13 private process session shutdown is unproven")

var errProcessSamplingFailed = errors.New("T40.13 process sampling failed")

var errProcessIdentityMissing = errors.New("T40.13 process disappeared before identity capture")

var errProcessChildIdentityDisappeared = errors.New("T40.13 child disappeared before identity capture")

var errProcessRootExitTransition = errors.New("T40.13 process root exited during snapshot capture")

type privateToolchain struct {
	Schema            string
	Phebs             string
	Zoekt             string
	Focused           string
	Buf               string
	TempDir           string
	ClosedEnvironment bool
	controls          executionControls
	controlsDigest    string
	extraEnvironment  []string
	host              hostToolchainBinding
	digests           [4]string
}

type privateServer struct {
	command         *exec.Cmd
	sessionIsolated bool
	started         time.Time
	done            chan error
	log             *os.File
	logPath         string
	sampler         *rssSampler
	stopOnce        sync.Once
	stopErr         error
}

type rssSampler struct {
	pid                 int
	strict              bool
	stop                chan struct{}
	done                chan struct{}
	sampleMu            sync.Mutex
	mu                  sync.Mutex
	peakRSS             int64
	samples             int64
	gitChildren         map[int]struct{}
	indexChildren       map[int]struct{}
	otherChildren       map[int]struct{}
	activeChildren      map[int]sampledProcess
	identityProbe       func(int, processSnapshot) (processIdentityObservation, error)
	snapshotProbe       func(context.Context) ([]byte, error)
	nativeSnapshotProbe func(context.Context, int) ([]int, map[int]processSnapshot, error)
	retryWait           func(context.Context) error
	strictGitChildren   int64
	strictIndexChildren int64
	strictOtherChildren int64
	rootIdentity        processIdentity
	rootSeen            bool
	rootExited          bool
	rootWait            chan struct{}
	rootExitOnce        sync.Once
	firstErr            error
	failedSamples       uint64
	closeOnce           sync.Once
	closeErr            error
}

type processIdentity struct {
	pid   int
	token string
}

type processIdentityObservation struct {
	token  string
	parent int
	name   string
}

type processClass uint8

const (
	processClassOther processClass = iota
	processClassGit
	processClassIndex
)

type sampledProcess struct {
	identity processIdentity
	class    processClass
}

type frozenSourceExportContract uint8

const (
	frozenSourceExportLegacy frozenSourceExportContract = iota
	frozenSourceExportV25
)

func frozenSourceExportContractForPlan(schema string) frozenSourceExportContract {
	if planSchemaVersion(schema) >= 25 {
		return frozenSourceExportV25
	}
	return frozenSourceExportLegacy
}

func buildPrivateToolchain(
	ctx context.Context, moduleRoot, workspace, controlsDigest string, plan Plan, hostTools hostToolchainBinding,
) (toolchain privateToolchain, metrics PhaseMetrics, retErr error) {
	if ctx == nil || !filepath.IsAbs(moduleRoot) || !filepath.IsAbs(workspace) ||
		(frozenSourceExportContractForPlan(plan.Schema) == frozenSourceExportV25 && !hexIdentity(plan.SourceCommit, 40)) {
		return privateToolchain{}, PhaseMetrics{}, errors.New("T40.13 toolchain scope is invalid")
	}
	v25 := planSchemaVersion(plan.Schema) >= 25
	started := time.Now()
	privateTemp := ""
	var controls executionControls
	if v25 {
		var err error
		controls, err = openExecutionControls(workspace, controlsDigest, hostTools, true)
		if err != nil {
			return privateToolchain{}, PhaseMetrics{}, err
		}
		privateTemp = controls.Temp
	}
	var allocation *allocationSampler
	if v25 {
		_, allocated, err := measureDataBytesForContract(workspace, true)
		if err != nil {
			return privateToolchain{}, PhaseMetrics{}, err
		}
		allocation, err = newAllocationSampler(workspace, allocated, true)
		if err != nil {
			return privateToolchain{}, PhaseMetrics{}, err
		}
		defer func() {
			logical, allocated, measureErr := measureDataBytesForContract(workspace, true)
			peakAllocated, allocationErr := allocation.close()
			metrics.WallMS = time.Since(started).Milliseconds()
			metrics.DataLogicalBytes = max(metrics.DataLogicalBytes, logical)
			metrics.DataAllocatedBytes = max(metrics.DataAllocatedBytes, allocated, peakAllocated)
			retErr = errors.Join(retErr, measureErr, allocationErr)
		}()
	}
	source := filepath.Join(workspace, "toolchain-source")
	if v25 {
		exportMetrics, exportErr := exportReviewedSourceMeasuredWithBoundGit(
			ctx, moduleRoot, plan.SourceCommit, source, workspace, hostTools.gitCore, controls,
		)
		var combinedErr error
		metrics, combinedErr = mergeMetricsPreservingError(exportErr, metrics, exportMetrics)
		if combinedErr != nil {
			return privateToolchain{}, metrics, combinedErr
		}
	} else if err := exportFrozenSourceForPlan(ctx, moduleRoot, plan, source); err != nil {
		return privateToolchain{}, PhaseMetrics{}, err
	}
	output := filepath.Join(workspace, "toolchain")
	if err := os.Mkdir(output, 0o700); err != nil {
		return privateToolchain{}, metrics, err
	}
	buildCache := filepath.Join(workspace, "go-build-cache")
	moduleCacheDigest := ""
	if v25 {
		buildCache = controls.BuildCache
		for _, path := range []string{controls.ModuleCache, buildCache} {
			if err := os.Mkdir(path, 0o700); err != nil {
				return privateToolchain{}, metrics, errors.New("T40.13 private Go cache is not new")
			}
		}
		goPath, err := hostTools.goDriver.pathForLaunch(ctx)
		if err != nil {
			return privateToolchain{}, metrics, err
		}
		command := exec.CommandContext(ctx, goPath, "list", "-deps",
			"./cmd/phebs", "github.com/sourcegraph/zoekt/cmd/zoekt-git-index",
			"./cmd/phebs-focused-index", "github.com/bufbuild/buf/cmd/buf")
		command.Dir = source
		command.Env = executionEnvironmentForControls(controls, true)
		command.Stdout, command.Stderr = io.Discard, io.Discard
		commandMetrics, commandErr := runMeasuredCommand(command, workspace, true)
		if commandErr != nil {
			commandErr = sanitizeMeasuredCommandFailure("T40.13 private module hydration failed", commandErr)
		}
		metrics, err = mergeMetricsPreservingError(commandErr, metrics, commandMetrics)
		if err != nil {
			return privateToolchain{}, metrics, err
		}
		goPath, err = hostTools.goDriver.pathForLaunch(ctx)
		if err != nil {
			return privateToolchain{}, metrics, err
		}
		command = exec.CommandContext(ctx, goPath, "mod", "verify")
		command.Dir = source
		command.Env = executionEnvironmentForControls(controls, true)
		command.Stdout, command.Stderr = io.Discard, io.Discard
		commandMetrics, commandErr = runMeasuredCommand(command, workspace, true)
		if commandErr != nil {
			commandErr = sanitizeMeasuredCommandFailure("T40.13 private module verification failed", commandErr)
		}
		metrics, err = mergeMetricsPreservingError(commandErr, metrics, commandMetrics)
		if err != nil {
			return privateToolchain{}, metrics, err
		}
		moduleCacheDigest, err = privateCacheDigest(ctx, controls.ModuleCache)
		if err != nil {
			return privateToolchain{}, metrics, err
		}
	}
	toolchain = privateToolchain{
		Schema: privateToolchainSchema,
		Phebs:  filepath.Join(output, "phebs"), Zoekt: filepath.Join(output, "zoekt-git-index"),
		Focused: filepath.Join(output, "phebs-focused-index"), Buf: filepath.Join(output, "buf"),
		TempDir:           privateTemp,
		ClosedEnvironment: v25,
		controls:          controls,
		controlsDigest:    controlsDigest,
		host:              hostTools,
	}
	builds := []struct {
		output string
		args   []string
		env    []string
	}{
		{toolchain.Phebs, []string{"build", "-trimpath", "-o", toolchain.Phebs, "./cmd/phebs"}, nil},
		{toolchain.Zoekt, []string{"build", "-trimpath", "-o", toolchain.Zoekt, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index"}, nil},
		{toolchain.Focused, []string{"build", "-trimpath", "-o", toolchain.Focused, "./cmd/phebs-focused-index"}, nil},
		{toolchain.Buf, []string{"build", "-trimpath", "-o", toolchain.Buf, "github.com/bufbuild/buf/cmd/buf"}, []string{"CGO_ENABLED=0"}},
	}
	for _, build := range builds {
		if _, err := os.Lstat(build.output); err == nil || !os.IsNotExist(err) {
			return privateToolchain{}, metrics, errors.New("T40.13 toolchain output already exists")
		}
		goPath := "go"
		if v25 {
			var pathErr error
			goPath, pathErr = hostTools.goDriver.pathForLaunch(ctx)
			if pathErr != nil {
				return privateToolchain{}, metrics, pathErr
			}
		}
		command := exec.CommandContext(ctx, goPath, build.args...)
		command.Dir = source
		command.Env = executionEnvironmentForPlan(plan, privateTemp)
		if v25 {
			command.Env = executionEnvironmentForControls(controls, false)
		}
		command.Env = append(command.Env, build.env...)
		if v25 {
			command.Stdout, command.Stderr = io.Discard, io.Discard
			commandMetrics, commandErr := runMeasuredCommand(command, workspace, true)
			if commandErr != nil {
				commandErr = sanitizeMeasuredCommandFailure("T40.13 toolchain build failed", commandErr)
			}
			metrics, combinedErr := mergeMetricsPreservingError(commandErr, metrics, commandMetrics)
			if combinedErr != nil {
				return privateToolchain{}, metrics, combinedErr
			}
		} else if output, err := command.CombinedOutput(); err != nil {
			_ = output
			return privateToolchain{}, PhaseMetrics{}, errors.New("T40.13 toolchain build failed")
		}
	}
	if v25 {
		after, err := privateCacheDigest(ctx, controls.ModuleCache)
		if err != nil || after != moduleCacheDigest {
			return privateToolchain{}, metrics,
				errors.Join(err, errors.New("T40.13 private module cache changed during toolchain build"))
		}
		for _, path := range []string{controls.ModuleCache, buildCache} {
			if err := removePrivateGoCache(path); err != nil {
				return privateToolchain{}, metrics, fmt.Errorf("T40.13 private Go cache cleanup failed: %w", err)
			}
		}
		if err := syncDirectory(filepath.Dir(controls.ModuleCache)); err != nil {
			return privateToolchain{}, metrics, fmt.Errorf("persist T40.13 private Go cache cleanup: %w", err)
		}
		if err := validateExecutionControlPaths(controls, true); err != nil {
			return privateToolchain{}, metrics, err
		}
		if _, err := bindPrivateToolchain(ctx, &toolchain); err != nil {
			return privateToolchain{}, metrics, err
		}
	}
	return toolchain, metrics, nil
}

func runCustodyCombinedOutput(command *exec.Cmd) ([]byte, error) {
	if command == nil {
		return nil, errors.New("T40.13 custody command is invalid")
	}
	if err := isolatePrivateServerSession(command); err != nil {
		return nil, err
	}
	output, commandErr := command.CombinedOutput()
	if command.Process == nil {
		return output, commandErr
	}
	return output, errors.Join(
		commandErr,
		signaledCommandShutdownUnproven(commandErr),
		finishCustodyCommandSession(command.Process.Pid),
	)
}

func signaledCommandShutdownUnproven(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == -1 {
		return errPrivateServerShutdownUnproven
	}
	return nil
}

func finishCustodyCommandSession(pid int) error {
	sessionErr := waitPrivateServerSession(pid, time.Now().Add(5*time.Second))
	if sessionErr == nil {
		return nil
	}
	killErr := killPrivateServerSession(pid)
	killWaitErr := waitPrivateServerSession(pid, time.Now().Add(5*time.Second))
	return errors.Join(
		errors.New("T40.13 custody command left a surviving process session"),
		errPrivateServerShutdownUnproven, sessionErr, killErr, killWaitErr,
	)
}

func custodyRetentionCause(ctx context.Context, cause error) error {
	if errors.Is(cause, errPrivateServerShutdownUnproven) {
		return errPrivateServerShutdownUnproven
	}
	if errors.Is(cause, context.Canceled) {
		return context.Canceled
	}
	if ctx == nil {
		return nil
	}
	ctxCause := context.Cause(ctx)
	if errors.Is(ctxCause, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(ctxCause, context.DeadlineExceeded) && !errors.Is(ctxCause, errTotalWallDeadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func exportFrozenSourceForPlan(ctx context.Context, moduleRoot string, plan Plan, output string) error {
	if frozenSourceExportContractForPlan(plan.Schema) == frozenSourceExportLegacy {
		return exportFrozenSource(ctx, moduleRoot, output)
	}
	return exportReviewedSource(ctx, moduleRoot, plan.SourceCommit, output)
}

// exportFrozenSource preserves the exact V1-V24 git archive contract.
func exportFrozenSource(ctx context.Context, moduleRoot, output string) error {
	if err := os.Mkdir(output, 0o700); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "git", "archive", "--format=tar", "HEAD")
	command.Dir = moduleRoot
	command.Env = legacyExecutionEnvironment()
	return extractFrozenSourceCommand(command, output)
}

func extractFrozenSourceCommand(command *exec.Cmd, output string) error {
	stream, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	if err := extractFrozenSource(stream, output); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return err
	}
	if err := command.Wait(); err != nil {
		return errors.New("T40.13 frozen source export failed")
	}
	return nil
}

func extractFrozenSourceCommandMeasured(
	command *exec.Cmd, output, dataDir string,
) (PhaseMetrics, error) {
	if command == nil || !filepath.IsAbs(output) || !filepath.IsAbs(dataDir) {
		return PhaseMetrics{}, errors.New("T40.13 measured source export is invalid")
	}
	_, allocatedBefore, err := measureDataBytesForContract(dataDir, true)
	if err != nil {
		return PhaseMetrics{}, err
	}
	allocation, err := newAllocationSampler(dataDir, allocatedBefore, true)
	if err != nil {
		return PhaseMetrics{}, err
	}
	closeAllocation := func() error {
		_, closeErr := allocation.close()
		return closeErr
	}
	if err := isolatePrivateServerSession(command); err != nil {
		return PhaseMetrics{}, errors.Join(err, closeAllocation())
	}
	stream, err := command.StdoutPipe()
	if err != nil {
		return PhaseMetrics{}, errors.Join(err, closeAllocation())
	}
	started := time.Now()
	if err := command.Start(); err != nil {
		return PhaseMetrics{}, errors.Join(err, closeAllocation())
	}
	sampler := newRSSSampler(command.Process.Pid, true)
	sampler.captureRootIdentity()
	sampler.sample()
	sampler.expectConcurrentRootWait()
	go sampler.run()
	extractErr := extractFrozenSource(stream, output)
	if extractErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	sampler.observeRootExit()
	sessionErr := finishCustodyCommandSession(command.Process.Pid)
	_ = sampler.close()
	logical, allocated, measureErr := measureDataBytesForContract(dataDir, true)
	peakAllocated, allocationErr := allocation.close()
	allocated = max(allocated, peakAllocated)
	metrics := PhaseMetrics{
		WallMS: time.Since(started).Milliseconds(), DataLogicalBytes: logical,
		DataAllocatedBytes: allocated, GitChildren: 1,
	}
	var descendantGit int64
	var samplerErr error
	metrics.PeakRSSBytes, descendantGit, metrics.IndexChildren, metrics.OtherChildren, samplerErr = sampler.metrics()
	var addErr error
	metrics.GitChildren, addErr = checkedAddInt64(metrics.GitChildren, descendantGit)
	return metrics, errors.Join(
		extractErr, waitErr, signaledCommandShutdownUnproven(waitErr),
		sessionErr, samplerErr, addErr, measureErr, allocationErr,
	)
}

func exportReviewedSource(ctx context.Context, moduleRoot, sourceCommit, output string) error {
	gitCore, err := resolveGitCoreExecutable(ctx)
	if err != nil {
		return err
	}
	return exportReviewedSourceWith(
		ctx, moduleRoot, sourceCommit, output, gitCore, extractFrozenSourceCommand,
	)
}

func exportReviewedSourceMeasured(
	ctx context.Context, moduleRoot, sourceCommit, output, measureRoot string,
) (PhaseMetrics, error) {
	gitCore, err := resolveGitCoreExecutable(ctx)
	if err != nil {
		return PhaseMetrics{}, err
	}
	return exportReviewedSourceMeasuredWithGit(
		ctx, moduleRoot, sourceCommit, output, measureRoot, gitCore,
	)
}

func exportReviewedSourceMeasuredWithGit(
	ctx context.Context, moduleRoot, sourceCommit, output, measureRoot, gitCore string,
) (PhaseMetrics, error) {
	return exportReviewedSourceMeasuredWithResolver(
		ctx, moduleRoot, sourceCommit, output, measureRoot,
		func() (string, error) { return gitCore, nil }, nil,
	)
}

func exportReviewedSourceMeasuredWithBoundGit(
	ctx context.Context, moduleRoot, sourceCommit, output, measureRoot string,
	git boundExecutable, controls executionControls,
) (PhaseMetrics, error) {
	return exportReviewedSourceMeasuredWithResolver(
		ctx, moduleRoot, sourceCommit, output, measureRoot,
		func() (string, error) { return git.pathForLaunch(ctx) },
		executionEnvironmentForControls(controls, false),
	)
}

func exportReviewedSourceMeasuredWithResolver(
	ctx context.Context, moduleRoot, sourceCommit, output, measureRoot string,
	gitPath func() (string, error),
	environment []string,
) (PhaseMetrics, error) {
	var metrics PhaseMetrics
	err := exportReviewedSourceWithResolver(ctx, moduleRoot, sourceCommit, output, gitPath, environment, func(command *exec.Cmd, output string) error {
		var err error
		metrics, err = extractFrozenSourceCommandMeasured(command, output, measureRoot)
		return err
	})
	return metrics, err
}

func exportReviewedSourceWith(
	ctx context.Context,
	moduleRoot, sourceCommit, output, gitCore string,
	extract func(*exec.Cmd, string) error,
) (retErr error) {
	if ctx == nil || !filepath.IsAbs(moduleRoot) || !filepath.IsAbs(output) ||
		!filepath.IsAbs(gitCore) || !hexIdentity(sourceCommit, 40) {
		return errors.New("T40.13 frozen source export scope is invalid")
	}
	return exportReviewedSourceWithResolver(
		ctx, moduleRoot, sourceCommit, output,
		func() (string, error) { return gitCore, nil }, nil, extract,
	)
}

func exportReviewedSourceWithResolver(
	ctx context.Context,
	moduleRoot, sourceCommit, output string,
	gitPath func() (string, error),
	environment []string,
	extract func(*exec.Cmd, string) error,
) (retErr error) {
	if ctx == nil || !filepath.IsAbs(moduleRoot) || !filepath.IsAbs(output) ||
		!hexIdentity(sourceCommit, 40) || gitPath == nil {
		return errors.New("T40.13 frozen source export scope is invalid")
	}
	if extract == nil {
		return errors.New("T40.13 frozen source export is unavailable")
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		return err
	}
	gitCore, err := gitPath()
	if err != nil || !filepath.IsAbs(gitCore) {
		return errors.Join(err, errors.New("T40.13 frozen source Git executable is invalid"))
	}
	gitEnvironment := environment
	if gitEnvironment == nil {
		gitEnvironment = gitEnvironmentForContract(true)
	}
	objectPath, err := gitOutputWithExecutableEnvironment(ctx, moduleRoot, gitCore, gitEnvironment,
		"rev-parse", "--git-path", "objects")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(objectPath) {
		objectPath = filepath.Join(moduleRoot, objectPath)
	}
	objectPath, err = filepath.EvalSymlinks(objectPath)
	if err != nil {
		return errors.New("T40.13 frozen source object database is invalid")
	}
	objectInfo, err := os.Lstat(objectPath)
	if err != nil || !objectInfo.IsDir() || objectInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("T40.13 frozen source object database is invalid")
	}

	gitDir, err := os.MkdirTemp(filepath.Dir(output), ".t4013-git-export-")
	if err != nil {
		return err
	}
	defer func() {
		if custodyRetentionCause(ctx, retErr) == nil {
			retErr = errors.Join(retErr, os.RemoveAll(gitDir))
		}
	}()
	if err := os.Mkdir(filepath.Join(gitDir, "objects"), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(gitDir, "refs"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/unused\n"), 0o600); err != nil {
		return err
	}

	gitCore, err = gitPath()
	if err != nil || !filepath.IsAbs(gitCore) {
		return errors.Join(err, errors.New("T40.13 frozen source Git executable is invalid"))
	}
	command := exec.CommandContext(ctx, gitCore, "-c", "core.attributesFile="+os.DevNull,
		"archive", "--format=tar", sourceCommit)
	command.Dir = moduleRoot
	command.Env = append(slices.Clone(gitEnvironment),
		"GIT_DIR="+gitDir,
		"GIT_OBJECT_DIRECTORY="+objectPath,
		"GIT_NO_REPLACE_OBJECTS=1",
	)
	return extract(command, output)
}

func extractFrozenSource(stream io.Reader, output string) error {
	if stream == nil || !filepath.IsAbs(output) {
		return errors.New("T40.13 frozen source extraction scope is invalid")
	}
	reader := tar.NewReader(stream)
	entries := 0
	var total int64
	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return errors.New("T40.13 frozen source archive is invalid")
		}
		entries++
		if entries > 100_000 || header.Size < 0 || header.Size > 2<<30 || total > 2<<30-header.Size {
			return errors.New("T40.13 frozen source archive exceeds its bound")
		}
		total += header.Size
		if header.Typeflag == tar.TypeXHeader || header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name, err := frozenArchiveName(header)
		if err != nil {
			return err
		}
		path := filepath.Join(output, name)
		if !isWithin(path, output) {
			return errors.New("T40.13 frozen source archive path escaped custody")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, byte(0):
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if header.FileInfo().Mode()&0o111 != 0 {
				mode = 0o700
			}
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return errors.Join(copyErr, closeErr)
			}
		default:
			return fmt.Errorf("T40.13 frozen source archive contains entry type %d", header.Typeflag)
		}
	}
	return nil
}

func frozenArchiveName(header *tar.Header) (string, error) {
	if header == nil {
		return "", errors.New("T40.13 frozen source archive header is missing")
	}
	name := header.Name
	if strings.HasSuffix(name, "/") {
		if header.Typeflag != tar.TypeDir {
			return "", errors.New("T40.13 frozen source archive entry type is invalid")
		}
		name = strings.TrimSuffix(name, "/")
	}
	if name == "" || name == "." || name == ".." || strings.Contains(name, "\\") ||
		pathpkg.IsAbs(name) || pathpkg.Clean(name) != name || strings.HasPrefix(name, "../") {
		return "", errors.New("T40.13 frozen source archive escaped custody")
	}
	return filepath.FromSlash(name), nil
}

const (
	maxStartupLogBytes = 64 << 20
	startupLogPrefix   = "T40.13 startup lifecycle: "
)

func launchPrivateServer(
	ctx context.Context,
	profile PreparedProfile,
	toolchain privateToolchain,
	label string,
) (*privateServer, error) {
	if ctx == nil || toolchain.Schema != privateToolchainSchema ||
		label == "" || strings.ContainsAny(label, "/\\: ") {
		return nil, errors.New("T40.13 server start is invalid")
	}
	if err := revalidatePrivateToolchain(ctx, toolchain); err != nil {
		return nil, err
	}
	logPath := filepath.Join(filepath.Dir(profile.Config), "server-"+label+".log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, toolchain.Phebs, "serve", "-config", profile.Config)
	if toolchain.ClosedEnvironment {
		if err := isolatePrivateServerSession(command); err != nil {
			_ = logFile.Close()
			return nil, err
		}
	}
	command.Stdout, command.Stderr = logFile, logFile
	if err := validatePrivateTemporaryDirectory(toolchain); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	command.Env = append(executionEnvironmentForToolchain(toolchain),
		"PHEBS_ZOEKT_GIT_INDEX="+toolchain.Zoekt,
		"PHEBS_FOCUSED_INDEX="+toolchain.Focused,
		"PHEBS_BUF="+toolchain.Buf,
		"PHEBS_T4013_STARTUP_DIAGNOSTICS=source-free-v1",
	)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	server := &privateServer{
		command: command, started: time.Now(), done: make(chan error, 1), log: logFile, logPath: logPath,
		sampler: newRSSSampler(command.Process.Pid, toolchain.ClosedEnvironment),
	}
	if toolchain.ClosedEnvironment {
		server.sessionIsolated = true
		server.sampler.captureRootIdentity()
		server.sampler.sample()
		server.sampler.expectConcurrentRootWait()
	}
	go func() {
		waitErr := command.Wait()
		server.sampler.observeRootExit()
		server.done <- waitErr
	}()
	go server.sampler.run()
	return server, nil
}

func awaitPrivateServerHealth(
	ctx context.Context,
	server *privateServer,
	profile PreparedProfile,
	label string,
	deadlineLimit time.Duration,
) (ServerStartupObservation, error) {
	if ctx == nil || server == nil || deadlineLimit <= 0 {
		return ServerStartupObservation{}, errors.New("T40.13 server health wait is invalid")
	}
	inspector, err := newProfileInspector(profile, profileInspectionLegacy)
	if err != nil {
		observation, observeErr := observeServerStartup(server, profile.Name, label, "inspector_error", "not_attempted", 0)
		return observation, errors.Join(err, observeErr)
	}
	deadline := time.NewTimer(deadlineLimit)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var attempts int64
	for {
		attempts++
		healthClass, healthErr := inspector.healthClass(ctx, profile)
		if healthErr == nil {
			return observeServerStartup(server, profile.Name, label, "healthy", "ok", attempts)
		}
		select {
		case err := <-server.done:
			// Preserve the single wait result for the idempotent stop path, which
			// owns sampler and log closure even when readiness observes exit first.
			server.done <- err
			observation, observeErr := observeServerStartup(server, profile.Name, label, "exited", healthClass, attempts)
			return observation, errors.Join(err, observeErr, errors.New("T40.13 server exited before health"))
		case <-deadline.C:
			observation, observeErr := observeServerStartup(server, profile.Name, label, "deadline", healthClass, attempts)
			return observation, errors.Join(observeErr, errors.New("T40.13 server health deadline expired"))
		case <-ticker.C:
		case <-ctx.Done():
			observation, observeErr := observeServerStartup(server, profile.Name, label, "canceled", "context", attempts)
			return observation, errors.Join(ctx.Err(), observeErr)
		}
	}
}

func observeServerStartup(
	server *privateServer,
	profile, label, outcome, healthClass string,
	attempts int64,
) (ServerStartupObservation, error) {
	if server == nil || server.logPath == "" {
		return ServerStartupObservation{}, errors.New("T40.13 startup observation is invalid")
	}
	logBytes, logDigest, stage, err := inspectStartupLog(server.logPath)
	peakRSS, gitChildren, indexChildren, otherChildren, samplerErr := server.sampler.metrics()
	if err != nil || samplerErr != nil {
		return ServerStartupObservation{}, errors.Join(err, samplerErr)
	}
	return ServerStartupObservation{
		Profile: profile, Label: label, Outcome: outcome, LastStage: stage,
		LastHealthClass: healthClass, HealthAttempts: attempts,
		WallMS: time.Since(server.started).Milliseconds(), PeakRSSBytes: peakRSS,
		GitChildren: gitChildren, IndexChildren: indexChildren, OtherChildren: otherChildren,
		LogBytes: logBytes, LogSHA256: logDigest,
	}, nil
}

func inspectStartupLog(path string) (int64, string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxStartupLogBytes {
		return 0, "", "", errors.Join(err, errors.New("T40.13 startup log exceeds its bound"))
	}
	hash := sha256.New()
	tee := io.TeeReader(io.LimitReader(file, info.Size()), hash)
	scanner := bufio.NewScanner(tee)
	scanner.Buffer(make([]byte, 4<<10), 64<<10)
	stage := "unreported"
	for scanner.Scan() {
		line := scanner.Text()
		index := strings.Index(line, startupLogPrefix)
		if index < 0 {
			continue
		}
		var report struct {
			Schema string `json:"schema"`
			Stage  string `json:"stage"`
		}
		if err := decodeStrict([]byte(line[index+len(startupLogPrefix):]), &report); err != nil ||
			report.Schema != "t4013-source-free-startup-v1" || !validStartupStage(report.Stage) {
			return 0, "", "", errors.New("T40.13 startup lifecycle report is invalid")
		}
		stage = report.Stage
	}
	if err := scanner.Err(); err != nil {
		return 0, "", "", err
	}
	return info.Size(), "sha256:" + hex.EncodeToString(hash.Sum(nil)), stage, nil
}

func validStartupStage(stage string) bool {
	switch stage {
	case "process_started", "config_loaded", "data_directory_ready", "store_opened",
		"authority_recovery_complete", "artifact_recovery_complete",
		"scheduler_recovery_complete", "searcher_ready", "http_ready":
		return true
	default:
		return false
	}
}

func (server *privateServer) stop(timeout time.Duration) error {
	if server == nil || server.command == nil || server.command.Process == nil {
		return nil
	}
	if !server.sessionIsolated {
		server.stopOnce.Do(func() {
			_ = server.command.Process.Signal(os.Interrupt)
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			var waitErr error
			select {
			case waitErr = <-server.done:
			case <-timer.C:
				_ = server.command.Process.Kill()
				waitErr = <-server.done
			}
			_ = server.sampler.close()
			closeErr := server.log.Close()
			if waitErr != nil {
				var exit *exec.ExitError
				if !errors.As(waitErr, &exit) || exit.ExitCode() != -1 {
					server.stopErr = errors.Join(waitErr, closeErr)
					return
				}
			}
			server.stopErr = closeErr
		})
		return server.stopErr
	}
	server.stopOnce.Do(func() {
		pid := server.command.Process.Pid
		interrupted, interruptErr := interruptPrivateServerRoot(server.command.Process)
		deadline := time.Now().Add(timeout)
		waitErr, parentExited := waitPrivateServerCommand(server.done, deadline)
		sessionErr := waitPrivateServerSession(pid, deadline)
		forced := !parentExited || sessionErr != nil
		var killErr error
		if forced {
			killErr = killPrivateServerSession(pid)
			forcedDeadline := time.Now().Add(5 * time.Second)
			if !parentExited {
				waitErr, _ = waitPrivateServerCommand(server.done, forcedDeadline)
			}
			sessionErr = waitPrivateServerSession(pid, forcedDeadline)
		}
		samplerErr := server.sampler.close()
		closeErr := server.log.Close()
		if forced {
			server.stopErr = errors.Join(
				errors.New("T40.13 private server required forced process-session kill"),
				errPrivateServerShutdownUnproven,
				interruptErr, killErr, waitErr, sessionErr, samplerErr, closeErr,
			)
			return
		}
		if waitErr != nil && (!interrupted || !expectedPrivateServerInterrupt(waitErr)) {
			server.stopErr = errors.Join(
				waitErr, signaledCommandShutdownUnproven(waitErr), interruptErr, samplerErr, closeErr,
			)
			return
		}
		server.stopErr = errors.Join(interruptErr, samplerErr, closeErr)
	})
	return server.stopErr
}

func waitPrivateServerCommand(done <-chan error, deadline time.Time) (error, bool) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		select {
		case err := <-done:
			return err, true
		default:
			return nil, false
		}
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case err := <-done:
		return err, true
	case <-timer.C:
		return nil, false
	}
}

func waitPrivateServerSession(pid int, deadline time.Time) error {
	for {
		alive, err := privateServerSessionAlive(pid)
		if err != nil || !alive {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("T40.13 private process session survived its shutdown deadline")
		}
		if remaining > 10*time.Millisecond {
			remaining = 10 * time.Millisecond
		}
		time.Sleep(remaining)
	}
}

func newRSSSampler(pid int, strict bool) *rssSampler {
	return &rssSampler{
		pid: pid, strict: strict, stop: make(chan struct{}), done: make(chan struct{}),
		gitChildren: map[int]struct{}{}, indexChildren: map[int]struct{}{}, otherChildren: map[int]struct{}{},
		activeChildren: map[int]sampledProcess{}, identityProbe: processStartIdentity,
		snapshotProbe: func(ctx context.Context) ([]byte, error) {
			return boundedCommandOutput(ctx, maxProcessProbeBytes,
				"/bin/ps", "-Ao", "pid=,ppid=,rss=,comm=")
		},
		nativeSnapshotProbe: nativeProcessSnapshotProbe(), retryWait: waitProcessSampleRetry,
	}
}

func waitProcessSampleRetry(ctx context.Context) error {
	timer := time.NewTimer(processSampleRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (sampler *rssSampler) captureRootIdentity() {
	if sampler == nil || !sampler.strict {
		return
	}
	sampler.sampleMu.Lock()
	defer sampler.sampleMu.Unlock()
	if sampler.identityProbe == nil {
		sampler.recordFailure(errors.New("T40.13 process identity probe is unavailable"))
		return
	}
	observed, err := sampler.identityProbe(sampler.pid, processSnapshot{})
	if err != nil {
		sampler.recordFailure(fmt.Errorf("T40.13 capture process root identity: %w", err))
		return
	}
	if observed.token == "" {
		sampler.recordFailure(errors.New("T40.13 captured process root identity is empty"))
		return
	}
	identity := processIdentity{pid: sampler.pid, token: observed.token}
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	if sampler.rootSeen && sampler.rootIdentity != identity {
		sampler.recordFailureLocked(errors.New("T40.13 captured process root identity changed"))
		return
	}
	sampler.rootIdentity = identity
	sampler.rootSeen = true
}

func (sampler *rssSampler) run() {
	defer close(sampler.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	if sampler.strict {
		for {
			select {
			case <-sampler.stop:
				return
			case <-ticker.C:
			}
			select {
			case <-sampler.stop:
				return
			default:
				sampler.sample()
			}
		}
	}
	for {
		sampler.sample()
		select {
		case <-sampler.stop:
			return
		case <-ticker.C:
		}
	}
}

func (sampler *rssSampler) sample() {
	if !sampler.strict {
		sampler.sampleLegacy()
		return
	}
	sampler.sampleMu.Lock()
	defer sampler.sampleMu.Unlock()
	if sampler.snapshotProbe == nil && sampler.nativeSnapshotProbe == nil {
		sampler.recordFailure(errors.New("T40.13 process snapshot probe is unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()
	var attemptErrs []error
	childAttempts := 0
	rootHandoffUsed := false
	for {
		rootExitedAtStart := sampler.observedRootExit()
		pids, processes, probeErr := sampler.probeProcessSnapshot(ctx)
		if ctx.Err() != nil {
			probeErr = errors.Join(probeErr, ctx.Err())
		}
		sampleErr := sampler.recordProcessSnapshotAttemptLocked(
			ctx, pids, processes, probeErr, rootExitedAtStart,
		)
		if sampleErr == nil {
			return
		}
		attemptErrs = append(attemptErrs, sampleErr)
		if errors.Is(sampleErr, errProcessRootExitTransition) && !rootHandoffUsed {
			rootHandoffUsed = true
			continue
		}
		if !errors.Is(sampleErr, errProcessChildIdentityDisappeared) {
			sampler.recordFailure(errors.Join(attemptErrs...))
			return
		}
		childAttempts++
		if childAttempts >= maxProcessSampleAttempts {
			sampler.recordFailure(errors.Join(attemptErrs...))
			return
		}
		if sampler.retryWait == nil {
			sampler.recordFailure(errors.Join(append(attemptErrs,
				errors.New("T40.13 process sample retry wait is unavailable"))...))
			return
		}
		if waitErr := sampler.retryWait(ctx); waitErr != nil {
			sampler.recordFailure(errors.Join(append(attemptErrs, waitErr)...))
			return
		}
	}
}

func (sampler *rssSampler) probeProcessSnapshot(
	ctx context.Context,
) ([]int, map[int]processSnapshot, error) {
	if sampler.nativeSnapshotProbe != nil {
		return sampler.nativeSnapshotProbe(ctx, sampler.pid)
	}
	if sampler.snapshotProbe == nil {
		return nil, nil, errors.New("T40.13 process snapshot probe is unavailable")
	}
	output, err := sampler.snapshotProbe(ctx)
	if err != nil {
		return nil, nil, err
	}
	return parseProcessSnapshot(output, sampler.pid)
}

func (sampler *rssSampler) recordSnapshot(output []byte, probeErr error) {
	sampler.sampleMu.Lock()
	defer sampler.sampleMu.Unlock()
	sampler.recordSnapshotLocked(output, probeErr)
}

func (sampler *rssSampler) recordSnapshotLocked(output []byte, probeErr error) {
	if err := sampler.recordSnapshotAttemptLocked(output, probeErr, sampler.observedRootExit()); err != nil {
		sampler.recordFailure(err)
	}
}

func (sampler *rssSampler) recordSnapshotAttemptLocked(
	output []byte, probeErr error, rootExitedAtStart bool,
) error {
	if probeErr != nil {
		return probeErr
	}
	pids, processes, parseErr := parseProcessSnapshot(output, sampler.pid)
	if parseErr != nil {
		return parseErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()
	return sampler.recordProcessSnapshotAttemptLocked(ctx, pids, processes, nil, rootExitedAtStart)
}

func (sampler *rssSampler) recordProcessSnapshotAttemptLocked(
	ctx context.Context, pids []int, processes map[int]processSnapshot,
	probeErr error, rootExitedAtStart bool,
) error {
	if ctx == nil {
		return errors.New("T40.13 process snapshot context is invalid")
	}
	if probeErr != nil {
		return probeErr
	}
	if len(pids) == 0 {
		return errors.New("T40.13 process snapshot traversal is empty")
	}
	root, rootPresent := processes[sampler.pid]
	rootExitedAfterEnumeration := sampler.observedRootExit()
	if !rootExitedAtStart && rootExitedAfterEnumeration {
		return errProcessRootExitTransition
	}
	if !rootPresent {
		if rootExitedAtStart {
			if len(pids) != 1 {
				return fmt.Errorf("T40.13 process snapshot retained descendants after root exit (exit_at_start=true descendants=%d)",
					len(pids)-1)
			}
			sampler.commitEmptyProcessSample()
			return nil
		}
		if !sampler.awaitObservedRootExit(ctx) {
			return fmt.Errorf("T40.13 process snapshot omitted its live root (exit_at_start=%t exit_after_enumeration=%t)",
				rootExitedAtStart, rootExitedAfterEnumeration)
		}
		return errProcessRootExitTransition
	}
	if rootExitedAtStart {
		if len(pids) != 1 {
			return fmt.Errorf("T40.13 process snapshot retained descendants after root exit (exit_at_start=%t exit_after_enumeration=%t descendants=%d)",
				rootExitedAtStart, rootExitedAfterEnumeration, len(pids)-1)
		}
		sampler.commitEmptyProcessSample()
		return nil
	}
	rootObservation, identityErr := sampler.processIdentityObservation(sampler.pid, root)
	if identityErr != nil {
		if sampler.awaitObservedRootExit(ctx) {
			return errProcessRootExitTransition
		}
		return fmt.Errorf("T40.13 process root identity is unavailable: %w", identityErr)
	}
	rootIdentity, _, identityErr := validateProcessIdentityObservation(sampler.pid, root, rootObservation)
	if identityErr != nil {
		return fmt.Errorf("T40.13 process root identity is invalid (exit_at_start=%t exit_after_enumeration=%t): %w",
			rootExitedAtStart, rootExitedAfterEnumeration, identityErr)
	}

	sampler.mu.Lock()
	if sampler.rootExited {
		sampler.mu.Unlock()
		return errProcessRootExitTransition
	}
	if !sampler.rootSeen {
		sampler.mu.Unlock()
		return errors.New("T40.13 process root identity was not captured before its Wait owner")
	}
	if sampler.rootIdentity != rootIdentity {
		sampler.mu.Unlock()
		return errors.New("T40.13 process snapshot root identity changed")
	}
	previousChildren := sampler.activeChildren
	gitChildren := sampler.strictGitChildren
	indexChildren := sampler.strictIndexChildren
	otherChildren := sampler.strictOtherChildren
	sampler.mu.Unlock()

	nextActive := make(map[int]sampledProcess, len(pids)-1)
	totalChildren, err := checkedSumInt64(gitChildren, indexChildren, otherChildren)
	if err != nil {
		return err
	}
	for _, pid := range pids[1:] {
		process := processes[pid]
		previous, previouslyActive := previousChildren[pid]
		identityObservation, probeErr := sampler.processIdentityObservation(pid, process)
		if probeErr != nil {
			if errors.Is(probeErr, errProcessIdentityMissing) {
				return errors.Join(errProcessChildIdentityDisappeared,
					fmt.Errorf("T40.13 process child identity is unavailable (pid=%d ppid=%d table_class=%d exit_at_start=%t exit_after_enumeration=%t exit_after_identity=%t): %w",
						pid, process.parent, classifyProcess(process.name), rootExitedAtStart,
						rootExitedAfterEnumeration, sampler.observedRootExit(), probeErr))
			}
			return fmt.Errorf("T40.13 process child identity is unavailable (pid=%d ppid=%d table_class=%d exit_at_start=%t exit_after_enumeration=%t exit_after_identity=%t): %w",
				pid, process.parent, classifyProcess(process.name), rootExitedAtStart,
				rootExitedAfterEnumeration, sampler.observedRootExit(), probeErr)
		}
		identity, class, identityErr := validateProcessIdentityObservation(pid, process, identityObservation)
		if identityErr != nil {
			return fmt.Errorf("T40.13 process child identity is invalid (pid=%d ppid=%d kernel_ppid=%d table_class=%d kernel_class=%d token=%s exit_at_start=%t exit_after_enumeration=%t exit_after_identity=%t): %w",
				pid, process.parent, identityObservation.parent, classifyProcess(process.name),
				classifyProcess(identityObservation.name), identityObservation.token, rootExitedAtStart,
				rootExitedAfterEnumeration, sampler.observedRootExit(), identityErr)
		}
		observed := sampledProcess{
			identity: identity,
			class:    class,
		}
		if previouslyActive && previous.identity == observed.identity {
			if previous.class != observed.class {
				return errors.New("T40.13 process child classification changed within one lifetime")
			}
			nextActive[pid] = observed
			continue
		}
		if totalChildren >= maxProcessChildLifetimes {
			return errors.New("T40.13 cumulative process child inventory exceeds its bound")
		}
		totalChildren++
		switch observed.class {
		case processClassGit:
			gitChildren++
		case processClassIndex:
			indexChildren++
		default:
			otherChildren++
		}
		nextActive[pid] = observed
	}
	if sampler.observedRootExit() {
		return errProcessRootExitTransition
	}
	var total int64
	for _, pid := range pids {
		process := processes[pid]
		if process.rssBytes > 1<<63-1-total {
			return errors.New("T40.13 process RSS observation overflowed")
		}
		var addErr error
		total, addErr = checkedAddInt64(total, process.rssBytes)
		if addErr != nil {
			return addErr
		}
	}

	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	sampler.rootIdentity = rootIdentity
	sampler.rootSeen = true
	if total > sampler.peakRSS {
		sampler.peakRSS = total
	}
	sampler.activeChildren = nextActive
	sampler.strictGitChildren = gitChildren
	sampler.strictIndexChildren = indexChildren
	sampler.strictOtherChildren = otherChildren
	sampler.samples++
	return nil
}

func (sampler *rssSampler) processIdentityObservation(
	pid int, process processSnapshot,
) (processIdentityObservation, error) {
	if process.coherent {
		if process.identityToken == "" {
			return processIdentityObservation{}, errors.New("T40.13 coherent process identity is empty")
		}
		return processIdentityObservation{
			token: process.identityToken, parent: process.parent, name: process.name,
		}, nil
	}
	if sampler.identityProbe == nil {
		return processIdentityObservation{}, errors.New("T40.13 process identity probe is unavailable")
	}
	return sampler.identityProbe(pid, process)
}

func (sampler *rssSampler) commitEmptyProcessSample() {
	sampler.mu.Lock()
	sampler.activeChildren = map[int]sampledProcess{}
	sampler.samples++
	sampler.mu.Unlock()
}

func classifyProcess(name string) processClass {
	base := normalizedProcessName(name)
	switch base {
	case "zoekt-git-index", "phebs-focused-index", "phebs-focused-i", "phebs-focused-in":
		return processClassIndex
	case "git":
		return processClassGit
	default:
		return processClassOther
	}
}

func validateProcessIdentityObservation(
	pid int, candidate processSnapshot, observed processIdentityObservation,
) (processIdentity, processClass, error) {
	if observed.token == "" || observed.parent != candidate.parent || observed.name == "" {
		return processIdentity{}, 0, errors.New("kernel identity does not match the process-table row")
	}
	candidateClass := classifyProcess(candidate.name)
	observedClass := classifyProcess(observed.name)
	if candidateClass != observedClass {
		return processIdentity{}, 0, errors.New("kernel process class does not match the process-table row")
	}
	return processIdentity{pid: pid, token: observed.token}, observedClass, nil
}

func normalizedProcessName(name string) string {
	name = filepath.Base(name)
	if len(name) > 2 && name[0] == '(' && name[len(name)-1] == ')' {
		return name[1 : len(name)-1]
	}
	return name
}

func (sampler *rssSampler) recordFailure(err error) {
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	sampler.recordFailureLocked(err)
}

func (sampler *rssSampler) recordFailureLocked(err error) {
	if sampler.firstErr == nil {
		sampler.firstErr = err
	}
	if sampler.failedSamples < ^uint64(0) {
		sampler.failedSamples++
	}
}

type processSnapshot struct {
	parent        int
	rssBytes      int64
	name          string
	identityToken string
	coherent      bool
}

func parseProcessSnapshot(output []byte, root int) ([]int, map[int]processSnapshot, error) {
	if root <= 0 {
		return nil, nil, errors.New("T40.13 process snapshot root is invalid")
	}
	processes := make(map[int]processSnapshot)
	children := make(map[int][]int)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 4 || len(processes) >= maxProcessSnapshotRows {
			return nil, nil, errors.New("T40.13 process snapshot is invalid or exceeds its bound")
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		kilobytes, rssErr := strconv.ParseInt(fields[2], 10, 64)
		if pidErr != nil || parentErr != nil || rssErr != nil || pid <= 0 || parent < 0 ||
			kilobytes < 0 || kilobytes > (1<<63-1)/1024 {
			return nil, nil, errors.New("T40.13 process snapshot row is invalid")
		}
		if _, exists := processes[pid]; exists {
			return nil, nil, errors.New("T40.13 process snapshot contains a duplicate PID")
		}
		processes[pid] = processSnapshot{
			parent: parent, rssBytes: kilobytes * 1024, name: strings.Join(fields[3:], " "),
		}
		children[parent] = append(children[parent], pid)
	}
	result := []int{root}
	seen := map[int]struct{}{root: {}}
	for index := 0; index < len(result); index++ {
		for _, child := range children[result[index]] {
			if _, exists := seen[child]; exists {
				return nil, nil, errors.New("T40.13 process snapshot contains a parent cycle")
			}
			seen[child] = struct{}{}
			result = append(result, child)
			if len(result) > maxProcessDescendants+1 {
				return nil, nil, errors.New("T40.13 process descendant inventory exceeds its bound")
			}
		}
	}
	return result, processes, nil
}

func (sampler *rssSampler) sampleLegacy() {
	pids := processTree(sampler.pid)
	var total int64
	for _, pid := range pids {
		output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			continue
		}
		kilobytes, err := strconv.ParseInt(string(bytesTrimSpace(output)), 10, 64)
		if err == nil && kilobytes >= 0 {
			bytes, mulErr := checkedMulInt64(kilobytes, 1024)
			if mulErr == nil {
				var addErr error
				total, addErr = checkedAddInt64(total, bytes)
				if addErr != nil {
					sampler.recordFailure(addErr)
					return
				}
			}
		}
	}
	sampler.mu.Lock()
	if total > sampler.peakRSS {
		sampler.peakRSS = total
	}
	for _, pid := range pids[1:] {
		name := processName(pid)
		switch {
		case strings.Contains(name, "zoekt-git-index") || strings.Contains(name, "phebs-focused-index"):
			sampler.indexChildren[pid] = struct{}{}
		case filepath.Base(name) == "git":
			sampler.gitChildren[pid] = struct{}{}
		default:
			sampler.otherChildren[pid] = struct{}{}
		}
	}
	sampler.samples++
	sampler.mu.Unlock()
}

func processTree(root int) []int {
	result := []int{root}
	for index := 0; index < len(result) && len(result) <= 128; index++ {
		output, err := exec.Command("pgrep", "-P", strconv.Itoa(result[index])).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Fields(string(output)) {
			pid, parseErr := strconv.Atoi(line)
			if parseErr == nil && pid > 0 {
				result = append(result, pid)
			}
		}
	}
	return result
}

func processName(pid int) string {
	output, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return string(bytesTrimSpace(output))
}

func boundedCommandOutput(ctx context.Context, limit int64, name string, args ...string) ([]byte, error) {
	if ctx == nil || limit <= 0 {
		return nil, errors.New("T40.13 bounded command output is invalid")
	}
	command := exec.CommandContext(ctx, name, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, limit+1))
	if int64(len(output)) > limit {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, errors.New("T40.13 command output exceeds its bound")
	}
	return output, errors.Join(readErr, command.Wait())
}

func (sampler *rssSampler) metrics() (peakRSS, gitChildren, indexChildren, otherChildren int64, err error) {
	if sampler == nil {
		return 0, 0, 0, 0, nil
	}
	if sampler.strict {
		sampler.sampleMu.Lock()
		defer sampler.sampleMu.Unlock()
	}
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	if sampler.strict {
		return sampler.peakRSS, sampler.strictGitChildren, sampler.strictIndexChildren,
			sampler.strictOtherChildren, sampler.strictErrorLocked()
	}
	return sampler.peakRSS, int64(len(sampler.gitChildren)), int64(len(sampler.indexChildren)),
		int64(len(sampler.otherChildren)), nil
}

func (sampler *rssSampler) strictErrorLocked() error {
	if sampler.firstErr == nil {
		return nil
	}
	return fmt.Errorf("%w after %d failed samples: %w",
		errProcessSamplingFailed, sampler.failedSamples, sampler.firstErr)
}

func (sampler *rssSampler) expectConcurrentRootWait() {
	if sampler == nil || !sampler.strict {
		return
	}
	sampler.mu.Lock()
	if sampler.rootWait == nil {
		sampler.rootWait = make(chan struct{})
	}
	sampler.mu.Unlock()
}

func (sampler *rssSampler) observeRootExit() {
	if sampler == nil || !sampler.strict {
		return
	}
	sampler.mu.Lock()
	sampler.rootExited = true
	wait := sampler.rootWait
	sampler.mu.Unlock()
	if wait != nil {
		sampler.rootExitOnce.Do(func() { close(wait) })
	}
}

func (sampler *rssSampler) observedRootExit() bool {
	if sampler == nil {
		return false
	}
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	return sampler.rootExited
}

func (sampler *rssSampler) awaitObservedRootExit(ctx context.Context) bool {
	sampler.mu.Lock()
	exited := sampler.rootExited
	wait := sampler.rootWait
	sampler.mu.Unlock()
	if exited || wait == nil {
		return exited
	}
	select {
	case <-wait:
		sampler.mu.Lock()
		defer sampler.mu.Unlock()
		return sampler.rootExited
	case <-ctx.Done():
		return false
	}
}

func (sampler *rssSampler) resetWindow() {
	if sampler == nil {
		return
	}
	if sampler.strict {
		sampler.sampleMu.Lock()
		defer sampler.sampleMu.Unlock()
	}
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	sampler.peakRSS = 0
	sampler.samples = 0
	if sampler.strict {
		sampler.activeChildren = map[int]sampledProcess{}
		sampler.strictGitChildren = 0
		sampler.strictIndexChildren = 0
		sampler.strictOtherChildren = 0
		return
	}
	sampler.gitChildren = map[int]struct{}{}
	sampler.indexChildren = map[int]struct{}{}
	sampler.otherChildren = map[int]struct{}{}
}

func (sampler *rssSampler) close() error {
	if sampler == nil {
		return nil
	}
	sampler.closeOnce.Do(func() {
		close(sampler.stop)
		<-sampler.done
		if !sampler.strict {
			return
		}
		sampler.sampleMu.Lock()
		defer sampler.sampleMu.Unlock()
		sampler.mu.Lock()
		defer sampler.mu.Unlock()
		sampler.closeErr = sampler.strictErrorLocked()
	})
	return sampler.closeErr
}

func scrubExecutionEnvironment() []string {
	return []string{
		"CGO_ENABLED=0",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GOARCH=" + runtime.GOARCH,
		"GOENV=off",
		"GOEXPERIMENT=",
		"GOFLAGS=-mod=readonly",
		"GOFIPS140=off",
		"GOOS=" + runtime.GOOS,
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTELEMETRY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"LANG=C",
		"LC_ALL=C",
		"TZ=UTC",
	}
}

func legacyExecutionEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "PHEBS_") || name == "ZOEKT_DISABLE_CATFILE_BATCH" ||
			strings.HasPrefix(name, "GIT_") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func executionEnvironment(closed bool) []string {
	if closed {
		return scrubExecutionEnvironment()
	}
	return legacyExecutionEnvironment()
}

func executionEnvironmentForPlan(plan Plan, tempDir ...string) []string {
	environment := executionEnvironment(planSchemaVersion(plan.Schema) >= 25)
	if planSchemaVersion(plan.Schema) >= 25 && len(tempDir) == 1 {
		environment = append(environment, "TMPDIR="+tempDir[0], "GOTMPDIR="+tempDir[0])
	}
	return environment
}

func executionEnvironmentForControls(controls executionControls, allowDownload bool) []string {
	environment := scrubExecutionEnvironment()
	if allowDownload {
		environment = replaceEnvironmentValue(environment, "GOPROXY", "https://proxy.golang.org")
		environment = replaceEnvironmentValue(environment, "GOSUMDB", "sum.golang.org")
	}
	return append(environment,
		"HOME="+controls.Home,
		"PATH="+controls.GitExecPath,
		"TMPDIR="+controls.Temp,
		"TEMP="+controls.Temp,
		"TMP="+controls.Temp,
		"GOTMPDIR="+controls.Temp,
		"GOMODCACHE="+controls.ModuleCache,
		"GOCACHE="+controls.BuildCache,
		"XDG_CONFIG_HOME="+controls.Home,
		"XDG_CACHE_HOME="+controls.Temp,
		"XDG_DATA_HOME="+controls.Home,
		"GIT_EXEC_PATH="+controls.GitExecPath,
	)
}

func replaceEnvironmentValue(environment []string, name, value string) []string {
	prefix := name + "="
	for index := range environment {
		if strings.HasPrefix(environment[index], prefix) {
			environment[index] = prefix + value
			return environment
		}
	}
	return append(environment, prefix+value)
}

func executionEnvironmentForToolchain(toolchain privateToolchain) []string {
	environment := executionEnvironment(toolchain.ClosedEnvironment)
	if toolchain.ClosedEnvironment {
		environment = executionEnvironmentForControls(toolchain.controls, false)
		if toolchain.host.surreal.path != "" {
			environment = append(environment,
				"PHEBS_SURREAL="+toolchain.host.surreal.path,
				"PHEBS_SURREAL_SHA256="+toolchain.host.surreal.sha256,
				"PHEBS_ZOEKT_GIT_INDEX_SHA256="+toolchain.digests[1],
				"PHEBS_FOCUSED_INDEX_SHA256="+toolchain.digests[2],
				"PHEBS_BUF_SHA256="+toolchain.digests[3],
			)
		}
		environment = append(environment, toolchain.extraEnvironment...)
	}
	return environment
}

func validatePrivateTemporaryDirectory(toolchain privateToolchain) error {
	if !toolchain.ClosedEnvironment {
		return nil
	}
	if toolchain.TempDir != toolchain.controls.Temp || !digestIdentity(toolchain.controlsDigest) {
		return errors.New("T40.13 private temporary directory is invalid")
	}
	controls, err := openExecutionControls(
		toolchain.controls.Workspace, toolchain.controlsDigest, toolchain.host, true,
	)
	if err != nil {
		return errors.Join(err, errors.New("T40.13 private temporary directory is invalid"))
	}
	if controls != toolchain.controls {
		return errors.New("T40.13 private temporary directory is invalid")
	}
	return nil
}

func validateToolchain(value privateToolchain) error {
	if value.Schema != privateToolchainSchema {
		return errors.New("T40.13 toolchain identity is invalid")
	}
	for _, path := range []string{value.Phebs, value.Zoekt, value.Focused, value.Buf} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("T40.13 toolchain executable is invalid")
		}
	}
	return validatePrivateTemporaryDirectory(value)
}

func observeToolchain(value privateToolchain) ([]ToolchainObservation, error) {
	if err := validateToolchain(value); err != nil {
		return nil, err
	}
	if privateToolchainDigestsValid(value.digests) {
		return privateToolchainObservations(value), nil
	}
	return observeToolchainContext(context.Background(), value)
}

func bindPrivateToolchain(
	ctx context.Context, value *privateToolchain,
) ([]ToolchainObservation, error) {
	if value == nil {
		return nil, errors.New("T40.13 private toolchain is absent")
	}
	observed, err := observeToolchainContext(ctx, *value)
	if err != nil {
		return nil, err
	}
	for index := range observed {
		value.digests[index] = observed[index].SHA256
	}
	return observed, nil
}

func revalidatePrivateToolchain(ctx context.Context, value privateToolchain) error {
	if !value.ClosedEnvironment {
		return nil
	}
	if err := validateToolchain(value); err != nil {
		return err
	}
	if !privateToolchainDigestsValid(value.digests) {
		return errors.New("T40.13 private toolchain lacks bound executable digests")
	}
	for index, input := range privateToolchainInputs(value) {
		observed, err := privateExecutableDigest(ctx, input.path)
		if err != nil || observed != value.digests[index] {
			return errors.Join(err, fmt.Errorf("T40.13 private executable changed: %s", input.name))
		}
	}
	return nil
}

func observeToolchainContext(ctx context.Context, value privateToolchain) ([]ToolchainObservation, error) {
	if ctx == nil {
		return nil, errors.New("T40.13 private toolchain context is nil")
	}
	if err := validateToolchain(value); err != nil {
		return nil, err
	}
	inputs := privateToolchainInputs(value)
	result := make([]ToolchainObservation, 0, len(inputs))
	for _, input := range inputs {
		digest, err := privateExecutableDigest(ctx, input.path)
		if err != nil {
			return nil, err
		}
		result = append(result, ToolchainObservation{Name: input.name, SHA256: digest})
	}
	return result, nil
}

func privateExecutableDigest(ctx context.Context, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode()&0o111 == 0 || info.Size() <= 0 || info.Size() > 2<<30 {
		return "", errors.Join(err, errors.New("T40.13 toolchain executable exceeds its digest bound"))
	}
	return regularFileDigestContext(ctx, path, 2<<30)
}

func privateToolchainInputs(value privateToolchain) [4]struct {
	name string
	path string
} {
	return [4]struct {
		name string
		path string
	}{
		{"phebs", value.Phebs},
		{"zoekt-git-index", value.Zoekt},
		{"phebs-focused-index", value.Focused},
		{"buf", value.Buf},
	}
}

func privateToolchainDigestsValid(values [4]string) bool {
	for _, value := range values {
		if !digestIdentity(value) {
			return false
		}
	}
	return true
}

func privateToolchainObservations(value privateToolchain) []ToolchainObservation {
	inputs := privateToolchainInputs(value)
	result := make([]ToolchainObservation, 0, len(inputs))
	for index, input := range inputs {
		result = append(result, ToolchainObservation{Name: input.name, SHA256: value.digests[index]})
	}
	return result
}
