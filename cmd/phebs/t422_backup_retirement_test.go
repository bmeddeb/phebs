//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/custodybytes"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/recovery"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/bmeddeb/phebs/spike/t4013"
)

const t422BackupFixture = "PHEBS_T422_BACKUP_RETIREMENT_TEST"

// Joined real child output; a single positive phase-two pair is a prefix,
// never completeness for the two-phase finish sequence.
func assertT422EarlyNativeWorkspaceReports(t *testing.T, raw string, input [32]byte) {
	t.Helper()
	var records []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "WB") {
			records = append(records, line)
		}
	}
	if len(records) != 3 || records[0] != fmt.Sprintf("WBB1:2:sha256:%x", input) ||
		records[1] != "WB1:2:2B:0000000000000001" {
		t.Fatal("actual early WB framing", records)
	}
	var sequence, logical, allocated uint64
	n, err := fmt.Sscanf(records[2], "WB1:2:2S:%016x:%016x:%016x", &sequence, &logical, &allocated)
	if n != 3 || err != nil || sequence != 1 || logical < 1<<20 || allocated == 0 ||
		records[2] != fmt.Sprintf("WB1:2:2S:%016x:%016x:%016x", sequence, logical, allocated) ||
		len(strings.Join(records, "\n"))+1 != 79+26+60 {
		t.Fatal("actual early WB positive prefix", records, err)
	}
}

// This test-only tap consumes the one exec stderr copier, never a live read of
// its bytes.Buffer. It accepts exactly the first five source-bound WB records.
// Other diagnostics use fixed line storage, not an unbounded line allocation.
// Joined output remains independently checked after Wait, including warm finish.
type t422WarmNativeOutput struct {
	sink   io.Writer
	input  [32]byte
	line   [79]byte
	length int
	record int
	ready  chan t422WorkspaceSampleResponse
	sample t422WorkspaceSampleResponse
}

func (output *t422WarmNativeOutput) Write(raw []byte) (int, error) {
	n, err := output.sink.Write(raw)
	if err != nil {
		return n, err
	}
	for _, b := range raw[:n] {
		if output.record == 5 {
			break
		}
		if b != '\n' {
			if output.length < len(output.line) {
				output.line[output.length] = b
			}
			if output.length <= len(output.line) {
				output.length++
			}
			continue
		}
		length := output.length
		output.length = 0
		if length < 2 || output.line[0] != 'W' || output.line[1] != 'B' {
			continue
		}
		valid := length < len(output.line)
		line := string(output.line[:min(length, len(output.line))])
		switch output.record {
		case 0:
			valid = valid && line == fmt.Sprintf("WBB1:2:sha256:%x", output.input)
		case 1:
			valid = valid && line == "WB1:2:2B:0000000000000001"
		case 3:
			valid = valid && line == "WB1:2:3B:0000000000000002"
		case 2, 4:
			phase, sequence := uint64(2), uint64(1)
			if output.record == 4 {
				phase, sequence = 3, 2
			}
			sample, ok := t422WarmNativeSample(line, phase, sequence)
			valid = valid && ok
			if valid && output.record == 4 {
				output.sample = sample
			}
		}
		if !valid {
			output.ready <- t422WorkspaceSampleResponse{}
			output.record = 5
			return n, errors.New("native warm WB prefix refused")
		}
		output.record++
		if output.record == 5 {
			output.ready <- output.sample
		}
	}
	return n, nil
}

func t422WarmNativeSample(line string, phase, sequence uint64) (t422WorkspaceSampleResponse, bool) {
	var gotPhase, gotSequence, logical, allocated uint64
	n, err := fmt.Sscanf(line, "WB1:2:%dS:%016x:%016x:%016x", &gotPhase, &gotSequence, &logical, &allocated)
	return t422WorkspaceSampleResponse{LogicalBytes: logical, AllocatedBytes: allocated},
		n == 4 && err == nil && gotPhase == phase && gotSequence == sequence && logical >= 1<<20 && allocated > 0 &&
			line == fmt.Sprintf("WB1:2:%dS:%016x:%016x:%016x", phase, sequence, logical, allocated)
}

func assertT422WarmNativeWorkspaceReports(t *testing.T, raw string, input [32]byte, live, finish t422WorkspaceSampleResponse) {
	t.Helper()
	var records []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "WB") {
			records = append(records, line)
		}
	}
	if len(records) != 7 || records[0] != fmt.Sprintf("WBB1:2:sha256:%x", input) ||
		len(strings.Join(records, "\n"))+1 != 79+3*(26+60) {
		t.Fatal("joined native warm WB framing", records)
	}
	for i, phase := range []uint64{2, 3, 3} {
		sequence := uint64(i + 1)
		if records[1+2*i] != fmt.Sprintf("WB1:2:%dB:%016x", phase, sequence) {
			t.Fatal("joined native warm begin", records)
		}
		sample, ok := t422WarmNativeSample(records[2+2*i], phase, sequence)
		if !ok || i == 1 && sample != live || i == 2 && sample != finish {
			t.Fatal("joined native warm exact payload", records)
		}
	}
}

