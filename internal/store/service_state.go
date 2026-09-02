package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

// ServiceStateStore owns the current service-local lifecycle rows and their
// bounded repository summary. Catalog history is never scanned by this API.
type ServiceStateStore interface {
	ReconcileServiceStates(context.Context, servicecatalog.Publication) error
	ActivateService(context.Context, servicecatalog.ServiceActivation) error
	ServiceGenerationActivationNeeded(context.Context, string, string, string, string) (bool, error)
	ActivateServiceGeneration(context.Context, string, string, string, string) (int, error)
	GetServiceState(context.Context, string, string) (*servicecatalog.ServiceState, error)
	GetServiceStateSummary(context.Context, string) (*servicecatalog.RepositoryState, error)
	GetServiceStateRead(context.Context, string, string) (*ServiceStateRead, error)
	ConfirmServiceStateSnapshot(
		context.Context,
		string,
		servicecatalog.RepositoryState,
	) error
	ListServiceStates(
		context.Context,
		string,
		ServiceStateFilter,
		ServiceStatePosition,
		int,
	) (*ServiceStatePage, error)
}

// ServiceGenerationActivationNeeded proves at most one accepted non-current
// row under the current catalog/summary snapshot. Proposal/conflict rows do not
// force a repeated full filesystem validation on every startup or retry.
func (s *Surreal) ServiceGenerationActivationNeeded(
	ctx context.Context,
	repository, catalogGeneration, sourceGeneration, searchGeneration string,
) (bool, error) {
	if !validSHA256Digest(searchGeneration) {
		return false, fmt.Errorf("service generation activation check: invalid search generation")
	}
	publication, verified, _, err := s.serviceStateSnapshot(ctx, repository)
	if err != nil {
		return false, fmt.Errorf("service generation activation check: %w", err)
	}
	wantSource, err := servicecatalog.SourceGenerationDigest(*publication)
	if err != nil || publication.GenerationDigest != catalogGeneration ||
		wantSource != sourceGeneration {
		return false, fmt.Errorf("service generation activation check: generation changed: %w", ErrConflict)
	}
	results, err := surrealdb.Query[[]serviceStateRec](
		ctx, s.db,
		`SELECT * FROM service_state_current
			WHERE repository = $repository
				AND removed = false
				AND disposition = $accepted
				AND (status != $current OR (active_search_generation ?? '') != $search)
			LIMIT 1`,
		map[string]any{
			"repository": repository,
			"accepted":   servicecatalog.DispositionAccepted,
			"current":    servicecatalog.StatusCurrent,
			"search":     searchGeneration,
		},
	)
	if err != nil {
		return false, fmt.Errorf("service generation activation check: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > 1 {
		return false, fmt.Errorf("service generation activation check exceeded point bound: %w", ErrConflict)
	}
	if len(rows) == 0 {
		return false, nil
	}
	state, err := serviceStateFromRec(rows[0])
	if err != nil {
		return false, fmt.Errorf("service generation activation check: %w", err)
	}
	if _, err := verifyServiceStateProjection(*state, verified); err != nil {
		return false, fmt.Errorf("service generation activation check: %w", err)
	}
	if state.Status != servicecatalog.StatusCurrent &&
		state.Status != servicecatalog.StatusStale &&
		state.Status != servicecatalog.StatusUnavailable {
		return false, fmt.Errorf("service generation activation check: invalid accepted status: %w", servicecatalog.ErrInvalidServiceState)
	}
	return true, nil
}

var _ ServiceStateStore = (*Surreal)(nil)

const (
	MaxServiceStateReadPage = 101
	maxServiceStateScanPage = 500
)

// ServiceStateFilter is the closed inventory filter. Empty status or
// disposition means any value; removed rows require IncludeRemoved.
type ServiceStateFilter struct {
	Status         string
	Disposition    string
	IncludeRemoved bool
}

// ServiceStatePosition is the stable seek boundary carried by an authorized
// cursor. Incarnation makes removal/re-add of the boundary key invalidate the
// cursor even if its spelling returns; v3 additionally binds the immutable
// member range that supplied the boundary.
type ServiceStatePosition struct {
	ServiceKey        string
	Incarnation       uint64
	MemberRangeDigest string
}

// ServiceStateEntry pairs one strict lifecycle row with its current catalog
// projection. Projection is nil only for an omitted-key tombstone that is no
// longer present in the selected catalog.
type ServiceStateEntry struct {
	State      servicecatalog.ServiceState
	Projection *servicecatalog.ServiceProjection
}

// ServiceStateRead is one exact detail snapshot under a catalog/summary fence.
type ServiceStateRead struct {
	Publication servicecatalog.Publication
	Summary     servicecatalog.RepositoryState
	Entry       ServiceStateEntry
}

// AcceptedServiceStateSnapshot is the one-pass v2 batch boundary used by
// relationship builders. Publication, summary, and rows are final-fenced
// before return.
type AcceptedServiceStateSnapshot struct {
	Publication servicecatalog.Publication
	Summary     servicecatalog.RepositoryState
	States      []servicecatalog.ServiceState
}

// ServiceStatePage is one ordered bounded inventory snapshot. Continuation is
// the scan boundary when a sparse filter did not fill the requested page.
type ServiceStatePage struct {
	Publication  servicecatalog.Publication
	Summary      servicecatalog.RepositoryState
	Entries      []ServiceStateEntry
	Continuation *ServiceStatePosition
}

type serviceStateRec struct {
	Schema                   string           `json:"schema"`
	Repository               string           `json:"repository"`
	ServiceKey               string           `json:"service_key"`
	DisplayName              string           `json:"display_name"`
	Disposition              string           `json:"disposition"`
	Origin                   string           `json:"origin"`
	Reason                   string           `json:"reason"`
	Successors               []string         `json:"successors"`
	Incarnation              uint64           `json:"incarnation"`
	DesiredGeneration        string           `json:"desired_generation"`
	DesiredSourceGeneration  string           `json:"desired_source_generation"`
	DesiredCatalogGeneration string           `json:"desired_catalog_generation"`
	ActiveDesiredGeneration  string           `json:"active_desired_generation"`
	ActiveSourceGeneration   string           `json:"active_source_generation"`
	ActiveCatalogGeneration  string           `json:"active_catalog_generation"`
	ActiveSearchGeneration   string           `json:"active_search_generation"`
	Status                   string           `json:"status"`
	Removed                  bool             `json:"removed"`
	StateDigest              string           `json:"state_digest"`
	ControlRevision          uint64           `json:"control_revision"`
	ChangedAt                time.Time        `json:"changed_at"`
	VisibleFrom              uint64           `json:"visible_from"`
	SnapshotRevision         uint64           `json:"snapshot_revision"`
	SnapshotDigest           string           `json:"snapshot_digest"`
	RecID                    *models.RecordID `json:"id"`
}

type serviceRepositoryStateRec struct {
	Schema                 string           `json:"schema"`
	Repository             string           `json:"repository"`
	CatalogGeneration      string           `json:"catalog_generation"`
	CatalogControlRevision uint64           `json:"catalog_control_revision"`
	CatalogServiceCount    int              `json:"catalog_service_count"`
	LiveServiceCount       int              `json:"live_service_count"`
	CurrentCount           int              `json:"current_count"`
	StaleCount             int              `json:"stale_count"`
	UnavailableCount       int              `json:"unavailable_count"`
	ConflictCount          int              `json:"conflict_count"`
	TombstoneCount         int              `json:"tombstone_count"`
	SummaryDigest          string           `json:"summary_digest"`
	ControlRevision        uint64           `json:"control_revision"`
	UpdatedAt              time.Time        `json:"updated_at"`
	SnapshotRevision       uint64           `json:"snapshot_revision"`
	SnapshotDigest         string           `json:"snapshot_digest"`
	RecID                  *models.RecordID `json:"id"`
}

func serviceStateID(repository, serviceKey string) models.RecordID {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-service-state-id-v1\x00"))
	_, _ = hash.Write([]byte(repository))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(serviceKey))
	return models.NewRecordID("service_state_current", fmt.Sprintf("%x", hash.Sum(nil)))
}

