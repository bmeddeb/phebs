package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
)

// Subprocess calls the real main boundary with actual role arguments, rather
// than replacing security checks or operations with successful callbacks.
func TestTypedCommandProcess(t *testing.T) {
	if os.Getenv("PHEBS_TYPED_COMMAND_TEST") != "1" {
		return
	}
	split := 0
	for i, arg := range os.Args {
		if arg == "--" {
			split = i
			break
		}
	}
	if split == 0 || split+1 == len(os.Args) {
		os.Exit(99)
	}
	os.Args = os.Args[split+1:]
	main()
	os.Exit(0)
}

func typedCommandProcess(t *testing.T, dir, selector string, argv ...string) ([]byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestTypedCommandProcess$", "--"}, argv...)...)
	c.Dir = dir
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, dispatchadmission.ProductionEnvironment+"=") && !strings.HasPrefix(e, "PHEBS_TYPED_COMMAND_TEST=") {
			c.Env = append(c.Env, e)
		}
	}
	c.Env = append(c.Env, "PHEBS_TYPED_COMMAND_TEST=1")
	if selector != "absent" {
		c.Env = append(c.Env, dispatchadmission.ProductionEnvironment+"="+selector)
	}
	b, err := c.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("command hung: %v", ctx.Err())
	}
	if err == nil {
		return b, 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return b, exit.ExitCode()
}

func TestTypedCommandRefusals(t *testing.T) {
	cases := [][]string{
		{"phebs", "__typed_supervisor"},
		{"phebs", "__typed_supervisor", "plan", "bad", "{}", "bad"},
		{"phebs", "__typed_worker"},
		{"phebs", "__typed_worker", "plan", "bad", "{}", "bad"},
		{provider.NativeAdapterPath},
		{"phebs", "__native_bazel"},
		{"phebs", "__native_bazel", "arbitrary", "query"},
		{"phebs", "__plan_helper"},
		{"phebs", "__plan_helper", "/private-input", "out.phebs-plan.json"},
	}
	for _, argv := range cases {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			b, code := typedCommandProcess(t, "", "absent", argv...)
			want := ""
			if len(argv) == 2 && argv[1] == "__typed_supervisor" {
				want = "phebs_site=argv\n"
			}
			if code != 125 || string(b) != want {
				t.Fatalf("refusal: exit=%d output=%q", code, b)
			}
		})
	}
}

func TestTypedCommandProductionSelector(t *testing.T) {
	for _, selector := range []string{"", dispatchadmission.ProductionSelector, "unknown"} {
		for _, argv := range [][]string{
			{"phebs", "__typed_supervisor", "plan", "bad", "{}", "bad"},
			{"phebs", "__typed_worker", "plan", "bad", "{}", "bad"},
			{provider.NativeAdapterPath},
			{"phebs", "__native_bazel", "load", "query"},
			{"phebs", "__plan_helper", "in.json", "out.phebs-plan.json"},
		} {
			b, code := typedCommandProcess(t, "", selector, argv...)
			if code != 125 || string(b) != "phebs_site=selector\n" {
				t.Fatalf("selector %q: exit=%d output=%q", selector, code, b)
			}
		}
	}
}

func TestTypedCommandOrdinary(t *testing.T) {
	b, code := typedCommandProcess(t, "", "absent", "phebs", "version")
	if code != 0 || !bytes.Contains(b, []byte(version)) {
		t.Fatalf("version: %d %q", code, b)
	}
	b, code = typedCommandProcess(t, "", "absent", "phebs", "__typed_unknown")
	if code != 2 || !bytes.Contains(b, []byte("usage:")) {
		t.Fatalf("unknown: %d %q", code, b)
	}
}

func TestTypedCommandPlanner(t *testing.T) {
	dir := t.TempDir()
	// No target source or tool: a declared forwarding projection is enough to
	// prove the real copied-helper argv reaches the promoted implementation.
	raw := []byte(`{"version":"phebs-t451a-projection-input-v1","owner":"//alias:lib","forward":{"owner":"//lib:lib","export":"bazel-out/lib.a"}}`)
	if err := os.WriteFile(filepath.Join(dir, "in.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	b, code := typedCommandProcess(t, dir, "absent", "bazel-out/phebs_plan/t451a", "__plan_helper", "in.json", "out.phebs-plan.json")
	eligible := runtime.GOOS == "linux" && runtime.GOARCH == "arm64" && os.Getuid() == 65534 && os.Getgid() == 65534
	if !eligible {
		if code != 125 || len(b) != 0 {
			t.Fatalf("host planner refusal: %d %q", code, b)
		}
		if _, err := os.Stat(filepath.Join(dir, "out.phebs-plan.json")); !os.IsNotExist(err) {
			t.Fatalf("refused planner wrote output: %v", err)
		}
		return
	}
	if code != 0 || len(b) != 0 {
		t.Fatalf("neutral planner: %d %q", code, b)
	}
	output, err := os.ReadFile(filepath.Join(dir, "out.phebs-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Version string `json:"version"`
		Owner   string `json:"owner"`
		Forward struct {
			Owner  string `json:"owner"`
			Export string `json:"export"`
		} `json:"forward"`
	}
	if err = json.Unmarshal(output, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Version != "phebs-t451a-projection-v1" || projection.Owner != "@@//alias:lib" || projection.Forward.Owner != "@@//lib:lib" || projection.Forward.Export != "bazel-out/lib.a" {
		t.Fatalf("wrong projection: %s", output)
	}
	t.Log("actual unprivileged Linux planner command passed")
}
