package t421

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// Separate process streams share one byte allowance, not one interleavable
// stream. Only the archive-capable parent pays this per-copy write lock.
type epochBackupOutput struct {
	mu        sync.Mutex
	remaining int64
	server    *checkoutCommandOutput
	backup    *checkoutCommandOutput
	restore   *checkoutCommandOutput
}

func (output *epochBackupOutput) Write(raw []byte) (int, error) {
	return output.write(output.server, raw)
}

type epochBackupCommandOutput struct {
	shared  *epochBackupOutput
	restore bool
}

func (output epochBackupCommandOutput) Write(raw []byte) (int, error) {
	stream := output.shared.backup
	if output.restore {
		stream = output.shared.restore
	}
	return output.shared.write(stream, raw)
}

func (output *epochBackupOutput) write(stream *checkoutCommandOutput, raw []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.server.err != nil {
		stream.cancel()
		return 0, output.server.err
	}
	if int64(len(raw)) > output.remaining {
		output.server.err = ErrExecutionEpochOne
		output.server.cancel()
		stream.cancel()
		return 0, output.server.err
	}
	output.remaining -= int64(len(raw))
	n, err := stream.Write(raw)
	if err != nil {
		output.server.err = err
	}
	return n, err
}

// Called after a late failure, never as a successful continuation boundary.
// Attempt both fences independently: a failed parent Pause must not short
// circuit the controller fence. An existing sticky failure already refuses.
func (run *ExecutionEpochOneRun) fenceFailedBackup(ctx context.Context) {
	_ = run.flow.parent.Pause(ctx)
	_ = run.flow.controller.Fence()
}

