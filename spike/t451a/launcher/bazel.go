package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

const (
	BazelWrapperPath = "/scratch/driver-bazel"
	BazelWrapper     = "#!/bin/sh\nexec /inputs/t451a __driver_bazel \"$@\"\n"
	ScopePath        = "/scratch/driver-scope.json"
	ScopeDigestEnv   = "PHEBS_DRIVER_SCOPE_SHA256"
	ToolPath         = "/inputs/tools/bin:/scratch/toolchain/usr/bin:/usr/bin:/bin"
	LibraryPath      = "/scratch/toolchain/usr/lib/aarch64-linux-gnu:/scratch/toolchain/lib/aarch64-linux-gnu"
	GCOVPath         = "/scratch/toolchain/usr/bin/gcov-12"
)

type scope struct {
	Version         string   `json:"version"`
	MappingSHA256   string   `json:"mapping_sha256"`
	DocumentsSHA256 string   `json:"documents_sha256"`
	Roots           []string `json:"roots"`
	Patterns        []string `json:"patterns"`
}

func scopeBytes(mapping, documents string, roots, patterns []string) []byte {
	b, _ := json.Marshal(scope{"phebs-t451a-driver-scope-v1", mapping, documents, roots, patterns})
	return b
}

func BazelStartup() []string {
	return []string{"--batch", "--ignore_all_rc_files", "--output_user_root=/scratch/bazel-user", "--output_base=" + OutputBase, "--host_jvm_args=-Xmx256m", "--host_jvm_args=-XX:ActiveProcessorCount=1", "--noautodetect_server_javabase", "--host_jvm_args=-Djdk.virtualThreadScheduler.parallelism=1", "--host_jvm_args=-Djdk.virtualThreadScheduler.maxPoolSize=8"}
}
func BazelCommon() []string {
	return []string{"--repository_cache=/scratch/repository-cache", "--distdir=/inputs/tools/cache/distdir", "--registry=file:///inputs/tools/cache/registry", "--spawn_strategy=local", "--jobs=1", "--loading_phase_threads=1", "--noexperimental_merged_skyframe_analysis_execution", "--remote_cache=", "--remote_executor=", "--repo_env=CC=/scratch/toolchain/cc", "--repo_env=GOPROXY=file:///inputs/tools/cache/goproxy", "--repo_env=GOSUMDB=off", "--repo_env=GOTOOLCHAIN=local", "--repo_env=GOROOT=/inputs/tools/go", "--repo_env=PATH=" + ToolPath, "--repo_env=LD_LIBRARY_PATH=" + LibraryPath, "--repo_env=GCOV=" + GCOVPath, "--action_env=PATH=" + ToolPath, "--action_env=LD_LIBRARY_PATH=" + LibraryPath, "--action_env=GCOV=" + GCOVPath}
}
func BazelBuild() []string {
	return []string{"--@protobuf//bazel/toolchains:prefer_prebuilt_protoc"}
}

func commandPrefix(command string) []string {
	args := append(BazelStartup(), command, "--tool_tag=gopackagesdriver", "--ui_actions_shown=0")
	return append(args, BazelCommon()...)
}

func queryArgs(s scope) []string {
	scopeExpr := "set(" + strings.Join(s.Roots, " ") + ")"
	var terms []string
	for _, pattern := range s.Patterns {
		terms = append(terms, fmt.Sprintf(`kind("^(go_library) rule$", attr(importpath, "%s", deps(%s)))`, regexp.QuoteMeta(pattern), scopeExpr))
	}
	return append(commandPrefix("query"), "--consistent_labels", "--ui_event_filters=-info,-stderr", "--noshow_progress", "--order_output=no", "--output=label", "--nodep_deps", "--noimplicit_deps", "--notool_deps", strings.Join(terms, " union "))
}

func buildArgs(s scope, bep string) []string {
	args := append(commandPrefix("build"), "--show_result=0", "--build_event_json_file="+bep, "--build_event_json_file_path_conversion=no",
		"--experimental_convenience_symlinks=ignore", "--ui_event_filters=-info,-stderr", "--noshow_progress",
		"--aspects=@rules_go//go/tools/gopackagesdriver:aspect.bzl%go_pkg_info_aspect",
		"--output_groups=go_pkg_driver_json_file,go_pkg_driver_stdlib_json_file,go_pkg_driver_stdlib_cache_dir,go_pkg_driver_srcs", "--keep_going")
	args = append(args, BazelBuild()...)
	return append(args, s.Roots...)
}

var bepPattern = regexp.MustCompile(`^/scratch/tmp/gopackagesdriver_bep_[0-9]+$`)

