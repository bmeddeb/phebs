//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

const offlineWorkTestMode = "PHEBS_T422_OFFLINE_WORK_TEST"

func assertT422OfflineBindings(t *testing.T, raw string, producer uint32, digestByte string) {
	t.Helper()
	for _, family := range []string{"SR", "CC", "EP", "RM", "RL", "SB", "GC", "OP"} {
		want := fmt.Sprintf("%sB1:%d:sha256:%s%s\n", family, producer, digestByte, strings.Repeat("00", 31))
		if len(want) != 80 || strings.Count(raw, want) != 1 {
			t.Fatal("missing/duplicate actual offline binding", family, raw)
		}
	}
	for _, family := range []string{"ATB", "LCB", "IXB"} {
		if strings.Contains(raw, family) {
			t.Fatal("offline command acquired server-only coverage", family)
		}
	}
}

// Genuine inherited DA/PC/SA and native stderr pipe, with no database/engine.
// Event mode invokes the real bridge APIs, not product builders or native Git;
// the separate actual backup/restore fixture owns that CLI coverage claim.
func TestT422OfflineInheritedWork(t *testing.T) {
	for _, producer := range []uint32{10, 11} {
		for _, mode := range []string{"zero", "events", "missing", "wrong-command", "canceled-resolver", "closed-pipe"} {
			t.Run(fmt.Sprintf("%d/%s", producer, mode), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				record := t422ServeFlagsRecord()
				record.Producer.ID, record.Phase, record.InputSHA256 = producer, 12, [32]byte{1}
				record.Limits.Phases = 1
				record.Control = dispatchadmission.PhaseControlConfig{Phases: []uint32{12}, InitialPhase: 12, MaximumPhases: 1, MaximumWireBytes: 2 * dispatchadmission.FrameBytes, Timeout: time.Second}
				controller, err := dispatchadmission.New(ctx, dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}, Phases: []dispatchadmission.Phase{{ID: 12, Roles: []dispatchadmission.RoleBudget{{Role: 1}, {Role: 2}, {Role: 3}, {Role: 4}}}}})
				if err != nil {
					t.Fatal(err)
				}
				sa, err := storeaccounting.New(ctx, storeaccounting.Config{Producers: []storeaccounting.Producer{{ID: producer, Calls: 1, Transactions: 1}}, Phases: []storeaccounting.Phase{{ID: 12, Transactions: 10, Rows: 100}}})
				if err != nil {
					t.Fatal(err)
				}
				transport, err := storeaccounting.NewTransport(ctx, sa, storeaccounting.WireConfig{Producers: []storeaccounting.WireProducer{{ID: producer, Binding: record.Producer.Binding, Phases: 2048}}, AckTimeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = transport.Close() }()
				storeFile, config, err := transport.Open(producer)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = storeFile.Close() }()
				record.Store = &config
				parent, child, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = parent.Close(); _ = child.Close() }()
				pcParent, pcChild, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = pcParent.Close(); _ = pcChild.Close() }()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422OfflineWorkHelper$")
				command.Env = []string{offlineWorkTestMode + "=" + mode, dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionStoreSelector, "GORACE=atexit_sleep_ms=0"}
				command.ExtraFiles = []*os.File{child, pcChild, storeFile}
				command.WaitDelay = time.Second
				var output bytes.Buffer
				command.Stdout, command.Stderr = &output, &output
				if err = command.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if command.ProcessState == nil {
						_ = command.Process.Kill()
						_ = command.Wait()
					}
				}()
				_ = child.Close()
				_ = pcChild.Close()
				_ = storeFile.Close()
				if err = dispatchadmission.SendProductionBootstrap(ctx, parent, pcParent, record); err != nil {
					t.Fatal(err)
				}
				served := make(chan error, 1)
				go func() { served <- controller.Serve(ctx, producer, command.Process.Pid, parent) }()
				receiverJoined := false
				defer func() {
					cancel()
					if !receiverJoined {
						<-served
					}
				}()
				if err = command.Wait(); err != nil {
					t.Fatal(err, output.String())
				}
				failed := mode == "missing" || mode == "wrong-command" || mode == "canceled-resolver" || mode == "closed-pipe"
				err = <-served
				receiverJoined = true
				if (err != nil) != failed {
					t.Fatal("DA close", err, output.String())
				}
				if !failed {
					if err = transport.Wait(ctx, producer); err != nil {
						t.Fatal(err)
					}
					assertT422OfflineBindings(t, output.String(), producer, "01")
					want := 0
					if mode == "events" {
						want = 1
					}
					for _, record := range []string{"SR1:%X:C\n", "OP1:%X:C\n", "EP1:%X:C\n", "RM1:%X:C:000000000000002b\n", "RL1:%X:CP\n", "CC1:%X:CR\n", "CC1:%X:Cr\n", "SB1:%X:CB\n", "SB1:%X:CE\n", "GC1:%X:CB\n", "GC1:%X:CN\n"} {
						if strings.Count(output.String(), fmt.Sprintf(record, producer)) != want {
							t.Fatal("offline event framing", record, output.String())
						}
					}
				} else if mode == "canceled-resolver" || mode == "closed-pipe" {
					assertT422OfflineBindings(t, output.String(), producer, "01")
					if mode == "canceled-resolver" && strings.Count(output.String(), fmt.Sprintf("RM1:%X:C:000000000000002b\n", producer)) != 1 {
						t.Fatal("successful returned bytes lost on caller cancellation", output.String())
					}
				}
			})
		}
	}
}