func serviceRepositoryStateID(repository string) models.RecordID {
	return models.NewRecordID("service_state_repository", repository)
}

const reconcileServiceStatesSQL = `
BEGIN;
LET $catalog = (SELECT generation_digest, control_revision FROM $catalog_rid)[0];
LET $summary = (SELECT summary_digest, control_revision FROM $summary_rid)[0];
LET $catalog_ok = $catalog != NONE
	AND $catalog.generation_digest = $catalog_generation
	AND $catalog.control_revision = $catalog_control_revision;
LET $summary_ok = IF $expected_summary_revision = 0 THEN $summary = NONE
	ELSE $summary != NONE
		AND $summary.control_revision = $expected_summary_revision
		AND $summary.summary_digest = $expected_summary_digest END;
LET $saved = IF $catalog_ok AND $summary_ok THEN
	(UPSERT $summary_rid CONTENT $summary_content RETURN AFTER)
	ELSE [] END;
IF array::len($saved) = 1 {
	FOR $update IN $updates {
		LET $existing = (SELECT state_digest, control_revision FROM $update.rid)[0];
		LET $revision = IF $existing = NONE THEN 0 ELSE $existing.control_revision END;
		LET $digest = IF $existing = NONE THEN '' ELSE $existing.state_digest END;
		IF $revision != $update.expected_revision OR
			$digest != $update.expected_digest {
			THROW 'phebs-permanent: service state compare-and-swap conflict'
		};
		UPSERT $update.rid CONTENT $update.content RETURN NONE
	}
};
RETURN $saved;
COMMIT;`

// ReconcileServiceStates advances only service rows whose exact local desired
// projection changed. The repository summary is the serialization point and
// is committed atomically with every row update.
func (s *Surreal) ReconcileServiceStates(
	ctx context.Context,
	publication servicecatalog.Publication,
) error {
	if err := servicecatalog.ValidatePublication(publication, true); err != nil {
		return fmt.Errorf("reconcile service states: %w", err)
	}
	currentGeneration, currentRevision, err := s.serviceCatalogPointer(
		ctx, publication.Repository,
	)
	if err != nil {
		return fmt.Errorf("reconcile service states: current catalog pointer: %w", err)
	}
	if currentGeneration != publication.GenerationDigest ||
		currentRevision != publication.ControlRevision {
		return fmt.Errorf("reconcile service states: catalog is no longer current: %w", ErrConflict)
	}
	priorSummary, err := s.getRawServiceStateSummary(ctx, publication.Repository)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if priorSummary != nil &&
		priorSummary.CatalogGeneration == publication.GenerationDigest &&
		priorSummary.CatalogControlRevision == publication.ControlRevision {
		return nil
	}
	projections, err := servicecatalog.ProjectServices(publication)
	if err != nil {
		return fmt.Errorf("reconcile service states: project catalog: %w", err)
	}
	existing, err := s.serviceStatesForTransition(ctx, publication.Repository, projections)
	if err != nil {
		return err
	}
	if priorSummary == nil && len(existing) != 0 {
		return fmt.Errorf(
			"reconcile service states: rows exist without repository summary: %w",
			servicecatalog.ErrInvalidServiceState,
		)
	}
	now := storeTimestamp(time.Now())
	updates, nextByKey, tombstones, err := planServiceStateTransition(
		publication, projections, existing, priorSummary, now,
	)
	if err != nil {
		return fmt.Errorf("reconcile service states: %w", err)
	}
	nextSummary, err := buildServiceStateSummary(
		publication, projections, nextByKey, tombstones, priorSummary, now,
	)
	if err != nil {
		return fmt.Errorf("reconcile service states: %w", err)
	}
	expectedRevision, expectedDigest := uint64(0), ""
	if priorSummary != nil {
		expectedRevision = priorSummary.ControlRevision
		expectedDigest = priorSummary.SummaryDigest
	}
	return s.commitServiceStateTransition(
		ctx, publication, expectedRevision, expectedDigest, nextSummary, updates,
	)
}

func (s *Surreal) serviceCatalogPointer(
	ctx context.Context,
	repository string,
) (string, uint64, error) {
	results, err := surrealdb.Query[[]serviceCatalogCurrentRec](
		ctx, s.db,
		"SELECT generation_digest, control_revision FROM $rid",
		map[string]any{"rid": serviceCatalogCurrentID(repository)},
	)
	if err != nil {
		return "", 0, err
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return "", 0, fmt.Errorf("absent pointer: %w", ErrNotFound)
	}
	if len(rows) != 1 || !validSHA256Digest(rows[0].GenerationDigest) ||
		rows[0].ControlRevision == 0 {
		return "", 0, fmt.Errorf("malformed pointer: %w", ErrConflict)
	}
	return rows[0].GenerationDigest, rows[0].ControlRevision, nil
}

