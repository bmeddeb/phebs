//go:build darwin || linux

package t4013

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	custodyControllerName = "controller.lock"
	custodyLeaseName      = "descendants.lock"
	custodyStateName      = "state.json"
	custodyStateMaxBytes  = 2 << 10
	custodyMinimumFD      = 64
	custodyCreatingInfix  = ".creating."
	custodyRetiringSuffix = ".retiring"
	custodyRetiredSuffix  = ".retired"
)

var errCustodyCreationActive = errors.New("T40.13 custody creation is already active")

func beginPrepareCustody(workspace, planDigest, token string) (_ *custodySupervision, retErr error) {
	workspace, directory, err := custodyControlDirectory(workspace)
	if err != nil || !digestIdentity(planDigest) || !hexIdentity(token, 64) {
		return nil, errors.Join(err, errors.New("T40.13 prepare custody identity is invalid"))
	}
	state := custodyControlState{
		Schema: custodyControlSchema, Token: token,
		PlanDigest: planDigest, Workspace: workspace,
		Operation: custodyOperationPrepare, Phase: custodyPhaseCreated,
	}
	if err := validateCustodyStateEncoding(state); err != nil {
		return nil, err
	}
	controller, lease, err := createOrRecoverPrepareCustody(directory, state)
	if err != nil {
		return nil, err
	}
	supervision := &custodySupervision{
		directory: directory, state: state, controller: controller, lease: lease,
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, supervision.Close())
		}
	}()
	if err := setCustodyCloseOnExec(lease, false); err != nil {
		return nil, err
	}
	state.Phase = custodyPhaseLive
	if err := writeCustodyState(directory, state); err != nil {
		return nil, err
	}
	supervision.state = state
	return supervision, nil
}

// createOrRecoverPrepareCustody publishes only a complete created control.
// The caller's separately durable token makes every pre-live crash retryable.
func createOrRecoverPrepareCustody(
	directory string,
	state custodyControlState,
) (_ *os.File, _ *os.File, retErr error) {
	stage := directory + custodyCreatingInfix + state.Token
	if _, err := os.Lstat(directory); err == nil {
		if _, stageErr := os.Lstat(stage); !errors.Is(stageErr, os.ErrNotExist) {
			return nil, nil, errors.Join(stageErr, errors.New("T40.13 custody creation paths conflict"))
		}
		return openCreatedCustody(directory, state)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("inspect T40.13 custody control: %w", err)
	}
	if err := ensureCustodyStage(stage); err != nil {
		return nil, nil, err
	}
	return publishCreatedPrepareCustody(directory, stage, state)
}

func recoverCreatedPrepareCustody(
	directory string,
	state custodyControlState,
) (*custodySupervision, error) {
	stage := directory + custodyCreatingInfix + state.Token
	if _, err := os.Lstat(stage); err != nil {
		return nil, err
	}
	controller, lease, err := publishCreatedPrepareCustody(directory, stage, state)
	if err != nil {
		return nil, err
	}
	return &custodySupervision{
		directory: directory, state: state, controller: controller, lease: lease,
	}, nil
}

