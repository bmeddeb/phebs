package t4013

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func drainedPrepareSupervision(t *testing.T, workspace, planDigest string) string {
	t.Helper()
	token, err := newCustodyToken()
	if err != nil {
		t.Fatal(err)
	}
	supervision, err := beginPrepareCustody(workspace, planDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervision.Drain(""); err != nil {
		t.Fatal(err)
	}
	if err := supervision.Close(); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestV25AtomicEvidenceFilesystemPreflight(t *testing.T) {
	parent := t.TempDir()
	if err := preflightAtomicEvidenceProtocol(parent, Plan{Schema: PlanSchemaV25}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("filesystem probe residue = %v, %v", entries, err)
	}

	missing := filepath.Join(parent, "missing")
	if err := preflightAtomicEvidenceProtocol(missing, Plan{Schema: PlanSchemaV24}); err != nil {
		t.Fatalf("historical plan acquired V25 filesystem probe: %v", err)
	}
	if err := preflightAtomicEvidenceProtocol(missing, Plan{Schema: PlanSchemaV25}); err == nil {
		t.Fatal("V25 filesystem probe accepted an absent ceremony filesystem")
	}
}

func TestPreparePortPreflightRefusesOccupiedPair(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	if port < 1024 || port > 65533 {
		t.Skipf("ephemeral port %d cannot start a ceremony pair", port)
	}
	release, err := reserveLoopbackPortsForPlan(Plan{Schema: PlanSchemaV24}, port)
	if err != nil {
		t.Fatalf("historical plan acquired V25 port reservation: %v", err)
	}
	release()
	if _, err := reserveLoopbackPortsForPlan(Plan{Schema: PlanSchemaV25}, port); err == nil {
		t.Fatal("occupied ceremony port passed the final pre-authoring check")
	}
}

func TestPreparedCleanupRetainsCrashIndeterminateCustody(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{module, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	module, err := filepath.EvalSymlinks(module)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := frozenV25PlanWithHostToolchain(testSourceCommit, fakeHostToolchainV25())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(root, "plan.json")
	preparedPath := filepath.Join(root, "prepared.json")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := newCustodyToken()
	if err != nil {
		t.Fatal(err)
	}
	supervision, err := beginPrepareCustody(workspace, PlanDigest(planBytes), token)
	if err != nil {
		t.Fatal(err)
	}
	if err := supervision.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writePreparedCleanupControl(preparedPath+".preparing", preparedCleanupControl{
		Schema: preparedCleanupSchemaV2, PlanDigest: PlanDigest(planBytes),
		ModuleRoot: module, Workspace: workspace, SupervisionToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preparedPath+".tmp", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupPrepared(module, planPath, preparedPath, CleanupConfirm); err == nil ||
		!strings.Contains(err.Error(), "not durably drained") {
		t.Fatalf("crash-indeterminate cleanup = %v", err)
	}
	for _, path := range []string{workspace, preparedPath + ".tmp", preparedPath + ".preparing"} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("crash-indeterminate cleanup changed %s: %v", path, err)
		}
	}
}

func TestFailedPreparationRetainsIncompleteV25Custody(t *testing.T) {
	for _, test := range []struct {
		name       string
		version    int
		cancel     bool
		cause      error
		wantText   string
		wantRetain bool
		wantErr    error
	}{
		{name: "V25 ordinary failure", version: 25, cause: errors.New("author failed"), wantRetain: true, wantText: "external process-absence proof"},
		{name: "V25 cancellation", version: 25, cancel: true, cause: errors.New("author failed"), wantRetain: true, wantErr: context.Canceled},
		{name: "V25 shutdown uncertainty", version: 25, cause: errPrivateServerShutdownUnproven, wantRetain: true, wantErr: errPrivateServerShutdownUnproven},
		{name: "historical cancellation", version: 24, cancel: true, cause: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			module := filepath.Join(root, "module")
			workspace := filepath.Join(root, "custody")
			output := filepath.Join(root, "prepared.json")
			control := output + ".preparing"
			for _, path := range []string{module, workspace} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{output, control} {
				if err := os.WriteFile(path, []byte("control\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			if test.cancel {
				cancel()
			} else {
				defer cancel()
			}
			err := cleanupFailedPreparation(
				ctx, test.version, test.cause, true, workspace, module, output, control,
			)
			if test.wantErr != nil && !errors.Is(err, test.wantErr) ||
				test.wantText != "" && (err == nil || !strings.Contains(err.Error(), test.wantText)) ||
				test.wantErr == nil && test.wantText == "" && err != nil {
				t.Fatalf("cleanup error = %v, want %v", err, test.wantErr)
			}
			for _, path := range []string{workspace, output, control} {
				_, err := os.Lstat(path)
				if test.wantRetain && err != nil {
					t.Fatalf("retained path %s = %v", path, err)
				}
				if !test.wantRetain && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("cleaned path %s = %v", path, err)
				}
			}
		})
	}
}

func TestPreparedPublicationChecksCancellationAtEveryBoundary(t *testing.T) {
	for _, test := range []struct {
		name      string
		cancelAt  int
		wantTemp  bool
		wantFinal bool
	}{
		{name: "before stage", cancelAt: 1},
		{name: "after stage", cancelAt: 2, wantTemp: true},
		{name: "after publication", cancelAt: 3, wantFinal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "prepared.json")
			ctx := &cancelAtErrContext{Context: t.Context(), cancelAt: test.cancelAt}
			err := publishPreparedOutput(ctx, output, []byte("prepared\n"))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("publication cancellation = %v", err)
			}
			for path, want := range map[string]bool{output + ".tmp": test.wantTemp, output: test.wantFinal} {
				_, err := os.Lstat(path)
				if (err == nil) != want || err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("publication path %s present=%t, err=%v", path, err == nil, err)
				}
			}
		})
	}
}

