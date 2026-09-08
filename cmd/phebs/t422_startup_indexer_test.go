//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/executableidentity"
)

const t422StartupIndexerHelperMode = "PHEBS_T422_STARTUP_INDEXER_TEST"

// t422PinnedZoektVersion is the version this process links, read from its own
// build metadata (or go.mod for a test binary that omits the reader).
func t422PinnedZoektVersion(t *testing.T) string {
	t.Helper()
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range build.Deps {
			if dependency.Path == "github.com/sourcegraph/zoekt" && dependency.Replace == nil {
				return dependency.Version
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "github.com/sourcegraph/zoekt" {
			return fields[1]
		}
	}
	t.Fatal("pinned zoekt version is absent from go.mod")
	return ""
}

// buildT422PrivateIndexImage builds one real executable with exactly the
// admitted V3 private replacement identity: package cmd/zoekt-git-index of the
// pinned zoekt version replaced by ./zoekt, closed settings, this toolchain.
// It is a tiny neutral program, not the instrumented index child.
func buildT422PrivateIndexImage(t *testing.T, ctx context.Context) string {
	t.Helper()
	driver, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.CommandContext(ctx, driver, "version").Output()
	if err != nil || string(version) != "go version "+runtime.Version()+" "+runtime.GOOS+"/"+runtime.GOARCH+"\n" {
		t.Fatalf("PATH Go driver %q is not this test's toolchain %s: %v", strings.TrimSpace(string(version)), runtime.Version(), err)
	}
	goRoot, err := exec.CommandContext(ctx, driver, "env", "GOROOT").Output()
	if err != nil || len(bytes.TrimSpace(goRoot)) == 0 {
		t.Fatal("native GOROOT unavailable", err)
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "tmp", "cache", "modules", "zoekt", "zoekt/cmd", "zoekt/cmd/zoekt-git-index"} {
		if err := os.Mkdir(filepath.Join(workspace, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pinned := t422PinnedZoektVersion(t)
	base := "module github.com/bmeddeb/phebs\n\ngo 1.26.0\n\nrequire github.com/sourcegraph/zoekt " + pinned + "\n"
	for name, value := range map[string]string{
		"go.mod":                            base,
		"v3.mod":                            base + "\nreplace github.com/sourcegraph/zoekt " + pinned + " => ./zoekt\n",
		"zoekt/go.mod":                      "module github.com/sourcegraph/zoekt\n\ngo 1.25.0\n",
		"zoekt/cmd/zoekt-git-index/main.go": "package main\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(workspace, "zoekt-git-index")
	command := exec.CommandContext(ctx, driver, "build", "-trimpath", "-pgo=off", "-buildvcs=false", "-p=1",
		"-modfile=v3.mod", "-o", output, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
	command.Dir = workspace
	command.Env = []string{
		"CGO_ENABLED=0", "GOENV=off", "GOWORK=off", "GOFLAGS=-mod=readonly", "GOTOOLCHAIN=local",
		"GOPROXY=off", "GOSUMDB=off", "GOTELEMETRY=off", "GOROOT=" + string(bytes.TrimSpace(goRoot)),
		"GOMODCACHE=" + filepath.Join(workspace, "modules"), "GOCACHE=" + filepath.Join(workspace, "cache"),
		"GOPATH=" + filepath.Join(workspace, "home", "go"), "HOME=" + filepath.Join(workspace, "home"),
		"TMPDIR=" + filepath.Join(workspace, "tmp"), "PATH=" + filepath.Dir(driver), "LANG=C", "LC_ALL=C",
	}
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("private replacement build: %v: %s", err, raw)
	}
	info, err := buildinfo.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != "github.com/sourcegraph/zoekt/cmd/zoekt-git-index" || info.Main.Path != "github.com/sourcegraph/zoekt" ||
		info.Main.Version != pinned || info.Main.Sum != "" || info.Main.Replace == nil ||
		*info.Main.Replace != (debug.Module{Path: "./zoekt", Version: "(devel)"}) {
		t.Fatalf("actual replacement identity differs: %+v replacement=%+v", info.Main, info.Main.Replace)
	}
	return output
}

// The ordinary server keeps its historical behavior: a private V3 image is
// not admitted outside a selected launch, and serving continues without an
// index child instead of failing.
func TestT422StartupIndexerOrdinaryKeepsServing(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	image := buildT422PrivateIndexImage(t, ctx)
	digest, err := executableidentity.Digest(image)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHEBS_ZOEKT_GIT_INDEX", image)
	t.Setenv("PHEBS_ZOEKT_GIT_INDEX_SHA256", digest)
	if dispatchadmission.ProductionSemanticSelected() {
		t.Fatal("ordinary test process is unexpectedly selected")
	}
	bin, focused, err := admitStartupIndexer(true)
	if err != nil || bin != "" || focused != "" {
		t.Fatalf("ordinary startup changed: bin=%q focused=%q err=%v", bin, focused, err)
	}
}

// Actual inherited DA/PC V3 lifetime around the real startup admission seam.
// The parent-verified private image is admitted; the same image without its
// digest, with a wrong digest, or an executable without Go module identity
// makes the selected startup fail closed before serving.
func TestT422StartupIndexerSelected(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	image := buildT422PrivateIndexImage(t, ctx)
	digest, err := executableidentity.Digest(image)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, binary, digest, want string
	}{
		{"private_admitted", image, digest, "admitted"},
		{"private_without_digest", image, "", "refused"},
		{"private_wrong_digest", image, "sha256:" + strings.Repeat("0", 64), "refused"},
		{"unavailable", "/usr/bin/true", "", "refused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testT422StartupIndexerSelected(t, test.binary, test.digest, test.want)
		})
	}
}

func testT422StartupIndexerSelected(t *testing.T, binary, digest, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	record, _ := t422LifecycleBootstrapRecord(t)
	record.Producer.ID = 5
	config := dispatchadmission.Config{Limits: record.Limits, Producers: []dispatchadmission.Producer{record.Producer}}
	for _, phase := range record.Control.Phases {
		config.Phases = append(config.Phases, dispatchadmission.Phase{ID: phase, Roles: []dispatchadmission.RoleBudget{
			{Role: dispatchadmission.RoleGit}, {Role: dispatchadmission.RoleSurreal}, {Role: dispatchadmission.RoleZoekt}, {Role: dispatchadmission.RoleCompatibility}}})
	}
	controller, err := dispatchadmission.New(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(); _ = child.Close() }()
	controlParent, controlChild, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controlParent.Close(); _ = controlChild.Close() }()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestT422StartupIndexerHelper$")
	command.Env = []string{t422StartupIndexerHelperMode + "=" + want, "PHEBS_ZOEKT_GIT_INDEX=" + binary,
		dispatchadmission.ProductionEnvironment + "=" + dispatchadmission.ProductionSelector, "GORACE=atexit_sleep_ms=0"}
	if digest != "" {
		command.Env = append(command.Env, "PHEBS_ZOEKT_GIT_INDEX_SHA256="+digest)
	}
	command.ExtraFiles = []*os.File{child, controlChild}
	command.WaitDelay = time.Second
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
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
	_ = controlChild.Close()
	if err := dispatchadmission.SendProductionBootstrap(ctx, parent, controlParent, record); err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- controller.Serve(ctx, 5, command.Process.Pid, parent) }()
	defer func() {
		cancel()
		select {
		case <-served:
		default:
		}
	}()
	phase, err := dispatchadmission.NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = phase.Close() }()
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != want {
		t.Fatalf("selected startup admission %q, want %q: %v %s", scanner.Text(), want, scanner.Err(), diagnostic.String())
	}
	// The same fenced completion the other inherited helpers use before their
	// producer-local close; startup admission itself performs no owner turn.
	for _, operation := range []func() error{
		func() error { return phase.DrainOwners(ctx) }, func() error { return phase.Pause(ctx) },
		controller.Fence, func() error { return phase.Checkpoint(ctx) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err, diagnostic.String())
		}
	}
	if _, err := input.Write([]byte{'c'}); err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() || scanner.Text() != "joined" {
		t.Fatalf("selected startup helper did not join: %q %v %s", scanner.Text(), scanner.Err(), diagnostic.String())
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err, diagnostic.String())
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal("admission receiver refused the helper closure", err, diagnostic.String())
		}
	case <-ctx.Done():
		t.Fatal("admission receiver did not join the helper")
	}
	if refused := strings.Contains(diagnostic.String(), "selected launch refused before serving"); refused != (want == "refused") {
		t.Fatalf("private refusal diagnostic=%v for %q: %s", refused, want, diagnostic.String())
	}
	snapshot, err := controller.Snapshot()
	if err != nil || !snapshot.Complete || snapshot.Attempts != 0 || !snapshot.Producers[0].Closed {
		t.Fatalf("startup admission changed the exact empty prefix: %+v, %v", snapshot, err)
	}
}

// The helper is the actual selected process: a real inherited V3 lifetime,
// then the same startup admission serve performs, reported as one word.
func TestT422StartupIndexerHelper(t *testing.T) {
	if os.Getenv(t422StartupIndexerHelperMode) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	lifetime, err := dispatchadmission.BootstrapProduction(ctx)
	if err != nil || lifetime == nil {
		t.Fatal(err)
	}
	if _, err := dispatchadmission.ProductionSemanticState(); err != nil {
		t.Fatal("actual selected V3 lifetime is absent", err)
	}
	owners, err := dispatchadmission.NewProductionOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil || dispatchadmission.BindProductionOwners(owners) != nil {
		t.Fatal("actual owner binding failed", err)
	}
	bin, _, err := admitStartupIndexer(false)
	switch {
	case err == nil && bin != "":
		fmt.Println("admitted")
	case errors.Is(err, errT422StartupIndexer) && bin == "":
		fmt.Println("refused")
	default:
		fmt.Println("unexpected")
	}
	var raw [1]byte
	if _, err := io.ReadFull(os.Stdin, raw[:]); err != nil || raw[0] != 'c' {
		t.Fatal("parent signal", err)
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	fmt.Println("joined")
}