// ActivateService makes exactly one accepted desired generation current. A
// stale worker cannot activate a prior incarnation or desired row.
func (s *Surreal) ActivateService(
	ctx context.Context,
	request servicecatalog.ServiceActivation,
) error {
	if request.Repository == "" || request.ServiceKey == "" || request.Incarnation == 0 ||
		!validSHA256Digest(request.DesiredGeneration) ||
		request.StateControlRevision == 0 || request.RepositoryControlRevision == 0 {
		return fmt.Errorf("activate service: invalid compare-and-swap request")
	}
	publication, verified, summary, err := s.serviceStateSnapshot(ctx, request.Repository)
	if err != nil {
		return fmt.Errorf("activate service: %w", err)
	}
	state, err := s.getServiceStateAtSnapshot(
		ctx, request.Repository, request.ServiceKey, verified,
	)
	if err != nil {
		return fmt.Errorf("activate service: %w", err)
	}
	if state.Incarnation != request.Incarnation ||
		state.DesiredGeneration != request.DesiredGeneration {
		return fmt.Errorf("activate service: desired identity changed: %w", ErrConflict)
	}
	if state.Status == servicecatalog.StatusCurrent &&
		state.ActiveDesiredGeneration == state.DesiredGeneration {
		return nil
	}
	if state.Removed || state.Disposition != servicecatalog.DispositionAccepted ||
		state.ControlRevision != request.StateControlRevision ||
		summary.ControlRevision != request.RepositoryControlRevision {
		return fmt.Errorf("activate service: state is not activatable: %w", ErrConflict)
	}
	now := storeTimestamp(time.Now())
	next := *state
	next.Successors = slices.Clone(state.Successors)
	next.ActiveDesiredGeneration = state.DesiredGeneration
	next.ActiveSourceGeneration = state.DesiredSourceGeneration
	next.ActiveCatalogGeneration = state.DesiredCatalogGeneration
	next.ActiveSearchGeneration = ""
	next.Status = servicecatalog.StatusCurrent
	next.ControlRevision++
	next.ChangedAt = now
	if err := servicecatalog.SetServiceStateDigest(&next); err != nil {
		return fmt.Errorf("activate service: %w", err)
	}
	if err := servicecatalog.ValidateServiceState(next, true); err != nil {
		return fmt.Errorf("activate service: %w", err)
	}
	nextSummary := *summary
	switch state.Status {
	case servicecatalog.StatusStale:
		nextSummary.StaleCount--
	case servicecatalog.StatusUnavailable:
		nextSummary.UnavailableCount--
	default:
		return fmt.Errorf("activate service: unsupported prior status: %w", ErrConflict)
	}
	nextSummary.CurrentCount++
	nextSummary.ControlRevision++
	nextSummary.UpdatedAt = now
	if err := servicecatalog.SetRepositoryStateDigest(&nextSummary); err != nil {
		return fmt.Errorf("activate service: %w", err)
	}
	if err := servicecatalog.ValidateRepositoryState(nextSummary, true); err != nil {
		return fmt.Errorf("activate service: %w", err)
	}
	update := serviceStateUpdate{
		State: next, ExpectedRevision: state.ControlRevision,
		ExpectedDigest: state.StateDigest,
	}
	err = s.commitServiceStateTransition(
		ctx, *publication, summary.ControlRevision, summary.SummaryDigest,
		nextSummary, []serviceStateUpdate{update},
	)
	if err == nil {
		return nil
	}
	current, currentErr := s.GetServiceState(ctx, request.Repository, request.ServiceKey)
	if currentErr == nil && current.Incarnation == request.Incarnation &&
		current.DesiredGeneration == request.DesiredGeneration &&
		current.Status == servicecatalog.StatusCurrent {
		return nil
	}
	return err
}

// ActivateServiceGeneration atomically advances every accepted service whose
// desired identity belongs to one already-validated repository search
// generation. Catalog decoding, projection, and row loading are each bounded
// once per repository transition rather than once per service.
func (s *Surreal) ActivateServiceGeneration(
	ctx context.Context,
	repository, catalogGeneration, sourceGeneration, searchGeneration string,
) (int, error) {
	if !validSHA256Digest(catalogGeneration) ||
		!validSHA256Digest(sourceGeneration) ||
		!validSHA256Digest(searchGeneration) {
		return 0, fmt.Errorf("activate service generation: invalid generation identity")
	}
	publication, verified, summary, err := s.serviceStateSnapshot(ctx, repository)
	if err != nil {
		return 0, fmt.Errorf("activate service generation: %w", err)
	}
	if publication.GenerationDigest != catalogGeneration {
		return 0, fmt.Errorf("activate service generation: catalog changed: %w", ErrConflict)
	}
	wantSource, err := servicecatalog.SourceGenerationDigest(*publication)
	if err != nil {
		return 0, fmt.Errorf("activate service generation: source identity: %w", err)
	}
	if wantSource != sourceGeneration {
		return 0, fmt.Errorf("activate service generation: source changed: %w", ErrConflict)
	}
	projections, err := verified.ProjectServices()
	if err != nil {
		return 0, fmt.Errorf("activate service generation: project catalog: %w", err)
	}
	existing, err := s.serviceStatesForTransition(ctx, repository, projections)
	if err != nil {
		return 0, fmt.Errorf("activate service generation: %w", err)
	}
	now := storeTimestamp(time.Now())
	nextByKey := make(map[string]servicecatalog.ServiceState, len(projections))
	updates := make([]serviceStateUpdate, 0, len(projections))
	for _, projection := range projections {
		state, found := existing[projection.Service.Key]
		if !found {
			return 0, fmt.Errorf(
				"activate service generation: service state is absent: %w",
				servicecatalog.ErrInvalidServiceState,
			)
		}
		if _, err := verifyServiceStateProjection(state, verified); err != nil {
			return 0, fmt.Errorf("activate service generation: %w", err)
		}
		if projection.Removed ||
			projection.Service.Disposition != servicecatalog.DispositionAccepted {
			nextByKey[state.ServiceKey] = state
			continue
		}
		if state.Status == servicecatalog.StatusCurrent &&
			state.ActiveDesiredGeneration == state.DesiredGeneration &&
			state.ActiveSourceGeneration == state.DesiredSourceGeneration &&
			state.ActiveCatalogGeneration == state.DesiredCatalogGeneration &&
			state.ActiveSearchGeneration == searchGeneration {
			nextByKey[state.ServiceKey] = state
			continue
		}
		if state.Status != servicecatalog.StatusCurrent &&
			state.Status != servicecatalog.StatusStale &&
			state.Status != servicecatalog.StatusUnavailable {
			return 0, fmt.Errorf(
				"activate service generation: accepted service %q has status %q: %w",
				state.ServiceKey, state.Status, servicecatalog.ErrInvalidServiceState,
			)
		}
		next := state
		next.Successors = slices.Clone(state.Successors)
		next.ActiveDesiredGeneration = state.DesiredGeneration
		next.ActiveSourceGeneration = state.DesiredSourceGeneration
		next.ActiveCatalogGeneration = state.DesiredCatalogGeneration
		next.ActiveSearchGeneration = searchGeneration
		next.Status = servicecatalog.StatusCurrent
		next.ControlRevision++
		next.ChangedAt = now
		if err := servicecatalog.SetServiceStateDigest(&next); err != nil {
			return 0, fmt.Errorf("activate service generation: %w", err)
		}
		if err := servicecatalog.ValidateServiceState(next, true); err != nil {
			return 0, fmt.Errorf("activate service generation: %w", err)
		}
		nextByKey[next.ServiceKey] = next
		updates = append(updates, serviceStateUpdate{
			State: next, ExpectedRevision: state.ControlRevision,
			ExpectedDigest: state.StateDigest,
		})
	}
	if len(updates) == 0 {
		return 0, nil
	}
	nextSummary, err := buildServiceStateSummary(
		*publication, projections, nextByKey, summary.TombstoneCount, summary, now,
	)
	if err != nil {
		return 0, fmt.Errorf("activate service generation: %w", err)
	}
	if err := s.commitServiceStateTransition(
		ctx, *publication, summary.ControlRevision, summary.SummaryDigest,
		nextSummary, updates,
	); err != nil {
		return 0, fmt.Errorf("activate service generation: %w", err)
	}
	return len(updates), nil
}