type cancelAtErrContext struct {
	context.Context
	calls    int
	cancelAt int
}

func (ctx *cancelAtErrContext) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestV25CleanupPreparedRefusesExecutedCustody(t *testing.T) {
	tests := []struct {
		name       string
		makeMarker func(string, string) error
	}{
		{
			name: "regular marker",
			makeMarker: func(marker, _ string) error {
				return os.WriteFile(marker, []byte("executed\n"), 0o600)
			},
		},
		{
			name: "symlink marker",
			makeMarker: func(marker, target string) error {
				return os.Symlink(target, marker)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			module := filepath.Join(root, "module")
			workspace := filepath.Join(root, "custody")
			for _, path := range []string{module, workspace} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			module, err := filepath.EvalSymlinks(module)
			if err != nil {
				t.Fatal(err)
			}
			workspace, err = filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := frozenV25PlanWithHostToolchain(testSourceCommit, fakeHostToolchainV25())
			if err != nil {
				t.Fatal(err)
			}
			planBytes, err := MarshalPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			planPath := filepath.Join(root, "plan.json")
			preparedPath := filepath.Join(root, "prepared.json")
			if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			token := drainedPrepareSupervision(t, workspace, PlanDigest(planBytes))
			controlPath := preparedPath + ".preparing"
			if err := writePreparedCleanupControl(controlPath, preparedCleanupControl{
				Schema: preparedCleanupSchemaV2, PlanDigest: PlanDigest(planBytes),
				ModuleRoot: module, Workspace: workspace, SupervisionToken: token,
			}); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(root, "outside")
			if err := os.WriteFile(outside, []byte("retain"), 0o600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(workspace, executedMarkerName)
			if err := test.makeMarker(marker, outside); err != nil {
				t.Fatal(err)
			}

			err = CleanupPrepared(module, planPath, preparedPath, CleanupConfirm)
			if err == nil || !strings.Contains(err.Error(), "executed custody") {
				t.Fatalf("cleanup error = %v", err)
			}
			for _, path := range []string{workspace, marker, controlPath, outside} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("refused cleanup changed %s: %v", path, err)
				}
			}
		})
	}
}

