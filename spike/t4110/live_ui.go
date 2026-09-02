package t4110

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/spike/t4013"
)

const liveUIProbeSchema = "t4110-live-ui-probe-v1"

type liveUIProbeInput struct {
	BaseURL                string `json:"base_url"`
	BearerToken            string `json:"bearer_token"`
	BrowserPath            string `json:"browser_path"`
	CatalogControlRevision uint64 `json:"catalog_control_revision"`
	CatalogGeneration      string `json:"catalog_generation"`
	Repository             string `json:"repository"`
	ServiceKey             string `json:"service_key"`
	StateControlRevision   uint64 `json:"state_control_revision"`
}

type liveUIProbeReport struct {
	Schema             string `json:"schema"`
	AuthStatusRequests int    `json:"auth_status_requests"`
	VersionRequests    int    `json:"version_requests"`
	InventoryRequests  int    `json:"inventory_requests"`
	DetailRequests     int    `json:"detail_requests"`
	CatalogServices    int    `json:"catalog_services"`
	PageServices       int    `json:"page_services"`
	ConsoleErrors      int    `json:"console_errors"`
	PageErrors         int    `json:"page_errors"`
	RequestFailures    int    `json:"request_failures"`
	APIFailures        int    `json:"api_failures"`
	ExternalRequests   int    `json:"external_requests"`
	Passed             bool   `json:"passed"`
}

func (report liveUIProbeReport) valid() bool {
	return report.Schema == liveUIProbeSchema &&
		report.AuthStatusRequests == 1 && report.VersionRequests == 1 &&
		report.InventoryRequests == 1 && report.DetailRequests == 2 &&
		report.CatalogServices == 10_000 && report.PageServices == 50 &&
		report.ConsoleErrors == 0 && report.PageErrors == 0 &&
		report.RequestFailures == 0 && report.APIFailures == 0 &&
		report.ExternalRequests == 0 && report.Passed
}

func decodeLiveUIProbeReport(data []byte) (liveUIProbeReport, error) {
	var report liveUIProbeReport
	if len(data) == 0 || len(data) > 4<<10 || json.Unmarshal(data, &report) != nil {
		return report, errors.New("live UI probe output is invalid")
	}
	canonical, err := json.Marshal(report)
	if err != nil {
		return report, err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(data, canonical) || !report.valid() {
		return report, errors.New("live UI probe output is not an exact pass")
	}
	return report, nil
}

type liveUIRequestCounts struct {
	authStatus int
	version    int
	inventory  int
	detail     int
	unexpected int
}

type liveUIRequestGate struct {
	mu         sync.Mutex
	token      string
	serviceKey string
	counts     liveUIRequestCounts
}

func (gate *liveUIRequestGate) record(request *http.Request) bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	wantQuery := url.Values{}
	switch request.URL.Path {
	case "/api/auth/status":
		gate.counts.authStatus++
	case "/api/version":
		gate.counts.version++
	case "/api/services":
		gate.counts.inventory++
		wantQuery = url.Values{
			"repository": {liveRepository},
			"page_size":  {"50"},
		}
	case "/api/service":
		gate.counts.detail++
		wantQuery = url.Values{
			"repository":  {liveRepository},
			"service_key": {gate.serviceKey},
		}
	default:
		gate.counts.unexpected++
		return false
	}
	query, queryErr := url.ParseQuery(request.URL.RawQuery)
	if request.Method != http.MethodGet || queryErr != nil ||
		len(request.Header.Values("Authorization")) != 1 ||
		request.Header.Get("Authorization") != "Bearer "+gate.token ||
		!reflect.DeepEqual(query, wantQuery) {
		gate.counts.unexpected++
		return false
	}
	return true
}

func (gate *liveUIRequestGate) snapshot() liveUIRequestCounts {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.counts
}

func (h *liveHarness) runLiveUIProbe(
	ctx context.Context,
	serviceKey string,
	apiOptions api.Options,
) (liveUIProbeReport, error) {
	if h.composedRoot == "" {
		return liveUIProbeReport{}, errors.New("exact composed UI tree is absent")
	}
	if err := h.composedTools.verify(); err != nil {
		return liveUIProbeReport{}, err
	}
	if err := h.browser.verify(); err != nil {
		return liveUIProbeReport{}, fmt.Errorf("verify browser executable: %w", err)
	}
	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return liveUIProbeReport{}, fmt.Errorf("create live UI bearer: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	apiOptions.APIKey = token
	apiOptions.Principal = func(context.Context) string { return "api-key:t4110-live-ui" }
	apiHandler := api.New(apiOptions)
	requestGate := &liveUIRequestGate{token: token, serviceKey: serviceKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/status", func(writer http.ResponseWriter, request *http.Request) {
		if !requestGate.record(request) {
			http.Error(writer, "invalid live UI request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(
			`{"authenticated":true,"auth_required":true,"setup_required":false,` +
				`"oidc_enabled":false,"password_enabled":false,` +
				`"user":{"id":"t4110","email":"gate@neutral.invalid","is_admin":false}}`,
		))
	})
	mux.Handle("/api/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !requestGate.record(request) {
			http.Error(writer, "invalid live UI request", http.StatusBadRequest)
			return
		}
		apiHandler.ServeHTTP(writer, request)
	}))
	mux.Handle("/", http.FileServer(http.Dir(filepath.Join(h.composedRoot, "ui", "dist"))))

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return liveUIProbeReport{}, fmt.Errorf("listen for live UI probe: %w", err)
	}
	server := httptest.NewUnstartedServer(mux)
	server.Listener = listener
	server.Start()
	defer server.Close()

	input, err := json.Marshal(liveUIProbeInput{
		BaseURL: server.URL + "/", BearerToken: token, BrowserPath: h.browser.path,
		CatalogControlRevision: h.selector.CatalogControlRevision,
		CatalogGeneration:      h.selector.CatalogRootDigest,
		Repository:             liveRepository,
		ServiceKey:             serviceKey,
		StateControlRevision:   h.selector.StateControlRevision,
	})
	if err != nil {
		return liveUIProbeReport{}, err
	}
	command := exec.CommandContext(
		ctx, h.composedTools.node.path,
		filepath.Join(h.composedRoot, "spike", "t4110", "browser_probe.mjs"),
	)
	command.Dir = h.composedRoot
	command.Env = composedEnvironment(h.composedTools, h.composedRoot, false)
	command.Stdin = bytes.NewReader(input)
	output, err := t4013.RunCustodyCombinedOutput(command)
	if err != nil {
		return liveUIProbeReport{}, errors.Join(
			errors.New("live UI browser proof failed"), err, boundedCommandError(string(output)),
		)
	}
	report, err := decodeLiveUIProbeReport(output)
	if err != nil {
		return liveUIProbeReport{}, err
	}
	counts := requestGate.snapshot()
	if counts != (liveUIRequestCounts{
		authStatus: 1, version: 1, inventory: 1, detail: 2,
	}) {
		return liveUIProbeReport{}, errors.New("live UI server request count is not exact")
	}
	if err := h.composedTools.verify(); err != nil {
		return liveUIProbeReport{}, err
	}
	if err := h.browser.verify(); err != nil {
		return liveUIProbeReport{}, fmt.Errorf("reverify browser executable: %w", err)
	}
	return report, nil
}
