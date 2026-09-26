// Package t451b contains only the closed neutral Bazel/SCIP compatibility spike.
package t451b

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
)

const (
	Profile         = "neutral-linux-arm64-typed-v1"
	AdapterPath     = "/inputs/tools/bin/phebs-t451b-driver"
	ProbePath       = "/inputs/tools/bin/t451b-load-probe"
	SCIPPath        = "/inputs/tools/bin/scip-go"
	GoDigest        = "sha256:4b4667aec6954798f54de64a96addb757ecf4472e566ed397ef36ff01464c389"
	SCIPDigest      = "sha256:7d162fc544b6669fc8470c59480b754ea24339dfb6f27791e2f66346146ba765"
	SCIPVersion     = "v0.2.7"
	SCIPCommit      = "2e9ff3c2603a85daabe125c9f20075ec52df0731"
	ToolsVersion    = "v0.45.0"
	ToolsSum        = "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="
	ModulePath      = "example.test/neutral"
	ModuleVersion   = "t451b-neutral-v1"
	maxRequestBytes = 4096
	maxClientBytes  = 1 << 20
	maxTraceBytes   = 4 << 20
)

// ProbeSource is compiled by the operator in the isolated scip-go module graph.
//
//go:embed probe/main.go.txt
var ProbeSource []byte

type Request struct {
	Schema            string `json:"schema"`
	Profile           string `json:"profile"`
	BundleSHA256      string `json:"bundle_sha256"`
	HelperSHA256      string `json:"helper_sha256"`
	ProbeSHA256       string `json:"probe_sha256"`
	ProbeSourceSHA256 string `json:"probe_source_sha256"`
	SCIPGoSHA256      string `json:"scip_go_sha256"`
	GoSHA256          string `json:"go_sha256"`
	ImageID           string `json:"image_id"`
}

func DecodeRequest(data []byte) (Request, error) {
	r, err := decode[Request](data, maxRequestBytes, true)
	if err != nil {
		return r, err
	}
	if r.Schema != "phebs-t451b-request-v1" || r.Profile != Profile || !digest(r.BundleSHA256) || !digest(r.HelperSHA256) || !digest(r.ProbeSHA256) || !digest(r.ImageID) || r.ProbeSourceSHA256 != t451a.Digest(ProbeSource) || r.SCIPGoSHA256 != SCIPDigest || r.GoSHA256 != GoDigest {
		return Request{}, errors.New("compatibility request identity refused")
	}
	return r, nil
}

func digest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}

// Exact re-encoding rejects duplicate, omitted, case-aliased and unknown keys.
func decode[T any](data []byte, limit int, indent bool) (T, error) {
	var value T
	if len(data) == 0 || len(data) > limit {
		return value, errors.New("wire byte limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return value, err
	}
	var encoded []byte
	var err error
	if indent {
		encoded, err = json.MarshalIndent(value, "", "  ")
	} else {
		encoded, err = json.Marshal(value)
	}
	if err != nil || !bytes.Equal(data, append(encoded, '\n')) {
		return value, errors.New("noncanonical wire")
	}
	return value, nil
}

func readBounded(name string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, errors.New("file type/byte bound")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("file read bound")
	}
	return b, nil
}

func validateLayout(bundle t451a.Bundle, r Request) error {
	required := map[string]string{
		"tools/bin/bazel": "", "tools/go/bin/go": GoDigest, "tools/bin/gopackagesdriver": "sha256:" + launcher.DriverSHA256,
		"tools/bin/scip-go": SCIPDigest, "tools/bin/t451b-load-probe": r.ProbeSHA256, "tools/bin/phebs-t451b-driver": r.HelperSHA256,
	}
	compiler := false
	for _, f := range bundle.Files {
		if !strings.HasPrefix(f.Path, "tools/") {
			return errors.New("bundle may supply only tools")
		}
		if want, ok := required[f.Path]; ok {
			if !f.Executable || f.Bytes == 0 || want != "" && f.SHA256 != want {
				return fmt.Errorf("required tool identity refused: %s", f.Path)
			}
			delete(required, f.Path)
		} else if strings.HasPrefix(f.Path, "tools/bin/") || strings.HasPrefix(f.Path, "tools/go/bin/") && f.Path != "tools/go/bin/gofmt" {
			return errors.New("unlisted caller PATH executable refused")
		}
		if f.Path == "tools/cc-sysroot.zip" {
			compiler = !f.Executable && f.Bytes > 0
		}
	}
	if len(required) != 0 || !compiler {
		return errors.New("required offline tools missing")
	}
	return nil
}
