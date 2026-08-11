package t4013

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

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
	plan, err := FrozenPlan(testSourceCommit)
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
	prepared := Prepared{Schema: PreparedSchema, PlanDigest: PlanDigest(planBytes), Profiles: []PreparedProfile{
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
	if err := os.WriteFile(preparedPath, preparedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupPrepared(moduleRoot, planPath, preparedPath, CleanupConfirm); err != nil {
		t.Fatalf("cleanup after ordinary custody teardown: %v", err)
	}
	if _, err := os.Lstat(preparedPath); !os.IsNotExist(err) {
		t.Fatal("cleanup retained the post-execution prepared manifest")
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
	}); err != nil {
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
	})
	if !errors.Is(err, syscall.EPERM) || calls != 1 {
		t.Fatalf("cleanup error/calls = %v/%d", err, calls)
	}
	if _, statErr := os.Lstat(workspace); statErr != nil {
		t.Fatalf("non-transient cleanup changed custody: %v", statErr)
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
	})
	if err == nil || !strings.Contains(err.Error(), "did not settle") ||
		calls != custodyRemoveAttempts || waits != custodyRemoveAttempts-1 {
		t.Fatalf("bounded cleanup = %v, calls=%d, waits=%d", err, calls, waits)
	}
	if _, statErr := os.Lstat(workspace); statErr != nil {
		t.Fatalf("exhausted cleanup changed custody: %v", statErr)
	}
}
