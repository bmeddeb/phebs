//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestLinuxEpochJoinedSamplerAndControlFailurePreservesImages(t *testing.T) {
	for _, mode := range []string{"sampler", "control", "signer-keeper"} {
		t.Run(mode, func(t *testing.T) {
			tools, plan := linuxEpochLoanTools(t)
			operation, cancel := context.WithCancel(context.Background())
			settlement, settleCancel := context.WithCancel(context.Background())
			defer cancel()
			defer settleCancel()
			flow := &LinuxExecutionEpochFlow{lifetime: operation, cancel: cancel, settlement: settlement, settleCancel: settleCancel}
			// Only real control constructors and sealed image loans are exercised.
			// The workspace placeholders issue no execution/resource authority.
			held := linuxEpochWorkspaceFixture(t)
			held.flow = flow
			core, err := newLinuxEpochCoordinator(flow, held, accountingTestPlan(t))
			if err != nil {
				t.Fatal("actual DA/SA control constructor", err)
			}
			flow.core = core
			defer func() { _ = core.Close() }()
			var outcome linuxEpochLoanOutcome
			var images func(int) error
			images = func(index int) error {
				if index < len(tools) {
					return tools[index].WithImage(t.Context(), tools[index].identity.Role, func(ExecutionToolIdentity, *os.File) error { return images(index + 1) })
				}
				return plan.WithInput(t.Context(), "plan", func(*os.File) error {
					switch mode {
					case "sampler":
						row, e := linuxEpochProcessRow(os.Getpid())
						if e != nil {
							t.Fatal(e)
						}
						failed := make(chan struct{}, 1)
						var calls atomic.Int32
						// A real ticker performs a successful native initial census,
						// then takes the fixed failed probe. Its joined sticky result
						// must remain an operation failure, not an image failure.
						held.whole, e = newEpochProcessObservation(t.Context(), os.Getpid(), 2, row.ObservedName, map[string]string{row.ObservedName: "controller"}, func() { failed <- struct{}{} },
							func(ctx context.Context, pid int) ([]t4013.NativeProcessRecord, error) {
								if calls.Add(1) == 1 {
									return t4013.ObserveProcessTreeRecords(ctx, pid)
								}
								return nil, errors.New("fixed bounded probe refusal")
							})
						if e != nil {
							t.Fatal("initial native census", e)
						}
						defer func() { _, _ = held.whole.close() }()
						select {
						case <-failed:
						case <-time.After(5 * time.Second):
							t.Fatal("actual ticker did not report probe refusal")
						}
					case "control":
						// The genuinely attached local producer cannot be canceled
						// as unused. This latches the real DA protocol failure and
						// cancels its receiver; Close must still actually join it.
						if err := core.controller.CancelUnused(executionRootProducer); !errors.Is(err, dispatchadmission.ErrProtocol) {
							t.Fatal("real DA refusal not exercised", err)
						}
					case "signer-keeper":
						file, e := os.Open("/dev/null")
						if e != nil || file.Close() != nil {
							t.Fatal("keeper close fixture", e)
						}
						core.profileSignerNamespace = &executionSignerNamespaceCustody{file: file}
					}
					operationErr, custodyErr := held.join()
					if !core.closed || !core.closeJoined {
						t.Fatal("control join was inferred from an error")
					}
					if mode != "sampler" && core.Close() == nil {
						t.Fatal("public Close erased its sticky control/keeper error")
					}
					if mode == "signer-keeper" {
						if custodyErr == nil {
							t.Fatal("closed core hid actual signer keeper failure")
						}
					} else if operationErr == nil || custodyErr != nil {
						t.Fatal("joined operational failure mislabeled as custody", operationErr, custodyErr)
					}
					if mode == "sampler" {
						prefix, e := held.whole.snapshot()
						if e == nil || !prefix.Joined || len(prefix.Phases) != 1 || prefix.Phases[0].Observation.Available || prefix.Phases[0].Observation.CompletedCensuses != 1 {
							t.Fatal("failed sampler prefix/join lost", prefix, e)
						}
					}
					return outcome.afterJoin(nil, custodyErr, operationErr)
				})
			}
			loanErr := images(0)
			wantCustodyFailure := mode == "signer-keeper"
			if (loanErr != nil) != wantCustodyFailure || !wantCustodyFailure && outcome.operation == nil {
				t.Fatal("actual joined failure contaminated protected loans or disappeared", loanErr, outcome.operation)
			}
			for _, tool := range tools {
				_, checkErr := tool.Check(t.Context(), tool.identity.Role)
				if (checkErr != nil) != wantCustodyFailure {
					t.Fatal("tool post-loan state", checkErr)
				}
				if err := tool.Close(); (err != nil) != wantCustodyFailure || !tool.input.closed {
					t.Fatal("explicit tool keeper retirement", err)
				}
			}
		})
	}
}
