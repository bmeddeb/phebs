package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/danielgtaylor/huma/v2"
)

const TypedIndexPath = "/api/code-navigation-indexing"
const TypedIndexResourceProfile = "native-arm64-bounded-v1"

type typedOperatorStore interface {
	CheckTypedIndexPreview(context.Context, string, string, []byte) error
	ReadTypedIndexOperator(context.Context, string) (store.TypedIndexOperator, error)
	EnqueueTypedIndexExpected(context.Context, string, string, []byte) (store.TypedIndexStatus, error)
}

type TypedIndexProvider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}
type TypedIndexProviders struct {
	SchemaVersion string               `json:"schema"`
	Providers     []TypedIndexProvider `json:"providers" maxItems:"3"`
}
type TypedIndexView struct {
	SchemaVersion   string             `json:"schema"`
	Repository      string             `json:"repository"`
	Commit          string             `json:"commit"`
	Revision        string             `json:"revision"`
	Available       bool               `json:"available"`
	State           string             `json:"state" enum:"absent,current,stale,planning,indexing,validating,publishing,failed,canceled"`
	Provider        string             `json:"provider"`
	Profile         string             `json:"profile"`
	TargetProfile   string             `json:"target_profile"`
	ConfigProfile   string             `json:"config_profile"`
	ResourceProfile string             `json:"resource_profile"`
	RequestDigest   string             `json:"request_digest"`
	JobState        store.JobStatus    `json:"job_state"`
	CurrentCommit   string             `json:"current_commit"`
	Reason          typedindex.Refusal `json:"reason"`
	CheckedPurpose  typedindex.Purpose `json:"checked_purpose"`
}
type TypedIndexSelection struct {
	Repository       string             `json:"repository" maxLength:"512"`
	ExpectedRevision string             `json:"expected_revision" maxLength:"71"`
	Provider         string             `json:"provider" maxLength:"64"`
	Profile          string             `json:"profile" maxLength:"128"`
	Purpose          typedindex.Purpose `json:"purpose" enum:"publish,canary,dry-run"`
}
type TypedIndexPreview struct {
	SchemaVersion   string              `json:"schema"`
	Selection       TypedIndexSelection `json:"selection"`
	Commit          string              `json:"commit"`
	RequestDigest   string              `json:"request_digest"`
	IdempotencyKey  string              `json:"idempotency_key"`
	ResourceProfile string              `json:"resource_profile"`
}
type TypedIndexEnqueue struct {
	TypedIndexSelection
	RequestDigest  string `json:"request_digest" maxLength:"71"`
	IdempotencyKey string `json:"idempotency_key" maxLength:"64"`
}

