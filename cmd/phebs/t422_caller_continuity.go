package main

import (
	"net/http"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const (
	t422CallerContinuityHeader = "X-Phebs-T422-Caller-Continuity"
	t422CallerContinuityValue  = "manifest-v1"
)

type t422CallerContinuityKey struct{}

func t422CallerContinuityRoute(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	values := request.Header.Values(t422CallerContinuityHeader)
	return len(values) == 1 && values[0] == t422CallerContinuityValue &&
		request.Method == http.MethodGet && request.URL.Path == t421ExactFinalAuthorityPath &&
		request.URL.Path == request.URL.EscapedPath() && request.URL.RawQuery == "" &&
		!request.URL.ForceQuery && request.URL.Fragment == ""
}

func (state *t421ExactReadAccountingState) callerContinuityRequest(request *http.Request) bool {
	if state == nil || state.semantic == nil || !dispatchadmission.ProductionWorkSelected() || !t422CallerContinuityRoute(request) {
		return false
	}
	admitted, present := request.Context().Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	return present && state.semantic.matches(admitted)
}
