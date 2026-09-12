//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
)

const t422BackupFixture = "PHEBS_T422_BACKUP_RETIREMENT_TEST"

// This is actual selected SDK/DA/PC retirement, the real backup CLI function,
// native surrealkv/export and joined shutdown on empty private fixture data.
// It does not reproduce protected executable custody, pressure/F, the frozen
// corpus, BackupAndStop's author/epoch constructor or a whole phase receipt.
func TestT422BackupRetiredNativeEndpoint(t *testing.T) {
	testT422ArchiveRetiredNativeEndpoint(t, false, false, false)
}

func TestT422RestoreRetiredNativeEndpoint(t *testing.T) {
	testT422ArchiveRetiredNativeEndpoint(t, true, false, false)
}

// Actual retired engine -> parent PC -> backup FD7 -> FD6 walk, followed by
// actual import/repair engine scopes. Tiny native data, not a pressure-volume
// or frozen-corpus deadline/whole-phase proof.
func TestT422ArchiveWorkspaceNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpoint(t, true, false, true)
}

func TestT422ArchiveWorkspaceNativeFailures(t *testing.T) {
	for _, mode := range []string{"lost_release", "parent_cancel", "report_loss"} {
		t.Run(mode, func(t *testing.T) {
			testT422ArchiveRetiredNativeEndpointFailure(t, true, false, true, mode)
		})
	}
}

func testT422ArchiveRetiredNativeEndpoint(t *testing.T, restore, workspace, archiveWorkspace bool, cleanup ...bool) {
	testT422ArchiveRetiredNativeEndpointFailure(t, restore, workspace, archiveWorkspace, "", cleanup...)
}

