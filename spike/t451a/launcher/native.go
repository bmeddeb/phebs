package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

const (
	NativeDriverSHA256   = "f49a0ff4339e32cc699c6fbb5a80b9d8f936b3bfe6b08a924fa19495e35b2bfd"
	nativeGoFilesVersion = "phebs-t451b-native-go-files-v1"
	nativeScopeVersion   = "phebs-t451b-native-driver-scope-v1"
	nativeDescriptorFlag = "--experimental_proto_descriptor_sets_include_source_info"
)

// NativeGoFiles supplements the unchanged declared-source plan with the exact
// active GoFiles expected from rules_go 0.59.0. It grants no new membership.
type NativeGoFiles struct {
	Version         string                 `json:"version"`
	MappingSHA256   string                 `json:"mapping_sha256"`
	DocumentsSHA256 string                 `json:"documents_sha256"`
	Packages        []NativeGoFilesPackage `json:"packages"`
}

type NativeGoFilesPackage struct {
	ID      string   `json:"id"`
	GoFiles []string `json:"go_files"`
}

type nativeScope struct {
	Version         string   `json:"version"`
	Slot            string   `json:"slot"`
	MappingSHA256   string   `json:"mapping_sha256"`
	DocumentsSHA256 string   `json:"documents_sha256"`
	GoFilesSHA256   string   `json:"go_files_sha256"`
	Roots           []string `json:"roots"`
	Patterns        []string `json:"patterns"`
}

// NativeBazelCommon preserves the target's locked HTTPS registry identities.
// All HTTP inputs come from the imported read-only cache; the sealed downloader
// configuration blocks every cache miss. No cache is shared with the host.
func NativeBazelCommon() []string {
	args := BazelCommon()
	for i, arg := range args {
		switch arg {
		case "--repository_cache=/scratch/repository-cache":
			args[i] = "--repository_cache=/inputs/tools/cache/repository"
		case "--registry=file:///inputs/tools/cache/registry":
			args[i] = "--registry=https://bcr.bazel.build"
		}
	}
	return append(args, "--lockfile_mode=error", "--downloader_config=/inputs/tools/cache/downloader.cfg", "--repo_contents_cache=", "--experimental_repository_cache_hardlinks=false")
}

func nativeCommandPrefix(command string) []string {
	args := append(BazelStartup(), command, "--tool_tag=gopackagesdriver", "--ui_actions_shown=0")
	return append(args, NativeBazelCommon()...)
}

func nativePaths(slot string) (string, string, error) {
	if _, _, err := compatibilityPaths(slot); err != nil {
		return "", "", err
	}
	return "/scratch/t451b-native-" + slot + "-scope.json", "/scratch/t451b-native-" + slot + "-bazel", nil
}

