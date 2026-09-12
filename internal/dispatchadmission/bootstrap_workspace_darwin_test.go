//go:build darwin

package dispatchadmission

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"golang.org/x/sys/unix"
)

func workspaceTestRecord() ProductionBootstrap {
	r := productionStoreTestRecord()
	r.Producer.ID, r.Store.Producer = 6, 6
	r.SemanticMode, r.Control.OwnerControl = ProductionSemanticV3, true
	r.Store.Calls, r.Store.Transactions, r.Store.Phases = 40, 2, 14336
	r.Control.Phases, r.Control.MaximumPhases, r.Limits.Phases = []uint32{12, 13, 14}, 3, 3
	return r
}

func workspaceTestRoot(t *testing.T) (*os.File, ProductionWorkspaceBinding) {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	binding, err := DescribeProductionWorkspace(file, path)
	if err != nil {
		t.Fatal(err)
	}
	return file, binding
}

func workspaceEarlyTestRecord(producer uint32) ProductionBootstrap {
	r := workspaceTestRecord()
	var phases []uint32
	switch producer {
	case 2:
		phases = []uint32{2, 3, 4}
	case 3:
		phases = []uint32{5}
	case 4:
		phases = []uint32{6, 7, 8}
	default:
		return r
	}
	r.Producer.ID, r.Store.Producer = producer, producer
	r.Phase, r.Store.Phase, r.Control.InitialPhase = phases[0], phases[0], phases[0]
	r.Control.Phases = phases
	r.Control.MaximumPhases, r.Limits.Phases = len(phases), len(phases)
	r.Store.Phases = 0
	for _, phase := range phases {
		r.Store.Phases |= 1 << (phase - 1)
	}
	return r
}

// These are only exact descriptor profiles, not permission to emit early WB.
func TestProductionWorkspaceEarlyProfiles(t *testing.T) {
	_, binding := workspaceTestRoot(t)
	for _, producer := range []uint32{2, 3, 4} {
		for _, mode := range []string{"valid", "ordinary", "wrong_initial", "wrong_phases", "no_store", "omitted"} {
			t.Run(fmt.Sprintf("%d/%s", producer, mode), func(t *testing.T) {
				r := workspaceEarlyTestRecord(producer)
				r.Workspace = &binding
				switch mode {
				case "ordinary":
					r.SemanticMode = ""
				case "wrong_initial":
					r.Phase++
				case "wrong_phases":
					r.Control.Phases[len(r.Control.Phases)-1]++
				case "no_store":
					r.Store = nil
				case "omitted":
					r.Workspace = nil
				}
				if (r.validate() == nil) != (mode == "valid" || mode == "omitted") {
					t.Fatal("early descriptor profile", mode)
				}
			})
		}
	}
}