// BackupAndStop consumes the completed pressure-75 boundary once. It keeps only
// the retired server's native endpoint through the actual backup command and
// returns only after server/session join. No restore, archive verification,
// installation removal, phase-work receipt or source-release claim is made.
func (run *ExecutionEpochOneRun) BackupAndStop(ctx context.Context) (result ExecutionEpochOneResult, retErr error) {
	if run == nil || ctx == nil || ctx.Err() != nil || run.flow == nil || run.control == nil || run.inspection == nil {
		return result, ErrExecutionEpochOne
	}
	run.mu.Lock()
	valid := run.epoch.Epoch == 4 && run.backupAllowed && !run.backupUsed && !run.stopping && run.err == nil && time.Now().Before(run.lifetimeDeadline)
	if valid {
		run.backupUsed = true
	}
	run.mu.Unlock()
	if !valid {
		return result, ErrExecutionEpochOne
	}
	reader := run.inspection
	reader.mu.Lock()
	valid = reader.err == nil && reader.projection.Phase == "pressure_75" && reader.pressure.step == 9 && reader.finalUsed && len(reader.evidence.rows) > 0 && reader.evidence.rows[len(reader.evidence.rows)-1].SelectorAccepted
	reader.mu.Unlock()
	if !valid {
		return result, ErrExecutionEpochOne
	}
	deadline := time.Now().Add(time.Duration(run.flow.plan.PhaseDeadlines[11].DeadlineMS) * time.Millisecond)
	if deadline.After(run.lifetimeDeadline) {
		deadline = run.lifetimeDeadline
	}
	operation, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	operationDone := make(chan struct{})
	run.mu.Lock()
	if run.stopping || run.err != nil {
		run.mu.Unlock()
		return result, ErrExecutionEpochOne
	}
	run.backupCancel, run.backupDone = cancel, operationDone
	run.mu.Unlock()
	// An attempted irreversible retirement always owns stop/join, including
	// failed backup/bootstrap. Never leave a borrowed live endpoint behind.
	defer func() {
		if retErr != nil {
			run.mu.Lock()
			run.err = ErrExecutionEpochOne
			run.mu.Unlock()
		}
		close(operationDone)
		run.stopOnce.Do(func() { close(run.stop) })
		cleanup, stop := context.WithTimeout(context.Background(), 40*time.Second)
		defer stop()
		var err error
		result, err = run.Wait(cleanup)
		retErr = errors.Join(retErr, err)
	}()
	flow := run.flow
	// The final server Pause is already reserved by the 24-pair pressure
	// budget. Its echo now follows SDK close and the final DA checkpoint.
	if flow.parent.Pause(operation) != nil || flow.controller.Fence() != nil || flow.store.Fence() != nil || run.control.Pause(operation) != nil ||
		flow.store.Wait(operation, 5) != nil || flow.controller.RetireBackupEndpoint() != nil {
		return result, ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.backupRetired = true
	run.retainParent = true
	if run.stopping || run.err != nil || run.phaseTimer == nil || !time.Now().Before(run.phaseDeadline) || !run.phaseTimer.Stop() {
		run.mu.Unlock()
		return result, ErrExecutionEpochOne
	}
	close(run.phaseDone) // The stopped callback cannot own this completion.
	run.setPhaseDeadlineLocked(deadline)
	run.mu.Unlock()
	flow.mu.Lock()
	flow.retained = run
	flow.mu.Unlock()
	if flow.parent.Checkpoint(operation) != nil || run.processPhaseAdvance(operation, 12) != nil || flow.parent.Resume(12) != nil {
		return result, ErrExecutionEpochOne
	}
	if err := run.runNativeArchive(operation, false); err != nil {
		return result, err
	}
	if operation.Err() != nil {
		return result, ErrExecutionEpochOne
	}
	run.mu.Lock()
	run.backupComplete = true
	run.mu.Unlock()
	return result, nil
}

// runNativeArchive uses only the two frozen command recipes and the same epoch
// four configuration/tool custody. Producer ten owns backup; eleven owns restore.
// The restore caller separately owns prior server join and target emptying.
func (run *ExecutionEpochOneRun) runNativeArchive(ctx context.Context, restore bool) (retErr error) {
	flow := run.flow
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	epochs.mu.Lock()
	valid := author.borrowedBy == run && epochs.active && epochs.checkLocked(ctx, 4) == nil
	path, tools, environment, err := flow.checkEpochTools(ctx, 4)
	epochs.mu.Unlock()
	author.mu.Unlock()
	if !valid || err != nil {
		return ErrExecutionEpochOne
	}
	// Backup is not a semantic serve process. Keep the same closed tool
	// environment, but remove the selected index-only recipe additions.
	for index := range tools {
		if tools[index].Role == "zoekt-git-index" {
			var filtered []string
			for _, value := range tools[index].Environment {
				if !strings.HasPrefix(value, dispatchadmission.IndexOfferEnvironment+"=") && value != "ZOEKT_DISABLE_CATFILE_BATCH=true" {
					filtered = append(filtered, value)
				}
			}
			tools[index].Environment = filtered
		}
	}
	producer, site := uint32(10), executionSiteBackup
	verb, flag := "backup", "-output"
	if restore {
		producer, site, verb, flag = 11, executionSiteRestore, "restore", "-backup"
	}
	view, err := flow.controller.ProducerLaunch(producer)
	if err != nil || view.Phase != 12 {
		return ErrExecutionEpochOne
	}
	var files [4]*os.File
	defer func() {
		for _, file := range files {
			if file != nil {
				_ = file.Close()
			}
		}
	}()
	for i := 0; i < 4; i += 2 {
		files[i], files[i+1], err = dispatchadmission.NewPipe()
		if err != nil {
			return ErrExecutionEpochOne
		}
	}
	storeFile, storeConfig, err := flow.store.Open(producer)
	if err != nil {
		return ErrExecutionEpochOne
	}
	defer func() { _ = storeFile.Close() }()
	command := exec.Command(path, verb, "-config", run.epoch.ConfigPath, flag, filepath.Join(run.epoch.BackupRoot, "archive"))
	command.Dir, command.Env = author.parent, environment
	backupOutput := epochBackupCommandOutput{shared: run.backupOutput, restore: restore}
	command.Stdout, command.Stderr = backupOutput, backupOutput
	command.ExtraFiles = []*os.File{files[1], files[3], storeFile}
	command.WaitDelay = 5 * time.Second
	prepareProductionSession(command)
	handle, err := flow.parent.StartInPhase(ctx, 12, dispatchadmission.Site{ID: site, Role: executionRolePhebs}, command)
	if err != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if restore {
		run.restoreStarted = true
	} else {
		run.backupStarted = true
	}
	run.mu.Unlock()
	waited := make(chan error, 1)
	go func() { waited <- handle.Wait() }()
	joined := false
	var waitErr error
	var served <-chan error
	defer func() {
		if !joined {
			_ = t4013.KillPrivateProcessSession(command.Process.Pid)
		}
		var empty bool
		var stopErr error
		joined, empty, stopErr = finishExecutionProcessSession(command.Process.Pid, waited, joined, waitErr, time.Now().Add(30*time.Second))
		run.mu.Lock()
		if restore {
			run.restoreJoined, run.restoreSessionEmpty = joined, empty
		} else {
			run.backupJoined, run.backupSessionEmpty = joined, empty
		}
		run.mu.Unlock()
		if !joined || !empty || stopErr != nil || waitErr != nil {
			retErr = ErrExecutionEpochOne
		}
		if served != nil {
			joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer joinCancel()
			select {
			case err := <-served:
				if err != nil {
					retErr = ErrExecutionEpochOne
				}
			case <-joinCtx.Done():
				flow.release()
				<-served
				retErr = ErrExecutionEpochOne
			}
		}
		// Native Wait joins this command's copier before the sole work scan.
		// A failed native/protocol tail preserves real positive counters but
		// cannot supply complete coverage. Never inspect an unjoined buffer.
		stream := run.backupOutput.backup
		if restore {
			stream = run.backupOutput.restore
		}
		observed, observeErr := observeArchiveWork(stream, flow.plan, producer, run.attemptInput, joined, retErr == nil && ctx.Err() == nil)
		if observeErr != nil {
			retErr = ErrExecutionEpochOne
		}
		run.mu.Lock()
		if restore {
			run.result.RestoreWork = observed
		} else {
			run.backupWork = observed
		}
		run.mu.Unlock()
	}()
	if files[1].Close() != nil || files[3].Close() != nil || storeFile.Close() != nil {
		return ErrExecutionEpochOne
	}
	files[1], files[3] = nil, nil
	config := dispatchadmission.PhaseControlConfig{Phases: []uint32{12}, InitialPhase: 12, MaximumPhases: 1, MaximumWireBytes: 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	record := dispatchadmission.ProductionBootstrap{Program: dispatchadmission.ProgramPhebs, InputSHA256: run.attemptInput, Producer: view.Producer, Phase: 12, Limits: view.Limits, Control: config, Tools: tools, Store: &storeConfig}
	if dispatchadmission.SendProductionBootstrap(ctx, files[0], files[2], record) != nil {
		return ErrExecutionEpochOne
	}
	completion := make(chan error, 1)
	file := files[0]
	files[0] = nil
	go func() {
		completion <- flow.controller.ServeChecked(flow.controller.Context(), producer, command.Process.Pid, file, func(checkCtx context.Context, _ dispatchadmission.Site) error {
			author.mu.Lock()
			defer author.mu.Unlock()
			epochs.mu.Lock()
			defer epochs.mu.Unlock()
			if epochs.checkLocked(checkCtx, 4) != nil {
				return ErrExecutionEpochOne
			}
			_, _, _, err := flow.checkEpochTools(checkCtx, 4)
			return err
		})
	}()
	served = completion
	select {
	case waitErr = <-waited:
		joined = true
	case <-ctx.Done():
		return ErrExecutionEpochOne
	}
	if waitErr != nil || flow.store.Wait(ctx, producer) != nil {
		return ErrExecutionEpochOne
	}
	stream := run.backupOutput.backup
	if restore {
		stream = run.backupOutput.restore
	}
	digest, err := epochArchiveCommandDigest(stream.buffer.Bytes(), filepath.Join(run.epoch.BackupRoot, "archive"), restore)
	if err != nil || stream.err != nil {
		return ErrExecutionEpochOne
	}
	run.mu.Lock()
	if restore {
		run.restoreManifestSHA256 = digest
	} else {
		run.backupManifestSHA256 = digest
	}
	run.mu.Unlock()
	return nil
}

// Parse the actual owning command's joined output, not an expected-plan value
// or another manifest read. Ordinary diagnostic lines remain private; exactly
// one matching native success line and a complete stream are required.
func epochArchiveCommandDigest(raw []byte, archive string, restore bool) (string, error) {
	if len(raw) > 64<<20 {
		return "", ErrExecutionEpochOne
	}
	prefix := []byte("backup published: " + archive + " (")
	family := []byte("backup published:")
	if restore {
		prefix = []byte("restore verified and imported: ")
		family = []byte("restore verified and imported:")
	}
	var digest string
	for len(raw) > 0 {
		end := bytes.IndexByte(raw, '\n')
		if end < 0 {
			return "", ErrExecutionEpochOne
		}
		line := raw[:end]
		raw = raw[end+1:]
		if !bytes.HasPrefix(line, family) {
			continue
		}
		if digest != "" || !bytes.HasPrefix(line, prefix) {
			return "", ErrExecutionEpochOne
		}
		value := line[len(prefix):]
		if !restore {
			if len(value) == 0 || value[len(value)-1] != ')' {
				return "", ErrExecutionEpochOne
			}
			value = value[:len(value)-1]
		}
		if len(value) != 71 {
			return "", ErrExecutionEpochOne
		}
		digest = string(value)
		if !validDigest(digest) || strings.ToLower(digest) != digest {
			return "", ErrExecutionEpochOne
		}
	}
	if digest == "" {
		return "", ErrExecutionEpochOne
	}
	return digest, nil
}

func epochBackupClosedPrefix(ctx context.Context, result ExecutionEpochOneResult) bool {
	return epochArchiveClosedPrefix(ctx, result, 10)
}

func epochRestoreClosedPrefix(ctx context.Context, result ExecutionEpochOneResult) bool {
	return epochArchiveClosedPrefix(ctx, result, 11)
}

func epochRestoredClosedPrefix(ctx context.Context, result ExecutionEpochOneResult) bool {
	return epochArchiveClosedPrefix(ctx, result, 6)
}

func epochArchiveClosedPrefix(ctx context.Context, result ExecutionEpochOneResult, stage uint32) bool {
	if stage != 10 && stage != 11 && stage != 6 {
		return false
	}
	opened, ordinal := 5, uint64(8)
	producers := []uint32{1, 2, 3, 4, 5, 7, 8, 9, 10}
	storeProducers := []uint32{2, 3, 4, 5, 10}
	if stage == 11 || stage == 6 {
		opened, ordinal = 6, 9
		producers = append(producers, 11)
		storeProducers = append(storeProducers, 11)
	}
	if stage == 6 {
		opened, ordinal = 7, 10
		producers = append(producers, 6)
		storeProducers = append(storeProducers, 6)
	}
	if ctx == nil || ctx.Err() != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || result.Store.Store.Phase != 12 || result.Store.Opened != opened || result.Store.TerminalEOF != opened || result.Store.Complete {
		return false
	}
	for _, id := range producers {
		found := false
		for _, p := range result.Accounting.Producers {
			if p.Producer == id {
				found = p.Attached && p.Active == 0 && p.Closed
				if id == 1 {
					found = p.Attached && p.Active == 0 && p.Closed == (stage == 6) && p.Ordinal == ordinal
				}
				if id == 5 {
					found = found && p.Checkpoint == 11
				}
				if id >= 7 && id <= 9 {
					found = found && p.Ordinal == authorCustodyAttempts(int(id-7))
				}
			}
		}
		if !found {
			return false
		}
	}
	for _, id := range storeProducers {
		found := false
		for _, p := range result.Store.Store.Producers {
			if p.Producer == id {
				found = p.Attached && p.Calls == 0 && p.Transactions == 0 && p.Closed
				if id == 4 {
					found = p.Attached && p.Calls == 0 && p.Transactions == 0 && !p.Closed && p.TerminalFencedEOF && p.TerminalPhase == 8 && p.Checkpoint == 8
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}
