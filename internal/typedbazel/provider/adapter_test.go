package provider

import (
	"context"
	"runtime/debug"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
)

func TestExactCallerContract(t *testing.T) {
	_, s, p, matches, _ := fixture(t)
	roots, e := configuredRoots(p, s)
	if e != nil {
		t.Fatal(e)
	}
	for _, slot := range []string{"load", "scip"} {
		prepared, e := launcher.PrepareNativeCompatibility(p, roots, slot, matches)
		if e != nil {
			t.Fatal(e)
		}
		data, e := nativeControlBytes(p, roots, matches, s, slot)
		if e != nil {
			t.Fatal(e)
		}
		for _, change := range []string{"none", "environment", "mode", "argv", "directory", "digest", "duplicate-field", "roots"} {
			t.Run(slot+"/"+change, func(t *testing.T) {
				b := slices.Clone(data)
				digest := hash(b)
				env := callerEnvironment(prepared, slot, digest)
				wire := callerRequest(env)
				argv := append([]string{NativeAdapterPath}, prepared.Invocation().Arguments...)
				cwd := launcher.Workspace
				switch change {
				case "environment":
					env = append(env, "UNAPPROVED=1")
				case "mode":
					wire = []byte(`{"mode":31,"env":[]}`)
				case "argv":
					argv = append(argv, "//other:root")
				case "directory":
					cwd = "/other"
				case "digest":
					digest = hash([]byte("other"))
				case "duplicate-field":
					b = append([]byte(`{"version":"phebs-bazel-client-plan-v1",`), b[1:]...)
				case "roots":
					changed := s
					changed.Roots = []string{"//other:root"}
					b, e = nativeControlBytes(p, roots, matches, changed, slot)
					if e != nil {
						t.Fatal(e)
					}
				}
				_, _, e := inspectNativeCall(b, digest, argv, env, cwd, wire)
				if (e == nil) != (change == "none") {
					t.Fatal(change, e)
				}
			})
		}
	}
	if _, _, e := nativeSlotPaths("../escape"); e == nil {
		t.Fatal("unknown slot accepted")
	}
}

func TestPinnedBuildInformation(t *testing.T) {
	base := debug.BuildInfo{GoVersion: "go1.25.0", Path: "phebs.local/t451b-native-probe", Settings: []debug.BuildSetting{{Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "arm64"}, {Key: "CGO_ENABLED", Value: "0"}, {Key: "GOARM64", Value: "v8.0"}}, Deps: []*debug.Module{{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="}}}
	for _, change := range []string{"none", "go", "main", "platform", "duplicate", "replace", "dependency"} {
		b := base
		b.Settings = slices.Clone(base.Settings)
		d := *base.Deps[0]
		b.Deps = []*debug.Module{&d}
		switch change {
		case "go":
			b.GoVersion = "go1.25.7"
		case "main":
			b.Path = "other"
		case "platform":
			b.Settings[0].Value = "darwin"
		case "duplicate":
			b.Settings = append(b.Settings, b.Settings[0])
		case "replace":
			d.Replace = &debug.Module{Path: "other"}
		case "dependency":
			d.Version = "v0.48.0"
		}
		if e := checkBuild(&b, base.Path, true); (e == nil) != (change == "none") {
			t.Fatal(change, e)
		}
	}
	// Public adapter cannot run on the portable host under this test profile.
	if e := RunAdapter(context.Background()); e == nil {
		t.Fatal("adapter accepted ordinary host")
	}
}