func typedIndexAuthorize(ctx context.Context, opts Options, repository string) (*store.Repo, error) {
	if opts.IsAdmin == nil || !opts.IsAdmin(ctx) {
		return nil, huma.Error403Forbidden("administrator access required")
	}
	if reponame.Validate(repository) != nil || len(repository) > 512 || opts.Store == nil {
		return nil, huma.Error404NotFound("repository not found")
	}
	repo, err := opts.Store.GetRepo(ctx, repository)
	if err != nil || repo == nil || repo.Deleting {
		return nil, huma.Error404NotFound("repository not found")
	}
	if allow := repoFilter(ctx, opts); allow != nil && !allow(*repo) {
		return nil, huma.Error404NotFound("repository not found")
	}
	return repo, nil
}
func typedIndexRead(ctx context.Context, opts Options, repository string) (TypedIndexView, store.TypedIndexOperator, error) {
	repo, err := typedIndexAuthorize(ctx, opts, repository)
	if err != nil {
		return TypedIndexView{}, store.TypedIndexOperator{}, err
	}
	view := TypedIndexView{SchemaVersion: "phebs-typed-index-status-v1", Repository: repository, Commit: repo.IndexedCommitHash, State: "absent"}
	backend, ok := opts.Store.(typedOperatorStore)
	if !opts.TypedIndexAvailable || !ok {
		return view, store.TypedIndexOperator{}, nil
	}
	snapshot, err := backend.ReadTypedIndexOperator(ctx, repository)
	if errors.Is(err, typedindex.Unprepared) || errors.Is(err, store.ErrNotFound) {
		return view, store.TypedIndexOperator{}, nil
	}
	if err != nil {
		return view, snapshot, typedIndexHTTPError(err)
	}
	if snapshot.Source.Repository != repository || snapshot.Source.Commit != repo.IndexedCommitHash {
		return view, snapshot, huma.Error409Conflict("indexing authority changed; refresh")
	}
	confirmed, err := typedIndexAuthorize(ctx, opts, repository)
	if err != nil {
		return view, snapshot, err
	}
	if confirmed.IndexedCommitHash != repo.IndexedCommitHash || confirmed.EvidenceRevision != repo.EvidenceRevision {
		return view, snapshot, huma.Error409Conflict("indexing authority changed; refresh")
	}
	status := snapshot.Status
	view.Revision = snapshot.Revision
	view.Available = snapshot.Profile.Digest() != "" && !status.RestoreRequired
	view.Provider = snapshot.Profile.Provider()
	view.Profile = snapshot.Profile.Definition().Name
	if view.Available {
		view.TargetProfile = view.Profile
		view.ConfigProfile = snapshot.Profile.Definition().Config.Mode
		view.ResourceProfile = TypedIndexResourceProfile
	}
	view.RequestDigest = status.Desired
	view.JobState = snapshot.Coordinator
	view.Reason = status.Reason
	if status.Current != nil {
		view.CurrentCommit = status.Current.Binding.Source.Commit
	}
	if status.Check != nil {
		view.CheckedPurpose = status.Check.Purpose
	}
	switch {
	case status.Stale || status.RestoreRequired:
		view.State = "stale"
	case snapshot.Schedule != nil && snapshot.Schedule.Status == store.GenerationScheduleSuperseded:
		view.State = "canceled"
	case status.Canceled || status.Reason == typedindex.Canceled || snapshot.Coordinator == store.StatusCanceled:
		view.State = "canceled"
	case status.Reason != "" || snapshot.Coordinator == store.StatusFailed || snapshot.Schedule != nil && snapshot.Schedule.Failed > 0:
		view.State = "failed"
	case snapshot.Schedule != nil && snapshot.Schedule.Status == store.GenerationScheduleActive && (snapshot.Schedule.NextOffset < snapshot.Schedule.TotalItems || snapshot.Schedule.Pending > 0 || snapshot.Schedule.Running > 0) && status.Stage == "":
		view.State = "planning"
	case status.Stage == store.TypedPlanning || status.Stage == store.TypedPreflight:
		view.State = "planning"
	case status.Stage == store.TypedExecution:
		view.State = "indexing"
	case status.Stage == store.TypedValidation:
		view.State = "validating"
	case status.Stage == store.TypedPublication:
		view.State = "publishing"
	case status.Desired != "" && status.Check == nil && status.Stage == "" && (snapshot.Coordinator == store.StatusPending || snapshot.Coordinator == store.StatusClaimed || snapshot.Coordinator == store.StatusRunning):
		view.State = "planning"
	case status.Current != nil:
		view.State = "current"
	}
	return view, snapshot, nil
}
func typedIndexPreview(ctx context.Context, opts Options, selection TypedIndexSelection) (TypedIndexPreview, []byte, error) {
	view, snapshot, err := typedIndexRead(ctx, opts, selection.Repository)
	if err != nil {
		return TypedIndexPreview{}, nil, err
	}
	if !view.Available {
		return TypedIndexPreview{}, nil, huma.Error503ServiceUnavailable("managed indexing unavailable")
	}
	if selection.ExpectedRevision != view.Revision || selection.Provider != view.Provider || selection.Profile != view.Profile {
		return TypedIndexPreview{}, nil, huma.Error409Conflict("indexing selection changed; refresh")
	}
	request := typedindex.NewManagedRequest(snapshot.Source, snapshot.Profile, snapshot.ProfileEpoch, snapshot.UniverseDigest, selection.Purpose)
	raw, err := json.Marshal(request)
	if err != nil {
		return TypedIndexPreview{}, nil, typedIndexHTTPError(err)
	}
	admission, err := typedindex.Admit(ctx, typedindex.Authority{Enabled: true, Administrator: true, Source: snapshot.Source, Profile: typedindex.Epoch{Number: snapshot.ProfileEpoch, Digest: snapshot.Profile.Digest()}, UniverseDigest: snapshot.UniverseDigest}, snapshot.Profile, raw)
	if err != nil {
		return TypedIndexPreview{}, nil, typedIndexHTTPError(err)
	}
	return TypedIndexPreview{SchemaVersion: "phebs-typed-index-preview-v1", Selection: selection, Commit: view.Commit, RequestDigest: admission.Digest(), IdempotencyKey: request.IdempotencyKey, ResourceProfile: TypedIndexResourceProfile}, raw, nil
}
func typedIndexHTTPError(err error) error {
	switch {
	case errors.Is(err, typedindex.Stale), errors.Is(err, typedindex.Canceled):
		return huma.Error409Conflict("indexing authority changed; refresh")
	case errors.Is(err, typedindex.Disabled), errors.Is(err, typedindex.Unprepared):
		return huma.Error503ServiceUnavailable("managed indexing unavailable")
	case errors.Is(err, typedindex.Invalid), errors.Is(err, typedindex.Unsupported), errors.Is(err, typedindex.Capacity):
		return huma.Error400BadRequest("indexing request refused")
	default:
		return huma.Error503ServiceUnavailable("indexing status unavailable; retry")
	}
}
func registerTypedIndex(api huma.API, opts Options) {
	type providersOut struct{ Body TypedIndexProviders }
	huma.Get(api, TypedIndexPath+"/providers", func(ctx context.Context, _ *struct{}) (*providersOut, error) {
		if opts.IsAdmin == nil || !opts.IsAdmin(ctx) {
			return nil, huma.Error403Forbidden("administrator access required")
		}
		out := &providersOut{Body: TypedIndexProviders{SchemaVersion: "phebs-typed-index-providers-v1", Providers: []TypedIndexProvider{}}}
		names := []string{"Bazel", "Go module / workspace", "Existing artifact"}
		for n, id := range typedindex.ProviderOrder() {
			out.Body.Providers = append(out.Body.Providers, TypedIndexProvider{ID: id, Name: names[n], Available: opts.TypedIndexAvailable})
		}
		return out, nil
	})
	type statusIn struct {
		Repository string `query:"repository" required:"true" maxLength:"512"`
	}
	type statusOut struct{ Body TypedIndexView }
	huma.Get(api, TypedIndexPath+"/status", func(ctx context.Context, in *statusIn) (*statusOut, error) {
		view, _, err := typedIndexRead(ctx, opts, in.Repository)
		if err != nil {
			return nil, err
		}
		return &statusOut{Body: view}, nil
	})
	type planIn struct{ Body TypedIndexSelection }
	type planOut struct{ Body TypedIndexPreview }
	huma.Register(api, huma.Operation{OperationID: "plan-code-navigation-indexing", Method: http.MethodPost, Path: TypedIndexPath + "/plan", MaxBodyBytes: 4096}, func(ctx context.Context, in *planIn) (*planOut, error) {
		setAuditTarget(ctx, in.Body.Repository)
		preview, raw, err := typedIndexPreview(ctx, opts, in.Body)
		if err != nil {
			return nil, err
		}
		if err := opts.Store.(typedOperatorStore).CheckTypedIndexPreview(ctx, in.Body.Repository, in.Body.ExpectedRevision, raw); err != nil {
			if errors.Is(err, store.ErrTypedIndexRequestRecorded) {
				return nil, huma.Error422UnprocessableEntity("request_already_recorded")
			}
			return nil, typedIndexHTTPError(err)
		}
		return &planOut{Body: preview}, nil
	})
	type enqueueIn struct{ Body TypedIndexEnqueue }
	huma.Register(api, huma.Operation{OperationID: "enqueue-code-navigation-indexing", Method: http.MethodPost, Path: TypedIndexPath + "/enqueue", MaxBodyBytes: 4096}, func(ctx context.Context, in *enqueueIn) (*statusOut, error) {
		setAuditTarget(ctx, in.Body.Repository)
		preview, raw, err := typedIndexPreview(ctx, opts, in.Body.TypedIndexSelection)
		if err != nil {
			return nil, err
		}
		if in.Body.RequestDigest != preview.RequestDigest || in.Body.IdempotencyKey != preview.IdempotencyKey {
			return nil, huma.Error409Conflict("indexing request changed; refresh")
		}
		backend := opts.Store.(typedOperatorStore)
		if _, err = backend.EnqueueTypedIndexExpected(ctx, in.Body.Repository, in.Body.ExpectedRevision, raw); err != nil {
			return nil, typedIndexHTTPError(err)
		}
		view, _, err := typedIndexRead(ctx, opts, in.Body.Repository)
		if err != nil {
			return nil, err
		}
		return &statusOut{Body: view}, nil
	})
}
