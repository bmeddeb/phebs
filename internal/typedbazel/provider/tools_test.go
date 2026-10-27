package provider

import (
	"context"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestSCIPArgumentsUseSelectedSDK(t *testing.T) {
	for _, tc := range []struct{ arch, flag string }{
		{"arm64", "--go-version=go1.25.0"},
		{"amd64", "--go-version=go1.25.7"},
	} {
		if args := scipArguments(Selection{}, nil, tc.arch); !slices.Contains(args, tc.flag) {
			t.Fatalf("%s arguments lack %s: %v", tc.arch, tc.flag, args)
		}
	}
}

func buildInfo(path, goVersion string, settings map[string]string, deps ...*debug.Module) *debug.BuildInfo {
	info := &debug.BuildInfo{Path: path, GoVersion: goVersion, Deps: deps}
	for key, value := range settings {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: key, Value: value})
	}
	return info
}

func TestProductionRejectsTestHelperMain(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		t.Fatal("test binary carries no build info")
	}
	if info.Path == productionHelperMain {
		t.Fatalf("preparation test binary unexpectedly reports production main %q", productionHelperMain)
	}
	if err := checkBuild(info, productionHelperMain, false); err != typedindex.Unsupported {
		t.Fatalf("production checkBuild accepted the test helper: %v", err)
	}
}

func TestCheckBuildRejectsIncorrectCmdMetadata(t *testing.T) {
	linuxArm64 := map[string]string{"GOOS": "linux", "GOARCH": "arm64"}
	cases := []struct {
		name  string
		info  *debug.BuildInfo
		main  string
		typed bool
	}{
		{"nil", nil, "cmd/go", false},
		{"wrong-main", buildInfo("example.invalid/other", "go1.25.0", linuxArm64), "cmd/go", false},
		{"wrong-goos", buildInfo("cmd/go", "go1.25.0", map[string]string{"GOOS": "darwin", "GOARCH": "arm64"}), "cmd/go", false},
		{"wrong-goarch", buildInfo("cmd/go", "go1.25.0", map[string]string{"GOOS": "linux", "GOARCH": "386"}), "cmd/go", false},
		{"wrong-go-version", buildInfo("cmd/go", "go1.99.0", linuxArm64), "cmd/go", false},
		{"typed-wrong-go-version", buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.24.0", map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0", "GOARM64": "v8.0"}, &debug.Module{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="}), "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"typed-cgo-enabled", buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "1", "GOARM64": "v8.0"}, &debug.Module{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="}), "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"typed-wrong-goarm64", buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0", "GOARM64": "v8.1"}, &debug.Module{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="}), "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"typed-wrong-goamd64", buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", map[string]string{"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "GOAMD64": "v2"}, &debug.Module{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="}), "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"typed-wrong-xtools-version", buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0", "GOARM64": "v8.0"}, &debug.Module{Path: "golang.org/x/tools", Version: "v0.44.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="}), "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"typed-missing-xtools", buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0", "GOARM64": "v8.0"}), "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"typed-replaced-xtools", buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0", "GOARM64": "v8.0"}, &debug.Module{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8=", Replace: &debug.Module{Path: "example.invalid/x/tools"}}), "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"duplicate-setting", buildInfo("cmd/go", "go1.25.0", nil), "cmd/go", false},
	}
	cases[len(cases)-1].info.Settings = []debug.BuildSetting{{Key: "GOOS", Value: "linux"}, {Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "arm64"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkBuild(tc.info, tc.main, tc.typed); err != typedindex.Unsupported {
				t.Fatalf("checkBuild(%s) = %v, want %v", tc.name, err, typedindex.Unsupported)
			}
		})
	}
}

func TestCheckBuildAcceptsExactIdentities(t *testing.T) {
	host := map[string]string{"GOOS": "linux", "GOARCH": runtime.GOARCH}
	if err := checkBuild(buildInfo("cmd/go", "go"+typedindex.NativeSDKVersion(runtime.GOARCH), host), "cmd/go", false); err != nil {
		t.Fatalf("cmd/go rejected: %v", err)
	}
	if err := checkBuild(buildInfo(productionHelperMain, "go1.25.0", host), productionHelperMain, false); err != nil {
		t.Fatalf("helper rejected: %v", err)
	}
	typedSettings := map[string]string{"GOOS": "linux", "GOARCH": runtime.GOARCH, "CGO_ENABLED": "0"}
	other := map[string]string{"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0", "GOARM64": "v8.0"}
	if runtime.GOARCH == "arm64" {
		typedSettings["GOARM64"] = "v8.0"
		other = map[string]string{"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "GOAMD64": "v1"}
	} else {
		typedSettings["GOAMD64"] = "v1"
	}
	typed := buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", typedSettings,
		&debug.Module{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="})
	if err := checkBuild(typed, "github.com/scip-code/scip-go/cmd/scip-go", true); err != nil {
		t.Fatalf("scip-go rejected: %v", err)
	}
	if err := checkBuild(buildInfo("github.com/scip-code/scip-go/cmd/scip-go", "go1.25.0", other,
		&debug.Module{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="}), "github.com/scip-code/scip-go/cmd/scip-go", true); err == nil {
		t.Fatal("other admitted architecture accepted on this host")
	}
}

func TestCheckBuildRejectsOtherSDKPatch(t *testing.T) {
	other := "go1.25.7"
	if runtime.GOARCH == "amd64" {
		other = "go1.25.0"
	}
	settings := map[string]string{"GOOS": "linux", "GOARCH": runtime.GOARCH}
	if err := checkBuild(buildInfo("cmd/go", other, settings), "cmd/go", false); err != typedindex.Unsupported {
		t.Fatalf("other SDK patch accepted: %v", err)
	}
}

func TestVerifyToolsSignatureUnchanged(t *testing.T) {
	// Compile-time guard: verifyTools must remain the fixed production entry
	// point with this exact signature; a drift breaks the build, not the test.
	requireSignature := func(func(context.Context, Invocation) error) {}
	requireSignature(verifyTools)
}

func TestBootstrapBuildAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, main, version, arch, variant, cgo string
		want                                    bool
	}{
		{"builder", "command-line-arguments", "go1.25.7", "amd64", "v1", "0", true},
		{"pack", "cmd/pack", "go1.25.7", "amd64", "v1", "0", true},
		{"old-sdk", "cmd/pack", "go1.25.0", "amd64", "v1", "0", false},
		{"foreign-arch", "cmd/pack", "go1.25.7", "arm64", "v1", "0", false},
		{"foreign-variant", "cmd/pack", "go1.25.7", "amd64", "v2", "0", false},
		{"dynamic", "cmd/pack", "go1.25.7", "amd64", "v1", "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := buildInfo(tc.main, tc.version, map[string]string{"GOOS": "linux", "GOARCH": tc.arch, "GOAMD64": tc.variant, "CGO_ENABLED": tc.cgo})
			if got := checkBootstrapBuild(info, tc.main) == nil; got != (tc.want && runtime.GOARCH == "amd64") {
				t.Fatalf("bootstrap admission = %v, want %v on %s", got, tc.want, runtime.GOARCH)
			}
		})
	}
	if checkBootstrapBuild(nil, "cmd/pack") == nil {
		t.Fatal("missing build metadata admitted")
	}
}
