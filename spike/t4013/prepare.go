package t4013

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t401"
	"gopkg.in/yaml.v3"
)

const (
	PreparedSchema           = "t4013-private-prepared-custody-v1"
	PreparedSchemaV2         = "t4013-private-prepared-custody-v2"
	PrepareConfirm           = "prepare-neutral-t4013-custody"
	CleanupConfirm           = "cleanup-neutral-t4013-custody"
	preparedCleanupSchema    = "t4013-prepared-cleanup-v1"
	preparedCleanupSchemaV2  = "t4013-prepared-cleanup-v2"
	preparedCleanupMaxBytes  = 4 << 10
	custodyRemoveAttempts    = 11
	custodyRemoveRetryDelay  = 100 * time.Millisecond
	custodyRemoveSettleDelay = 250 * time.Millisecond
)

type PrepareRequest struct {
	ModuleRoot string
	Workspace  string
	PlanPath   string
	Confirm    string
	BasePort   int
}

type preparedCleanupControl struct {
	Schema           string `json:"schema"`
	PlanDigest       string `json:"plan_digest"`
	ModuleRoot       string `json:"module_root"`
	Workspace        string `json:"workspace"`
	SupervisionToken string `json:"supervision_token,omitempty"`
}

type Prepared struct {
	Schema                  string            `json:"schema"`
	PlanDigest              string            `json:"plan_digest"`
	SupervisionToken        string            `json:"supervision_token,omitempty"`
	ExecutionControlsSHA256 string            `json:"execution_controls_sha256,omitempty"`
	Profiles                []PreparedProfile `json:"profiles"`
}

type PreparedProfile struct {
	Name           string            `json:"name"`
	Repository     string            `json:"repository"`
	RepositoryName string            `json:"repository_name"`
	Config         string            `json:"config"`
	Credential     string            `json:"credential"`
	DataDir        string            `json:"data_dir"`
	Address        string            `json:"address"`
	Catalog        string            `json:"catalog"`
	Revisions      map[string]string `json:"revisions"`
}

func Prepare(ctx context.Context, request PrepareRequest) (result Prepared, retErr error) {
	return prepare(ctx, request, "")
}

// PrepareToOutput adds V25 durable manifest publication without changing the
// historical PrepareRequest shape.
func PrepareToOutput(ctx context.Context, request PrepareRequest, output string) (result Prepared, retErr error) {
	return prepare(ctx, request, output)
}

