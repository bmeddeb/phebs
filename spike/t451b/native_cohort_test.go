package t451b

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

var nativeCohortConfig = flag.String("t451b-native-cohort-config", "", "explicit closed native cohort config; never discovers or builds tools")
var publicArchiveTest = flag.String("t451b-public-archive", "", "data-only check of the exact public source archive; executes no source")

func TestNativeCohort(t *testing.T) {
	if *nativeCohortConfig == "" {
		t.Skip("native cohort requires an explicit reviewed pinned config")
	}
	c, err := ReadNativeConfig(*nativeCohortConfig)
	if err != nil {
		t.Fatal(err)
	}
	r, err := RunNativeConfig(context.Background(), c)
	payload := r.NativeEvidence
	r.NativeEvidence = nil
	b, _ := json.Marshal(r)
	t.Log(string(b))
	if err != nil || r.Outcome != "NATIVE_COHORT_OBSERVED" || !r.Removed || !r.InputsRemoved || !r.Resources.LimitsVerified {
		t.Fatalf("native cohort not established: %v", err)
	}
	if _, err = DecodeNativeEvidence(payload); err != nil {
		t.Fatal(err)
	}
}

func validNativeRequest(t *testing.T) NativeRequest {
	t.Helper()
	aspect, err := NativeAspect()
	if err != nil {
		t.Fatal(err)
	}
	h := t451a.Digest([]byte("test identity"))
	return NativeRequest{"phebs-t451b-native-request-v1", NativeProfile, "neutral", h, h, h, t451a.Digest(NativeProbeSource), SCIPDigest, GoDigest, NativeBazelDigest, "sha256:" + launcher.NativeDriverSHA256, PublicArchiveDigest, t451a.Digest(aspect), h}
}

