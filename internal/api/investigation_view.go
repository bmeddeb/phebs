package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/investigation"
	"github.com/bmeddeb/phebs/internal/store"
)

const investigationCoreViewsCapability = "investigation-core-views"

// InvestigationViewSource returns an already-authorized MCP-envelope
// projection for the current principal. The UI intentionally consumes the
// same semantic axes as the agent contract rather than inventing a parallel
// confidence or eligibility model.
type InvestigationViewSource interface {
	ListInvestigationViewsAs(context.Context, string) ([]InvestigationViewSummary, error)
	GetInvestigationViewAs(context.Context, string, string) (*InvestigationViewDocument, error)
}

type InvestigationViewSummary struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Outcome string `json:"outcome"`
}

type InvestigationViewDocument struct {
	InvestigationViewSummary
	Envelope json.RawMessage `json:"envelope"`
}

type fixtureInvestigationViews struct {
	documents []InvestigationViewDocument
}

type fixtureViewSpec struct {
	ID, Filename, Title, State string
}

var fixtureViewSpecs = []fixtureViewSpec{
	{"01", "01-complete-with-findings.json", "Complete with findings", "complete_with_findings"},
	{"02", "02-complete-zero-findings.json", "Complete with no supported facts", "complete_zero_findings"},
	{"03", "03-partial-failed-processing.json", "Partial and failed processing", "partial_failed_processing"},
	{"04", "04-unresolved-attribution.json", "Unresolved attribution", "unresolved_attribution"},
	{"05", "05-stale-pack-validation.json", "Stale pack validation", "stale_pack_validation"},
}

// NewInvestigationFixtureViews loads the five canonical synthetic T16.5
// fixtures. It is a development/demo adapter only: callers must opt in with
// an explicit directory, and the production server binds no default source.
func NewInvestigationFixtureViews(dir string) (InvestigationViewSource, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("investigation fixture directory is required")
	}
	source := &fixtureInvestigationViews{documents: make([]InvestigationViewDocument, 0, len(fixtureViewSpecs))}
	for _, spec := range fixtureViewSpecs {
		raw, err := os.ReadFile(filepath.Join(dir, spec.Filename))
		if err != nil {
			return nil, fmt.Errorf("read investigation fixture %s: %w", spec.ID, err)
		}
		var envelope investigation.Envelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("decode investigation fixture %s: %w", spec.ID, err)
		}
		if err := envelope.ValidateSemantics(); err != nil {
			return nil, fmt.Errorf("validate investigation fixture %s: %w", spec.ID, err)
		}
		if envelope.Scope == nil || envelope.Scope.InvestigationID == nil ||
			*envelope.Scope.InvestigationID == "" || envelope.Scope.NormalizedQuestion == "" {
			return nil, fmt.Errorf("investigation fixture %s is missing required envelope metadata", spec.ID)
		}
		source.documents = append(source.documents, InvestigationViewDocument{
			InvestigationViewSummary: InvestigationViewSummary{
				ID: spec.ID, Title: spec.Title, State: spec.State, Outcome: envelope.Outcome,
			},
			Envelope: slices.Clone(raw),
		})
	}
	return source, nil
}

func (s *fixtureInvestigationViews) ListInvestigationViewsAs(_ context.Context, principal string) ([]InvestigationViewSummary, error) {
	if strings.TrimSpace(principal) == "" {
		return nil, store.ErrNotFound
	}
	out := make([]InvestigationViewSummary, len(s.documents))
	for i := range s.documents {
		out[i] = s.documents[i].InvestigationViewSummary
	}
	return out, nil
}

func (s *fixtureInvestigationViews) GetInvestigationViewAs(_ context.Context, principal, id string) (*InvestigationViewDocument, error) {
	if strings.TrimSpace(principal) == "" {
		return nil, store.ErrNotFound
	}
	for i := range s.documents {
		if s.documents[i].ID == id {
			out := s.documents[i]
			out.Envelope = slices.Clone(out.Envelope)
			return &out, nil
		}
	}
	return nil, store.ErrNotFound
}

func apiCapabilities(opts Options) []string {
	capabilities := proofCapabilities(opts)
	if opts.InvestigationViews != nil {
		capabilities = append(capabilities, investigationCoreViewsCapability)
	}
	slices.Sort(capabilities)
	return capabilities
}

type investigationViewsOutput struct {
	Body []InvestigationViewSummary
}

type investigationViewInput struct {
	ID string `path:"id"`
}

type investigationViewOutput struct {
	Body InvestigationViewDocument
}

func registerInvestigationViews(api huma.API, opts Options) {
	if opts.InvestigationViews == nil {
		return
	}
	huma.Get(api, "/api/investigation_views", func(ctx context.Context, _ *struct{}) (*investigationViewsOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		views, err := opts.InvestigationViews.ListInvestigationViewsAs(ctx, principal)
		if err != nil {
			return nil, investigationStoreError("list investigation views", err)
		}
		return &investigationViewsOutput{Body: views}, nil
	})
	huma.Get(api, "/api/investigation_views/{id}", func(ctx context.Context, in *investigationViewInput) (*investigationViewOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		view, err := opts.InvestigationViews.GetInvestigationViewAs(ctx, principal, in.ID)
		if err != nil {
			return nil, investigationStoreError("read investigation view", err)
		}
		return &investigationViewOutput{Body: *view}, nil
	})
}
