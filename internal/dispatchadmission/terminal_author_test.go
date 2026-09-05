//go:build darwin || linux

package dispatchadmission

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// This uses actual inherited PB/PC/DA sockets, a real parent-admitted helper
// process and real nested native commands. Its generic fixtures establish only
// transport/accounting composition, not protected author or Phebs admission.
func TestTerminalAuthorInheritedSharedController(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	record := productionTestRecord()
	record.Program, record.Producer.Sites, record.Tools = ProgramCorpusAuthor, AuthorSites(), record.Tools[:1]
	record.Producer.ID, record.InputSHA256 = 7, [32]byte{7}
	record.Control = PhaseControlConfig{TerminalAuthor: true, Phases: []uint32{1}, InitialPhase: 1,
		MaximumPhases: 1, MaximumWireBytes: 4 * FrameBytes, Timeout: time.Second}
	record.Limits.Producers, record.Limits.Sites, record.Limits.Roles = 3, 3, 2
	rootSite := Site{ID: 1004, Role: 6}
	config := Config{Limits: record.Limits, Producers: []Producer{
		{ID: 1, Binding: [32]byte{1}, Sites: []Site{rootSite}},
		{ID: 2, Binding: [32]byte{2}, Sites: []Site{{ID: 1, Role: RoleGit}}}, record.Producer},
		Phases: []Phase{{ID: 1, Roles: []RoleBudget{{Role: RoleGit, Attempts: 8}, {Role: 6, Attempts: 1}}},
			{ID: 2, Roles: []RoleBudget{{Role: RoleGit, Attempts: 8}, {Role: 6, Attempts: 1}}}}}
	controller, err := New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := controller.NewLocalProducer(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(ctx) }()
	other, err := controller.NewLocalProducer(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close(ctx) }()
	otherCommand := exec.CommandContext(ctx, "/usr/bin/true")
	otherHandle, err := other.Start(ctx, 1, otherCommand)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately retain its actual unsettled handle until the author closes.
	defer func() {
		if otherCommand.ProcessState == nil {
			_ = otherHandle.Wait()
		}
	}()
	admissionParent, admissionChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	controlParent, controlChild, err := NewPipe()
	if err != nil {
		_ = admissionParent.Close()
		_ = admissionChild.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = admissionParent.Close()
		_ = admissionChild.Close()
		_ = controlParent.Close()
		_ = controlChild.Close()
	}()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, executable, "-test.run=^TestProductionBootstrapHelper$", "terminal-author")
	command.Env = []string{"DISPATCH_PRODUCTION_TEST_HELPER=1", ProductionEnvironment + "=" + ProductionSelector}
	command.ExtraFiles, command.Stdout = []*os.File{admissionChild, controlChild}, writer
	var stderr bytes.Buffer
	command.Stderr = &stderr
	rootHandle, err := parent.StartInPhase(ctx, 1, rootSite, command)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			cancel()
			_ = rootHandle.Wait()
		}
	}()
	_ = writer.Close()
	_ = admissionChild.Close()
	_ = controlChild.Close()
	if err := SendProductionBootstrap(ctx, admissionParent, controlParent, record); err != nil {
		t.Fatal(err)
	}
	control, err := NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Close() }()
	served := make(chan error, 1)
	go func() {
		served <- controller.ServeChecked(ctx, 7, command.Process.Pid, admissionParent,
			func(context.Context, Site) error { return nil })
	}()
	if line, err := bufio.NewReader(reader).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("helper readiness %q: %v", line, err)
	}
	if err := control.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rootHandle.Wait(); err != nil {
		t.Fatal("helper root did not join", err, stderr.String())
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	count, err := controller.ProducerCount(7)
	if err != nil || !count.Attached || !count.Closed || count.Active != 0 || count.Ordinal != 1 {
		t.Fatal("terminal author invented checkpoint or lost actual Git", count, err)
	}
	controller.mu.Lock()
	sequence, fenced := controller.producers[7].sequence, controller.fenced
	controller.mu.Unlock()
	if sequence != 3 || fenced {
		t.Fatal("terminal close inserted a DA checkpoint or global fence")
	}
	snapshot, err := controller.Snapshot()
	if err != nil || snapshot.Complete || snapshot.Attempts != 3 {
		t.Fatal("author terminal close required global completion", snapshot, err)
	}
	otherCount, err := other.Count()
	if err != nil || otherCount.Active != 1 {
		t.Fatal("other producer was drained by author", err)
	}
	if err := otherHandle.Wait(); err != nil {
		t.Fatal(err)
	}
	continued, err := other.Start(ctx, 1, exec.CommandContext(ctx, "/usr/bin/true"))
	if err != nil || continued.Wait() != nil {
		t.Fatal("other producer could not continue in same phase", err)
	}
	if parent.Pause(ctx) != nil || other.Pause(ctx) != nil || controller.Fence() != nil || parent.Checkpoint(ctx) != nil || other.Checkpoint(ctx) != nil || controller.Advance() != nil || parent.Resume(2) != nil || other.Resume(2) != nil {
		t.Fatal("closed author did not preserve genuine global phase transition")
	}
	if parent.Close(ctx) != nil || other.Close(ctx) != nil || controller.Fence() != nil {
		t.Fatal("shared controller final drainage failed")
	}
	if snapshot, err := controller.Snapshot(); err != nil || !snapshot.Complete || snapshot.Attempts != 4 {
		t.Fatal("final shared prefix incomplete", err)
	}
}
