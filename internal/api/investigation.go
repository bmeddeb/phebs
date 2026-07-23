package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/store"
)

const (
	investigationPreviewSchemaVersion = "investigation-scope-preview-v1"
	maxInvestigationPreviewRepos      = 1_000
	maxInvestigationInputBytes        = 64 << 10
)

// InvestigationPlan is the guided-creation input shared by preview and
// submission. Repositories are explicit caller inputs; an unavailable entry
// is reported identically whether it is missing, deleting, or unauthorized.
type InvestigationPlan struct {
	Referent           string   `json:"referent" maxLength:"65536"`
	ClaimFamily        string   `json:"claim_family" maxLength:"65536"`
	Title              string   `json:"title" maxLength:"65536"`
	NormalizedQuestion string   `json:"normalized_question" maxLength:"65536"`
	DecisionSought     string   `json:"decision_sought" maxLength:"65536"`
	Repositories       []string `json:"repositories,omitempty" maxItems:"1000"`
	SnapshotPolicy     string   `json:"snapshot_policy" maxLength:"65536"`
	BuildConfiguration string   `json:"build_configuration" maxLength:"65536"`
	PackVersions       []string `json:"pack_versions" minItems:"1" maxItems:"64"`
	EnumerationMethod  string   `json:"enumeration_method" maxLength:"65536"`
}

type InvestigationScopeRepository struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type InvestigationPreflightBlocker struct {
	Code  string `json:"code"`
	Input string `json:"input,omitempty"`
}

type InvestigationWorkEstimate struct {
	RepositoryCount int    `json:"repository_count"`
	AnalysisUnits   int    `json:"analysis_units"`
	Unit            string `json:"unit"`
	HardLimit       int    `json:"hard_limit"`
}

// InvestigationScopePreview is safe to return to the current principal: it
// is built only from the already-filtered visible repository slice.
type InvestigationScopePreview struct {
	SchemaVersion      string                          `json:"schema_version"`
	Ready              bool                            `json:"ready"`
	PreviewDigest      string                          `json:"preview_digest,omitempty"`
	AuthorizationState string                          `json:"authorization_state"`
	Repositories       []InvestigationScopeRepository  `json:"repositories"`
	PackVersions       []string                        `json:"pack_versions"`
	Estimate           InvestigationWorkEstimate       `json:"estimate"`
	Blockers           []InvestigationPreflightBlocker `json:"blockers"`

	declaredUniverse        string
	requestedSnapshot       string
	inputManifestDigest     string
	resolvedInputIdentities []string
}

func validateInvestigationPlan(plan InvestigationPlan) error {
	for name, value := range map[string]string{
		"referent": plan.Referent, "claim family": plan.ClaimFamily,
		"title": plan.Title, "normalized question": plan.NormalizedQuestion,
		"decision sought": plan.DecisionSought, "snapshot policy": plan.SnapshotPolicy,
		"build configuration": plan.BuildConfiguration,
		"enumeration method":  plan.EnumerationMethod,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value ||
			!utf8.ValidString(value) || len(value) > maxInvestigationInputBytes {
			return fmt.Errorf("%s is required as bounded canonical UTF-8", name)
		}
	}
	if len(plan.PackVersions) == 0 || len(plan.PackVersions) > 64 {
		return errors.New("pack_versions must contain 1 through 64 values")
	}
	if len(plan.Repositories) > maxInvestigationPreviewRepos {
		return fmt.Errorf("repositories exceed the hard limit of %d", maxInvestigationPreviewRepos)
	}
	return nil
}

func canonicalInvestigationValues(name string, values []string) ([]string, error) {
	values = slices.Clone(values)
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value || !utf8.ValidString(value) ||
			len(value) > maxInvestigationInputBytes {
			return nil, fmt.Errorf("%s contains an invalid value", name)
		}
	}
	sort.Strings(values)
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return nil, fmt.Errorf("%s contains duplicate %q", name, values[i])
		}
	}
	return values, nil
}

func investigationJSONDigest(value any) (string, []byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), encoded, nil
}

