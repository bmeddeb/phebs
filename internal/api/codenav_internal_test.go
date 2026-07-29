package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/codenav"
)

func TestCodeNavBindingChangeIsConflict(t *testing.T) {
	err := codeNavErr(codenav.ErrBindingChanged)
	var statusErr huma.StatusError
	if !errors.As(err, &statusErr) ||
		statusErr.GetStatus() != http.StatusConflict {
		t.Fatalf("binding-change API error = %v, want 409", err)
	}
}
