package t451b

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
)

const (
	NativeProfile       = "native-linux-arm64-rules-go-059-v2"
	NativeProfileAmd64  = "native-linux-amd64-rules-go-059-v1"
	NativeAdapterPath   = "/inputs/tools/bin/phebs-t451b-native-driver"
	NativeProbePath     = "/inputs/tools/bin/t451b-native-probe"
	NativeBazelDigest   = "sha256:cab23c59d3d39c5e5382f12cd116b47445afdff9813516c18ae3ee8836b3037f"
	PublicModule        = "github.com/bazelbuild/remote-apis-sdks"
	PublicCommit        = "d5824b1a2286806b07efd030aa3a139c4f540157"
	PublicArchiveDigest = "sha256:c9ecf680cd7bd0d88d8a6d1a0084a09c0a9dc45145fc28fbdcda888586d54bcc"
	PublicArchivePath   = "/inputs/tools/corpus/remote-apis-sdks.tar.gz"
	nativeSDKRoot       = "external/rules_go++go_sdk+go_default_sdk"
	// Measured linux/amd64 pins. They admit only NativeProfileAmd64.
	goDigestAmd64      = "sha256:b93cdfdbc72f1afc3f21498c80bf3d155a44a9b95e2d690c940511051574bc25"
	bazelDigestAmd64   = "sha256:c44a93f25398c68f904fa1d19b61d321de6c0d2f09dca375d7bc0dc9b9428403"
	driverDigestAmd64  = "sha256:2b58a9c9a294fc8d9c899bd66f881f7236ed4422a998a4cebab07662ec373bb8"
	scipDigestAmd64    = "sha256:31bf2f3bbbcb25efd4bba6964e08971a9c9c2fba745db4345c0d438ef28b93c4"
	sysrootDigestAmd64 = "sha256:2f2ec79d40bb602c2c957ffa2f4be62ec2709a53e28060c57e5d5664a7f8dcde"
)

type nativeProfilePins struct {
	goSHA, bazelSHA, driverSHA, scipSHA, sysrootSHA string
}

func pinsForNativeProfile(profile string) (nativeProfilePins, bool) {
	switch profile {
	case NativeProfile:
		return nativeProfilePins{GoDigest, NativeBazelDigest, "sha256:" + launcher.NativeDriverSHA256, SCIPDigest, ""}, true
	case NativeProfileAmd64:
		return nativeProfilePins{goDigestAmd64, bazelDigestAmd64, driverDigestAmd64, scipDigestAmd64, sysrootDigestAmd64}, true
	default:
		return nativeProfilePins{}, false
	}
}

// NativeProbeSource is compiled in the same isolated client module graph as
// ProbeSource; the historical source and executable remain unchanged.
//
//go:embed probe/native.go.txt
var NativeProbeSource []byte

type NativeRequest struct {
	Schema            string `json:"schema"`
	Profile           string `json:"profile"`
	Cohort            string `json:"cohort"`
	BundleSHA256      string `json:"bundle_sha256"`
	HelperSHA256      string `json:"helper_sha256"`
	ProbeSHA256       string `json:"probe_sha256"`
	ProbeSourceSHA256 string `json:"probe_source_sha256"`
	SCIPGoSHA256      string `json:"scip_go_sha256"`
	GoSHA256          string `json:"go_sha256"`
	BazelSHA256       string `json:"bazel_sha256"`
	DriverSHA256      string `json:"driver_sha256"`
	ArchiveSHA256     string `json:"archive_sha256"`
	AspectSHA256      string `json:"aspect_sha256"`
	ImageID           string `json:"image_id"`
}

