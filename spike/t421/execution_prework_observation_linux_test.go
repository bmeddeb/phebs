//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLinuxExecutorPreworkObservationEarlyRefusalAndOwnedCloseFailure(t *testing.T) {
	observation := &executionPreworkObservation{}
	if roots, _, err := gatherInputTreeSourcesObserved(t.Context(), t.TempDir(), []LinuxInputTreeSource{{Name: "bad", Path: "relative"}}, observation); err == nil || roots != nil || !observation.settle(t.Context()) {
		t.Fatal("ordinary nil-factory refusal invented native uncertainty")
	}
	// Exact early-factory cleanup helper, with a deliberately preclosed
	// test-owned descriptor. No foreign descriptor is closed or replaced.
	file, err := os.CreateTemp(t.TempDir(), "owned")
	if err != nil || file.Close() != nil {
		t.Fatal("controlled owned Close fixture", err)
	}
	if err := closeInputTreeSources([]*inputTreeSourceRoot{{file: file, observation: observation}}); err == nil || !observation.nativeCloseFailed.Load() || observation.settle(t.Context()) {
		t.Fatal("partial factory cleanup lost actual owned Close refusal")
	}
	if observation.close(nil) != nil || !observation.nativeCloseFailed.Load() {
		t.Fatal("later successful cleanup reset native uncertainty")
	}
}

func TestLinuxExecutorPreworkObservationLoanAndKeeperCloseFailures(t *testing.T) {
	for _, mode := range []string{"loan", "keeper", "operation-only"} {
		t.Run(mode, func(t *testing.T) {
			file := linuxExecutorCandidateFixture(t, true, true, true, 120)
			observation := &executionPreworkObservation{}
			tool, err := protectLinuxExecutorCandidateFDObserved(t.Context(), file, observation)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tool.Close() })
			switch mode {
			case "loan":
				if err := tool.input.WithInput(t.Context(), "t422-execute", func(loan *os.File) error { return loan.Close() }); err == nil {
					t.Fatal("unexpected trusted loan closure accepted")
				}
			case "keeper":
				if tool.input.inputs["t422-execute"].file.Close() != nil {
					t.Fatal("controlled keeper Close fixture")
				}
			case "operation-only":
				tool.input.mu.Lock()
				tool.input.err = ErrExecutionInputCustody
				tool.input.mu.Unlock()
			}
			err = tool.Close()
			if err == nil {
				t.Fatal("sticky refusal was erased by Close")
			}
			if mode == "operation-only" {
				if observation.nativeCloseFailed.Load() || !linuxExecutorToolReleased(tool) {
					t.Fatal("operation error was promoted to native Close uncertainty")
				}
			} else if !observation.nativeCloseFailed.Load() || tool.input.closeErr == nil || linuxExecutorToolReleased(tool) {
				t.Fatal("native close evidence was lost at keeper release")
			}
		})
	}
}

func TestLinuxExecutorPreworkObservedNativeFailureIsRetired(t *testing.T) {
	observation := &executionPreworkObservation{}
	command := exec.CommandContext(t.Context(), "/usr/bin/false")
	if err := runReferenceCommandObserved(t.Context(), command, observation); err == nil || command.ProcessState == nil || command.ProcessState.ExitCode() != 1 || observation.unknown() || !observation.settle(t.Context()) {
		t.Fatal("genuine joined nonzero exit was confused with unknown helper custody")
	}
	// Preserve an earlier modeled unknown while genuinely settling this exact
	// already-Waited command; this is not a newly fabricated helper admission.
	observation.process(command, true, false)
	if !observation.unknown() || observation.canStart() {
		t.Fatal("original unknown command was discarded or allowed a new attempt")
	}
	if !observation.settle(t.Context()) || !observation.unknown() {
		t.Fatal("later actual settlement reset the original negative record")
	}
	observation.retainWorkspace(filepath.Join(t.TempDir(), "retained"))
	if !observation.workspaceRetained || observation.retainedWorkspace == "" {
		t.Fatal("private diagnostic residue was discarded")
	}
}

