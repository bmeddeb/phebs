//go:build darwin || linux

package dispatchadmission

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func productionTestRecord() ProductionBootstrap {
	environment := []string{"HOME=/private/tmp", "TMPDIR=/private/tmp", "TMP=/private/tmp", "TEMP=/private/tmp", "PATH=/private/tmp", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	gitEnvironment := append(slices.Clone(environment), "GIT_EXEC_PATH=/private/tmp", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR=/dev/null", "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_VALUE_2=/dev/null")
	return ProductionBootstrap{
		Program:  ProgramPhebs,
		Producer: Producer{ID: 1, Binding: [32]byte{5}, Sites: ProductionSites()}, Phase: 1,
		Limits:  Limits{Producers: 1, Sites: 16, Roles: 4, Phases: 2, ActivePerProducer: 8, Attempts: 30, WireBytes: 64 << 10, AckTimeout: time.Second},
		Control: PhaseControlConfig{Phases: []uint32{1, 2}, InitialPhase: 1, MaximumPhases: 2, MaximumWireBytes: 64 << 10, Timeout: 2 * time.Second},
		// These deliberately are generic protocol fixtures, not admitted tools.
		Tools: []ProductionToolBinding{{Role: "git", Path: "/bin/sh", Environment: slices.Clone(gitEnvironment)},
			{Role: "surreal", Path: "/bin/sh", Environment: slices.Clone(environment)},
			{Role: "zoekt-git-index", Path: "/bin/sh", Environment: slices.Clone(gitEnvironment)}},
	}
}

func productionTestConfig(record ProductionBootstrap) Config {
	config := Config{Limits: record.Limits, Producers: []Producer{record.Producer}, Phases: []Phase{
		{ID: 1, Roles: []RoleBudget{{Role: RoleGit, Attempts: 10}, {Role: RoleSurreal, Attempts: 10}, {Role: RoleZoekt, Attempts: 10}, {Role: RoleCompatibility}}},
		{ID: 2, Roles: []RoleBudget{{Role: RoleGit, Attempts: 0}, {Role: RoleSurreal, Attempts: 0}, {Role: RoleZoekt, Attempts: 0}, {Role: RoleCompatibility}}},
	}}
	if record.Program == ProgramCorpusAuthor {
		config.Phases[0].Roles = config.Phases[0].Roles[:1]
		config.Phases[1].Roles = config.Phases[1].Roles[:1]
	}
	return config
}

func TestProductionBootstrapHelper(t *testing.T) {
	if os.Getenv("DISPATCH_PRODUCTION_TEST_HELPER") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	mode := os.Args[len(os.Args)-1]
	var lifetime *ProductionLifetime
	var err error
	if mode == "author" {
		lifetime, err = BootstrapAuthor(ctx)
	} else {
		lifetime, err = BootstrapProduction(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	if lifetime == nil || ProcessContext() != lifetime.client.Context() || ProductionTool("unknown") != "" || ProductionTool("phebs-focused-index") != "" {
		t.Fatal("bootstrap did not bind the exact lifetime")
	}
	if mode == "author" {
		if err := RequireAuthorBootstrap(); err != nil {
			t.Fatal(err)
		}
		handle, err := StartAuthor(ctx, exec.CommandContext(ctx, ProductionTool("git"), "-c", "exit 0"))
		if err != nil {
			t.Fatal(err)
		}
		if err := handle.Wait(); err != nil {
			t.Fatal(err)
		}
		productionHelperFinish(t, ctx, lifetime)
		return
	}
	if mode == "output-overflow" {
		command := exec.CommandContext(ctx, ProductionTool("surreal"), "-c", `i=0; while [ "$i" -lt 4097 ]; do printf x; i=$((i+1)); done; while :; do :; done`)
		output, err := CombinedOutputProduction(ctx, SiteSurrealVersion, command)
		if err == nil || !errors.Is(err, ErrLimit) || len(output) != 4096 || command.ProcessState == nil {
			t.Fatalf("overflow not refused/joined: bytes=%d, err=%v, state=%v", len(output), err, command.ProcessState)
		}
		if lifetime.Close(ctx) == nil {
			t.Fatal("overflow closed successfully")
		}
		return
	}
	command := exec.CommandContext(ctx, ProductionTool("git"), "-c", "exit 0")
	site := SiteGitOutput
	switch mode {
	case "wrong-path":
		command = exec.CommandContext(ctx, "/usr/bin/true")
	case "wrong-argv0":
		command.Args[0] = "git"
	case "extra-files":
		command.ExtraFiles = []*os.File{os.Stdin}
	case "unknown-site":
		site = 999
	case "compatibility":
		site = SiteCompatibilitySandbox
	case "healthy", "check-refused", "zero-budget":
	default:
		t.Fatal("unknown helper mode")
	}
	if mode != "healthy" {
		if _, err := StartProduction(ctx, site, command); err == nil {
			t.Fatal("invalid command started")
		}
		if err := lifetime.Close(ctx); err == nil {
			t.Fatal("failed producer closed successfully")
		}
		return
	}
	var descriptor uintptr
	raw, err := lifetime.client.conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Control(func(fd uintptr) { descriptor = fd }); err != nil {
		t.Fatal(err)
	}
	// Check all socket descriptors currently owned by this process, including
	// the separately adopted phase endpoint; a native exec must inherit none.
	directory, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	entries, readErr := directory.Readdirnames(-1)
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(errors.Join(readErr, closeErr))
	}
	var descriptors []string
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry)
		if err != nil || fd < 3 {
			continue
		}
		info, err := os.Stat("/dev/fd/" + entry)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			descriptors = append(descriptors, entry)
		}
	}
	if len(descriptors) != 2 || !slices.Contains(descriptors, strconv.FormatUint(uint64(descriptor), 10)) {
		t.Fatalf("expected two owned socket descriptors: %v", descriptors)
	}
	script := `test -z "$PHEBS_T422_DISPATCH" && test -z "$DISPATCH_PRODUCTION_TEST_HELPER" && test -z "$DYLD_INSERT_LIBRARIES" || exit 41; for fd do test ! -e "/dev/fd/$fd" || exit 42; done`
	command = exec.CommandContext(ctx, ProductionTool("git"), append([]string{"-c", script, "fd-isolation"}, descriptors...)...)
	command.Env = []string{"DYLD_INSERT_LIBRARIES=untrusted", "PHEBS_T422_DISPATCH=untrusted"}
	if err := RunProduction(ctx, SiteSyncGit, command); err != nil {
		t.Fatal(err)
	}
	version := exec.CommandContext(ctx, ProductionTool("surreal"), "-c", "printf 'native-version\\n'")
	output, err := CombinedOutputProduction(ctx, SiteSurrealVersion, version)
	if err != nil || string(output) != "native-version\n" {
		t.Fatalf("combined output: %q, %v", output, err)
	}
	recovery := exec.CommandContext(ctx, ProductionTool("surreal"), "-c", `test "$SURREAL_USER" = root && test "$SURREAL_PASS" = root`)
	if err := RunProduction(ctx, SiteRecoverySurreal, recovery); err != nil {
		t.Fatal(err)
	}
	productionHelperFinish(t, ctx, lifetime)
}

