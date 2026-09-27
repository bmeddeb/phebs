package launcher

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
)

const (
	CompatibilityMode            = 8681
	compatibilityRequest         = `{"mode":8681,"env":[],"build_flags":null,"tests":false,"overlay":null}`
	compatibilityScopeVersion    = "phebs-t451b-driver-scope-v1"
	compatibilityGroups          = "--output_groups=go_pkg_driver_json_file,go_pkg_driver_stdlib_json_file,go_pkg_driver_stdlib_cache_dir,go_pkg_driver_srcs,go_pkg_driver_export_file"
	maxCompatibilityPatternBytes = 16383
)

// ExportArtifact seals bytes materialized by the typed output group. Its path
// must already occur in the selected plan; this inventory grants no membership.
type ExportArtifact struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type CompatibilityResult struct {
	DriverResponse []byte           `json:"driver_response"`
	Response       []byte           `json:"response"`
	Exports        []ExportArtifact `json:"exports"`
}

type compatibilityScope struct {
	Version         string   `json:"version"`
	Slot            string   `json:"slot"`
	MappingSHA256   string   `json:"mapping_sha256"`
	DocumentsSHA256 string   `json:"documents_sha256"`
	Roots           []string `json:"roots"`
	Patterns        []string `json:"patterns"`
}

func compatibilityPaths(slot string) (string, string, error) {
	if slot != "load" && slot != "scip" {
		return "", "", errors.New("unsupported compatibility slot")
	}
	return "/scratch/t451b-" + slot + "-scope.json", "/scratch/t451b-" + slot + "-bazel", nil
}

func compatibilityPatterns(patterns []string) error {
	total := 0
	for _, pattern := range patterns {
		if !validImportPattern(pattern) || len(pattern)+1 > maxCompatibilityPatternBytes-total {
			return errors.New("compatibility pattern chunk bound")
		}
		total += len(pattern) + 1
	}
	return nil
}

// PrepareCompatibility derives the T45.1b typed profile from the unchanged
// T45.1a authority. The only client legs are the fixed load and scip slots.
func PrepareCompatibility(plan planner.Plan, roots []planner.Configured, slot string) (Prepared, error) {
	_, wrapper, err := compatibilityPaths(slot)
	if err != nil {
		return Prepared{}, err
	}
	p, err := Prepare(plan, roots)
	if err != nil {
		return Prepared{}, err
	}
	if err := compatibilityPatterns(p.patterns); err != nil {
		return Prepared{}, err
	}
	// Prepare checks selected archive modes. Typed metadata additionally needs
	// an exact SDK for every selected archive, including those with no stdlib
	// imports, so a missing SDK cannot silently inherit a Go language version.
	sdks := make(map[string]planner.SDKPlan, len(plan.SDKs))
	for _, sdk := range plan.SDKs {
		sdks[sdk.ID] = sdk
	}
	units := make(map[string]planner.Unit, len(plan.Units))
	for _, unit := range plan.Units {
		units[unit.ID] = unit
	}
	selectedRoots := make(map[planner.Configured]bool, len(roots))
	for _, root := range roots {
		selectedRoots[root] = true
	}
	var pending []string
	for _, target := range plan.Targets {
		if selectedRoots[target.Configured] {
			pending = append(pending, target.Units...)
		}
	}
	seen := make(map[string]bool)
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		unit := units[id] // Prepare has already validated this exact closure.
		sdk, ok := sdks[unit.SDK]
		if !ok || sdk.Version != "1.25.0" || jsonHash(sdk.Mode) != jsonHash(unit.Mode) {
			return Prepared{}, fmt.Errorf("%w: compatibility requires uniform Go 1.25.0 SDK authority", ErrUnrepresentable)
		}
		for _, im := range unit.Imports {
			pending = append(pending, im.Unit)
		}
	}
	p.scope, err = json.Marshal(compatibilityScope{compatibilityScopeVersion, slot, plan.MappingSHA256, plan.DocumentsSHA256, p.roots, p.patterns})
	if err != nil {
		return Prepared{}, err
	}
	if len(p.scope) > 1<<20 {
		return Prepared{}, errors.New("compatibility scope byte bound")
	}
	for i, value := range p.environment {
		switch {
		case strings.HasPrefix(value, "GOPACKAGESDRIVER_BAZEL="):
			p.environment[i] = "GOPACKAGESDRIVER_BAZEL=" + wrapper
		case strings.HasPrefix(value, ScopeDigestEnv+"="):
			p.environment[i] = ScopeDigestEnv + "=" + hash(p.scope)
		}
	}
	p.request = []byte(compatibilityRequest)
	p.digest = jsonHash(struct {
		Version, Slot, Mapping, Documents string
		Roots                             []planner.Configured
		Patterns, Environment             []string
		Request                           string
	}{compatibilityScopeVersion, slot, plan.MappingSHA256, plan.DocumentsSHA256, roots, p.patterns, p.environment, string(p.request)})
	return p, nil
}