// inspectBazel accepts exactly the three invocation shapes emitted by the
// pinned driver. Its query result is the pre-sealed root list, never a Bazel
// unconfigured-query expansion of select branches.
func inspectBazel(s scope, args []string) ([]byte, []string, error) {
	if slices.Equal(args, commandPrefix("info")) {
		return []byte("release: release 9.2.0\nexecution_root: " + ExecRoot + "\noutput_base: " + OutputBase + "\noutput_path: " + ExecRoot + "/bazel-out\n"), nil, nil
	}
	if slices.Equal(args, queryArgs(s)) {
		return []byte(strings.Join(s.Roots, "\n") + "\n"), nil, nil
	}
	var bep string
	for _, arg := range args {
		if v, ok := strings.CutPrefix(arg, "--build_event_json_file="); ok {
			if bep != "" || !bepPattern.MatchString(v) {
				return nil, nil, errors.New("unclosed driver BEP path")
			}
			bep = v
		}
	}
	if bep == "" || !slices.Equal(args, buildArgs(s, bep)) {
		return nil, nil, errors.New("driver Bazel argv differs from sealed roots/profile")
	}
	return nil, append([]string{}, args...), nil
}

func readScope() (scope, error) {
	want := os.Getenv(ScopeDigestEnv)
	if len(want) != 64 {
		return scope{}, errors.New("missing launcher scope digest")
	}
	data, err := readFile(ScopePath, 1<<20)
	if err != nil {
		return scope{}, err
	}
	if hash(data) != want {
		return scope{}, errors.New("launcher scope digest mismatch")
	}
	if err := uniqueJSON(data); err != nil {
		return scope{}, err
	}
	var s scope
	if err := planner.ValidateJSONFields(data, &s); err != nil {
		return scope{}, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return scope{}, err
	}
	canonical, err := json.Marshal(s)
	if err != nil {
		return scope{}, err
	}
	if !bytes.Equal(data, canonical) {
		return scope{}, errors.New("noncanonical launcher scope")
	}
	if s.Version != "phebs-t451a-driver-scope-v1" || len(s.MappingSHA256) != 64 || len(s.DocumentsSHA256) != 64 || len(s.Roots) == 0 || len(s.Roots) > 64 || len(s.Patterns) != len(s.Roots) {
		return scope{}, errors.New("launcher scope shape")
	}
	for i, root := range s.Roots {
		if !labelPattern.MatchString(root) || (i > 0 && root <= s.Roots[i-1]) {
			return scope{}, errors.New("launcher scope root identity")
		}
	}
	for i, pattern := range s.Patterns {
		if !validImportPattern(pattern) || (i > 0 && pattern <= s.Patterns[i-1]) {
			return scope{}, errors.New("launcher scope import identity")
		}
	}
	return s, nil
}

// RunBazel is dispatched only by the pinned runner's __driver_bazel command.
// It rechecks canonical scope bytes/digest on every invocation. Its sole real
// child is the fixed Bazel binary with the exact validated argument vector.
func RunBazel(ctx context.Context, args []string) error {
	s, err := readScope()
	if err != nil {
		return err
	}
	output, argv, err := inspectBazel(s, args)
	if err != nil {
		return err
	}
	if argv == nil {
		_, err = os.Stdout.Write(output)
		return err
	}
	for _, arg := range argv {
		if bep, ok := strings.CutPrefix(arg, "--build_event_json_file="); ok {
			info, err := os.Lstat(bep)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() != 0 {
				return errors.New("driver BEP custody mismatch")
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, MaxWall)
	defer cancel()
	command := exec.CommandContext(ctx, "/inputs/tools/bin/bazel", argv...)
	command.Dir = Workspace
	command.Env = BazelEnvironment()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func writeClosedFile(name string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Close())
}

func validImportPattern(pattern string) bool {
	return pattern != "" && len(pattern) <= 4096 && !strings.ContainsAny(pattern, "\x00\r\n\"\\") && !strings.HasSuffix(pattern, ".go") && !strings.HasSuffix(pattern, "/...")
}

type Invocation struct {
	Executable    string   `json:"executable"`
	Directory     string   `json:"directory"`
	Arguments     []string `json:"arguments"`
	Environment   []string `json:"environment"`
	RequestSHA256 string   `json:"request_sha256"`
	ScopeSHA256   string   `json:"scope_sha256"`
}

func (p Prepared) Invocation() Invocation {
	return Invocation{DriverPath, Workspace, slices.Clone(p.patterns), slices.Clone(p.environment), hash(p.request), hash(p.scope)}
}