// BuildInvestigationScopePreview performs authorization before aggregation:
// visible must already be the caller's filtered universe.
func BuildInvestigationScopePreview(visible []store.Repo, plan InvestigationPlan) (*InvestigationScopePreview, error) {
	if err := validateInvestigationPlan(plan); err != nil {
		return nil, err
	}
	var err error
	plan.Repositories, err = canonicalInvestigationValues("repositories", plan.Repositories)
	if err != nil {
		return nil, err
	}
	plan.PackVersions, err = canonicalInvestigationValues("pack_versions", plan.PackVersions)
	if err != nil {
		return nil, err
	}

	available := make(map[string]store.Repo, len(visible))
	for _, repo := range visible {
		if repo.Name == "" || repo.Deleting {
			continue
		}
		available[repo.Name] = repo
	}
	selected := plan.Repositories
	if len(selected) == 0 {
		selected = make([]string, 0, len(available))
		for name := range available {
			selected = append(selected, name)
		}
		sort.Strings(selected)
	}
	preview := &InvestigationScopePreview{
		SchemaVersion: investigationPreviewSchemaVersion,
		Repositories:  []InvestigationScopeRepository{},
		PackVersions:  plan.PackVersions,
		Blockers:      []InvestigationPreflightBlocker{},
		Estimate: InvestigationWorkEstimate{
			RepositoryCount: len(selected), AnalysisUnits: len(selected),
			Unit: "repository_snapshots", HardLimit: maxInvestigationPreviewRepos,
		},
	}
	if len(selected) == 0 {
		preview.Blockers = append(preview.Blockers, InvestigationPreflightBlocker{Code: "SCOPE_EMPTY"})
	}
	if len(selected) > maxInvestigationPreviewRepos {
		preview.Blockers = append(preview.Blockers,
			InvestigationPreflightBlocker{Code: "SCOPE_LIMIT_EXCEEDED"})
	}
	for _, name := range selected {
		repo, ok := available[name]
		if !ok {
			preview.Blockers = append(preview.Blockers,
				InvestigationPreflightBlocker{Code: "SCOPE_REPOSITORY_NOT_AVAILABLE", Input: name})
			continue
		}
		if repo.IndexedCommitHash == "" {
			preview.Blockers = append(preview.Blockers,
				InvestigationPreflightBlocker{Code: "SCOPE_REPOSITORY_NOT_INDEXED", Input: name})
			continue
		}
		preview.Repositories = append(preview.Repositories,
			InvestigationScopeRepository{Name: name, Commit: repo.IndexedCommitHash})
	}
	preview.Ready = len(preview.Blockers) == 0
	if !preview.Ready {
		preview.AuthorizationState = "blocked"
		return preview, nil
	}
	preview.AuthorizationState = "ready"
	preview.resolvedInputIdentities = make([]string, len(preview.Repositories))
	for i, repo := range preview.Repositories {
		preview.resolvedInputIdentities[i] = "repo:" + repo.Name + "@" + repo.Commit
	}
	declared := struct {
		Repositories []string `json:"repositories"`
	}{Repositories: selected}
	_, declaredBytes, err := investigationJSONDigest(declared)
	if err != nil {
		return nil, err
	}
	snapshot := struct {
		Repositories []InvestigationScopeRepository `json:"repositories"`
	}{Repositories: preview.Repositories}
	var snapshotBytes []byte
	preview.inputManifestDigest, snapshotBytes, err = investigationJSONDigest(snapshot)
	if err != nil {
		return nil, err
	}
	preview.declaredUniverse = string(declaredBytes)
	preview.requestedSnapshot = string(snapshotBytes)
	digestInput := struct {
		SchemaVersion      string                         `json:"schema_version"`
		Plan               InvestigationPlan              `json:"plan"`
		Repositories       []InvestigationScopeRepository `json:"repositories"`
		InputManifest      string                         `json:"input_manifest_digest"`
		AuthorizationState string                         `json:"authorization_state"`
	}{
		SchemaVersion: investigationPreviewSchemaVersion, Plan: plan,
		Repositories: preview.Repositories, InputManifest: preview.inputManifestDigest,
		AuthorizationState: preview.AuthorizationState,
	}
	preview.PreviewDigest, _, err = investigationJSONDigest(digestInput)
	if err != nil {
		return nil, err
	}
	return preview, nil
}

type investigationPreviewInput struct {
	Body InvestigationPlan
}

type investigationPreviewOutput struct {
	Body InvestigationScopePreview
}

type investigationCreateInput struct {
	Body struct {
		Plan           InvestigationPlan `json:"plan"`
		PreviewDigest  string            `json:"preview_digest"`
		IdempotencyKey string            `json:"idempotency_key" maxLength:"65536"`
	}
}

type investigationCreateOutput struct {
	Body store.GuidedInvestigationCreation
}

type investigationRunInput struct {
	RunID string `path:"run_id"`
}

type investigationRunStatus struct {
	Run      store.Run                          `json:"run"`
	Events   []store.RunEvent                   `json:"events"`
	Artifact *store.RunArtifact                 `json:"artifact,omitempty"`
	Coverage *store.InvestigationCoverageLedger `json:"coverage,omitempty"`
}

type investigationRunOutput struct {
	Body investigationRunStatus
}

type investigationCancelInput struct {
	RunID string `path:"run_id"`
	Body  struct {
		Reason string `json:"reason" maxLength:"65536"`
	}
}

type investigationCancelOutput struct {
	Body store.RunEvent
}

func investigationPrincipal(ctx context.Context, opts Options) (string, error) {
	if opts.Principal == nil {
		return "", huma.Error401Unauthorized("authenticated principal required")
	}
	principal := strings.TrimSpace(opts.Principal(ctx))
	if principal == "" {
		return "", huma.Error401Unauthorized("authenticated principal required")
	}
	return principal, nil
}

