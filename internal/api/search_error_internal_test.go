package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestSearchHTTPErrorClassifiesOnlyColdWarmingAsRetryable(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		wantDetail string
	}{
		{
			name: "warming",
			err: errors.Join(
				search.ErrWholeGenerationWarming,
				context.DeadlineExceeded,
				errors.New("private shard path"),
			),
			status:     http.StatusConflict,
			wantDetail: SearchGenerationWarmingDetail,
		},
		{
			name: "bare deadline", err: context.DeadlineExceeded,
			status: http.StatusInternalServerError,
		},
		{
			name: "scope unavailable", err: servicequery.ErrUnavailable,
			status: http.StatusConflict,
		},
		{
			name: "hidden scope", err: search.ErrScopeNotFound,
			status: http.StatusNotFound, wantDetail: "search scope not found",
		},
		{
			name: "unrelated store miss", err: store.ErrNotFound,
			status: http.StatusInternalServerError,
		},
		{
			name: "other", err: errors.New("broken search generation"),
			status: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := searchHTTPError(test.err)
			var status huma.StatusError
			if !errors.As(err, &status) || status.GetStatus() != test.status {
				t.Fatalf("search error = %v, status=%v", err, status)
			}
			if test.wantDetail != "" && !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("search error detail = %v", err)
			}
			if test.name == "warming" && strings.Contains(err.Error(), "private shard path") {
				t.Fatalf("warming error exposed private cause: %v", err)
			}
		})
	}
}
