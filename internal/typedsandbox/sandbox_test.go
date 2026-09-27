package typedsandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const testImage = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testContainer = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestBoundedReadPreservesFilesystemError(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		want       error
	}{
		{"open disappearance", filepath.Join(root, "absent"), os.ErrNotExist},
		{"read syscall", root, syscall.EISDIR},
		{"byte ceiling", file, ErrRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := readSmall(tc.path, 4)
			if !errors.Is(err, tc.want) || data != nil {
				t.Fatalf("lost read classification or retained partial bytes: %v", err)
			}
		})
	}
}

type daemon struct {
	mu                        sync.Mutex
	options                   Options
	config                    config
	name, fault               string
	created, removed, started bool
	start                     chan struct{}
	deadline                  chan struct{}
	inspections               []string
}

func fakeDaemon(t *testing.T, fault string) (*daemon, Options) {
	t.Helper()
	// Unix socket paths are shorter than the testing package's per-case paths.
	root, err := os.MkdirTemp("/tmp", "typed-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(name, 0700)
			}
			return nil
		})
		_ = os.RemoveAll(root)
	})
	inputs := filepath.Join(root, "inputs")
	if err = os.Mkdir(inputs, 0o700); err != nil {
		t.Fatal(err)
	}
	a := testScratchAuthority()
	options := Options{scratch: &a, Socket: filepath.Join(root, "docker.sock"), ImageID: testImage, Inputs: inputs}
	options = testControlOptions(t, options)

	d := &daemon{options: options, fault: fault, start: make(chan struct{}), deadline: make(chan struct{})}
	listener, err := net.Listen("unix", options.Socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(d.serve))
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return d, options
}