func TestNativeCohortBoundary(t *testing.T) {
	t.Run("closed request", func(t *testing.T) {
		r := validNativeRequest(t)
		for _, cohort := range []string{"neutral", "ordinary", "proto", "fanout"} {
			r.Cohort = cohort
			if _, err := DecodeNativeRequest(nativeRequestBytes(r)); err != nil {
				t.Fatal(err)
			}
		}
		for _, change := range []func(*NativeRequest){
			func(r *NativeRequest) { r.Profile = Profile }, func(r *NativeRequest) { r.Cohort = "./..." }, func(r *NativeRequest) { r.BazelSHA256 = r.HelperSHA256 }, func(r *NativeRequest) { r.DriverSHA256 = "sha256:" + launcher.DriverSHA256 }, func(r *NativeRequest) { r.ArchiveSHA256 = r.HelperSHA256 }, func(r *NativeRequest) { r.AspectSHA256 = r.HelperSHA256 }, func(r *NativeRequest) { r.ProbeSourceSHA256 = t451a.Digest(ProbeSource) }, func(r *NativeRequest) { r.GoSHA256 = r.HelperSHA256 }, func(r *NativeRequest) { r.ImageID = "" },
		} {
			bad := r
			change(&bad)
			if _, err := DecodeNativeRequest(nativeRequestBytes(bad)); err == nil {
				t.Fatal("accepted changed profile", bad)
			}
		}
		wire := nativeRequestBytes(r)
		for _, bad := range [][]byte{bytes.Replace(wire, []byte(`"cohort":`), []byte(`"cohort":"neutral","cohort":`), 1), bytes.Replace(wire, []byte(`"schema":`), []byte(`"Schema":`), 1), bytes.Replace(wire, []byte(`"profile":`), []byte(`"extra":0,"profile":`), 1), append(slices.Clone(wire), ' ')} {
			if _, err := DecodeNativeRequest(bad); err == nil {
				t.Fatal("accepted noncanonical request")
			}
		}
		if _, err := DecodeRequest(wire); err == nil {
			t.Fatal("old request accepts native profile")
		}
	})
	t.Run("exact native caller", testNativeCallerBoundary)
	t.Run("typed closure", testNativeProbeBoundary)
	t.Run("native offline layout", func(t *testing.T) {
		r := validNativeRequest(t)
		bundle := t451a.Bundle{Schema: "phebs-t451a-offline-v1", Files: []t451a.BundleFile{
			{Path: "tools/bin/bazel", Bytes: 1, SHA256: NativeBazelDigest, Executable: true},
			{Path: "tools/bin/gopackagesdriver", Bytes: 1, SHA256: r.DriverSHA256, Executable: true},
			{Path: "tools/cc-sysroot.zip", Bytes: 1, SHA256: r.BundleSHA256},
			{Path: "tools/corpus/remote-apis-sdks.tar.gz", Bytes: 249496, SHA256: PublicArchiveDigest},
			{Path: "tools/cache/downloader.cfg", Bytes: 8, SHA256: t451a.Digest([]byte("block *\n"))},
		}}
		for _, tool := range nativeToolProfiles(r)[1:] {
			bundle.Files = append(bundle.Files, t451a.BundleFile{Path: strings.TrimPrefix(tool.path, "/inputs/"), Bytes: 1, SHA256: tool.sha, Executable: true})
		}
		if err := validateNativeLayout(bundle, r); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(*t451a.Bundle){func(b *t451a.Bundle) { b.Files = b.Files[1:] }, func(b *t451a.Bundle) { b.Files[1].SHA256 = "sha256:" + launcher.DriverSHA256 }, func(b *t451a.Bundle) { b.Files[3].Executable = true }, func(b *t451a.Bundle) { b.Files[4].SHA256 = r.BundleSHA256 }, func(b *t451a.Bundle) {
			b.Files = append(b.Files, t451a.BundleFile{Path: "tools/go/bin/git", Bytes: 1, Executable: true})
		}, func(b *t451a.Bundle) {
			b.Files = append(b.Files, t451a.BundleFile{Path: "workspace/MODULE.bazel", Bytes: 1})
		}} {
			bad := bundle
			bad.Files = slices.Clone(bundle.Files)
			change(&bad)
			if err := validateNativeLayout(bad, r); err == nil {
				t.Fatal("accepted native input substitution")
			}
		}
	})
	t.Run("raw generated paths and neutral semantic oracle", testNativeSCIPBoundary)
	t.Run("partial STOP cannot become success", func(t *testing.T) {
		e := NativeEvidence{Version: "phebs-t451b-native-evidence-v1", Request: validNativeRequest(t), Decision: "STOP", Stage: "planning", WallNanoseconds: 1}
		b, _ := json.Marshal(e)
		if _, err := DecodeNativeEvidence(append(b, '\n')); err != nil {
			t.Fatal("lost valid partial STOP", err)
		}
		for _, change := range []func(*NativeEvidence){func(e *NativeEvidence) { e.Decision = "GO" }, func(e *NativeEvidence) { e.Decision = "COHORT_OBSERVED"; e.Stage = "complete" }, func(e *NativeEvidence) { e.Stage = "guessed compilation error" }, func(e *NativeEvidence) { e.SCIP = []byte("partial") }, func(e *NativeEvidence) { e.Oracle = &NativeSCIPFacts{} }, func(e *NativeEvidence) { e.WallNanoseconds = 0 }} {
			bad := e
			change(&bad)
			b, _ := json.Marshal(bad)
			if _, err := DecodeNativeEvidence(append(b, '\n')); err == nil {
				t.Fatal("accepted unsubstantiated partial evidence")
			}
		}
	})
	t.Run("primary failure survives measurement", func(t *testing.T) {
		first, second := errors.New("planning failure"), errors.New("sampling failure")
		for _, prior := range []error{nil, first} {
			e := NativeEvidence{Stage: "planning"}
			got := prior
			recordNativeFailure(&e, &got, "containment/measurement", second)
			want := "planning"
			if prior == nil {
				want = "containment/measurement"
			}
			if e.Stage != want || !errors.Is(got, second) || prior != nil && !errors.Is(got, prior) {
				t.Fatal("lost primary or measurement failure")
			}
			recordNativeFailure(&e, &got, "validation", nil)
			if e.Stage != want {
				t.Fatal("nil failure changed attribution")
			}
		}
	})
	t.Run("public archive data only", func(t *testing.T) {
		if *publicArchiveTest == "" {
			t.Skip("explicit retained public archive path required")
		}
		archive, err := os.ReadFile(*publicArchiveTest)
		if err != nil {
			t.Fatal(err)
		}
		for _, cohort := range []string{"neutral", "ordinary", "proto", "fanout"} {
			original, owned, err := nativeWorkspaceFiles(cohort, archive, []byte("pinned helper"))
			if err != nil {
				t.Fatal(err)
			}
			if len(owned["phebs_plan/aspect.bzl"]) == 0 || !bytes.Contains(owned["phebs_excluded/BUILD.bazel"], []byte("/scratch/t451b-excluded-sentinel")) {
				t.Fatal("owned profile incomplete")
			}
			if cohort != "neutral" {
				public, _ := publicSources(archive)
				if !reflect.DeepEqual(original, public) {
					t.Fatal("public source changed")
				}
			} else {
				if bytes.Contains(owned["MODULE.bazel"], []byte("go_sdk.host")) || !bytes.Contains(owned["MODULE.bazel"], []byte(`version = "0.59.0"`)) || !bytes.Contains(owned["external/MODULE.bazel"], []byte(`version = "0.59.0"`)) {
					t.Fatal("native dependency/SDK selection changed")
				}
				if !bytes.Contains(owned["lib/BUILD.bazel"], []byte(`"inactive_windows.go"`)) {
					t.Fatal("inactive source not declared")
				}
				match, err := planner.MatchGoFile("lib/inactive_windows.go", owned["lib/inactive_windows.go"], planner.GoMode{GOOS: "linux", GOARCH: "arm64", Cgo: true, Tags: []string{}})
				if err != nil || match {
					t.Fatal("inactive source positive control", match, err)
				}
			}
		}
		archive[len(archive)-1] ^= 1
		if _, err := publicSources(archive); err == nil {
			t.Fatal("admitted changed archive")
		}
	})
}

