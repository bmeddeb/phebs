package typedsandbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testScratchAuthority() ScratchAuthority {
	return ScratchAuthority{Source: "/var/lib/phebs-typed-index/ordinary-11/scratch", DeviceMajor: 7, DeviceMinor: 2, BlockSize: 4096, Blocks: 1090000, Inodes: 262144, ImageBytes: ScratchBytes / 4096 * 4096}
}

func TestScratchAuthorityAndRecipe(t *testing.T) {
	a := testScratchAuthority()
	raw, err := EncodeScratchAuthority(a)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeScratchAuthority(raw); err != nil || got != a {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*ScratchAuthority){
		func(v *ScratchAuthority) { v.Source += "/../scratch" }, func(v *ScratchAuthority) { v.Source = "/tmp/scratch" },
		func(v *ScratchAuthority) { v.DeviceMajor = 0 }, func(v *ScratchAuthority) { v.BlockSize = 8192 },
		func(v *ScratchAuthority) { v.Blocks = v.ImageBytes/4096 + 1 }, func(v *ScratchAuthority) { v.Inodes++ },
		func(v *ScratchAuthority) { v.ImageBytes += 4096 },
	} {
		bad := a
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	for _, value := range [][]byte{append(raw, []byte("{}")...), []byte(`{"source":"/tmp"}`), append(raw[:len(raw)-1], []byte(`,"unknown":1}`)...)} {
		if _, err := DecodeScratchAuthority(value); err == nil {
			t.Fatal("accepted malformed authority")
		}
	}
	o := Options{Inputs: "/inputs-on-host", Controls: "/controls-plan", Control: testControlIdentity(), ImageID: testImage, scratch: &a}
	c := recipe(o, "owner")
	if c.Cmd[0] != SupervisorCommand || c.HostConfig.Memory != MemoryBytes || c.HostConfig.MemorySwap != MemoryBytes || c.HostConfig.NanoCpus != 2_000_000_000 || c.HostConfig.PidsLimit != TaskLimit || len(c.HostConfig.Tmpfs) != 0 || len(c.HostConfig.Mounts) != 3 || c.HostConfig.Mounts[1].Source != a.Source || c.HostConfig.Mounts[1].ReadOnly {
		t.Fatal("wrong sealed phase2 recipe", c)
	}
	owner := journal{Schema: ownerSchema, Name: "owner", Controls: o.Controls, Control: o.Control, Scratch: &a}
	got := inspection{ID: testContainer, Image: testImage, Name: "/owner", Config: wantWithoutHost(c), HostConfig: c.HostConfig, AppArmorProfile: "docker-default"}
	for _, m := range c.HostConfig.Mounts {
		got.Mounts = append(got.Mounts, struct {
			Type, Source, Destination string
			RW                        bool
		}{m.Type, m.Source, m.Target, !m.ReadOnly})
	}
	if verify(got, o, owner) != nil {
		t.Fatal("valid phase2 inspection refused")
	}
	for _, mutate := range []func(*inspection){func(v *inspection) { v.HostConfig.Memory-- }, func(v *inspection) { v.HostConfig.PidsLimit-- }, func(v *inspection) { v.HostConfig.NanoCpus /= 2 }, func(v *inspection) { v.Mounts[1].Source = "/wrong" }, func(v *inspection) { v.Mounts[1].Type = "tmpfs" }, func(v *inspection) { v.HostConfig.Mounts[1].BindOptions.NonRecursive = false }} {
		copyJSON, _ := json.Marshal(got)
		var bad inspection
		_ = json.Unmarshal(copyJSON, &bad)
		mutate(&bad)
		if verify(bad, o, owner) == nil {
			t.Fatal("changed effective phase2 config accepted")
		}
	}
}

func TestScratchRunAndAuthorityRefusal(t *testing.T) {
	d, o := fakeDaemon(t, "")
	a := testScratchAuthority()
	result, err := run(context.Background(), o)
	if err != nil || !result.Removed {
		t.Fatal(result, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.config.Cmd[0] != SupervisorCommand {
		t.Fatal("wrong dispatch")
	}
	a.DeviceMinor++
	o.scratch = &a
	if _, err := run(context.Background(), o); err == nil {
		t.Fatal("unsealed authority accepted")
	}
}

func TestScratchMountIdentity(t *testing.T) {
	a := testScratchAuthority()
	good := "20 10 7:2 / /scratch rw,nosuid,nodev,relatime - ext4 /dev/loop2 rw\n"
	if verifyScratchMounts(good, a) != nil {
		t.Fatal("valid mount refused")
	}
	for _, bad := range []string{strings.Replace(good, "7:2", "7:3", 1), strings.Replace(good, " / /scratch", " /sub /scratch", 1), strings.Replace(good, "nosuid,", "", 1), strings.Replace(good, "nodev,", "noexec,", 1), strings.Replace(good, " - ext4", " shared:1 - ext4", 1), good + strings.Replace(good, " /scratch ", " /scratch/nested ", 1), good + good} {
		if verifyScratchMounts(bad, a) == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestScratchScratchEmpty(t *testing.T) {
	for _, kind := range []string{"empty", "file", "directory", "missing", "not-directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			switch kind {
			case "file":
				if err := os.WriteFile(filepath.Join(root, "leftover"), []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(filepath.Join(root, "leftover"), 0700); err != nil {
					t.Fatal(err)
				}
			case "missing":
				root = filepath.Join(root, "absent")
			case "not-directory":
				root = filepath.Join(root, "file")
				if err := os.WriteFile(root, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := scratchEmpty(root); got != (kind == "empty") {
				t.Fatalf("empty=%v", got)
			}
		})
	}
}

// Every effective recipe field is pinned, including defaults which Docker may
// otherwise fill from ambient daemon configuration.
func TestEveryEffectiveRecipeFieldIsPinned(t *testing.T) {
	a := testScratchAuthority()
	o := Options{Inputs: "/owned/inputs", Controls: "/owned/controls-plan", Control: testControlIdentity(), ImageID: testImage, scratch: &a}
	c := recipe(o, "owner")
	owner := journal{Schema: ownerSchema, Name: "owner", Controls: o.Controls, Control: o.Control, Scratch: &a}
	good := inspection{ID: testContainer, Image: testImage, Name: "/owner", Config: wantWithoutHost(c), HostConfig: c.HostConfig, AppArmorProfile: "docker-default"}
	for _, m := range c.HostConfig.Mounts {
		good.Mounts = append(good.Mounts, struct {
			Type, Source, Destination string
			RW                        bool
		}{m.Type, m.Source, m.Target, !m.ReadOnly})
	}
	mutations := map[string]func(*inspection){
		"image": func(v *inspection) { v.Image = "other" }, "name": func(v *inspection) { v.Name = "/other" }, "label": func(v *inspection) { v.Config.Labels[ownerLabel] = "other" },
		"entrypoint": func(v *inspection) { v.Config.Entrypoint = []string{"/bin/sh"} }, "selector": func(v *inspection) { v.Config.Cmd = []string{"__worker"} }, "cwd": func(v *inspection) { v.Config.WorkingDir = "/" }, "uid": func(v *inspection) { v.Config.User = "65534" },
		"environment": func(v *inspection) { v.Config.Env = append(v.Config.Env, "LD_PRELOAD=/escape") }, "stdin": func(v *inspection) { v.Config.OpenStdin = true }, "tty": func(v *inspection) { v.Config.Tty = true }, "healthcheck": func(v *inspection) { v.Config.Healthcheck.Test = []string{"CMD", "bad"} },
		"network": func(v *inspection) { v.HostConfig.NetworkMode = "host" }, "ipc": func(v *inspection) { v.HostConfig.IpcMode = "host" }, "pid": func(v *inspection) { v.HostConfig.PidMode = "host" }, "cgroup": func(v *inspection) { v.HostConfig.CgroupnsMode = "host" }, "userns": func(v *inspection) { v.HostConfig.UsernsMode = "host" }, "runtime": func(v *inspection) { v.HostConfig.Runtime = "other" },
		"writable-root": func(v *inspection) { v.HostConfig.ReadonlyRootfs = false }, "privileged": func(v *inspection) { v.HostConfig.Privileged = true }, "capdrop": func(v *inspection) { v.HostConfig.CapDrop = nil }, "capadd": func(v *inspection) { v.HostConfig.CapAdd = append(v.HostConfig.CapAdd, "SYS_ADMIN") }, "security": func(v *inspection) { v.HostConfig.SecurityOpt = nil }, "apparmor": func(v *inspection) { v.AppArmorProfile = "unconfined" }, "groups": func(v *inspection) { v.HostConfig.GroupAdd = []string{"0"} },
		"bind": func(v *inspection) { v.HostConfig.Binds = []string{"/:/escape"} }, "tmpfs": func(v *inspection) { v.HostConfig.Tmpfs = map[string]string{"/scratch": "rw"} }, "input-readonly": func(v *inspection) { v.HostConfig.Mounts[0].ReadOnly = false }, "input-recursive": func(v *inspection) { v.HostConfig.Mounts[0].BindOptions.NonRecursive = false }, "propagation": func(v *inspection) { v.HostConfig.Mounts[1].BindOptions.Propagation = "shared" }, "effective-mount": func(v *inspection) { v.Mounts[0].RW = true },
		"memory": func(v *inspection) { v.HostConfig.Memory++ }, "swap": func(v *inspection) { v.HostConfig.MemorySwap++ }, "cpu": func(v *inspection) { v.HostConfig.NanoCpus++ }, "tasks": func(v *inspection) { v.HostConfig.PidsLimit++ }, "shm": func(v *inspection) { v.HostConfig.ShmSize++ }, "fd": func(v *inspection) { v.HostConfig.Ulimits[0].Hard++ }, "restart": func(v *inspection) { v.HostConfig.RestartPolicy.Name = "always" }, "log": func(v *inspection) { v.HostConfig.LogConfig.Type = "json-file" }, "auto-remove": func(v *inspection) { v.HostConfig.AutoRemove = true }, "init": func(v *inspection) { v.HostConfig.Init = true }, "ports": func(v *inspection) { v.HostConfig.PublishAllPorts = true },
	}
	raw, _ := json.Marshal(good)
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var bad inspection
			if err := json.Unmarshal(raw, &bad); err != nil {
				t.Fatal(err)
			}
			mutate(&bad)
			if verify(bad, o, owner) == nil {
				t.Fatal("effective configuration mutation accepted")
			}
		})
	}
}
