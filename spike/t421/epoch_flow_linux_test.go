//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func TestLinuxEpochFlowRequiresGenuineOwners(t *testing.T) {
	for _, request := range []LinuxExecutionEpochRequest{{}, {Epochs: &LinuxExecutionEpochConfigCustody{}}, {Epochs: &LinuxExecutionEpochConfigCustody{author: &LinuxExecutionAuthorCustody{}}, Phebs: &LinuxExecutionToolCustody{}, Zoekt: &LinuxExecutionToolCustody{}, Surreal: &LinuxExecutionToolCustody{}}} {
		flow, err := PrepareLinuxExecutionEpochFlow(t.Context(), request)
		if flow != nil || !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatalf("unissued request accepted: %v", err)
		}
	}
}

func TestLinuxEpochPreparationMemoryReleasePreservesLoans(t *testing.T) {
	held := linuxEpochWorkspaceFixture(t)
	if held.openWorkspace() != nil {
		t.Fatal("actual owned workspace")
	}
	defer func() { _ = held.workspace.file.Close() }()
	tools, plan := linuxEpochLoanTools(t)
	var images func(int) error
	images = func(index int) error {
		if index < len(tools) {
			return tools[index].WithImage(t.Context(), tools[index].identity.Role, func(_ ExecutionToolIdentity, image *os.File) error {
				held.images[index], held.tools[index] = image, tools[index]
				return images(index + 1)
			})
		}
		return plan.WithInput(t.Context(), "plan", func(file *os.File) error {
			held.plan = file
			before, err := io.ReadAll(io.NewSectionReader(file, 0, MaxPlanBytes))
			if err != nil || len(before) == 0 {
				t.Fatal("actual sealed input bytes", err)
			}
			// A real collection/scavenge runs under genuine sealed loans and an
			// owned root. It grants no authority and promises no memory reduction.
			if err := held.releasePreparationMemory(t.Context()); err != nil {
				return err
			}
			after, err := io.ReadAll(io.NewSectionReader(file, 0, MaxPlanBytes))
			if err != nil || !bytes.Equal(before, after) || held.checkWorkspace() != nil {
				t.Fatal("collection changed bytes or owned-root identity", err)
			}
			for _, image := range held.images {
				if _, err := image.Stat(); err != nil {
					t.Fatal("collection retired a held image", err)
				}
			}
			return nil
		})
	}
	if err := images(0); err != nil {
		t.Fatal("real loan post-use validation", err)
	}
	for _, tool := range tools {
		if tool.Close() != nil || !tool.input.closed {
			t.Fatal("explicit image keeper retirement")
		}
	}
	if plan.Close() != nil || !plan.closed {
		t.Fatal("explicit input retirement")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if held.releasePreparationMemory(ctx) == nil {
			t.Fatal("invalid context reached checkpoint")
		}
	}
	if (*linuxEpochHeldFlow)(nil).releasePreparationMemory(t.Context()) == nil {
		t.Fatal("missing owner reached checkpoint")
	}
}

func TestLinuxEpochControlSettlementOutlivesOperationCancellation(t *testing.T) {
	for _, canceled := range []string{"operation", "settlement"} {
		t.Run(canceled, func(t *testing.T) {
			operation, stopOperation := context.WithCancel(context.Background())
			settlement, stopSettlement := context.WithCancel(context.Background())
			defer stopOperation()
			defer stopSettlement()
			flow := &LinuxExecutionEpochFlow{lifetime: operation, cancel: stopOperation, settlement: settlement, settleCancel: stopSettlement}
			// This fixture proves the real DA/SA/local-producer constructor and
			// terminal exchanges only. Its host tree placeholders issue no input
			// or execution authority and no operational child can be launched.
			held := linuxEpochWorkspaceFixture(t)
			held.flow = flow
			core, err := newLinuxEpochCoordinator(flow, held, accountingTestPlan(t))
			if err != nil {
				t.Fatal("real control constructor", err)
			}
			defer func() { _ = core.Close() }()
			if canceled == "operation" {
				stopOperation()
				if core.controller.Context().Err() != nil {
					t.Fatal("operation cancellation killed settlement context")
				}
				if _, err := core.parent.Count(); err != nil {
					t.Fatal("local producer lost actual settlement", err)
				}
				if _, err := core.store.Snapshot(); err != nil {
					t.Fatal("store prefix lost before actual close", err)
				}
			} else {
				stopSettlement()
			}
			err = core.Close() // Actual local Close exchange, receiver and SA joins.
			if canceled == "operation" && err != nil || canceled == "settlement" && err == nil {
				t.Fatal("actual control close lost cancellation boundary", err)
			}
			if !core.closed || operation.Err() == nil {
				t.Fatal("closed controls did not stop operational use")
			}
			if canceled == "operation" && settlement.Err() != nil {
				t.Fatal("settlement ended before its owner retired it")
			}
			stopSettlement()
		})
	}
}