func prepare(ctx context.Context, request PrepareRequest, output string) (result Prepared, retErr error) {
	if ctx == nil {
		return Prepared{}, errors.New("T40.13 prepare requires a context")
	}
	if request.Confirm != PrepareConfirm || request.BasePort < 1024 || request.BasePort > 65533 {
		return Prepared{}, errors.New("T40.13 prepare confirmation or base port is invalid")
	}
	moduleRoot, err := filepath.EvalSymlinks(request.ModuleRoot)
	if err != nil || !filepath.IsAbs(moduleRoot) {
		return Prepared{}, errors.New("T40.13 module root must be an absolute real path")
	}
	workspaceParent, err := filepath.EvalSymlinks(filepath.Dir(request.Workspace))
	if err != nil || !filepath.IsAbs(request.Workspace) {
		return Prepared{}, errors.New("T40.13 workspace parent must be an absolute real path")
	}
	workspace := filepath.Join(workspaceParent, filepath.Base(request.Workspace))
	planIdentity, plan, err := readPlanIdentity(request.PlanPath)
	if err != nil {
		return Prepared{}, err
	}
	planBytes := planIdentity.raw
	version := planSchemaVersion(plan.Schema)
	var admissionLock *runRootLock
	if version >= 25 {
		admissionLock, err = lockRunRoot(workspaceParent)
		if err != nil {
			return Prepared{}, err
		}
		defer func() {
			retErr = errors.Join(retErr, admissionLock.Close())
		}()
		if err := planIdentity.revalidate(); err != nil {
			return Prepared{}, err
		}
	}
	if workspace == moduleRoot || isWithin(workspace, moduleRoot) || isWithin(moduleRoot, workspace) {
		return Prepared{}, errors.New("T40.13 workspace must be outside and must not contain the module root")
	}
	if _, err := os.Lstat(workspace); err == nil || !os.IsNotExist(err) {
		return Prepared{}, errors.New("T40.13 workspace must not already exist")
	}
	preparedOutput := ""
	if version >= 25 {
		if output == "" {
			return Prepared{}, errors.New("T40.13 V25 preparation requires durable output authority")
		}
		preparedOutput, err = canonicalNewOutputPath(output)
		if err != nil || preparedOutput == workspace || isWithin(preparedOutput, workspace) ||
			preparedOutput == moduleRoot || isWithin(preparedOutput, moduleRoot) {
			return Prepared{}, errors.Join(err, errors.New("T40.13 prepared output is invalid"))
		}
		for _, path := range []string{preparedOutput, preparedOutput + ".tmp", preparedOutput + ".preparing"} {
			if _, statErr := os.Lstat(path); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
				return Prepared{}, errors.New("T40.13 prepared output already exists")
			}
		}
	}
	if version >= 2 && version < 25 {
		if err := verifyHostToolchainForPlan(ctx, plan); err != nil {
			return Prepared{}, fmt.Errorf("verify frozen host toolchain before custody: %w", err)
		}
	}
	var supervision *custodySupervision
	var hostTools hostToolchainBinding
	cleanupControl := preparedCleanupControl{
		Schema: preparedCleanupSchema, PlanDigest: PlanDigest(planBytes),
		ModuleRoot: moduleRoot, Workspace: workspace,
	}
	controlPath := preparedOutput + ".preparing"
	controlWritten := false
	stateStarted := false
	supervisionDrained := false
	completed := false
	defer func() {
		if supervision != nil {
			if !supervisionDrained {
				retErr = errors.Join(retErr, supervision.Drain(""))
			}
			retErr = errors.Join(retErr, supervision.Close())
		}
		if completed {
			return
		}
		retErr = errors.Join(retErr, cleanupFailedPreparation(
			ctx, version, retErr, stateStarted, workspace, moduleRoot, preparedOutput, controlPath,
		))
	}()
	if version >= 25 {
		token, err := newCustodyToken()
		if err != nil {
			return Prepared{}, err
		}
		cleanupControl.Schema = preparedCleanupSchemaV2
		cleanupControl.SupervisionToken = token
		stateStarted = true
		if err := writePreparedCleanupControl(controlPath, cleanupControl); err != nil {
			return Prepared{}, err
		}
		controlWritten = true
		supervision, err = beginPrepareCustody(workspace, PlanDigest(planBytes), token)
		if err != nil {
			return Prepared{}, err
		}
	}
	if version >= 25 {
		hostTools, err = bindHostToolchainForPlan(ctx, plan)
		if err != nil {
			return Prepared{}, fmt.Errorf("verify frozen host toolchain before custody: %w", err)
		}
	}
	if err := VerifyInputs(moduleRoot); err != nil {
		return Prepared{}, err
	}
	var checkoutErr error
	if version >= 25 {
		checkoutErr = verifyCleanCheckoutWithBoundGit(
			ctx, moduleRoot, plan.SourceCommit, hostTools.gitCore, gitEnvironmentForContract(true),
		)
	} else {
		checkoutErr = verifyCleanCheckoutForPlan(ctx, moduleRoot, plan)
	}
	if checkoutErr != nil {
		return Prepared{}, checkoutErr
	}
	if _, err := HostPreflight(ctx, workspaceParent, plan); err != nil {
		return Prepared{}, err
	}
	releasePorts, err := reserveLoopbackPortsForPlan(plan, request.BasePort)
	if err != nil {
		return Prepared{}, err
	}
	defer releasePorts()
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("create T40.13 workspace: %w", err)
	}
	stateStarted = true
	var controls executionControls
	controlsDigest := ""
	if version >= 25 {
		controls, controlsDigest, err = createExecutionControls(workspace, hostTools)
		if err != nil {
			return Prepared{}, err
		}
	}
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		return Prepared{}, err
	}
	prepared := Prepared{
		Schema: PreparedSchema, PlanDigest: PlanDigest(planBytes), Profiles: []PreparedProfile{},
	}
	if supervision != nil {
		prepared.Schema = PreparedSchemaV2
		prepared.SupervisionToken = supervision.Token()
		prepared.ExecutionControlsSHA256 = controlsDigest
	}
	for index, profile := range profiles {
		profileRoot := filepath.Join(workspace, profile.Kind)
		if err := os.Mkdir(profileRoot, 0o700); err != nil {
			return Prepared{}, err
		}
		authored := filepath.Join(profileRoot, "authored")
		authorRequest := t401.AuthorRequest{
			ModuleRoot: moduleRoot, Output: authored, Profile: profile, ConfirmFrozen: true,
		}
		var receipt t401.Receipt
		if version >= 25 {
			gitPath, pathErr := hostTools.gitCore.pathForLaunch(ctx)
			if pathErr != nil {
				return Prepared{}, pathErr
			}
			receipt, err = t401.AuthorClosedSystemWithGit(
				ctx, authorRequest, gitPath, hostTools.gitCore.sha256,
				executionEnvironmentForControls(controls, false),
			)
		} else {
			receipt, err = t401.Author(ctx, authorRequest)
		}
		if err != nil {
			return Prepared{}, fmt.Errorf("author %s: %w", profile.Name, err)
		}
		repository := filepath.Join(authored, "repository.git")
		revisions := make(map[string]string, len(receipt.Revisions))
		for _, revision := range receipt.Revisions {
			revisions[revision.Revision] = revision.Commit
		}
		if version >= 25 {
			err = updateSourceRevisionWithGit(ctx, repository, revisions["a"], hostTools.gitCore, controls)
		} else {
			err = updateSourceRevision(ctx, repository, revisions["a"], false)
		}
		if err != nil {
			return Prepared{}, err
		}
		repositoryName, err := phebssync.RepoName(repository)
		if err != nil {
			return Prepared{}, err
		}
		catalogPath := filepath.Join(profileRoot, "service-catalog.json")
		catalogBytes, err := catalogFor(profile.Kind)
		if err != nil {
			return Prepared{}, err
		}
		if err := writePrivateNew(catalogPath, catalogBytes); err != nil {
			return Prepared{}, err
		}
		credential, err := randomCredential()
		if err != nil {
			return Prepared{}, err
		}
		credentialPath := filepath.Join(profileRoot, "api-key")
		if err := writePrivateNew(credentialPath, []byte(credential+"\n")); err != nil {
			return Prepared{}, err
		}
		dataDir := filepath.Join(profileRoot, "data")
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			return Prepared{}, err
		}
		address := fmt.Sprintf("127.0.0.1:%d", request.BasePort+index)
		configPath := filepath.Join(profileRoot, "phebs.yaml")
		configBytes, err := configFor(repository, repositoryName, catalogPath, dataDir, address, credential)
		if err != nil {
			return Prepared{}, err
		}
		if err := writePrivateNew(configPath, configBytes); err != nil {
			return Prepared{}, err
		}
		prepared.Profiles = append(prepared.Profiles, PreparedProfile{
			Name: profile.Name, Repository: repository, RepositoryName: repositoryName,
			Config: configPath, Credential: credentialPath, DataDir: dataDir,
			Address: address, Catalog: catalogPath, Revisions: revisions,
		})
	}
	if planSchemaVersion(plan.Schema) >= 25 {
		_, allocated, measureErr := measureDataBytesForPlan(plan, workspace)
		if measureErr != nil {
			return Prepared{}, measureErr
		}
		if _, err := hostPreflight(ctx, workspaceParent, allocated, plan); err != nil {
			return Prepared{}, err
		}
	}
	if planSchemaVersion(plan.Schema) >= 2 {
		if err := verifyHostToolchainForPlan(ctx, plan); err != nil {
			return Prepared{}, fmt.Errorf("verify frozen host toolchain after custody authoring: %w", err)
		}
	}
	if supervision != nil {
		if err := supervision.Drain(""); err != nil {
			return Prepared{}, fmt.Errorf("drain T40.13 preparation descendants: %w", err)
		}
		supervisionDrained = true
	}
	if preparedOutput != "" {
		encoded, encodeErr := MarshalPrepared(prepared)
		if encodeErr != nil {
			return Prepared{}, encodeErr
		}
		if planSchemaVersion(plan.Schema) >= 25 {
			if err := publishPreparedOutput(ctx, preparedOutput, encoded); err != nil {
				return Prepared{}, err
			}
		} else if err := writePrivateNew(preparedOutput, encoded); err != nil {
			return Prepared{}, err
		}
	}
	if version >= 25 && preparedOutput == "" {
		if err := custodyRetentionCause(ctx, nil); err != nil {
			return Prepared{}, err
		}
	}
	if controlWritten {
		if err := removePreparedCleanupControl(controlPath); err != nil {
			return Prepared{}, err
		}
	}
	completed = true
	return prepared, nil
}