func TestProductionWorkspaceRecordAndDescriptor(t *testing.T) {
	file, binding := workspaceTestRoot(t)
	for _, mode := range []string{"valid", "epoch4", "ordinary", "author", "offline", "no_store", "input", "phase", "phases", "path", "inode", "fsid"} {
		r := workspaceTestRecord()
		value := binding
		r.Workspace = &value
		switch mode {
		case "epoch4":
			r.Producer.ID, r.Store.Producer, r.Phase, r.Store.Phase, r.Control.InitialPhase = 5, 5, 8, 8, 8
			r.Store.Phases = 1920
			r.Control.Phases = []uint32{8, 9, 10, 11}
			r.Control.MaximumPhases, r.Limits.Phases = 4, 4
		case "ordinary":
			r.SemanticMode = ""
		case "author":
			r.Program = ProgramCorpusAuthor
		case "offline":
			r.Producer.ID = 11
		case "no_store":
			r.Store = nil
		case "input":
			r.InputSHA256 = [32]byte{}
		case "phase":
			r.Phase = 13
		case "phases":
			r.Control.MaximumPhases = 4
		case "path":
			value.Path = "relative"
		case "inode":
			value.Inode = 0
		case "fsid":
			value.FSID = [2]int32{}
		}
		if (r.validate() == nil) != (mode == "valid" || mode == "epoch4") {
			t.Fatal("workspace profile", mode)
		}
	}
	legacy := productionTestRecord()
	raw, err := json.Marshal(legacy)
	if err != nil || bytes.Contains(raw, []byte(`"Workspace"`)) {
		t.Fatal("omitted legacy bytes changed")
	}
	for _, mode := range []string{"valid", "device", "inode", "fsid", "path", "mode", "replaced", "closed", "nil"} {
		t.Run(mode, func(t *testing.T) {
			file, value := workspaceTestRoot(t)
			switch mode {
			case "device":
				value.Device++
			case "inode":
				value.Inode++
			case "fsid":
				value.FSID[0]++
			case "path":
				value.Path += "/."
			case "mode":
				if err := os.Chmod(value.Path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := os.Rename(value.Path, value.Path+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(value.Path + "-old") })
				if err := os.Mkdir(value.Path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "closed":
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			case "nil":
				file = nil
			}
			workspace, err := adoptProductionWorkspace(file, value)
			if mode != "valid" {
				if err == nil {
					t.Fatal("descriptor mismatch admitted")
				}
				return
			}
			if err != nil || workspace.file != file {
				t.Fatal(err)
			}
			flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
			if err != nil || flags&unix.FD_CLOEXEC == 0 {
				t.Fatal("descriptor leaks to descendants", err)
			}
			lifetime := &ProductionLifetime{workspace: workspace}
			if lifetime.closeWorkspace() != nil {
				t.Fatal("owned close")
			}
			if lifetime.closeWorkspace() != nil {
				t.Fatal("repeated owned close")
			}
			if _, err := file.Stat(); err == nil {
				t.Fatal("workspace survived owner close")
			}
		})
	}
	// Merely describing identity never transfers or closes the parent handle.
	if _, err := file.Stat(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionWorkspaceHelper(t *testing.T) {
	mode := os.Getenv("DISPATCH_WORKSPACE_HELPER")
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	bootstrapContext := ctx
	if mode == "backup" || mode == "restore" {
		bootstrapContext = context.Background() // Parent must supply the deadline.
	}
	lifetime, err := BootstrapProduction(bootstrapContext)
	if mode == "missing" || mode == "wrong" {
		if err == nil || lifetime != nil || productionRuntime.Load() != nil {
			t.Fatal("missing original FD6 accepted")
		}
		// Bootstrap refusal must leave newly allocated descriptors usable;
		// no aliased os.File owner of a former socket slot may survive it.
		a, b, pipeErr := NewPipe()
		if pipeErr != nil {
			t.Fatal(pipeErr)
		}
		if a.Close() != nil || b.Close() != nil {
			t.Fatal("descriptor cleanup corrupted endpoint allocation")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	file, path, info, fsid, err := ProductionWorkspace()
	if mode == "omitted" {
		var native unix.Stat_t
		if err == nil || file != nil || unix.Fstat(6, &native) != nil || native.Mode&unix.S_IFMT != unix.S_IFDIR {
			t.Fatal("omitted binding touched FD6")
		}
	} else {
		if err != nil || file == nil || path == "" || !info.IsDir() || fsid == ([2]int32{}) {
			t.Fatal("borrowed workspace", err)
		}
		flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatal("inherited descriptor not protected", err)
		}
	}
	if _, err := lifetime.TakeStoreOwner(); err != nil {
		t.Fatal(err)
	}
	ready := "ready"
	if mode == "backup" || mode == "restore" {
		deadline, bounded := ProcessContext().Deadline()
		if !bounded || ProductionSemanticSelected() || RequireProductionWorkCommand(mode) != nil {
			t.Fatal("offline workspace/deadline binding lost")
		}
		ready = fmt.Sprintf("ready:%d", deadline.UnixNano())
	} else {
		owners, err := NewProductionOwners(ctx, OwnerLimits{Owners: 1, Requests: 1})
		if err != nil || BindProductionOwners(owners) != nil {
			t.Fatal("owner control fixture", err)
		}
	}
	if mode == "warm" {
		// Actual inherited DA/PC/SA/FD6 and one callback, not a native walk.
		if err := BindWarmStartWorkspace(func(warm context.Context) error {
			state, err := ProductionWarmStartWorkspaceState(warm)
			deadline, bounded := warm.Deadline()
			if err != nil || state.Phase != 3 || !state.OrdinaryOwnersDrained || !bounded {
				return ErrProtocol
			}
			_, err = fmt.Fprintf(os.Stdout, "warm:%d\n", deadline.UnixNano())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fmt.Fprintln(os.Stdout, ready); err != nil {
		t.Fatal(err)
	}
	var stop [1]byte
	if _, err := io.ReadFull(os.Stdin, stop[:]); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ProductionWorkspace(); err == nil {
		t.Fatal("closed borrowed workspace")
	}
	if mode == "omitted" {
		var native unix.Stat_t
		if unix.Fstat(6, &native) != nil || native.Mode&unix.S_IFMT != unix.S_IFDIR {
			t.Fatal("lifetime closed omitted FD6")
		}
	} else if _, err := file.Stat(); err == nil {
		t.Fatal("descriptor not closed")
	}
}

func TestProductionWorkspaceFailedBootstrapClosesExplicitFile(t *testing.T) {
	file, _ := workspaceTestRoot(t)
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	controlParent, controlChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controlParent.Close() }()
	// EOF rejects before any record/client; this explicit injection must not
	// consult the process's ambient FD6 and always releases its owned file.
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	lifetime, err := bootstrapProgramWithWorkspace(t.Context(), child, controlChild, nil, ProgramPhebs, file, nil)
	if err == nil || lifetime != nil {
		t.Fatal("truncated bootstrap admitted")
	}
	if _, err := file.Stat(); err == nil {
		t.Fatal("failed bootstrap leaked injected workspace")
	}
}

func TestProductionWorkspaceInherited(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "wrong", "omitted", "backup", "restore"} {
		t.Run(mode, func(t *testing.T) { testProductionWorkspaceInherited(t, mode, 0) })
	}
}

func TestProductionWorkspaceWarmInherited(t *testing.T) {
	testProductionWorkspaceInherited(t, "warm", 2)
}

func TestProductionWorkspaceEarlyInherited(t *testing.T) {
	for _, producer := range []uint32{2, 3, 4} {
		for _, mode := range []string{"valid", "missing", "wrong", "omitted"} {
			t.Run(fmt.Sprintf("%d/%s", producer, mode), func(t *testing.T) {
				testProductionWorkspaceInherited(t, mode, producer)
			})
		}
	}
}

func testProductionWorkspaceInherited(t *testing.T, mode string, earlyProducer uint32) {
	// Real inherited FD6 and DA/PC/SA mechanics, not a production-image or
	// whole-workspace traversal/pressure proof. No native database is started.
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	file, binding := workspaceTestRoot(t)
	r := workspaceTestRecord()
	if earlyProducer != 0 {
		r = workspaceEarlyTestRecord(earlyProducer)
	}
	if mode == "backup" || mode == "restore" {
		r = productionStoreTestRecord()
		if mode == "restore" {
			r.Producer.ID, r.Store.Producer = 11, 11
		}
		r.Control.MaximumPhases, r.Limits.Phases = 1, 3
		deadline, _ := ctx.Deadline()
		r.ArchiveDeadlineUnixNano = deadline.UnixNano()
	}
	r.Workspace = &binding
	if mode == "warm" {
		r.Control.WarmStartWorkspace = true
	}
	if mode == "omitted" {
		r.Workspace = nil
	}
	phaseIDs := []uint32{12, 13, 14}
	if earlyProducer != 0 {
		phaseIDs = r.Control.Phases
	}
	var storePhases []storeaccounting.Phase
	var dispatchPhases []Phase
	for i, phase := range phaseIDs {
		transactions := uint64(0)
		if i == 0 {
			transactions = 1
		}
		storePhases = append(storePhases, storeaccounting.Phase{ID: phase, Transactions: transactions, Rows: transactions})
		dispatchPhases = append(dispatchPhases, Phase{ID: phase, Roles: []RoleBudget{{Role: RoleGit, Attempts: transactions}, {Role: RoleSurreal}, {Role: RoleZoekt}, {Role: RoleCompatibility}}})
	}
	store, err := storeaccounting.New(ctx, storeaccounting.Config{Producers: []storeaccounting.Producer{{ID: r.Producer.ID, Calls: r.Store.Calls, Transactions: r.Store.Transactions}}, Phases: storePhases})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := storeaccounting.NewTransport(ctx, store, storeaccounting.WireConfig{Producers: []storeaccounting.WireProducer{{ID: r.Producer.ID, Binding: r.Store.Binding, Phases: r.Store.Phases}}, AckTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	storeChild, config, err := transport.Open(r.Producer.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeChild.Close() }()
	r.Store = &config
	parent, child, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(); _ = child.Close() }()
	pcParent, pcChild, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pcParent.Close(); _ = pcChild.Close() }()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProductionWorkspaceHelper$")
	command.Env = []string{"DISPATCH_WORKSPACE_HELPER=" + mode, ProductionEnvironment + "=" + ProductionStoreSelector, "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles, command.Stderr, command.WaitDelay = []*os.File{child, pcChild, storeChild, file}, os.Stderr, time.Second
	switch mode {
	case "missing":
		command.ExtraFiles = command.ExtraFiles[:3]
	case "wrong":
		other, _ := workspaceTestRoot(t)
		command.ExtraFiles[3] = other
	}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
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
	_ = pcChild.Close()
	_ = storeChild.Close()
	bootstrapErr := SendProductionBootstrap(ctx, parent, pcParent, r)
	if mode == "missing" || mode == "wrong" {
		if bootstrapErr == nil {
			t.Fatal("missing FD6 acknowledged")
		}
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
		snapshot, _ := transport.Snapshot()
		if snapshot.Store.Producers[0].Attached || snapshot.Store.Transactions != 0 {
			t.Fatal("refusal attached or charged unrelated endpoint", snapshot)
		}
		return
	}
	if bootstrapErr != nil {
		t.Fatal(bootstrapErr)
	}
	control, err := NewPhaseControl(ctx, pcParent, r.Producer.Binding, r.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Close() }()
	dispatch, err := New(ctx, Config{Limits: r.Limits, Producers: []Producer{r.Producer}, Phases: dispatchPhases})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- dispatch.Serve(ctx, r.Producer.ID, command.Process.Pid, parent) }()
	defer func() { cancel(); <-done }()
	reader := bufio.NewReader(output)
	ready, err := reader.ReadString('\n')
	wantReady := "ready\n"
	if r.ArchiveDeadlineUnixNano != 0 {
		wantReady = fmt.Sprintf("ready:%d\n", r.ArchiveDeadlineUnixNano)
	}
	if err != nil || ready != wantReady {
		t.Fatal(ready, err)
	}
	if r.Control.OwnerControl && control.DrainOwners(ctx) != nil {
		t.Fatal("parent owner fence")
	}
	if control.Pause(ctx) != nil || dispatch.Fence() != nil || transport.Fence() != nil {
		t.Fatal("parent fence")
	}
	if mode == "warm" {
		warmCtx, warmCancel := context.WithTimeout(ctx, time.Second)
		defer warmCancel()
		deadline, _ := warmCtx.Deadline()
		if control.Checkpoint(ctx) != nil || dispatch.Advance() != nil || transport.Advance() != nil || control.Resume(warmCtx) != nil {
			t.Fatal("actual inherited warm handoff")
		}
		line, err := reader.ReadString('\n')
		if err != nil || line != fmt.Sprintf("warm:%d\n", deadline.UnixNano()) {
			t.Fatal("original warm deadline/callback missing", err)
		}
		if control.ReopenOwners(ctx) != nil || control.DrainOwners(ctx) != nil ||
			control.Pause(ctx) != nil || dispatch.Fence() != nil || transport.Fence() != nil {
			t.Fatal("post-callback owner/request and terminal fences")
		}
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Wait(ctx, r.Producer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("parent descriptor closed by child", err)
	}
}

func TestProductionWorkspaceWarmProfile(t *testing.T) {
	_, binding := workspaceTestRoot(t)
	for _, mode := range []string{"selected", "omitted", "no_workspace", "no_store", "other_producer", "other_semantics", "short_profile"} {
		t.Run(mode, func(t *testing.T) {
			r := workspaceEarlyTestRecord(2)
			r.Workspace, r.Control.WarmStartWorkspace = &binding, true
			switch mode {
			case "omitted":
				r.Control.WarmStartWorkspace = false
				r.Workspace = nil
			case "no_workspace":
				r.Workspace = nil
			case "no_store":
				r.Store = nil
			case "other_producer":
				r.Producer.ID = 3
			case "other_semantics":
				r.SemanticMode = ""
			case "short_profile":
				r.Control.Phases = []uint32{2, 3}
				r.Control.MaximumPhases = 2
			}
			if (r.validate() == nil) != (mode == "selected" || mode == "omitted") {
				t.Fatal(mode)
			}
		})
	}
}
