package t451b

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

type Materialization struct {
	PlanningLockSHA256    string `json:"planning_lock_sha256,omitempty"`
	ArchiveSHA256         string `json:"archive_sha256"`
	SourceFiles           int    `json:"source_files"`
	SourceBytes           int    `json:"source_bytes"`
	SourceSHA256          string `json:"source_sha256"`
	OwnedSHA256           string `json:"owned_sha256"`
	AspectSHA256          string `json:"aspect_sha256"`
	OriginalsUnchanged    bool   `json:"originals_unchanged"`
	ExcludedControlAbsent bool   `json:"excluded_control_absent"`
}

// NativeAspect retains the original provider projection, binding only the
// separately closed native SDK's canonical repository and provider root.
func NativeAspect() ([]byte, error) {
	files, err := planner.Fixtures()
	if err != nil {
		return nil, err
	}
	s := string(files["phebs_plan/aspect.bzl"])
	old := `Label("@phebs_sdk//:src/" + p)`
	if strings.Count(s, old) != 1 || strings.Count(s, "        mode = target[GoInfo].mode\n") != 1 {
		return nil, errors.New("native aspect source drift")
	}
	s = strings.Replace(s, old, `Label("@@rules_go++go_sdk+go_default_sdk//:src/" + p)`, 1)
	s = strings.Replace(s, "        mode = target[GoInfo].mode\n", `        mode = target[GoInfo].mode
        if sdk.version != "1.25.0" or sdk.root_file.dirname != "external/rules_go++go_sdk+go_default_sdk":
            fail("T45.1b selected SDK provider differs from frozen native profile")
        for f in ctx.files._sdk_helper_srcs:
            if not f.path.startswith(sdk.root_file.dirname + "/src/"):
                fail("T45.1b helper File belongs to another SDK")
`, 1)
	return []byte(s), nil
}