func testNativeCallerBoundary(t *testing.T) {
	// Three configured aliases of one unchanged package exercise the closed
	// cohort selector independently of a new native driver execution.
	p := retainedPlan(t)
	var unit []string
	for _, target := range p.Targets {
		if target.Label == "@@//lib:alias" {
			unit = target.Units
		}
	}
	for i := range p.Targets {
		if p.Targets[i].Label == "@@//proto:message_go" || p.Targets[i].Label == "@@//cgo:cgo" {
			p.Targets[i].Units = slices.Clone(unit)
		}
	}
	b, _ := json.Marshal(struct {
		Targets []planner.Target
		Units   []planner.Unit
		SDKs    []planner.SDKPlan
	}{p.Targets, p.Units, p.SDKs})
	p.MappingSHA256 = strings.TrimPrefix(t451a.Digest(b), "sha256:")
	roots, err := nativeRoots(p, "neutral")
	if err != nil {
		t.Fatal(err)
	}
	rows := launcher.NativeGoFiles{Version: "phebs-t451b-native-go-files-v1", MappingSHA256: p.MappingSHA256, DocumentsSHA256: p.DocumentsSHA256, Packages: []launcher.NativeGoFilesPackage{{ID: "@@//lib:lib", GoFiles: []string{}}, {ID: "@@neutral_external+//pkg:pkg", GoFiles: []string{}}}}
	for _, slot := range []string{"load", "scip"} {
		prepared, err := launcher.PrepareNativeCompatibility(p, roots, slot, rows)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := nativeControlBytes(p, roots, rows, "neutral", slot)
		hash := t451a.Digest(data)
		env := nativeCallerEnvironment(prepared, slot, hash)
		argv := append([]string{NativeAdapterPath}, prepared.Invocation().Arguments...)
		wire := callerRequest(env)
		if _, _, err := inspectNativeCall(data, hash, argv, env, launcher.Workspace, wire); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func([]byte) []byte{func(b []byte) []byte { return bytes.Replace(b, []byte(`"mode":8681`), []byte(`"mode":31`), 1) }, func(b []byte) []byte { return bytes.Replace(b, []byte(`"tests":false`), []byte(`"tests":true`), 1) }, func(b []byte) []byte { return append(b, '\n') }} {
			if _, _, err := inspectNativeCall(data, hash, argv, env, launcher.Workspace, change(slices.Clone(wire))); err == nil {
				t.Fatal("accepted altered caller wire")
			}
		}
		badEnv := append(slices.Clone(env), "GOFLAGS=-tags=other")
		if _, _, err := inspectNativeCall(data, hash, argv, badEnv, launcher.Workspace, callerRequest(badEnv)); err == nil {
			t.Fatal("accepted altered environment")
		}
		badArgv := slices.Clone(argv)
		badArgv[0] = AdapterPath
		if _, _, err := inspectNativeCall(data, hash, badArgv, env, launcher.Workspace, wire); err == nil {
			t.Fatal("accepted historical adapter")
		}
		if _, _, err := inspectNativeCall(data, t451a.Digest(nil), argv, env, launcher.Workspace, wire); err == nil {
			t.Fatal("accepted control tamper")
		}
	}
}