func TestPreparedCustodyIsStrictAndPlanBound(t *testing.T) {
	profile := func(name string, port string) PreparedProfile {
		return PreparedProfile{
			Name: name, Repository: "/private/t4013/repository.git", RepositoryName: "local.invalid/neutral",
			Config: "/private/t4013/phebs.yaml", Credential: "/private/t4013/api-key",
			DataDir: "/private/t4013/data", Address: "127.0.0.1:" + port,
			Catalog: "/private/t4013/catalog.json", Revisions: map[string]string{
				"a": testSourceCommit, "b": testSourceCommit, "a-return": testSourceCommit,
			},
		}
	}
	value := Prepared{Schema: PreparedSchema, PlanDigest: "sha256:" + strings.Repeat("a", 64), Profiles: []PreparedProfile{
		profile("structural-2m-v1", "41731"), profile("semantic-262144-v1", "41732"),
	}}
	raw, err := MarshalPrepared(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePrepared(raw, value.PlanDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePrepared(raw, "sha256:"+strings.Repeat("b", 64)); err == nil {
		t.Fatal("prepared custody passed against another plan")
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["extra"] = true
	changed, _ := json.Marshal(object)
	if _, err := DecodePrepared(changed, value.PlanDigest); err == nil {
		t.Fatal("prepared custody with unknown field passed")
	}
	value.SupervisionToken = strings.Repeat("a", 64)
	if _, err := MarshalPrepared(value); err == nil {
		t.Fatal("historical prepared schema accepted a supervision token")
	}
	value.Schema = PreparedSchemaV2
	value.ExecutionControlsSHA256 = "sha256:" + strings.Repeat("b", 64)
	if _, err := MarshalPrepared(value); err != nil {
		t.Fatalf("supervised prepared schema rejected its token: %v", err)
	}
}

func TestPreparedCleanupControlRefusesUnreadableOversizeAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prepared.json.preparing")
	longPath := string(filepath.Separator) + strings.Repeat("a", preparedCleanupMaxBytes)
	err := writePreparedCleanupControl(path, preparedCleanupControl{
		Schema: preparedCleanupSchemaV2, PlanDigest: "sha256:" + strings.Repeat("a", 64),
		ModuleRoot: longPath, Workspace: longPath + "-workspace",
		SupervisionToken: strings.Repeat("b", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "byte bound") {
		t.Fatalf("oversize prepared cleanup authority = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversize prepared cleanup authority left residue: %v", err)
	}
}

func TestPreparedCatalogsAreExactSmallControls(t *testing.T) {
	tests := []struct {
		kind        string
		services    int
		memberships int
		unowned     int
	}{
		{kind: "structural", services: 100, memberships: 100, unowned: 101},
		{kind: "semantic", services: 1, memberships: 1, unowned: 2},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			raw, err := catalogFor(test.kind)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := servicecatalog.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.Services) != test.services || len(catalog.Memberships) != test.memberships ||
				len(catalog.Unowned) != test.unowned {
				t.Fatalf("catalog shape = %d/%d/%d, want %d/%d/%d",
					len(catalog.Services), len(catalog.Memberships), len(catalog.Unowned),
					test.services, test.memberships, test.unowned)
			}
			if len(catalog.Memberships)+len(catalog.Unowned) > servicecatalog.MaxDistinctPaths {
				t.Fatal("catalog exceeds the retained v2 path ceiling")
			}
			if test.kind == "structural" && len(catalog.Memberships)+len(catalog.Unowned) != 201 {
				t.Fatal("structural catalog distinct-path oracle changed")
			}
		})
	}
	if _, err := catalogFor("unknown"); err == nil {
		t.Fatal("unknown profile kind passed")
	}
	projectionRaw, err := catalogForShape("structural", 64)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := servicecatalog.Decode(projectionRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Services) != 64 || len(projection.Memberships) != 64 ||
		len(projection.Unowned) != 2 {
		t.Fatalf(
			"structural projection catalog shape = %d/%d/%d, want 64/64/2",
			len(projection.Services), len(projection.Memberships), len(projection.Unowned),
		)
	}
}

func TestPreparedConfigPassesProductionParser(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository.git")
	catalog := filepath.Join(root, "catalog.json")
	dataDir := filepath.Join(root, "data")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := configFor(repository, "local.invalid/neutral", catalog, dataDir, "127.0.0.1:41731", "credential")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Server.DataDir != dataDir || parsed.Server.Addr != "127.0.0.1:41731" ||
		len(parsed.Connections) != 1 || parsed.Connections[0].URL != repository ||
		!parsed.Diagnostics.Jobs || !parsed.Diagnostics.Candidates || !parsed.Diagnostics.Extraction ||
		!parsed.Lifecycle.EnabledFor() {
		t.Fatalf("parsed config = %+v", parsed)
	}
}

func TestCustodyContainmentIsPathComponentAware(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", ".t4013")
	if !isWithin(filepath.Join(root, ".hidden", "child"), root) {
		t.Fatal("hidden child was not recognized as within custody")
	}
	if isWithin(root, root) || isWithin(filepath.Join(root, "..", ".t4013-other"), root) ||
		isWithin(filepath.Dir(root), root) {
		t.Fatal("non-child path was recognized as within custody")
	}
}

func TestGitEnvironmentDoesNotOverrideHomeOrInheritGitControls(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", "/unsafe")
	t.Setenv("T4013_NON_GIT_SENTINEL", "retained")
	environment := gitEnvironment()
	seenSentinel := false
	for _, entry := range environment {
		if entry == "HOME=/dev/null" {
			t.Fatal("git environment repurposes HOME")
		}
		if entry == "GIT_CONFIG_COUNT=1" || entry == "GIT_CONFIG_KEY_0=core.hooksPath" ||
			entry == "GIT_CONFIG_VALUE_0=/unsafe" {
			t.Fatal("git environment inherited ambient Git controls")
		}
		if entry == "T4013_NON_GIT_SENTINEL=retained" {
			seenSentinel = true
		}
	}
	if !seenSentinel {
		t.Fatal("git environment discarded unrelated process state")
	}
}

func TestCleanupPreparedDestroysOnlyPlanBoundCustodyAndManifest(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	privateRoot := filepath.Join(root, "private")
	for _, path := range []string{moduleRoot, workspace, privateRoot} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := frozenV25PlanWithHostToolchain(testSourceCommit, fakeHostToolchainV25())
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := MarshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(privateRoot, "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	profile := func(name, lane, port string) PreparedProfile {
		laneRoot := filepath.Join(workspace, lane)
		return PreparedProfile{
			Name: name, Repository: filepath.Join(laneRoot, "repository.git"),
			RepositoryName: "local.invalid/neutral-" + lane,
			Config:         filepath.Join(laneRoot, "phebs.yaml"), Credential: filepath.Join(laneRoot, "api-key"),
			DataDir: filepath.Join(laneRoot, "data"), Address: "127.0.0.1:" + port,
			Catalog: filepath.Join(laneRoot, "catalog.json"), Revisions: map[string]string{
				"a": testSourceCommit, "b": testSourceCommit, "a-return": testSourceCommit,
			},
		}
	}
	token := drainedPrepareSupervision(t, workspace, PlanDigest(planBytes))
	prepared := Prepared{
		Schema: PreparedSchemaV2, PlanDigest: PlanDigest(planBytes), SupervisionToken: token,
		ExecutionControlsSHA256: "sha256:" + strings.Repeat("b", 64),
		Profiles: []PreparedProfile{
			profile("structural-2m-v1", "structural", "41731"),
			profile("semantic-262144-v1", "semantic", "41732"),
		}}
	preparedBytes, err := MarshalPrepared(prepared)
	if err != nil {
		t.Fatal(err)
	}
	preparedPath := filepath.Join(privateRoot, "prepared.json")
	if err := os.WriteFile(preparedPath, preparedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	realModuleRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	controlPath := preparedPath + ".preparing"
	if err := writePreparedCleanupControl(controlPath, preparedCleanupControl{
		Schema: preparedCleanupSchemaV2, PlanDigest: PlanDigest(planBytes),
		ModuleRoot: realModuleRoot, Workspace: workspace, SupervisionToken: token,
	}); err != nil {
		t.Fatal(err)
	}
	if err := CleanupPrepared(moduleRoot, planPath, preparedPath, "wrong"); err == nil {
		t.Fatal("cleanup accepted the wrong confirmation")
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatal("failed cleanup changed custody")
	}
	if err := CleanupPrepared(moduleRoot, planPath, preparedPath, CleanupConfirm); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{workspace, preparedPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup retained %s", path)
		}
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "retain" {
		t.Fatal("cleanup crossed its exact custody boundary")
	}
}

func TestDestroyPreparedCanonicalizesModuleRootBeforeDeletion(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "custody")
	module := filepath.Join(workspace, "module")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	moduleLink := filepath.Join(root, "module-link")
	if err := os.Symlink(module, moduleLink); err != nil {
		t.Fatal(err)
	}
	profile := func(name, lane, port string) PreparedProfile {
		laneRoot := filepath.Join(workspace, lane)
		return PreparedProfile{
			Name: name, Repository: filepath.Join(laneRoot, "repository.git"),
			RepositoryName: "local.invalid/" + lane,
			Config:         filepath.Join(laneRoot, "phebs.yaml"), Credential: filepath.Join(laneRoot, "api-key"),
			DataDir: filepath.Join(laneRoot, "data"), Address: "127.0.0.1:" + port,
			Catalog: filepath.Join(laneRoot, "catalog.json"), Revisions: map[string]string{
				"a": testSourceCommit, "b": testSourceCommit, "a-return": testSourceCommit,
			},
		}
	}
	prepared := Prepared{
		Schema: PreparedSchema, PlanDigest: "sha256:" + strings.Repeat("a", 64),
		Profiles: []PreparedProfile{
			profile("structural-2m-v1", "structural", "41731"),
			profile("semantic-262144-v1", "semantic", "41732"),
		},
	}
	if err := DestroyPrepared(prepared, moduleLink); err == nil {
		t.Fatal("DestroyPrepared accepted a module symlink into custody")
	}
	if _, err := os.Lstat(workspace); err != nil {
		t.Fatalf("refused DestroyPrepared removed custody: %v", err)
	}
}

func TestDestroyPreparedRefusesLegacyDowngradeOverSupervisedCustody(t *testing.T) {
	for _, suffix := range []string{"", custodyRetiringSuffix, custodyRetiredSuffix} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			module := filepath.Join(root, "module")
			workspace := filepath.Join(root, "custody")
			for _, path := range []string{module, workspace} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			digest := "sha256:" + strings.Repeat("a", 64)
			_, directory, err := custodyControlDirectory(workspace)
			if err != nil {
				t.Fatal(err)
			}
			if suffix == "" {
				supervision, err := beginPrepareCustody(workspace, digest, mustCustodyToken(t))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = supervision.Close() })
			} else if err := os.Mkdir(directory+suffix, 0o700); err != nil {
				t.Fatal(err)
			}
			profile := func(name, lane, port string) PreparedProfile {
				root := filepath.Join(workspace, lane)
				return PreparedProfile{
					Name: name, Repository: filepath.Join(root, "repository.git"),
					RepositoryName: "local.invalid/" + lane,
					Config:         filepath.Join(root, "phebs.yaml"), Credential: filepath.Join(root, "api-key"),
					DataDir: filepath.Join(root, "data"), Address: "127.0.0.1:" + port,
					Catalog: filepath.Join(root, "catalog.json"), Revisions: map[string]string{
						"a": testSourceCommit, "b": testSourceCommit, "a-return": testSourceCommit,
					},
				}
			}
			prepared := Prepared{
				Schema: PreparedSchema, PlanDigest: digest,
				Profiles: []PreparedProfile{
					profile("structural-2m-v1", "structural", "41731"),
					profile("semantic-262144-v1", "semantic", "41732"),
				},
			}
			if err := DestroyPrepared(prepared, module); err == nil {
				t.Fatal("DestroyPrepared accepted a legacy manifest over supervised custody")
			}
			if _, err := os.Lstat(workspace); err != nil {
				t.Fatalf("refused legacy downgrade removed custody: %v", err)
			}
		})
	}
}

