//go:build linux

package t421

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

const authorGitRouteSchema = "t42-linux-controller-git-route-diagnostic-v1"

type authorGitRouteRequest struct {
	Schema, Nonce, Workspace, SHA256, Version string
	Controller, LoanFD                        int
}

func (request authorGitRouteRequest) core() string {
	return fmt.Sprintf("/proc/%d/fd/%d/core", request.Controller, request.LoanFD)
}

func decodeAuthorGitRouteRequest(raw []byte, parent int) (authorGitRouteRequest, error) {
	var request authorGitRouteRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 4096 || decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		return request, ErrExecutionGitCustody
	}
	nonce, nonceErr := hex.DecodeString(request.Nonce)
	canonical, err := json.Marshal(request)
	if err != nil || !bytes.Equal(canonical, raw) || request.Schema != authorGitRouteSchema || request.Controller <= 1 || request.Controller != parent ||
		request.LoanFD < 3 || request.LoanFD > 1<<20 || nonceErr != nil || len(nonce) != 32 || hex.EncodeToString(nonce) != request.Nonce ||
		!executionGitAbsolutePath(request.Workspace) || filepath.Base(request.Workspace) != "work" ||
		!validExecutionSHA256(request.SHA256) || !strings.HasPrefix(request.Version, "git version ") || !validPublicToolVersion(request.Version) {
		return authorGitRouteRequest{}, ErrExecutionGitCustody
	}
	return request, nil
}

func TestLinuxAuthorGitRouteClosedShape(t *testing.T) {
	request := authorGitRouteRequest{Schema: authorGitRouteSchema, Nonce: strings.Repeat("ab", 32), Workspace: "/private/work",
		SHA256: SHA256([]byte("git")), Version: "git version 2.53.0", Controller: 42, LoanFD: 17}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := decodeAuthorGitRouteRequest(raw, 42); err != nil || actual != request || actual.core() != "/proc/42/fd/17/core" {
		t.Fatal("fixed controller route shape refused", err)
	}
	for _, mutate := range []func(*authorGitRouteRequest){
		func(r *authorGitRouteRequest) { r.Schema = "another-recipe" }, func(r *authorGitRouteRequest) { r.Controller = 43 },
		func(r *authorGitRouteRequest) { r.LoanFD = 2 }, func(r *authorGitRouteRequest) { r.LoanFD = 1<<20 + 1 },
		func(r *authorGitRouteRequest) { r.Nonce = "unbound" }, func(r *authorGitRouteRequest) { r.Workspace = "relative/work" },
		func(r *authorGitRouteRequest) { r.Workspace = "/private/../work" }, func(r *authorGitRouteRequest) { r.SHA256 = "unbound" },
		func(r *authorGitRouteRequest) { r.Version = "unobserved" },
	} {
		changed := request
		mutate(&changed)
		encoded, _ := json.Marshal(changed)
		if _, err := decodeAuthorGitRouteRequest(encoded, 42); err == nil {
			t.Fatal("foreign/malformed route admitted")
		}
	}
	for _, malformed := range [][]byte{nil, append(bytes.Clone(raw), '\n'), append(bytes.Clone(raw), []byte("{}")...),
		bytes.Replace(raw, []byte(`"Schema":`), []byte(`"Unknown":`), 1),
		bytes.Replace(raw, []byte(`"Controller":42`), []byte(`"Controller":42,"Controller":42`), 1), bytes.Repeat([]byte{'x'}, 4097)} {
		if _, err := decodeAuthorGitRouteRequest(malformed, 42); err == nil {
			t.Fatal("ambiguous/over-bound route admitted")
		}
	}
	// Preserve the actual H2p three controls. commitGraph=false is an additional
	// fixed argv control in the diagnostic, not a changed H2p environment.
	environment := executionGitEnvironment(request.core(), "/private/work/home", "/private/work/tmp")
	for _, expected := range []string{"PATH=" + request.core(), "GIT_EXEC_PATH=" + request.core(), "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_KEY_1=core.untrackedCache", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2=/dev/null"} {
		found := 0
		for _, value := range environment {
			if value == expected {
				found++
			}
		}
		if found != 1 {
			t.Fatal("H2p control missing or duplicated", expected)
		}
	}
}