func TestLinuxExecutorPreworkCanonicalClaimPresentationsStaySpent(t *testing.T) {
	for _, mode := range []string{"clone", "rewrap", "foreign-tool", "foreign-builds", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			// This models the private publication shape only. The real source
			// issuer is exercised exclusively by the opt-in native composition.
			builds, tool := &LinuxExecutionGoBuildCustody{}, &LinuxExecutionToolCustody{}
			claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
			tool.executorClaim = claim
			original := newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
			presented := original
			switch mode {
			case "clone":
				presented = &linuxExecutorPrework{self: original, claim: claim, builds: builds, tool: tool}
			case "rewrap":
				presented = newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
			case "foreign-tool":
				copy := &LinuxExecutionToolCustody{executorClaim: claim}
				presented = newLinuxExecutorPrework(builds, copy, linuxExecutorPreworkSelection{})
			case "foreign-builds":
				presented = newLinuxExecutorPrework(&LinuxExecutionGoBuildCustody{}, tool, linuxExecutorPreworkSelection{})
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if presented.Run(ctx) == nil || !claim.used.Load() || original.Run(t.Context()) == nil {
				t.Fatal("copied/foreign/canceled binding minted a fresh attempt")
			}
			if presented != original && presented.self != presented {
				if presented.Close(t.Context()) == nil {
					t.Fatal("shallow copy closed original custody")
				}
			}
			if original.Close(t.Context()) == nil {
				t.Fatal("failed source claim was promoted to success")
			}
		})
	}
}

func TestLinuxExecutorPreworkCanonicalClaimIdentityPredicates(t *testing.T) {
	// Modeled private issuance shapes exercise exact predicates, independently
	// of Run's later custody/context refusals. They issue no protected authority.
	for _, route := range []string{"outer", "inner"} {
		for _, mode := range []string{"original", "clone", "foreign-tool", "foreign-builds", "rewrap"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				builds, tool := &LinuxExecutionGoBuildCustody{}, &LinuxExecutionToolCustody{}
				claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
				tool.executorClaim = claim
				var accepted bool
				if route == "outer" {
					original := newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
					presented := original
					switch mode {
					case "clone":
						presented = &linuxExecutorPrework{self: original, claim: claim, builds: builds, tool: tool}
					case "foreign-tool":
						original.tool = &LinuxExecutionToolCustody{executorClaim: claim}
					case "foreign-builds":
						original.builds = &LinuxExecutionGoBuildCustody{}
					case "rewrap":
						presented = newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
					}
					accepted = claim.consumeOuter(presented)
					if claim.consumeOuter(original) {
						t.Fatal("first presentation did not spend shared claim")
					}
				} else {
					original := &linuxExecutorInnerPrework{builds: builds, tool: tool}
					if !claim.bindInner(original) {
						t.Fatal("modeled canonical inner binding")
					}
					presented := original
					switch mode {
					case "clone":
						presented = &linuxExecutorInnerPrework{builds: builds, tool: tool}
					case "foreign-tool":
						original.tool = &LinuxExecutionToolCustody{executorClaim: claim}
					case "foreign-builds":
						original.builds = &LinuxExecutionGoBuildCustody{}
					case "rewrap":
						presented = &linuxExecutorInnerPrework{builds: builds, tool: tool}
						if claim.bindInner(presented) {
							t.Fatal("second inner binding replaced canonical owner")
						}
					}
					accepted = claim.consumeInner(presented)
					if claim.consumeInner(original) {
						t.Fatal("first presentation did not spend shared claim")
					}
				}
				if accepted != (mode == "original") || !claim.used.Load() {
					t.Fatal("canonical/foreign identity predicate was bypassed", accepted)
				}
			})
		}
	}
}

func TestLinuxExecutorPreworkClaimConcurrentFirstPresentation(t *testing.T) {
	builds, tool := &LinuxExecutionGoBuildCustody{}, &LinuxExecutionToolCustody{}
	claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
	tool.executorClaim = claim
	owner := newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{})
	var group sync.WaitGroup
	results := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() { defer group.Done(); results <- claim.consumeOuter(owner) }()
	}
	group.Wait()
	close(results)
	winners := 0
	for accepted := range results {
		if accepted {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("canonical first presentation is not atomic", winners)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if owner.Run(ctx) == nil || !errors.Is(owner.Close(ctx), ErrExecutionLauncher) {
		t.Fatal("spent modeled claim became usable")
	}
}