func TestDestroyCustodyRetriesTransientDirectoryNotEmptyAndVerifiesStableAbsence(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{moduleRoot, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "initial"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	var waits []time.Duration
	remove := func(path string) error {
		calls++
		if calls == 1 {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(path, "late-writer"), []byte("private"), 0o600); err != nil {
				return err
			}
			return &os.PathError{Op: "unlinkat", Path: path, Err: syscall.ENOTEMPTY}
		}
		return os.RemoveAll(path)
	}
	if err := destroyCustodyWith(workspace, moduleRoot, remove, func(delay time.Duration) {
		waits = append(waits, delay)
	}, syncDirectory); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("remove calls = %d, want 2", calls)
	}
	if _, err := os.Lstat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("custody survived transient retry: %v", err)
	}
	if len(waits) != 3 || waits[0] != custodyRemoveRetryDelay ||
		waits[1] != custodyRemoveRetryDelay || waits[2] != custodyRemoveSettleDelay {
		t.Fatalf("retry waits = %v", waits)
	}
}

func TestDestroyCustodyDoesNotRetryNonTransientFailure(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{moduleRoot, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	want := &os.PathError{Op: "remove", Path: workspace, Err: syscall.EPERM}
	var calls int
	err := destroyCustodyWith(workspace, moduleRoot, func(string) error {
		calls++
		return want
	}, func(time.Duration) {
		t.Fatal("non-transient cleanup waited for a retry")
	}, syncDirectory)
	if !errors.Is(err, syscall.EPERM) || calls != 1 {
		t.Fatalf("cleanup error/calls = %v/%d", err, calls)
	}
	if _, statErr := os.Lstat(workspace); statErr != nil {
		t.Fatalf("non-transient cleanup changed custody: %v", statErr)
	}
}

func TestDestroyCustodyRequiresDurableParentDeletion(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{moduleRoot, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	want := errors.New("injected parent sync failure")
	err := destroyCustodyWith(
		workspace, moduleRoot, os.RemoveAll, func(time.Duration) {},
		func(path string) error {
			if path != root {
				t.Fatalf("synced parent = %q, want %q", path, root)
			}
			return want
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("durable custody deletion = %v", err)
	}
	if _, statErr := os.Lstat(workspace); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("custody survived removal before sync failure: %v", statErr)
	}
}

func TestDestroyCustodyTransientRetryIsHardBounded(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	workspace := filepath.Join(root, "custody")
	for _, path := range []string{moduleRoot, workspace} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var calls, waits int
	err := destroyCustodyWith(workspace, moduleRoot, func(path string) error {
		calls++
		return &os.PathError{Op: "unlinkat", Path: path, Err: syscall.EEXIST}
	}, func(delay time.Duration) {
		if delay != custodyRemoveRetryDelay {
			t.Fatalf("exhausted retry delay = %s", delay)
		}
		waits++
	}, syncDirectory)
	if err == nil || !strings.Contains(err.Error(), "did not settle") ||
		calls != custodyRemoveAttempts || waits != custodyRemoveAttempts-1 {
		t.Fatalf("bounded cleanup = %v, calls=%d, waits=%d", err, calls, waits)
	}
	if _, statErr := os.Lstat(workspace); statErr != nil {
		t.Fatalf("exhausted cleanup changed custody: %v", statErr)
	}
}