func publicSources(data []byte) (map[string][]byte, error) {
	if len(data) != 249496 || t451a.Digest(data) != PublicArchiveDigest {
		return nil, errors.New("public archive identity mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(io.LimitReader(gz, 2<<20))
	files := map[string][]byte{}
	total, entries := 0, 0
	prefix := "remote-apis-sdks-" + PublicCommit + "/"
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entries++
		if entries == 1 && h.Typeflag == tar.TypeXGlobalHeader && h.Name == "pax_global_header" && len(h.PAXRecords) == 1 && h.PAXRecords["comment"] == PublicCommit {
			continue
		}
		if entries > 256 || !strings.HasPrefix(h.Name, prefix) {
			return nil, errors.New("public archive entry bound/path")
		}
		name := strings.TrimPrefix(h.Name, prefix)
		if h.Typeflag == tar.TypeDir {
			if name != "" && !safeRelative(strings.TrimSuffix(name, "/")) {
				return nil, errors.New("public archive directory path")
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || !safeRelative(name) || strings.HasPrefix(name, "phebs_plan/") || strings.HasPrefix(name, "phebs_excluded/") || name == "phebs_plan" || name == "phebs_excluded" || files[name] != nil || h.Size <= 0 || h.Size > 1079184 || len(files) == 128 {
			return nil, errors.New("public archive file refused")
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil || int64(len(b)) != h.Size {
			return nil, errors.New("public archive file bytes")
		}
		total += len(b)
		if total > 1079184 {
			return nil, errors.New("public source byte bound")
		}
		files[name] = b
	}
	if len(files) != 128 || total != 1079184 {
		return nil, errors.New("public source inventory mismatch")
	}
	return files, nil
}

func safeRelative(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00\r\n")
}

func nativeWorkspaceFiles(cohort string, archive []byte, helper []byte) (map[string][]byte, map[string][]byte, error) {
	if _, err := cohortRoots(cohort); err != nil {
		return nil, nil, err
	}
	public, err := publicSources(archive)
	if err != nil {
		return nil, nil, err
	}
	fixtures, err := planner.Fixtures()
	if err != nil {
		return nil, nil, err
	}
	originals := public
	owned := map[string][]byte{}
	if cohort == "neutral" {
		originals = map[string][]byte{}
		for name, b := range fixtures {
			owned[name] = b
		}
		owned["MODULE.bazel"] = []byte("module(name = \"phebs_t451b_native\")\nbazel_dep(name = \"rules_go\", version = \"0.59.0\")\nbazel_dep(name = \"gazelle\", version = \"0.47.0\")\nbazel_dep(name = \"rules_proto\", version = \"7.1.0\")\nbazel_dep(name = \"protobuf\", version = \"33.5\")\nbazel_dep(name = \"grpc\", version = \"1.76.0.bcr.1\")\nbazel_dep(name = \"googleapis\", version = \"0.0.0-20260130-c0fcb356\")\nbazel_dep(name = \"neutral_external\", version = \"0.0.0\")\nlocal_path_override(module_name = \"neutral_external\", path = \"external\")\n")
		// Only the owned-neutral lock may update. Preserve upstream facts as the
		// input to native SDK selection; the public lock is never changed.
		owned["MODULE.bazel.lock"] = public["MODULE.bazel.lock"]
		if strings.Count(string(owned["external/MODULE.bazel"]), `version = "0.63.0"`) != 1 || strings.Count(string(owned["lib/BUILD.bazel"]), `srcs = ["lib.go"]`) != 1 {
			return nil, nil, errors.New("native fixture pin/source drift")
		}
		owned["external/MODULE.bazel"] = bytes.Replace(owned["external/MODULE.bazel"], []byte(`version = "0.63.0"`), []byte(`version = "0.59.0"`), 1)
		owned["lib/BUILD.bazel"] = bytes.Replace(owned["lib/BUILD.bazel"], []byte(`srcs = ["lib.go"]`), []byte(`srcs = ["lib.go", "inactive_windows.go"]`), 1)
		owned["lib/inactive_windows.go"] = []byte("package lib\n\nfunc windowsOnly() int { return 3 }\n")
	} else {
		for name, b := range fixtures {
			if strings.HasPrefix(name, "phebs_plan/") {
				owned[name] = b
			}
		}
	}
	aspect, err := NativeAspect()
	if err != nil {
		return nil, nil, err
	}
	owned["phebs_plan/aspect.bzl"] = aspect
	owned["phebs_plan/t451a"] = helper
	owned["phebs_excluded/BUILD.bazel"] = []byte("genrule(name = \"broken\", outs = [\"sentinel\"], cmd = \"touch /scratch/t451b-excluded-sentinel; touch $@; exit 1\")\n")
	for name := range owned {
		if _, ok := originals[name]; ok {
			return nil, nil, errors.New("owned control overlaps public source")
		}
	}
	return originals, owned, nil
}

func fileSetDigest(files map[string][]byte) string {
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	type entry struct {
		Path   string
		Bytes  int
		SHA256 string
	}
	rows := make([]entry, 0, len(keys))
	for _, name := range keys {
		rows = append(rows, entry{name, len(files[name]), t451a.Digest(files[name])})
	}
	b, _ := json.Marshal(rows)
	return t451a.Digest(b)
}

func materializeNative(r NativeRequest) (Materialization, map[string][]byte, error) {
	var facts Materialization
	archive, err := readBounded(PublicArchivePath, 249496)
	if err != nil {
		return facts, nil, err
	}
	helper, err := readBounded("/inputs/t451a", t451a.MaxFileBytes)
	if err != nil || t451a.Digest(helper) != r.HelperSHA256 {
		return facts, nil, errors.New("native workspace helper identity")
	}
	originals, owned, err := nativeWorkspaceFiles(r.Cohort, archive, helper)
	if err != nil {
		return facts, nil, err
	}
	for _, files := range []map[string][]byte{originals, owned} {
		keys := make([]string, 0, len(files))
		for name := range files {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		for _, name := range keys {
			if !safeRelative(name) {
				return facts, nil, errors.New("native workspace path")
			}
			full := path.Join(launcher.Workspace, name)
			if err = os.MkdirAll(path.Dir(full), 0700); err != nil {
				return facts, nil, err
			}
			mode := os.FileMode(0600)
			if name == "phebs_plan/t451a" {
				mode = 0500
			}
			f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return facts, nil, err
			}
			_, writeErr := f.Write(files[name])
			if err = errors.Join(writeErr, f.Close()); err != nil {
				return facts, nil, err
			}
		}
	}
	facts = Materialization{ArchiveSHA256: PublicArchiveDigest, SourceFiles: len(originals), SourceSHA256: fileSetDigest(originals), OwnedSHA256: fileSetDigest(owned), AspectSHA256: r.AspectSHA256}
	for _, b := range originals {
		facts.SourceBytes += len(b)
	}
	// Keep the public source receipt separate while reusing this private
	// verification snapshot for every owned control too. Only the explicitly
	// mutable neutral planning lock is excluded; public lock bytes stay exact.
	for name, b := range owned {
		if r.Cohort != "neutral" || name != "MODULE.bazel.lock" {
			originals[name] = b
		}
	}
	return facts, originals, nil
}

func verifyNativeWorkspace(plan planner.Plan, originals map[string][]byte) error {
	for name, want := range originals {
		info, err := os.Lstat(path.Join(launcher.Workspace, name))
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("native source/control file type changed")
		}
		got, err := readBounded(path.Join(launcher.Workspace, name), int64(len(want)))
		if err != nil || !bytes.Equal(got, want) {
			return errors.New("native source/control file changed")
		}
	}
	for _, t := range plan.Targets {
		if strings.HasPrefix(t.Label, "@@//phebs_excluded:") {
			return errors.New("excluded control entered configured universe")
		}
	}
	// A fixed absolute sentinel is independent of Bazel configuration directories.
	if _, err := os.Lstat("/scratch/t451b-excluded-sentinel"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("excluded control sentinel present or unavailable")
	}

	return nil
}