func nativePackageIDs(p Prepared) []string {
	ids := make([]string, 0, len(p.packages))
	for id := range p.packages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// validateNativeGoFiles is deliberately structural: host receipt validation has
// no source custody. Runtime recomputes every match from sealed bytes before it
// allows these rows to constrain the driver response.
func validateNativeGoFiles(p Prepared, plan planner.Plan, matches NativeGoFiles) error {
	if matches.Version != nativeGoFilesVersion || matches.MappingSHA256 != plan.MappingSHA256 || matches.DocumentsSHA256 != plan.DocumentsSHA256 || len(matches.Packages) != len(p.packages) {
		return errors.New("native GoFiles sidecar authority mismatch")
	}
	ids := nativePackageIDs(p)
	for i, row := range matches.Packages {
		if row.ID != ids[i] || row.GoFiles == nil || len(row.GoFiles) > len(p.packages[row.ID].GoFiles) {
			return errors.New("native GoFiles package inventory mismatch")
		}
		declared := p.packages[row.ID].GoFiles
		for j, name := range row.GoFiles {
			if _, found := slices.BinarySearch(declared, name); !found || (j > 0 && name <= row.GoFiles[j-1]) {
				return errors.New("native GoFiles source membership mismatch")
			}
		}
	}
	data, err := json.Marshal(matches)
	if err != nil {
		return err
	}
	if len(data) > MaxResponseBytes {
		return errors.New("native GoFiles sidecar byte bound")
	}
	return nil
}

func matchNativeGoFile(name string, data []byte, mode planner.GoMode) (bool, error) {
	base := path.Base(name)
	// These are exactly the 0.59 driver's extensionless-cache and cgo output
	// exceptions. Their bytes remain sealed, including when MatchFile skips them.
	if path.Ext(base) == "" || base == "_cgo_gotypes.go" || base == "_cgo_imports.go" || strings.HasSuffix(base, ".cgo1.go") {
		return true, nil
	}
	return planner.MatchGoFile(name, data, mode)
}

func sealNativeGoFiles(ctx context.Context, p Prepared, plan planner.Plan) (NativeGoFiles, error) {
	matches := NativeGoFiles{Version: nativeGoFilesVersion, MappingSHA256: plan.MappingSHA256, DocumentsSHA256: plan.DocumentsSHA256, Packages: make([]NativeGoFilesPackage, 0, len(p.packages))}
	active := make(map[string]bool)
	for _, pkg := range p.packages {
		for _, name := range pkg.GoFiles {
			active[name] = false
		}
	}
	total := 0
	for name, want := range p.files {
		if err := ctx.Err(); err != nil {
			return NativeGoFiles{}, err
		}
		data, err := readFile(name, min(planner.MaxFileBytes, planner.MaxProtoBytes-total))
		if err != nil {
			return NativeGoFiles{}, err
		}
		total += len(data)
		if len(data) != want.bytes || hash(data) != want.digest {
			return NativeGoFiles{}, errors.New("sealed document changed before native GoFiles selection")
		}
		if _, needed := active[name]; needed {
			active[name], err = matchNativeGoFile(name, data, p.mode)
			if err != nil {
				return NativeGoFiles{}, fmt.Errorf("native GoFiles constraint: %w", err)
			}
		}
	}
	for _, id := range nativePackageIDs(p) {
		row := NativeGoFilesPackage{ID: id, GoFiles: []string{}}
		for _, name := range p.packages[id].GoFiles {
			if active[name] {
				row.GoFiles = append(row.GoFiles, name)
			}
		}
		matches.Packages = append(matches.Packages, row)
	}
	if err := validateNativeGoFiles(p, plan, matches); err != nil {
		return NativeGoFiles{}, err
	}
	return matches, ctx.Err()
}

// SealNativeGoFiles hashes every declared selected source and derives the
// native driver's active-file rows without changing plan-v1 or its documents.
func SealNativeGoFiles(ctx context.Context, plan planner.Plan, roots []planner.Configured) (NativeGoFiles, error) {
	p, err := PrepareCompatibility(plan, roots, "load")
	if err != nil {
		return NativeGoFiles{}, err
	}
	return sealNativeGoFiles(ctx, p, plan)
}

// PrepareNativeCompatibility structurally binds a sidecar to the exact plan.
// RunNativeCompatibility independently recomputes it before driver execution.
func PrepareNativeCompatibility(plan planner.Plan, roots []planner.Configured, slot string, matches NativeGoFiles) (Prepared, error) {
	_, wrapper, err := nativePaths(slot)
	if err != nil {
		return Prepared{}, err
	}
	p, err := PrepareCompatibility(plan, roots, slot)
	if err != nil {
		return Prepared{}, err
	}
	if err := validateNativeGoFiles(p, plan, matches); err != nil {
		return Prepared{}, err
	}
	for _, row := range matches.Packages {
		pkg := p.packages[row.ID]
		pkg.GoFiles = slices.Clone(row.GoFiles)
		p.packages[row.ID] = pkg
	}
	matchesSHA256 := jsonHash(matches)
	p.scope, err = json.Marshal(nativeScope{nativeScopeVersion, slot, plan.MappingSHA256, plan.DocumentsSHA256, matchesSHA256, p.roots, p.patterns})
	if err != nil {
		return Prepared{}, err
	}
	if len(p.scope) > 1<<20 {
		return Prepared{}, errors.New("native scope byte bound")
	}
	for i, value := range p.environment {
		switch {
		case strings.HasPrefix(value, "GOPACKAGESDRIVER_BAZEL="):
			p.environment[i] = "GOPACKAGESDRIVER_BAZEL=" + wrapper
		case strings.HasPrefix(value, "GOPACKAGESDRIVER_BAZEL_COMMON_FLAGS="):
			p.environment[i] = "GOPACKAGESDRIVER_BAZEL_COMMON_FLAGS=" + strings.Join(NativeBazelCommon(), " ")
		case strings.HasPrefix(value, "GOPACKAGESDRIVER_BAZEL_BUILD_FLAGS="):
			p.environment[i] = value + " " + nativeDescriptorFlag
		case strings.HasPrefix(value, ScopeDigestEnv+"="):
			p.environment[i] = ScopeDigestEnv + "=" + hash(p.scope)
		}
	}
	p.digest = jsonHash(struct {
		Version, Slot, Mapping, Documents, GoFiles string
		Roots                                      []planner.Configured
		Patterns, Environment                      []string
		Request                                    string
	}{nativeScopeVersion, slot, plan.MappingSHA256, plan.DocumentsSHA256, matchesSHA256, roots, p.patterns, p.environment, string(p.request)})
	return p, nil
}

func inspectNativeScope(data []byte, want, slot string) (scope, error) {
	if _, _, err := nativePaths(slot); err != nil {
		return scope{}, err
	}
	if len(data) == 0 || len(data) > 1<<20 || !compatibilityDigest(want) || hash(data) != want {
		return scope{}, errors.New("native scope byte/digest mismatch")
	}
	if err := uniqueJSON(data); err != nil {
		return scope{}, err
	}
	var s nativeScope
	if err := planner.ValidateJSONFields(data, &s); err != nil {
		return scope{}, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return scope{}, err
	}
	canonical, err := json.Marshal(s)
	if err != nil {
		return scope{}, err
	}
	if !bytes.Equal(data, canonical) || s.Version != nativeScopeVersion || s.Slot != slot || !compatibilityDigest(s.GoFilesSHA256) {
		return scope{}, errors.New("native scope shape")
	}
	// Reuse the closed root/pattern/seal validator, with only the separately
	// checked native fields translated to its existing scope representation.
	common, err := json.Marshal(compatibilityScope{compatibilityScopeVersion, slot, s.MappingSHA256, s.DocumentsSHA256, s.Roots, s.Patterns})
	if err != nil {
		return scope{}, err
	}
	return inspectCompatibilityScope(common, hash(common), slot)
}

func nativeQueryArgs(s scope) []string {
	args := queryArgs(s)
	args = append(nativeCommandPrefix("query"), args[len(commandPrefix("query")):]...)
	expr := "set(" + strings.Join(s.Roots, " ") + ")"
	terms := make([]string, 0, len(s.Patterns))
	for _, pattern := range s.Patterns {
		terms = append(terms, fmt.Sprintf(`kind("^(go_library) rule$", attr(importpath, "%s", deps(%s)))`, pattern, expr))
	}
	args[len(args)-1] = strings.Join(terms, " union ")
	return args
}

func nativeBuildArgs(s scope, bep string) []string {
	args := buildArgs(s, bep)
	args = append(nativeCommandPrefix("build"), args[len(commandPrefix("build")):]...)
	for i, arg := range args {
		if strings.HasPrefix(arg, "--output_groups=") {
			args[i] = compatibilityGroups
		}
	}
	return slices.Insert(args, len(args)-len(s.Roots), nativeDescriptorFlag)
}

func inspectNativeBazel(s scope, args []string) ([]byte, []string, error) {
	if slices.Equal(args, nativeCommandPrefix("info")) {
		return []byte("release: release 9.0.0\nexecution_root: " + ExecRoot + "\noutput_base: " + OutputBase + "\noutput_path: " + ExecRoot + "/bazel-out\n"), nil, nil
	}
	if slices.Equal(args, nativeQueryArgs(s)) {
		return []byte(strings.Join(s.Roots, "\n") + "\n"), nil, nil
	}
	var bep string
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--build_event_json_file="); ok {
			if bep != "" || !bepPattern.MatchString(value) {
				return nil, nil, errors.New("unclosed native driver BEP path")
			}
			bep = value
		}
	}
	if bep == "" || !slices.Equal(args, nativeBuildArgs(s, bep)) {
		return nil, nil, errors.New("native driver Bazel argv differs from sealed roots/profile")
	}
	return nil, slices.Clone(args), nil
}