func publishCreatedPrepareCustody(
	directory string,
	stage string,
	state custodyControlState,
) (_ *os.File, _ *os.File, retErr error) {
	controller, err := ensureCustodyLock(filepath.Join(stage, custodyControllerName))
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, controller.Close())
		}
	}()
	locked, err := tryCustodyLock(controller)
	if err != nil || !locked {
		return nil, nil, errors.Join(err, errCustodyCreationActive)
	}

	// A publisher can die after rename and before its caller observes success.
	// The stable controller inode tells a retry which path now owns the stage.
	if _, err := os.Lstat(stage); errors.Is(err, os.ErrNotExist) {
		if err := sameCustodyLock(filepath.Join(directory, custodyControllerName), controller); err != nil {
			return nil, nil, err
		}
		return openCreatedCustodyWithController(directory, state, controller)
	} else if err != nil {
		return nil, nil, fmt.Errorf("inspect T40.13 custody creation stage: %w", err)
	}
	if err := validateCustodyCreationStage(stage); err != nil {
		return nil, nil, err
	}
	lease, err := ensureCustodyLock(filepath.Join(stage, custodyLeaseName))
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, lease.Close())
		}
	}()
	if locked, err := tryCustodyLock(lease); err != nil || !locked {
		return nil, nil, errors.Join(err, errCustodyCreationActive)
	}
	if err := removeCustodyStateStage(stage); err != nil {
		return nil, nil, err
	}
	if existing, err := readCustodyState(stage); err == nil {
		if existing != state {
			return nil, nil, errors.New("T40.13 custody creation state differs")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := syncCustodyControls(stage, controller, lease); err != nil {
			return nil, nil, err
		}
		if err := writeCustodyState(stage, state); err != nil {
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}
	if err := os.Rename(stage, directory); err != nil {
		return nil, nil, fmt.Errorf("publish T40.13 custody control: %w", err)
	}
	if err := syncDirectory(filepath.Dir(directory)); err != nil {
		return nil, nil, fmt.Errorf("sync T40.13 custody publication: %w", err)
	}
	return controller, lease, nil
}

func openCreatedCustody(
	directory string,
	state custodyControlState,
) (_ *os.File, _ *os.File, retErr error) {
	if err := validateCustodyDirectory(directory); err != nil {
		return nil, nil, err
	}
	controller, err := openCustodyLock(filepath.Join(directory, custodyControllerName))
	if err != nil {
		return nil, nil, err
	}
	locked, err := tryCustodyLock(controller)
	if err != nil || !locked {
		return nil, nil, errors.Join(
			err, errors.New("T40.13 custody controller lock is unavailable"), controller.Close(),
		)
	}
	controllerResult, lease, err := openCreatedCustodyWithController(directory, state, controller)
	if err != nil {
		return nil, nil, errors.Join(err, controller.Close())
	}
	return controllerResult, lease, nil
}

func openCreatedCustodyWithController(
	directory string,
	state custodyControlState,
	controller *os.File,
) (_ *os.File, _ *os.File, retErr error) {
	if err := recoverCustodyTransition(directory); err != nil {
		return nil, nil, err
	}
	existing, err := readCustodyStateFile(filepath.Join(directory, custodyStateName))
	if err != nil || existing != state {
		return nil, nil, errors.Join(err, errors.New("T40.13 created custody state differs"))
	}
	lease, err := openCustodyLock(filepath.Join(directory, custodyLeaseName))
	if err != nil {
		return nil, nil, err
	}
	if locked, err := tryCustodyLock(lease); err != nil || !locked {
		return nil, nil, errors.Join(
			err, errors.New("T40.13 created custody lease is unavailable"), lease.Close(),
		)
	}
	return controller, lease, nil
}

func beginExecuteCustody(workspace, planDigest, token string) (_ *custodySupervision, retErr error) {
	status, supervision, err := inspectCustodySupervision(
		workspace, planDigest, token, custodyOperationPrepare, "",
	)
	if err != nil || status != custodyStatusDrained || supervision == nil {
		return nil, errors.Join(
			err, errors.New("T40.13 prepare custody is not drained"), supervision.Close(),
		)
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, supervision.Close())
		}
	}()
	if err := setCustodyCloseOnExec(supervision.lease, false); err != nil {
		return nil, err
	}
	state := supervision.state
	state.Operation = custodyOperationExecute
	state.Phase = custodyPhaseLive
	if err := writeCustodyState(supervision.directory, state); err != nil {
		return nil, err
	}
	supervision.state = state
	return supervision, nil
}