func publishPreparedOutput(ctx context.Context, path string, raw []byte) error {
	if err := custodyRetentionCause(ctx, nil); err != nil {
		return err
	}
	if err := stageAtomicOutput(path, raw, MaxObservationBytes, false); err != nil {
		return err
	}
	if err := custodyRetentionCause(ctx, nil); err != nil {
		return err
	}
	if err := publishAtomicOutput(path, raw, MaxObservationBytes, false); err != nil {
		return err
	}
	return custodyRetentionCause(ctx, nil)
}

func cleanupFailedPreparation(
	ctx context.Context,
	version int,
	cause error,
	stateStarted bool,
	workspace, moduleRoot, preparedOutput, controlPath string,
) error {
	if version >= 25 {
		if retentionErr := custodyRetentionCause(ctx, cause); retentionErr != nil {
			return retentionErr
		}
		if stateStarted {
			return errors.New("T40.13 incomplete preparation custody is retained pending external process-absence proof")
		}
		return nil
	}
	cleanupErr := destroyCustody(workspace, moduleRoot)
	if cleanupErr == nil && preparedOutput != "" {
		cleanupErr = errors.Join(
			removePreparedPublication(preparedOutput),
			removePreparedCleanupControl(controlPath),
		)
	}
	return cleanupErr
}

func reserveLoopbackPorts(basePort int) (func(), error) {
	if basePort < 1024 || basePort > 65533 {
		return nil, errors.New("T40.13 loopback port preflight is invalid")
	}
	listeners, err := reserveLoopbackAddresses(
		fmt.Sprintf("127.0.0.1:%d", basePort),
		fmt.Sprintf("127.0.0.1:%d", basePort+1),
	)
	if err != nil {
		return nil, err
	}
	return func() { _ = releaseLoopbackAddresses(listeners) }, nil
}

func reserveLoopbackPortsForPlan(plan Plan, basePort int) (func(), error) {
	if planSchemaVersion(plan.Schema) < 25 {
		return func() {}, nil
	}
	return reserveLoopbackPorts(basePort)
}

func reserveLoopbackAddresses(addresses ...string) (map[string]net.Listener, error) {
	listeners := make(map[string]net.Listener, len(addresses))
	for _, address := range addresses {
		if !loopbackAddress(address) || listeners[address] != nil {
			_ = releaseLoopbackAddresses(listeners)
			return nil, errors.New("T40.13 loopback port preflight is invalid")
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			_ = releaseLoopbackAddresses(listeners)
			return nil, fmt.Errorf("T40.13 loopback address %s is unavailable: %w", address, err)
		}
		listeners[address] = listener
	}
	return listeners, nil
}

func releaseLoopbackAddresses(listeners map[string]net.Listener) error {
	var result error
	for address, listener := range listeners {
		result = errors.Join(result, listener.Close())
		delete(listeners, address)
	}
	return result
}

func destroyCustody(workspace, moduleRoot string) error {
	return destroyCustodyWith(workspace, moduleRoot, os.RemoveAll, time.Sleep, syncDirectory)
}

func destroyCustodyWith(
	workspace, moduleRoot string,
	removeAll func(string) error,
	wait func(time.Duration),
	syncParent func(string) error,
) error {
	if !filepath.IsAbs(workspace) || !filepath.IsAbs(moduleRoot) || workspace == moduleRoot ||
		isWithin(moduleRoot, workspace) || isWithin(workspace, moduleRoot) ||
		removeAll == nil || wait == nil || syncParent == nil {
		return errors.New("T40.13 custody cleanup scope is invalid")
	}
	for attempt := 0; attempt < custodyRemoveAttempts; attempt++ {
		info, err := os.Lstat(workspace)
		if errors.Is(err, os.ErrNotExist) {
			if attempt > 0 {
				wait(custodyRemoveSettleDelay)
			}
			if _, settleErr := os.Lstat(workspace); settleErr == nil {
				continue
			} else if !errors.Is(settleErr, os.ErrNotExist) {
				return errors.New("T40.13 custody cleanup absence check failed")
			}
			if err := syncParent(filepath.Dir(workspace)); err != nil {
				return fmt.Errorf("sync T40.13 custody parent after cleanup: %w", err)
			}
			if _, durableErr := os.Lstat(workspace); errors.Is(durableErr, os.ErrNotExist) {
				return nil
			} else if durableErr != nil {
				return errors.New("T40.13 custody cleanup durable absence check failed")
			}
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("T40.13 custody cleanup target is invalid")
		}
		removeErr := removeAll(workspace)
		if removeErr != nil && !errors.Is(removeErr, syscall.ENOTEMPTY) &&
			!errors.Is(removeErr, syscall.EEXIST) {
			return removeErr
		}
		if attempt+1 < custodyRemoveAttempts {
			wait(custodyRemoveRetryDelay)
		}
	}
	return errors.New("T40.13 custody cleanup did not settle within its retry bound")
}

func confirmCustodyDeletionDurable(workspace string) error {
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) == string(filepath.Separator) {
		return errors.New("T40.13 custody deletion proof scope is invalid")
	}
	if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("T40.13 custody remains after teardown"))
	}
	if err := syncDirectory(filepath.Dir(workspace)); err != nil {
		return fmt.Errorf("sync T40.13 custody parent after teardown: %w", err)
	}
	if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("T40.13 custody reappeared after durable deletion proof"))
	}
	return nil
}

