package t451a

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var nativeSocket = flag.String("t451a-native-socket", "", "explicit local Docker socket; empty skips native containment gates")
var nativeImage = flag.String("t451a-native-image", "", "exact Linux arm64 image ID")
var nativeParent = flag.String("t451a-native-parent", "", "private parent visible at the same path to host and daemon")
var nativeBundle = flag.String("t451a-native-bundle", "", "offline tool bundle for the neutral planner gate")
var nativeManifest = flag.String("t451a-native-manifest", "", "canonical offline tool manifest")
var nativeBundleDigest = flag.String("t451a-native-bundle-sha256", "", "exact offline manifest identity")
var nativeReceipt = flag.String("t451a-native-receipt", "", "optional new file for the full neutral plan receipt")

func nativeHelper(t *testing.T) (string, string, string) {
	t.Helper()
	if *nativeSocket == "" {
		t.Skip("native gate requires explicit socket, image and private parent")
	}
	if *nativeParent == "" || !validDigest(*nativeImage) {
		t.Fatal("missing closed native gate inputs")
	}
	root, err := os.MkdirTemp(*nativeParent, "native-gate-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Only remove this test's build image. Run owns input/container custody;
		// interrupted or failed cleanup leaves its private owner record intact.
		if err := os.Remove(filepath.Join(root, "t451a-linux")); err != nil {
			t.Error(err)
		}
		if err := os.Remove(root); err != nil {
			t.Log("private gate root retained")
		}
	})
	helper := filepath.Join(root, "t451a-linux")
	build := exec.Command("go", "build", "-trimpath", "-o", helper, "./cmd/t451a")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GOOS=") && !strings.HasPrefix(value, "GOARCH=") && !strings.HasPrefix(value, "CGO_ENABLED=") {
			build.Env = append(build.Env, value)
		}
	}
	build.Env = append(build.Env, "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, output)
	}
	data, err := readBounded(helper, MaxFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	helperDigest := Digest(data)
	return root, helper, helperDigest
}

func TestNativeBoundary(t *testing.T) {
	root, helper, helperDigest := nativeHelper(t)
	for _, probe := range []string{"access", "watchdog", "descriptors", "scratch-bytes", "scratch-inodes", "tasks", "output", "memory"} {
		t.Run(probe, func(t *testing.T) {
			request := Request{Schema: "phebs-t451a-request-v1", Profile: Profile, Mode: "probe", Probe: probe, BundleSHA256: Digest(nil),
				HelperSHA256: helperDigest, PlannerSHA256: helperDigest, LauncherSHA256: helperDigest, ImageID: *nativeImage}
			receipt, err := Run(context.Background(), Options{Socket: *nativeSocket, Parent: root, Helper: helper, Request: request})
			encoded, _ := json.Marshal(receipt)
			t.Log(string(encoded))
			if !receipt.Removed || !receipt.InputsRemoved || !receipt.Resources.LimitsVerified {
				t.Fatalf("native boundary not established: %v", err)
			}
			switch probe {
			case "tasks":
				if err == nil || receipt.StopReason != "process_limit" || receipt.Resources.TaskLimitEvents == 0 {
					t.Fatal("task limit evidence missing")
				}
			case "output":
				if err == nil || receipt.StopReason != "output_limit" {
					t.Fatal("output limit evidence missing")
				}
			case "memory":
				if err == nil || receipt.StopReason != "memory_limit" || receipt.Resources.MemoryOOMKills == 0 {
					t.Fatal("memory limit evidence missing")
				}
			default:
				if err != nil || receipt.Outcome != "PROBE_OBSERVED" {
					t.Fatalf("boundary probe failed: %v", err)
				}
			}
		})
	}
}

func TestNativePlan(t *testing.T) {
	if *nativeBundle == "" {
		t.Skip("neutral plan requires explicit offline bundle and digest")
	}
	root, helper, helperDigest := nativeHelper(t)
	manifest, err := ReadManifest(*nativeManifest)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Schema: "phebs-t451a-request-v1", Profile: Profile, Mode: "plan", BundleSHA256: *nativeBundleDigest,
		HelperSHA256: helperDigest, PlannerSHA256: helperDigest, LauncherSHA256: helperDigest, ImageID: *nativeImage}
	receipt, err := Run(context.Background(), Options{Socket: *nativeSocket, Parent: root, Helper: helper, Request: request, BundleRoot: *nativeBundle, Manifest: manifest})
	if *nativeReceipt != "" {
		file, createErr := os.OpenFile(*nativeReceipt, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			t.Fatal(createErr)
		}
		encodeErr := json.NewEncoder(file).Encode(receipt)
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatalf("retain receipt: %v, %v", encodeErr, closeErr)
		}
	}
	receipt.NeutralEvidence = nil // Keep routine test logs small; the optional file retains it.
	encoded, _ := json.Marshal(receipt)
	t.Log(string(encoded))
	if err != nil || receipt.Outcome != "NEUTRAL_PLAN_OBSERVED" || !receipt.Removed || !receipt.InputsRemoved {
		t.Fatalf("neutral plan not established: %v", err)
	}
}