func investigationStoreError(action string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("investigation resource not found")
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrLeaseLost):
		return huma.Error409Conflict(err.Error())
	default:
		return huma.Error500InternalServerError(action, err)
	}
}

func registerInvestigations(api huma.API, opts Options) {
	if opts.Investigations == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID:  "preview-investigation",
		Method:       http.MethodPost,
		Path:         "/api/investigation_previews",
		Summary:      "Preview and authorize a frozen Investigation scope",
		MaxBodyBytes: 1 << 20,
	}, func(ctx context.Context, in *investigationPreviewInput) (*investigationPreviewOutput, error) {
		if _, err := investigationPrincipal(ctx, opts); err != nil {
			return nil, err
		}
		visible, err := visibleRepositories(ctx, opts)
		if err != nil {
			return nil, err
		}
		preview, err := BuildInvestigationScopePreview(visible, in.Body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &investigationPreviewOutput{Body: *preview}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-investigation",
		Method:        http.MethodPost,
		Path:          "/api/investigations",
		Summary:       "Freeze and queue an idempotent Investigation",
		MaxBodyBytes:  1 << 20,
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *investigationCreateInput) (*investigationCreateOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Body.IdempotencyKey) == "" {
			return nil, huma.Error400BadRequest("idempotency_key is required")
		}
		visible, err := visibleRepositories(ctx, opts)
		if err != nil {
			return nil, err
		}
		preview, err := BuildInvestigationScopePreview(visible, in.Body.Plan)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		if !preview.Ready {
			return nil, huma.Error422UnprocessableEntity("investigation preflight is blocked")
		}
		if in.Body.PreviewDigest == "" || in.Body.PreviewDigest != preview.PreviewDigest {
			return nil, huma.Error409Conflict("investigation preview changed; preview the scope again")
		}
		request := store.GuidedInvestigationRequest{
			IdempotencyKey: in.Body.IdempotencyKey, PreviewDigest: preview.PreviewDigest,
			Referent: in.Body.Plan.Referent, ClaimFamily: in.Body.Plan.ClaimFamily,
			Title: in.Body.Plan.Title, NormalizedQuestion: in.Body.Plan.NormalizedQuestion,
			DecisionSought:          in.Body.Plan.DecisionSought,
			DeclaredUniverse:        preview.declaredUniverse,
			SnapshotPolicy:          in.Body.Plan.SnapshotPolicy,
			BuildConfiguration:      in.Body.Plan.BuildConfiguration,
			PackSelection:           strings.Join(preview.PackVersions, ","),
			EnumerationMethod:       in.Body.Plan.EnumerationMethod,
			ResolvedInputIdentities: preview.resolvedInputIdentities,
			RequestedSnapshot:       preview.requestedSnapshot,
			InputManifestDigest:     preview.inputManifestDigest,
			RequestedPackVersions:   preview.PackVersions, Principal: principal,
		}
		created, err := opts.Investigations.CreateGuidedInvestigation(ctx, request)
		if err != nil {
			return nil, investigationStoreError("create investigation", err)
		}
		return &investigationCreateOutput{Body: *created}, nil
	})

	huma.Get(api, "/api/investigation_runs/{run_id}", func(ctx context.Context, in *investigationRunInput) (*investigationRunOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		run, err := opts.Investigations.GetRunAs(ctx, principal, in.RunID)
		if err != nil {
			return nil, investigationStoreError("read investigation run", err)
		}
		events, err := opts.Investigations.ListRunEventsAs(ctx, principal, in.RunID)
		if err != nil {
			return nil, investigationStoreError("read investigation run events", err)
		}
		artifact, err := opts.Investigations.GetRunArtifactForRunAs(ctx, principal, in.RunID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, investigationStoreError("read investigation run artifact", err)
		}
		var coverage *store.InvestigationCoverageLedger
		if artifact != nil {
			coverage, _, err = store.ParseInvestigationCoverageLedger(artifact.CoverageLedger)
			if err != nil {
				return nil, huma.Error500InternalServerError("stored investigation coverage is inconsistent", err)
			}
		}
		return &investigationRunOutput{Body: investigationRunStatus{
			Run: *run, Events: events, Artifact: artifact, Coverage: coverage,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:  "cancel-investigation-run",
		Method:       http.MethodPost,
		Path:         "/api/investigation_runs/{run_id}/cancel",
		Summary:      "Cancel an active Investigation Run",
		MaxBodyBytes: 64 << 10,
	}, func(ctx context.Context, in *investigationCancelInput) (*investigationCancelOutput, error) {
		principal, err := investigationPrincipal(ctx, opts)
		if err != nil {
			return nil, err
		}
		event, err := opts.Investigations.CancelInvestigationRunAs(ctx, principal, in.RunID, in.Body.Reason)
		if err != nil {
			return nil, investigationStoreError("cancel investigation run", err)
		}
		return &investigationCancelOutput{Body: *event}, nil
	})
}