// Supplied framing only; the native selector below supplies real child bytes.
func TestT422WarmNativeOutput(t *testing.T) {
	input := [32]byte{9}
	prefix := fmt.Sprintf("WBB1:2:sha256:%x\nWB1:2:2B:0000000000000001\nWB1:2:2S:0000000000000001:0000000000100000:0000000000000200\nWB1:2:3B:0000000000000002\nWB1:2:3S:0000000000000002:0000000000100001:0000000000000400\n", input)
	// A begin without its actual completed payload must never release the parent.
	incomplete := &t422WarmNativeOutput{sink: io.Discard, input: input, ready: make(chan t422WorkspaceSampleResponse, 1)}
	if _, err := incomplete.Write([]byte(prefix[:strings.LastIndex(prefix, "WB1:2:3S:")])); err != nil {
		t.Fatal(err)
	}
	select {
	case <-incomplete.ready:
		t.Fatal("incomplete warm observation released parent")
	default:
	}
	for _, tc := range []struct {
		name, raw string
		refused   bool
	}{
		{"fragmented", prefix, false},
		{"diagnostics", strings.Repeat("x", 4096) + "\n" + prefix, false},
		{"wrong_binding", strings.Replace(prefix, "0900", "0800", 1), true},
		{"wrong_sequence", strings.Replace(prefix, "3S:0000000000000002", "3S:0000000000000001", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sink bytes.Buffer
			output := &t422WarmNativeOutput{sink: &sink, input: input, ready: make(chan t422WorkspaceSampleResponse, 1)}
			var err error
			for _, b := range []byte(tc.raw) {
				_, err = output.Write([]byte{b})
				if err != nil {
					break
				}
			}
			if (err != nil) != tc.refused {
				t.Fatal(err)
			}
			select {
			case sample := <-output.ready:
				if tc.refused != (sample == (t422WorkspaceSampleResponse{})) {
					t.Fatal(sample)
				}
			default:
				t.Fatal("missing actual framing result")
			}
			if !tc.refused && sink.String() != tc.raw {
				t.Fatal("diagnostic bytes changed")
			}
		})
	}
}

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

// Actual restored store, FD6 walks, parked runner, cursor writes, authenticated
// HTTP and WB reports. The sixteen owner callbacks and Gate capacity are
// modeled; this is not real-owner collection, native capacity, archive R,
// query replay or a whole-phase proof.
func TestT422WorkspaceEpochFiveNativeComposition(t *testing.T) {
	testT422ArchiveRetiredNativeEndpointFailure(t, true, false, true, "read-workspace")
}

func TestT422ArchiveWorkspaceNativeFailures(t *testing.T) {
	for _, mode := range []string{"lost_release", "parent_cancel", "report_loss"} {
		t.Run(mode, func(t *testing.T) {
			testT422ArchiveRetiredNativeEndpointFailure(t, true, false, true, mode)
		})
	}
}

// The positive R input is a supplied strict manifest, not a claim that this
// empty-data backup produced five positive artifact publications.
func TestT422ArchiveReadNativeSuppliedManifest(t *testing.T) {
	for _, mode := range []string{"read-supplied", "read-report-loss"} {
		t.Run(mode, func(t *testing.T) {
			testT422ArchiveRetiredNativeEndpointFailure(t, true, false, true, mode)
		})
	}
}

func testT422ArchiveRetiredNativeEndpoint(t *testing.T, restore, workspace, archiveWorkspace bool, cleanup ...bool) {
	testT422ArchiveRetiredNativeEndpointFailure(t, restore, workspace, archiveWorkspace, "", cleanup...)
}