func HostPreflight(ctx context.Context, dataParent string, plan Plan) (EnvironmentObservation, error) {
	return hostPreflight(ctx, dataParent, 0, plan)
}

func hostPreflight(
	ctx context.Context,
	dataParent string,
	workspaceAllocated int64,
	plan Plan,
) (EnvironmentObservation, error) {
	if ctx == nil || !filepath.IsAbs(dataParent) {
		return EnvironmentObservation{}, errors.New("T40.13 host preflight scope is invalid")
	}
	memory, err := physicalMemory(ctx, planSchemaVersion(plan.Schema) >= 25)
	if err != nil {
		return EnvironmentObservation{}, err
	}
	capacity, err := lifecycle.ProbeCapacity(ctx, dataParent)
	if err != nil {
		return EnvironmentObservation{}, fmt.Errorf("probe T40.13 custody capacity: %w", err)
	}
	if memory < plan.Safety.MinimumMemoryBytes || capacity.AvailableBytes < plan.Safety.MinimumAvailableDiskBytes {
		return EnvironmentObservation{}, errors.New("T40.13 frozen host prerequisite is not met")
	}
	usedPercent := int((capacity.UsedBytes * 100) / capacity.TotalBytes)
	if planSchemaVersion(plan.Schema) >= 10 {
		observedCapacity, capacityErr := lifecycle.NewGate(dataParent).Check(ctx, 0)
		if capacityErr != nil {
			return EnvironmentObservation{}, fmt.Errorf("gate T40.13 custody capacity: %w", capacityErr)
		}
		pressureErr := validatePressureHostPreflight(observedCapacity, plan.Safety)
		if planSchemaVersion(plan.Schema) >= 23 {
			pressureErr = validatePressureHostPreflightV23(
				observedCapacity, workspaceAllocated, plan.Safety,
			)
		}
		if planSchemaVersion(plan.Schema) >= 25 {
			pressureErr = validatePressureHostPreflightV25(
				observedCapacity, workspaceAllocated, plan.Safety,
			)
		}
		if pressureErr != nil {
			return EnvironmentObservation{}, errors.New("T40.13 frozen pressure host prerequisite is not met")
		}
		usedPercent = observedCapacity.UsedPercent
	}
	if err := preflightAtomicEvidenceProtocol(dataParent, plan); err != nil {
		return EnvironmentObservation{}, fmt.Errorf("probe T40.13 atomic evidence filesystem protocol: %w", err)
	}
	return EnvironmentObservation{
		OS: runtime.GOOS, Arch: runtime.GOARCH, MemoryBytes: memory,
		FilesystemTotalBytes: capacity.TotalBytes, FilesystemAvailableBytes: capacity.AvailableBytes,
		InitialUsedPercent: usedPercent,
	}, nil
}

func preflightAtomicEvidenceProtocol(dataParent string, plan Plan) (retErr error) {
	if planSchemaVersion(plan.Schema) < 25 {
		return nil
	}
	probeDirectory, err := os.MkdirTemp(dataParent, ".t4013-evidence-probe-")
	if err != nil {
		return err
	}
	output := filepath.Join(probeDirectory, "output")
	renamed := filepath.Join(probeDirectory, "renamed")
	defer func() {
		var cleanupErr error
		for _, path := range []string{output + ".tmp", output, renamed} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
		cleanupErr = errors.Join(cleanupErr, os.Remove(probeDirectory), syncDirectory(dataParent))
		if cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("clean atomic evidence filesystem probe: %w", cleanupErr))
		}
	}()
	if err := syncDirectory(dataParent); err != nil {
		return err
	}
	payload := []byte("T40.13 atomic evidence filesystem probe\n")
	if err := stageAtomicOutput(output, payload, len(payload), false); err != nil {
		return err
	}
	if err := publishAtomicOutput(output, payload, len(payload), false); err != nil {
		return err
	}
	if err := os.Rename(output, renamed); err != nil {
		return err
	}
	if err := syncDirectory(probeDirectory); err != nil {
		return err
	}
	if err := os.Remove(renamed); err != nil {
		return err
	}
	return syncDirectory(probeDirectory)
}

func physicalMemory(ctx context.Context, closedSystem bool) (int64, error) {
	switch runtime.GOOS {
	case "darwin":
		sysctl := "sysctl"
		if closedSystem {
			sysctl = "/usr/sbin/sysctl"
		}
		command := exec.CommandContext(ctx, sysctl, "-n", "hw.memsize")
		output, err := command.Output()
		if err != nil {
			return 0, errors.New("T40.13 physical memory is unavailable")
		}
		value, err := strconv.ParseInt(string(bytesTrimSpace(output)), 10, 64)
		if err != nil || value <= 0 {
			return 0, errors.New("T40.13 physical memory is invalid")
		}
		return value, nil
	case "linux":
		raw, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0, errors.New("T40.13 physical memory is unavailable")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
				value, parseErr := strconv.ParseInt(fields[1], 10, 64)
				if parseErr == nil && value > 0 && value <= (1<<63-1)/1024 {
					return value * 1024, nil
				}
			}
		}
		return 0, errors.New("T40.13 physical memory is invalid")
	default:
		return 0, errors.New("T40.13 physical memory probe is unsupported")
	}
}

func MarshalPrepared(value Prepared) ([]byte, error) {
	if err := validatePrepared(value); err != nil {
		return nil, errors.New("T40.13 prepared custody is invalid")
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxObservationBytes {
		return nil, errors.New("T40.13 prepared custody exceeds its fixed byte bound")
	}
	return encoded, nil
}

func DecodePrepared(raw []byte, planDigest string) (Prepared, error) {
	if len(raw) == 0 || len(raw) > MaxObservationBytes {
		return Prepared{}, errors.New("T40.13 prepared custody is outside its fixed byte bound")
	}
	var value Prepared
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Prepared{}, errors.New("T40.13 prepared custody cannot be decoded")
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Prepared{}, errors.New("T40.13 prepared custody has trailing data")
	}
	if value.PlanDigest != planDigest || validatePrepared(value) != nil {
		return Prepared{}, errors.New("T40.13 prepared custody is invalid")
	}
	return value, nil
}