// RunNativeCompatibilityBazel accepts only the distinct native wrapper slot.
// Its literal-import query is synthetic and cannot expand the sealed roots.
func RunNativeCompatibilityBazel(ctx context.Context, slot string, args []string) error {
	scopePath, _, err := nativePaths(slot)
	if err != nil {
		return err
	}
	data, err := readFile(scopePath, 1<<20)
	if err != nil {
		return err
	}
	s, err := inspectNativeScope(data, os.Getenv(ScopeDigestEnv), slot)
	if err != nil {
		return err
	}
	output, argv, err := inspectNativeBazel(s, args)
	if err != nil {
		return err
	}
	return runBazelCommand(ctx, output, argv)
}

func verifyNativeGoFiles(ctx context.Context, p Prepared, plan planner.Plan, matches NativeGoFiles) error {
	actual, err := sealNativeGoFiles(ctx, p, plan)
	if err != nil {
		return err
	}
	if jsonHash(actual) != jsonHash(matches) {
		return errors.New("native GoFiles differ from independently sealed source constraints")
	}
	return nil
}

// RunNativeCompatibility rederives every active source before executing the
// fixed native driver, then retains the existing exact raw/export/source checks.
func RunNativeCompatibility(ctx context.Context, plan planner.Plan, roots []planner.Configured, slot string, matches NativeGoFiles) (CompatibilityResult, error) {
	p, err := PrepareNativeCompatibility(plan, roots, slot, matches)
	if err != nil {
		return CompatibilityResult{}, err
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Getuid() != 65534 {
		return CompatibilityResult{}, errors.New("native compatibility driver requires admitted Linux arm64 worker")
	}
	if err := checkPinnedDriver(NativeDriverSHA256); err != nil {
		return CompatibilityResult{}, err
	}
	declared, err := PrepareCompatibility(plan, roots, slot)
	if err != nil {
		return CompatibilityResult{}, err
	}
	if err := verifyNativeGoFiles(ctx, declared, plan, matches); err != nil {
		return CompatibilityResult{}, err
	}
	scopePath, wrapper, _ := nativePaths(slot)
	if err := writeClosedFile(scopePath, p.scope, 0400); err != nil {
		return CompatibilityResult{}, err
	}
	if err := writeClosedFile(wrapper, []byte("#!/bin/sh\nexec /inputs/t451a __native_bazel "+slot+" \"$@\"\n"), 0500); err != nil {
		return CompatibilityResult{}, err
	}
	data, err := runClosedChild(ctx, DriverPath, p.patterns, p.environment, p.request)
	if err != nil {
		return CompatibilityResult{}, err
	}
	return finishCompatibility(ctx, p, data)
}

// VerifyNativeCompatibilityFiles rechecks the complete original source
// inventory, including inactive declared files, and exact exports after a client.
func VerifyNativeCompatibilityFiles(ctx context.Context, plan planner.Plan, roots []planner.Configured, slot string, matches NativeGoFiles, exports []ExportArtifact) error {
	p, err := PrepareNativeCompatibility(plan, roots, slot, matches)
	if err != nil {
		return err
	}
	return verifyCompatibilityFiles(ctx, p, exports)
}
