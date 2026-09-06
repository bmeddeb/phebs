package t421

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// This is HTTP/control mechanics, not native Phebs or profile admission.
func TestExecutionEpochOneHealthOneShotAndFailure(t *testing.T) {
	for _, mode := range []string{"healthy", "unauthorized", "oversized", "truncated", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			parent, child, err := dispatchadmission.NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := child.Close(); err != nil {
					t.Error(err)
				}
			}()
			control, err := dispatchadmission.NewPhaseControl(t.Context(), parent, [32]byte{1}, dispatchadmission.PhaseControlConfig{
				OwnerControl: true, Phases: []uint32{2, 3, 4}, InitialPhase: 2, MaximumPhases: 3,
				MaximumWireBytes: 384, Timeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := control.Close(); err != nil {
					t.Error(err)
				}
			}()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.RequestURI() != "/api/health" ||
					r.Header.Get("Authorization") != "Bearer private-key" ||
					r.Header.Get(dispatchadmission.ProductionRequestHeader) != control.RequestToken() {
					t.Error("health request lost fixed route, authentication or private owner token")
				}
				switch mode {
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
				case "oversized":
					_, _ = fmt.Fprint(w, strings.Repeat("x", 4097))
				case "truncated":
					w.Header().Set("Content-Length", "100")
					_, _ = fmt.Fprint(w, "short")
				default:
					_, _ = fmt.Fprint(w, `{"status":"ok"}`)
				}
			}))
			defer server.Close()
			run := &ExecutionEpochOneRun{control: control, stop: make(chan struct{}), done: make(chan struct{}),
				epoch: ExecutionEpochConfig{Listen: strings.TrimPrefix(server.URL, "http://"), APIKey: "private-key"}}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			err = run.Health(ctx)
			if (err == nil) != (mode == "healthy") {
				t.Fatalf("health result = %v", err)
			}
			if mode == "healthy" {
				select {
				case <-run.stop:
					t.Fatal("healthy request stopped the native lifetime")
				default:
				}
			} else {
				select {
				case <-run.stop:
				default:
					t.Fatal("uncertain health did not stop the lifetime")
				}
				if run.err != ErrExecutionEpochOne {
					t.Fatal("health failure was not sticky")
				}
			}
			if run.Health(ctx) == nil {
				t.Fatal("one-shot health admitted a retry")
			}
			want := int32(1)
			if mode == "canceled" {
				want = 0
			}
			if requests.Load() != want {
				t.Fatalf("HTTP requests = %d, want %d", requests.Load(), want)
			}
		})
	}
}
