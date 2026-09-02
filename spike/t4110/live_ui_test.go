package t4110

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestDecodeLiveUIProbeReportRequiresExactPass(t *testing.T) {
	report := liveUIProbeReport{
		Schema:             liveUIProbeSchema,
		AuthStatusRequests: 1, VersionRequests: 1,
		InventoryRequests: 1, DetailRequests: 2,
		CatalogServices: 10_000, PageServices: 50,
		Passed: true,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if got, err := decodeLiveUIProbeReport(encoded); err != nil || got != report {
		t.Fatalf("exact live UI report = %+v, %v", got, err)
	}

	wrongCount := report
	wrongCount.InventoryRequests = 2
	wrong, err := json.Marshal(wrongCount)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeLiveUIProbeReport(append(wrong, '\n')); err == nil {
		t.Fatal("wrong live UI request count was accepted")
	}
	external := report
	external.ExternalRequests = 1
	externalJSON, err := json.Marshal(external)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeLiveUIProbeReport(append(externalJSON, '\n')); err == nil {
		t.Fatal("external live UI request was accepted")
	}
	unknown := append([]byte(nil), encoded[:len(encoded)-2]...)
	unknown = append(unknown, []byte(`,"unknown":true}`+"\n")...)
	if _, err := decodeLiveUIProbeReport(unknown); err == nil {
		t.Fatal("unknown live UI report field was accepted")
	}
	if _, err := decodeLiveUIProbeReport(append(encoded, []byte("{}\n")...)); err == nil {
		t.Fatal("multiple live UI report values were accepted")
	}
}

func TestBrowserProbeBlocksEveryExternalOriginBeforeNavigation(t *testing.T) {
	probe, err := os.ReadFile("browser_probe.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"await context.route('**/*'",
		"!isGateOrigin(route.request().url(), input.base.origin)",
		"await route.abort('blockedbyclient')",
		"await context.routeWebSocket(",
		"!isGateOrigin(url, input.base.origin)",
		"await webSocket.close({ code: 1008",
		"external_requests: externalRequests",
	} {
		if !bytes.Contains(probe, []byte(required)) {
			t.Fatalf("browser external-origin fence omits %q", required)
		}
	}
	closed := bytes.Index(probe, []byte("await context.close()"))
	sealed := bytes.Index(probe, []byte("assert(unexpectedAPIRequests === 0 && externalRequests === 0)"))
	if closed < 0 || sealed < 0 || closed > sealed {
		t.Fatal("browser context is not closed before the final counter fence")
	}
}

func TestLiveUIRequestGateRequiresExactAuthorizedFilters(t *testing.T) {
	tests := []struct {
		name          string
		target        string
		authorization []string
		want          bool
	}{
		{
			name:          "exact inventory",
			target:        "/api/services?repository=" + liveRepository + "&page_size=50",
			authorization: []string{"Bearer exact-token"},
			want:          true,
		},
		{
			name:          "exact detail",
			target:        "/api/service?repository=" + liveRepository + "&service_key=service-a",
			authorization: []string{"Bearer exact-token"},
			want:          true,
		},
		{
			name:          "extra filter",
			target:        "/api/services?repository=" + liveRepository + "&page_size=50&status=current",
			authorization: []string{"Bearer exact-token"},
		},
		{
			name:          "duplicate authorization",
			target:        "/api/version",
			authorization: []string{"Bearer exact-token", "Bearer exact-token"},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			gate := &liveUIRequestGate{token: "exact-token", serviceKey: "service-a"}
			request := httptest.NewRequest(http.MethodGet, testCase.target, nil)
			request.Header["Authorization"] = testCase.authorization
			if got := gate.record(request); got != testCase.want {
				t.Fatalf("request admission = %t, want %t", got, testCase.want)
			}
			if !testCase.want && gate.snapshot().unexpected != 1 {
				t.Fatalf("unexpected request count = %+v", gate.snapshot())
			}
		})
	}
}
