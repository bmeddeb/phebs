package launcher

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a/planner"
)

func TestNativeCompatDriverPinsStayArchSpecific(t *testing.T) {
	if NativeDriverSHA256 != "f49a0ff4339e32cc699c6fbb5a80b9d8f936b3bfe6b08a924fa19495e35b2bfd" {
		t.Fatal("arm64 driver pin changed")
	}
	if NativeDriverSHA256Amd64 != "2b58a9c9a294fc8d9c899bd66f881f7236ed4422a998a4cebab07662ec373bb8" {
		t.Fatal("amd64 driver pin changed")
	}
	for _, tc := range []struct {
		arch, digest, variantKey, variant string
	}{
		{"arm64", NativeDriverSHA256, "GOARM64", "v8.0"},
		{"amd64", NativeDriverSHA256Amd64, "GOAMD64", "v1"},
		{"386", "", "", ""},
		{"", "", "", ""},
	} {
		t.Run(tc.arch, func(t *testing.T) {
			profile, ok := planner.NativeProfile(tc.arch)
			if ok != (tc.digest != "") || profile.DriverSHA256 != tc.digest || profile.VariantKey != tc.variantKey || profile.Variant != tc.variant || NativeCompatDriverDigest(tc.arch) != tc.digest {
				t.Fatalf("unexpected compatibility profile for %q: %+v", tc.arch, profile)
			}
			if tc.digest == "" {
				if err := checkNativeCompatDriver(tc.arch); err == nil || err.Error() != "driver closed build profile mismatch" {
					t.Fatalf("unsupported architecture reached driver read: %v", err)
				}
			}
		})
	}
}

func TestNativeCompatWorkerRefusesUnadmittedProcess(t *testing.T) {
	want := runtime.GOOS == "linux" && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") && os.Getuid() == 65534
	if got := NativeCompatWorkerAdmitted(); got != want {
		t.Fatalf("worker admission = %t, want %t", got, want)
	}
	if want {
		t.Skip("process is an admitted sandbox worker")
	}
	arch := runtime.GOARCH
	if NativeCompatDriverDigest(arch) == "" {
		arch = "arm64"
	}
	plan, roots := nativeCompatibilityFixture(arch)
	prepared, err := PrepareCompatibility(plan, roots, "load")
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunNativeCompatibility(context.Background(), plan, roots, "load", nativeRows(prepared, plan))
	wantError := "native compatibility driver requires an admitted Linux worker"
	if arch != runtime.GOARCH {
		wantError = "native compatibility plan/worker architecture mismatch"
	}
	if err == nil || err.Error() != wantError {
		t.Fatalf("unadmitted process reached driver check: %v", err)
	}
}

func nativeCompatibilityFixture(arch string) (planner.Plan, []planner.Configured) {
	plan, roots := compatibilityFixture()
	for i := range plan.Units {
		plan.Units[i].Mode.GOARCH = arch
	}
	for i := range plan.SDKs {
		plan.SDKs[i].Mode.GOARCH = arch
	}
	seal(&plan)
	return plan, roots
}

func TestNativeCompatibilityPlanModes(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			plan, roots := nativeCompatibilityFixture(arch)
			declared, err := PrepareCompatibility(plan, roots, "load")
			if err != nil {
				t.Fatal(err)
			}
			matches := nativeRows(declared, plan)
			prepared, err := PrepareNativeCompatibility(plan, roots, "load", matches)
			if err != nil || prepared.mode.GOARCH != arch {
				t.Fatalf("native plan mode: %+v, %v", prepared.mode, err)
			}
			if arch != runtime.GOARCH {
				_, err := RunNativeCompatibility(context.Background(), plan, roots, "load", matches)
				if err == nil || err.Error() != "native compatibility plan/worker architecture mismatch" {
					t.Fatalf("foreign plan reached worker/driver admission: %v", err)
				}
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*planner.Plan)
	}{
		{"unsupported", func(p *planner.Plan) { p.Units[0].Mode.GOARCH = "386" }},
		{"mixed units", func(p *planner.Plan) { p.Units[0].Mode.GOARCH = "arm64" }},
		{"mixed SDK", func(p *planner.Plan) { p.SDKs[0].Mode.GOARCH = "arm64" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, roots := nativeCompatibilityFixture("amd64")
			tc.change(&plan)
			seal(&plan)
			if _, err := PrepareCompatibility(plan, roots, "load"); err == nil {
				t.Fatal("accepted mismatched or unsupported plan mode")
			}
		})
	}
}

func TestNativeCompatibilityResponseArchitecture(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			prepared, raw := compatibilityFileFixture(t)
			prepared.mode.GOARCH = arch
			result, err := finishCompatibility(context.Background(), prepared, raw)
			if err != nil {
				t.Fatal(err)
			}
			var adapted response
			if err := json.Unmarshal(result.Response, &adapted); err != nil {
				t.Fatal(err)
			}
			if adapted.Arch != arch || adapted.Compiler != "gc" || adapted.GoVersion != 25 {
				t.Fatalf("wrong typed response metadata: %+v", adapted)
			}
		})
	}
}