// GetServiceStateSummary strict-opens the point summary against the current
// catalog pointer without reading any service or historical generation rows.
func (s *Surreal) GetServiceStateSummary(
	ctx context.Context,
	repository string,
) (*servicecatalog.RepositoryState, error) {
	generation, catalogRevision, err := s.serviceCatalogPointer(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("get service state summary: catalog pointer: %w", err)
	}
	summary, err := s.getRawServiceStateSummary(ctx, repository)
	if err != nil {
		return nil, err
	}
	if summary.CatalogGeneration != generation ||
		summary.CatalogControlRevision != catalogRevision {
		return nil, fmt.Errorf("get service state summary: catalog transition is unreconciled: %w", ErrConflict)
	}
	return summary, nil
}

// ConfirmServiceStateSnapshot performs the final response fence with only the
// current catalog-pointer and summary points. The caller already strict-opened
// the catalog while building its response, so this avoids a second 5-MiB
// decode without weakening the generation/revision/digest comparison.
func (s *Surreal) ConfirmServiceStateSnapshot(
	ctx context.Context,
	repository string,
	expected servicecatalog.RepositoryState,
) error {
	generation, catalogRevision, err := s.serviceCatalogPointer(ctx, repository)
	if err != nil {
		return fmt.Errorf("confirm service state snapshot: catalog pointer: %w", err)
	}
	current, err := s.getRawServiceStateSummary(ctx, repository)
	if err != nil {
		return fmt.Errorf("confirm service state snapshot: %w", err)
	}
	if generation != expected.CatalogGeneration ||
		catalogRevision != expected.CatalogControlRevision ||
		current.CatalogGeneration != expected.CatalogGeneration ||
		current.CatalogControlRevision != expected.CatalogControlRevision ||
		current.ControlRevision != expected.ControlRevision ||
		current.SummaryDigest != expected.SummaryDigest {
		return fmt.Errorf("confirm service state snapshot: state changed: %w", ErrConflict)
	}
	return nil
}

// serviceStateSnapshot binds one strict current catalog read to its one point
// summary. Callers may reuse both values throughout a CAS attempt instead of
// reopening and reprojecting the catalog through nested public reads.
func (s *Surreal) serviceStateSnapshot(
	ctx context.Context,
	repository string,
) (
	*servicecatalog.Publication,
	servicecatalog.VerifiedPublication,
	*servicecatalog.RepositoryState,
	error,
) {
	opened, err := s.getVerifiedServiceCatalog(ctx, repository)
	if err != nil {
		return nil, servicecatalog.VerifiedPublication{}, nil, err
	}
	summary, err := s.getRawServiceStateSummary(ctx, repository)
	if err != nil {
		return nil, servicecatalog.VerifiedPublication{}, nil, err
	}
	if summary.CatalogGeneration != opened.Publication.GenerationDigest ||
		summary.CatalogControlRevision != opened.Publication.ControlRevision {
		return nil, servicecatalog.VerifiedPublication{}, nil, fmt.Errorf(
			"catalog transition is unreconciled: %w",
			ErrConflict,
		)
	}
	return opened.Publication, opened.Verified, summary, nil
}

// GetServiceState strict-opens one point row and proves its current desired
// projection when the key remains present in the current catalog.
func (s *Surreal) GetServiceState(
	ctx context.Context,
	repository, serviceKey string,
) (*servicecatalog.ServiceState, error) {
	read, err := s.GetServiceStateRead(ctx, repository, serviceKey)
	if err != nil {
		return nil, err
	}
	state := read.Entry.State
	state.Successors = slices.Clone(state.Successors)
	return &state, nil
}

// GetServiceStateRead strict-opens one point row and retains the exact catalog
// projection and repository summary used to prove it.
func (s *Surreal) GetServiceStateRead(
	ctx context.Context,
	repository, serviceKey string,
) (*ServiceStateRead, error) {
	if err := validateCandidateRepository(repository); err != nil {
		return nil, fmt.Errorf("get service state: repository: %w", err)
	}
	publication, verified, summary, err := s.serviceStateSnapshot(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("get service state: %w", err)
	}
	entry, err := s.getServiceStateEntryAtSnapshot(ctx, repository, serviceKey, verified)
	if err != nil {
		return nil, err
	}
	return &ServiceStateRead{
		Publication: cloneServiceStatePublication(*publication),
		Summary:     *summary,
		Entry:       *entry,
	}, nil
}

