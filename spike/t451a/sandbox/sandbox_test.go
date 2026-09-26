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
		if d.fault == "changed effective configuration" {
			d.config.HostConfig.Privileged = true
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
		report := supervisorReport{Schema: "t451a-supervisor-v1", Stdout: []byte("fixture result"), Complete: true, Resources: Resources{LimitsVerified: true, Samples: 1}}
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

func TestRunRefusesWithoutLosingCustody(t *testing.T) {
	for _, test := range []struct {
		fault             string
		started, retained bool
	}{
		{"missing seccomp", false, false}, {"oversized info", false, false}, {"image environment", false, false}, {"image volume", false, false},
		{"changed effective configuration", false, true}, {"start error", false, false}, {"truncated stream", true, false},
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
	if ValidateWorker() == nil || Supervisor() != 125 {
		t.Fatal("host process entered worker boundary")
	}
}