func inspectCompatibilityScope(data []byte, want, slot string) (scope, error) {
	if _, _, err := compatibilityPaths(slot); err != nil {
		return scope{}, err
	}
	if len(data) == 0 || len(data) > 1<<20 || len(want) != 64 || hash(data) != want {
		return scope{}, errors.New("compatibility scope byte/digest mismatch")
	}
	if err := uniqueJSON(data); err != nil {
		return scope{}, err
	}
	var s compatibilityScope
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
	if !bytes.Equal(data, canonical) || s.Version != compatibilityScopeVersion || s.Slot != slot || !compatibilityDigest(s.MappingSHA256) || !compatibilityDigest(s.DocumentsSHA256) || len(s.Roots) == 0 || len(s.Roots) > 64 || len(s.Patterns) != len(s.Roots) {
		return scope{}, errors.New("compatibility scope shape")
	}
	for i, root := range s.Roots {
		if !labelPattern.MatchString(root) || (i > 0 && root <= s.Roots[i-1]) {
			return scope{}, errors.New("compatibility scope root identity")
		}
	}
	if err := compatibilityPatterns(s.Patterns); err != nil {
		return scope{}, err
	}
	for i := 1; i < len(s.Patterns); i++ {
		if s.Patterns[i] <= s.Patterns[i-1] {
			return scope{}, errors.New("compatibility scope import identity")
		}
	}
	return scope{s.Version, s.MappingSHA256, s.DocumentsSHA256, s.Roots, s.Patterns}, nil
}

func inspectCompatibilityBazel(s scope, args []string) ([]byte, []string, error) {
	if slices.Equal(args, commandPrefix("info")) || slices.Equal(args, queryArgs(s)) {
		return inspectBazel(s, args)
	}
	// Reuse the exact original build oracle after translating the single new
	// output-group spelling. Nothing else in the driver profile is extended.
	old := slices.Clone(args)
	groups := 0
	for i, arg := range old {
		if arg == compatibilityGroups {
			old[i] = strings.TrimSuffix(arg, ",go_pkg_driver_export_file")
			groups++
		}
	}
	if groups != 1 {
		return nil, nil, errors.New("compatibility output groups differ from typed profile")
	}
	if _, _, err := inspectBazel(s, old); err != nil {
		return nil, nil, err
	}
	return nil, slices.Clone(args), nil
}

// RunCompatibilityBazel is dispatched only by the fixed __compat_bazel slot
// wrapper. Every call rechecks the exact slot, canonical scope and build shape.
func RunCompatibilityBazel(ctx context.Context, slot string, args []string) error {
	scopePath, _, err := compatibilityPaths(slot)
	if err != nil {
		return err
	}
	data, err := readFile(scopePath, 1<<20)
	if err != nil {
		return err
	}
	s, err := inspectCompatibilityScope(data, os.Getenv(ScopeDigestEnv), slot)
	if err != nil {
		return err
	}
	output, argv, err := inspectCompatibilityBazel(s, args)
	if err != nil {
		return err
	}
	return runBazelCommand(ctx, output, argv)
}

