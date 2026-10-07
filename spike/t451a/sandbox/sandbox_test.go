package sandbox

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
	"reflect"
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
}

func fakeDaemon(t *testing.T, fault string) (*daemon, Options) {
	t.Helper()
	// Unix socket paths are shorter than the testing package's per-case paths.
	root, err := os.MkdirTemp("/private/tmp", "t451a-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	inputs := filepath.Join(root, "inputs")
	if err = os.Mkdir(inputs, 0o700); err != nil {
		t.Fatal(err)
	}
	options := Options{Socket: filepath.Join(root, "docker.sock"), ImageID: testImage, Inputs: inputs}
	d := &daemon{options: options, fault: fault, start: make(chan struct{})}
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
		if d.fault == "modern apparmor" {
			security[1] = "name=apparmor,profile=default"
		}
		if d.fault == "unknown apparmor profile" {
			security[1] = "name=apparmor,profile=unconfined"
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
		arch := runtime.GOARCH
		if d.fault == "wrong image architecture" {
			if arch == "amd64" {
				arch = "arm64"
			} else {
				arch = "amd64"
			}
		}
		write(200, map[string]any{"Id": testImage, "Os": "linux", "Architecture": arch, "Config": imageConfig})
	case path == "/containers/create":
		if json.NewDecoder(r.Body).Decode(&d.config) != nil {
			write(400, nil)
			return
		}
		d.name = r.URL.Query().Get("name")
		d.created = true
		if d.fault == "changed effective configuration" {
			d.config.HostConfig.Privileged = true
		}
		if d.fault == "crossed scratch cap" {
			native := d.config.Cmd[0] == supervisorCommand(true)
			d.config.HostConfig.Tmpfs["/scratch"] = recipe(Options{nativeT451b: !native}, "").HostConfig.Tmpfs["/scratch"]
		}
		if d.fault == "shared memory inode cap" {
			d.config.HostConfig.Tmpfs["/dev/shm"] = "rw,noexec,nosuid,nodev,size=16777216,nr_inodes=2097152,mode=1777"
		}
		if d.fault == "crossed supervisor command" {
			d.config.Cmd = []string{supervisorCommand(d.config.Cmd[0] != supervisorCommand(true))}
		}
		write(201, map[string]any{"Id": testContainer})
	case strings.HasSuffix(path, "/json"):
		if !d.created || d.removed {
			write(404, nil)
			return
		}
		got := inspection{ID: testContainer, Image: testImage, Name: "/" + d.name, Config: wantWithoutHost(d.config), HostConfig: d.config.HostConfig, AppArmorProfile: "docker-default"}
		got.Mounts = append(got.Mounts, struct {
			Type, Source, Destination string
			RW                        bool
		}{"bind", d.options.Inputs, "/inputs", false})
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
		if d.fault == "truncated stream" {
			_, _ = w.Write([]byte{1, 0, 0})
			return
		}
		native := d.config.Cmd[0] == supervisorCommand(true)
		report := supervisorReport{Schema: reportSchema(native), Stdout: []byte("fixture result"), Complete: true, Resources: Resources{LimitsVerified: true, Samples: 1}}
		if d.fault == "crossed supervisor report" {
			report.Schema = reportSchema(!native)
		}
		if d.fault == "unknown supervisor report" {
			report.Schema = "unknown-profile"
		}
		if d.fault == "incomplete report" {
			report.Complete = false
		}
		raw, _ := json.Marshal(report)
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
		write(200, map[string]int{"StatusCode": 0})
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
	result, err := Run(ctx, options)
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

func TestHostImagePreflight(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wantOK bool
	}{
		{"", true},
		{"modern apparmor", true},
		{"unknown apparmor profile", false},
		{"wrong image architecture", false},
		{"missing seccomp", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, options := fakeDaemon(t, tc.name)
			c, err := newClient(options)
			if err != nil {
				t.Fatal(err)
			}
			defer c.http.CloseIdleConnections()
			_, err = c.preflight(context.Background(), options)
			if (err == nil) != tc.wantOK {
				t.Fatalf("preflight accepted=%v, want %v: %v", err == nil, tc.wantOK, err)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.created || d.started {
				t.Fatal("preflight created or started a container")
			}
		})
	}
}

func TestRunRefusesWithoutLosingCustody(t *testing.T) {
	for _, test := range []struct {
		fault             string
		started, retained bool
	}{
		{"missing seccomp", false, false}, {"unknown apparmor profile", false, false}, {"wrong image architecture", false, false},
		{"oversized info", false, false}, {"image environment", false, false}, {"image volume", false, false},
		{"changed effective configuration", false, true}, {"start error", false, false}, {"truncated stream", true, false},
		{"shared memory inode cap", false, true},
		{"incomplete report", true, false}, {"cleanup error", true, true},
	} {
		t.Run(test.fault, func(t *testing.T) {
			d, options := fakeDaemon(t, test.fault)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := Run(ctx, options)
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
	if _, err := Run(ctx, options); !errors.Is(err, ErrCustody) {
		t.Fatalf("error=%v", err)
	}
	d.mu.Lock()
	d.fault = ""
	d.mu.Unlock()
	result, err := Recover(ctx, options)
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
	if ValidateWorker() == nil || Supervisor() != 125 || ValidateNativeT451bWorker() == nil || SupervisorNativeT451b() != 125 {
		t.Fatal("host process entered worker boundary")
	}
}

func TestNativeScratchProfiles(t *testing.T) {
	if ScratchInodes != 65536 || NativeT451bScratchInodes != 262144 {
		t.Fatal("approved inode caps changed")
	}
	old := recipe(Options{}, "owner")
	native := recipe(Options{nativeT451b: true}, "owner")
	if old.Cmd[0] != "__supervisor" || native.Cmd[0] != "__native_supervisor" || workerCommand(false) != "__worker" || workerCommand(true) != "__native_worker" || old.HostConfig.Tmpfs["/scratch"] != "rw,exec,nosuid,nodev,size=2130706432,nr_inodes=65536,mode=1777" || native.HostConfig.Tmpfs["/scratch"] != "rw,exec,nosuid,nodev,size=2130706432,nr_inodes=262144,mode=1777" {
		t.Fatal("closed recipe or dispatch changed")
	}
	native.Cmd, native.HostConfig.Tmpfs = old.Cmd, old.HostConfig.Tmpfs
	if !reflect.DeepEqual(old, native) {
		t.Fatal("native profile changed more than inode cap and fixed dispatch")
	}
	if ownerSchema(false) != "t451a-container-owner-v1" || reportSchema(false) != "t451a-supervisor-v1" || ownerSchema(true) != "t451b-native-container-owner-v1" || reportSchema(true) != "t451b-native-supervisor-v1" {
		t.Fatal("profile journal/report identity changed")
	}
	for _, tc := range []struct {
		actual      uint64
		old, native bool
	}{{65535, true, false}, {65536, true, false}, {65537, false, false}, {262143, false, false}, {262144, false, true}, {262145, false, false}, {^uint64(0), false, false}} {
		if scratchInodesMatch(false, tc.actual) != tc.old || scratchInodesMatch(true, tc.actual) != tc.native {
			t.Fatalf("crossed actual statfs cap %d", tc.actual)
		}
	}
	for _, native := range []bool{false, true} {
		for _, fault := range []string{"", "crossed scratch cap", "crossed supervisor command", "crossed supervisor report", "unknown supervisor report"} {
			t.Run(fmt.Sprintf("native=%t/%s", native, fault), func(t *testing.T) {
				d, options := fakeDaemon(t, fault)
				// Even an internally preselected option cannot change Run's old
				// default; only the separately named API chooses the native cap.
				options.nativeT451b = true
				run := Run
				if native {
					run = RunNativeT451b
				}
				result, err := run(context.Background(), options)
				if fault == "" {
					if err != nil || !result.Removed {
						t.Fatal(result, err)
					}
					d.mu.Lock()
					defer d.mu.Unlock()
					if !reflect.DeepEqual(d.config, recipe(Options{Socket: options.Socket, Inputs: options.Inputs, ImageID: options.ImageID, nativeT451b: native}, d.name)) {
						t.Fatal("entrypoint selected the other recipe")
					}
				} else {
					configuration := fault == "crossed scratch cap" || fault == "crossed supervisor command"
					if err == nil || result.Removed == configuration {
						t.Fatal("profile mismatch lost refusal or custody", result, err)
					}
				}
			})
		}
	}
}

func TestNativeScratchRecovery(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			d, options := fakeDaemon(t, "cleanup error")
			run, recoverRight, recoverWrong := Run, Recover, RecoverNativeT451b
			if native {
				run, recoverRight, recoverWrong = RunNativeT451b, RecoverNativeT451b, Recover
			}
			if _, err := run(context.Background(), options); !errors.Is(err, ErrCustody) {
				t.Fatal(err)
			}
			before, err := os.ReadFile(journalPath(options))
			if err != nil {
				t.Fatal(err)
			}
			d.mu.Lock()
			d.fault = ""
			d.mu.Unlock()
			if result, err := recoverWrong(context.Background(), options); !errors.Is(err, ErrCustody) || result.Removed {
				t.Fatal("crossed journal recovered", result, err)
			}
			after, _ := os.ReadFile(journalPath(options))
			if !bytes.Equal(before, after) {
				t.Fatal("mismatched recovery changed journal")
			}
			var owner journal
			if err = json.Unmarshal(before, &owner); err != nil || owner.Schema != ownerSchema(native) {
				t.Fatal("wrong durable owner profile", err)
			}
			owner.Schema = "unknown-profile"
			unknown, _ := json.Marshal(owner)
			if err = os.WriteFile(journalPath(options), unknown, 0600); err != nil {
				t.Fatal(err)
			}
			if result, err := recoverRight(context.Background(), options); !errors.Is(err, ErrCustody) || result.Removed {
				t.Fatal("unknown journal recovered", result, err)
			}
			if err = os.WriteFile(journalPath(options), before, 0600); err != nil {
				t.Fatal(err)
			}
			if result, err := recoverRight(context.Background(), options); err != nil || !result.Removed {
				t.Fatal("matching recovery failed", result, err)
			}
		})
	}
}
