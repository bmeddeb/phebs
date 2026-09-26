package t451a

import (
	"errors"
	"strings"
)

const Profile = "neutral-linux-arm64-v1"

// Request has no repository path, argv, environment, rc file, or package pattern.
// The only executable workload is the compiled-in neutral fixture/probe suite.
type Request struct {
	Schema         string `json:"schema"`
	Profile        string `json:"profile"`
	Mode           string `json:"mode"`
	Probe          string `json:"probe"`
	BundleSHA256   string `json:"bundle_sha256"`
	HelperSHA256   string `json:"helper_sha256"`
	PlannerSHA256  string `json:"planner_sha256"`
	LauncherSHA256 string `json:"launcher_sha256"`
	ImageID        string `json:"image_id"`
}

func DecodeRequest(data []byte) (Request, error) {
	request, err := canonicalDecode[Request](data, 4096)
	if err != nil {
		return Request{}, err
	}
	if request.Schema != "phebs-t451a-request-v1" || request.Profile != Profile || !validDigest(request.BundleSHA256) ||
		!validDigest(request.HelperSHA256) || !validDigest(request.PlannerSHA256) || !validDigest(request.LauncherSHA256) || !validDigest(request.ImageID) {
		return Request{}, errors.New("request identity refused")
	}
	if request.HelperSHA256 != request.LauncherSHA256 || request.HelperSHA256 != request.PlannerSHA256 {
		return Request{}, errors.New("planner and launcher must be the pinned owned helper")
	}
	switch request.Mode {
	case "plan":
		if request.Probe != "" {
			return Request{}, errors.New("plan cannot select a probe")
		}
	case "probe":
		switch request.Probe {
		case "access", "detached", "output", "memory", "descriptors", "tasks", "scratch-bytes", "scratch-inodes", "watchdog":
		default:
			return Request{}, errors.New("unknown neutral probe")
		}
	default:
		return Request{}, errors.New("unknown neutral mode")
	}
	return request, nil
}

func validateToolLayout(bundle Bundle) error {
	var bazel, goSDK, driver, compiler bool
	for _, file := range bundle.Files {
		if !strings.HasPrefix(file.Path, "tools/") {
			return errors.New("offline bundle may supply only tools")
		}
		switch file.Path {
		case "tools/bin/bazel":
			bazel = file.Executable
		case "tools/go/bin/go":
			goSDK = file.Executable
		case "tools/bin/gopackagesdriver":
			driver = file.Executable
		case "tools/cc-sysroot.zip":
			compiler = !file.Executable && file.Bytes > 0
		}
	}
	if !bazel || !goSDK || !driver || !compiler {
		return errors.New("missing pinned Bazel, Go SDK, driver, or compiler material")
	}
	return nil
}