// inspectCustodySupervision returns a held controller and lease for drained or
// terminal controls, so a caller can transition or retire them without a race.
func inspectCustodySupervision(
	workspace, planDigest, token string,
	operation custodyOperation,
	checkpointDigest string,
) (custodyStatus, *custodySupervision, error) {
	workspace, directory, err := custodyControlDirectory(workspace)
	if err != nil || !digestIdentity(planDigest) || !hexIdentity(token, 64) ||
		operation != custodyOperationPrepare && operation != custodyOperationExecute ||
		checkpointDigest != "" && !digestIdentity(checkpointDigest) {
		return custodyStatusIndeterminate, nil,
			errors.Join(err, errors.New("T40.13 custody inspection identity is invalid"))
	}
	if err := validateCustodyDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) && operation == custodyOperationPrepare &&
			checkpointDigest == "" {
			state := custodyControlState{
				Schema: custodyControlSchema, Token: token, PlanDigest: planDigest,
				Workspace: workspace, Operation: operation, Phase: custodyPhaseCreated,
			}
			supervision, recoverErr := recoverCreatedPrepareCustody(directory, state)
			if errors.Is(recoverErr, errCustodyCreationActive) {
				return custodyStatusLive, nil, nil
			}
			if recoverErr == nil {
				return custodyStatusCreated, supervision, nil
			}
			return custodyStatusIndeterminate, nil, errors.Join(err, recoverErr)
		}
		return custodyStatusIndeterminate, nil, err
	}
	controller, err := openCustodyLock(filepath.Join(directory, custodyControllerName))
	if err != nil {
		return custodyStatusIndeterminate, nil, err
	}
	locked, err := tryCustodyLock(controller)
	if err != nil {
		return custodyStatusIndeterminate, nil, errors.Join(err, controller.Close())
	}
	if !locked {
		return custodyStatusLive, nil, controller.Close()
	}
	if err := recoverCustodyTransition(directory); err != nil {
		return custodyStatusIndeterminate, nil, errors.Join(err, controller.Close())
	}
	state, err := readCustodyState(directory)
	if err != nil || state.Token != token || state.PlanDigest != planDigest ||
		state.Workspace != workspace || state.Operation != operation ||
		state.CheckpointDigest != checkpointDigest {
		return custodyStatusIndeterminate, nil, errors.Join(
			err, errors.New("T40.13 custody state differs from the expected identity"),
			controller.Close(),
		)
	}
	lease, err := openCustodyLock(filepath.Join(directory, custodyLeaseName))
	if err != nil {
		return custodyStatusIndeterminate, nil, errors.Join(err, controller.Close())
	}
	locked, err = tryCustodyLock(lease)
	if err != nil {
		return custodyStatusIndeterminate, nil, errors.Join(err, lease.Close(), controller.Close())
	}
	if !locked {
		return custodyStatusLive, nil, errors.Join(lease.Close(), controller.Close())
	}
	supervision := &custodySupervision{
		directory: directory, state: state, controller: controller, lease: lease,
	}
	switch state.Phase {
	case custodyPhaseCreated:
		if state.Operation == custodyOperationPrepare && state.CheckpointDigest == "" {
			return custodyStatusCreated, supervision, nil
		}
		return custodyStatusIndeterminate, nil, supervision.Close()
	case custodyPhaseDrained:
		return custodyStatusDrained, supervision, nil
	case custodyPhaseTerminal:
		return custodyStatusTerminal, supervision, nil
	default:
		// Only the controller that performed the absence proof may record a
		// drain. Its hard death can therefore never turn an active state into
		// drained merely because the two kernel locks later become free.
		return custodyStatusIndeterminate, nil, supervision.Close()
	}
}

func (supervision *custodySupervision) Drain(checkpointDigest string) error {
	if supervision == nil || supervision.state.Phase != custodyPhaseLive {
		return errors.New("T40.13 custody is not live")
	}
	if supervision.state.Operation == custodyOperationPrepare && checkpointDigest != "" ||
		supervision.state.Operation == custodyOperationExecute && !digestIdentity(checkpointDigest) {
		return errors.New("T40.13 custody drain checkpoint digest is invalid")
	}
	state := supervision.state
	state.Phase = custodyPhaseDrained
	state.CheckpointDigest = checkpointDigest
	return supervision.drainTo(state)
}

func (supervision *custodySupervision) AbortPrepareAdmission() error {
	if supervision == nil || supervision.state.Phase != custodyPhaseCreated ||
		supervision.state.Operation != custodyOperationPrepare {
		return errors.New("T40.13 prepare custody is not created")
	}
	state := supervision.state
	state.Phase = custodyPhaseDrained
	return supervision.drainTo(state)
}

func (supervision *custodySupervision) AbortExecuteAdmission() error {
	if supervision == nil || supervision.state.Phase != custodyPhaseLive ||
		supervision.state.Operation != custodyOperationExecute {
		return errors.New("T40.13 execute custody is not live")
	}
	state := supervision.state
	state.Operation = custodyOperationPrepare
	state.Phase = custodyPhaseDrained
	state.CheckpointDigest = ""
	return supervision.drainTo(state)
}

