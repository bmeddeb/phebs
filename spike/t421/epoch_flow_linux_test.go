//go:build linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"github.com/bmeddeb/phebs/spike/t4013"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinuxEpochFlowRequiresGenuineOwners(t *testing.T) {
	for _, request := range []LinuxExecutionEpochRequest{{}, {Epochs: &LinuxExecutionEpochConfigCustody{}}, {Epochs: &LinuxExecutionEpochConfigCustody{author: &LinuxExecutionAuthorCustody{}}, Phebs: &LinuxExecutionToolCustody{}, Zoekt: &LinuxExecutionToolCustody{}, Surreal: &LinuxExecutionToolCustody{}}} {
		flow, err := PrepareLinuxExecutionEpochFlow(t.Context(), request)
		if flow != nil || !errors.Is(err, ErrExecutionEpochOne) {
			t.Fatalf("unissued request accepted: %v", err)
		}
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