func (s *Surreal) GetAcceptedServiceStateSnapshot(
	ctx context.Context,
	repository string,
	limit int,
) (*AcceptedServiceStateSnapshot, error) {
	if validateCandidateRepository(repository) != nil ||
		limit < 1 || limit > servicecatalog.MaxServices {
		return nil, fmt.Errorf("get accepted service states: invalid request")
	}
	publication, verified, summary, err := s.serviceStateSnapshot(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("get accepted service states: %w", err)
	}
	results, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_current
	WHERE repository = $repository AND removed = false AND disposition = $accepted
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": repository, "accepted": servicecatalog.DispositionAccepted,
		"limit": limit + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("get accepted service states: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > limit {
		return nil, fmt.Errorf("get accepted service states: service limit exceeded")
	}
	states := make([]servicecatalog.ServiceState, 0, len(rows))
	prior := ""
	for _, row := range rows {
		state, stateErr := serviceStateFromRec(row)
		if stateErr != nil || state.Repository != repository ||
			state.ServiceKey <= prior || state.Removed ||
			state.Disposition != servicecatalog.DispositionAccepted {
			return nil, fmt.Errorf(
				"get accepted service states: invalid row: %w",
				servicecatalog.ErrInvalidServiceState,
			)
		}
		if _, projectionErr := verifyServiceStateProjection(*state, verified); projectionErr != nil {
			return nil, fmt.Errorf("get accepted service states: %w", projectionErr)
		}
		prior = state.ServiceKey
		cloned := *state
		cloned.Successors = slices.Clone(state.Successors)
		states = append(states, cloned)
	}
	if err := s.ConfirmServiceStateSnapshot(ctx, repository, *summary); err != nil {
		return nil, fmt.Errorf("get accepted service states: final fence: %w", err)
	}
	return &AcceptedServiceStateSnapshot{
		Publication: cloneServiceStatePublication(*publication),
		Summary:     *summary, States: states,
	}, nil
}

func (s *Surreal) getServiceStateAtSnapshot(
	ctx context.Context,
	repository, serviceKey string,
	verified servicecatalog.VerifiedPublication,
) (*servicecatalog.ServiceState, error) {
	entry, err := s.getServiceStateEntryAtSnapshot(
		ctx, repository, serviceKey, verified,
	)
	if err != nil {
		return nil, err
	}
	state := entry.State
	state.Successors = slices.Clone(state.Successors)
	return &state, nil
}

func (s *Surreal) getServiceStateEntryAtSnapshot(
	ctx context.Context,
	repository, serviceKey string,
	verified servicecatalog.VerifiedPublication,
) (*ServiceStateEntry, error) {
	results, err := surrealdb.Query[[]serviceStateRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceStateID(repository, serviceKey)},
	)
	if err != nil {
		return nil, fmt.Errorf("get service state: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("service %q in %q: %w", serviceKey, repository, ErrNotFound)
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("get service state: duplicate point row: %w", ErrConflict)
	}
	state, err := serviceStateFromRec(rows[0])
	if err != nil {
		return nil, fmt.Errorf("get service state: %w", err)
	}
	projection, err := verifyServiceStateProjection(*state, verified)
	if err != nil {
		return nil, fmt.Errorf("get service state: %w", err)
	}
	return &ServiceStateEntry{State: *state, Projection: projection}, nil
}

// ListServiceStates returns one bounded, service-key-ordered page under one
// verified publication. It scans at most maxServiceStateScanPage rows through
// the existing repository/key index, applies filters in Go, and independently
// digest- and projection-checks every scanned row before it escapes.
func (s *Surreal) ListServiceStates(
	ctx context.Context,
	repository string,
	filter ServiceStateFilter,
	after ServiceStatePosition,
	limit int,
) (*ServiceStatePage, error) {
	if err := validateCandidateRepository(repository); err != nil {
		return nil, fmt.Errorf("list service states: repository: %w", err)
	}
	if err := validateServiceStateFilter(filter); err != nil {
		return nil, fmt.Errorf("list service states: %w", err)
	}
	if limit < 1 || limit > MaxServiceStateReadPage {
		return nil, fmt.Errorf("list service states: invalid page limit")
	}
	if (after.ServiceKey == "") != (after.Incarnation == 0) {
		return nil, fmt.Errorf("list service states: invalid seek position")
	}
	publication, verified, summary, err := s.serviceStateSnapshot(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("list service states: %w", err)
	}
	if after.ServiceKey != "" {
		anchor, anchorErr := s.getServiceStateEntryAtSnapshot(
			ctx, repository, after.ServiceKey, verified,
		)
		if anchorErr != nil || anchor.State.Incarnation != after.Incarnation {
			return nil, fmt.Errorf("list service states: seek position changed: %w", ErrConflict)
		}
	}
	results, err := surrealdb.Query[[]serviceStateRec](
		ctx, s.db,
		`SELECT * FROM service_state_current
			WHERE repository = $repository
				AND service_key > $after
			ORDER BY service_key ASC LIMIT $limit`,
		map[string]any{
			"repository": repository, "after": after.ServiceKey,
			"limit": maxServiceStateScanPage + 1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list service states: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > maxServiceStateScanPage+1 {
		return nil, fmt.Errorf("list service states: store exceeded scan limit: %w", ErrConflict)
	}
	hasMoreRows := len(rows) > maxServiceStateScanPage
	if hasMoreRows {
		rows = rows[:maxServiceStateScanPage]
	}
	entries := make([]ServiceStateEntry, 0, min(limit, len(rows)))
	priorKey := after.ServiceKey
	var continuation *ServiceStatePosition
	for index, row := range rows {
		state, stateErr := serviceStateFromRec(row)
		if stateErr != nil {
			return nil, fmt.Errorf("list service states: %w", stateErr)
		}
		if state.Repository != repository || state.ServiceKey <= priorKey {
			return nil, fmt.Errorf("list service states: inconsistent row: %w", servicecatalog.ErrInvalidServiceState)
		}
		projection, projectionErr := verifyServiceStateProjection(*state, verified)
		if projectionErr != nil {
			return nil, fmt.Errorf("list service states: %w", projectionErr)
		}
		priorKey = state.ServiceKey
		matches := (filter.Status == "" || state.Status == filter.Status) &&
			(filter.Disposition == "" || state.Disposition == filter.Disposition) &&
			(filter.IncludeRemoved || !state.Removed)
		if !matches {
			continue
		}
		entries = append(entries, ServiceStateEntry{
			State: *state, Projection: projection,
		})
		if len(entries) == limit {
			if index < len(rows)-1 || hasMoreRows {
				continuation = &ServiceStatePosition{
					ServiceKey: state.ServiceKey, Incarnation: state.Incarnation,
				}
			}
			break
		}
	}
	if len(entries) < limit && hasMoreRows && len(rows) != 0 {
		last, stateErr := serviceStateFromRec(rows[len(rows)-1])
		if stateErr != nil {
			return nil, fmt.Errorf("list service states: %w", stateErr)
		}
		continuation = &ServiceStatePosition{
			ServiceKey: last.ServiceKey, Incarnation: last.Incarnation,
		}
	}
	return &ServiceStatePage{
		Publication:  cloneServiceStatePublication(*publication),
		Summary:      *summary,
		Entries:      entries,
		Continuation: continuation,
	}, nil
}

func verifyServiceStateProjection(
	state servicecatalog.ServiceState,
	verified servicecatalog.VerifiedPublication,
) (*servicecatalog.ServiceProjection, error) {
	projection, found, err := verified.ProjectService(state.ServiceKey)
	if err != nil {
		return nil, fmt.Errorf("project catalog: %w", err)
	}
	if !found {
		if state.Removed {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"live key is absent from catalog: %w",
			servicecatalog.ErrInvalidServiceState,
		)
	}
	desiredGeneration, err := servicecatalog.ServiceDesiredGeneration(
		projection, state.Incarnation,
	)
	if err != nil {
		return nil, fmt.Errorf("desired generation: %w", err)
	}
	if state.DesiredGeneration != desiredGeneration ||
		state.DesiredSourceGeneration != projection.SourceGeneration ||
		state.Removed != projection.Removed {
		return nil, fmt.Errorf(
			"desired projection disagrees with catalog: %w",
			servicecatalog.ErrInvalidServiceState,
		)
	}
	return &projection, nil
}

func validateServiceStateFilter(filter ServiceStateFilter) error {
	switch filter.Status {
	case "", servicecatalog.StatusCurrent, servicecatalog.StatusStale,
		servicecatalog.StatusUnavailable, servicecatalog.StatusConflict,
		servicecatalog.StatusRemoved:
	default:
		return fmt.Errorf("invalid status filter")
	}
	switch filter.Disposition {
	case "", servicecatalog.DispositionAccepted, servicecatalog.DispositionProposal,
		servicecatalog.DispositionConflict, servicecatalog.DispositionRejected:
	default:
		return fmt.Errorf("invalid disposition filter")
	}
	if filter.Status == servicecatalog.StatusRemoved && !filter.IncludeRemoved {
		return fmt.Errorf("removed status requires removed rows")
	}
	return nil
}

func cloneServiceStatePublication(publication servicecatalog.Publication) servicecatalog.Publication {
	publication.Canonical = slices.Clone(publication.Canonical)
	if publication.Override != nil {
		override := *publication.Override
		publication.Override = &override
	}
	return publication
}

type serviceStateUpdate struct {
	State            servicecatalog.ServiceState
	ExpectedRevision uint64
	ExpectedDigest   string
}

func (s *Surreal) commitServiceStateTransition(
	ctx context.Context,
	publication servicecatalog.Publication,
	expectedSummaryRevision uint64,
	expectedSummaryDigest string,
	summary servicecatalog.RepositoryState,
	updates []serviceStateUpdate,
) error {
	encodedUpdates := make([]map[string]any, 0, len(updates))
	for _, update := range updates {
		encodedUpdates = append(encodedUpdates, map[string]any{
			"rid":               serviceStateID(update.State.Repository, update.State.ServiceKey),
			"expected_revision": update.ExpectedRevision,
			"expected_digest":   update.ExpectedDigest,
			"content":           serviceStateContent(update.State),
		})
	}
	vars := map[string]any{
		"catalog_rid":               serviceCatalogCurrentID(publication.Repository),
		"summary_rid":               serviceRepositoryStateID(publication.Repository),
		"catalog_generation":        publication.GenerationDigest,
		"catalog_control_revision":  publication.ControlRevision,
		"expected_summary_revision": expectedSummaryRevision,
		"expected_summary_digest":   expectedSummaryDigest,
		"summary_content":           serviceRepositoryStateContent(summary),
		"updates":                   encodedUpdates,
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]serviceRepositoryStateRec](
			ctx, s.db, reconcileServiceStatesSQL, vars,
		)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return fmt.Errorf("commit service state transition: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return fmt.Errorf("commit service state transition: stale catalog or concurrent writer: %w", ErrConflict)
		}
		return nil
	}
}

