package t451a

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

// WorkerPlan is called only after WorkerRequest validates the sandbox boundary.
// Bazel receives the same closed startup/configuration on every invocation.
func WorkerPlan(ctx context.Context) error {
	if err := unpackCompiler("/inputs/tools/cc-sysroot.zip", "/scratch/toolchain"); err != nil {
		return fmt.Errorf("offline compiler import: %w", err)
	}
	const compiler = `#!/bin/sh
export GCC_EXEC_PREFIX=/scratch/toolchain/usr/lib/gcc/
export LIBRARY_PATH=/scratch/toolchain/usr/lib/aarch64-linux-gnu:/scratch/toolchain/lib/aarch64-linux-gnu
export LD_LIBRARY_PATH=/scratch/toolchain/usr/lib/aarch64-linux-gnu:/scratch/toolchain/lib/aarch64-linux-gnu
exec /scratch/toolchain/usr/bin/aarch64-linux-gnu-gcc-12 --sysroot=/scratch/toolchain -B/scratch/toolchain/usr/bin/ -B/scratch/toolchain/usr/lib/gcc/aarch64-linux-gnu/12/ "$@"
`
	if err := os.WriteFile("/scratch/toolchain/cc", []byte(compiler), 0500); err != nil {
		return err
	}
	const workspace = "/scratch/workspace"
	fixtures, err := planner.Fixtures()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(fixtures))
	for name := range fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !validPath(name) {
			return errors.New("invalid owned fixture path")
		}
		destination := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(destination, fixtures[name], 0600); err != nil {
			return err
		}
	}
	helper, err := readBounded("/inputs/t451a", MaxFileBytes)
	if err != nil {
		return err
	}
	if err := os.WriteFile(workspace+"/phebs_plan/t451a", helper, 0500); err != nil {
		return err
	}
	startup, common := launcher.BazelStartup(), launcher.BazelCommon()
	budget := commandBudget{remaining: sandbox.OutputBytes}
	var outputs [][]byte
	var evicted cacheEviction
	for index, suffix := range planner.Commands() {
		args := append(append(append([]string{}, startup...), suffix...), common...)
		commandCtx, commandCancel := context.WithCancel(ctx)
		budget.cancel = commandCancel
		command := exec.CommandContext(commandCtx, "/inputs/tools/bin/bazel", args...)
		command.Dir = workspace
		command.Env = launcher.BazelEnvironment()
		stdout, stderr := commandOutput{budget: &budget}, commandOutput{budget: &budget}
		command.Stdout, command.Stderr = &stdout, &stderr
		command.WaitDelay = time.Second
		runErr := command.Run()
		commandCancel()
		if runErr != nil {
			// These diagnostics contain only compiled-in neutral names and public
			// tool material. Host receipts retain no raw command/error bytes.
			return fmt.Errorf("neutral %s refused: %w: %.8192s", suffix[0], runErr, stderr.buffer.Bytes())
		}
		outputs = append(outputs, stdout.buffer.Bytes())
		if index == 1 {
			if err := ensureQuiescentWorker(); err != nil {
				return err
			}
			evicted, err = evictCompilerCache(gazelleCompilerCache)
			if err != nil {
				return fmt.Errorf("private compiler cache eviction: %w", err)
			}
		}
	}
	if len(outputs) != 3 {
		return errors.New("closed command sequence changed")
	}
	paths, err := planner.ProjectionPaths(outputs[1])
	if err != nil {
		return err
	}
	execRoot, err := os.OpenRoot("/scratch/bazel-output/execroot/_main")
	if err != nil {
		return err
	}
	defer func() { _ = execRoot.Close() }()
	projections := make(map[string][]byte, len(paths))
	var total int
	for _, name := range paths {
		if !validPath(name) {
			return errors.New("invalid projection locator")
		}
		info, err := execRoot.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() > int64(planner.MaxProjectionBytes) {
			return errors.New("projection file refused")
		}
		file, err := execRoot.Open(name)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(planner.MaxProjectionBytes)+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(data) > planner.MaxProjectionBytes {
			return errors.New("projection bytes refused")
		}
		total += len(data)
		if total > planner.MaxProtoBytes {
			return errors.New("aggregate projection byte limit")
		}
		projections[name] = data
	}
	plan, err := planner.Assemble(outputs[0], outputs[1], projections)
	if err != nil {
		return err
	}
	if err := planner.VerifyNeutral(plan); err != nil {
		return err
	}
	var ordinary, transition []planner.Configured
	for _, target := range plan.Targets {
		switch target.Label {
		case "@@//lib:alias":
			ordinary = append(ordinary, target.Configured)
		case "@@//transition:split":
			transition = append(transition, target.Configured)
		}
	}
	if len(ordinary) != 1 || len(transition) != 1 {
		return errors.New("neutral launcher roots are not exact")
	}
	if _, err := launcher.Prepare(plan, transition); !errors.Is(err, launcher.ErrUnrepresentable) {
		return errors.New("driver accepted lossy repeated-label configurations")
	}
	prepared, err := launcher.Prepare(plan, ordinary)
	if err != nil {
		return err
	}
	response, err := launcher.RunThroughEntrypoint(ctx, plan, ordinary)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Plan                       planner.Plan        `json:"plan"`
		Launcher                   launcher.Invocation `json:"launcher"`
		InvocationSHA256           string              `json:"invocation_sha256"`
		ResponseSHA256             string              `json:"response_sha256"`
		ResponseBytes              int                 `json:"response_bytes"`
		RepeatedConfigurationStops bool                `json:"repeated_configuration_stops"`
		CompilerCacheEviction      cacheEviction       `json:"compiler_cache_eviction"`
	}{plan, prepared.Invocation(), prepared.Digest(), Digest(response), len(response), true, evicted})
}

type commandBudget struct {
	mu        sync.Mutex
	remaining int
	cancel    context.CancelFunc
}
type commandOutput struct {
	budget *commandBudget
	buffer bytes.Buffer
}

func (writer *commandOutput) Write(data []byte) (int, error) {
	writer.budget.mu.Lock()
	defer writer.budget.mu.Unlock()
	if len(data) > writer.budget.remaining {
		if writer.budget.cancel != nil {
			writer.budget.cancel()
		}
		return 0, errors.New("bazel aggregate output limit")
	}
	writer.budget.remaining -= len(data)
	return writer.buffer.Write(data)
}

// Compiler archives are digest-bound bundle entries. Expansion happens in
// Linux to preserve case-sensitive header names; all links were flattened by
// the operator's offline preparation. Nothing from the archive is executed
// until the complete count/byte/type/path-bounded import finishes.
func unpackCompiler(archive, destination string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	if len(reader.File) == 0 || len(reader.File) > 12000 {
		return errors.New("compiler file count limit")
	}
	var total uint64
	previous := ""
	for _, entry := range reader.File {
		if !validPath(entry.Name) || entry.Name <= previous || !entry.Mode().IsRegular() || entry.UncompressedSize64 > 128<<20 {
			return errors.New("compiler archive entry refused")
		}
		previous = entry.Name
		total += entry.UncompressedSize64
		if total > 512<<20 {
			return errors.New("compiler archive byte limit")
		}
	}
	for _, entry := range reader.File {
		name := filepath.Join(destination, entry.Name)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0400)
		if entry.Mode().Perm()&0111 != 0 {
			mode = 0500
		}
		output, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			_ = output.Close()
			return err
		}
		count, copyErr := io.Copy(output, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		err = errors.Join(copyErr, input.Close(), output.Close())
		if err != nil || count != int64(entry.UncompressedSize64) {
			return errors.New("compiler archive content refused")
		}
	}
	return nil
}