func TestLinuxEpochOperationLinkStopsWithoutCancelingSettlement(t *testing.T) {
	for _, canceled := range []string{"caller", "flow"} {
		t.Run(canceled, func(t *testing.T) {
			caller, stopCaller := context.WithCancel(t.Context())
			lifetime, stopFlow := context.WithCancel(t.Context())
			settlement, stopSettlement := context.WithCancel(t.Context())
			defer stopCaller()
			defer stopFlow()
			defer stopSettlement()
			link := newLinuxEpochOperationLink(caller, lifetime)
			if link.ctx.Err() != nil {
				t.Fatal("live operation prematurely canceled")
			}
			if canceled == "caller" {
				stopCaller()
			} else {
				stopFlow()
			}
			select {
			case <-link.ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("operation ignored cancellation")
			}
			link.close() // Joins a fired callback before releasing its context.
			link.close()
			if settlement.Err() != nil {
				t.Fatal("operation cancellation became control cancellation")
			}
		})
	}
}

// These are genuine sealed direct-image/input loans only. They do not issue
// reference-build lineage, protected-tree authority or an operational flow.
func linuxEpochLoanTools(t *testing.T) ([4]*LinuxExecutionToolCustody, *LinuxExecutionInputCustody) {
	t.Helper()
	var tools [4]*LinuxExecutionToolCustody
	for i, role := range [4]string{"t422-author", "phebs", "zoekt-git-index", "surreal"} {
		tool, err := protectLinuxExecutionTool(t.Context(), role, "/usr/bin/sleep")
		if err != nil {
			t.Fatal("fixed sealed mechanical image", err)
		}
		tool.identity = ExecutionToolIdentity{Role: role}
		tools[i] = tool
		t.Cleanup(func() { _ = tool.Close() })
	}
	selection := linuxInputFixture(t)
	selection.Name = "plan"
	return tools, requireLinuxInputCustody(t, selection)
}

func TestLinuxEpochJoinedFailurePreservesLoanCleanup(t *testing.T) {
	for _, mode := range []string{"admission", "async-resource", "unclassified", "custody", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			tools, plan := linuxEpochLoanTools(t)
			operationErr := errors.New("fixed operational refusal")
			var outcome linuxEpochLoanOutcome
			var firstLoan *os.File
			var images func(int) error
			images = func(index int) error {
				if index < len(tools) {
					tool := tools[index]
					return tool.WithImage(t.Context(), tool.identity.Role, func(_ ExecutionToolIdentity, file *os.File) error {
						if index == 0 {
							firstLoan = file
						}
						return images(index + 1)
					})
				}
				return plan.WithInput(t.Context(), "plan", func(*os.File) error {
					var cleanupErr, resourceErr error
					useErr := error(&linuxEpochOperationFailure{operationErr})
					switch mode {
					case "async-resource":
						useErr, resourceErr = nil, operationErr
					case "unclassified":
						useErr = operationErr
					case "custody":
						if firstLoan.Close() != nil {
							t.Fatal("close selected actual loan")
						}
					case "cleanup":
						keeper, err := os.Open("/dev/null")
						if err != nil || keeper.Close() != nil {
							t.Fatal("cleanup error fixture")
						}
						cleanupErr = keeper.Close() // Actual keeper close refusal.
						if cleanupErr == nil {
							t.Fatal("cleanup error fixture did not fail")
						}
					}
					return outcome.afterJoin(useErr, cleanupErr, resourceErr)
				})
			}
			loanErr := images(0)
			clean := mode == "admission" || mode == "async-resource"
			if clean && (loanErr != nil || !errors.Is(outcome.operation, operationErr)) {
				t.Fatal("joined operational refusal contaminated protected loans", loanErr, outcome.operation)
			}
			if !clean && loanErr == nil {
				t.Fatal("actual custody/cleanup/unclassified failure was masked")
			}
			if !errors.Is(errors.Join(outcome.operation, loanErr), operationErr) && clean {
				t.Fatal("flow lost its operational failure")
			}
			for i, tool := range tools {
				closeErr := tool.Close()
				wantSticky := mode == "unclassified" || mode == "cleanup" || mode == "custody" && i == 0
				if wantSticky != (closeErr != nil) || !tool.input.closed || len(tool.input.inputs) != 0 {
					t.Fatal("explicit tool cleanup lost error or release status", closeErr)
				}
			}
			if err := plan.Close(); err != nil || !plan.closed || len(plan.inputs) != 0 {
				t.Fatal("explicit plan cleanup incomplete", err)
			}
		})
	}
}