func testT422ArchiveRetiredNativeEndpointFailure(t *testing.T, restore, workspace, archiveWorkspace bool, failure string, cleanup ...bool) {
	warmWorkspace := failure == "workspace-warm"
	earlyWorkspace := failure == "workspace-early" || warmWorkspace
	if earlyWorkspace {
		if !workspace || restore || archiveWorkspace || len(cleanup) != 0 {
			t.Fatal("invalid early workspace fixture")
		}
		failure = ""
	}
	if (workspace || archiveWorkspace) && runtime.GOOS != "darwin" {
		t.Skip("native workspace descriptor admission requires Darwin")
	}
	readMode := "empty"
	if failure == "read-supplied" || failure == "read-report-loss" || failure == "read-workspace" {
		readMode, failure = failure, ""
	}
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
	if earlyWorkspace {
		record.Producer.ID, record.Producer.Binding, record.Phase = 2, [32]byte{2}, 2
		record.Limits.Phases = 3
		record.Control.BackupEndpointCarry = false
		record.Control.Phases, record.Control.InitialPhase, record.Control.MaximumPhases = []uint32{2, 3, 4}, 2, 3
	}
	if warmWorkspace {
		record.Control.WarmStartWorkspace = true
		record.Control.MaximumWireBytes = 21 * 2 * dispatchadmission.FrameBytes
	}
	var warmOutput *t422WarmNativeOutput
	if warmWorkspace {
		warmOutput = &t422WarmNativeOutput{ready: make(chan t422WorkspaceSampleResponse, 1)}
	}
	var workspaceFile *os.File
	var archiveObserver *custodybytes.Observer
	var archiveParentSamples int
	archivePath := filepath.Join(root, "archive")
	if workspace || archiveWorkspace {
		if workspace {
			semantic, _ := t422LifecycleBootstrapRecord(t)
			record.InputSHA256 = semantic.InputSHA256
			if earlyWorkspace {
				raw, _ := t422SemanticTestRequest(t)
				record.InputSHA256 = sha256.Sum256(raw)
			}
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
		if archiveWorkspace {
			info, err := workspaceFile.Stat()
			if err != nil {
				t.Fatal(err)
			}
			archiveObserver = custodybytes.NewBorrowed(workspaceFile, root, info, binding.FSID)
			backupRoot := filepath.Join(root, "t422-backup-1234")
			if err := os.Mkdir(backupRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			archivePath = filepath.Join(backupRoot, "archive")
		}
	}
	if archiveWorkspace {
		record.Control.BackupMeasurementMaximum = 1 + recovery.BackupCheckpointMaximum()
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
	storeProducers := []storeaccounting.Producer{{ID: record.Producer.ID, Calls: 40, Transactions: 2}, {ID: 10, Calls: 1, Transactions: 1}}
	wireProducers := []storeaccounting.WireProducer{{ID: record.Producer.ID, Binding: record.Producer.Binding, Phases: 1920}, {ID: 10, Binding: backupProducer.Binding, Phases: 2048}}
	if earlyWorkspace {
		// Only the server participates; no archive producer or phase is fabricated.
		configuration.Producers = configuration.Producers[:1]
		storeProducers, wireProducers = storeProducers[:1], wireProducers[:1]
		wireProducers[0].Phases = 14 // Phases two, three and four.
	}
	if restore {
		record.Limits.Producers, record.Limits.Sites = 3, 48
		configuration.Limits = record.Limits
		configuration.Producers = append(configuration.Producers, restoreProducer)
		storeProducers = append(storeProducers, storeaccounting.Producer{ID: 11, Calls: 1, Transactions: 1})
		wireProducers = append(wireProducers, storeaccounting.WireProducer{ID: 11, Binding: restoreProducer.Binding, Phases: 2048})
	}
	readProducer := record.Producer
	readProducer.ID, readProducer.Binding = 6, [32]byte{6}
	if archiveWorkspace && restore {
		record.Limits.Producers, record.Limits.Sites = 4, 64
		record.Limits.Phases = 7 // Existing epoch-five bootstrap names phases 12–14.
		configuration.Limits = record.Limits
		configuration.Producers = append(configuration.Producers, readProducer)
		storeProducers = append(storeProducers, storeaccounting.Producer{ID: 6, Calls: storeaccounting.MaximumCalls, Transactions: storeaccounting.MaximumTransactions})
		wireProducers = append(wireProducers, storeaccounting.WireProducer{ID: 6, Binding: readProducer.Binding, Phases: 14336})
	}
	var storePhases []storeaccounting.Phase
	lastPhase := uint32(12)
	if archiveWorkspace && restore {
		lastPhase = 14
	}
	firstPhase := uint32(8)
	if earlyWorkspace {
		firstPhase, lastPhase = 2, 4
	}
	for phase := firstPhase; phase <= lastPhase; phase++ {
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
			"PHEBS_T422_BACKUP_FIXTURE_ARCHIVE=" + archivePath,
			"PATH=" + filepath.Dir(surreal), "PHEBS_SURREAL=" + surreal, "PHEBS_SURREAL_SHA256=" + identity.SHA256,
			dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionStoreSelector, "GORACE=atexit_sleep_ms=0"}
		deadline, _ := ctx.Deadline()
		command.Env = append(command.Env, "PHEBS_T422_FIXTURE_DEADLINE="+strconv.FormatInt(deadline.UnixNano(), 10))
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
			if warmWorkspace {
				warmOutput.sink, warmOutput.input = diagnostic, bootstrap.InputSHA256
				command.Stderr = warmOutput
			}
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
	if warmWorkspace {
		serverMode = "workspace-warm"
	} else if earlyWorkspace {
		serverMode = "workspace-early"
	} else if allOwners {
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
	var warmFinish t422WorkspaceSampleResponse
	if earlyWorkspace {
		if err = control.DrainOwners(ctx); err != nil {
			t.Fatal(err)
		}
		if control.OpenRequests(ctx) != nil {
			t.Fatal("actual early request window")
		}
		if _, err = fmt.Fprintln(input, control.RequestToken()); err != nil || !output.Scan() || output.Text() != "early_measured_and_resumed" {
			failServer("native early workspace measurement", err)
		}
		if control.FenceRequests(ctx) != nil {
			t.Fatal("actual early request tail")
		}
		if warmWorkspace {
			// Anchor before Resume, clipped by the unchanged original fixture lifetime.
			warmCtx, cancelWarm := context.WithTimeout(ctx, 20*time.Minute)
			defer cancelWarm()
			if control.Pause(warmCtx) != nil || controller.Fence() != nil || transport.Fence() != nil ||
				control.Checkpoint(warmCtx) != nil || controller.Advance() != nil || transport.Advance() != nil ||
				control.Resume(warmCtx) != nil {
				t.Fatal("actual warm Resume handoff")
			}
			select {
			case sample := <-warmOutput.ready:
				if sample.LogicalBytes < 1<<20 || sample.AllocatedBytes == 0 {
					t.Fatal("actual warm-start report refused")
				}
			case <-warmCtx.Done():
				failServer("actual warm-start report deadline", warmCtx.Err())
			}
			// Only the actual bound report permits reopening. The callback receiver
			// also joins before it can ACK this next pair; no ready flag is supplied.
			if control.ReopenOwners(warmCtx) != nil || control.DrainOwners(warmCtx) != nil || control.OpenRequests(warmCtx) != nil {
				t.Fatal("actual warm owner/request handoff")
			}
			if _, err = fmt.Fprintln(input, control.RequestToken()); err != nil || !output.Scan() || !strings.HasPrefix(output.Text(), "warm_measured_and_resumed:") {
				failServer("native warm finish measurement", err)
			}
			if n, scanErr := fmt.Sscanf(output.Text(), "warm_measured_and_resumed:%016x:%016x", &warmFinish.LogicalBytes, &warmFinish.AllocatedBytes); n != 2 || scanErr != nil || output.Text() != fmt.Sprintf("warm_measured_and_resumed:%016x:%016x", warmFinish.LogicalBytes, warmFinish.AllocatedBytes) {
				t.Fatal("actual warm finish HTTP values")
			}
			if control.FenceRequests(warmCtx) != nil || control.Pause(warmCtx) != nil {
				t.Fatal("actual warm terminal fence/pause")
			}
		} else if control.Pause(ctx) != nil {
			t.Fatal("actual early terminal pause")
		}
		if _, err = fmt.Fprintln(input, "close"); err != nil {
			t.Fatal(err)
		}
		for output.Scan() {
		}
		if output.Err() != nil {
			t.Fatal(output.Err())
		}
		if err = server.Wait(); err != nil {
			t.Fatal("native early workspace helper", err, diagnostic.String())
		}
		if err = <-served; err != nil {
			t.Fatal(err)
		}
		if transport.Wait(ctx, 2) != nil || t4013.WaitPrivateProcessSession(server.Process.Pid, time.Now().Add(5*time.Second)) != nil {
			t.Fatal("native early SDK/session join")
		}
		prefix, err := transport.Snapshot()
		if err != nil || prefix.Opened != 1 || prefix.TerminalEOF != 1 {
			t.Fatal("native early store prefix", prefix, err)
		}
		if warmWorkspace {
			assertT422WarmNativeWorkspaceReports(t, diagnostic.String(), record.InputSHA256, warmOutput.sample, warmFinish)
		} else {
			assertT422EarlyNativeWorkspaceReports(t, diagnostic.String(), record.InputSHA256)
		}
		return
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
	if archiveWorkspace {
		if sample, err := archiveObserver.SampleGuarded(ctx, 12, control.WithRetiredBackupMeasurement, nil); err != nil || sample.LogicalBytes == 0 || sample.AllocatedBytes == 0 {
			t.Fatal("actual phase-start retired-engine workspace sample", sample, err)
		}
		archiveParentSamples++
	}
	backupRecord := record
	backupRecord.Producer, backupRecord.SemanticMode, backupRecord.Phase = backupProducer, "", 12
	backupRecord.Control = dispatchadmission.PhaseControlConfig{Phases: []uint32{12}, InitialPhase: 12, MaximumPhases: 1, MaximumWireBytes: 2 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
	command, backupOutput, _, backupServed, _, backupDiagnostic := start("backup", backupRecord)
	var backupDigest string
	for backupOutput.Scan() {
		prefix := "backup published: " + archivePath + " ("
		if strings.HasPrefix(backupOutput.Text(), prefix) && strings.HasSuffix(backupOutput.Text(), ")") {
			if backupDigest != "" {
				t.Fatal("duplicate native backup result")
			}
			backupDigest = strings.TrimSuffix(strings.TrimPrefix(backupOutput.Text(), prefix), ")")
		}
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
		if _, err := os.Lstat(filepath.Join(archivePath, recovery.ManifestName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed early backup left a completed manifest", err)
		}
		if archiveParentSamples != 1 || archiveObserver.Snapshot().Unavailable || !archiveObserver.Snapshot().Phases[11].Completed {
			t.Fatal("failed backup lost its actual parent phase-start sample")
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
	raw, err := os.ReadFile(filepath.Join(archivePath, recovery.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest recovery.Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil || manifest.ManifestSHA256 == "" {
		t.Fatal("actual manifest", err)
	}
	if backupDigest != manifest.ManifestSHA256 {
		t.Fatal("actual backup output differs from manifest", backupDigest)
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
	if archiveWorkspace {
		if sample, err := archiveObserver.Sample(ctx, 12); err != nil || sample.LogicalBytes == 0 || sample.AllocatedBytes == 0 {
			t.Fatal("actual joined server/backup workspace sample", sample, err)
		}
		archiveParentSamples++
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
		if archiveWorkspace {
			if sample, err := archiveObserver.Sample(ctx, 12); err != nil || sample.LogicalBytes == 0 || sample.AllocatedBytes == 0 {
				t.Fatal("actual joined restore workspace sample", sample, err)
			}
			archiveParentSamples++
			if archiveParentSamples != 3 || archiveObserver.Snapshot().Unavailable || !archiveObserver.Snapshot().Phases[11].Completed {
				t.Fatal("actual parent archive boundary coverage")
			}
			t.Log("actual parent same-observer archive walks: retired phase start, joined server/backup, joined restore; 3 completed, not whole-phase high-water")
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
		if archiveWorkspace {
			if readMode != "empty" && readMode != "read-workspace" {
				manifest = t422SuppliedArchiveReadManifest(t, manifest)
				raw, e := json.Marshal(manifest)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(archivePath, recovery.ManifestName), append(raw, '\n'), 0o600); e != nil {
					t.Fatal(e)
				}
				// Explicitly supplied parser/HTTP evidence, not actual command outputs.
				backupDigest, nativeDigest = manifest.ManifestSHA256, manifest.ManifestSHA256
			}
			leaf := filepath.Dir(archivePath)
			held, e := os.Open(leaf)
			if e != nil {
				t.Fatal(e)
			}
			binding, e := dispatchadmission.DescribeProductionWorkspace(held, leaf)
			if closeErr := held.Close(); e != nil || closeErr != nil {
				t.Fatal(e, closeErr)
			}
			launchRaw, _ := t422SemanticTestRequest(t)
			var request t422SemanticLaunchRequest
			if e = json.Unmarshal(launchRaw, &request); e != nil {
				t.Fatal(e)
			}
			request.ServerEpoch = 5
			request.Archive = &t422ArchiveInput{BackupRoot: filepath.Base(leaf), Device: binding.Device, Inode: binding.Inode, FSID: binding.FSID,
				BackupCommandSHA256: backupDigest, RestoreCommandSHA256: nativeDigest}
			launchRaw, e = json.Marshal(request)
			if e != nil {
				t.Fatal(e)
			}
			launchRaw = append(launchRaw, '\n')
			if e = os.WriteFile(filepath.Join(root, "archive-input.json"), launchRaw, 0o600); e != nil {
				t.Fatal(e)
			}
			readRecord := record
			readRecord.Producer, readRecord.Phase, readRecord.InputSHA256 = readProducer, 12, sha256.Sum256(launchRaw)
			readRecord.Control = dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{12, 13, 14}, InitialPhase: 12, MaximumPhases: 3, MaximumWireBytes: 64 * dispatchadmission.FrameBytes, Timeout: 30 * time.Second}
			if readMode == "read-workspace" {
				readRecord.Control.MaximumWireBytes = 15 * 2 * dispatchadmission.FrameBytes
			}
			reader, readerOutput, readerInput, readerServed, readerControl, readerDiagnostic := start("archive-read-"+readMode, readRecord)
			if !readerOutput.Scan() || !strings.HasPrefix(readerOutput.Text(), "http://127.0.0.1:") {
				t.Fatal("native R listener", readerOutput.Text())
			}
			address := readerOutput.Text()
			if readMode == "read-workspace" {
				t422DriveNativeEpochFiveWorkspace(t, ctx, address, readerControl, controller, transport)
				if _, e = readerInput.Write([]byte{'v'}); e != nil || !readerOutput.Scan() || readerOutput.Text() != "workspace_measured_and_resumed" {
					t.Fatal("native epoch-five workspace verification", e, readerOutput.Text())
				}
			} else {
				if e = readerControl.DrainOwners(ctx); e != nil {
					t.Fatal(e)
				}
				if e = readerControl.OpenRequests(ctx); e != nil {
					t.Fatal(e)
				}
				client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}
				defer client.CloseIdleConnections()
				call := func(ordinal int) (int, []byte, t421ExactReadReport) {
					t.Helper()
					r, e := http.NewRequestWithContext(ctx, http.MethodGet, address+t422ArchiveTransitionPath, nil)
					if e != nil {
						t.Fatal(e)
					}
					r.Header.Set(dispatchadmission.ProductionRequestHeader, readerControl.RequestToken())
					r.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
					r.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
					r.Header.Set(t421ExactReadOrdinalHeader, strconv.Itoa(ordinal))
					response, e := client.Do(r)
					if e != nil {
						t.Fatal(e)
					}
					body, e := io.ReadAll(io.LimitReader(response.Body, t422ArchiveTransitionBytes+1))
					if closeErr := response.Body.Close(); e != nil || closeErr != nil {
						t.Fatal(e, closeErr)
					}
					if len(body) > t422ArchiveTransitionBytes {
						t.Fatal("unbounded native R")
					}
					var report t421ExactReadReport
					// The existing spine sets its trailer before sending the
					// separate report sink. Sink loss must reject the native
					// after-report commit, not invent retroactive HTTP loss.
					encoded, e := base64.RawURLEncoding.DecodeString(response.Trailer.Get(t421ExactReadTrailer))
					if e != nil {
						t.Fatal(e)
					}
					if e = json.Unmarshal(encoded, &report); e != nil {
						t.Fatal(e)
					}
					if report.RequestOrdinal != uint64(ordinal) || report.StoreReadAttempts != 0 || report.MemberVisits != 0 || report.StoreWriteAttempts != 0 {
						t.Fatal("native R accounting", report)
					}
					return response.StatusCode, body, report
				}
				status, body, report := call(1)
				if readMode == "empty" {
					if status != 409 || report.Status != "archive_transition_refused" || report.ControlFileReads != 1 {
						t.Fatal("actual empty archive R refusal", status, report)
					}
				} else {
					var projection recovery.ArchiveTransitionManifest
					if status != 200 || json.Unmarshal(body, &projection) != nil || !reflect.DeepEqual(projection, t422SuppliedArchiveProjection(manifest)) {
						t.Fatal("supplied native R projection", status, string(body))
					}
					if report.Status != "complete" || report.ControlFileReads != 1 {
						t.Fatal("positive native R accounting", report)
					}
				}
				if _, e = readerInput.Write([]byte{'i'}); e != nil {
					t.Fatal(e)
				}
				if !readerOutput.Scan() {
					t.Fatal("native R post-report state", readerOutput.Err())
				}
				want := "archive_reported=false failed=true"
				if readMode == "read-supplied" {
					want = "archive_reported=true failed=false"
				}
				if readerOutput.Text() != want {
					t.Fatal("native R after-report state", readerOutput.Text(), want)
				}
				if readMode == "read-supplied" {
					status, _, report = call(2)
					if status != 409 || report.ControlFileReads != 0 || report.Status != "archive_transition_refused" {
						t.Fatal("one-shot native R reread", status, report)
					}
				}
			}
			if e = readerControl.FenceRequests(ctx); e != nil {
				t.Fatal(e)
			}
			if e = readerControl.Pause(ctx); e != nil {
				t.Fatal(e)
			}
			if _, e = readerInput.Write([]byte{'c'}); e != nil {
				t.Fatal(e)
			}
			for readerOutput.Scan() {
			}
			if e = reader.Wait(); e != nil {
				t.Fatal("native R helper join", e, readerDiagnostic.String())
			}
			if readMode == "read-workspace" {
				assertT422EpochFiveNativeWorkspaceReports(t, readerDiagnostic.String(), readRecord.InputSHA256)
			}
			if e = <-readerServed; e != nil {
				t.Fatal(e)
			}
			if e = transport.Wait(ctx, 6); e != nil {
				t.Fatal(e)
			}
			if e = t4013.WaitPrivateProcessSession(reader.Process.Pid, time.Now().Add(5*time.Second)); e != nil {
				t.Fatal(e)
			}
			opened = 4
		}
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
	if strings.HasPrefix(mode, "archive-read-") {
		t422NativeArchiveReadHelper(t, root, mode)
		return
	}
	if mode == "backup" || mode == "restore" {
		flag := "-output"
		if mode == "restore" {
			flag = "-backup"
		}
		code, err := runPhebs([]string{mode, "-config", filepath.Join(root, "phebs.yaml"), flag, os.Getenv("PHEBS_T422_BACKUP_FIXTURE_ARCHIVE")})
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

func t422SuppliedArchiveReadManifest(t *testing.T, manifest recovery.Manifest) recovery.Manifest {
	t.Helper()
	manifest.FocusedIndex.Publications = 1
	manifest.ResolverCatalog.Publications = 1
	manifest.CallerPublication.Publications = 1
	manifest.Observation.Publications, manifest.Observation.V2Publications = 1, 1
	manifest.Observation.Files, manifest.Observation.Bytes = 1, 1
	manifest.Relationship.Publications, manifest.Relationship.Files, manifest.Relationship.Bytes = 1, 1, 1
	manifest.ManifestSHA256 = ""
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ManifestSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	return manifest
}

func t422SuppliedArchiveProjection(manifest recovery.Manifest) recovery.ArchiveTransitionManifest {
	value := recovery.ArchiveTransitionManifest{ManifestSchema: manifest.Schema, ManifestSHA256: manifest.ManifestSHA256}
	for _, artifact := range manifest.Inventory {
		value.Components = append(value.Components, recovery.ArchiveTransitionComponent{Name: artifact.Path, Classification: artifact.Classification,
			MediaType: artifact.MediaType, Bytes: uint64(artifact.Size), SHA256: artifact.SHA256})
	}
	value.Reports = []recovery.ArchiveTransitionReport{
		{Name: "focused_index", Schema: manifest.FocusedIndex.Schema, Publications: 1},
		{Name: "resolver_catalog", Schema: manifest.ResolverCatalog.Schema, Publications: 1},
		{Name: "caller_publication", Schema: manifest.CallerPublication.Schema, Publications: 1},
		{Name: "observation", Schema: manifest.Observation.Schema, Publications: 1, V2Publications: 1, Files: 1, Bytes: 1},
		{Name: "relationship", Schema: manifest.Relationship.Schema, Publications: 1, Files: 1, Bytes: 1},
	}
	return value
}

// Actual inherited DA/PC/FD6 and auth/HTTP/exact-read machinery. The launch
// bytes use a private fixture file, not the production stdin socket reader.
// The supplied-positive variants model the manifest only, not artifact owners.
func t422NativeArchiveReadHelper(t *testing.T, root, mode string) {
	t.Helper()
	deadline, err := strconv.ParseInt(os.Getenv("PHEBS_T422_FIXTURE_DEADLINE"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, deadline))
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
	snapshot, err := dispatchadmission.ProductionSemanticState()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "archive-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	launch, err := decodeT422SemanticLaunch(raw, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "archive-read-read-workspace" {
		t422NativeEpochFiveWorkspaceHelper(t, ctx, root, launch)
		return
	}
	var failed atomic.Bool
	// Observe the native control's failure latch without ending this small
	// helper before it can report the post-tail state. Main's cancellation is
	// separate from this HTTP/report-order fixture.
	launch.fail = func(error) { failed.Store(true) }
	control, err := newT422ArchiveControl(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.close() }()
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 2})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal(err)
	}
	state := t421NewExactReadAccountingState(func(raw []byte) error {
		var report t421ExactReadReport
		if json.Unmarshal(raw, &report) != nil || report.Schema != t421ExactReadReportSchema || report.RequestOrdinal < 1 || report.RequestOrdinal > 2 ||
			report.StoreReadAttempts != 0 || report.StoreWriteAttempts != 0 || report.MemberVisits != 0 {
			return errors.New("native archive R report malformed")
		}
		control.mu.Lock()
		reported := control.reported
		control.mu.Unlock()
		if report.RequestOrdinal == 1 && (reported || report.ControlFileReads != 1) {
			return errors.New("native archive R committed before report")
		}
		if mode == "archive-read-read-report-loss" {
			return io.ErrClosedPipe
		}
		return nil
	}, launch.fail)
	state.semantic, state.archive = launch, control
	authCtx, stopAuth := context.WithCancel(ctx)
	defer stopAuth()
	authService, err := auth.New(authCtx, auth.Options{Store: &t422LifecycleAuthFixture{}, Owners: owners, Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopAuth(); authService.WaitCleanup() }()
	server := httptest.NewUnstartedServer(t422OwnerHTTPHandler(owners, authService.Require(state.wrap(http.NotFoundHandler())), launch))
	server.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	server.Start()
	defer server.Close()
	fmt.Println(server.URL)
	var command [1]byte
	if _, err = io.ReadFull(os.Stdin, command[:]); err != nil || command[0] != 'i' {
		t.Fatal("native archive report inspection", err)
	}
	control.mu.Lock()
	reported := control.reported
	control.mu.Unlock()
	fmt.Printf("archive_reported=%t failed=%t\n", reported, failed.Load())
	if _, err = io.ReadFull(os.Stdin, command[:]); err != nil || command[0] != 'c' {
		t.Fatal("native archive helper close", err)
	}
}

func t422DriveNativeEpochFiveWorkspace(t *testing.T, ctx context.Context, address string, control *dispatchadmission.PhaseControl, dispatch *dispatchadmission.Controller, transport *storeaccounting.Transport) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 30 * time.Second}
	defer client.CloseIdleConnections()
	call := func(path, point string) {
		t.Helper()
		method := http.MethodPost
		if path == t422LifecycleFreshRead {
			method = http.MethodGet
		}
		request, err := http.NewRequestWithContext(ctx, method, address+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+t421ExactReadTestCredential)
		request.Header.Set(dispatchadmission.ProductionRequestHeader, control.RequestToken())
		if point != "" {
			request.Header.Set(t422WorkspacePointHeader, point)
		}
		if method == http.MethodGet {
			request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
			request.Header.Set(t421ExactReadOrdinalHeader, "1")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(path, point, err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, t422LifecycleReadBytes+1))
		if closeErr := response.Body.Close(); err != nil || closeErr != nil || len(body) > t422LifecycleReadBytes || response.StatusCode != http.StatusOK {
			t.Fatal(path, point, response.StatusCode, string(body), err, closeErr)
		}
		switch {
		case point != "":
			var sample t422WorkspaceSampleResponse
			if json.Unmarshal(body, &sample) != nil || sample.LogicalBytes == 0 || sample.AllocatedBytes == 0 {
				t.Fatal("actual epoch-five workspace sample", point, string(body))
			}
		case method == http.MethodGet:
			var cycle lifecycle.CycleObservation
			if json.Unmarshal(body, &cycle) != nil || cycle.OwnerTurns != 16 || cycle.Deleted != 16 || len(cycle.Owners) != 16 {
				t.Fatal("modeled-owner fresh cycle", string(body))
			}
			raw, err := base64.RawURLEncoding.DecodeString(response.Trailer.Get(t421ExactReadTrailer))
			var report t421ExactReadReport
			if err != nil || json.Unmarshal(raw, &report) != nil || report.Status != "complete" || report.RequestOrdinal != 1 ||
				report.ControlFileReads != 0 || report.StoreReadAttempts != 0 || report.StoreWriteAttempts != 0 || report.MemberVisits != 0 {
				t.Fatal("native fresh-cycle accounting trailer", report, err)
			}
		default:
			if string(body) != `{"status":"complete"}` {
				t.Fatal("native lifecycle command", path, string(body))
			}
		}
	}
	advance := func() {
		t.Helper()
		for _, operation := range []func() error{
			func() error { return control.FenceRequests(ctx) }, func() error { return control.Pause(ctx) },
			dispatch.Fence, transport.Fence, func() error { return control.Checkpoint(ctx) }, dispatch.Advance, transport.Advance,
			func() error { return control.Resume(ctx) }, func() error { return control.OpenRequests(ctx) },
		} {
			if err := operation(); err != nil {
				t.Fatal("native epoch-five transition", err)
			}
		}
	}
	call(t422LifecycleParkPath, "")
	if err := control.DrainOwners(ctx); err != nil {
		t.Fatal(err)
	}
	if err := control.OpenRequests(ctx); err != nil {
		t.Fatal(err)
	}
	call(t422WorkspaceSamplePath, "archive_finish")
	advance()
	call(t422WorkspaceSamplePath, "start")
	call(t422LifecycleFreshDrive, "")
	call(t422LifecycleFreshRead, "")
	call(t422WorkspaceSamplePath, "finish")
	advance()
	call(t422WorkspaceSamplePath, "start")
	call(t422WorkspaceSamplePath, "finish")
}

func t422NativeEpochFiveWorkspaceHelper(t *testing.T, ctx context.Context, root string, launch *t422SemanticLaunch) {
	t.Helper()
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
	runnerCtx, stopRunner := context.WithCancel(ctx)
	defer stopRunner()
	var turns, failures, readReports atomic.Uint64
	launch.fail = func(error) { failures.Add(1); stopRunner() }
	var modeledOwners []lifecycle.Owner
	for index := range 16 {
		modeledOwners = append(modeledOwners, t422LifecycleOwnerFixture{name: fmt.Sprintf("test-owner-%02d", index), turns: &turns})
	}
	control, err := newT422LifecycleControl(runnerCtx, launch, modeledOwners)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.bindWorkspaceBytes(st); err != nil {
		t.Fatal(err)
	}
	controller, err := lifecycle.NewController(st, modeledOwners...)
	if err != nil {
		t.Fatal(err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal(err)
	}
	runnerDone := make(chan struct{})
	// The host may itself be under pressure. This fixture supplies only the
	// Gate's capacity facts so it tests the closed route sequence independently
	// of host fullness; guarded workspace byte observations remain native.
	gate := lifecycle.NewGateWithProbe("supplied-epoch-five-capacity", func(ctx context.Context, _ string) (lifecycle.Capacity, error) {
		if err := ctx.Err(); err != nil {
			return lifecycle.Capacity{}, err
		}
		return lifecycle.Capacity{TotalBytes: 100 << 20, AvailableBytes: 90 << 20, UsedBytes: 10 << 20}, nil
	})
	go func() {
		defer close(runnerDone)
		lifecycle.RunWithControl(runnerCtx, controller, gate,
			lifecycle.DefaultIdleInterval, lifecycle.DefaultBacklogDelay, control.ObserveOwner, nil, owners, control.runner)
	}()
	defer func() { stopRunner(); <-runnerDone }()
	state := t421NewExactReadAccountingState(func(raw []byte) error {
		var report t421ExactReadReport
		if json.Unmarshal(raw, &report) != nil || report.Status != "complete" || report.RequestOrdinal != 1 ||
			report.ControlFileReads != 0 || report.StoreReadAttempts != 0 || report.MemberVisits != 0 || report.StoreWriteAttempts != 0 {
			return errT422LifecycleControl
		}
		readReports.Add(1)
		return nil
	}, launch.fail)
	state.semantic, state.lifecycle = launch, control
	authCtx, stopAuth := context.WithCancel(ctx)
	defer stopAuth()
	authService, err := auth.New(authCtx, auth.Options{Store: &t422LifecycleAuthFixture{}, Owners: owners, Config: config.Auth{APIKey: t421ExactReadTestCredential}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stopAuth(); authService.WaitCleanup() }()
	server := httptest.NewUnstartedServer(t422OwnerHTTPHandler(owners, authService.Require(state.wrap(http.NotFoundHandler())), launch))
	server.Config.BaseContext = func(net.Listener) context.Context { return runnerCtx }
	server.Start()
	defer server.Close()
	fmt.Println(server.URL)
	var command [1]byte
	if _, err := io.ReadFull(os.Stdin, command[:]); err != nil || command[0] != 'v' {
		t.Fatal("native epoch-five verification request", err)
	}
	if _, err := st.ListRepos(ctx); err != nil {
		t.Fatal("real SDK did not resume after native workspace walks", err)
	}
	control.mu.Lock()
	point, step := control.workspacePoint, control.step
	control.mu.Unlock()
	if failures.Load() != 0 || turns.Load() != 16 || readReports.Load() != 1 || point != 5 || step != 3 {
		t.Fatal("native epoch-five final control state", failures.Load(), turns.Load(), readReports.Load(), point, step)
	}
	observed := control.workspaceByteSnapshot()
	for _, phase := range []int{12, 13, 14} {
		if observed.Unavailable || !observed.Phases[phase-1].Completed || observed.Phases[phase-1].Maximum.LogicalBytes == 0 || observed.Phases[phase-1].Maximum.AllocatedBytes == 0 {
			t.Fatal("native epoch-five retained byte sample", phase, observed)
		}
	}
	fmt.Println("workspace_measured_and_resumed")
	if _, err := io.ReadFull(os.Stdin, command[:]); err != nil || command[0] != 'c' {
		t.Fatal("native epoch-five close request", err)
	}
}

func assertT422EpochFiveNativeWorkspaceReports(t *testing.T, raw string, input [32]byte) {
	t.Helper()
	counts := map[byte]int{'C': 0, 'D': 0, 'E': 0}
	bindings, sequence, pending := 0, uint64(0), byte(0)
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "WBB1:") {
			if line != fmt.Sprintf("WBB1:6:sha256:%x", input) {
				t.Fatal("native epoch-five WB binding", line)
			}
			bindings++
		}
		if !strings.HasPrefix(line, "WB1:") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 || fields[1] != "6" || len(fields[2]) != 2 {
			t.Fatal("native epoch-five WB frame", line)
		}
		phase, kind := fields[2][0], fields[2][1]
		ordinal, err := strconv.ParseUint(fields[3], 16, 64)
		if _, valid := counts[phase]; err != nil || !valid {
			t.Fatal("native epoch-five WB phase/ordinal", line)
		}
		if kind == 'B' && len(fields) == 4 && pending == 0 && ordinal == sequence+1 {
			pending, sequence = phase, ordinal
			continue
		}
		if kind != 'S' || len(fields) != 6 || phase != pending || ordinal != sequence {
			t.Fatal("native epoch-five WB pair", line)
		}
		logical, logicalErr := strconv.ParseUint(fields[4], 16, 64)
		allocated, allocatedErr := strconv.ParseUint(fields[5], 16, 64)
		if logicalErr != nil || allocatedErr != nil || logical == 0 || allocated == 0 {
			t.Fatal("native epoch-five positive WB sample", line)
		}
		counts[phase]++
		pending = 0
	}
	if bindings != 1 || sequence != 21 || pending != 0 || counts['C'] != 1 || counts['D'] != 18 || counts['E'] != 2 {
		t.Fatal("native epoch-five WB coverage", bindings, sequence, pending, counts)
	}
	t.Log("21 actual native WB pairs: five fixed boundaries plus sixteen modeled-owner turn workspace walks; Gate capacity is modeled, PC has 15 pairs, no real-owner/native-capacity/whole-phase claim")
}
