//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestT422AttemptEnvelope(t *testing.T) {
	state := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3,
		ProducerID: 2, Phase: 2, InputSHA256: [32]byte{1}}
	for _, mode := range []string{"job", "chunk", "unknown", "ordinary", "producer", "phase", "input", "oversized", "invalid JSON"} {
		t.Run(mode, func(t *testing.T) {
			selected, kind, raw := state, "job", []byte(`{"native":"retained"}`)
			switch mode {
			case "chunk", "unknown":
				kind = mode
			case "ordinary":
				selected.Mode = ""
			case "producer":
				selected.ProducerID = 1
			case "phase":
				selected.Phase = 5
			case "input":
				selected.InputSHA256 = [32]byte{}
			case "oversized":
				raw = bytes.Repeat([]byte{'x'}, store.MaxJobLifecycleReportSize+1)
			case "invalid JSON":
				raw = []byte("{")
			}
			encoded, err := t422EncodeAttemptReport(selected, kind, raw)
			if (err == nil) != (mode == "job" || mode == "chunk") {
				t.Fatalf("envelope result %v", err)
			}
			if err == nil {
				var event t422AttemptReport
				if json.Unmarshal(encoded, &event) != nil || event.Producer != 2 || event.Phase != 2 || !bytes.Equal(event.Report, raw) ||
					event.InputSHA256 != "sha256:"+hex.EncodeToString(state.InputSHA256[:]) {
					t.Fatal("native report or authenticated binding changed")
				}
			}
		})
	}
}

