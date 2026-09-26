package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

func nativeRows(p Prepared, plan planner.Plan) NativeGoFiles {
	rows := NativeGoFiles{Version: nativeGoFilesVersion, MappingSHA256: plan.MappingSHA256, DocumentsSHA256: plan.DocumentsSHA256, Packages: []NativeGoFilesPackage{}}
	for _, id := range nativePackageIDs(p) {
		rows.Packages = append(rows.Packages, NativeGoFilesPackage{ID: id, GoFiles: slices.Clone(p.packages[id].GoFiles)})
	}
	return rows
}

func TestNativeCompatibilityBoundary(t *testing.T) {
	t.Run("separate profile and unchanged old authority", func(t *testing.T) {
		plan, roots := compatibilityFixture()
		old, err := PrepareCompatibility(plan, roots, "load")
		if err != nil {
			t.Fatal(err)
		}
		matches := nativeRows(old, plan)
		p, err := PrepareNativeCompatibility(plan, roots, "load", matches)
		if err != nil {
			t.Fatal(err)
		}
		if p.Digest() == old.Digest() || bytes.Equal(p.scope, old.scope) || !bytes.Equal(p.request, old.request) || len(p.files) != len(old.files) || NativeDriverSHA256 == DriverSHA256 || !compatibilityDigest(NativeDriverSHA256) {
			t.Fatal("native/old profile identity collapsed or declared authority changed")
		}
		if !slices.Contains(p.environment, "GOPACKAGESDRIVER_BAZEL=/scratch/t451b-native-load-bazel") || !slices.Contains(p.environment, "GOPACKAGESDRIVER_BAZEL_BUILD_FLAGS="+strings.Join(BazelBuild(), " ")+" "+nativeDescriptorFlag) {
			t.Fatal("native profile not fixed")
		}
		if !slices.Contains(p.environment, "GOPACKAGESDRIVER_BAZEL_COMMON_FLAGS="+strings.Join(NativeBazelCommon(), " ")) || !slices.Contains(old.environment, "GOPACKAGESDRIVER_BAZEL_COMMON_FLAGS="+strings.Join(BazelCommon(), " ")) {
			t.Fatal("native and legacy common flags not independently bound")
		}
		other, err := PrepareNativeCompatibility(plan, roots, "scip", matches)
		if err != nil || other.Digest() == p.Digest() {
			t.Fatal("native slots collapsed:", err)
		}
		after, err := PrepareCompatibility(plan, roots, "load")
		if err != nil || after.Digest() != old.Digest() {
			t.Fatal("old profile mutated:", err)
		}
		for _, slot := range []string{"", "LOAD", "../load", "other"} {
			if _, err := RunNativeCompatibility(context.Background(), plan, roots, slot, matches); err == nil || !strings.Contains(err.Error(), "slot") {
				t.Fatalf("invalid slot reached execution: %v", err)
			}
			if err := RunNativeCompatibilityBazel(context.Background(), slot, nil); err == nil || !strings.Contains(err.Error(), "slot") {
				t.Fatalf("invalid slot reached control read: %v", err)
			}
			if err := VerifyNativeCompatibilityFiles(context.Background(), plan, roots, slot, matches, nil); err == nil || !strings.Contains(err.Error(), "slot") {
				t.Fatalf("invalid slot reached verification: %v", err)
			}
		}
		// Sidecar callers cannot mutate a prepared response expectation.
		matches.Packages[0].GoFiles[0] = "/outside.go"
		good, _ := json.Marshal(exactResponse(p))
		if err := Reconcile(p, good); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("sidecar shape and membership", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			change func(*NativeGoFiles)
		}{
			{"version", func(s *NativeGoFiles) { s.Version = compatibilityScopeVersion }},
			{"mapping", func(s *NativeGoFiles) { s.MappingSHA256 = strings.Repeat("0", 64) }},
			{"documents", func(s *NativeGoFiles) { s.DocumentsSHA256 = strings.Repeat("0", 64) }},
			{"missing package", func(s *NativeGoFiles) { s.Packages = s.Packages[1:] }},
			{"extra package", func(s *NativeGoFiles) { s.Packages = append(s.Packages, s.Packages[0]) }},
			{"package duplicate", func(s *NativeGoFiles) { s.Packages[1] = s.Packages[0] }},
			{"package order", func(s *NativeGoFiles) { s.Packages[0], s.Packages[1] = s.Packages[1], s.Packages[0] }},
			{"package identity", func(s *NativeGoFiles) { s.Packages[0].ID = "@@//outside:lib" }},
			{"null files", func(s *NativeGoFiles) { s.Packages[0].GoFiles = nil }},
			{"outside file", func(s *NativeGoFiles) { s.Packages[0].GoFiles[0] = "/outside.go" }},
			{"file duplicate", func(s *NativeGoFiles) { s.Packages[0].GoFiles[1] = s.Packages[0].GoFiles[0] }},
			{"file order", func(s *NativeGoFiles) { slices.Reverse(s.Packages[0].GoFiles) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				plan, roots := compatibilityFixture()
				p, err := PrepareCompatibility(plan, roots, "load")
				if err != nil {
					t.Fatal(err)
				}
				matches := nativeRows(p, plan)
				tc.change(&matches)
				if _, err := PrepareNativeCompatibility(plan, roots, "load", matches); err == nil {
					t.Fatal("accepted malformed native sidecar")
				}
			})
		}
	})
	t.Run("sealed active source derivation and inactive custody", testNativeSourceSelection)
	t.Run("native scope and exact argv", testNativeScope)
	t.Run("immutable cache profile", testNativeCacheProfile)
	t.Run("sidecar byte bound", func(t *testing.T) {
		plan, _ := compatibilityFixture()
		files := make([]string, 4097)
		for i := range files {
			files[i] = "/" + strings.Repeat("x", 4090) + fmt.Sprintf("%05d", i)
		}
		p := Prepared{packages: map[string]flatPackage{"@@//lib:lib": {GoFiles: files}}}
		if err := validateNativeGoFiles(p, plan, nativeRows(p, plan)); err == nil || !strings.Contains(err.Error(), "byte bound") {
			t.Fatal("sidecar byte bound missing:", err)
		}
	})
}

