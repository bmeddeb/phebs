package indexer

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func privateBuildTestInfo() *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: runtime.Version(),
		Path:      "github.com/sourcegraph/zoekt/cmd/zoekt-git-index",
		Main: debug.Module{
			Path: "github.com/sourcegraph/zoekt", Version: zoektModuleVersion,
			Replace: &debug.Module{Path: "./zoekt", Version: "(devel)"},
		},
		Deps: []*debug.Module{
			{Path: "github.com/go-git/go-git/v5", Version: "v5.16.0", Sum: "h1:go-git"},
			{Path: "github.com/example/unshared", Version: "v0.1.0", Sum: "h1:unshared"},
		},
		Settings: []debug.BuildSetting{
			{Key: "CGO_ENABLED", Value: "0"}, {Key: "-trimpath", Value: "true"},
			{Key: "GOOS", Value: runtime.GOOS}, {Key: "GOARCH", Value: runtime.GOARCH},
		},
	}
}

// The runtime decision is the same function FindBinary applies to the
// parent-supplied override. The private V3 shape is admitted only under a
// selected V3 launch with a parent-verified digest; every other shape keeps
// the direct module pin, and a private build that drifts from the admitted
// replacement, toolchain, settings or shared dependency graph is refused.
func TestVerifyPinnedBuildAdmitsOnlyExactPrivateV3Shape(t *testing.T) {
	linked := "github.com/sourcegraph/zoekt@" + zoektModuleVersion + " " + zoektModuleSum
	graph := map[string]debug.Module{
		"github.com/go-git/go-git/v5": {Path: "github.com/go-git/go-git/v5", Version: "v5.16.0", Sum: "h1:go-git"},
	}
	admitted := privateIndexAdmission{selectedV3: true, expectedDigest: "sha256:" + strings.Repeat("a", 64)}
	for _, test := range []struct {
		name      string
		mutate    func(*debug.BuildInfo)
		admission privateIndexAdmission
		linked    string
		want      string
	}{
		{name: "private admitted", admission: admitted},
		{name: "pinned direct build", mutate: func(info *debug.BuildInfo) {
			info.Main = debug.Module{Path: "github.com/sourcegraph/zoekt", Version: zoektModuleVersion, Sum: zoektModuleSum}
		}},
		{name: "pinned direct build under selection", mutate: func(info *debug.BuildInfo) {
			info.Main = debug.Module{Path: "github.com/sourcegraph/zoekt", Version: zoektModuleVersion, Sum: zoektModuleSum}
		}, admission: admitted},
		{name: "private not selected", want: "admitted only for a selected V3 launch"},
		{name: "private without digest", admission: privateIndexAdmission{selectedV3: true}, want: "admitted only for a selected V3 launch"},
		{name: "private digest without selection", admission: privateIndexAdmission{expectedDigest: admitted.expectedDigest}, want: "admitted only for a selected V3 launch"},
		{name: "devel main without replacement", mutate: func(info *debug.BuildInfo) {
			info.Main = debug.Module{Path: "github.com/sourcegraph/zoekt", Version: "(devel)"}
		}, admission: admitted, want: "differs from linked reader"},
		{name: "wrong package", mutate: func(info *debug.BuildInfo) { info.Path = "github.com/sourcegraph/zoekt/cmd/zoekt-index" }, admission: admitted, want: "unexpected package/module identity"},
		{name: "wrong module", mutate: func(info *debug.BuildInfo) { info.Main.Path = "github.com/other/zoekt" }, admission: admitted, want: "unexpected package/module identity"},
		{name: "linked reader drift", admission: admitted, linked: "github.com/sourcegraph/zoekt@v0.0.0-other h1:other", want: "embedded zoekt pin"},
		{name: "replacement path", mutate: func(info *debug.BuildInfo) { info.Main.Replace.Path = "/private/zoekt" }, admission: admitted, want: "differs from the admitted V3 private build"},
		{name: "replacement version", mutate: func(info *debug.BuildInfo) { info.Main.Replace.Version = "" }, admission: admitted, want: "differs from the admitted V3 private build"},
		{name: "replacement sum", mutate: func(info *debug.BuildInfo) { info.Main.Replace.Sum = "h1:invented" }, admission: admitted, want: "differs from the admitted V3 private build"},
		{name: "nested replacement", mutate: func(info *debug.BuildInfo) {
			info.Main.Replace.Replace = &debug.Module{Path: "./nested"}
		}, admission: admitted, want: "differs from the admitted V3 private build"},
		{name: "private main sum", mutate: func(info *debug.BuildInfo) { info.Main.Sum = zoektModuleSum }, admission: admitted, want: "differs from the admitted V3 private build"},
		{name: "private wrong version", mutate: func(info *debug.BuildInfo) { info.Main.Version = "v0.0.0-20250101000000-000000000000" }, admission: admitted, want: "differs from the admitted V3 private build"},
		{name: "toolchain", mutate: func(info *debug.BuildInfo) { info.GoVersion = "go1.0" }, admission: admitted, want: "toolchain"},
		{name: "cgo", mutate: func(info *debug.BuildInfo) { info.Settings[0].Value = "1" }, admission: admitted, want: "closed host-native"},
		{name: "trimpath", mutate: func(info *debug.BuildInfo) { info.Settings[1].Value = "false" }, admission: admitted, want: "closed host-native"},
		{name: "goos", mutate: func(info *debug.BuildInfo) { info.Settings[2].Value = "plan9" }, admission: admitted, want: "closed host-native"},
		{name: "vcs revision", mutate: func(info *debug.BuildInfo) {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "vcs.revision", Value: strings.Repeat("b", 40)})
		}, admission: admitted, want: "closed host-native"},
		{name: "duplicate setting", mutate: func(info *debug.BuildInfo) {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "GOOS", Value: runtime.GOOS})
		}, admission: admitted, want: "duplicate build settings"},
		{name: "replaced dependency", mutate: func(info *debug.BuildInfo) {
			info.Deps[1].Replace = &debug.Module{Path: "./unshared"}
		}, admission: admitted, want: "replaces a dependency"},
		{name: "shared dependency version", mutate: func(info *debug.BuildInfo) { info.Deps[0].Version = "v5.15.0" }, admission: admitted, want: "differs from the linked reader graph"},
		{name: "shared dependency sum", mutate: func(info *debug.BuildInfo) { info.Deps[0].Sum = "h1:other" }, admission: admitted, want: "differs from the linked reader graph"},
		{name: "unshared dependency drift", mutate: func(info *debug.BuildInfo) { info.Deps[1].Version = "v0.2.0" }, admission: admitted},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := privateBuildTestInfo()
			if test.mutate != nil {
				test.mutate(info)
			}
			linkedIdentity := linked
			if test.linked != "" {
				linkedIdentity = test.linked
			}
			err := verifyPinnedBuild(info, linkedIdentity, graph, test.admission)
			if test.want == "" {
				if err != nil {
					t.Fatalf("exact identity refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("drifted identity admitted or misreported: %v", err)
			}
		})
	}
	if err := verifyPinnedBuild(nil, linked, graph, admitted); err == nil {
		t.Fatal("absent build identity admitted")
	}
}

// The linked graph never carries a replaced local dependency as a comparable
// identity, so a private child cannot be admitted against an invented pin.
func TestLinkedModuleGraphOmitsReplacedDependencies(t *testing.T) {
	graph, err := linkedModuleGraph()
	if err != nil {
		t.Fatal(err)
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("test binary has no build information")
	}
	for _, dependency := range build.Deps {
		pinned, shared := graph[dependency.Path]
		if dependency.Replace != nil {
			if shared {
				t.Fatalf("replaced dependency %s entered the comparable graph", dependency.Path)
			}
			continue
		}
		if !shared || pinned.Version != dependency.Version || pinned.Sum != dependency.Sum || pinned.Replace != nil {
			t.Fatalf("linked dependency %s was not retained exactly: %+v", dependency.Path, pinned)
		}
	}
}