func TestT422AttemptOrdinaryBindings(t *testing.T) {
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	runner, scheduler, candidate := &store.Runner{}, &generationscheduler.Scheduler{}, &candidatejob.Worker{}
	failures := 0
	fail := func(error) { failures++ }
	bindT4013ExactReports(false, fail, candidate, runner)
	bindT422ExactChunkReports(false, fail, scheduler)
	if runner.LifecycleReports != nil || scheduler.ChunkReports != nil || candidate.OperationReports != nil {
		t.Fatal("ordinary disabled path allocated sinks")
	}
	bindT4013ExactReports(true, fail, candidate, runner)
	bindT422ExactChunkReports(true, fail, scheduler)
	for _, sink := range []func([]byte) error{runner.LifecycleReports, scheduler.ChunkReports, candidate.OperationReports} {
		if err := sink([]byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if failures != 0 || strings.Contains(output.String(), t422AttemptReportPrefix) ||
		!strings.Contains(output.String(), "job lifecycle: {}") || !strings.Contains(output.String(), "generation chunk lifecycle: {}") || !strings.Contains(output.String(), "candidate operation: {}") {
		t.Fatal("T40/candidate report bytes or selection changed", output.String())
	}
	if t422AttemptReportSink("job")([]byte(`{}`)) == nil {
		t.Fatal("unselected process invented phase authority")
	}
}

const t422AttemptHelperMode = "PHEBS_T422_ATTEMPT_HELPER_TEST"

// Actual inherited DA/PC and semantic owner turns bind the sink across a
// real phase change. Reports are supplied native-shaped test inputs, not real
// queue work, protected tool admission, or a phase measurement result.
func TestT422AttemptInheritedPhase(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	record, _ := t422LifecycleBootstrapRecord(t)
	record.Producer.ID = 5
	config := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}}
	for _, phase := range record.Control.Phases {
		config.Phases = append(config.Phases, dispatchadmission.Phase{ID: phase, Roles: []dispatchadmission.RoleBudget{
			{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}}})
	}
	controller, err := dispatchadmission.New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(); _ = child.Close() }()
	controlParent, controlChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controlParent.Close(); _ = controlChild.Close() }()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422AttemptInheritedHelper$")
	command.Env = []string{t422AttemptHelperMode + "=1", dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = []*os.File{child, controlChild}
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	_ = child.Close()
	_ = controlChild.Close()
	if err := dispatchadmission.SendProductionBootstrap(ctx, parent, controlParent, record); err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- controller.Serve(ctx, 5, command.Process.Pid, parent) }()
	defer func() { cancel(); <-served }()
	phase, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = phase.Close() }()
	scanner := bufio.NewScanner(output)
	read := func(want string) {
		t.Helper()
		if !scanner.Scan() || scanner.Text() != want {
			t.Fatalf("helper %q: %q %v", want, scanner.Text(), scanner.Err())
		}
	}
	read("held")
	drained := make(chan error, 1)
	go func() { drained <- phase.DrainOwners(ctx) }()
	select {
	case err := <-drained:
		t.Fatal("phase crossed held reporting turn", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := input.Write([]byte{'e'}); err != nil {
		t.Fatal(err)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func() error{func() error { return phase.Pause(ctx) }, controller.Fence, func() error { return phase.Checkpoint(ctx) }, controller.Advance, func() error { return phase.Resume(ctx) }, func() error { return phase.ReopenOwners(ctx) }} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := input.Write([]byte{'n'}); err != nil {
		t.Fatal(err)
	}
	read("reported")
	for _, operation := range []func() error{func() error { return phase.DrainOwners(ctx) }, func() error { return phase.Pause(ctx) }, controller.Fence, func() error { return phase.Checkpoint(ctx) }} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := input.Write([]byte{'c'}); err != nil {
		t.Fatal(err)
	}
	read("joined")
	if err := command.Wait(); err != nil {
		t.Fatal(err, diagnostic.String())
	}
	if !strings.Contains(diagnostic.String(), "SRB1:5:sha256:") || strings.Count(diagnostic.String(), "SR1:5:8\n") != 1 || strings.Count(diagnostic.String(), "SR1:5:9\n") != 1 {
		t.Fatal("actual selected compact source binding/phase missing", diagnostic.String())
	}
	if !strings.Contains(diagnostic.String(), "IXB1:5:sha256:") {
		t.Fatal("actual selected index binding missing", diagnostic.String())
	}
}

func TestT422AttemptInheritedHelper(t *testing.T) {
	if os.Getenv(t422AttemptHelperMode) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("actual owner binding failed", err)
	}
	ctx, err = bindT422SourceReports(ctx, func(error) { cancel() })
	if err != nil {
		t.Fatal("actual source binding failed", err)
	}
	ctx, err = bindT422IndexReports(ctx, func(error) { cancel() })
	if err != nil {
		t.Fatal("actual index binding failed", err)
	}
	var captured bytes.Buffer
	old := log.Writer()
	log.SetOutput(&captured)
	defer log.SetOutput(old)
	read := func() {
		var raw [1]byte
		if _, err := io.ReadFull(os.Stdin, raw[:]); err != nil {
			t.Fatal(err)
		}
	}
	runner, scheduler := &store.Runner{}, &generationscheduler.Scheduler{}
	bindT4013ExactReports(true, func(error) { cancel() }, nil, runner)
	bindT422ExactChunkReports(true, func(error) { cancel() }, scheduler)
	turn, err := owners.Enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatchadmission.ObserveProductionSourceRead(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := json.Marshal(store.JobLifecycleReport{Schema: store.JobLifecycleSchema, Event: "started", JobID: "job:neutral", Kind: store.JobCandidate, Target: "neutral", Attempt: 1, Outcome: "running"})
	if err := runner.LifecycleReports(job); err != nil {
		t.Fatal(err)
	}
	fmt.Println("held")
	read()
	turn.End()
	read()
	turn, err = owners.Enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatchadmission.ObserveProductionSourceRead(ctx); err != nil {
		t.Fatal(err)
	}
	chunk, _ := json.Marshal(generationscheduler.ChunkLifecycleReport{Schema: generationscheduler.ChunkLifecycleSchema, Event: "started",
		Identity: "sha256:" + strings.Repeat("1", 64), Generation: "sha256:" + strings.Repeat("2", 64), Stage: extractionpublication.ScheduleStage, Attempt: 0, Outcome: "running"})
	if err := scheduler.ChunkReports(chunk); err != nil {
		t.Fatal(err)
	}
	turn.End()
	lines := strings.Split(strings.TrimSpace(captured.String()), "\n")
	if len(lines) != 2 {
		t.Fatal("selected sink duplicated or lost reports")
	}
	record, _ := t422LifecycleBootstrapRecord(t)
	for index, line := range lines {
		var report t422AttemptReport
		_, raw, ok := strings.Cut(line, t422AttemptReportPrefix)
		if !ok || json.Unmarshal([]byte(raw), &report) != nil || report.Producer != 5 || report.Phase != uint32(8+index) || report.InputSHA256 != "sha256:"+hex.EncodeToString(record.InputSHA256[:]) {
			t.Fatal("sink did not bind actual native phase", line)
		}
	}
	fmt.Println("reported")
	read()
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if runner.LifecycleReports(job) == nil {
		t.Fatal("closed producer emitted guessed phase")
	}
	fmt.Println("joined")
}