func productionHelperFinish(t *testing.T, ctx context.Context, lifetime *ProductionLifetime) {
	t.Helper()
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if ProcessContext().Err() == nil || ProductionTool("git") == "git" {
		t.Fatal("closed exact runtime fell back to ordinary execution")
	}
}

func TestProductionBootstrapInheritedBoundary(t *testing.T) {
	for _, mode := range []string{"healthy", "author", "wrong-path", "wrong-argv0", "extra-files", "unknown-site", "compatibility", "check-refused", "zero-budget", "output-overflow"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			record := productionTestRecord()
			if mode == "author" {
				record.Program, record.Producer.Sites, record.Tools = ProgramCorpusAuthor, AuthorSites(), record.Tools[:1]
			}
			config := productionTestConfig(record)
			if mode == "zero-budget" {
				config.Phases[0].Roles[0].Attempts = 0
			}
			controller, err := New(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			controlParent, controlChild, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Close(); _ = child.Close(); _ = controlParent.Close(); _ = controlChild.Close() })
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProductionBootstrapHelper$", "--", mode)
			command.Env = []string{"DISPATCH_PRODUCTION_TEST_HELPER=1", ProductionEnvironment + "=" + ProductionSelector, "GORACE=atexit_sleep_ms=0"}
			command.ExtraFiles = []*os.File{child, controlChild}
			command.WaitDelay = time.Second
			command.Stderr = os.Stderr
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			_ = child.Close()
			_ = controlChild.Close()
			defer func() {
				_ = input.Close()
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			if err := SendProductionBootstrap(ctx, parent, controlParent, record); err != nil {
				t.Fatal(err)
			}
			if _, err := parent.Stat(); err != nil {
				t.Fatal("bootstrap closed borrowed parent")
			}
			control, err := NewPhaseControl(ctx, controlParent, record.Producer.Binding, record.Control)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = control.Close() }()
			var checks atomic.Int32
			server := make(chan error, 1)
			go func() {
				server <- controller.ServeChecked(ctx, 1, command.Process.Pid, parent, func(ctx context.Context, site Site) error {
					// Snapshot acquires c.mu: this would deadlock if the resource
					// check were called under the controller's accounting lock.
					if _, err := controller.Snapshot(); err != nil || ctx.Err() != nil || site.ID == 0 {
						return ErrConfig
					}
					checks.Add(1)
					if mode == "check-refused" {
						return errors.New("private diagnostic must not escape")
					}
					return nil
				})
			}()
			if mode == "healthy" || mode == "author" {
				reader := bufio.NewReader(output)
				ready, err := reader.ReadString('\n')
				if err != nil || ready != "ready\n" {
					rest, _ := io.ReadAll(reader)
					t.Fatalf("helper ready: %q %q, %v", ready, rest, err)
				}
				if err := control.Pause(ctx); err != nil {
					t.Fatal(err)
				}
				if err := controller.Fence(); err != nil {
					t.Fatal(err)
				}
				if err := control.Checkpoint(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := input.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
			}
			if err := command.Wait(); err != nil {
				t.Fatal(err)
			}
			serveErr := <-server
			snapshot, snapshotErr := controller.Snapshot()
			if mode == "healthy" || mode == "author" {
				expected := uint64(3)
				if mode == "author" {
					expected = 1
				}
				if serveErr != nil || snapshotErr != nil || !snapshot.Complete || snapshot.Attempts != expected || uint64(checks.Load()) != expected {
					t.Fatalf("healthy prefix: %+v, %v/%v, checks=%d", snapshot, serveErr, snapshotErr, checks.Load())
				}
			} else {
				expected := uint64(0)
				if mode == "output-overflow" {
					expected = 1
				}
				if serveErr == nil || snapshotErr == nil || snapshot.Complete || snapshot.Attempts != expected ||
					strings.Contains(serveErr.Error(), "private diagnostic") || mode == "zero-budget" && checks.Load() != 0 {
					t.Fatalf("refused prefix: %+v, %v/%v, checks=%d", snapshot, serveErr, snapshotErr, checks.Load())
				}
			}
		})
	}
}

