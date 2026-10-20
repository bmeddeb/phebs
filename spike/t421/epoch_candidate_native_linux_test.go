//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"debug/buildinfo"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

// TestLinuxEpochPrepareZoektCandidate produces supplied bytes only. The full
// driver must independently rebuild and compare them before issuing custody.
// Scratch is retained on success and failure; a generic runner error never
// certifies that its private session drained. The outer native wrapper owns
// independent process/session evidence. No helper or operational flow is run.
func TestLinuxEpochPrepareZoektCandidate(t *testing.T) {
	if os.Getuid() == 0 || os.Geteuid() == 0 || runtime.GOARCH != "amd64" || runtime.Version() != "go1.26.5" {
		t.Fatal("unprivileged Linux amd64 Go1.26.5 required; no skip")
	}
	request := readNativeGoReferenceRequest(t, "PHEBS_T42_ZOEKT_CANDIDATE_REQUEST")
	scratch := os.Getenv("PHEBS_T42_ZOEKT_CANDIDATE_SCRATCH")
	parent, err := linuxEpochCandidateShape(request, scratch)
	if err != nil {
		t.Fatal("closed fresh supplied-candidate paths required")
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || os.Mkdir(scratch, 0o700) != nil {
		t.Fatal("private supplied-candidate scratch unavailable")
	}
	t.Log("supplied-only scratch retained", scratch)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	if _, err := InspectExecutionCheckout(ctx, request.RepositoryRoot, request.GitBinary, request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit); err != nil {
		t.Fatal("exact clean source required before candidate preparation", err)
	}
	for _, name := range []string{"home", "tmp", "cache", "bin"} {
		if os.Mkdir(filepath.Join(scratch, name), 0o700) != nil {
			t.Fatal("private build directories unavailable")
		}
	}
	if os.Symlink(request.GitBinary, filepath.Join(scratch, "bin", "git")) != nil {
		t.Fatal("fixed supplied Git binding unavailable")
	}
	goBinary := filepath.Join(request.GoRoot, "bin", "go")
	goDigest, err := executableidentity.Digest(goBinary)
	if err != nil {
		t.Fatal("selected Go driver unavailable")
	}
	gitDigest, err := executableidentity.Digest(request.GitBinary)
	if err != nil {
		t.Fatal("selected Git image unavailable")
	}
	packagePath, modulePath, version, sum, _, err := referenceToolRoleForSchema(request.Role, request.PlanSchema, request.SourceCommit)
	if err != nil {
		t.Fatal("fixed supplied role unavailable")
	}
	intermediate := filepath.Join(scratch, "candidate")
	root, args, check, err := referenceToolBuildArgs(ctx, request.Role, request.PlanSchema, request.RepositoryRoot, request.ModuleCache, scratch, intermediate, packagePath)
	if err != nil || check() != nil || executableidentity.Verify(goBinary, goDigest) != nil || executableidentity.Verify(request.GitBinary, gitDigest) != nil {
		t.Fatal("exact supplied build recipe unavailable")
	}
	started := time.Now()
	raw, buildErr := runReferenceGo(ctx, root, goBinary, referenceBuildEnvironment(request, scratch), 64<<10, args...)
	if buildErr != nil {
		if err := os.WriteFile(filepath.Join(scratch, "build.stderr"), raw, 0o600); err != nil {
			t.Error("bounded private build diagnostic unavailable")
		}
		t.Fatal("supplied build failed; scratch/session uncertainty retained", buildErr)
	}
	// The private runner exposes no PID. Successful return proves its actual
	// sole Wait and empty session; do not invent a PID or infer this on failure.
	t.Logf("supplied build duration=%s runner_actual_wait=true runner_session_empty=true pid_available=false", time.Since(started))
	if ctx.Err() != nil || check() != nil || executableidentity.Verify(goBinary, goDigest) != nil || executableidentity.Verify(request.GitBinary, gitDigest) != nil {
		t.Fatal("supplied build inputs changed")
	}
	if _, err := InspectExecutionCheckout(ctx, request.RepositoryRoot, request.GitBinary, request.PlanSourceCommit, request.IntegratedMainCommit, request.SourceCommit); err != nil {
		t.Fatal("exact source changed after candidate preparation", err)
	}
	info, err := buildinfo.ReadFile(intermediate)
	if err != nil || validateReferenceBuildInfoForSchema(info, request.Role, request.PlanSchema, packagePath, request.SourceCommit, modulePath, version, sum, nil) != nil {
		t.Fatal("supplied candidate metadata differs")
	}
	current, err := os.Lstat(parent)
	if err != nil || !os.SameFile(parentInfo, current) || !linuxEpochCandidatePrivateDirectory(parent) {
		t.Fatal("supplied output parent replaced")
	}
	input, err := os.Open(intermediate)
	if err != nil {
		t.Fatal("supplied output unavailable")
	}
	output, err := os.OpenFile(request.Binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		_ = input.Close()
		t.Fatal("supplied output already exists or cannot be created")
	}
	n, copyErr := io.Copy(output, io.LimitReader(input, maxInputCustodyFileBytes+1))
	closeErr := errors.Join(output.Sync(), output.Close(), input.Close())
	if copyErr != nil || closeErr != nil || n < 1 || n > maxInputCustodyFileBytes {
		t.Fatal("supplied output incomplete; retained without authority")
	}
	digest, err := executableidentity.Digest(request.Binary)
	expected, expectedErr := executableidentity.Digest(intermediate)
	if err != nil || expectedErr != nil || digest != expected || ctx.Err() != nil {
		t.Fatal("supplied output copy differs")
	}
	t.Logf("supplied_only=true role=zoekt-git-index schema=%s bytes=%d sha256=%s source=%s", request.PlanSchema, n, digest, request.SourceCommit)
}

func linuxEpochCandidatePrivateDirectory(path string) bool {
	canonical, err := filepath.EvalSymlinks(path)
	info, statErr := os.Lstat(path)
	return executionGitAbsolutePath(path) && err == nil && canonical == path && statErr == nil && info.IsDir() && info.Mode().Perm() == 0o700 && inputCustodyCopyOwned(info)
}

func linuxEpochCandidateShape(request ReferenceToolRequest, scratch string) (string, error) {
	if request.Role != "zoekt-git-index" || request.PlanSchema != PlanV3Schema || !validCommit(request.SourceCommit) || !validCommit(request.PlanSourceCommit) || !validCommit(request.IntegratedMainCommit) || !executionGitAbsolutePath(scratch) || !executionGitAbsolutePath(request.Binary) || scratch == request.Binary || filepath.Dir(scratch) != filepath.Dir(request.Binary) {
		return "", ErrExecutionGoBuildCustody
	}
	parent := filepath.Dir(scratch)
	if !linuxEpochCandidatePrivateDirectory(parent) {
		return "", ErrExecutionGoBuildCustody
	}
	for _, path := range []string{scratch, request.Binary} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return "", ErrExecutionGoBuildCustody
		}
	}
	for _, path := range []string{request.RepositoryRoot, request.GoRoot, request.ModuleCache, request.GitBinary} {
		canonical, err := filepath.EvalSymlinks(path)
		if !executionGitAbsolutePath(path) || err != nil || canonical != path {
			return "", ErrExecutionGoBuildCustody
		}
		if path != request.GitBinary {
			if relative, err := filepath.Rel(path, parent); err != nil || relative == "." || filepath.IsLocal(relative) {
				return "", ErrExecutionGoBuildCustody
			}
			if relative, err := filepath.Rel(parent, path); err != nil || relative == "." || filepath.IsLocal(relative) {
				return "", ErrExecutionGoBuildCustody
			}
		}
	}
	return parent, nil
}