func (supervision *custodySupervision) BeginFinalization(checkpointDigest string) error {
	if supervision == nil || supervision.state.Phase != custodyPhaseDrained ||
		supervision.state.CheckpointDigest != checkpointDigest {
		return errors.New("T40.13 drained custody checkpoint differs")
	}
	if supervision.state.Operation == custodyOperationExecute && !digestIdentity(checkpointDigest) {
		return errors.New("T40.13 execute finalization checkpoint digest is invalid")
	}
	if err := setCustodyCloseOnExec(supervision.lease, false); err != nil {
		return err
	}
	state := supervision.state
	state.Phase = custodyPhaseFinalizing
	if err := writeCustodyState(supervision.directory, state); err != nil {
		return err
	}
	supervision.state = state
	return nil
}

func (supervision *custodySupervision) DrainTerminal() error {
	if supervision == nil || supervision.state.Phase != custodyPhaseFinalizing {
		return errors.New("T40.13 custody is not finalizing")
	}
	state := supervision.state
	state.Phase = custodyPhaseTerminal
	return supervision.drainTo(state)
}

func (supervision *custodySupervision) drainTo(state custodyControlState) error {
	if supervision.lease != nil {
		if err := supervision.lease.Close(); err != nil {
			return fmt.Errorf("close T40.13 inherited descendant lease: %w", err)
		}
		supervision.lease = nil
	}
	lease, err := openCustodyLock(filepath.Join(supervision.directory, custodyLeaseName))
	if err != nil {
		return err
	}
	locked, err := tryCustodyLock(lease)
	if err != nil {
		return errors.Join(err, lease.Close())
	}
	if !locked {
		return errors.Join(errCustodyDescendantsLive, lease.Close())
	}
	supervision.lease = lease
	if err := writeCustodyState(supervision.directory, state); err != nil {
		return err
	}
	supervision.state = state
	return nil
}

