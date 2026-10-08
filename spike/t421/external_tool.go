package t421

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// ObserveExecutionExternalTool measures one explicitly selected, trusted host
// tool on an admitted host: Darwin/arm64 or Linux/amd64. Both hosts run the same
// closed git and go child-probe recipe and the same fixed-system roles (sh,
// hdiutil, ssh-keygen), which run no version child; Linux resolves hdiutil as
// absent because its pressure adapter is a separate unimplemented prerequisite.
// Linux keeps exactly one admitted SurrealDB route, the sealed custody in
// ProtectLinuxExecutionExternalTool, so this public-path observer refuses that
// role there instead of issuing a second, weaker identity for the same bytes.
// Fixed-system roles compare resolved paths, so the packaged Linux /bin/sh
// symlink observes its resolved shell image while an absent /usr/bin/hdiutil
// refuses; HoldExecutionSystemTool keeps the separate literal-path rule.
// Version probes execute that selected image; this is not a sandbox for
// untrusted programs or vendor attestation.
// Native-image headers and Git core equality reject scripts and a delegating
// shim, but do not prove arbitrary native delegation or helper closure. On
// Linux/amd64 the two delegation locations this identity cannot see are
// observed separately and structurally: ObserveExecutionGitExecPathManifest
// bounds Git's exec-path helper census and binds its core helper to the
// selected image, while ObserveExecutionGoSDKLocation bounds the GOROOT,
// GOROOT/bin and GOTOOLDIR shapes an explicitly selected Go image reports for
// itself. Neither hashes a tree and neither is admitted on Darwin, so both
// locations stay unadmitted there. They add no field to this identity and
// widen nothing here, so an observed identity is never a launch or dispatch
// recipe.
// This observation issues no CheckoutAdmissionBinding or launch authority, and
// validateExecutionHost remains the separate freeze-platform fence.
func ObserveExecutionExternalTool(ctx context.Context, role, binary string) (identity ExecutionToolIdentity, retErr error) {
	if ctx == nil || ((runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") &&
		(runtime.GOOS != "linux" || runtime.GOARCH != "amd64")) {
		return identity, errors.New("external tool observation requires a context and the frozen host platform")
	}
	var arguments []string
	var systemPath string
	switch role {
	case "git":
		arguments = []string{"--version"}
	case "go", "surreal":
		arguments = []string{"version"}
	case "hdiutil", "ssh-keygen":
		systemPath = "/usr/bin/" + role
	case "sh":
		systemPath = "/bin/sh"
	default:
		return identity, errors.New("external tool role is not in the implemented inventory")
	}
	// Linux admits an allowlist of the roles that carry an explicit decision here,
	// not a denial of one named role, so a future probed role fails closed instead
	// of being silently observed with no platform decision. SurrealDB keeps its
	// single admitted Linux route in ProtectLinuxExecutionExternalTool.
	if runtime.GOOS == "linux" {
		switch role {
		case "surreal":
			return identity, errors.New("external SurrealDB observation requires its sealed Linux custody")
		case "git", "go", "sh", "ssh-keygen", "hdiutil":
			// The two shared child-probe recipes and the three fixed-system images
			// that run no version child.
		default:
			return identity, errors.New("external tool role has no admitted Linux observation")
		}
	}
	if !filepath.IsAbs(binary) || strings.TrimSpace(binary) != binary {
		return identity, errors.New("external tool requires an explicit absolute image path")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return identity, errors.New("external tool observation canceled")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return identity, errors.New("external tool image cannot be resolved")
	}
	if systemPath != "" {
		resolved, resolveErr := filepath.EvalSymlinks(systemPath)
		if resolveErr != nil || binary != resolved {
			return identity, errors.New("external system tool is not the fixed platform image")
		}
	}
	digest, err := t4013.DigestHostExecutable(ctx, binary)
	if err != nil || validateExternalToolNativeImage(binary) != nil {
		return identity, errors.New("external tool image is not a bounded native executable")
	}
	verify := func(path string) bool {
		observed, hashErr := t4013.DigestHostExecutable(ctx, path)
		return hashErr == nil && observed == digest
	}
	version := "bound executable"
	if len(arguments) != 0 {
		preparationParent := executionPreparationParent(ctx)
		workspace, createErr := os.MkdirTemp(preparationParent, "phebs-t422-external-")
		if createErr != nil {
			return identity, errors.New("external tool cannot create private probe directory")
		}
		defer func() {
			// A volume-owned preparation retains even failed probe scratch until
			// the owner's non-forced detach. Ordinary observation stays unchanged.
			if preparationParent == "" && os.RemoveAll(workspace) != nil {
				retErr = errors.Join(retErr, errors.New("external tool private probe cleanup failed"))
			}
			if retErr != nil {
				identity = ExecutionToolIdentity{}
			}
		}()
		resolved, resolveErr := filepath.EvalSymlinks(workspace)
		if resolveErr != nil {
			return identity, errors.New("external tool cannot resolve private probe directory")
		}
		workspace = resolved
		environment := externalToolEnvironment(workspace)
		run := func(args ...string) (string, error) {
			if !verify(binary) {
				return "", errors.New("external tool image changed before probe")
			}
			return runExternalToolProbe(ctx, workspace, binary, environment, args...)
		}
		version, err = run(arguments...)
		if err != nil || !validPublicToolVersion(version) {
			return identity, errors.New("external tool version probe failed or was not source-free")
		}
		switch role {
		case "git":
			if !strings.HasPrefix(version, "git version ") || len(strings.TrimPrefix(version, "git version ")) == 0 {
				return identity, errors.New("external Git version is invalid")
			}
			coreDirectory, probeErr := run("--exec-path")
			if probeErr != nil || !filepath.IsAbs(coreDirectory) || strings.ContainsAny(coreDirectory, "\r\n\x00") {
				return identity, errors.New("external Git core directory is invalid")
			}
			core, resolveErr := filepath.EvalSymlinks(filepath.Join(coreDirectory, "git"))
			if resolveErr != nil || !verify(core) {
				return identity, errors.New("external Git requires the actual core image, not a delegating shim")
			}
		case "go":
			if version != "go version "+runtime.Version()+" "+runtime.GOOS+"/"+runtime.GOARCH {
				return identity, errors.New("external Go version differs from the verifier toolchain")
			}
		case "surreal":
			fields := strings.Fields(version)
			if len(fields) == 0 || store.ValidateSurrealVersionToken(fields[0]) != nil {
				return identity, errors.New("external SurrealDB version is unsupported")
			}
		}
	}
	if !verify(binary) || ctx.Err() != nil {
		return identity, errors.New("external tool image changed or observation expired")
	}
	return ExecutionToolIdentity{Role: role, FileType: regularFileType, SHA256: digest,
		Version: version, Provenance: "external-executed-file-v1"}, nil
}

func externalToolEnvironment(workspace string) []string {
	return []string{
		"HOME=" + workspace, "TMPDIR=" + workspace, "TMP=" + workspace, "TEMP=" + workspace,
		"PATH=" + workspace, "LANG=C", "LC_ALL=C", "TZ=UTC",
		"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOTELEMETRY=off",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
	}
}

func runExternalToolProbe(ctx context.Context, root, binary string, environment []string, args ...string) (string, error) {
	if ctx.Err() != nil {
		return "", errors.New("external tool probe canceled")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stdout, stderr := checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}, checkoutCommandOutput{remaining: 4 << 10, cancel: cancel}
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir, command.Env = root, environment
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = time.Second
	if err := runReferenceCommand(ctx, command); err != nil || ctx.Err() != nil || stdout.err != nil || stderr.err != nil || stderr.buffer.Len() != 0 {
		return "", errors.New("external tool probe failed, expired, or exceeded output bound")
	}
	return strings.TrimSuffix(stdout.buffer.String(), "\n"), nil
}