func TestLinuxEpochUnknownJoinRetainsImageLoan(t *testing.T) {
	tools, _ := linuxEpochLoanTools(t)
	tool := tools[0]
	entered, joined := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-joined:
		default:
			close(joined)
		}
	}()
	var outcome linuxEpochLoanOutcome
	loanDone, closeDone := make(chan error, 1), make(chan error, 1)
	go func() {
		loanDone <- tool.WithImage(t.Context(), tool.identity.Role, func(_ ExecutionToolIdentity, _ *os.File) error {
			close(entered)
			<-joined // The owned join remains unresolved; no loan may return.
			return outcome.afterJoin(&linuxEpochOperationFailure{ErrExecutionEpochOne}, nil, nil)
		})
	}()
	<-entered
	go func() { closeDone <- tool.Close() }()
	select {
	case <-loanDone:
		t.Fatal("unknown join released its loan")
	case <-closeDone:
		t.Fatal("unknown join allowed keeper cleanup")
	case <-time.After(10 * time.Millisecond):
	}
	close(joined)
	if err := <-loanDone; err != nil || !errors.Is(outcome.operation, ErrExecutionEpochOne) {
		t.Fatal("joined operation failure contaminated image", err)
	}
	if err := <-closeDone; err != nil || !tool.input.closed {
		t.Fatal("actual join failed to release keeper", err)
	}
}
func TestLinuxEpochCoordinatorRejectsForgedViews(t *testing.T) {
	for _, adapter := range []*linuxEpochCoordinator{nil, {}, {held: &linuxEpochHeldFlow{}, core: &ExecutionEpochOne{}}} {
		if adapter.check(t.Context()) == nil {
			t.Fatal("unissued logical view acquired authority")
		}
	}
	core := &ExecutionEpochOne{}
	flow := &LinuxExecutionEpochFlow{core: core}
	adapter := &linuxEpochCoordinator{held: &linuxEpochHeldFlow{flow: flow}, core: core}
	core.native = adapter
	core.epochs = &ExecutionEpochConfigCustody{native: &linuxEpochConfigAdapter{adapter}, author: &ExecutionAuthorCustody{native: adapter}}
	if adapter.check(t.Context()) == nil {
		t.Fatal("connected logical views replaced actual owners")
	}
}
func TestLinuxEpochBorrowRefusesPublicReentry(t *testing.T) {
	flow := &LinuxExecutionEpochFlow{}
	author := &LinuxExecutionAuthorCustody{borrowedBy: flow}
	epochs := &LinuxExecutionEpochConfigCustody{author: author, borrowedBy: flow, active: true}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := author.AuthorNext(ctx); err == nil {
		t.Fatal("public author reentry")
	}
	if err := author.Close(ctx); err == nil || author.closing || author.released {
		t.Fatal("borrowed Close changed author")
	}
	if err := epochs.Close(ctx); err == nil || epochs.closing || epochs.closed {
		t.Fatal("borrowed Close changed epoch")
	}
	if err := epochs.ReleaseListener(ctx, 1); err == nil || epochs.err != nil || epochs.released != 0 {
		t.Fatal("public listener reentry changed epoch")
	}
}
func TestLinuxEpochClassifierBindsActualObjectAndLifetime(t *testing.T) {
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()
	row, err := linuxEpochProcessRow(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	images := []linuxEpochClassImage{{image, "controller"}}
	if class, ok := linuxEpochClassifyRow(t.Context(), row, images); !ok || class != "controller" {
		t.Fatal("actual image did not match")
	}
	for _, mutation := range []func(){func() { row.StartIdentity = "1" }, func() { row.ParentPID++ }, func() { row.ObservedName = "unknown" }} {
		original := row
		mutation()
		if _, ok := linuxEpochClassifyRow(t.Context(), row, images); ok {
			t.Fatal("changed process identity matched")
		}
		row = original
	}
	if _, ok := linuxEpochClassifyRow(t.Context(), row, nil); ok {
		t.Fatal("unknown image accepted")
	}
	if _, ok := linuxEpochClassifyRow(context.Background(), row, []linuxEpochClassImage{{image, "controller"}, {image, "git"}}); ok {
		t.Fatal("ambiguous class accepted")
	}
}

func TestLinuxEpochClassifierDeadlineDoesNotCommitCensus(t *testing.T) {
	gauge, err := NewProcessObservationGauge(1, "birth", map[string]string{"root": "controller"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	gauge.classify = func(check context.Context, _ t4013.NativeProcessRecord) (int, bool) { <-check.Done(); return 0, true }
	observation, err := gauge.acceptRowsContext(ctx, []t4013.NativeProcessRecord{{PID: 1, ParentPID: 0, StartIdentity: "birth", ObservedName: "root", RSSBytes: 1}})
	if err == nil || observation.Available || observation.CompletedCensuses != 0 || observation.FailureClass != "measurement_unavailable" {
		t.Fatal("late classification committed a census", observation, err)
	}
}
func TestLinuxEpochCloseDeadlineRetainsBorrowAndDoesNotHoldMutex(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	flow := &LinuxExecutionEpochFlow{lifetime: lifetime, cancel: cancel, done: make(chan struct{})}
	author := &LinuxExecutionAuthorCustody{borrowedBy: flow}
	epochs := &LinuxExecutionEpochConfigCustody{author: author, borrowedBy: flow, active: true}
	flow.request.Epochs = epochs
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()
	finished := make(chan error, 1)
	go func() { finished <- flow.Close(ctx) }()
	<-lifetime.Done()
	if !flow.mu.TryLock() {
		t.Fatal("Close holds its mutex while joining")
	}
	flow.mu.Unlock()
	if err := <-finished; err == nil || author.borrowedBy != flow || !epochs.active || epochs.borrowedBy != flow {
		t.Fatal("deadline was treated as a join")
	}
	close(flow.done)
	if err := flow.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxEpochCloseRejectsUnissuedLifetime(t *testing.T) {
	var absent *LinuxExecutionEpochFlow
	if err := absent.Close(t.Context()); err != nil {
		t.Fatal("nil Close changed", err)
	}
	for _, missing := range []string{"all", "lifetime", "cancel", "done"} {
		t.Run(missing, func(t *testing.T) {
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			flow := &LinuxExecutionEpochFlow{lifetime: lifetime, cancel: cancel, done: make(chan struct{})}
			switch missing {
			case "all":
				flow = &LinuxExecutionEpochFlow{}
			case "lifetime":
				flow.lifetime = nil
			case "cancel":
				flow.cancel = nil
			case "done":
				flow.done = nil
			}
			if err := flow.Close(t.Context()); err != ErrExecutionEpochOne || flow.closing || lifetime.Err() != nil {
				t.Fatal("unissued Close mutated or canceled state", err)
			}
		})
	}
}

func TestLinuxEpochWholePlanLoanRewindsOnlyProtectedDescription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan")
	raw := []byte("fixed-plan-bytes")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := ProtectLinuxExecutionInputs(t.Context(), []ExecutionInputCopy{{Name: "plan", Path: path, SHA256: SHA256(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	if err := input.WithInput(t.Context(), "plan", func(plan *os.File) error {
		held := &linuxEpochHeldFlow{plan: plan, planEntry: input.inputs["plan"]}
		for i := 0; i < 3; i++ {
			if held.rewindAuthorPlan(t.Context()) != nil {
				return ErrExecutionAuthorCustody
			}
			actual, err := io.ReadAll(plan)
			if err != nil || !bytes.Equal(actual, raw) {
				return ErrExecutionAuthorCustody
			}
		}
		return nil
	}); err != nil {
		t.Fatal("whole-loan shared offset lost a later author", err)
	}
}
