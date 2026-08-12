package t4013

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t401"
)

const readinessRehearsalEnvironment = "PHEBS_T4013_READINESS_REHEARSAL"

// TestProductionPathReadinessRehearsal is an opt-in, bounded rehearsal of the
// production paths that failed Takes 11-15. It deliberately builds the current
// working tree rather than HEAD so a correction can cross this bar before its
// readiness commit. The frozen ceremony still builds only its committed source.
func TestProductionPathReadinessRehearsal(t *testing.T) {
	if os.Getenv(readinessRehearsalEnvironment) != "1" {
		t.Skip("set " + readinessRehearsalEnvironment + "=1 to run the real-binary readiness rehearsal")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, err = filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	toolchain, err := buildWorkingTreeToolchain(ctx, moduleRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"semantic", "structural"} {
		t.Run(kind, func(t *testing.T) {
			rehearseProductionPath(t, ctx, moduleRoot, workspace, toolchain, kind)
		})
	}
}

func buildWorkingTreeToolchain(
	ctx context.Context,
	moduleRoot string,
	workspace string,
) (privateToolchain, error) {
	output := filepath.Join(workspace, "working-tree-toolchain")
	if err := os.Mkdir(output, 0o700); err != nil {
		return privateToolchain{}, err
	}
	toolchain := privateToolchain{
		Schema:  privateToolchainSchema,
		Phebs:   filepath.Join(output, "phebs"),
		Zoekt:   filepath.Join(output, "zoekt-git-index"),
		Focused: filepath.Join(output, "phebs-focused-index"),
		Buf:     filepath.Join(output, "buf"),
	}
	builds := []struct {
		output string
		path   string
		env    []string
	}{
		{toolchain.Phebs, "./cmd/phebs", nil},
		{toolchain.Zoekt, "github.com/sourcegraph/zoekt/cmd/zoekt-git-index", nil},
		{toolchain.Focused, "./cmd/phebs-focused-index", nil},
		{toolchain.Buf, "github.com/bufbuild/buf/cmd/buf", []string{"CGO_ENABLED=0"}},
	}
	for _, build := range builds {
		command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", build.output, build.path)
		command.Dir = moduleRoot
		command.Env = append(scrubExecutionEnvironment(), build.env...)
		if output, err := command.CombinedOutput(); err != nil {
			return privateToolchain{}, fmt.Errorf("build readiness tool %s: %w: %s", build.path, err, output)
		}
	}
	if err := validateToolchain(toolchain); err != nil {
		return privateToolchain{}, err
	}
	return toolchain, nil
}

func rehearseProductionPath(
	t *testing.T,
	ctx context.Context,
	moduleRoot string,
	workspace string,
	toolchain privateToolchain,
	kind string,
) {
	t.Helper()
	profile, err := prepareProjectionProfile(ctx, moduleRoot, workspace, kind)
	if err != nil {
		t.Fatal(err)
	}
	server, err := launchPrivateServer(ctx, profile, toolchain, "rehearsal-cold")
	if err != nil {
		t.Fatal(err)
	}
	running := true
	defer func() {
		if running {
			_ = server.stop(30 * time.Second)
		}
	}()
	if _, err := awaitPrivateServerHealth(ctx, server, profile, "rehearsal-cold", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	a := awaitReadinessSnapshot(t, ctx, profile, "a", 12*time.Minute)
	t.Log("cold revision A converged")
	if _, err := waitLifecycle(ctx, profile, true, 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	serviceKey, requireCitation := "service-000", false
	if kind == "semantic" {
		serviceKey, requireCitation = "semantic", true
	}
	if _, exact, err := queryProfile(ctx, profile, serviceKey, requireCitation); err != nil || !exact {
		t.Fatalf("cold authorized query exact=%t: %v", exact, err)
	}
	t.Log("cold lifecycle and authorized query passed")

	if kind == "structural" {
		if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["b"]); err != nil {
			t.Fatal(err)
		}
		b := awaitReadinessSnapshot(t, ctx, profile, "b", 12*time.Minute)
		t.Log("structural revision B converged")
		if snapshotAuthority(a) == snapshotAuthority(b) || changedSourceMembers(a, b) <= 0 {
			t.Fatal("structural B did not change exact source and derived authority")
		}
		if err := updateSourceRevision(ctx, profile.Repository, profile.Revisions["a-return"]); err != nil {
			t.Fatal(err)
		}
		a = awaitReadinessSnapshot(t, ctx, profile, "a-return", 12*time.Minute)
		t.Log("structural revision A-return converged")
	}

	backup, _, err := createLiveBackup(ctx, toolchain, profile, workspace, "rehearsal-"+kind)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	running = false
	if _, err := restoreBackup(ctx, toolchain, profile, workspace, backup, "rehearsal-"+kind); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestoredBoundary(ctx, profile, a); err != nil {
		t.Fatal(err)
	}
	t.Log("live backup and offline restore boundary passed")

	server, err = launchPrivateServer(ctx, profile, toolchain, "rehearsal-restored")
	if err != nil {
		t.Fatal(err)
	}
	running = true
	if _, err := awaitPrivateServerHealth(ctx, server, profile, "rehearsal-restored", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	revision := "a"
	if kind == "structural" {
		revision = "a-return"
	}
	restored := awaitReadinessSnapshot(t, ctx, profile, revision, 12*time.Minute)
	if snapshotAuthority(restored) != snapshotAuthority(a) {
		t.Fatal("live backup and offline restore changed exact authority")
	}
	if _, err := waitLifecycle(ctx, profile, false, 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, exact, err := queryProfile(ctx, profile, serviceKey, requireCitation); err != nil || !exact {
		t.Fatalf("restored authorized query exact=%t: %v", exact, err)
	}
	t.Log("restored convergence, lifecycle, and authorized query passed")
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	running = false
}

func prepareProjectionProfile(
	ctx context.Context,
	moduleRoot string,
	workspace string,
	kind string,
) (PreparedProfile, error) {
	profile, err := t401.ProjectionProfile(kind)
	if err != nil {
		return PreparedProfile{}, err
	}
	profileRoot := filepath.Join(workspace, kind)
	if err := os.Mkdir(profileRoot, 0o700); err != nil {
		return PreparedProfile{}, err
	}
	authored := filepath.Join(profileRoot, "authored")
	receipt, err := t401.Author(ctx, t401.AuthorRequest{
		ModuleRoot: moduleRoot,
		Output:     authored,
		Profile:    profile,
	})
	if err != nil {
		return PreparedProfile{}, err
	}
	revisions := make(map[string]string, len(receipt.Revisions))
	for _, revision := range receipt.Revisions {
		revisions[revision.Revision] = revision.Commit
	}
	repository := filepath.Join(authored, "repository.git")
	if err := updateSourceRevision(ctx, repository, revisions["a"]); err != nil {
		return PreparedProfile{}, err
	}
	repositoryName, err := phebssync.RepoName(repository)
	if err != nil {
		return PreparedProfile{}, err
	}
	catalogPath := filepath.Join(profileRoot, "service-catalog.json")
	catalog, err := catalogForShape(kind, profile.Shape.Cells)
	if err != nil {
		return PreparedProfile{}, err
	}
	if err := writePrivateNew(catalogPath, catalog); err != nil {
		return PreparedProfile{}, err
	}
	credential, err := randomCredential()
	if err != nil {
		return PreparedProfile{}, err
	}
	credentialPath := filepath.Join(profileRoot, "api-key")
	if err := writePrivateNew(credentialPath, []byte(credential+"\n")); err != nil {
		return PreparedProfile{}, err
	}
	dataDir := filepath.Join(profileRoot, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		return PreparedProfile{}, err
	}
	address, err := reserveLoopbackAddress()
	if err != nil {
		return PreparedProfile{}, err
	}
	configPath := filepath.Join(profileRoot, "phebs.yaml")
	config, err := configFor(repository, repositoryName, catalogPath, dataDir, address, credential)
	if err != nil {
		return PreparedProfile{}, err
	}
	if err := writePrivateNew(configPath, config); err != nil {
		return PreparedProfile{}, err
	}
	return PreparedProfile{
		Name:           profile.Name,
		Repository:     repository,
		RepositoryName: repositoryName,
		Config:         configPath,
		Credential:     credentialPath,
		DataDir:        dataDir,
		Address:        address,
		Catalog:        catalogPath,
		Revisions:      revisions,
	}, nil
}

func reserveLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return address, nil
}

func awaitReadinessSnapshot(
	t *testing.T,
	ctx context.Context,
	profile PreparedProfile,
	revision string,
	limit time.Duration,
) privateProfileSnapshot {
	t.Helper()
	inspector, err := newProfileInspector(profile)
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := convergenceProbe("not_started")
	var lastErr error
	lastDiagnostic := ""
	for {
		snapshot, probe, inspectErr := inspector.inspectWithProgress(wait, profile, revision)
		last = probe
		if inspectErr == nil {
			return snapshot
		}
		lastErr = inspectErr
		diagnostic := probe.Stage + ": " + inspectErr.Error()
		if diagnostic != lastDiagnostic {
			t.Logf("readiness pending at %s: %v", probe.Stage, inspectErr)
			lastDiagnostic = diagnostic
		}
		if errors.Is(inspectErr, errRepositoryIndexTerminal) {
			t.Fatalf("repository index terminated at %s (%s)", probe.Stage, probe.RepositoryIndexFailureClass)
		}
		select {
		case <-wait.Done():
			t.Fatalf(
				"readiness convergence expired at %s: deadline=%v last_error=%v",
				last.Stage, wait.Err(), lastErr,
			)
		case <-ticker.C:
		}
	}
}