func (s *Surreal) getRawServiceStateSummary(
	ctx context.Context,
	repository string,
) (*servicecatalog.RepositoryState, error) {
	if err := validateCandidateRepository(repository); err != nil {
		return nil, fmt.Errorf("get service state summary: repository: %w", err)
	}
	results, err := surrealdb.Query[[]serviceRepositoryStateRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceRepositoryStateID(repository)},
	)
	if err != nil {
		return nil, fmt.Errorf("get service state summary: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, fmt.Errorf("service state summary for %q: %w", repository, ErrNotFound)
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("get service state summary: duplicate point row: %w", ErrConflict)
	}
	summary := repositoryStateFromRec(rows[0])
	if err := servicecatalog.ValidateRepositoryState(summary, true); err != nil {
		return nil, fmt.Errorf("get service state summary: %w", err)
	}
	return &summary, nil
}

func (s *Surreal) serviceStatesForTransition(
	ctx context.Context,
	repository string,
	projections []servicecatalog.ServiceProjection,
) (map[string]servicecatalog.ServiceState, error) {
	results, err := surrealdb.Query[[]serviceStateRec](
		ctx, s.db,
		"SELECT * FROM service_state_current WHERE repository = $repository AND removed = false LIMIT $limit",
		map[string]any{"repository": repository, "limit": servicecatalog.MaxServices + 1},
	)
	if err != nil {
		return nil, fmt.Errorf("reconcile service states: read live rows: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) > servicecatalog.MaxServices {
		return nil, fmt.Errorf("reconcile service states: live-row bound exceeded: %w", servicecatalog.ErrInvalidServiceState)
	}
	states := make(map[string]servicecatalog.ServiceState, len(rows)+len(projections))
	for _, row := range rows {
		state, stateErr := serviceStateFromRec(row)
		if stateErr != nil {
			return nil, fmt.Errorf("reconcile service states: live row: %w", stateErr)
		}
		if state.Repository != repository || state.Removed {
			return nil, fmt.Errorf("reconcile service states: invalid live row: %w", servicecatalog.ErrInvalidServiceState)
		}
		if _, duplicate := states[state.ServiceKey]; duplicate {
			return nil, fmt.Errorf("reconcile service states: duplicate live key: %w", servicecatalog.ErrInvalidServiceState)
		}
		states[state.ServiceKey] = *state
	}
	rids := make([]models.RecordID, 0, len(projections))
	for _, projection := range projections {
		if _, exists := states[projection.Service.Key]; !exists {
			rids = append(rids, serviceStateID(repository, projection.Service.Key))
		}
	}
	if len(rids) == 0 {
		return states, nil
	}
	pointResults, err := surrealdb.Query[[]serviceStateRec](
		ctx, s.db, "SELECT * FROM $rids", map[string]any{"rids": rids},
	)
	if err != nil {
		return nil, fmt.Errorf("reconcile service states: read desired tombstones: %w", err)
	}
	for _, row := range firstDomainRows(pointResults) {
		state, stateErr := serviceStateFromRec(row)
		if stateErr != nil {
			return nil, fmt.Errorf("reconcile service states: desired point row: %w", stateErr)
		}
		if state.Repository != repository {
			return nil, fmt.Errorf("reconcile service states: cross-repository point row: %w", servicecatalog.ErrInvalidServiceState)
		}
		if _, duplicate := states[state.ServiceKey]; duplicate {
			return nil, fmt.Errorf("reconcile service states: duplicate point key: %w", servicecatalog.ErrInvalidServiceState)
		}
		states[state.ServiceKey] = *state
	}
	return states, nil
}

func planServiceStateTransition(
	publication servicecatalog.Publication,
	projections []servicecatalog.ServiceProjection,
	existing map[string]servicecatalog.ServiceState,
	priorSummary *servicecatalog.RepositoryState,
	now time.Time,
) ([]serviceStateUpdate, map[string]servicecatalog.ServiceState, int, error) {
	updates := make([]serviceStateUpdate, 0, len(projections)+len(existing))
	nextByKey := make(map[string]servicecatalog.ServiceState, len(projections))
	tombstones := 0
	if priorSummary != nil {
		tombstones = priorSummary.TombstoneCount
	}
	seen := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		key := projection.Service.Key
		seen[key] = struct{}{}
		prior, exists := existing[key]
		next, changed, transitionErr := projectServiceState(projection, prior, exists, now)
		if transitionErr != nil {
			return nil, nil, 0, transitionErr
		}
		tombstones += tombstoneDelta(prior, exists, next)
		if tombstones < 0 {
			return nil, nil, 0, servicecatalog.ErrInvalidServiceState
		}
		nextByKey[key] = next
		if changed {
			update := serviceStateUpdate{State: next}
			if exists {
				update.ExpectedRevision = prior.ControlRevision
				update.ExpectedDigest = prior.StateDigest
			}
			updates = append(updates, update)
		}
	}
	for key, prior := range existing {
		if _, present := seen[key]; present || prior.Removed {
			continue
		}
		next, removalErr := implicitRemovedServiceState(prior, now)
		if removalErr != nil {
			return nil, nil, 0, removalErr
		}
		tombstones++
		updates = append(updates, serviceStateUpdate{
			State: next, ExpectedRevision: prior.ControlRevision,
			ExpectedDigest: prior.StateDigest,
		})
	}
	slices.SortFunc(updates, func(left, right serviceStateUpdate) int {
		switch {
		case left.State.ServiceKey < right.State.ServiceKey:
			return -1
		case left.State.ServiceKey > right.State.ServiceKey:
			return 1
		default:
			return 0
		}
	})
	return updates, nextByKey, tombstones, nil
}

