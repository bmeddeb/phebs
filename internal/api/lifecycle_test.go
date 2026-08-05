package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func TestLifecycleStatusAuthorizesBeforeSourceAndReturnsBoundedSnapshot(t *testing.T) {
	owners := lifecycle.ClosedOwners()
	owners = append(owners,
		lifecycle.CatalogGenerationOwner{}, lifecycle.GenerationOwner{}, lifecycle.JobOwnerImpl{},
	)
	monitor, err := lifecycle.NewStatusMonitor(true, owners)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	opts := api.Options{
		IsAdmin: func(context.Context) bool { return false },
		LifecycleStatusSource: func(context.Context) lifecycle.Status {
			calls++
			return monitor.Snapshot()
		},
	}
	handler := api.New(opts)
	request := httptest.NewRequest(http.MethodGet, api.LifecycleStatusPath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("denied lifecycle status = %d calls=%d", response.Code, calls)
	}

	opts.IsAdmin = func(context.Context) bool { return true }
	handler = api.New(opts)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("admin lifecycle status = %d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
	var status lifecycle.Status
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.ValidateStatus(status); err != nil {
		t.Fatal(err)
	}
	if len(response.Body.Bytes()) > api.LifecycleStatusResponseLimit {
		t.Fatalf("lifecycle response = %d bytes", response.Body.Len())
	}
}