func DestroyPrepared(value Prepared, moduleRoot string) (retErr error) {
	preparedRaw, err := MarshalPrepared(value)
	if err != nil {
		return errors.New("T40.13 prepared cleanup request is invalid")
	}
	boundValue, err := DecodePrepared(preparedRaw, value.PlanDigest)
	if err != nil {
		return errors.New("T40.13 prepared cleanup request is invalid")
	}
	realModuleRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil || !filepath.IsAbs(realModuleRoot) {
		return errors.New("T40.13 prepared cleanup request is invalid")
	}
	workspace := filepath.Dir(filepath.Dir(boundValue.Profiles[0].Config))
	if filepath.Dir(filepath.Dir(boundValue.Profiles[1].Config)) != workspace {
		return errors.New("T40.13 prepared cleanup custody differs")
	}
	realWorkspace, supervisionDirectory, err := custodyControlDirectory(workspace)
	if err != nil {
		return errors.New("T40.13 prepared cleanup custody is invalid")
	}
	if boundValue.SupervisionToken != "" {
		admissionLock, lockErr := lockRunRoot(filepath.Dir(realWorkspace))
		if lockErr != nil {
			return lockErr
		}
		defer func() {
			retErr = errors.Join(retErr, admissionLock.Close())
		}()
		currentRaw, marshalErr := MarshalPrepared(value)
		if marshalErr != nil || !bytes.Equal(currentRaw, preparedRaw) {
			return errors.Join(
				marshalErr, errors.New("T40.13 prepared cleanup admission changed before locking"),
			)
		}
	}
	for _, profile := range boundValue.Profiles {
		for _, path := range []string{profile.Repository, profile.Config, profile.Credential, profile.DataDir, profile.Catalog} {
			if !isWithin(path, workspace) {
				return errors.New("T40.13 prepared cleanup path escaped custody")
			}
		}
	}
	if boundValue.SupervisionToken == "" {
		for _, path := range []string{
			supervisionDirectory,
			supervisionDirectory + ".retiring",
			supervisionDirectory + ".retired",
		} {
			if _, statErr := os.Lstat(path); statErr == nil {
				return errors.New("T40.13 legacy cleanup found supervised custody authority")
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return errors.New("T40.13 legacy cleanup cannot establish supervision absence")
			}
		}
		return destroyCustody(realWorkspace, realModuleRoot)
	}
	if _, markerErr := os.Lstat(filepath.Join(realWorkspace, executedMarkerName)); markerErr == nil {
		return errors.New("T40.13 executed custody requires separately reviewed purge")
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return errors.New("T40.13 executed custody marker cannot be inspected")
	}
	return destroySupervisedPreparedCustody(
		realWorkspace, realModuleRoot, boundValue.PlanDigest, boundValue.SupervisionToken,
	)
}

// CleanupPrepared removes the exact custody named by a plan-bound prepared
// manifest, then removes the manifest itself. It is safe to call after Execute
// has already removed the custody directory.
func CleanupPrepared(moduleRoot, planPath, preparedPath, confirm string) (retErr error) {
	if confirm != CleanupConfirm || !filepath.IsAbs(moduleRoot) || !filepath.IsAbs(planPath) ||
		!filepath.IsAbs(preparedPath) {
		return errors.New("T40.13 prepared cleanup request is invalid")
	}
	realModuleRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		return errors.New("T40.13 prepared cleanup module root is invalid")
	}
	info, statErr := os.Lstat(planPath)
	if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("T40.13 prepared cleanup control is invalid")
	}
	planIdentity, plan, err := readPlanIdentity(planPath)
	if err != nil {
		return err
	}
	planBytes := planIdentity.raw
	if planSchemaVersion(plan.Schema) < 25 {
		return cleanupPreparedLegacy(realModuleRoot, planPath, preparedPath, planBytes)
	}
	planDigest := PlanDigest(planBytes)
	locator, err := readPreparedCleanupAdmission(realModuleRoot, preparedPath, planDigest)
	if err != nil {
		return err
	}
	canonicalWorkspace, _, err := custodyControlDirectory(locator.control.Workspace)
	if err != nil {
		return err
	}
	admissionLock, err := lockRunRoot(filepath.Dir(canonicalWorkspace))
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, admissionLock.Close())
	}()
	if err := planIdentity.revalidate(); err != nil {
		return err
	}
	locked, err := readPreparedCleanupAdmission(realModuleRoot, preparedPath, planDigest)
	if err != nil {
		return err
	}
	if !locator.equal(locked) {
		return errors.New("T40.13 prepared cleanup admission changed before locking")
	}
	control := locked.control
	controlPath := locked.controlPath
	if !locked.controlPreexisting {
		if err := ensurePreparedCleanupControl(controlPath, control); err != nil {
			return err
		}
	}
	if control.Schema != preparedCleanupSchemaV2 || control.PlanDigest != planDigest ||
		control.ModuleRoot != realModuleRoot || !filepath.IsAbs(control.Workspace) ||
		!hexIdentity(control.SupervisionToken, 64) ||
		control.Workspace == realModuleRoot || isWithin(control.Workspace, realModuleRoot) ||
		isWithin(realModuleRoot, control.Workspace) || planPath == control.Workspace ||
		locked.preparedPath == control.Workspace || isWithin(planPath, control.Workspace) ||
		isWithin(locked.preparedPath, control.Workspace) {
		return errors.New("T40.13 prepared cleanup controls must remain outside custody")
	}
	if _, markerErr := os.Lstat(filepath.Join(control.Workspace, executedMarkerName)); markerErr == nil {
		return errors.New("T40.13 executed custody requires separately reviewed purge")
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return errors.New("T40.13 executed custody marker cannot be inspected")
	}
	if err := destroySupervisedPreparedCustody(
		control.Workspace, realModuleRoot, planDigest, control.SupervisionToken,
	); err != nil {
		return err
	}
	if err := removePreparedPublication(locked.preparedPath); err != nil {
		return fmt.Errorf("remove T40.13 prepared manifest: %w", err)
	}
	if err := removePreparedCleanupControl(controlPath); err != nil {
		return err
	}
	return nil
}