func testNativeProbeBoundary(t *testing.T) {
	response := []byte(`{"Roots":["b","a"],"Packages":[{"ID":"a","PkgPath":"example/a","CompiledGoFiles":["a.go"]},{"ID":"b","PkgPath":"example/b","CompiledGoFiles":["b.go","c.go"]},{"ID":"d","PkgPath":"example/d","CompiledGoFiles":["d.go"]}]}`)
	a := loadFact{"a", "example/a", true, 1, true, 0, false}
	b := loadFact{"b", "example/b", true, 2, true, 0, false}
	d := loadFact{"d", "example/d", true, 0, false, 0, false}
	r := loadReport{launcher.CompatibilityMode, []loadFact{a, b}, []loadFact{a, b, d}}
	raw, _ := json.Marshal(r)
	if err := verifyNativeProbe(append(raw, '\n'), response); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*loadReport){func(r *loadReport) { r.Roots = r.Roots[:1] }, func(r *loadReport) { r.Packages = r.Packages[:2] }, func(r *loadReport) { r.Roots[0].TypeInfo = false }, func(r *loadReport) { r.Packages[1].Syntax = 1 }, func(r *loadReport) { r.Packages[2].Errors = 1 }, func(r *loadReport) { r.Packages[2].Types = false }, func(r *loadReport) { r.Packages[2].ID = "outside" }, func(r *loadReport) { r.Packages[1] = r.Packages[0] }} {
		bad := r
		bad.Roots = slices.Clone(r.Roots)
		bad.Packages = slices.Clone(r.Packages)
		change(&bad)
		raw, _ := json.Marshal(bad)
		if err := verifyNativeProbe(append(raw, '\n'), response); err == nil {
			t.Fatal("accepted incomplete typed closure")
		}
	}
}

func testNativeSCIPBoundary(t *testing.T) {
	index := oracleIndex()
	patterns := []string{"example.test/neutral/lib", "example.test/neutral/message"}
	index.Metadata.ToolInfo.Arguments = nativeSCIPArguments("neutral", patterns)
	generated := "../bazel-output/execroot/_main/bazel-out/config/bin/proto/message.go"
	index.Documents = append(index.Documents, &scip.Document{RelativePath: generated, Language: "go"})
	plan := planner.Plan{Documents: []planner.Document{{ID: "a", Kind: "source", ExecPath: "lib/a.go"}, {ID: "b", Kind: "source", ExecPath: "lib/lib.go"}, {ID: "g", Kind: "generated", ExecPath: "bazel-out/config/bin/proto/message.go"}}}
	response := []byte(`{"Roots":["lib","proto"],"Packages":[{"ID":"lib","CompiledGoFiles":["/scratch/workspace/lib/a.go","/scratch/workspace/lib/lib.go"]},{"ID":"proto","CompiledGoFiles":["/scratch/bazel-output/execroot/_main/bazel-out/config/bin/proto/message.go"]}]}`)
	raw, _ := proto.Marshal(index)
	facts, err := verifyNativeSCIP(raw, plan, response, "neutral", patterns)
	if err != nil || facts.Counts.Documents != 3 || facts.Counts.Hovers != 2 || facts.Counts.CrossFileReferences != 1 || facts.CurrentAdmissionEstablished {
		t.Fatal(facts, err)
	}
	found := false
	for _, m := range facts.Members {
		found = found || m.RawPath == generated
	}
	if !found {
		t.Fatal("generated raw path was rewritten")
	}
	for _, change := range []func(*scip.Index){func(i *scip.Index) { i.Documents = i.Documents[:2] }, func(i *scip.Index) { i.Documents[2].RelativePath = "proto/message.go" }, func(i *scip.Index) { i.Documents[1].Occurrences = i.Documents[1].Occurrences[:1] }, func(i *scip.Index) { i.Metadata.ToolInfo.Arguments = scipArguments(patterns) }} {
		bad := proto.Clone(index).(*scip.Index)
		change(bad)
		raw, _ := proto.Marshal(bad)
		if _, err := verifyNativeSCIP(raw, plan, response, "neutral", patterns); err == nil {
			t.Fatal("admitted lost authority or semantics")
		}
	}
}