func (d *daemon) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, apiVersion)
	write := func(status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if value != nil {
			_ = json.NewEncoder(w).Encode(value)
		}
	}
	switch {
	case path == "/info":
		if d.fault == "oversized info" {
			w.WriteHeader(200)
			_, _ = w.Write(bytes.Repeat([]byte("x"), maxResponseBytes+1))
			return
		}
		security := []string{"name=seccomp,profile=builtin", "name=apparmor"}
		if d.fault == "missing seccomp" {
			security = security[1:]
		}
		write(200, map[string]any{"ID": "test-daemon", "OSType": "linux", "CgroupVersion": "2", "MemoryLimit": true, "SwapLimit": true, "PidsLimit": true, "CPUCfsQuota": true, "SecurityOptions": security})
	case strings.HasPrefix(path, "/images/"):
		imageConfig := config{}
		if d.fault == "image environment" {
			imageConfig.Env = []string{"SECRET=private"}
		}
		if d.fault == "image volume" {
			imageConfig.Volumes = map[string]struct{}{"/escape": {}}
		}
		write(200, map[string]any{"Id": testImage, "Os": "linux", "Architecture": "arm64", "Config": imageConfig})
	case path == "/containers/create":
		if json.NewDecoder(r.Body).Decode(&d.config) != nil {
			write(400, nil)
			return
		}
		d.name = r.URL.Query().Get("name")
		d.created = true
		if d.fault == "changed invocation" {
			d.config.Cmd[3] += " "
		}
		if d.fault == "changed effective configuration" {
			d.config.HostConfig.Privileged = true
		}
		write(201, map[string]any{"Id": testContainer})
	case strings.HasSuffix(path, "/json"):
		d.inspections = append(d.inspections, path)
		if !d.created || d.removed {
			write(404, nil)
			return
		}
		got := inspection{ID: testContainer, Image: testImage, Name: "/" + d.name, Config: wantWithoutHost(d.config), HostConfig: d.config.HostConfig, AppArmorProfile: "docker-default"}
		got.Mounts = append(got.Mounts, struct {
			Type, Source, Destination string
			RW                        bool
		}{"bind", d.options.Inputs, "/inputs", false})
		if d.config.Cmd[0] == SupervisorCommand {
			for _, m := range d.config.HostConfig.Mounts {
				if m.Target == "/scratch" || m.Target == "/controls" {
					got.Mounts = append(got.Mounts, struct {
						Type, Source, Destination string
						RW                        bool
					}{m.Type, m.Source, m.Target, !m.ReadOnly})
				}
			}
		}
		switch d.fault {
		case "controls missing mount":
			got.Mounts = got.Mounts[:2]
		case "controls writable mount":
			got.Mounts[2].RW = true
		case "controls wrong mount":
			got.Mounts[2].Source += "-other"
		case "controls extra mount":
			got.Mounts = append(got.Mounts, got.Mounts[2])
		case "controls recursive mount":
			got.HostConfig.Mounts[2].BindOptions.NonRecursive = false
		case "controls shared mount":
			got.HostConfig.Mounts[2].BindOptions.Propagation = "shared"
		}
		if d.started && strings.HasPrefix(d.fault, "watchdog") {
			got.State.ExitCode = 124
		}
		write(200, got)
	case strings.HasSuffix(path, "/attach"):
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		d.mu.Unlock()
		select {
		case <-d.start:
		case <-r.Context().Done():
			d.mu.Lock()
			return
		}
		d.mu.Lock()
		if strings.Contains(d.fault, "delayed") {
			d.mu.Unlock()
			select {
			case <-d.deadline:
			case <-r.Context().Done():
				d.mu.Lock()
				return
			}
			time.Sleep(30 * time.Millisecond)
			d.mu.Lock()
		}
		if d.fault == "canceled normal report" {
			d.mu.Unlock()
			select {
			case <-d.deadline:
			case <-r.Context().Done():
				d.mu.Lock()
				return
			}
			d.mu.Lock()
		}
		if d.fault == "truncated stream" {
			_, _ = w.Write([]byte{1, 0, 0})
			return
		}
		report := supervisorReport{Schema: reportSchema, Allowance: d.options.Allowance, Phase: d.options.Control.Phase, RequestDigest: d.options.Control.RequestDigest, Stdout: []byte("fixture result"), Complete: true, Resources: Resources{LimitsVerified: true, Samples: 1}}
		if d.config.Cmd[0] == SupervisorCommand {
			report.Schema = reportSchema
		}
		if d.fault == "wrong allowance report" {
			report.Allowance.Deadline++
		}
		if d.fault == "wrong phase report" {
			report.Phase = ControlExecute
		}
		if d.fault == "crossed supervisor report" {
			report.Schema = "unknown-profile"
		}
		if d.fault == "unknown supervisor report" {
			report.Schema = "unknown-profile"
		}
		if d.fault == "incomplete report" {
			report.Complete = false
		}
		raw, _ := json.Marshal(report)
		if strings.HasPrefix(d.fault, "watchdog") {
			snapshots := newWatchdogSnapshots(WallLimit)
			snapshots.invocation(invocationDigest(d.options.Allowance, d.options.Control.Phase, d.options.Control.RequestDigest))
			snapshots.resources(Resources{LimitsVerified: true, Samples: 1})
			snapshots.progress(2, 0)
			frame := snapshots.frame.Load().data
			if strings.Contains(d.fault, "malformed") {
				frame = []byte("bad frame")
			}
			if strings.Contains(d.fault, "wrong invocation") {
				snapshots.invocation(testImage)
				frame = snapshots.frame.Load().data
			}
			if strings.Contains(d.fault, "unavailable") {
				return
			}
			var stderrHeader [8]byte
			stderrHeader[0] = 2
			binary.BigEndian.PutUint32(stderrHeader[4:], uint32(len(frame)))
			_, _ = w.Write(stderrHeader[:])
			_, _ = w.Write(frame)
			if d.fault == "watchdog only" {
				return
			}
			raw = []byte(`{"stdout":"truncated normal report`)
		}
		var header [8]byte
		header[0] = 1
		binary.BigEndian.PutUint32(header[4:], uint32(len(raw)))
		_, _ = w.Write(header[:])
		_, _ = w.Write(raw)
	case strings.HasSuffix(path, "/start"):
		if d.fault == "start error" {
			write(500, map[string]string{"message": "private daemon diagnostic"})
			return
		}
		d.started = true
		close(d.start)
		write(204, nil)
	case strings.HasSuffix(path, "/wait"):
		if strings.Contains(d.fault, "deadline") || d.fault == "canceled normal report" {
			d.mu.Unlock()
			<-r.Context().Done()
			close(d.deadline)
			d.mu.Lock()
			return
		}
		status := 0
		if strings.HasPrefix(d.fault, "watchdog") {
			status = 124
		}
		write(200, map[string]int{"StatusCode": status})
	case r.Method == "DELETE":
		if d.fault == "cleanup error" {
			write(500, nil)
			return
		}
		d.removed = true
		write(204, nil)
	default:
		write(404, nil)
	}
}