type preparedCleanupAdmission struct {
	preparedPath       string
	controlPath        string
	preparedRaw        []byte
	controlRaw         []byte
	controlPreexisting bool
	control            preparedCleanupControl
}

func readPreparedCleanupAdmission(
	moduleRoot, preparedPath, planDigest string,
) (preparedCleanupAdmission, error) {
	canonical, err := canonicalExistingAuthorityPath(preparedPath)
	if err != nil {
		return preparedCleanupAdmission{}, errors.Join(
			err, errors.New("T40.13 prepared cleanup path is not canonical"),
		)
	}
	admission := preparedCleanupAdmission{
		preparedPath: canonical,
		controlPath:  canonical + ".preparing",
	}
	admission.preparedRaw, err = readPreparedPublicationBytes(canonical)
	preparedErr := err
	var prepared Prepared
	if preparedErr == nil {
		prepared, preparedErr = DecodePrepared(admission.preparedRaw, planDigest)
	}
	admission.controlRaw, err = readAtomicRegular(admission.controlPath, preparedCleanupMaxBytes)
	if err == nil {
		admission.controlPreexisting = true
		if err := decodeStrict(admission.controlRaw, &admission.control); err != nil ||
			!validPreparedCleanupIdentity(admission.control) {
			return preparedCleanupAdmission{}, errors.Join(
				err, errors.New("T40.13 prepared cleanup authority is invalid"),
			)
		}
		if admission.control.Schema == preparedCleanupSchemaV2 {
			if err := requireCanonicalCompact(admission.controlRaw, admission.control); err != nil {
				return preparedCleanupAdmission{}, errors.New(
					"T40.13 prepared cleanup authority is not canonical",
				)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return preparedCleanupAdmission{}, errors.New("T40.13 prepared cleanup authority cannot be inspected")
	}
	if preparedErr == nil {
		if prepared.Schema != PreparedSchemaV2 {
			return preparedCleanupAdmission{}, errors.New("T40.13 prepared custody lacks supervised identity")
		}
		expected := preparedCleanupControl{
			Schema: preparedCleanupSchemaV2, PlanDigest: planDigest, ModuleRoot: moduleRoot,
			Workspace:        filepath.Dir(filepath.Dir(prepared.Profiles[0].Config)),
			SupervisionToken: prepared.SupervisionToken,
		}
		if admission.controlPreexisting && admission.control != expected {
			return preparedCleanupAdmission{}, errors.New("T40.13 prepared cleanup authority differs")
		}
		admission.control = expected
	} else if !admission.controlPreexisting {
		return preparedCleanupAdmission{}, errors.Join(preparedErr, os.ErrNotExist)
	}
	return admission, nil
}

func (admission preparedCleanupAdmission) equal(other preparedCleanupAdmission) bool {
	return admission.preparedPath == other.preparedPath &&
		admission.controlPath == other.controlPath &&
		admission.controlPreexisting == other.controlPreexisting &&
		admission.control == other.control &&
		bytes.Equal(admission.preparedRaw, other.preparedRaw) &&
		bytes.Equal(admission.controlRaw, other.controlRaw)
}

func destroySupervisedPreparedCustody(
	workspace, moduleRoot, planDigest, token string,
) (retErr error) {
	status, supervision, err := inspectCustodySupervision(
		workspace, planDigest, token, custodyOperationPrepare, "",
	)
	if err == nil && status == custodyStatusCreated && supervision != nil {
		err = supervision.AbortPrepareAdmission()
		status = custodyStatusDrained
	}
	if err != nil || status == custodyStatusLive || status == custodyStatusIndeterminate || supervision == nil {
		if confirmCustodySupervisionRetired(
			workspace, planDigest, token, custodyOperationPrepare, "",
		) == nil && confirmCustodyDeletionDurable(workspace) == nil {
			return nil
		}
		var closeErr error
		if supervision != nil {
			closeErr = supervision.Close()
		}
		return errors.Join(
			err, closeErr,
			errors.New("T40.13 prepared custody is not durably drained; custody retained"),
		)
	}
	defer func() {
		retErr = errors.Join(retErr, supervision.Close())
	}()
	if status == custodyStatusDrained {
		if err := supervision.BeginFinalization(""); err != nil {
			return err
		}
		if err := destroyCustody(workspace, moduleRoot); err != nil {
			return err
		}
		if err := supervision.DrainTerminal(); err != nil {
			return err
		}
	} else if status != custodyStatusTerminal {
		return errors.New("T40.13 prepared custody supervision state is invalid")
	} else if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("T40.13 terminal prepared custody still exists"))
	}
	if err := supervision.Retire(); err != nil {
		if retiredErr := confirmCustodySupervisionRetired(
			workspace, planDigest, token, custodyOperationPrepare, "",
		); retiredErr != nil {
			return errors.Join(err, retiredErr)
		}
	}
	return nil
}

func cleanupPreparedLegacy(moduleRoot, planPath, preparedPath string, planBytes []byte) error {
	info, err := os.Lstat(preparedPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("T40.13 prepared cleanup control is invalid")
	}
	preparedBytes, err := os.ReadFile(preparedPath)
	if err != nil {
		return fmt.Errorf("read T40.13 cleanup custody: %w", err)
	}
	prepared, err := DecodePrepared(preparedBytes, PlanDigest(planBytes))
	if err != nil {
		return err
	}
	if prepared.Schema != PreparedSchema {
		return errors.New("T40.13 historical prepared custody schema differs")
	}
	workspace := filepath.Dir(filepath.Dir(prepared.Profiles[0].Config))
	if planPath == workspace || preparedPath == workspace || isWithin(planPath, workspace) ||
		isWithin(preparedPath, workspace) {
		return errors.New("T40.13 prepared cleanup controls must remain outside custody")
	}
	if err := DestroyPrepared(prepared, moduleRoot); err != nil {
		return err
	}
	return os.Remove(preparedPath)
}

