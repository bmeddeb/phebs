package provider

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"runtime"
	"runtime/debug"

	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const rulesArchive = "68af54cb97fbdee5e5e8fe8d210d15a518f9d62abfd71620c3eaff3b26a5ff86"
const rulesMarker = "id-5ecce7d467cc4ee7c12a627edd8f76673775fd451848dbf3c8759ed8c7e73558"

var rulesFiles = [...]struct {
	digest string
	bytes  int64
}{
	{rulesArchive, 2546753},
	{"1df17bb7865cfc029492c30163cee891d0dd8658ea0d5bfdf252c4b6db5c1ef6", 344},
	{"b7e43e7414a3139a7547d1b4909b29085fbe5182b6c58cbe1ed4c6272815aeae", 1825},
	{"f0ea14c601a983d77cd58e400d38ce4188af94e948b2bd8c37a633b913487030", 400},
}

// productionHelperMain is the fixed expected main identity for the managed
// helper and native driver. Production verification never accepts a caller-
// supplied value; only the package-local preparation test overrides it through
// the unexported seam below to supply its own observed build main.
const productionHelperMain = "github.com/bmeddeb/phebs/cmd/phebs"

func verifyTools(ctx context.Context, i Invocation) error {
	return verifyToolsWithHelperMain(ctx, i, productionHelperMain)
}

func verifyToolsWithHelperMain(ctx context.Context, i Invocation, helperMain string) error {
	p := i.Profile.Definition()
	helper, e := inventoryFile(i.Inventory, typedindex.ManagedHelperFile)
	if e != nil || !helper.Executable || p.Tools.Planner.Digest != helper.Digest || p.Tools.Launcher.Digest != helper.Digest || p.Tools.RulesGo.Digest != "sha256:"+rulesArchive {
		return typedindex.Unsupported
	}
	for _, r := range rulesFiles {
		if e = ctx.Err(); e != nil {
			return e
		}
		name := "tools/cache/repository/content_addressable/sha256/" + r.digest + "/file"
		b, err := readInventory(i.Inventory, name, r.bytes)
		if err != nil || int64(len(b)) != r.bytes || hash(b) != "sha256:"+r.digest {
			return typedindex.Unprepared
		}
	}
	marker := "tools/cache/repository/content_addressable/sha256/" + rulesArchive + "/" + rulesMarker
	b, e := readInventory(i.Inventory, marker, 0)
	if e != nil || len(b) != 0 {
		return typedindex.Unprepared
	}
	for _, x := range []struct{ path, embedded string }{{"tools/metadata/typed-bazel/aspect.bzl", "bootstrap/aspect.bzl"}, {"tools/metadata/typed-bazel/probe.go", "bootstrap/probe.go.txt"}} {
		want, _ := bootstrap.ReadFile(x.embedded)
		got, err := readInventory(i.Inventory, x.path, int64(len(want)))
		if err != nil || !bytes.Equal(got, want) {
			return typedindex.Unprepared
		}
	}
	b, e = readInventory(i.Inventory, "tools/cache/downloader.cfg", int64(len(launcher.NativeDownloaderConfig)))
	if e != nil || string(b) != launcher.NativeDownloaderConfig {
		return typedindex.Unprepared
	}
	for _, t := range []struct {
		path, digest, main string
		typed              bool
	}{
		{typedindex.ManagedHelperFile, helper.Digest, helperMain, false},
		{"tools/bin/phebs-t451b-native-driver", helper.Digest, helperMain, false},
		{"tools/bin/t451b-native-probe", "", "phebs.local/t451b-native-probe", true},
		{"tools/bin/scip-go", SCIPDigest, "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"tools/go/bin/go", GoDigest, "cmd/go", false},
	} {
		if e = ctx.Err(); e != nil {
			return e
		}
		f, err := inventoryFile(i.Inventory, t.path)
		if err != nil || !f.Executable || t.digest != "" && f.Digest != t.digest {
			return typedindex.Unprepared
		}
		if _, err = readInventory(i.Inventory, t.path, typedindex.MaxFileBytes); err != nil {
			return err
		}
		info, err := buildinfo.ReadFile("/inputs/" + t.path)
		if err != nil || checkBuild(info, t.main, t.typed) != nil {
			return typedindex.Unsupported
		}
	}
	for name, want := range map[string]string{"tools/bin/bazel": BazelDigest, "tools/bin/gopackagesdriver": "sha256:" + launcher.NativeDriverSHA256} {
		f, err := inventoryFile(i.Inventory, name)
		if err != nil || !f.Executable || f.Digest != want {
			return typedindex.Unprepared
		}
		if _, err = readInventory(i.Inventory, name, typedindex.MaxFileBytes); err != nil {
			return err
		}
	}
	return nil
}
func checkBuild(info *debug.BuildInfo, main string, typed bool) error {
	if info == nil || info.Path != main {
		return typedindex.Unsupported
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		if _, ok := settings[s.Key]; ok {
			return typedindex.Unsupported
		}
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != runtime.GOARCH || !typedindex.AdmittedNativeArch(settings["GOARCH"]) {
		return typedindex.Unsupported
	}
	if main == "cmd/go" && info.GoVersion != "go1.25.0" {
		return typedindex.Unsupported
	}
	if typed {
		if info.GoVersion != "go1.25.0" || settings["CGO_ENABLED"] != "0" || !typedindex.AdmittedNativeVariant(settings["GOARCH"], settings["GOARM64"], settings["GOAMD64"]) {
			return typedindex.Unsupported
		}
		count := 0
		for _, d := range info.Deps {
			if d == nil {
				return typedindex.Unsupported
			}
			if d.Path == "golang.org/x/tools" {
				if d.Version != "v0.45.0" || d.Sum != "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8=" || d.Replace != nil {
					return typedindex.Unsupported
				}
				count++
			}
		}
		if count != 1 {
			return typedindex.Unsupported
		}
	}
	return nil
}
func verifySDK(p planner.Plan) error {
	if len(p.SDKs) == 0 {
		return typedindex.Unsupported
	}
	for _, s := range p.SDKs {
		if s.Root != nativeSDKRoot || s.Version != "1.25.0" {
			return typedindex.Unsupported
		}
	}
	name := launcher.ExecRoot + "/" + nativeSDKRoot + "/bin/go"
	b, e := readBounded(name, typedindex.MaxFileBytes)
	if e != nil || hash(b) != GoDigest {
		return typedindex.Unsupported
	}
	info, e := buildinfo.ReadFile(name)
	if e != nil {
		return typedindex.Unsupported
	}
	return checkBuild(info, "cmd/go", false)
}