func TestProductionBootstrapRecordRefusals(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ProductionBootstrap)
	}{
		{"unknown-program", func(r *ProductionBootstrap) { r.Program = "unknown" }},
		{"other-program", func(r *ProductionBootstrap) { r.Program = ProgramCorpusAuthor }},
		{"binding", func(r *ProductionBootstrap) { r.Producer.Binding = [32]byte{} }},
		{"site-alias", func(r *ProductionBootstrap) { r.Producer.Sites[0].Role = RoleSurreal }},
		{"missing-site", func(r *ProductionBootstrap) { r.Producer.Sites = r.Producer.Sites[1:] }},
		{"persistent-alias", func(r *ProductionBootstrap) { r.Producer.Sites[0].Persistent = true }},
		{"unbounded-active", func(r *ProductionBootstrap) { r.Limits.ActivePerProducer = 129 }},
		{"unbounded-phases", func(r *ProductionBootstrap) { r.Limits.Phases = 33 }},
		{"unknown-role", func(r *ProductionBootstrap) { r.Tools[0].Role = "sh" }},
		{"missing-tool", func(r *ProductionBootstrap) { r.Tools = r.Tools[1:] }},
		{"relative-path", func(r *ProductionBootstrap) { r.Tools[0].Path = "git" }},
		{"unclean-path", func(r *ProductionBootstrap) { r.Tools[0].Path = "/bin/../bin/git" }},
		{"unknown-environment", func(r *ProductionBootstrap) {
			r.Tools[0].Environment = append(r.Tools[0].Environment, "DYLD_INSERT_LIBRARIES=/private/library")
		}},
		{"duplicate-environment", func(r *ProductionBootstrap) { r.Tools[0].Environment = append(r.Tools[0].Environment, "LANG=C") }},
		{"socket-selector", func(r *ProductionBootstrap) {
			r.Tools[0].Environment = append(r.Tools[0].Environment, ProductionEnvironment+"="+ProductionSelector)
		}},
		{"network-path", func(r *ProductionBootstrap) {
			r.Tools[0].Environment = append(r.Tools[0].Environment, "GIT_ALLOW_PROTOCOL=https")
		}},
		{"control-phase", func(r *ProductionBootstrap) { r.Control.InitialPhase = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := productionTestRecord()
			test.mutate(&record)
			if !errors.Is(record.validate(), ErrProductionBootstrap) {
				t.Fatal("invalid bootstrap accepted")
			}
		})
	}
}

