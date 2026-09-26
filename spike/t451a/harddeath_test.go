package t451a

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
)

type deathOwner struct{ Schema, Name, ContainerID, DaemonID, ImageID, Socket, Inputs string }
type deathInspection struct {
	ID          string `json:"Id"`
	Name, Image string
	Config      struct{ Labels map[string]string }
	State       struct {
		Running, Restarting, Paused, Dead bool
		Pid, ExitCode                     int
	}
}
type deathTop struct {
	Titles    []string
	Processes [][]string
}

// This gate intentionally kills its host controller after independently
// witnessing a live session-detached child. Only the container PID 1 watchdog
// can satisfy the subsequent five-minute termination predicate.
func TestNativeControllerDeath(t *testing.T) {
	if *nativeSocket == "" {
		t.Skip("native gate requires explicit socket, image and private parent")
	}
	if *nativeParent == "" || !validDigest(*nativeImage) {
		t.Fatal("missing closed native gate inputs")
	}
	root, err := os.MkdirTemp(*nativeParent, "native-harddeath-")
	if err != nil {
		t.Fatal(err)
	}
	host, helper := filepath.Join(root, "t451a-host"), filepath.Join(root, "t451a-linux")
	t.Cleanup(func() {
		for _, name := range []string{host, helper} {
			if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Error(err)
			}
		}
		if err := os.Remove(root); err != nil {
			t.Log("hard-death private custody retained:", root)
		}
	})
	for _, build := range []struct{ path, system, architecture string }{{host, runtime.GOOS, runtime.GOARCH}, {helper, "linux", "arm64"}} {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", build.path, "./cmd/t451a")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GOOS=") && !strings.HasPrefix(entry, "GOARCH=") && !strings.HasPrefix(entry, "CGO_ENABLED=") {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, "GOOS="+build.system, "GOARCH="+build.architecture, "CGO_ENABLED=0")
		err := command.Run()
		cancel()
		if err != nil {
			t.Fatalf("build private gate image: %v", err)
		}
	}
	bytes, err := readBounded(helper, MaxFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), sandbox.WallLimit+45*time.Second)
	defer cancel()
	client := deathClient(*nativeSocket)
	defer client.CloseIdleConnections()
	command := exec.CommandContext(ctx, host, "probe", "--socket", *nativeSocket, "--image", *nativeImage,
		"--helper", helper, "--helper-sha256", Digest(bytes), "--parent", root, "--probe", "detached")
	// Discard untrusted output: the independent daemon observations are this
	// gate's witness, never a marker printed by the process about to be killed.
	command.Stdout, command.Stderr = io.Discard, io.Discard
	started := time.Now()
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	joined := false
	defer func() {
		if !joined {
			_ = command.Process.Kill()
			<-wait
		}
	}()
	var owner deathOwner
	var witnessed deathInspection
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		candidate, ready, ownerErr := deathJournal(root, *nativeSocket, *nativeImage)
		if ownerErr != nil {
			t.Fatal(ownerErr)
		}
		if ready {
			var inspected deathInspection
			if deathGet(ctx, client, "/containers/"+candidate.ContainerID+"/json", &inspected) == nil && deathIdentity(inspected, candidate) &&
				inspected.State.Running && inspected.State.Pid > 0 && !inspected.State.Restarting && !inspected.State.Paused {
				var top deathTop
				if deathGet(ctx, client, "/containers/"+candidate.ContainerID+"/top?ps_args="+url.QueryEscape("-eo pid,ppid,sid,args"), &top) == nil && deathDetached(top, inspected.State.Pid) {
					owner, witnessed = candidate, inspected
					break
				}
			}
		}
		select {
		case err = <-wait:
			joined = true
			t.Fatalf("controller stopped before independent detached-child witness: %v", err)
		case <-ctx.Done():
			t.Fatal("hard-death admission deadline")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if owner.ContainerID == "" {
		t.Fatal("no exact live detached-child witness; custody retained")
	}
	runParent := filepath.Dir(owner.Inputs)
	parentInfo, err := os.Lstat(runParent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("private run identity unavailable")
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = <-wait
	joined = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal("controller did not exit after SIGKILL")
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("controller termination was not SIGKILL")
	}
	// No cleanup call occurs until the watchdog has independently stopped PID1.
	stopped := false
	deadline = started.Add(sandbox.WallLimit + 15*time.Second)
	for time.Now().Before(deadline) {
		var inspected deathInspection
		if err = deathGet(ctx, client, "/containers/"+owner.ContainerID+"/json", &inspected); err != nil {
			t.Fatal("container observation unavailable; custody retained")
		}
		if !deathIdentity(inspected, owner) || inspected.State.Restarting || inspected.State.Paused || inspected.State.Dead {
			t.Fatal("container identity/state changed; custody retained")
		}
		if !inspected.State.Running {
			if inspected.State.Pid != 0 || inspected.State.ExitCode != 124 {
				t.Fatal("container stopped without the fixed watchdog exit")
			}
			stopped = true
			break
		}
		if inspected.State.Pid != witnessed.State.Pid {
			t.Fatal("container PID changed; custody retained")
		}
		select {
		case <-ctx.Done():
			t.Fatal("watchdog observation deadline; custody retained")
		case <-time.After(time.Second):
		}
	}
	if !stopped {
		t.Fatal("independent watchdog failed to stop the container; custody retained")
	}
	recovered, err := sandbox.Recover(ctx, sandbox.Options{Socket: *nativeSocket, ImageID: *nativeImage, Inputs: owner.Inputs})
	if err != nil || !recovered.Removed || recovered.ContainerID != owner.ContainerID {
		t.Fatalf("exact-owner recovery failed: %v", err)
	}
	after, err := os.Lstat(runParent)
	if err != nil || !os.SameFile(parentInfo, after) {
		t.Fatal("removed-container input custody changed")
	}
	if err = os.RemoveAll(runParent); err != nil {
		t.Fatal(err)
	}
	t.Logf(`{"controller_sigkill":true,"detached_child_witnessed":true,"watchdog_exit_code":124,"container_removed":true,"inputs_removed":true,"elapsed_milliseconds":%d}`, time.Since(started).Milliseconds())
}

func deathClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("daemon redirect refused") }}
}

func deathGet(ctx context.Context, client *http.Client, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", "http://docker/v1.47"+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("daemon observation unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<10+1))
	if err != nil || response.StatusCode != 200 || len(data) > 32<<10 {
		return errors.New("daemon observation refused")
	}
	if json.Unmarshal(data, out) != nil {
		return errors.New("daemon observation encoding refused")
	}
	return nil
}

func deathJournal(root, socket, image string) (deathOwner, bool, error) {
	var owner deathOwner
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > 8 {
		return owner, false, errors.New("private run inventory unavailable")
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "t451a-run-") {
			continue
		}
		if !entry.IsDir() || owner.Inputs != "" {
			return deathOwner{}, false, errors.New("private run inventory ambiguous")
		}
		inputs := filepath.Join(root, entry.Name(), "inputs")
		data, err := readBounded(inputs+".t451a-container.json", 8192)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return deathOwner{}, false, errors.New("private journal unavailable")
		}
		if json.Unmarshal(data, &owner) != nil {
			return deathOwner{}, false, nil
		}
		if owner.Schema != "t451a-container-owner-v1" || owner.Socket != socket || owner.ImageID != image || owner.Inputs != inputs || owner.DaemonID == "" ||
			!strings.HasPrefix(owner.Name, "phebs-t451a-") {
			return deathOwner{}, false, errors.New("private journal binding refused")
		}
		if owner.ContainerID == "" {
			return owner, false, nil
		}
		if len(owner.ContainerID) != 64 {
			return deathOwner{}, false, errors.New("private container ID refused")
		}
		if _, err := hex.DecodeString(owner.ContainerID); err != nil {
			return deathOwner{}, false, errors.New("private container ID refused")
		}
	}
	return owner, owner.ContainerID != "", nil
}

func deathIdentity(got deathInspection, owner deathOwner) bool {
	return got.ID == owner.ContainerID && got.Name == "/"+owner.Name && got.Image == owner.ImageID && got.Config.Labels["phebs.t451a.owner"] == owner.Name
}

func deathDetached(top deathTop, rootPID int) bool {
	if len(top.Titles) != 4 || len(top.Processes) < 3 || len(top.Processes) > sandbox.TaskLimit {
		return false
	}
	if top.Titles[0] != "PID" || top.Titles[1] != "PPID" || top.Titles[2] != "SID" || top.Titles[3] != "COMMAND" {
		return false
	}
	type process struct{ pid, parent, session int }
	var supervisor, worker, child process
	seen := map[int]bool{}
	for _, row := range top.Processes {
		if len(row) != 4 {
			return false
		}
		values := [3]int{}
		for i := range values {
			value, err := strconv.Atoi(row[i])
			if err != nil || value < 0 || i != 1 && value == 0 {
				return false
			}
			values[i] = value
		}
		if seen[values[0]] {
			return false
		}
		seen[values[0]] = true
		value := process{values[0], values[1], values[2]}
		switch row[3] {
		case "/inputs/t451a __supervisor":
			if supervisor.pid != 0 {
				return false
			}
			supervisor = value
		case "/inputs/t451a __worker":
			if worker.pid != 0 {
				return false
			}
			worker = value
		case "/inputs/t451a __probe_child":
			if child.pid != 0 {
				return false
			}
			child = value
		default:
			return false
		}
	}
	return supervisor.pid == rootPID && worker.parent == supervisor.pid && child.parent == worker.pid && child.session == child.pid && child.session != supervisor.session
}

func TestDetachedWitnessRejectsUnsubstantiatedRows(t *testing.T) {
	valid := deathTop{Titles: []string{"PID", "PPID", "SID", "COMMAND"}, Processes: [][]string{{"40", "9", "40", "/inputs/t451a __supervisor"}, {"41", "40", "40", "/inputs/t451a __worker"}, {"42", "41", "42", "/inputs/t451a __probe_child"}}}
	if !deathDetached(valid, 40) {
		t.Fatal("valid detached witness refused")
	}
	for _, test := range []struct {
		name   string
		mutate func(*deathTop)
	}{
		{"wrong session", func(top *deathTop) { top.Processes[2][2] = "40" }},
		{"wrong parent", func(top *deathTop) { top.Processes[2][1] = "40" }},
		{"no child", func(top *deathTop) { top.Processes = top.Processes[:2] }},
		{"unexpected command", func(top *deathTop) { top.Processes[2][3] = "private unrelated process" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var changed deathTop
			if json.Unmarshal(raw, &changed) != nil {
				t.Fatal("fixture")
			}
			test.mutate(&changed)
			if deathDetached(changed, 40) {
				t.Fatal("unsubstantiated witness accepted")
			}
		})
	}
}