func projectServiceState(
	projection servicecatalog.ServiceProjection,
	prior servicecatalog.ServiceState,
	exists bool,
	now time.Time,
) (servicecatalog.ServiceState, bool, error) {
	incarnation := uint64(1)
	if exists {
		incarnation = prior.Incarnation
		if prior.Removed && !projection.Removed {
			incarnation++
			if incarnation == 0 {
				return servicecatalog.ServiceState{}, false, servicecatalog.ErrInvalidServiceState
			}
		}
	}
	desiredGeneration, err := servicecatalog.ServiceDesiredGeneration(
		projection, incarnation,
	)
	if err != nil {
		return servicecatalog.ServiceState{}, false, err
	}
	if exists && prior.DesiredGeneration == desiredGeneration &&
		prior.Removed == projection.Removed {
		return prior, false, nil
	}
	next := servicecatalog.ServiceState{
		Schema:     servicecatalog.ServiceStateSchema,
		Repository: projection.Repository, ServiceKey: projection.Service.Key,
		DisplayName: projection.Service.DisplayName,
		Disposition: projection.Service.Disposition, Origin: projection.Service.Origin,
		Reason:                   projection.Service.Reason,
		Successors:               append([]string{}, projection.Service.Successors...),
		Incarnation:              incarnation,
		DesiredGeneration:        desiredGeneration,
		DesiredSourceGeneration:  projection.SourceGeneration,
		DesiredCatalogGeneration: projection.CatalogGeneration,
		Removed:                  projection.Removed,
		ControlRevision:          1, ChangedAt: now,
	}
	if exists {
		next.ControlRevision = prior.ControlRevision + 1
		if !prior.Removed || projection.Removed {
			next.ActiveDesiredGeneration = prior.ActiveDesiredGeneration
			next.ActiveSourceGeneration = prior.ActiveSourceGeneration
			next.ActiveCatalogGeneration = prior.ActiveCatalogGeneration
			next.ActiveSearchGeneration = prior.ActiveSearchGeneration
		}
	}
	switch projection.Service.Disposition {
	case servicecatalog.DispositionRejected:
		next.Status = servicecatalog.StatusRemoved
	case servicecatalog.DispositionConflict:
		next.Status = servicecatalog.StatusConflict
	case servicecatalog.DispositionProposal:
		next.Status = servicecatalog.StatusUnavailable
	case servicecatalog.DispositionAccepted:
		switch {
		case next.ActiveDesiredGeneration == next.DesiredGeneration &&
			next.ActiveSourceGeneration == next.DesiredSourceGeneration:
			next.Status = servicecatalog.StatusCurrent
		case next.ActiveDesiredGeneration != "":
			next.Status = servicecatalog.StatusStale
		default:
			next.Status = servicecatalog.StatusUnavailable
		}
	default:
		return servicecatalog.ServiceState{}, false, servicecatalog.ErrInvalidServiceState
	}
	if err := servicecatalog.SetServiceStateDigest(&next); err != nil {
		return servicecatalog.ServiceState{}, false, err
	}
	if err := servicecatalog.ValidateServiceState(next, true); err != nil {
		return servicecatalog.ServiceState{}, false, err
	}
	return next, true, nil
}