func TestT422OfflineWorkHelper(t *testing.T) {
	mode := os.Getenv(offlineWorkTestMode)
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	if _, err = lifetime.TakeStoreOwner(); err != nil {
		t.Fatal(err)
	}
	state, err := dispatchadmission.ProductionWorkState()
	if err != nil || state.Mode != "" || state.Phase != 12 || state.OrdinaryOwnersDrained || dispatchadmission.ProductionSemanticSelected() {
		t.Fatal(state, err)
	}
	if _, err = dispatchadmission.ProductionSemanticState(); err == nil {
		t.Fatal("offline semantic state admitted")
	}
	verb := "backup"
	if state.ProducerID == 11 {
		verb = "restore"
	}
	if mode == "wrong-command" {
		verb = "serve"
	}
	err = dispatchadmission.RequireProductionWorkCommand(verb)
	if mode == "wrong-command" {
		if err == nil || lifetime.Close(ctx) == nil {
			t.Fatal("wrong command accepted")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "missing" {
		if dispatchadmission.ObserveProductionSourceRead(ctx) == nil || lifetime.Close(ctx) == nil {
			t.Fatal("missing coverage accepted")
		}
		return
	}
	ctx, err = bindT422ArchiveReports(ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "closed-pipe" {
		if err = os.Stderr.Close(); err != nil {
			t.Fatal(err)
		}
		if dispatchadmission.ObserveProductionSourceRead(ctx) == nil || lifetime.Close(ctx) == nil {
			t.Fatal("closed sink accepted")
		}
		return
	}
	if mode == "canceled-resolver" {
		caller, stop := context.WithCancel(ctx)
		stop()
		if dispatchadmission.ObserveProductionResolverBlob(caller, 43) == nil || lifetime.Close(ctx) == nil {
			t.Fatal("canceled successful resolver return accepted")
		}
		return
	}
	if mode == "events" {
		for _, observe := range []func() error{
			func() error { return dispatchadmission.ObserveProductionSourceRead(ctx) },
			func() error { return dispatchadmission.ObserveProductionParsedBlob(ctx) },
			func() error { return dispatchadmission.ObserveProductionPublication(ctx) },
			func() error { return dispatchadmission.ObserveProductionResolverBlob(ctx, 43) },
			func() error {
				return dispatchadmission.ObserveProductionRelationship(ctx, readaccounting.RelationshipProjection, 1)
			},
			func() error {
				_, err := dispatchadmission.ObserveProductionCache(ctx, readaccounting.CacheRootLoad, 0)
				return err
			},
			func() error {
				_, err := dispatchadmission.ObserveProductionCache(ctx, readaccounting.CacheRootValidation, 12)
				return err
			},
			func() error {
				_, err := dispatchadmission.ObserveProductionSourceCensus(ctx, readaccounting.SourceCensusBegin, 0, 0, 0)
				return err
			},
			func() error {
				_, err := dispatchadmission.ObserveProductionSourceCensus(ctx, readaccounting.SourceCensusEnd, 12, 0, 0)
				return err
			},
			func() error {
				_, err := dispatchadmission.ObserveProductionCatalogCensus(ctx, readaccounting.CatalogCensusBegin, 0, 0)
				return err
			},
			func() error {
				_, err := dispatchadmission.ObserveProductionCatalogCensus(ctx, readaccounting.CatalogCensusNoChild, 12, 0)
				return err
			},
		} {
			if err = observe(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = dispatchadmission.ProductionWorkState(); err == nil || !dispatchadmission.ProductionWorkSelected() {
		t.Fatal("closed lifetime lost sticky selection")
	}
}
