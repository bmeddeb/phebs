//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"slices"
	"testing"
	"time"
)

func checkpointEngineTestBinding(producer uint32) EngineMeasurementBinding {
	return EngineMeasurementBinding{Recipe: EngineMeasurementCheckpointV2, ProducerID: producer,
		ProducerBinding: [32]byte{4}, InputSHA256: [32]byte{7}}
}

func checkpointEngineTestRecord(producer uint32) ProductionBootstrap {
	record := indexGCTestRecord(producer)
	record.Tools[0].RuntimeRecipe, record.Tools[2].RuntimeRecipe = ProductionLinuxForegroundGitV3, ProductionLinuxForegroundGitV3
	record.Workspace = engineMeasurementTestRecord(3).Workspace
	record.EngineMeasurementRecipe = EngineMeasurementCheckpointV2
	record.EngineMeasurements = checkpointEngineTestBinding(producer).maximum()
	if producer == 4 {
		record.Control.TerminalPhase = 8
	}
	return record
}

func TestCheckpointEngineClosedBootstrap(t *testing.T) {
	for _, producer := range []uint32{4, 5} {
		record := checkpointEngineTestRecord(producer)
		if (record.validate() == nil) != (runtime.GOOS == "linux" && runtime.GOARCH == "amd64") {
			t.Fatal("checkpoint bootstrap platform/shape", producer)
		}
	}
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"absent-recipe", func(r *ProductionBootstrap) { r.EngineMeasurementRecipe = "" }},
		{"unknown-recipe", func(r *ProductionBootstrap) { r.EngineMeasurementRecipe += "-unknown" }},
		{"no-measurements", func(r *ProductionBootstrap) { r.EngineMeasurements = 0 }},
		{"old-maximum", func(r *ProductionBootstrap) { r.EngineMeasurements = 2 }},
		{"extra-measurement", func(r *ProductionBootstrap) { r.EngineMeasurements++ }},
		{"old-runtime", func(r *ProductionBootstrap) {
			r.Tools[0].RuntimeRecipe, r.Tools[2].RuntimeRecipe = ProductionLinuxForegroundGitV2, ProductionLinuxForegroundGitV2
		}},
		{"unpaired-runtime", func(r *ProductionBootstrap) { r.Tools[2].RuntimeRecipe = ProductionLinuxForegroundGitV2 }},
		{"no-workspace", func(r *ProductionBootstrap) { r.Workspace = nil }},
		{"no-store", func(r *ProductionBootstrap) { r.Store = nil }},
		{"no-input", func(r *ProductionBootstrap) { r.InputSHA256 = [32]byte{} }},
		{"no-terminal-owner", func(r *ProductionBootstrap) { r.Control.TerminalPhase = 0 }},
		{"archive", func(r *ProductionBootstrap) { r.ArchiveMeasurements = 1 }},
		{"ordinary", func(r *ProductionBootstrap) { r.SemanticMode = "" }},
		{"foreign-producer", func(r *ProductionBootstrap) { r.Producer.ID = 2 }},
		{"extra-phase", func(r *ProductionBootstrap) { r.Control.Phases = append(r.Control.Phases, 9) }},
		{"no-gc50", func(r *ProductionBootstrap) {
			r.Tools[2].Environment = r.Tools[2].Environment[:len(r.Tools[2].Environment)-1]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := checkpointEngineTestRecord(4)
			test.edit(&record)
			if !errors.Is(record.validate(), ErrProductionBootstrap) {
				t.Fatal("foreign checkpoint selection admitted")
			}
		})
	}
	legacy := engineMeasurementTestRecord(4)
	raw, err := json.Marshal(legacy)
	header, headerErr := (EngineMeasurementBinding{ProducerID: 4, ProducerBinding: [32]byte{4}, InputSHA256: [32]byte{7}}).header(2)
	if err != nil || bytes.Contains(raw, []byte("EngineMeasurementRecipe")) || headerErr != nil || string(header[:4]) != "EM01" || engineMeasurementMaximum(4) != 2 {
		t.Fatal("legacy bootstrap/header/budget changed", err, headerErr)
	}
	for _, producer := range []uint32{2, 3, 4} {
		r := indexGCTestRecord(producer)
		if r.EngineMeasurementRecipe != "" || r.Tools[0].RuntimeRecipe != ProductionLinuxForegroundGitV2 {
			t.Fatal("old runtime selection changed")
		}
	}
	recovery := checkpointEngineTestRecord(5)
	recovery.Control.BackupEndpointCarry = true
	if recovery.validate() == nil {
		t.Fatal("unqualified backup variant admitted checkpoint engine recipe")
	}
}