func implicitRemovedServiceState(
	prior servicecatalog.ServiceState,
	now time.Time,
) (servicecatalog.ServiceState, error) {
	next := prior
	next.Successors = []string{}
	next.Disposition = servicecatalog.DispositionRejected
	next.Reason = servicecatalog.ImplicitRemovalReason
	next.DesiredGeneration = ""
	next.DesiredSourceGeneration = ""
	next.DesiredCatalogGeneration = ""
	next.Status = servicecatalog.StatusRemoved
	next.Removed = true
	next.ControlRevision++
	next.ChangedAt = now
	// The repository summary records the exact catalog transition that removed
	// an omitted key; the tombstone retains its prior active identity and ABA
	// fence without pretending to be a catalog-authored desired record.
	if err := servicecatalog.SetServiceStateDigest(&next); err != nil {
		return servicecatalog.ServiceState{}, err
	}
	if err := servicecatalog.ValidateServiceState(next, true); err != nil {
		return servicecatalog.ServiceState{}, err
	}
	return next, nil
}

func tombstoneDelta(
	prior servicecatalog.ServiceState,
	exists bool,
	next servicecatalog.ServiceState,
) int {
	if !exists {
		if next.Removed {
			return 1
		}
		return 0
	}
	if prior.Removed == next.Removed {
		return 0
	}
	if next.Removed {
		return 1
	}
	return -1
}

func buildServiceStateSummary(
	publication servicecatalog.Publication,
	projections []servicecatalog.ServiceProjection,
	states map[string]servicecatalog.ServiceState,
	tombstones int,
	prior *servicecatalog.RepositoryState,
	now time.Time,
) (servicecatalog.RepositoryState, error) {
	summary := servicecatalog.RepositoryState{
		Schema:                 servicecatalog.RepositoryStateSchema,
		Repository:             publication.Repository,
		CatalogGeneration:      publication.GenerationDigest,
		CatalogControlRevision: publication.ControlRevision,
		CatalogServiceCount:    len(projections), TombstoneCount: tombstones,
		ControlRevision: 1, UpdatedAt: now,
	}
	if prior != nil {
		summary.ControlRevision = prior.ControlRevision + 1
	}
	for _, projection := range projections {
		state, exists := states[projection.Service.Key]
		if !exists {
			return servicecatalog.RepositoryState{}, servicecatalog.ErrInvalidServiceState
		}
		switch state.Status {
		case servicecatalog.StatusCurrent:
			summary.CurrentCount++
		case servicecatalog.StatusStale:
			summary.StaleCount++
		case servicecatalog.StatusUnavailable:
			summary.UnavailableCount++
		case servicecatalog.StatusConflict:
			summary.ConflictCount++
		case servicecatalog.StatusRemoved:
			continue
		default:
			return servicecatalog.RepositoryState{}, servicecatalog.ErrInvalidServiceState
		}
		summary.LiveServiceCount++
	}
	if err := servicecatalog.SetRepositoryStateDigest(&summary); err != nil {
		return servicecatalog.RepositoryState{}, err
	}
	if err := servicecatalog.ValidateRepositoryState(summary, true); err != nil {
		return servicecatalog.RepositoryState{}, err
	}
	return summary, nil
}

func serviceStateFromRec(row serviceStateRec) (*servicecatalog.ServiceState, error) {
	state := servicecatalog.ServiceState{
		Schema: row.Schema, Repository: row.Repository, ServiceKey: row.ServiceKey,
		DisplayName: row.DisplayName, Disposition: row.Disposition, Origin: row.Origin,
		Reason: row.Reason, Successors: slices.Clone(row.Successors),
		Incarnation:              row.Incarnation,
		DesiredGeneration:        row.DesiredGeneration,
		DesiredSourceGeneration:  row.DesiredSourceGeneration,
		DesiredCatalogGeneration: row.DesiredCatalogGeneration,
		ActiveDesiredGeneration:  row.ActiveDesiredGeneration,
		ActiveSourceGeneration:   row.ActiveSourceGeneration,
		ActiveCatalogGeneration:  row.ActiveCatalogGeneration,
		ActiveSearchGeneration:   row.ActiveSearchGeneration,
		Status:                   row.Status, Removed: row.Removed, StateDigest: row.StateDigest,
		ControlRevision: row.ControlRevision, ChangedAt: row.ChangedAt.UTC(),
	}
	if err := servicecatalog.ValidateServiceState(state, true); err != nil {
		return nil, err
	}
	return &state, nil
}

func repositoryStateFromRec(row serviceRepositoryStateRec) servicecatalog.RepositoryState {
	return servicecatalog.RepositoryState{
		Schema: row.Schema, Repository: row.Repository,
		CatalogGeneration:      row.CatalogGeneration,
		CatalogControlRevision: row.CatalogControlRevision,
		CatalogServiceCount:    row.CatalogServiceCount,
		LiveServiceCount:       row.LiveServiceCount,
		CurrentCount:           row.CurrentCount, StaleCount: row.StaleCount,
		UnavailableCount: row.UnavailableCount, ConflictCount: row.ConflictCount,
		TombstoneCount: row.TombstoneCount, SummaryDigest: row.SummaryDigest,
		ControlRevision: row.ControlRevision, UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func serviceStateContent(state servicecatalog.ServiceState) map[string]any {
	successors := append([]string{}, state.Successors...)
	return map[string]any{
		"schema": state.Schema, "repository": state.Repository,
		"service_key": state.ServiceKey, "display_name": state.DisplayName,
		"disposition": state.Disposition, "origin": state.Origin,
		"reason": state.Reason, "successors": successors,
		"incarnation":                state.Incarnation,
		"desired_generation":         state.DesiredGeneration,
		"desired_source_generation":  state.DesiredSourceGeneration,
		"desired_catalog_generation": state.DesiredCatalogGeneration,
		"active_desired_generation":  state.ActiveDesiredGeneration,
		"active_source_generation":   state.ActiveSourceGeneration,
		"active_catalog_generation":  state.ActiveCatalogGeneration,
		"active_search_generation":   state.ActiveSearchGeneration,
		"status":                     state.Status, "removed": state.Removed,
		"state_digest":     state.StateDigest,
		"control_revision": state.ControlRevision, "changed_at": state.ChangedAt,
	}
}

func serviceRepositoryStateContent(state servicecatalog.RepositoryState) map[string]any {
	return map[string]any{
		"schema": state.Schema, "repository": state.Repository,
		"catalog_generation":       state.CatalogGeneration,
		"catalog_control_revision": state.CatalogControlRevision,
		"catalog_service_count":    state.CatalogServiceCount,
		"live_service_count":       state.LiveServiceCount,
		"current_count":            state.CurrentCount, "stale_count": state.StaleCount,
		"unavailable_count": state.UnavailableCount,
		"conflict_count":    state.ConflictCount,
		"tombstone_count":   state.TombstoneCount,
		"summary_digest":    state.SummaryDigest,
		"control_revision":  state.ControlRevision, "updated_at": state.UpdatedAt,
	}
}