func testT422ArchiveRetiredNativeEndpointFailure(t *testing.T, restore, workspace, archiveWorkspace bool, failure string, cleanup ...bool) {
	if failure != "" && (!restore || workspace || !archiveWorkspace || len(cleanup) != 0 ||
		failure != "lost_release" && failure != "parent_cancel" && failure != "report_loss") {
		t.Fatal("invalid native archive failure fixture")
	}
	cleanupWorkspace := len(cleanup) > 0 && cleanup[0]
	allOwners := len(cleanup) == 2 && cleanup[1]
	if cleanupWorkspace && (!workspace || restore) {
		t.Fatal("cleanup fixture requires workspace-only mode")
	}
	surreal, err := exec.LookPath("surreal")
	if err != nil {
		t.Skip("surreal binary not installed")
	}
	surreal, err = filepath.EvalSymlinks(surreal)
	if err != nil {
		t.Fatal(err)
	}
	fixtureLimit := 3 * time.Minute
	if cleanupWorkspace {
		fixtureLimit = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), fixtureLimit)
	defer cancel()
	identity, err := store.InspectSurrealBinaryContext(ctx, surreal)
	if err != nil {
		t.Fatal(err)
	}
	if restore && identity.Version != "3.2.0" {
		t.Skip("selected restore replay fixture requires SurrealDB 3.2.0")
	}
	root, err := os.MkdirTemp("", "t422-backup-retirement-")
	if err != nil {
		t.Fatal(err)
	}
	closedSessions := true
	t.Cleanup(func() {
		if t.Failed() || !closedSessions {
			t.Log("retained native fixture:", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	configPath := filepath.Join(root, "phebs.yaml")
	configRaw := []byte(fmt.Sprintf("server:\n  data_dir: %s\n", filepath.Join(root, "data")))
	if err := os.WriteFile(configPath, configRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	record := t422ServeFlagsRecord()
	record.Producer.ID, record.Producer.Binding = 5, [32]byte{5}
	record.SemanticMode, record.InputSHA256, record.Phase = dispatchadmission.ProductionSemanticV3, [32]byte{7}, 8
	record.Limits.Producers, record.Limits.Sites, record.Limits.Phases = 2, 32, 5
	record.Limits.Attempts, record.Limits.ActivePerProducer, record.Limits.WireBytes = 20, 2, 1<<20
	record.Control = dispatchadmission.PhaseControlConfig{BackupEndpointCarry: true, OwnerControl: true,
		Phases: []uint32{8, 9, 10, 11}, InitialPhase: 8, MaximumPhases: 4, MaximumWireBytes: 24 * 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	var workspaceFile *os.File
	if workspace || archiveWorkspace {
		if workspace {
			semantic, _ := t422LifecycleBootstrapRecord(t)
			record.InputSHA256 = semantic.InputSHA256
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		workspaceFile, err = os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = workspaceFile.Close() })
		binding, e := dispatchadmission.DescribeProductionWorkspace(workspaceFile, root)
		if e != nil {
			t.Fatal(e)
		}
		record.Workspace = &binding
	}
	if archiveWorkspace {
		record.Control.BackupMeasurementMaximum = recovery.BackupCheckpointMaximum()
		record.Control.MaximumWireBytes += uint64(record.Control.BackupMeasurementMaximum) * 4 * dispatchadmission.FrameBytes
	}
	for i := range record.Tools {
		if record.Tools[i].Role == "surreal" {
			record.Tools[i].Path = surreal
		}
	}
	backupProducer := record.Producer
	backupProducer.ID, backupProducer.Binding = 10, [32]byte{10}
	configuration := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer, backupProducer}}
	restoreProducer := record.Producer
	restoreProducer.ID, restoreProducer.Binding = 11, [32]byte{11}
	storeProducers := []storeaccounting.Producer{{ID: 5, Calls: 40, Transactions: 2}, {ID: 10, Calls: 1, Transactions: 1}}
	wireProducers := []storeaccounting.WireProducer{{ID: 5, Binding: record.Producer.Binding, Phases: 1920}, {ID: 10, Binding: backupProducer.Binding, Phases: 2048}}
	if restore {
		record.Limits.Producers, record.Limits.Sites = 3, 48
		configuration.Limits = record.Limits
		configuration.Producers = append(configuration.Producers, restoreProducer)
		storeProducers = append(storeProducers, storeaccounting.Producer{ID: 11, Calls: 1, Transactions: 1})
		wireProducers = append(wireProducers, storeaccounting.WireProducer{ID: 11, Binding: restoreProducer.Binding, Phases: 2048})
	}
	var storePhases []storeaccounting.Phase
	for phase := uint32(8); phase <= 12; phase++ {
		configuration.Phases = append(configuration.Phases, dispatchadmission.Phase{ID: phase, Roles: []dispatchadmission.RoleBudget{
			{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal, Attempts: 10}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}}})
		storePhases = append(storePhases, storeaccounting.Phase{ID: phase, Transactions: 10000, Rows: 100000})
	}
	controller, err := dispatchadmission.New(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	sa, err := storeaccounting.New(ctx, storeaccounting.Config{Producers: storeProducers, Phases: storePhases})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, sa, storeaccounting.WireConfig{Producers: wireProducers, AckTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	var retiredControl *dispatchadmission.PhaseControl
	var archiveMeasurementDone <-chan error
	var archiveMeasurementJoined bool
	var archiveHolds atomic.Uint32
	var backupDiagnosticDone <-chan struct{}
	start := func(mode string, bootstrap dispatchadmission.ProductionBootstrap) (*exec.Cmd, *bufio.Scanner, io.WriteCloser, <-chan error, *dispatchadmission.PhaseControl, *bytes.Buffer) {
		t.Helper()
		daParent, daChild, e := dispatchadmission.NewPipe()
		if e != nil {
			t.Fatal(e)
		}
		pcParent, pcChild, e := dispatchadmission.NewPipe()
		if e != nil {
			t.Fatal(e)
		}
		storeChild, storeConfig, e := transport.Open(bootstrap.Producer.ID)
		if e != nil {
			t.Fatal(e)
		}
		bootstrap.Store = &storeConfig
		if archiveWorkspace && bootstrap.Producer.ID >= 10 {
			deadline, _ := ctx.Deadline()
			bootstrap.ArchiveDeadlineUnixNano = deadline.UnixNano()
			bootstrap.ArchiveMeasurements = recovery.BackupCheckpointMaximum()
			if bootstrap.Producer.ID == 11 {
				bootstrap.ArchiveMeasurements, e = recovery.RestoreCheckpointMaximum(10000)
				if e != nil {
					t.Fatal(e)
				}
			}
		}
		helper := "^TestT422BackupRetirementHelper$"
		if workspace {
			helper = "^TestT422WorkspaceNativeHelper$"
		}
		command := exec.CommandContext(ctx, os.Args[0], "-test.run="+helper)
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		command.Cancel = func() error { return t4013.KillPrivateProcessSession(command.Process.Pid) }
		command.Env = []string{t422BackupFixture + "=" + mode, "PHEBS_T422_BACKUP_FIXTURE_ROOT=" + root,
			"PATH=" + filepath.Dir(surreal), "PHEBS_SURREAL=" + surreal, "PHEBS_SURREAL_SHA256=" + identity.SHA256,
			dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionStoreSelector, "GORACE=atexit_sleep_ms=0"}
		if archiveWorkspace {
			command.Env = append(command.Env, "TMPDIR="+root, "TMP="+root, "TEMP="+root)
		}
		command.ExtraFiles = []*os.File{daChild, pcChild, storeChild}
		if workspace || archiveWorkspace {
			command.ExtraFiles = append(command.ExtraFiles, workspaceFile)
		}
		var measurementChild *os.File
		if archiveWorkspace && bootstrap.Producer.ID == 10 {
			parent, child, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := net.FileConn(parent)
			_ = parent.Close()
			if err != nil {
				_ = child.Close()
				t.Fatal(err)
			}
			unix, ok := conn.(*net.UnixConn)
			if !ok {
				_ = conn.Close()
				_ = child.Close()
				t.Fatal("archive socket type")
			}
			measurementChild = child
			command.ExtraFiles = append(command.ExtraFiles, child)
			done := make(chan error, 1)
			archiveMeasurementDone = done
			relayContext, cancelRelay := context.WithCancel(ctx)
			guard := func(operation context.Context, measure func(context.Context) error) error {
				return retiredControl.WithRetiredBackupMeasurement(operation, func(held context.Context) error {
					ordinal := archiveHolds.Add(1) // Actual native HOLD ACK already consumed.
					if ordinal == 2 && failure == "parent_cancel" {
						cancelRelay()
						return held.Err()
					}
					err := measure(held)
					if ordinal == 2 && failure == "lost_release" {
						// Child RELEASE was read, but the parent cannot deliver
						// its ACK. The real PC guard still resumes the engine.
						_ = unix.Close()
					}
					return err
				})
			}
			go func() {
				done <- dispatchadmission.ServeArchiveMeasurement(relayContext, unix, dispatchadmission.ArchiveMeasurementBinding{
					ProducerID: bootstrap.Producer.ID, ProducerBinding: bootstrap.Producer.Binding, InputSHA256: bootstrap.InputSHA256,
				}, bootstrap.ArchiveMeasurements, guard)
			}()
			t.Cleanup(func() {
				cancelRelay()
				_ = unix.Close()
				_ = child.Close()
				if !archiveMeasurementJoined {
					<-done
				}
			})
		}
		command.WaitDelay = 5 * time.Second
		input, e := command.StdinPipe()
		if e != nil {
			t.Fatal(e)
		}
		output, e := command.StdoutPipe()
		if e != nil {
			t.Fatal(e)
		}
		diagnostic := new(bytes.Buffer)
		var diagnosticPipe io.ReadCloser
		var diagnosticDone chan struct{}
		if bootstrap.Producer.ID == 10 && failure == "report_loss" {
			diagnosticPipe, e = command.StderrPipe()
			if e != nil {
				t.Fatal(e)
			}
			diagnosticDone = make(chan struct{})
			backupDiagnosticDone = diagnosticDone
		} else {
			command.Stderr = diagnostic
		}
		if e = command.Start(); e != nil {
			t.Fatal(e)
		}
		if diagnosticPipe != nil {
			go func() {
				defer close(diagnosticDone)
				defer func() { _ = diagnosticPipe.Close() }()
				reader := bufio.NewReader(diagnosticPipe)
				for {
					line, err := reader.ReadString('\n')
					_, _ = diagnostic.WriteString(line)
					if err != nil || line == "WB1:A:CB:0000000000000002\n" {
						return // Lose the actual pipe sink, not a replacement writer.
					}
				}
			}()
		}
		t.Cleanup(func() {
			if command.ProcessState == nil {
				_ = t4013.KillPrivateProcessSession(command.Process.Pid)
				_ = command.Wait()
			}
			if diagnosticPipe != nil {
				_ = diagnosticPipe.Close()
				<-diagnosticDone
			}
			if t.Failed() {
				if e := os.WriteFile(filepath.Join(root, mode+"-diagnostic.log"), diagnostic.Bytes(), 0o600); e != nil {
					t.Error(e)
				}
			}
			if err := t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(5*time.Second)); err != nil {
				_ = t4013.KillPrivateProcessSession(command.Process.Pid)
				if err = t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(6*time.Second)); err != nil {
					closedSessions = false
					t.Error("fixture session survived cleanup", err)
				}
			}
			_ = input.Close()
		})
		_ = daChild.Close()
		_ = pcChild.Close()
		_ = storeChild.Close()
		if measurementChild != nil {
			_ = measurementChild.Close()
		}
		if e = dispatchadmission.SendProductionBootstrap(ctx, daParent, pcParent, bootstrap); e != nil {
			t.Fatal(e)
		}
		served := make(chan error, 1)
		go func() { served <- controller.Serve(ctx, bootstrap.Producer.ID, command.Process.Pid, daParent) }()
		control, e := dispatchadmission.NewPhaseControl(ctx, pcParent, bootstrap.Producer.Binding, bootstrap.Control)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = control.Close() })
		return command, bufio.NewScanner(output), input, served, control, diagnostic
	}
	serverMode := "server"
	if allOwners {
		serverMode = "workspace-all-owners"
	} else if cleanupWorkspace {
		serverMode = "workspace-cleanup"
	}
	server, output, input, served, control, diagnostic := start(serverMode, record)
	retiredControl = control
	failServer := func(stage string, cause error) {
		t.Helper()
		lines := t422NativeFailurePrefix(output, func() {
			_ = t4013.KillPrivateProcessSession(server.Process.Pid)
		})
		// Drain the killed pipe before Wait closes it; stderr's copier must
		// instead join through Wait before its buffer can be read.
		_ = server.Wait()
		if e := os.WriteFile(filepath.Join(root, "server-output.log"), []byte(strings.Join(lines, "\n")), 0o600); e != nil {
			t.Error(e)
		}
		t.Fatal(stage, cause, lines, output.Err(), diagnostic.String())
	}
	if !output.Scan() || !strings.HasPrefix(output.Text(), "endpoint=") {
		failServer("native startup", nil)
	}
	endpoint := strings.TrimPrefix(output.Text(), "endpoint=")
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Host == "" {
		t.Fatal("native endpoint", err)
	}
	if workspace {
		if _, err = fmt.Fprintln(input, control.RequestToken()); err != nil || !output.Scan() || output.Text() != "parked" {
			failServer("native lifecycle park", err)
		}
	}
	if err = control.DrainOwners(ctx); err != nil {
		t.Fatal(err)
	}
	for phase := uint32(9); phase <= 11; phase++ {
		if control.Pause(ctx) != nil || controller.Fence() != nil || transport.Fence() != nil || control.Checkpoint(ctx) != nil || controller.Advance() != nil || transport.Advance() != nil || control.Resume(ctx) != nil {
			t.Fatal("phase transition", phase)
		}
		if workspace {
			if err = control.OpenRequests(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = fmt.Fprintln(input, control.RequestToken()); err != nil || !output.Scan() || output.Text() != "measured_and_resumed" {
				failServer("native lifecycle measurement", err)
			}
			if err = control.FenceRequests(ctx); err != nil {
				t.Fatal(err)
			}
			if err = control.Pause(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = fmt.Fprintln(input, "close"); err != nil {
				t.Fatal(err)
			}
			for output.Scan() {
			}
			if err = output.Err(); err != nil {
				t.Fatal(err)
			}
			if err = server.Wait(); err != nil {
				t.Fatal("native workspace helper", err, diagnostic.String())
			}
			wantSamples := 18
			if allOwners {
				wantSamples = int(assertT422AllOwnersNativeReports(t, diagnostic.String(), record.InputSHA256)) + 2
			} else if cleanupWorkspace {
				wantSamples = t422CleanupExpectedTurns + 2
				assertT422CleanupNativeReports(t, diagnostic.String(), record.InputSHA256)
			}
			assertT422NativeWorkspaceReports(t, diagnostic.String(), record.InputSHA256, wantSamples)
			if err = <-served; err != nil {
				t.Fatal(err)
			}
			if err = transport.Wait(ctx, 5); err != nil {
				t.Fatal(err)
			}
			if err = t4013.WaitPrivateProcessSession(server.Process.Pid, time.Now().Add(5*time.Second)); err != nil {
				t.Fatal(err)
			}
			prefix, e := transport.Snapshot()
			if e != nil || prefix.Opened != 1 || prefix.TerminalEOF != 1 {
				t.Fatal("joined native store prefix", prefix, e)
			}
			if allOwners {
				counts, e := sa.Snapshot()
				turns := uint64(wantSamples - 2)
				if e != nil || len(counts.Phases) != 5 || counts.Phases[1].Phase != 9 ||
					counts.Phases[1].Transactions <= 2*turns || counts.Phases[1].Rows <= 2*turns ||
					counts.Phases[1].Transactions > 67*turns || counts.Phases[1].Rows > 514*turns {
					t.Fatal("actual full-owner cleanup store reserve", counts, e)
				}
				t.Logf("actual all-owner cleanup: turns=%d workspace_samples=%d store_transactions=%d store_rows=%d", turns, wantSamples, counts.Phases[1].Transactions, counts.Phases[1].Rows)
			} else if cleanupWorkspace {
				counts, e := sa.Snapshot()
				if e != nil || len(counts.Phases) != 5 || counts.Phases[1].Phase != 9 ||
					counts.Phases[1].Transactions != 2*t422CleanupExpectedTurns || counts.Phases[1].Rows != 2*t422CleanupExpectedTurns {
					t.Fatal("actual selected cleanup cursor transactions", counts, e)
				}
			}
			return
		}
	}
	if controller.Fence() != nil || transport.Fence() != nil || control.Pause(ctx) != nil || transport.Wait(ctx, 5) != nil || controller.RetireBackupEndpoint() != nil || controller.Advance() != nil || transport.Advance() != nil {
		t.Fatal("endpoint retirement")
	}
	if _, err = input.Write([]byte{'p'}); err != nil {
		t.Fatal(err)
	}
	if !output.Scan() || output.Text() != "store_retired" {
		t.Fatal("server SDK not retired", output.Text())
	}
	backupRecord := record
	backupRecord.Producer, backupRecord.SemanticMode, backupRecord.Phase = backupProducer, "", 12
	backupRecord.Control = dispatchadmission.PhaseControlConfig{Phases: []uint32{12}, InitialPhase: 12, MaximumPhases: 1, MaximumWireBytes: 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	command, backupOutput, _, backupServed, _, backupDiagnostic := start("backup", backupRecord)
	for backupOutput.Scan() {
	} // Drain the actual helper stream before owned Wait.
	if err = backupOutput.Err(); err != nil {
		t.Fatal("backup output", err)
	}
	backupErr := command.Wait()
	if backupDiagnosticDone != nil {
		<-backupDiagnosticDone
	}
	if failure != "" {
		if backupErr == nil {
			t.Fatal("injected archive failure completed successfully")
		}
		relayErr := <-archiveMeasurementDone
		archiveMeasurementJoined = true
		if failure != "report_loss" && relayErr == nil {
			t.Fatal("failed relay reported clean completion")
		}
		if archiveHolds.Load() < 2 {
			t.Fatal("failure did not follow a second actual native hold", archiveHolds.Load())
		}
		assertT422FailedArchiveWorkspacePrefix(t, backupDiagnostic.String(), failure == "report_loss")
		<-backupServed // A failed producer must still join its actual receiver.
		_ = transport.Wait(ctx, 10)
		// Listening alone is insufficient: a stopped engine can retain its
		// socket. An actual response proves it resumed after the failed hold.
		probe, stopProbe := context.WithTimeout(ctx, 2*time.Second)
		request, e := http.NewRequestWithContext(probe, http.MethodGet, "http://"+endpointURL.Host+"/health", nil)
		if e != nil {
			stopProbe()
			t.Fatal(e)
		}
		httpTransport := &http.Transport{DisableKeepAlives: true}
		response, e := (&http.Client{Transport: httpTransport}).Do(request)
		if e == nil {
			_, e = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			e = errors.Join(e, response.Body.Close())
		}
		stopProbe()
		httpTransport.CloseIdleConnections()
		if e != nil || response.StatusCode != http.StatusOK {
			t.Fatal("native engine did not resume after archive failure", e)
		}
		if _, err := input.Write([]byte{'c'}); err != nil {
			t.Fatal(err)
		}
		for output.Scan() {
		}
		if err := output.Err(); err != nil {
			t.Fatal(err)
		}
		serverErr := server.Wait() // Accounting refusal may make this nonzero.
		<-served
		for _, process := range []*exec.Cmd{server, command} {
			if err := t4013.WaitPrivateProcessSession(process.Process.Pid, time.Now().Add(5*time.Second)); err != nil {
				t.Fatal("failed native archive session survived", err)
			}
		}
		prefix, _ := transport.Snapshot()
		if prefix.Opened != 2 {
			t.Fatal("failed backup launched another producer", prefix)
		}
		for _, producer := range prefix.Store.Producers {
			if producer.Producer == 11 && producer.Attached {
				t.Fatal("restore ran after failed backup", prefix)
			}
		}
		if _, err := os.Lstat(filepath.Join(root, "archive", recovery.ManifestName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed early backup left a completed manifest", err)
		}
		t.Logf("actual failed archive: mode=%s holds=%d backup_error=%v relay_error=%v server_error=%v; one positive WB sample retained, restore unstarted, both sessions joined", failure, archiveHolds.Load(), backupErr, relayErr, serverErr)
		return
	}
	if backupErr != nil {
		t.Fatal("actual backup CLI", backupErr, backupDiagnostic.String())
	}
	if archiveWorkspace {
		err = <-archiveMeasurementDone
		archiveMeasurementJoined = true
		if err != nil {
			t.Fatal("archive measurement join", err)
		}
		assertT422ArchiveNativeWorkspaceReports(t, backupDiagnostic.String(), 10, record.InputSHA256, 15, uint64(recovery.BackupCheckpointMaximum()))
	}
	assertT422OfflineBindings(t, backupDiagnostic.String(), 10, "07")
	if strings.Contains(backupDiagnostic.String(), "RL1:") {
		t.Fatal("empty backup unexpectedly rebuilt relationships")
	}
	if err = <-backupServed; err != nil {
		t.Fatal(err)
	}
	if err = transport.Wait(ctx, 10); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "archive", recovery.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest recovery.Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil || manifest.ManifestSHA256 == "" {
		t.Fatal("actual manifest", err)
	}
	if connection, e := net.DialTimeout("tcp", endpointURL.Host, time.Second); e != nil {
		t.Fatal("retired native endpoint lost before server join", e)
	} else {
		_ = connection.Close()
	}
	if _, err = input.Write([]byte{'c'}); err != nil {
		t.Fatal(err)
	}
	for output.Scan() {
	}
	if err = output.Err(); err != nil {
		t.Fatal("server output", err)
	}
	if err = server.Wait(); err != nil {
		t.Fatal("native server close", err, diagnostic.String())
	}
	if err = <-served; err != nil {
		t.Fatal(err)
	}
	if connection, e := net.DialTimeout("tcp", endpointURL.Host, time.Second); e == nil {
		_ = connection.Close()
		t.Fatal("native endpoint survived joined server")
	}
	if err = t4013.WaitPrivateProcessSession(server.Process.Pid, time.Now().Add(5*time.Second)); err != nil {
		t.Fatal("old server session before restore", err)
	}
	if err = t4013.WaitPrivateProcessSession(command.Process.Pid, time.Now().Add(5*time.Second)); err != nil {
		t.Fatal("backup session before restore", err)
	}
	opened := 2
	if restore {
		dataPath := filepath.Join(root, "data")
		dataInfo, e := os.Stat(dataPath)
		if e != nil {
			t.Fatal(e)
		}
		// This tiny fixture preserves the actual directory inode too. The
		// production held-root safety seam has separate mutation/refusal tests.
		data, e := os.OpenRoot(dataPath)
		if e != nil {
			t.Fatal(e)
		}
		entries, e := os.ReadDir(dataPath)
		if e != nil {
			_ = data.Close()
			t.Fatal(e)
		}
		for _, entry := range entries {
			if e = data.RemoveAll(entry.Name()); e != nil {
				_ = data.Close()
				t.Fatal(e)
			}
		}
		if e = data.Close(); e != nil {
			t.Fatal(e)
		}
		if entries, e = os.ReadDir(dataPath); e != nil || len(entries) != 0 {
			t.Fatal("actual empty target", e)
		}
		restoreRecord := backupRecord
		restoreRecord.Producer = restoreProducer
		restored, restoreOutput, _, restoreServed, _, restoreDiagnostic := start("restore", restoreRecord)
		var nativeDigest string
		for restoreOutput.Scan() {
			line := restoreOutput.Text()
			if strings.HasPrefix(line, "restore verified and imported: ") {
				if nativeDigest != "" {
					t.Fatal("duplicate restore result")
				}
				nativeDigest = strings.TrimPrefix(line, "restore verified and imported: ")
			}
		}
		if e = restoreOutput.Err(); e != nil {
			t.Fatal(e)
		}
		if e = restored.Wait(); e != nil {
			t.Fatal("actual restore CLI", e, restoreDiagnostic.String())
		}
		assertT422OfflineBindings(t, restoreDiagnostic.String(), 11, "07")
		if archiveWorkspace {
			maximum, err := recovery.RestoreCheckpointMaximum(10000)
			if err != nil {
				t.Fatal(err)
			}
			assertT422ArchiveNativeWorkspaceReports(t, restoreDiagnostic.String(), 11, record.InputSHA256, 34, uint64(maximum))
		}
		// Real Restore installs archived members and recovers their authority;
		// it does not invoke relationship Build/projectors. The later restored
		// server is a separate lifetime, not invented positive work here.
		if strings.Contains(restoreDiagnostic.String(), "RL1:") {
			t.Fatal("empty native restore unexpectedly rebuilt relationships")
		}
		if e = <-restoreServed; e != nil {
			t.Fatal(e)
		}
		if e = transport.Wait(ctx, 11); e != nil {
			t.Fatal(e)
		}
		if e = t4013.WaitPrivateProcessSession(restored.Process.Pid, time.Now().Add(5*time.Second)); e != nil {
			t.Fatal(e)
		}
		if nativeDigest != manifest.ManifestSHA256 {
			t.Fatal("actual restored manifest changed", nativeDigest)
		}
		if info, e := os.Stat(dataPath); e != nil || !os.SameFile(info, dataInfo) {
			t.Fatal("restore replaced data root", e)
		}
		if raw, e := os.ReadFile(configPath); e != nil || !bytes.Equal(raw, configRaw) {
			t.Fatal("restore changed exact config", e)
		}
		opened = 3
	}
	prefix, err := transport.Snapshot()
	if err != nil || prefix.Opened != opened || prefix.TerminalEOF != opened {
		t.Fatal(prefix, err)
	}
	for _, p := range prefix.Store.Producers {
		if p.Producer == 5 && !p.Closed {
			t.Fatal("server store advanced after retirement", p)
		}
	}
	dispatchPrefix, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range dispatchPrefix.Producers {
		if p.Producer == 10 && (!p.Closed || p.Ordinal != 2) {
			t.Fatal("backup must own actual version probe and export", p)
		}
		if restore && p.Producer == 11 && (!p.Closed || p.Ordinal != 5) {
			t.Fatal("restore must own three version probes and two engine lifetimes", p)
		}
	}
}

// The report-loss case retains only the bytes read before closing the actual
// sink. It does not assert that no unobserved child sample completed later.
func assertT422FailedArchiveWorkspacePrefix(t *testing.T, raw string, reportLost bool) {
	t.Helper()
	bindings, begins, successes, failures := 0, 0, 0, 0
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "WBB1:") {
			if line != fmt.Sprintf("WBB1:10:sha256:%x", [32]byte{7}) {
				t.Fatal("failed archive binding changed", line)
			}
			bindings++
		}
		if !strings.HasPrefix(line, "WB1:") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 || fields[1] != "A" || len(fields[2]) != 2 || fields[2][0] != 'C' {
			t.Fatal("malformed failed archive report", line)
		}
		sequence, err := strconv.ParseUint(fields[3], 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		switch fields[2][1] {
		case 'B':
			begins++
			if sequence != uint64(begins) || begins > 2 || begins == 2 && successes != 1 {
				t.Fatal("failed archive sequence changed", line)
			}
		case 'S':
			successes++
			if len(fields) != 6 || sequence != 1 || successes != 1 || begins != 1 {
				t.Fatal("failed sample became complete", line)
			}
			logical, logicalErr := strconv.ParseUint(fields[4], 16, 64)
			allocated, allocatedErr := strconv.ParseUint(fields[5], 16, 64)
			if logicalErr != nil || allocatedErr != nil || logical == 0 || allocated == 0 {
				t.Fatal("earlier positive native sample lost", line)
			}
		case 'F':
			failures++
			if sequence != 2 || failures != 1 || begins != 2 || successes != 1 {
				t.Fatal("failed archive completion changed", line)
			}
		default:
			t.Fatal("unexpected archive report", line)
		}
	}
	if bindings != 1 || begins != 2 || successes != 1 || (!reportLost && failures != 1) || reportLost && failures != 0 {
		t.Fatal("incomplete native WB prefix", bindings, begins, successes, failures)
	}
}

// Go emits the failure summary before its buffered test details, after test
// cleanup has returned. Preserve that bounded tail before killing its session;
// unexpected live protocol output still requires stopping before pipe drainage.
func t422NativeFailurePrefix(output *bufio.Scanner, stop func()) []string {
	terminal := strings.HasPrefix(output.Text(), "--- FAIL: TestT422")
	if !terminal {
		stop()
	}
	lines := []string{output.Text()}
	for len(lines) < 20 && output.Scan() {
		lines = append(lines, output.Text())
	}
	if terminal {
		stop()
	}
	return lines
}

func TestT422NativeFailurePrefix(t *testing.T) {
	for _, test := range []struct {
		name, first string
		details     int
		want        int
	}{
		{"terminal_details", "--- FAIL: TestT422WorkspaceNativeHelper (1.00s)", 1, 2},
		{"live_protocol", "unexpected response", 1, 1},
		{"bounded_details", "--- FAIL: TestT422WorkspaceNativeHelper (1.00s)", 30, 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer func() { _ = reader.Close() }()
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				defer func() { _ = writer.Close() }()
				if _, err := fmt.Fprintln(writer, test.first); err != nil {
					return
				}
				for range test.details {
					if _, err := fmt.Fprintln(writer, "failure detail"); err != nil {
						return
					}
				}
			}()
			output := bufio.NewScanner(reader)
			if !output.Scan() {
				t.Fatal(output.Err())
			}
			stops := 0
			lines := t422NativeFailurePrefix(output, func() { stops++; _ = reader.Close() })
			<-joined
			if stops != 1 || len(lines) != test.want || lines[0] != test.first {
				t.Fatal("failure prefix", stops, lines)
			}
			if test.want > 1 && lines[1] != "failure detail" {
				t.Fatal("missing failure detail", lines)
			}
		})
	}
}

