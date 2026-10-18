package main

import (
	"net/http"

	documentation "github.com/bmeddeb/phebs"
	"github.com/bmeddeb/phebs/internal/auth"
)

func documentationAndUI(authService *auth.Service, docsHandler, uiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(documentation.Prefix, authService.LoadAndSave(authService.Require(docsHandler)))
	mux.Handle("/", uiHandler)
	return mux
}
