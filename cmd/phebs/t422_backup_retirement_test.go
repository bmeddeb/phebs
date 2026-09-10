//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	testT422ArchiveRetiredNativeEndpoint(t, false, false)
}

func TestT422RestoreRetiredNativeEndpoint(t *testing.T) {
	testT422ArchiveRetiredNativeEndpoint(t, true, false)
}

func testT422ArchiveRetiredNativeEndpoint(t *testing.T, restore, workspace bool) {
	surreal, err := exec.LookPath("surreal")
	if err != nil {
		t.Skip("surreal binary not installed")
	}
	surreal, err = filepath.EvalSymlinks(surreal)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
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
	if workspace {
		semantic, _ := t422LifecycleBootstrapRecord(t)
		record.InputSHA256 = semantic.InputSHA256
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
		command.ExtraFiles = []*os.File{daChild, pcChild, storeChild}
		if workspace {
			command.ExtraFiles = append(command.ExtraFiles, workspaceFile)
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
		command.Stderr = diagnostic
		if e = command.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if command.ProcessState == nil {
				_ = t4013.KillPrivateProcessSession(command.Process.Pid)
				_ = command.Wait()
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
	server, output, input, served, control, diagnostic := start("server", record)
	failServer := func(stage string, cause error) {
		t.Helper()
		_ = t4013.KillPrivateProcessSession(server.Process.Pid)
		lines := []string{output.Text()}
		for len(lines) < 20 && output.Scan() {
			lines = append(lines, output.Text())
		}
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
	if err = command.Wait(); err != nil {
		t.Fatal("actual backup CLI", err, backupDiagnostic.String())
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