func TestCheckpointEngineWireSelectionAndRuntime(t *testing.T) {
	for _, producer := range []uint32{4, 5} {
		binding := checkpointEngineTestBinding(producer)
		maximum := binding.maximum()
		header, err := binding.header(maximum)
		wire, wireErr := binding.WireBytes(maximum)
		if err != nil || wireErr != nil || string(header[:4]) != "EM02" || wire != 152+80*uint64(maximum) {
			t.Fatal("checkpoint header/budget", producer, wire, err, wireErr)
		}
		for _, phase := range []uint32{5, 6, 7, 8, 9, 10, 11} {
			want := producer == 4 && phase >= 6 && phase <= 8 || producer == 5 && phase == 8
			if binding.phaseAllowed(phase) != want {
				t.Fatal("control phases became engine permission", producer, phase)
			}
		}
		if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
			record := checkpointEngineTestRecord(producer)
			args := serialIndexTestArgs(record.Tools[2].Path)
			got, err := productionIndexRuntimeArgs(SiteIndexBuild, record.Tools[2], args)
			if err != nil || len(got) != 10 || got[8] != "-parallelism=1" || got[9] != args[8] {
				t.Fatal("checkpoint changed serial HEAD workload", got, err)
			}
			git := []string{record.Tools[0].Path, "fetch", "--prune", "origin"}
			got, err = productionIndexRuntimeArgs(SiteSyncGit, record.Tools[0], git)
			if err != nil || !slices.Equal(got, git) {
				t.Fatal("checkpoint changed foreground Git", got, err)
			}
		}
	}
	for _, binding := range []EngineMeasurementBinding{
		checkpointEngineTestBinding(2), checkpointEngineTestBinding(3), checkpointEngineTestBinding(6),
		{Recipe: "unknown", ProducerID: 4, ProducerBinding: [32]byte{4}, InputSHA256: [32]byte{7}},
	} {
		if _, err := binding.WireBytes(7); err == nil {
			t.Fatal("foreign recipe issued wire budget")
		}
	}
	if _, err := EngineMeasurementWireBytes(7); err == nil {
		t.Fatal("legacy wire bound widened")
	}
}

func TestCheckpointEngineActualSocketSequence(t *testing.T) {
	for _, producer := range []uint32{4, 5} {
		t.Run(string(rune('0'+producer)), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			binding := checkpointEngineTestBinding(producer)
			phases := []uint32{6, 6, 7, 7, 7, 8, 8}
			if producer == 5 {
				phases = []uint32{8}
			}
			parent, child := archiveMeasurementTestPipe(t)
			done := make(chan error, 1)
			observed := []uint32{}
			go func() {
				done <- ServeEngineMeasurement(ctx, parent, binding, binding.maximum(), func(ctx context.Context, phase uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
					observed = append(observed, phase)
					return measure(ctx, engineMeasurementTestIdentity())
				})
			}()
			client, err := newEngineMeasurementClient(ctx, ctx, child, binding, binding.maximum())
			if err != nil {
				t.Fatal(err)
			}
			for _, phase := range phases {
				if err := client.measure(ctx, phase, engineMeasurementTestIdentity(), func(context.Context) error { return nil }); err != nil {
					t.Fatal("actual EM02 HOLD/RELEASE", phase, err)
				}
			}
			if err := client.measure(ctx, 8, engineMeasurementTestIdentity(), func(context.Context) error { return nil }); !errors.Is(err, ErrLimit) {
				t.Fatal("excess point admitted", err)
			}
			if err := client.close(ctx); err != nil {
				t.Fatal("actual endpoint EOF", err)
			}
			if err := <-done; err != nil || !slices.Equal(observed, phases) {
				t.Fatal("actual endpoint join/points", observed, err)
			}
		})
	}
}

func TestCheckpointEngineRefusesLegacyFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	parent, child := archiveMeasurementTestPipe(t)
	defer func() { _ = child.Close() }()
	binding := checkpointEngineTestBinding(4)
	done := make(chan error, 1)
	go func() {
		done <- ServeEngineMeasurement(ctx, parent, binding, 7, func(context.Context, uint32, func(context.Context, EngineMeasurementIdentity) error) error {
			return errors.New("legacy frame reached native guard")
		})
	}()
	var header [engineMeasurementHeaderBytes]byte
	if _, err := io.ReadFull(child, header[:]); err != nil || string(header[:4]) != "EM02" {
		t.Fatal("EM02 hello", err)
	}
	if _, err := child.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	frame := engineMeasurementFrame(engineMeasurementHold, 1, 6)
	if _, err := child.Write(frame[:]); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrProtocol) {
		t.Fatal("EM01 frame crossed EM02 binding", err)
	}
}