func testNativeSourceSelection(t *testing.T) {
	plan, _ := compatibilityFixture()
	p, _ := compatibilityFileFixture(t)
	dir := t.TempDir()
	p.files = map[string]fileIdentity{}
	var declared, active []string
	for _, tc := range []struct {
		name, data string
		active     bool
	}{
		{"active.go", "//go:build linux && arm64 && go1.25 && !go1.26\n\npackage lib\n", true},
		{"inactive_windows.go", "package lib\n", false},
		{"disabled.go", "//go:build never\n\npackage lib\n", false},
		{"cgo.go", "package lib\nimport \"C\"\n", true},
		{"_cgo_gotypes.go", "//go:build never\n\npackage lib\n", true},
		{"_cgo_imports.go", "package lib\n", true},
		{"source.cgo1.go", "//go:build never\n\npackage lib\n", true},
		{"cache-d", "package lib\n", true},
	} {
		name := filepath.Join(dir, tc.name)
		if err := os.WriteFile(name, []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		p.files[name] = fileIdentity{hash([]byte(tc.data)), len(tc.data)}
		declared = append(declared, name)
		if tc.active {
			active = append(active, name)
		}
	}
	sort.Strings(declared)
	sort.Strings(active)
	for id, pkg := range p.packages {
		pkg.GoFiles = slices.Clone(declared)
		pkg.CompiledGoFiles = []string{filepath.Join(dir, "active.go")}
		p.packages[id] = pkg
	}
	matches, err := sealNativeGoFiles(context.Background(), p, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range matches.Packages {
		if !slices.Equal(row.GoFiles, active) {
			t.Fatalf("incorrect active files: %v", row.GoFiles)
		}
	}
	if err := verifyNativeGoFiles(context.Background(), p, plan, matches); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*NativeGoFiles){
		func(s *NativeGoFiles) { s.Packages[0].GoFiles = s.Packages[0].GoFiles[1:] },
		func(s *NativeGoFiles) { s.Packages[0].GoFiles = slices.Clone(declared) },
	} {
		bad := nativeRows(p, plan)
		for i := range bad.Packages {
			bad.Packages[i].GoFiles = slices.Clone(active)
		}
		change(&bad)
		if err := validateNativeGoFiles(p, plan, bad); err != nil {
			t.Fatal("valid declared subset needed as negative control:", err)
		}
		if err := verifyNativeGoFiles(context.Background(), p, plan, bad); err == nil {
			t.Fatal("runtime accepted arbitrary structurally valid subset")
		}
	}
	for _, row := range matches.Packages {
		pkg := p.packages[row.ID]
		pkg.GoFiles = row.GoFiles
		p.packages[row.ID] = pkg
	}
	raw, _ := json.Marshal(exactResponse(p))
	result, err := finishCompatibility(context.Background(), p, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inactive_windows.go"), []byte("package bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCompatibilityFiles(context.Background(), p, result.Exports); err == nil {
		t.Fatal("post-client check lost inactive source custody")
	}
	if _, err := finishCompatibility(context.Background(), p, raw); err == nil {
		t.Fatal("driver postcheck lost inactive source custody")
	}
	if _, err := sealNativeGoFiles(context.Background(), p, plan); err == nil {
		t.Fatal("filter admitted changed declared source")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sealNativeGoFiles(ctx, p, plan); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored source-selection cancellation:", err)
	}
	if _, err := matchNativeGoFile(filepath.Join(dir, "invalid.go"), []byte("//go:build (\n\npackage lib\n"), p.mode); err == nil {
		t.Fatal("invalid build constraint silently omitted a source")
	}
}

func testNativeScope(t *testing.T) {
	plan, roots := compatibilityFixture()
	old, err := PrepareCompatibility(plan, roots, "load")
	if err != nil {
		t.Fatal(err)
	}
	p, err := PrepareNativeCompatibility(plan, roots, "load", nativeRows(old, plan))
	if err != nil {
		t.Fatal(err)
	}
	s, err := inspectNativeScope(p.scope, hash(p.scope), "load")
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{old.scope, append(slices.Clone(p.scope), '\n'), []byte(strings.Replace(string(p.scope), `"slot":`, `"slot":"load","slot":`, 1)), []byte(strings.Replace(string(p.scope), `"slot":`, `"Slot":`, 1)), []byte(strings.Replace(string(p.scope), `"go_files_sha256":"`, `"go_files_sha256":"g`, 1))} {
		if _, err := inspectNativeScope(data, hash(data), "load"); err == nil {
			t.Fatal("accepted noncanonical/native scope")
		}
	}
	if _, err := inspectNativeScope(p.scope, hash(p.scope), "scip"); err == nil {
		t.Fatal("scope slot substitution")
	}
	if _, err := inspectNativeScope(p.scope, strings.Repeat("0", 64), "load"); err == nil {
		t.Fatal("scope hash substitution")
	}
	if _, err := inspectCompatibilityScope(p.scope, hash(p.scope), "load"); err == nil {
		t.Fatal("old profile accepts native scope")
	}
	info, argv, err := inspectNativeBazel(s, nativeCommandPrefix("info"))
	if err != nil || argv != nil || !bytes.HasPrefix(info, []byte("release: release 9.0.0\n")) {
		t.Fatal("wrong native synthetic info:", err)
	}
	query, argv, err := inspectNativeBazel(s, nativeQueryArgs(s))
	if err != nil || argv != nil || string(query) != strings.Join(s.Roots, "\n")+"\n" {
		t.Fatal("native query gained execution or lost roots:", err)
	}
	if slices.Equal(queryArgs(s), nativeQueryArgs(s)) {
		t.Fatal("query fixture does not exercise dotted import difference")
	}
	build := nativeBuildArgs(s, "/scratch/tmp/gopackagesdriver_bep_1234")
	if _, argv, err := inspectNativeBazel(s, build); err != nil || !slices.Equal(argv, build) {
		t.Fatal("exact native build refused:", err)
	}
	for _, args := range [][]string{
		commandPrefix("info"),
		queryArgs(s), buildArgs(s, "/scratch/tmp/gopackagesdriver_bep_1234"),
		append(slices.Clone(build), "@@//unselected:lib"),
		append(slices.Clone(build), nativeDescriptorFlag),
		append(slices.Clone(build), "--bazelrc=/outside"),
		append(slices.Clone(build), "--build_event_json_file=/scratch/tmp/gopackagesdriver_bep_5678"),
		build[:len(build)-1],
	} {
		if _, _, err := inspectNativeBazel(s, args); err == nil {
			t.Fatal("accepted wrong native argv/profile")
		}
	}
	if _, _, err := inspectCompatibilityBazel(s, build); err == nil {
		t.Fatal("old typed profile accepts native build")
	}
}

func testNativeCacheProfile(t *testing.T) {
	old := BazelCommon()
	native := NativeBazelCommon()
	if len(native) != len(old)+5 || native[0] != "--repository_cache=/inputs/tools/cache/repository" || native[2] != "--registry=https://bcr.bazel.build" || !slices.Equal(native[len(old):], []string{"--lockfile_mode=error", "--downloader_config=/inputs/tools/cache/downloader.cfg", "--repository_disable_download", "--repo_contents_cache=", "--experimental_repository_cache_hardlinks=false"}) {
		t.Fatal("native cache/registry/lock/downloader profile differs")
	}
	for i, arg := range old {
		if i != 0 && i != 2 && native[i] != arg {
			t.Fatal("native common flags changed another control")
		}
	}
	native[0] = "mutated"
	if NativeBazelCommon()[0] == "mutated" || !slices.Equal(BazelCommon(), old) {
		t.Fatal("mutable or shared common profile")
	}
	s := scope{Roots: []string{"@@//lib:lib"}, Patterns: []string{"example.test/lib"}}
	for _, command := range [][]string{nativeCommandPrefix("info"), nativeQueryArgs(s), nativeBuildArgs(s, "/scratch/tmp/gopackagesdriver_bep_1234")} {
		if _, _, err := inspectNativeBazel(s, command); err != nil {
			t.Fatal("exact native invocation is not a positive control:", err)
		}
		for _, tc := range []struct{ name, old, replacement string }{
			{"local registry", "--registry=https://bcr.bazel.build", "--registry=file:///inputs/tools/cache/registry"},
			{"writable cache", "--repository_cache=/inputs/tools/cache/repository", "--repository_cache=/scratch/repository-cache"},
			{"lock update", "--lockfile_mode=error", "--lockfile_mode=update"},
			{"missing block", "--downloader_config=/inputs/tools/cache/downloader.cfg", ""},
			{"alternate block file", "--downloader_config=/inputs/tools/cache/downloader.cfg", "--downloader_config=/scratch/downloader.cfg"},
			{"missing download disable", "--repository_disable_download", ""},
			{"downloads enabled", "--repository_disable_download", "--repository_disable_download=false"},
			{"repo contents cache", "--repo_contents_cache=", "--repo_contents_cache=/scratch/repo-contents-cache"},
			{"hardlinks enabled", "--experimental_repository_cache_hardlinks=false", "--experimental_repository_cache_hardlinks=true"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				changed := slices.Clone(command)
				index := slices.Index(changed, tc.old)
				if index < 0 {
					t.Fatal("ineffective negative mutation")
				}
				if tc.replacement == "" {
					changed = slices.Delete(changed, index, index+1)
				} else {
					changed[index] = tc.replacement
				}
				if _, _, err := inspectNativeBazel(s, changed); err == nil {
					t.Fatal("native cache/registry override admitted")
				}
			})
		}
		for _, override := range []string{"--registry=file:///outside", "--repository_cache=/scratch/other", "--lockfile_mode=update", "--downloader_config=", "--repository_disable_download=false", "--norepository_disable_download", "--experimental_repository_disable_download=false", "--repo_contents_cache=/scratch/other", "--experimental_repository_cache_hardlinks=true"} {
			if _, _, err := inspectNativeBazel(s, append(slices.Clone(command), override)); err == nil {
				t.Fatal("native duplicate flag override admitted")
			}
		}
	}
}