func TestRunFiniteContainerLifecycle(t *testing.T) {
	d, options := fakeDaemon(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := runFake(ctx, options)
	if err != nil || !result.Removed || result.ContainerID != testContainer || result.ExitCode != 0 || string(result.Stdout) != "fixture result" || !result.Resources.LimitsVerified {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.started || !d.removed {
		t.Fatal("lifecycle did not start and remove exact owner")
	}
	if _, err = os.Stat(journalPath(options)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
}

func TestRunRefusesWithoutLosingCustody(t *testing.T) {
	for _, test := range []struct {
		fault             string
		started, retained bool
	}{
		{"missing seccomp", false, false}, {"oversized info", false, false}, {"image environment", false, false}, {"image volume", false, false},
		{"changed effective configuration", false, true}, {"controls missing mount", false, true}, {"controls writable mount", false, true}, {"controls wrong mount", false, true}, {"controls extra mount", false, true}, {"controls recursive mount", false, true}, {"controls shared mount", false, true}, {"start error", false, false}, {"truncated stream", true, false},
		{"incomplete report", true, false}, {"cleanup error", true, true},
	} {
		t.Run(test.fault, func(t *testing.T) {
			d, options := fakeDaemon(t, test.fault)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := runFake(ctx, options)
			if err == nil || strings.Contains(err.Error(), "private daemon") {
				t.Fatalf("error=%v", err)
			}
			d.mu.Lock()
			started := d.started
			d.mu.Unlock()
			if started != test.started {
				t.Fatalf("started=%v", started)
			}
			_, statErr := os.Stat(journalPath(options))
			retained := statErr == nil
			if retained != test.retained || test.retained && result.Removed {
				t.Fatalf("result=%+v journal=%v", result, statErr)
			}
		})
	}
}

func TestRecoverOnlyRecordedOwner(t *testing.T) {
	d, options := fakeDaemon(t, "cleanup error")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := runFake(ctx, options); !errors.Is(err, ErrCustody) {
		t.Fatalf("error=%v", err)
	}
	d.mu.Lock()
	d.fault = ""
	d.mu.Unlock()
	result, err := recoverContainer(ctx, options)
	if err != nil || !result.Removed || result.ContainerID != testContainer {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestStreamRefusesHostileFraming(t *testing.T) {
	for _, test := range []struct {
		name   string
		header []byte
	}{
		{"partial", []byte{1}}, {"unknown stream", []byte{9, 0, 0, 0, 0, 0, 0, 1}}, {"reserved bits", []byte{1, 1, 0, 0, 0, 0, 0, 1}},
		{"oversized", []byte{1, 0, 0, 0, 255, 255, 255, 255}}, {"empty", []byte{1, 0, 0, 0, 0, 0, 0, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if readWire(bytes.NewReader(test.header)).err == nil {
				t.Fatal("accepted malformed stream")
			}
		})
	}
}

func TestOptionsAndPlatformRefusal(t *testing.T) {
	_, options := fakeDaemon(t, "")
	for _, mutate := range []func(*Options){func(o *Options) { o.Socket = "relative" }, func(o *Options) { o.ImageID = "latest" }, func(o *Options) { o.Inputs += "/../inputs" }} {
		candidate := options
		mutate(&candidate)
		if _, err := newClient(candidate); err == nil {
			t.Fatalf("accepted %s", fmt.Sprint(candidate))
		}
	}
	if ValidateWorker() == nil || Supervisor() != 125 {
		t.Fatal("host process entered worker boundary")
	}
}

func TestRecoveryRefusesChangedOwnerAndResolvesLostCreate(t *testing.T) {
	for _, test := range []string{"lost-create-response", "daemon", "image", "scratch", "schema"} {
		t.Run(test, func(t *testing.T) {
			d, o := fakeDaemon(t, "cleanup error")
			if _, err := runFake(context.Background(), o); !errors.Is(err, ErrCustody) {
				t.Fatal(err)
			}
			before, err := os.ReadFile(journalPath(o))
			if err != nil {
				t.Fatal(err)
			}
			var owner journal
			if err = json.Unmarshal(before, &owner); err != nil {
				t.Fatal(err)
			}
			switch test {
			case "lost-create-response":
				owner.ContainerID = ""
			case "daemon":
				owner.DaemonID = "different"
			case "image":
				owner.ImageID = "sha256:" + strings.Repeat("c", 64)
			case "scratch":
				owner.Scratch.DeviceMinor++
			case "schema":
				owner.Schema = "legacy"
			}
			raw, _ := json.Marshal(owner)
			if err = os.WriteFile(journalPath(o), raw, 0600); err != nil {
				t.Fatal(err)
			}
			d.mu.Lock()
			d.fault = ""
			d.mu.Unlock()
			result, err := recoverContainer(context.Background(), o)
			if test == "lost-create-response" {
				if err != nil || !result.Removed {
					t.Fatal(result, err)
				}
				return
			}
			if !errors.Is(err, ErrCustody) || result.Removed {
				t.Fatal("wrong owner cleaned", result, err)
			}
			after, _ := os.ReadFile(journalPath(o))
			if !bytes.Equal(raw, after) {
				t.Fatal("refusal changed custody journal")
			}
		})
	}
}

func TestPublicAPIRefusesInvalidScratchAndNonLinux(t *testing.T) {
	_, o := fakeDaemon(t, "")
	for _, call := range []func(context.Context, Options, ScratchAuthority) (Result, error){Run, Recover} {
		if _, err := call(context.Background(), o, ScratchAuthority{}); !errors.Is(err, ErrRefused) {
			t.Fatal(err)
		}
		if runtime.GOOS != "linux" {
			if _, err := call(context.Background(), o, testScratchAuthority()); !errors.Is(err, ErrRefused) {
				t.Fatal("nonLinux dispatch", err)
			}
		}
	}
	//nolint:staticcheck // Deliberately exercise invalid API input before any daemon operation.
	if _, err := runFake(nil, o); !errors.Is(err, ErrRefused) {
		t.Fatal("nil context", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runFake(ctx, o); err == nil {
		t.Fatal("canceled dispatch")
	}
}

func TestPendingJournalCrashRecovery(t *testing.T) {
	for _, scenario := range []string{"pending", "identical", "absent", "conflict", "malformed", "missing-fields", "cleanup-failure", "pending-removed-crash"} {
		t.Run(scenario, func(t *testing.T) {
			d, o := fakeDaemon(t, "cleanup error")
			if _, err := runFake(context.Background(), o); !errors.Is(err, ErrCustody) {
				t.Fatal(err)
			}
			full, err := readJournal(o)
			if err != nil {
				t.Fatal(err)
			}
			main := full
			if scenario != "identical" {
				main.ContainerID = ""
			}
			mainRaw, _ := json.Marshal(main)
			if err = os.WriteFile(journalPath(o), mainRaw, 0600); err != nil {
				t.Fatal(err)
			}
			next := full
			if scenario == "conflict" {
				next.Name = "phebs-typed-index-" + strings.Repeat("a", 32)
			}
			nextRaw, _ := json.Marshal(next)
			if scenario == "malformed" {
				nextRaw = []byte(`{"Schema":`)
			}
			if scenario == "missing-fields" {
				nextRaw = []byte(`{}`)
			}
			pending := journalPath(o) + ".next"
			if err = os.WriteFile(pending, nextRaw, 0600); err != nil {
				t.Fatal(err)
			}
			d.mu.Lock()
			d.inspections = nil
			if scenario != "cleanup-failure" {
				d.fault = ""
			}
			if scenario == "absent" || scenario == "pending-removed-crash" {
				d.removed = true
			}
			d.mu.Unlock()
			if scenario == "pending-removed-crash" {
				if err = os.Remove(pending); err != nil {
					t.Fatal(err)
				}
				if err = syncParent(pending); err != nil {
					t.Fatal(err)
				}
			}
			result, err := recoverContainer(context.Background(), o)
			refused := scenario == "conflict" || scenario == "malformed" || scenario == "missing-fields" || scenario == "cleanup-failure"
			if refused {
				if !errors.Is(err, ErrCustody) || result.Removed {
					t.Fatal("pending ambiguity lost custody", result, err)
				}
				for name, want := range map[string][]byte{journalPath(o): mainRaw, pending: nextRaw} {
					got, e := os.ReadFile(name)
					if e != nil || !bytes.Equal(got, want) {
						t.Fatal("refusal mutated journal", name, e)
					}
				}
				d.mu.Lock()
				defer d.mu.Unlock()
				if scenario != "cleanup-failure" && len(d.inspections) != 0 {
					t.Fatal("ambiguous pending reached daemon", d.inspections)
				}
				return
			}
			if err != nil || !result.Removed {
				t.Fatal(result, err)
			}
			for _, name := range []string{journalPath(o), pending} {
				if _, e := os.Lstat(name); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("journal residue", name, e)
				}
			}
			d.mu.Lock()
			if scenario != "pending-removed-crash" && (len(d.inspections) == 0 || d.inspections[0] != "/containers/"+testContainer+"/json") {
				t.Error("pending ID not used for exact inspection", d.inspections)
			}
			d.created = false
			d.removed = false
			d.started = false
			d.start = make(chan struct{})
			d.mu.Unlock()
			if result, err = runFake(context.Background(), o); err != nil || !result.Removed {
				t.Fatal("fresh run after recovery", result, err)
			}
		})
	}
}

func TestInitialJournalRefusesUnownedPending(t *testing.T) {
	d, o := fakeDaemon(t, "")
	if err := os.WriteFile(journalPath(o)+".next", []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := runFake(context.Background(), o); !errors.Is(err, ErrCustody) || result.Removed {
		t.Fatal(result, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.created {
		t.Fatal("created container with unowned pending journal")
	}
	if _, err := os.Lstat(journalPath(o)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created main despite pending", err)
	}
}
