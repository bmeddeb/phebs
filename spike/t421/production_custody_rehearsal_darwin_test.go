//go:build darwin

package t421

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/search"
)

// This selector requires an explicit exact clean candidate and prepopulated
// offline cache. It never runs automatically, downloads modules, edits installed
// tools, or turns a synthetic child into production-readiness evidence.
func TestExecutionProductionOptionalRealServeRehearsal(t *testing.T) {
	if os.Getenv("PHEBS_T422_PRODUCTION_REHEARSAL") != "1" {
		t.Skip("requires explicit serial real Phebs/Zoekt/Surreal admission")
	}
	requireExternalToolFrozenHost(t)
	repository := os.Getenv("PHEBS_T422_PRODUCTION_REPOSITORY")
	commit := os.Getenv("PHEBS_T422_PRODUCTION_COMMIT")
	goRoot := os.Getenv("PHEBS_T422_PRODUCTION_GOROOT")
	moduleCache := os.Getenv("PHEBS_T422_PRODUCTION_MODULE_CACHE")
	if !validCommit(commit) || !executionGitAbsolutePath(repository) || !executionGitAbsolutePath(goRoot) || !executionGitAbsolutePath(moduleCache) {
		t.Fatal("explicit exact source, protected-copy SDK and offline cache selections required")
	}
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	surrealBinary := toolCustodyExternalSurreal(t)
	parent, err := os.MkdirTemp("", "t422-production-rehearsal-")
	if err != nil {
		t.Fatal(err)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	t.Cleanup(func() {
		if !completed || t.Failed() {
			t.Logf("retained exact rehearsal custody (no automatic retry): %s", parent)
			return
		}
		current, err := os.Lstat(parent)
		if err != nil || !os.SameFile(parentInfo, current) {
			t.Error("rehearsal cleanup parent changed; retaining it")
			return
		}
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	defer cancel()
	var run *ExecutionProductionRun
	started := time.Now()
	git, err := ProtectExecutionGit(ctx, parent, gitBinary)
	if git != nil {
		defer func() {
			if productionRehearsalCanRelease(run) {
				_ = git.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := ProtectExecutionGoBuildInputs(ctx, parent, ExecutionGoBuildRequest{Git: git, RepositoryRoot: repository,
		PlanSourceCommit: commit, IntegratedMainCommit: commit, SourceCommit: commit, GoRoot: goRoot, ModuleCache: moduleCache})
	if inputs != nil {
		defer func() {
			if productionRehearsalCanRelease(run) {
				_ = inputs.Close()
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual full source/SDK/module custody: %s; %+v", time.Since(started), inputs.Inventory())
	workspace := filepath.Join(parent, "supplied-builds")
	for _, path := range []string{workspace, filepath.Join(workspace, "home"), filepath.Join(workspace, "tmp"), filepath.Join(workspace, "cache")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var tools []*ExecutionToolCustody
	defer func() {
		if productionRehearsalCanRelease(run) {
			for _, tool := range tools {
				_ = tool.Close()
			}
		}
	}()
	for _, role := range []string{"phebs", "zoekt-git-index"} {
		selected := productionRehearsalBuild(t, ctx, inputs, workspace, role)
		started = time.Now()
		tool, err := inputs.ProtectReferenceTool(ctx, parent, role, selected)
		if tool != nil {
			tools = append(tools, tool)
		}
		if err != nil {
			t.Fatalf("actual protected %s reference admission: %v", role, err)
		}
		t.Logf("actual protected %s independent reference admission: %s", role, time.Since(started))
	}
	surreal, err := ProtectExecutionExternalTool(ctx, parent, "surreal", surrealBinary)
	if surreal != nil {
		tools = append(tools, surreal)
	}
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(parent, "source.git")
	gitCustodyTestRun(t, git, parent, nil, "init", "--bare", "--template=", "--initial-branch=main", source)
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	const needle = "t422_parent_vertical_needle"
	gitCustodyTestRun(t, git, parent, strings.NewReader(gitCustodyTestImport(needle+"\n", "")), "-C", source, "fast-import", "--quiet", "--date-format=raw")
	sourceCommit := gitCustodyTestRevision(t, git, parent, source)[0]
	for _, name := range []string{"data", "home", "tmp"} {
		if err := os.Mkdir(filepath.Join(parent, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	custody, err := PrepareExecutionProduction(ctx, parent, ExecutionProductionRequest{Git: git, Builds: inputs,
		Phebs: tools[0], Zoekt: tools[1], Surreal: surreal, SourceRepository: source, SourceCommit: sourceCommit,
		DataRoot: filepath.Join(parent, "data"), Home: filepath.Join(parent, "home"), Temporary: filepath.Join(parent, "tmp"), Listen: address})
	if custody != nil {
		defer func() { _ = custody.Close() }()
	}
	if err != nil {
		t.Fatal(err)
	}
	run, err = custody.StartServe(ctx)
	if run != nil {
		defer func() {
			stopCtx, stop := context.WithTimeout(context.Background(), time.Minute)
			defer stop()
			result, err := run.Stop(stopCtx)
			if err != nil || !result.RootJoined || !result.SessionEmpty {
				t.Errorf("retained failed rehearsal prefix: %+v, %v", result, err)
			}
			if t.Failed() && result.RootJoined {
				// Wait has joined the sole combined-output copier. Preserve this
				// bounded private diagnostic, never put raw server text in evidence.
				file, err := os.OpenFile(filepath.Join(parent, "server.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err != nil {
					t.Error("private joined-server diagnostic could not be retained")
				} else {
					_, writeErr := file.Write(run.output.buffer.Bytes())
					if closeErr := file.Close(); writeErr != nil || closeErr != nil {
						t.Error("private joined-server diagnostic was not completely retained")
					}
				}
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	productionRehearsalHTTP(t, ctx, run, needle)
	stopCtx, stop := context.WithTimeout(ctx, time.Minute)
	result, err := run.Stop(stopCtx)
	stop()
	if err != nil || !result.RootStarted || !result.RootJoined || !result.SessionEmpty || !result.Accounting.Complete {
		t.Fatalf("real serve/native join/accounting close: %+v, %v", result, err)
	}
	t.Logf("real Phebs/Surreal indexed query plus owner-drained stop: %s; attempts=%d roles=%+v wire=%d",
		time.Since(started), result.Accounting.Attempts, result.Accounting.Phases, result.Accounting.ReservedWireBytes)
	// Only this successful, native-empty path registers thaw/removal of exact
	// fixture-owned copies. Any earlier failure retains private diagnostics.
	gitCustodyTestCleanup(t, git)
	goBuildTestCleanup(t, inputs)
	for _, tool := range tools {
		inputCustodyTestCleanup(t, tool.input, []ExecutionInputCopy{{Name: tool.identity.Role}})
	}
	inputCustodyTestCleanup(t, custody.config, []ExecutionInputCopy{{Name: "config"}})
	completed = true
}

func productionRehearsalCanRelease(run *ExecutionProductionRun) bool {
	if run == nil {
		return true
	}
	run.custody.mu.Lock()
	defer run.custody.mu.Unlock()
	return !run.custody.active
}

func productionRehearsalBuild(t *testing.T, ctx context.Context, inputs *ExecutionGoBuildCustody, workspace, role string) string {
	t.Helper()
	packagePath, _, _, _, _, err := referenceToolRole(role)
	if err != nil {
		t.Fatal(err)
	}
	request := ReferenceToolRequest{GoRoot: filepath.Join(inputs.Directory(), "sdk"), ModuleCache: filepath.Join(inputs.Directory(), "modules")}
	environment := referenceBuildEnvironment(request, workspace)
	for index, value := range environment {
		if strings.HasPrefix(value, "PATH=") {
			environment[index] = "PATH=" + inputs.git.Directory()
		}
	}
	environment = append(environment, "GIT_EXEC_PATH="+inputs.git.Directory(), "GIT_ALLOW_PROTOCOL=file", "GIT_TEMPLATE_DIR="+os.DevNull)
	selected := filepath.Join(workspace, role)
	if inputs.Check(ctx) != nil {
		t.Fatal("protected inputs drifted before supplied build")
	}
	started := time.Now()
	if _, err := runReferenceGo(ctx, inputs.reference.root.root, filepath.Join(request.GoRoot, "bin", "go"), environment, 64<<10,
		"build", "-trimpath", "-pgo=off", "-buildvcs=true", "-p=1", "-o", selected, packagePath); err != nil || inputs.Check(ctx) != nil {
		t.Fatalf("protected supplied %s build: %v", role, err)
	}
	t.Logf("protected supplied %s build: %s", role, time.Since(started))
	return selected
}

func productionRehearsalHTTP(t *testing.T, ctx context.Context, run *ExecutionProductionRun, needle string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(token, key string) (int, []byte, http.Header, error) {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+run.custody.request.Listen+"/api/search?q="+needle, nil)
		if err != nil {
			return 0, nil, nil, err
		}
		if token != "" {
			r.Header.Set(dispatchadmission.ProductionRequestHeader, token)
		}
		r.Header.Set("Authorization", "Bearer "+key)
		response, err := client.Do(r)
		if err != nil {
			return 0, nil, nil, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(raw) > 64<<10 {
			return 0, nil, nil, ErrExecutionProductionCustody
		}
		return response.StatusCode, raw, response.Header, nil
	}
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		status, _, header, err := request("", "invalid-key")
		if err == nil {
			if status != http.StatusServiceUnavailable || len(header.Values("Set-Cookie")) != 0 {
				t.Fatalf("missing parent token reached auth/session stack: %d", status)
			}
			break
		}
		select {
		case <-run.done:
			t.Fatal("real server stopped before HTTP boundary")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("real server HTTP boundary unavailable")
		case <-ticker.C:
		}
	}
	status, _, header, err := request(strings.Repeat("0", 64), "invalid-key")
	if err != nil || status != http.StatusServiceUnavailable || len(header.Values("Set-Cookie")) != 0 {
		t.Fatal("nonmatching parent token reached auth/session stack")
	}
	token := run.control.RequestToken()
	status, _, _, err = request(token, "invalid-key")
	if err != nil || status != http.StatusUnauthorized {
		t.Fatalf("parent request authority bypassed ordinary API authentication: %d, %v", status, err)
	}
	for {
		status, raw, _, err := request(token, run.custody.apiKey)
		var result search.Result
		if err == nil && status == http.StatusOK && json.Unmarshal(raw, &result) == nil {
			for _, file := range result.Files {
				if file.Path == "file.txt" && strings.Contains(string(raw), needle) {
					return
				}
			}
		}
		select {
		case <-run.done:
			t.Fatal("real server stopped before indexed query")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("real indexed query unavailable: status %d, %v", status, err)
		case <-ticker.C:
		}
	}
}
