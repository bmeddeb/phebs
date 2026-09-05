package main

import (
	"net/http"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func t422ServerOwnerLimits() dispatchadmission.OwnerLimits {
	// Seven job loops; observation 7, relationship 3 and extraction 4
	// generation loops; watcher, resync, lifecycle, audit, auth expiration,
	// proof and evidence maintenance; plus startup. Disabled loops use no slot.
	// This source construction ceiling is not an accepted frozen flow budget.
	return dispatchadmission.OwnerLimits{Owners: 7 + 7 + 3 + 4 + 7 + 1, Requests: 1}
}

// Wrap outside auth.LoadAndSave so a request remains owned through auth touch,
// handler audit/usage, session persistence and response completion. Mechanical
// OpenRequests does not authorize any probe or bypass this normal auth stack.
func t422OwnerHTTPHandler(owners *dispatchadmission.Owners, next http.Handler, semantic ...*t422SemanticLaunch) http.Handler {
	var launch *t422SemanticLaunch
	if len(semantic) > 1 {
		panic("T42.2 request owner binding is invalid")
	}
	if len(semantic) == 1 {
		launch = semantic[0]
	}
	if owners == nil {
		if launch != nil {
			panic("T42.2 semantic request owners are unavailable")
		}
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(request.Header.Values(dispatchadmission.ProductionRequestHeader)) != 1 {
			http.Error(writer, "request admission unavailable", http.StatusServiceUnavailable)
			return
		}
		turn, err := dispatchadmission.EnterProductionRequest(request.Context(), owners, request.Header.Get(dispatchadmission.ProductionRequestHeader))
		if err != nil {
			http.Error(writer, "request admission unavailable", http.StatusServiceUnavailable)
			return
		}
		defer turn.End()
		if launch != nil {
			request, err = launch.admitRequest(request)
			if err != nil {
				http.Error(writer, "request admission unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}
