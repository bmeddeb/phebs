//go:build ui

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	documentation "github.com/bmeddeb/phebs"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

// An explicit ready-file path holds one authenticated browser fixture for two
// minutes at most; creating its .stop sibling joins the server and store early.
func TestDocumentationRenderedBrowserFixture(t *testing.T) {
	ready := os.Getenv("PHEBS_DOCS_BROWSER_READY")
	if ready == "" {
		t.Skip("opt-in rendered documentation browser fixture")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state, err := store.OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(context.Background()) })
	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: state, Config: config.Auth{
		CookieSecure:  &insecure,
		BootstrapUser: config.BootstrapUser{Email: "docs@example.com", Password: "documentation-password"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := documentation.NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	notFound := http.NotFoundHandler()
	server := httptest.NewServer(newHTTPHandler(authService, notFound, notFound, notFound, documentationAndUI(authService, docs, notFound), config.Server{}))
	defer server.Close()
	if err := os.WriteFile(ready, []byte(server.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("documentation browser fixture expired")
		case <-ticker.C:
			if _, err := os.Stat(ready + ".stop"); err == nil {
				return
			}
		}
	}
}