func TestProductionBootstrapMalformedAndCanceled(t *testing.T) {
	for _, mode := range []string{"magic", "oversize", "unknown-field", "duplicate-field", "digest", "other-endpoint", "other-program", "partial-canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			parent, child, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			controlParent, controlChild, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.Close(); _ = child.Close(); _ = controlParent.Close(); _ = controlChild.Close() })
			done := make(chan error, 1)
			go func() { _, err := bootstrapProduction(ctx, child, controlChild); done <- err }()
			record := productionTestRecord()
			if mode == "other-program" {
				record.Program, record.Producer.Sites, record.Tools = ProgramCorpusAuthor, AuthorSites(), record.Tools[:1]
			}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown-field" {
				raw = append([]byte(`{"unknown":1,`), raw[1:]...)
			}
			if mode == "duplicate-field" {
				raw = append([]byte(`{"Phase":1,`), raw[1:]...)
			}
			header := bootstrapHeader(raw, productionTestRecord().Producer.Binding)
			switch mode {
			case "magic":
				header[0] = 'X'
			case "oversize":
				binary.BigEndian.PutUint32(header[4:8], MaximumProductionBootstrapBytes+1)
			case "digest":
				header[71] ^= 1
			}
			if mode == "partial-canceled" {
				_, _ = parent.Write(header[:1])
				cancel()
			} else {
				_, _ = parent.Write(header[:])
				if mode != "magic" && mode != "oversize" {
					_, _ = parent.Write(raw)
				}
				if mode == "other-endpoint" {
					header[8] ^= 1
					_, _ = controlParent.Write(header[:])
				}
			}
			if err := <-done; !errors.Is(err, ErrProductionBootstrap) || productionRuntime.Load() != nil {
				t.Fatalf("invalid bootstrap installed: %v", err)
			}
			if _, err := child.Stat(); err == nil {
				t.Fatal("admission original was retained")
			}
			if _, err := controlChild.Stat(); err == nil {
				t.Fatal("control original was retained")
			}
		})
	}
}

func TestProductionOrdinaryPathAndInvalidSelector(t *testing.T) {
	t.Setenv(ProductionEnvironment, "placeholder")
	if err := os.Unsetenv(ProductionEnvironment); err != nil {
		t.Fatal(err)
	}
	lifetime, err := BootstrapProduction(t.Context())
	if err != nil || lifetime != nil || productionRuntime.Load() != nil || ProcessContext() != context.Background() || ProductionTool("git") != "git" {
		t.Fatal("absent selector changed ordinary runtime")
	}
	if lifetime, err := BootstrapAuthor(t.Context()); lifetime != nil || !errors.Is(err, ErrProductionBootstrap) {
		t.Fatal("author bootstrap allowed an absent selector")
	}
	if RequireAuthorBootstrap() == nil {
		t.Fatal("ordinary process claims author bootstrap")
	}
	commandWithoutAdmission := exec.Command("/usr/bin/true")
	if _, err := StartAuthor(t.Context(), commandWithoutAdmission); err == nil || commandWithoutAdmission.Process != nil {
		t.Fatal("author command fell back to uncounted execution")
	}
	command := exec.CommandContext(t.Context(), "/bin/sh", "-c", `printf '%s' "$ORDINARY_VALUE"`)
	command.Env = []string{"ORDINARY_VALUE=unchanged"}
	output, err := CombinedOutputProduction(t.Context(), 999, command)
	if err != nil || string(output) != "unchanged" {
		t.Fatalf("ordinary output/environment changed: %q, %v", output, err)
	}
	for _, selector := range []string{"", "unknown", ProductionSelector + " "} {
		t.Setenv(ProductionEnvironment, selector)
		if lifetime, err := BootstrapProduction(t.Context()); lifetime != nil || !errors.Is(err, ErrProductionBootstrap) {
			t.Fatal("unknown selector admitted")
		}
	}
}
