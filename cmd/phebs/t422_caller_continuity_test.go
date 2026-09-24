package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestT422CallerContinuityClosedRequests(t *testing.T) {
	for _, test := range []struct {
		name, method, path, value string
		duplicate, want           bool
	}{
		{"complete", http.MethodGet, t421ExactFinalAuthorityPath, t422CallerContinuityValue, false, true},
		{"absent", http.MethodGet, t421ExactFinalAuthorityPath, "", false, false},
		{"wrong value", http.MethodGet, t421ExactFinalAuthorityPath, "manifest-v2", false, false},
		{"duplicate", http.MethodGet, t421ExactFinalAuthorityPath, t422CallerContinuityValue, true, false},
		{"method", http.MethodPost, t421ExactFinalAuthorityPath, t422CallerContinuityValue, false, false},
		{"tail", http.MethodGet, t421ExactTailReadinessPath, t422CallerContinuityValue, false, false},
		{"query", http.MethodGet, t421ExactFinalAuthorityPath + "?extra=1", t422CallerContinuityValue, false, false},
		{"empty query", http.MethodGet, t421ExactFinalAuthorityPath + "?", t422CallerContinuityValue, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.value != "" {
				request.Header.Set(t422CallerContinuityHeader, test.value)
			}
			if test.duplicate {
				request.Header.Add(t422CallerContinuityHeader, test.value)
			}
			if got := t422CallerContinuityRoute(request); got != test.want {
				t.Fatalf("route = %v", got)
			}
			if (&t421ExactReadAccountingState{}).callerContinuityRequest(request) {
				t.Fatal("ordinary/unselected request was admitted")
			}
		})
	}
}

func TestT422CallerContinuityFinalOmission(t *testing.T) {
	value := t421FinalAuthorityResponse{}
	before, err := t421FinalMarshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "caller_continuity_sha256") {
		t.Fatal("legacy F gained continuity evidence")
	}
	value.Authority.CallerContinuitySHA256 = "sha256:" + strings.Repeat("a", 64)
	selected, err := t421FinalMarshal(value)
	if err != nil {
		t.Fatal(err)
	}
	line := "    \"caller_continuity_sha256\": \"" + value.Authority.CallerContinuitySHA256 + "\",\n"
	if strings.Count(string(selected), line) != 1 || strings.Replace(string(selected), line, "", 1) != string(before) {
		t.Fatal("opt-in changed bytes beyond its one optional authority field")
	}
	var returned t421FinalAuthorityResponse
	if json.Unmarshal(selected, &returned) != nil || returned.Authority.CallerContinuitySHA256 != value.Authority.CallerContinuitySHA256 {
		t.Fatal("continuity field was not round-tripped")
	}
}