// RunCompatibility runs only the fixed typed request inside the admitted
// worker. Client environment, flags and overlays never reach this operation.
func RunCompatibility(ctx context.Context, plan planner.Plan, roots []planner.Configured, slot string) (CompatibilityResult, error) {
	p, err := PrepareCompatibility(plan, roots, slot)
	if err != nil {
		return CompatibilityResult{}, err
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Getuid() != 65534 {
		return CompatibilityResult{}, errors.New("compatibility driver requires admitted Linux arm64 worker")
	}
	if err := checkDriver(DriverSHA256); err != nil {
		return CompatibilityResult{}, err
	}
	if err := verifyFiles(ctx, p.files); err != nil {
		return CompatibilityResult{}, err
	}
	scopePath, wrapper, _ := compatibilityPaths(slot)
	if err := writeClosedFile(scopePath, p.scope, 0400); err != nil {
		return CompatibilityResult{}, err
	}
	if err := writeClosedFile(wrapper, []byte("#!/bin/sh\nexec /inputs/phebs-typed-worker __compat_bazel "+slot+" \"$@\"\n"), 0500); err != nil {
		return CompatibilityResult{}, err
	}
	data, err := runClosedChild(ctx, DriverPath, p.patterns, p.environment, p.request)
	if err != nil {
		return CompatibilityResult{}, err
	}
	return finishCompatibility(ctx, p, data)
}

func finishCompatibility(ctx context.Context, p Prepared, data []byte) (CompatibilityResult, error) {
	if !bytes.Equal(p.request, []byte(compatibilityRequest)) {
		return CompatibilityResult{}, errors.New("unprepared compatibility profile")
	}
	if err := reconcileAndVerifyFiles(ctx, p, data); err != nil {
		return CompatibilityResult{}, err
	}
	exports, err := sealCompatibilityExports(ctx, p)
	if err != nil {
		return CompatibilityResult{}, err
	}
	// Raw equality is complete before metadata changes. RawMessage preserves
	// package fields and array ordering; the three fields below are the entire
	// semantic adaptation, bound to PrepareCompatibility's uniform SDK check.
	var r map[string]json.RawMessage
	if err := json.Unmarshal(data, &r); err != nil {
		return CompatibilityResult{}, err
	}
	r["Compiler"], r["Arch"], r["GoVersion"] = json.RawMessage(`"gc"`), json.RawMessage(`"arm64"`), json.RawMessage(`25`)
	adapted, err := json.Marshal(r)
	if err != nil {
		return CompatibilityResult{}, err
	}
	if len(adapted) > MaxResponseBytes {
		return CompatibilityResult{}, errors.New("compatibility response byte bound")
	}
	return CompatibilityResult{DriverResponse: data, Response: adapted, Exports: exports}, nil
}

func compatibilityExportPaths(p Prepared) []string {
	paths := make(map[string]bool)
	for _, pkg := range p.packages {
		if pkg.ExportFile != "" {
			paths[pkg.ExportFile] = true
		}
	}
	result := make([]string, 0, len(paths))
	for name := range paths {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func sealCompatibilityExports(ctx context.Context, p Prepared) ([]ExportArtifact, error) {
	result := make([]ExportArtifact, 0)
	total := 0
	for _, name := range compatibilityExportPaths(p) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := readFile(name, min(planner.MaxFileBytes, planner.MaxProtoBytes-total))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || len(data) > planner.MaxProtoBytes-total {
			return nil, errors.New("compatibility export byte bound")
		}
		total += len(data)
		result = append(result, ExportArtifact{Path: name, Bytes: len(data), SHA256: hash(data)})
	}
	return result, ctx.Err()
}

func compatibilityDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func verifyCompatibilityFiles(ctx context.Context, p Prepared, exports []ExportArtifact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths := compatibilityExportPaths(p)
	if len(exports) != len(paths) {
		return errors.New("compatibility export inventory count mismatch")
	}
	total := 0
	for i, export := range exports {
		if export.Path != paths[i] || export.Bytes <= 0 || export.Bytes > planner.MaxFileBytes || export.Bytes > planner.MaxProtoBytes-total || !compatibilityDigest(export.SHA256) {
			return errors.New("compatibility export inventory identity mismatch")
		}
		total += export.Bytes
	}
	actual, err := sealCompatibilityExports(ctx, p)
	if err != nil {
		return err
	}
	if !slices.Equal(exports, actual) {
		return errors.New("sealed compatibility export changed after client")
	}
	return verifyFiles(ctx, p.files)
}

// VerifyCompatibilityFiles rechecks every selected source and exact export
// inventory after the complete typed-load or SCIP consumer has exited.
func VerifyCompatibilityFiles(ctx context.Context, plan planner.Plan, roots []planner.Configured, slot string, exports []ExportArtifact) error {
	p, err := PrepareCompatibility(plan, roots, slot)
	if err != nil {
		return err
	}
	return verifyCompatibilityFiles(ctx, p, exports)
}
