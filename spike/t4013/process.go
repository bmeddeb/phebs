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
	"strconv"
	"strings"
	"sync"
	"time"
)

const privateToolchainSchema = "t4013-private-toolchain-v1"

type privateToolchain struct {
	Schema  string
	Phebs   string
	Zoekt   string
	Focused string
	Buf     string
}

type privateServer struct {
	command  *exec.Cmd
	started  time.Time
	done     chan error
	log      *os.File
	logPath  string
	sampler  *rssSampler
	stopOnce sync.Once
	stopErr  error
}

type rssSampler struct {
	pid           int
	stop          chan struct{}
	done          chan struct{}
	mu            sync.Mutex
	peakRSS       int64
	samples       int64
	gitChildren   map[int]struct{}
	indexChildren map[int]struct{}
	otherChildren map[int]struct{}
}

func buildPrivateToolchain(ctx context.Context, moduleRoot, workspace string) (privateToolchain, error) {
	if ctx == nil || !filepath.IsAbs(moduleRoot) || !filepath.IsAbs(workspace) {
		return privateToolchain{}, errors.New("T40.13 toolchain scope is invalid")
	}
	source := filepath.Join(workspace, "toolchain-source")
	if err := exportFrozenSource(ctx, moduleRoot, source); err != nil {
		return privateToolchain{}, err
	}
	output := filepath.Join(workspace, "toolchain")
	if err := os.Mkdir(output, 0o700); err != nil {
		return privateToolchain{}, err
	}
	toolchain := privateToolchain{
		Schema: privateToolchainSchema,
		Phebs:  filepath.Join(output, "phebs"), Zoekt: filepath.Join(output, "zoekt-git-index"),
		Focused: filepath.Join(output, "phebs-focused-index"), Buf: filepath.Join(output, "buf"),
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
			return privateToolchain{}, errors.New("T40.13 toolchain output already exists")
		}
		command := exec.CommandContext(ctx, "go", build.args...)
		command.Dir = source
		command.Env = append(scrubExecutionEnvironment(), build.env...)
		if output, err := command.CombinedOutput(); err != nil {
			_ = output
			return privateToolchain{}, errors.New("T40.13 toolchain build failed")
		}
	}
	return toolchain, nil
}

func exportFrozenSource(ctx context.Context, moduleRoot, output string) error {
	if err := os.Mkdir(output, 0o700); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "git", "archive", "--format=tar", "HEAD")
	command.Dir = moduleRoot
	command.Env = scrubExecutionEnvironment()
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
	logPath := filepath.Join(filepath.Dir(profile.Config), "server-"+label+".log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, toolchain.Phebs, "serve", "-config", profile.Config)
	command.Stdout, command.Stderr = logFile, logFile
	command.Env = append(scrubExecutionEnvironment(),
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
		sampler: newRSSSampler(command.Process.Pid),
	}
	go func() { server.done <- command.Wait() }()
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
	inspector, err := newProfileInspector(profile)
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
	peakRSS, gitChildren, indexChildren, otherChildren := server.sampler.metrics()
	if err != nil {
		return ServerStartupObservation{}, err
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
		server.sampler.close()
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

func newRSSSampler(pid int) *rssSampler {
	return &rssSampler{
		pid: pid, stop: make(chan struct{}), done: make(chan struct{}),
		gitChildren: map[int]struct{}{}, indexChildren: map[int]struct{}{}, otherChildren: map[int]struct{}{},
	}
}

func (sampler *rssSampler) run() {
	defer close(sampler.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
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
	pids := processTree(sampler.pid)
	var total int64
	for _, pid := range pids {
		command := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid))
		output, err := command.Output()
		if err != nil {
			continue
		}
		kilobytes, err := strconv.ParseInt(string(bytesTrimSpace(output)), 10, 64)
		if err == nil && kilobytes >= 0 && kilobytes <= (1<<63-1)/1024 {
			total += kilobytes * 1024
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
		command := exec.Command("pgrep", "-P", strconv.Itoa(result[index]))
		output, err := command.Output()
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
	command := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid))
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return string(bytesTrimSpace(output))
}

func (sampler *rssSampler) metrics() (peakRSS, gitChildren, indexChildren, otherChildren int64) {
	if sampler == nil {
		return 0, 0, 0, 0
	}
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	return sampler.peakRSS, int64(len(sampler.gitChildren)), int64(len(sampler.indexChildren)), int64(len(sampler.otherChildren))
}

func (sampler *rssSampler) resetWindow() {
	if sampler == nil {
		return
	}
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	sampler.peakRSS = 0
	sampler.samples = 0
	sampler.gitChildren = map[int]struct{}{}
	sampler.indexChildren = map[int]struct{}{}
	sampler.otherChildren = map[int]struct{}{}
}

func (sampler *rssSampler) close() {
	if sampler == nil {
		return
	}
	select {
	case <-sampler.done:
		return
	default:
	}
	close(sampler.stop)
	<-sampler.done
}

func scrubExecutionEnvironment() []string {
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
	return nil
}

func observeToolchain(value privateToolchain) ([]ToolchainObservation, error) {
	if err := validateToolchain(value); err != nil {
		return nil, err
	}
	inputs := []struct {
		name string
		path string
	}{
		{"phebs", value.Phebs},
		{"zoekt-git-index", value.Zoekt},
		{"phebs-focused-index", value.Focused},
		{"buf", value.Buf},
	}
	result := make([]ToolchainObservation, 0, len(inputs))
	for _, input := range inputs {
		info, err := os.Lstat(input.path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 2<<30 {
			return nil, errors.New("T40.13 toolchain executable exceeds its digest bound")
		}
		file, err := os.Open(input.path)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		_, copyErr := io.CopyN(hash, file, info.Size())
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return nil, errors.Join(copyErr, closeErr)
		}
		result = append(result, ToolchainObservation{
			Name: input.name, SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		})
	}
	return result, nil
}
