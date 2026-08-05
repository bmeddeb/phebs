package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

const (
	LifecycleStatusPath          = "/api/lifecycle-status"
	LifecycleStatusResponseLimit = 16 << 10
)

func registerLifecycleStatus(api huma.API, opts Options) {
	type lifecycleStatusOut struct {
		Body lifecycle.Status
	}
	huma.Get(api, LifecycleStatusPath, func(
		ctx context.Context, _ *struct{},
	) (*lifecycleStatusOut, error) {
		if opts.IsAdmin == nil || !opts.IsAdmin(ctx) {
			return nil, huma.Error403Forbidden("administrator access required")
		}
		if opts.LifecycleStatusSource == nil {
			return nil, huma.Error503ServiceUnavailable("lifecycle status unavailable")
		}
		status := opts.LifecycleStatusSource(ctx)
		if err := lifecycle.ValidateStatus(status); err != nil {
			return nil, huma.Error500InternalServerError("invalid lifecycle status", err)
		}
		encoded, err := json.Marshal(status)
		if err != nil {
			return nil, huma.Error500InternalServerError("encode lifecycle status", err)
		}
		if len(encoded) > LifecycleStatusResponseLimit {
			return nil, huma.Error500InternalServerError(
				"lifecycle status exceeds response bound", errors.New("encoded response is too large"),
			)
		}
		return &lifecycleStatusOut{Body: status}, nil
	})
}