func (supervision *custodySupervision) Retire() error {
	if supervision == nil || supervision.state.Phase != custodyPhaseTerminal {
		return errors.New("T40.13 terminal custody is not held")
	}
	retiring := supervision.directory + custodyRetiringSuffix
	retired := supervision.directory + custodyRetiredSuffix
	if _, err := os.Lstat(supervision.directory); err == nil {
		if supervision.controller == nil || supervision.lease == nil {
			return errors.New("T40.13 terminal custody locks are not held")
		}
		state, stateErr := readCustodyState(supervision.directory)
		if stateErr != nil || state != supervision.state {
			return errors.Join(
				stateErr, errors.New("T40.13 terminal custody changed before retirement"),
			)
		}
		for _, path := range []string{
			retiring, retired, supervision.directory + custodyCreatingInfix + supervision.state.Token,
		} {
			if _, pathErr := os.Lstat(path); !errors.Is(pathErr, os.ErrNotExist) {
				return errors.Join(pathErr, errors.New("T40.13 custody retirement path already exists"))
			}
		}
		if err := os.Rename(supervision.directory, retiring); err != nil {
			return fmt.Errorf("commit T40.13 custody retirement: %w", err)
		}
		if err := syncDirectory(filepath.Dir(supervision.directory)); err != nil {
			return fmt.Errorf("sync T40.13 custody retirement commit: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect T40.13 custody retirement: %w", err)
	}
	if err := finishCustodyRetirement(supervision.directory, supervision.state); err != nil {
		return err
	}
	return supervision.Close()
}

func confirmCustodyRetirement(directory string, expected custodyControlState) error {
	if _, err := os.Lstat(directory); err == nil {
		return errors.New("T40.13 custody control has not committed retirement")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	creation := directory + custodyCreatingInfix + expected.Token
	if _, err := os.Lstat(creation); err == nil {
		return errors.New("T40.13 custody creation has not published its control")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return finishCustodyRetirement(directory, expected)
}

// finishCustodyRetirement keeps one strict terminal authority durable until
// both the renamed directory and its lock files are gone.
func finishCustodyRetirement(directory string, expected custodyControlState) error {
	parent := filepath.Dir(directory)
	retiring := directory + custodyRetiringSuffix
	retired := directory + custodyRetiredSuffix
	retiringExists, err := custodyPathExists(retiring)
	if err != nil {
		return err
	}
	retiredExists, err := custodyPathExists(retired)
	if err != nil {
		return err
	}
	if !retiringExists && !retiredExists {
		return syncDirectory(parent)
	}

	var state custodyControlState
	if retiringExists {
		if err := validateCustodyRetiringDirectory(retiring); err != nil {
			return err
		}
		if err := syncDirectory(parent); err != nil {
			return fmt.Errorf("sync T40.13 custody retirement commit: %w", err)
		}
		statePath := filepath.Join(retiring, custodyStateName)
		stateExists, err := custodyPathExists(statePath)
		if err != nil {
			return err
		}
		if stateExists == retiredExists {
			return errors.New("T40.13 custody retirement authority is ambiguous")
		}
		if stateExists {
			state, err = readCustodyState(retiring)
		} else {
			state, err = readCustodyStateFile(retired)
		}
		if err != nil {
			return err
		}
		if err := validateTerminalRetirementState(directory, state, expected); err != nil {
			return err
		}
		for _, name := range []string{custodyLeaseName, custodyControllerName} {
			if err := removeCustodyRetirementLock(filepath.Join(retiring, name)); err != nil {
				return err
			}
		}
		if err := syncDirectory(retiring); err != nil {
			return fmt.Errorf("sync T40.13 retiring custody locks: %w", err)
		}
		if stateExists {
			if err := os.Rename(statePath, retired); err != nil {
				return fmt.Errorf("publish T40.13 retired custody authority: %w", err)
			}
			if err := syncDirectory(retiring); err != nil {
				return fmt.Errorf("sync T40.13 retiring custody state: %w", err)
			}
			if err := syncDirectory(parent); err != nil {
				return fmt.Errorf("sync T40.13 retired custody authority: %w", err)
			}
		}
		if err := os.Remove(retiring); err != nil {
			return fmt.Errorf("remove T40.13 retiring custody directory: %w", err)
		}
		if err := syncDirectory(parent); err != nil {
			return fmt.Errorf("sync T40.13 retiring custody deletion: %w", err)
		}
	} else {
		if err := syncDirectory(parent); err != nil {
			return fmt.Errorf("sync T40.13 retiring custody deletion: %w", err)
		}
		state, err = readCustodyStateFile(retired)
		if err != nil {
			return err
		}
		if err := validateTerminalRetirementState(directory, state, expected); err != nil {
			return err
		}
	}
	if err := os.Remove(retired); err != nil {
		return fmt.Errorf("remove T40.13 retired custody authority: %w", err)
	}
	return syncDirectory(parent)
}

func validateTerminalRetirementState(
	directory string,
	state custodyControlState,
	expected custodyControlState,
) error {
	if state.Phase != custodyPhaseTerminal || directory != state.Workspace+".t4013-supervision" {
		return errors.New("T40.13 custody retirement state is not terminal authority")
	}
	if state != expected {
		return errors.New("T40.13 custody retirement state differs")
	}
	return nil
}

func validateCustodyRetiringDirectory(directory string) error {
	if err := validateCustodyDirectory(directory); err != nil {
		return err
	}
	entries, err := readDirectoryBounded(directory, 3)
	if err != nil {
		return fmt.Errorf("read T40.13 retiring custody: %w", err)
	}
	if len(entries) > 3 {
		return errors.New("T40.13 retiring custody has unexpected entries")
	}
	for _, entry := range entries {
		switch entry.Name() {
		case custodyStateName:
			if _, err := readCustodyState(directory); err != nil {
				return err
			}
		case custodyControllerName, custodyLeaseName:
			if err := validateCustodyRetirementLock(filepath.Join(directory, entry.Name())); err != nil {
				return err
			}
		default:
			return errors.New("T40.13 retiring custody has an unexpected entry")
		}
	}
	return nil
}

func validateCustodyRetirementLock(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o600 || info.Size() != 0 {
		return errors.Join(err, errors.New("T40.13 retiring custody lock is invalid"))
	}
	return nil
}

func removeCustodyRetirementLock(path string) error {
	if err := validateCustodyRetirementLock(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove T40.13 retiring custody lock: %w", err)
	}
	return nil
}

func custodyPathExists(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

func (supervision *custodySupervision) Close() error {
	if supervision == nil {
		return nil
	}
	var errs []error
	if supervision.lease != nil {
		errs = append(errs, supervision.lease.Close())
		supervision.lease = nil
	}
	if supervision.controller != nil {
		errs = append(errs, supervision.controller.Close())
		supervision.controller = nil
	}
	return errors.Join(errs...)
}

func validateCustodyDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.Join(err, errors.New("T40.13 custody directory is invalid"))
	}
	file, err := openNoFollowDirectory(directory)
	if err != nil {
		return fmt.Errorf("open T40.13 custody directory: %w", err)
	}
	opened, statErr := file.Stat()
	return errors.Join(func() error {
		if statErr != nil || !opened.IsDir() || !os.SameFile(info, opened) {
			return errors.New("T40.13 custody directory changed during open")
		}
		return nil
	}(), statErr, file.Close())
}

func ensureCustodyStage(directory string) error {
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create T40.13 custody stage: %w", err)
	}
	return validateCustodyCreationStage(directory)
}

func validateCustodyCreationStage(directory string) error {
	if err := validateCustodyDirectory(directory); err != nil {
		return err
	}
	entries, err := readDirectoryBounded(directory, 4)
	if err != nil {
		return fmt.Errorf("read T40.13 custody stage: %w", err)
	}
	if len(entries) > 4 {
		return errors.New("T40.13 custody stage has unexpected entries")
	}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			info.Mode().Perm() != 0o600 {
			return errors.Join(err, errors.New("T40.13 custody stage entry is invalid"))
		}
		switch entry.Name() {
		case custodyControllerName, custodyLeaseName:
			if info.Size() != 0 {
				return errors.New("T40.13 custody stage lock is invalid")
			}
		case custodyStateName, custodyStateName + ".tmp":
			if info.Size() < 0 || info.Size() > custodyStateMaxBytes {
				return errors.New("T40.13 custody stage state exceeds its byte bound")
			}
		default:
			return errors.New("T40.13 custody stage has an unexpected entry")
		}
	}
	return nil
}