func readPreparedPublicationBytes(path string) ([]byte, error) {
	var selected []byte
	for _, candidate := range []string{path, path + ".tmp"} {
		raw, err := readAtomicRegular(candidate, MaxObservationBytes)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if selected != nil && !bytes.Equal(selected, raw) {
			return nil, errors.New("T40.13 prepared publication controls differ")
		}
		selected = raw
	}
	if selected == nil {
		return nil, os.ErrNotExist
	}
	return selected, nil
}

func writePreparedCleanupControl(path string, value preparedCleanupControl) error {
	if !validPreparedCleanupIdentity(value) || !digestIdentity(value.PlanDigest) ||
		!filepath.IsAbs(value.ModuleRoot) || !filepath.IsAbs(value.Workspace) ||
		filepath.Clean(value.ModuleRoot) == string(filepath.Separator) ||
		filepath.Clean(value.Workspace) == string(filepath.Separator) {
		return errors.New("T40.13 prepared cleanup authority is invalid")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > preparedCleanupMaxBytes {
		return errors.New("T40.13 prepared cleanup authority exceeds its byte bound")
	}
	if err := writePrivateNew(path, raw); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func ensurePreparedCleanupControl(path string, value preparedCleanupControl) error {
	existing, err := readPreparedCleanupControl(path)
	if err == nil {
		if existing != value {
			return errors.New("T40.13 prepared cleanup authority differs")
		}
		return syncDirectory(filepath.Dir(path))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writePreparedCleanupControl(path, value)
}

func readPreparedCleanupControl(path string) (preparedCleanupControl, error) {
	raw, err := readAtomicRegular(path, preparedCleanupMaxBytes)
	if err != nil {
		return preparedCleanupControl{}, err
	}
	var value preparedCleanupControl
	if err := decodeStrict(raw, &value); err != nil || !validPreparedCleanupIdentity(value) ||
		!digestIdentity(value.PlanDigest) || !filepath.IsAbs(value.ModuleRoot) ||
		!filepath.IsAbs(value.Workspace) || filepath.Clean(value.ModuleRoot) == string(filepath.Separator) ||
		filepath.Clean(value.Workspace) == string(filepath.Separator) {
		return preparedCleanupControl{}, errors.New("T40.13 prepared cleanup authority is invalid")
	}
	if value.Schema == preparedCleanupSchemaV2 {
		if err := requireCanonicalCompact(raw, value); err != nil {
			return preparedCleanupControl{}, errors.New("T40.13 prepared cleanup authority is not canonical")
		}
	}
	return value, nil
}

func removePreparedPublication(path string) error {
	parent := filepath.Dir(path)
	if err := removeAtomicTemporary(path+".tmp", parent); err != nil {
		return err
	}
	return removeAtomicTemporary(path, parent)
}

func removePreparedCleanupControl(path string) error {
	if path == ".preparing" {
		return nil
	}
	return removeAtomicTemporary(path, filepath.Dir(path))
}

func validatePrepared(value Prepared) error {
	validIdentity := value.Schema == PreparedSchema && value.SupervisionToken == "" && value.ExecutionControlsSHA256 == "" ||
		value.Schema == PreparedSchemaV2 && hexIdentity(value.SupervisionToken, 64) &&
			digestIdentity(value.ExecutionControlsSHA256)
	if !validIdentity || !digestIdentity(value.PlanDigest) || len(value.Profiles) != 2 ||
		value.Profiles[0].Name != "structural-2m-v1" || value.Profiles[1].Name != "semantic-262144-v1" {
		return errors.New("invalid prepared identity")
	}
	seenAddresses := map[string]bool{}
	for _, profile := range value.Profiles {
		if reponame.Validate(profile.RepositoryName) != nil || !loopbackAddress(profile.Address) || seenAddresses[profile.Address] ||
			!absolutePrivatePaths(profile) || len(profile.Revisions) != 3 ||
			!hexIdentity(profile.Revisions["a"], 40) || !hexIdentity(profile.Revisions["b"], 40) ||
			!hexIdentity(profile.Revisions["a-return"], 40) {
			return errors.New("invalid prepared profile")
		}
		seenAddresses[profile.Address] = true
	}
	return nil
}

func validPreparedCleanupIdentity(value preparedCleanupControl) bool {
	return value.Schema == preparedCleanupSchema && value.SupervisionToken == "" ||
		value.Schema == preparedCleanupSchemaV2 && hexIdentity(value.SupervisionToken, 64)
}

func absolutePrivatePaths(profile PreparedProfile) bool {
	paths := []string{profile.Repository, profile.Config, profile.Credential, profile.DataDir, profile.Catalog}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
			return false
		}
	}
	return true
}

func loopbackAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1024 && number <= 65535
}

func catalogFor(kind string) ([]byte, error) {
	return catalogForShape(kind, 10_000)
}

func catalogForShape(kind string, structuralCells uint64) ([]byte, error) {
	catalog := servicecatalog.Catalog{
		Schema:    servicecatalog.Schema,
		Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "t4013-neutral", Version: "v1"},
		Services:  []servicecatalog.Service{}, Memberships: []servicecatalog.Membership{},
		Unowned: []servicecatalog.UnownedPlacement{
			{Path: ".phebs", Origin: servicecatalog.OriginBase},
			{Path: "go.mod", Origin: servicecatalog.OriginBase},
		},
	}
	switch kind {
	case "structural":
		if structuralCells == 0 || structuralCells > 10_000 {
			return nil, errors.New("T40.13 structural catalog shape is invalid")
		}
		serviceCount := min(100, int(structuralCells))
		for ordinal := 0; ordinal < serviceCount; ordinal++ {
			key := fmt.Sprintf("service-%03d", ordinal)
			catalog.Services = append(catalog.Services, servicecatalog.Service{
				Key: key, DisplayName: fmt.Sprintf("Neutral service %03d", ordinal),
				Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
			})
			catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
				ServiceKey: key, Path: fmt.Sprintf("structural/cells/b000/c%05d", ordinal),
				Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
			})
		}
		bucketCount := (int(structuralCells) + 99) / 100
		for bucket := 1; bucket < bucketCount; bucket++ {
			catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{
				Path: fmt.Sprintf("structural/cells/b%03d", bucket), Origin: servicecatalog.OriginBase,
			})
		}
	case "semantic":
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: "semantic", DisplayName: "Neutral semantic service",
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		})
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
			ServiceKey: "semantic", Path: "semantic", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
	default:
		return nil, errors.New("T40.13 profile kind is invalid")
	}
	return servicecatalog.Canonical(catalog)
}