func cohortRoots(cohort string) ([]string, error) {
	switch cohort {
	case "neutral":
		return []string{"//lib:alias", "//proto:message_go", "//cgo:cgo"}, nil
	case "ordinary":
		return []string{"//go/pkg/moreflag:moreflag", "//go/pkg/cache:cache", "//go/pkg/outerr:outerr"}, nil
	case "proto":
		return []string{"//go/api/command:command", "//go/pkg/command:command"}, nil
	case "fanout":
		return []string{"//go/pkg/client:client", "//go/pkg/cas:cas", "//go/pkg/rexec:rexec"}, nil
	default:
		return nil, errors.New("unknown native cohort")
	}
}

func DecodeNativeRequest(data []byte) (NativeRequest, error) {
	r, err := decode[NativeRequest](data, maxRequestBytes, true)
	if err != nil {
		return r, err
	}
	_, err = cohortRoots(r.Cohort)
	aspect, aspectErr := NativeAspect()
	pins, pinned := pinsForNativeProfile(r.Profile)
	if err != nil || aspectErr != nil || r.Schema != "phebs-t451b-native-request-v1" || !pinned || !digest(r.BundleSHA256) || !digest(r.HelperSHA256) || !digest(r.ProbeSHA256) || !digest(r.ImageID) || r.ProbeSourceSHA256 != t451a.Digest(NativeProbeSource) || r.SCIPGoSHA256 != pins.scipSHA || r.GoSHA256 != pins.goSHA || r.BazelSHA256 != pins.bazelSHA || r.DriverSHA256 != pins.driverSHA || r.ArchiveSHA256 != PublicArchiveDigest || r.AspectSHA256 != t451a.Digest(aspect) {
		return NativeRequest{}, errors.New("native request identity refused")
	}
	return r, nil
}

func nativeToolProfiles(r NativeRequest) []toolProfile {
	return []toolProfile{
		{"/inputs/t451a", r.HelperSHA256, "github.com/bmeddeb/phebs/spike/t451b/cmd/t451b", false},
		{NativeAdapterPath, r.HelperSHA256, "github.com/bmeddeb/phebs/spike/t451b/cmd/t451b", false},
		{NativeProbePath, r.ProbeSHA256, "phebs.local/t451b-native-probe", true},
		{SCIPPath, r.SCIPGoSHA256, "github.com/scip-code/scip-go/cmd/scip-go", true},
		{"/inputs/tools/go/bin/go", r.GoSHA256, "cmd/go", false},
	}
}

func validateNativeLayout(bundle t451a.Bundle, r NativeRequest) error {
	pins, pinned := pinsForNativeProfile(r.Profile)
	if !pinned {
		return errors.New("native profile refused")
	}
	required := map[string]string{"tools/bin/bazel": pins.bazelSHA, "tools/bin/gopackagesdriver": r.DriverSHA256, "tools/cc-sysroot.zip": pins.sysrootSHA, "tools/corpus/remote-apis-sdks.tar.gz": PublicArchiveDigest, "tools/cache/downloader.cfg": t451a.Digest([]byte(launcher.NativeDownloaderConfig))}
	for _, tool := range nativeToolProfiles(r)[1:] {
		required[strings.TrimPrefix(tool.path, "/inputs/")] = tool.sha
	}
	for _, f := range bundle.Files {
		if !strings.HasPrefix(f.Path, "tools/") {
			return errors.New("native bundle may supply only tools")
		}
		if want, ok := required[f.Path]; ok {
			executable := strings.HasPrefix(f.Path, "tools/bin/") || f.Path == "tools/go/bin/go"
			if f.Bytes <= 0 || f.Executable != executable || want != "" && f.SHA256 != want {
				return errors.New("native required input identity mismatch")
			}
			delete(required, f.Path)
		} else if strings.HasPrefix(f.Path, "tools/bin/") || strings.HasPrefix(f.Path, "tools/go/bin/") && f.Path != "tools/go/bin/gofmt" {
			return errors.New("unlisted native caller executable")
		}
	}
	if len(required) != 0 {
		return errors.New("native required inputs missing")
	}
	return nil
}

func nativeRequestBytes(r NativeRequest) []byte {
	b, _ := json.MarshalIndent(r, "", "  ")
	return append(b, '\n')
}