func TestLinuxEpochCandidateShapeRefusals(t *testing.T) {
	for _, mutation := range []string{"role", "schema", "source", "same-path", "other-parent", "existing-output", "existing-scratch", "symlink-parent", "public-parent"} {
		t.Run(mutation, func(t *testing.T) {
			base := t.TempDir()
			parent := filepath.Join(base, "private")
			if os.Mkdir(parent, 0o700) != nil {
				t.Fatal("fixture")
			}
			request := ReferenceToolRequest{Role: "zoekt-git-index", PlanSchema: PlanV3Schema, SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PlanSourceCommit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", IntegratedMainCommit: "cccccccccccccccccccccccccccccccccccccccc", Binary: filepath.Join(parent, "image")}
			for i, selected := range []*string{&request.RepositoryRoot, &request.GoRoot, &request.ModuleCache} {
				*selected = filepath.Join(base, []string{"source", "sdk", "modules"}[i])
				if os.Mkdir(*selected, 0o700) != nil {
					t.Fatal("fixture inputs")
				}
			}
			request.GitBinary = filepath.Join(base, "git")
			if os.WriteFile(request.GitBinary, []byte("shape only, never executed"), 0o700) != nil {
				t.Fatal("fixture Git")
			}
			scratch := filepath.Join(parent, "scratch")
			if _, err := linuxEpochCandidateShape(request, scratch); err != nil {
				t.Fatal("shape-only baseline refused; no admission asserted", err)
			}
			switch mutation {
			case "role":
				request.Role = "phebs"
			case "schema":
				request.PlanSchema = ""
			case "source":
				request.SourceCommit = "HEAD"
			case "same-path":
				request.Binary = scratch
			case "other-parent":
				request.Binary = filepath.Join(base, "image")
			case "existing-output":
				if os.WriteFile(request.Binary, []byte("foreign"), 0o600) != nil {
					t.Fatal("fixture")
				}
			case "existing-scratch":
				if os.Mkdir(scratch, 0o700) != nil {
					t.Fatal("fixture")
				}
			case "symlink-parent":
				alias := filepath.Join(base, "alias")
				if os.Symlink(parent, alias) != nil {
					t.Fatal("fixture")
				}
				request.Binary, scratch = filepath.Join(alias, "image"), filepath.Join(alias, "scratch")
			case "public-parent":
				if os.Chmod(parent, 0o755) != nil {
					t.Fatal("fixture")
				}
			}
			if _, err := linuxEpochCandidateShape(request, scratch); err == nil {
				t.Fatal("unsafe supplied candidate shape accepted")
			}
		})
	}
}