func TestT422BackupRetirementHelper(t *testing.T) {
	mode := os.Getenv(t422BackupFixture)
	if mode == "" {
		return
	}
	root := os.Getenv("PHEBS_T422_BACKUP_FIXTURE_ROOT")
	if mode == "backup" || mode == "restore" {
		flag := "-output"
		if mode == "restore" {
			flag = "-backup"
		}
		code, err := runPhebs([]string{mode, "-config", filepath.Join(root, "phebs.yaml"), flag, filepath.Join(root, "archive")})
		if code != 0 || err != nil {
			t.Fatal(code, err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lifetime.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if _, err = lifetime.TakeStoreOwner(); err != nil {
		t.Fatal(err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "phebs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenLocalWithConfig(ctx, filepath.Join(root, "data"), recovery.ConfigDigest(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = dispatchadmission.BindRetiredBackupMeasurement(st.WithRetiredLocalEngine); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	runtime, err := store.ReadLocalRuntime(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("endpoint=" + runtime.Endpoint)
	var command [1]byte
	if n, e := os.Stdin.Read(command[:]); e != nil || n != 1 || command[0] != 'p' {
		t.Fatal("retirement request", e)
	}
	if owner, e := dispatchadmission.ProcessStoreOwner(); e == nil || owner != nil {
		t.Fatal("retired SDK owner remained available")
	}
	fmt.Println("store_retired")
	if n, e := os.Stdin.Read(command[:]); e != nil || n != 1 || command[0] != 'c' {
		t.Fatal("close request", e)
	}
}
