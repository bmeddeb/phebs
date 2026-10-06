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
	"time"

	internallauncher "github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
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
	if err != nil {
		t.Logf("native cohort diagnostic: %v", err)
	}
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

func TestAmd64PinsDoNotAdmitArm64NativeCohort(t *testing.T) {
	r := validNativeRequest(t)
	r.GoSHA256 = goDigestAmd64
	r.BazelSHA256 = bazelDigestAmd64
	r.DriverSHA256 = driverDigestAmd64
	r.SCIPGoSHA256 = scipDigestAmd64
	if _, err := DecodeNativeRequest(nativeRequestBytes(r)); err == nil {
		t.Fatal("amd64 tool pins were admitted by the arm64 native cohort")
	}
}

func amd64NativeRequest(t *testing.T) NativeRequest {
	t.Helper()
	r := validNativeRequest(t)
	r.Profile = NativeProfileAmd64
	r.GoSHA256 = goDigestAmd64
	r.BazelSHA256 = bazelDigestAmd64
	r.DriverSHA256 = driverDigestAmd64
	r.SCIPGoSHA256 = scipDigestAmd64
	return r
}

func TestAmd64NativeProfileAdmitsMeasuredPins(t *testing.T) {
	if goDigestAmd64 != provider.GoDigestAmd64 || bazelDigestAmd64 != provider.BazelDigestAmd64 || driverDigestAmd64 != "sha256:"+internallauncher.NativeDriverSHA256Amd64 || scipDigestAmd64 != typedindex.SCIPGoIndexerDigestAmd64 {
		t.Fatal("amd64 admission pins diverged from the measured seals")
	}
	if NativeProfile != "native-linux-arm64-rules-go-059-v2" || Amd64RulesCCVersion != "0.2.14" || Amd64ProtobufVersion != "33.4" {
		t.Fatal("amd64 offline CC pin is ambiguous or the arm64 profile changed")
	}
	r := amd64NativeRequest(t)
	for _, cohort := range []string{"neutral", "ordinary", "proto", "fanout"} {
		r.Cohort = cohort
		got, err := DecodeNativeRequest(nativeRequestBytes(r))
		if err != nil || got.Profile != NativeProfileAmd64 {
			t.Fatal(cohort, err)
		}
	}
	native, err := nativeSandboxRequest(nativeRequestBytes(r))
	if err != nil || !native {
		t.Fatal("amd64 profile did not select the native scratch cap", err)
	}
	mixed := r
	mixed.BazelSHA256 = NativeBazelDigest
	if _, err = DecodeNativeRequest(nativeRequestBytes(mixed)); err == nil {
		t.Fatal("arm64 bazel digest admitted on the amd64 profile")
	}
	swapped := validNativeRequest(t)
	swapped.Profile = NativeProfileAmd64
	if _, err = DecodeNativeRequest(nativeRequestBytes(swapped)); err == nil {
		t.Fatal("arm64 pins admitted on the amd64 profile")
	}
	bundle := t451a.Bundle{Schema: "phebs-t451a-offline-v1", Files: []t451a.BundleFile{
		{Path: "tools/bin/bazel", Bytes: 65821854, SHA256: r.BazelSHA256, Executable: true},
		{Path: "tools/bin/gopackagesdriver", Bytes: 5210483, SHA256: r.DriverSHA256, Executable: true},
		{Path: "tools/cc-sysroot.zip", Bytes: 72352400, SHA256: sysrootDigestAmd64},
		{Path: "tools/corpus/remote-apis-sdks.tar.gz", Bytes: 249496, SHA256: PublicArchiveDigest},
		{Path: "tools/cache/downloader.cfg", Bytes: int64(len(launcher.NativeDownloaderConfig)), SHA256: t451a.Digest([]byte(launcher.NativeDownloaderConfig))},
	}}
	for _, tool := range nativeToolProfiles(r)[1:] {
		bundle.Files = append(bundle.Files, t451a.BundleFile{Path: strings.TrimPrefix(tool.path, "/inputs/"), Bytes: 1, SHA256: tool.sha, Executable: true})
	}
	if err = validateNativeLayout(bundle, r); err != nil {
		t.Fatal(err)
	}
	bad := bundle
	bad.Files = slices.Clone(bundle.Files)
	bad.Files[2].SHA256 = r.BundleSHA256
	if err = validateNativeLayout(bad, r); err == nil {
		t.Fatal("amd64 layout accepted a different sysroot")
	}
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
			{Path: "tools/cache/downloader.cfg", Bytes: int64(len(launcher.NativeDownloaderConfig)), SHA256: t451a.Digest([]byte(launcher.NativeDownloaderConfig))},
		}}
		for _, tool := range nativeToolProfiles(r)[1:] {
			bundle.Files = append(bundle.Files, t451a.BundleFile{Path: strings.TrimPrefix(tool.path, "/inputs/"), Bytes: 1, SHA256: tool.sha, Executable: true})
		}
		if err := validateNativeLayout(bundle, r); err != nil {
			t.Fatal(err)
		}
		if launcher.NativeDownloaderConfig != "block bcr.bazel.build\n" {
			t.Fatal("native registry block differs from approved contract")
		}
		for _, config := range []string{"", "block *\n", "block another.example\n", "block bcr.bazel.build\nallow bcr.bazel.build\n", "block bcr.bazel.build\nrewrite (.*) file:///outside\n"} {
			bad := bundle
			bad.Files = slices.Clone(bundle.Files)
			bad.Files[4].Bytes = int64(len(config))
			bad.Files[4].SHA256 = t451a.Digest([]byte(config))
			if err := validateNativeLayout(bad, r); err == nil {
				t.Fatal("accepted altered downloader configuration")
			}
		}
		withoutConfig := bundle
		withoutConfig.Files = slices.Delete(slices.Clone(bundle.Files), 4, 5)
		if err := validateNativeLayout(withoutConfig, r); err == nil {
			t.Fatal("accepted absent downloader configuration")
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
	t.Run("refusal diagnostics remain STOP only", func(t *testing.T) {
		diagnostic := &t451a.ProcessDiagnostic{Comm: "child", State: "Z", PPID: 1, PGID: 3, SID: 3, NoNewPrivs: true, CapPrm: "0000000000000000"}
		for _, attach := range []func(*NativeEvidence){
			func(e *NativeEvidence) { e.Observations.FailureProcess = diagnostic },
			func(e *NativeEvidence) { e.PlanningQuiescenceProcess = diagnostic },
			func(e *NativeEvidence) { e.FinalQuiescenceProcess = diagnostic },
		} {
			e := NativeEvidence{Version: "phebs-t451b-native-evidence-v1", Request: validNativeRequest(t), Decision: "STOP", Stage: "containment/measurement", WallNanoseconds: 1}
			attach(&e)
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeNativeEvidence(append(b, '\n'))
			if err != nil || got.Observations.FailureProcess == nil && got.PlanningQuiescenceProcess == nil && got.FinalQuiescenceProcess == nil {
				t.Fatal("lost partial refusal diagnostic", err)
			}
			if err := validateNativeMeasurements(e); err == nil || err.Error() != "native refusal diagnostics cannot establish completion" {
				t.Fatal("diagnostic admitted as healthy measurement", err)
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
	t.Run("planning scratch diagnostic", func(t *testing.T) {
		for _, snapshot := range []t451a.PlanningScratch{{}, {Available: true}, {Available: true, FreeBlocks: 12, FreeInodes: 34}} {
			e := NativeEvidence{Version: "phebs-t451b-native-evidence-v1", Request: validNativeRequest(t), Decision: "STOP", Stage: "planning", WallNanoseconds: 1, PlanningScratch: &snapshot}
			b, _ := json.Marshal(e)
			got, err := DecodeNativeEvidence(append(b, '\n'))
			if err != nil || got.PlanningScratch == nil || *got.PlanningScratch != snapshot {
				t.Fatal("lost planning snapshot", err)
			}
			if validateNativeMeasurements(e) == nil {
				t.Fatal("failure snapshot became completion proof")
			}
		}
		for _, change := range []func(*NativeEvidence){
			func(e *NativeEvidence) { e.PlanningScratch.FreeBlocks = 1 },
			func(e *NativeEvidence) { e.PlanningScratch.FreeInodes = 1 },
			func(e *NativeEvidence) { e.Stage = "containment/measurement" },
			func(e *NativeEvidence) { e.Decision = "COHORT_OBSERVED" },
		} {
			e := NativeEvidence{Version: "phebs-t451b-native-evidence-v1", Request: validNativeRequest(t), Decision: "STOP", Stage: "planning", WallNanoseconds: 1, PlanningScratch: &t451a.PlanningScratch{}}
			change(&e)
			b, _ := json.Marshal(e)
			if _, err := DecodeNativeEvidence(append(b, '\n')); err == nil {
				t.Fatal("accepted invalid planning snapshot")
			}
		}
	})
	t.Run("native cache inode ceiling", func(t *testing.T) {
		e := NativeEvidence{WallNanoseconds: 1, Observations: newProcessObserver(nil, 2, 65534).facts,
			Cache: PrivateCacheObservation{Version: "phebs-t451b-private-cache-v1", Complete: true,
				Roots: []string{"/scratch/bazel-user", "/scratch/bazel-output", "/scratch/repository-cache", "/scratch/gocache", "/scratch/gomodcache", "/scratch/cache"}}}
		e.Observations.DurationNanoseconds = 1
		for _, entries := range []uint64{65536, 65537, 262144, 262145} {
			e.Cache.Entries, e.Cache.RegularFiles, e.Cache.UniqueInodes = entries, entries, entries
			if err := validateNativeMeasurements(e); (err == nil) != (entries <= 262144) {
				t.Fatalf("native entries=%d: %v", entries, err)
			}
		}
		if sandbox.ScratchInodes != 65536 || sandbox.NativeT451bScratchInodes != 262144 {
			t.Fatal("profile caps differ from approval")
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
		public, err := publicSources(archive)
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
				if !reflect.DeepEqual(original, public) {
					t.Fatal("public source changed")
				}
			} else {
				// A smaller module graph can select versions absent from the
				// public lock's offline cache, before any requested target loads.
				for _, line := range strings.Split(string(public["MODULE.bazel"]), "\n") {
					if strings.HasPrefix(line, "bazel_dep(") && !bytes.Contains(owned["MODULE.bazel"], []byte(line+"\n")) {
						t.Fatal("native neutral omits public module constraint", line)
					}
				}
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

func nativeTestPlan(t *testing.T) (planner.Plan, []planner.Configured) {
	t.Helper()
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
	return p, roots
}

func testNativeCallerBoundary(t *testing.T) {
	p, roots := nativeTestPlan(t)
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
		for _, replacement := range []string{"", "--repository_disable_download=false", "--norepository_disable_download", "--repository_disable_download --downloader_config=/scratch/other"} {
			common := strings.Join(launcher.NativeBazelCommon(), " ")
			if !strings.Contains(common, "--repository_disable_download") {
				t.Fatal("ineffective downloader environment mutation")
			}
			// The adapter derives these flags internally; callers cannot
			// inject them even with a matching go/packages request envelope.
			changed := append(slices.Clone(env), "GOPACKAGESDRIVER_BAZEL_COMMON_FLAGS="+strings.Replace(common, "--repository_disable_download", replacement, 1))
			if _, _, err := inspectNativeCall(data, hash, argv, changed, launcher.Workspace, callerRequest(changed)); err == nil {
				t.Fatal("accepted altered downloader environment and matching wire")
			}
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

func TestNativeFailureEvidence(t *testing.T) {
	encode := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return append(b, '\n')
	}
	plan, roots := nativeTestPlan(t)
	_, _, original := retainedCall(t)
	var response struct {
		Packages []struct {
			ID      string
			GoFiles []string
		}
	}
	if err := json.Unmarshal(original.Result.DriverResponse, &response); err != nil {
		t.Fatal(err)
	}
	rows := launcher.NativeGoFiles{Version: "phebs-t451b-native-go-files-v1", MappingSHA256: plan.MappingSHA256, DocumentsSHA256: plan.DocumentsSHA256}
	for _, p := range response.Packages {
		slices.Sort(p.GoFiles)
		rows.Packages = append(rows.Packages, launcher.NativeGoFilesPackage{ID: p.ID, GoFiles: p.GoFiles})
	}
	slices.SortFunc(rows.Packages, func(a, b launcher.NativeGoFilesPackage) int { return strings.Compare(a.ID, b.ID) })
	var completed []LegEvidence
	for _, slot := range []string{"load", "scip"} {
		prepared, err := launcher.PrepareNativeCompatibility(plan, roots, slot, rows)
		if err != nil {
			t.Fatal(err)
		}
		control, err := nativeControlBytes(plan, roots, rows, "neutral", slot)
		if err != nil {
			t.Fatal(err)
		}
		hash := t451a.Digest(control)
		env := nativeCallerEnvironment(prepared, slot, hash)
		wire := callerRequest(env)
		call := CallEvidence{Slot: slot, PlanSHA256: hash, Argv: append([]string{NativeAdapterPath}, prepared.Invocation().Arguments...), Environment: env, Directory: launcher.Workspace, Request: wire, RequestSHA256: t451a.Digest(wire), Launcher: prepared.Invocation(), LauncherSHA256: prepared.Digest(), Result: original.Result, DriverResponseSHA256: original.DriverResponseSHA256, ResponseSHA256: original.ResponseSHA256}
		if err = verifyNativeCall(plan, roots, rows, "neutral", slot, call); err != nil {
			t.Fatal("positive trace control", err)
		}
		for _, point := range []string{"protocol", "files", "probe"} {
			if slot == "scip" && point == "probe" {
				continue
			}
			t.Run(slot+"/"+point+" validation failure", func(t *testing.T) {
				trace := &call
				if point == "protocol" {
					trace = nil
				}
				failed := nativeLegFailure(slot, point, false, time.Now().Add(-time.Millisecond), nil, nil, trace)
				stage := "package-load/typecheck"
				if slot == "scip" {
					stage = "indexer-unlocalized"
				}
				e := NativeEvidence{Version: "phebs-t451b-native-evidence-v1", Request: validNativeRequest(t), Decision: "STOP", Stage: stage, WallNanoseconds: failed.WallNanoseconds + 1, Plan: &plan, GoFiles: &rows, Legs: completed, FailedLeg: failed}
				if _, err := DecodeNativeEvidence(encode(e)); err != nil {
					t.Fatal("lost validation failure", err)
				}
				// Aggregate-output refusal removes bulky proof but preserves the
				// diagnostic identities and primary phase without claiming proof.
				e.OmittedEvidenceBytes, e.OmittedEvidenceSHA256 = sandbox.OutputBytes, t451a.Digest(encode(e))
				e.Plan, e.GoFiles, e.Legs, e.FailedLeg.Call = nil, nil, nil, nil
				e.FailedLeg.Protocol = "unproven"
				if _, err := DecodeNativeEvidence(encode(e)); err != nil {
					t.Fatal("lost bounded failure summary", err)
				}
			})
		}
		trace := encode(call)
		invalid := call
		invalid.ResponseSHA256 = t451a.Digest(nil)
		invalidTrace := encode(invalid)
		for _, tc := range []struct {
			name    string
			trace   []byte
			readErr error
			proved  bool
		}{
			{"completed driver call", trace, nil, true},
			{"missing trace", nil, os.ErrNotExist, false},
			{"empty trace", nil, nil, false},
			{"malformed trace", []byte(`{"slot":`), nil, false},
			{"invalid trace", invalidTrace, nil, false},
		} {
			t.Run(slot+"/"+tc.name, func(t *testing.T) {
				retained, traceErr := nativeCallFromTrace(plan, roots, rows, "neutral", slot, tc.trace, tc.readErr)
				if (traceErr == nil) != tc.proved || (retained != nil) != tc.proved {
					t.Fatal("protocol proof mismatch", traceErr)
				}
				stdout, stderr := []byte("private failed stdout"), []byte("private failed stderr")
				failed := nativeLegFailure(slot, "client", true, time.Now().Add(-time.Millisecond), stdout, stderr, retained)
				if failed.StdoutBytes != len(stdout) || failed.StdoutSHA256 != t451a.Digest(stdout) || failed.StderrBytes != len(stderr) || failed.StderrSHA256 != t451a.Digest(stderr) || (failed.Protocol == "completed") != tc.proved {
					t.Fatal("lost failed client identity")
				}
				stage := "package-load/typecheck"
				if slot == "scip" {
					stage = "indexer-unlocalized"
				}
				e := NativeEvidence{Version: "phebs-t451b-native-evidence-v1", Request: validNativeRequest(t), Decision: "STOP", Stage: stage, WallNanoseconds: failed.WallNanoseconds + 1, Plan: &plan, GoFiles: &rows, Legs: completed, FailedLeg: failed}
				b := encode(e)
				if bytes.Contains(b, stdout) || bytes.Contains(b, stderr) {
					t.Fatal("raw failed diagnostics escaped into evidence")
				}
				decoded, err := DecodeNativeEvidence(b)
				if err != nil || decoded.Decision != "STOP" || decoded.FailedLeg.Protocol != failed.Protocol || len(decoded.Legs) != len(completed) {
					t.Fatal("lost partial STOP", err)
				}
				for _, mutate := range []func(*NativeEvidence){
					func(e *NativeEvidence) { e.Decision = "COHORT_OBSERVED" },
					func(e *NativeEvidence) { e.Stage = "validation" },
					func(e *NativeEvidence) { e.FailedLeg.ClientError = false },
					func(e *NativeEvidence) { e.FailedLeg.StdoutBytes = maxClientBytes + 1 },
					func(e *NativeEvidence) { e.FailedLeg.StderrSHA256 = "unknown" },
					func(e *NativeEvidence) { e.FailedLeg.Protocol = "target failure" },
				} {
					var bad NativeEvidence
					_ = json.Unmarshal(b, &bad)
					mutate(&bad)
					wire := encode(bad)
					if _, err = DecodeNativeEvidence(wire); err == nil {
						t.Fatal("admitted unsubstantiated failure evidence")
					}
				}
				// The sandbox-error and incomplete-result branches retain the same
				// source-free STOP and expose only bounded private operator text.
				outerStderr := []byte("operator diagnostic " + strings.Repeat("x", 8192) + "beyond-bound")
				for _, runErr := range []error{errors.New("sandbox refused"), nil} {
					r, err := finishNativeReceipt(NativeReceipt{Outcome: "STOP", Stage: "sandbox", Request: e.Request, InputsRemoved: true}, sandbox.Result{Stdout: b, Stderr: outerStderr, ExitCode: 1, Removed: true, Resources: sandbox.Resources{LimitsVerified: true}}, runErr)
					if err == nil || runErr != nil && !errors.Is(err, runErr) || r.Outcome != "STOP" || r.Stage != stage || !bytes.Equal(r.NativeEvidence, b) || r.StderrBytes != len(outerStderr) || r.StderrSHA256 != t451a.Digest(outerStderr) || !strings.Contains(err.Error(), "operator diagnostic") || strings.Contains(err.Error(), "beyond-bound") {
						t.Fatal("lost refusal or bounded outer diagnostics", err)
					}
				}
				if tc.proved {
					// The host repeats full validation; it does not trust the worker's
					// completed marker or the earlier successful load invocation.
					e.FailedLeg.Call.ResponseSHA256 = t451a.Digest(nil)
					bad := encode(e)
					if _, err = DecodeNativeEvidence(bad); err == nil {
						t.Fatal("accepted a forged completed trace")
					}
				}
			})
		}
		if slot == "load" {
			var flat nativeFlat
			_ = json.Unmarshal(call.Result.Response, &flat)
			report := loadReport{Mode: launcher.CompatibilityMode}
			for _, p := range flat.Packages {
				fact := loadFact{ID: p.ID, Path: p.PkgPath, Types: true}
				if slices.Contains(flat.Roots, p.ID) {
					fact.Syntax, fact.TypeInfo = len(p.CompiledGoFiles), true
					report.Roots = append(report.Roots, fact)
				}
				report.Packages = append(report.Packages, fact)
			}
			order := func(a, b loadFact) int { return strings.Compare(a.ID, b.ID) }
			slices.SortFunc(report.Roots, order)
			slices.SortFunc(report.Packages, order)
			stdout := encode(report)
			completed = []LegEvidence{{Slot: slot, ClientArgv: append([]string{NativeProbePath}, call.Launcher.Arguments...), Environment: env, WallNanoseconds: 1, Stdout: stdout, Call: call}}
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