func configFor(repository, repositoryName, catalogPath, dataDir, address, credential string) ([]byte, error) {
	secure := false
	enabled := true
	value := config.Config{
		Server:      config.Server{Addr: address, DataDir: dataDir},
		Auth:        config.Auth{APIKey: credential, CookieSecure: &secure},
		Sync:        config.Sync{PollInterval: "250ms", ResyncInterval: "0"},
		Diagnostics: config.Diagnostics{Jobs: true, Candidates: true, Extraction: true},
		Lifecycle:   config.Lifecycle{Enabled: &enabled},
		Experimental: config.Experimental{
			ProvisionalProtoExtraction: true, ProvisionalThriftExtraction: true,
			ProvisionalKafkaExtraction: true,
		},
		ServiceCatalogs: map[string]config.ServiceCatalog{
			repositoryName: {Kind: servicecatalog.AuthorityOperator, ID: "t4013-neutral", Version: "v1", Path: catalogPath},
		},
		Connections: []config.Connection{{Name: "t4013", Type: "git", URL: repository, Watch: true}},
	}
	return yaml.Marshal(value)
}

func updateSourceRevision(ctx context.Context, repository, commit string, closedGit bool) error {
	if !hexIdentity(commit, 40) {
		return errors.New("T40.13 source revision is invalid")
	}
	if _, err := gitOutputForContract(ctx, repository, closedGit,
		"update-ref", "refs/heads/main", commit); err != nil {
		return err
	}
	return nil
}

func updateSourceRevisionWithGit(
	ctx context.Context, repository, commit string, git boundExecutable, controls executionControls,
) error {
	if !hexIdentity(commit, 40) {
		return errors.New("T40.13 source revision is invalid")
	}
	path, err := git.pathForLaunch(ctx)
	if err != nil {
		return err
	}
	if _, err := gitOutputWithExecutableEnvironment(ctx, repository, path,
		executionEnvironmentForControls(controls, false),
		"update-ref", "refs/heads/main", commit); err != nil {
		return err
	}
	return nil
}

func gitOutputForContract(ctx context.Context, directory string, closed bool, args ...string) (string, error) {
	executable := "git"
	if closed {
		var err error
		executable, err = resolveGitCoreExecutable(ctx)
		if err != nil {
			return "", err
		}
	}
	return gitOutputWithExecutable(ctx, directory, executable, closed, args...)
}

func gitOutputWithExecutable(
	ctx context.Context, directory, executable string, closed bool, args ...string,
) (string, error) {
	if closed && !filepath.IsAbs(executable) {
		return "", errors.New("T40.13 bound Git executable is invalid")
	}
	return gitOutputWithExecutableEnvironment(ctx, directory, executable, gitEnvironmentForContract(closed), args...)
}

func gitOutputWithExecutableEnvironment(
	ctx context.Context, directory, executable string, environment []string, args ...string,
) (string, error) {
	if executable == "" || len(environment) == 0 {
		return "", errors.New("T40.13 closed Git execution is invalid")
	}
	command := exec.CommandContext(ctx, executable, append([]string{"-C", directory}, args...)...)
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("T40.13 git command failed")
	}
	return string(bytesTrimSpace(output)), nil
}

func verifyCleanCheckoutForPlan(ctx context.Context, moduleRoot string, plan Plan) error {
	return verifyCleanCheckoutWithGit(ctx, moduleRoot, plan.SourceCommit,
		planSchemaVersion(plan.Schema) >= 25)
}

func verifyCleanCheckoutWithBoundGit(
	ctx context.Context, moduleRoot, sourceCommit string, git boundExecutable, environment []string,
) error {
	path, err := git.pathForLaunch(ctx)
	if err != nil {
		return err
	}
	if ctx == nil || !filepath.IsAbs(moduleRoot) || !hexIdentity(sourceCommit, 40) {
		return errors.New("T40.13 checkout identity is invalid")
	}
	commit, err := gitOutputWithExecutableEnvironment(ctx, moduleRoot, path, environment, "rev-parse", "HEAD")
	if err != nil || commit != sourceCommit {
		return errors.New("T40.13 checkout differs from the frozen source commit")
	}
	path, err = git.pathForLaunch(ctx)
	if err != nil {
		return err
	}
	status, err := gitOutputWithExecutableEnvironment(ctx, moduleRoot, path, environment,
		"status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status != "" {
		return errors.New("T40.13 checkout has modified or untracked files")
	}
	return nil
}

func verifyCleanCheckout(ctx context.Context, moduleRoot, sourceCommit string) error {
	return verifyCleanCheckoutWithGit(ctx, moduleRoot, sourceCommit, false)
}

func verifyCleanCheckoutWithGit(ctx context.Context, moduleRoot, sourceCommit string, closed bool) error {
	if ctx == nil || !filepath.IsAbs(moduleRoot) || !hexIdentity(sourceCommit, 40) {
		return errors.New("T40.13 checkout identity is invalid")
	}
	commit, err := gitOutputForContract(ctx, moduleRoot, closed, "rev-parse", "HEAD")
	if err != nil || commit != sourceCommit {
		return errors.New("T40.13 checkout differs from the frozen source commit")
	}
	status, err := gitOutputForContract(ctx, moduleRoot, closed,
		"status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status != "" {
		return errors.New("T40.13 checkout has modified or untracked files")
	}
	return nil
}

func gitEnvironment() []string {
	return gitEnvironmentForContract(false)
}

func gitEnvironmentForContract(closed bool) []string {
	if closed {
		return scrubExecutionEnvironment()
	}
	environment := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			environment = append(environment, entry)
		}
	}
	return append(environment,
		"GIT_NO_LAZY_FETCH=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
	)
}

func randomCredential() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func writePrivateNew(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func isWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