func ensureCustodyLock(path string) (*os.File, error) {
	file, err := createCustodyLock(path)
	if err == nil {
		return file, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return openCustodyLock(path)
}

func sameCustodyLock(path string, file *os.File) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, errors.New("T40.13 published custody lock is invalid"))
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		return errors.Join(statErr, errors.New("T40.13 custody publication changed its lock"))
	}
	return nil
}

func removeCustodyStateStage(directory string) error {
	path := filepath.Join(directory, custodyStateName+".tmp")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o600 || info.Size() < 0 || info.Size() > custodyStateMaxBytes {
		return errors.Join(err, errors.New("T40.13 custody state stage is invalid"))
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove T40.13 custody state stage: %w", err)
	}
	return syncDirectory(directory)
}

func recoverCustodyTransition(directory string) error {
	temporary := filepath.Join(directory, custodyStateName+".tmp")
	if _, err := os.Lstat(temporary); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	stable, err := readCustodyStateFile(filepath.Join(directory, custodyStateName))
	if err != nil {
		return err
	}
	rollback := stable
	switch {
	case stable.Operation == custodyOperationPrepare && stable.Phase == custodyPhaseCreated:
		rollback.Phase = custodyPhaseLive
	case stable.Operation == custodyOperationPrepare && stable.Phase == custodyPhaseDrained:
		rollback.Operation = custodyOperationExecute
		rollback.Phase = custodyPhaseLive
	case stable.Operation == custodyOperationExecute && stable.Phase == custodyPhaseDrained:
		rollback.Phase = custodyPhaseFinalizing
	}
	staged, err := readCustodyStateFile(temporary)
	if err != nil {
		return removeCustodyStateStage(directory)
	}
	if staged == rollback && staged != stable {
		return removeCustodyStateStage(directory)
	}
	proved := stable
	switch {
	case stable.Operation == custodyOperationPrepare &&
		(stable.Phase == custodyPhaseCreated || stable.Phase == custodyPhaseLive):
		proved.Phase = custodyPhaseDrained
	case stable.Operation == custodyOperationExecute && stable.Phase == custodyPhaseLive &&
		staged.Operation == custodyOperationExecute:
		proved.Phase = custodyPhaseDrained
		proved.CheckpointDigest = staged.CheckpointDigest
	case stable.Operation == custodyOperationExecute && stable.Phase == custodyPhaseLive &&
		staged.Operation == custodyOperationPrepare:
		proved.Operation = custodyOperationPrepare
		proved.Phase = custodyPhaseDrained
		proved.CheckpointDigest = ""
	case stable.Phase == custodyPhaseFinalizing:
		proved.Phase = custodyPhaseTerminal
	}
	if staged != proved || staged == stable {
		return errors.New("T40.13 custody state stage differs from a recoverable transition")
	}
	if err := os.Rename(temporary, filepath.Join(directory, custodyStateName)); err != nil {
		return fmt.Errorf("commit T40.13 proved custody transition: %w", err)
	}
	return syncDirectory(directory)
}

func createCustodyLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create T40.13 custody lock: %w", err)
	}
	file, err = reserveCustodyDescriptor(file)
	if err != nil {
		return nil, err
	}
	if err := setCustodyCloseOnExec(file, true); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func openCustodyLock(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 0 {
		return nil, errors.Join(err, errors.New("T40.13 custody lock is invalid"))
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open T40.13 custody lock: %w", err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.Join(
			errors.New("T40.13 custody lock changed during open"), statErr, file.Close(),
		)
	}
	file, err = reserveCustodyDescriptor(file)
	if err != nil {
		return nil, err
	}
	if err := setCustodyCloseOnExec(file, true); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func reserveCustodyDescriptor(file *os.File) (*os.File, error) {
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, custodyMinimumFD)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("reserve T40.13 custody descriptor: %w", err), file.Close(),
		)
	}
	duplicate := os.NewFile(uintptr(fd), file.Name())
	if duplicate == nil {
		return nil, errors.Join(
			errors.New("adopt T40.13 reserved custody descriptor"), unix.Close(fd), file.Close(),
		)
	}
	if err := file.Close(); err != nil {
		return nil, errors.Join(err, duplicate.Close())
	}
	return duplicate, nil
}

func syncCustodyControls(directory string, files ...*os.File) error {
	for _, file := range files {
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync T40.13 custody lock: %w", err)
		}
	}
	return syncDirectory(directory)
}

func tryCustodyLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func setCustodyCloseOnExec(file *os.File, enabled bool) error {
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil {
		return fmt.Errorf("read T40.13 custody descriptor flags: %w", err)
	}
	if enabled {
		flags |= unix.FD_CLOEXEC
	} else {
		flags &^= unix.FD_CLOEXEC
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETFD, flags); err != nil {
		return fmt.Errorf("write T40.13 custody descriptor flags: %w", err)
	}
	return nil
}

func writeCustodyState(directory string, state custodyControlState) error {
	if err := validateCustodyStateEncoding(state); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > custodyStateMaxBytes {
		return errors.New("T40.13 custody state exceeds its byte bound")
	}
	temporary := filepath.Join(directory, custodyStateName+".tmp")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create T40.13 custody state stage: %w", err)
	}
	written, writeErr := file.Write(raw)
	if writeErr == nil && written != len(raw) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("persist T40.13 custody state stage: %w", errors.Join(writeErr, closeErr))
	}
	if err := os.Rename(temporary, filepath.Join(directory, custodyStateName)); err != nil {
		return fmt.Errorf("publish T40.13 custody state: %w", err)
	}
	return syncDirectory(directory)
}

func validateCustodyStateEncoding(state custodyControlState) error {
	if err := validateCustodyState(state); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(raw)+1 > custodyStateMaxBytes {
		return errors.New("T40.13 custody state exceeds its byte bound")
	}
	return nil
}

func readCustodyState(directory string) (custodyControlState, error) {
	if _, err := os.Lstat(filepath.Join(directory, custodyStateName+".tmp")); err == nil {
		return custodyControlState{}, errors.New("T40.13 custody state has an unfinished stage")
	} else if !errors.Is(err, os.ErrNotExist) {
		return custodyControlState{}, err
	}
	return readCustodyStateFile(filepath.Join(directory, custodyStateName))
}

func readCustodyStateFile(path string) (custodyControlState, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o600 {
		return custodyControlState{}, errors.Join(err, errors.New("T40.13 custody state file is invalid"))
	}
	raw, err := readAtomicRegular(path, custodyStateMaxBytes)
	if err != nil {
		return custodyControlState{}, err
	}
	var state custodyControlState
	if err := decodeStrict(raw, &state); err != nil {
		return custodyControlState{}, fmt.Errorf("decode T40.13 custody state: %w", err)
	}
	if err := validateCustodyState(state); err != nil {
		return custodyControlState{}, err
	}
	if err := requireCanonicalCompact(raw, state); err != nil {
		return custodyControlState{}, errors.New("T40.13 custody state is not canonical")
	}
	return state, nil
}
