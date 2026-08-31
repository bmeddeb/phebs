package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

type ServiceStateV3ReadSource interface {
	servicecatalogv3.ReadSource
	GetServiceCatalogV3CandidatePointer(
		context.Context,
		string,
	) (ServiceCatalogV3Pointer, error)
	GetServiceStateV3SummaryPoint(
		context.Context,
		string,
	) (servicecatalog.RepositoryState, error)
	GetServiceStateV3Point(
		context.Context,
		string,
		string,
	) (servicecatalog.ServiceState, error)
	ListServiceStateV3Rows(
		context.Context,
		string,
		string,
		int,
	) ([]servicecatalog.ServiceState, error)
	ListAcceptedServiceStateV3Rows(
		context.Context,
		string,
		int,
	) ([]servicecatalog.ServiceState, error)
	ConfirmServiceStateV3Snapshot(
		context.Context,
		ServiceCatalogV3Pointer,
		servicecatalog.RepositoryState,
	) error
}

var _ ServiceStateV3ReadSource = (*Surreal)(nil)

type ServiceStateV3Reader struct {
	source ServiceStateV3ReadSource
	cache  *servicecatalogv3.ReadCache
}

func NewServiceStateV3Reader(
	source ServiceStateV3ReadSource,
	cache *servicecatalogv3.ReadCache,
) (*ServiceStateV3Reader, error) {
	if source == nil || cache == nil {
		return nil, fmt.Errorf("new service state v3 reader: %w", ErrInvalidServiceStateV3)
	}
	return &ServiceStateV3Reader{source: source, cache: cache}, nil
}

type ServiceStateV3Read struct {
	Pointer          ServiceCatalogV3Pointer
	Root             servicecatalogv3.Root
	ActiveRoot       servicecatalogv3.Root
	Summary          servicecatalog.RepositoryState
	Entry            ServiceStateEntry
	ActiveProjection *servicecatalog.ServiceProjection

	currentLease *servicecatalogv3.ReadLease
	activeLease  *servicecatalogv3.ReadLease
	closeOnce    sync.Once
}

func (read *ServiceStateV3Read) Close() {
	if read == nil {
		return
	}
	read.closeOnce.Do(func() {
		if read.activeLease != nil {
			read.activeLease.Close()
		}
		if read.currentLease != nil {
			read.currentLease.Close()
		}
	})
}

type ServiceStateV3Page struct {
	Pointer      ServiceCatalogV3Pointer
	Root         servicecatalogv3.Root
	Summary      servicecatalog.RepositoryState
	Entries      []ServiceStateEntry
	Continuation *ServiceStatePosition
}

type ServiceStateV3Snapshot struct {
	Pointer ServiceCatalogV3Pointer
	Root    servicecatalogv3.Root
	Summary servicecatalog.RepositoryState
	States  []servicecatalog.ServiceState
}

func (reader *ServiceStateV3Reader) OpenService(
	ctx context.Context,
	repository, serviceKey string,
) (_ *ServiceStateV3Read, retErr error) {
	if reader == nil || validateCandidateRepository(repository) != nil || serviceKey == "" {
		return nil, fmt.Errorf("open service state v3: %w", ErrInvalidServiceStateV3)
	}
	pointer, err := reader.source.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("open service state v3: catalog pointer: %w", err)
	}
	current, err := reader.cache.Open(
		ctx, reader.source, repository, pointer.RootDigest,
	)
	if err != nil {
		return nil, fmt.Errorf("open service state v3: current catalog: %w", err)
	}
	read := &ServiceStateV3Read{Pointer: pointer, currentLease: current}
	defer func() {
		if retErr != nil {
			read.Close()
		}
	}()
	root, valid := current.Root()
	if !valid {
		return nil, fmt.Errorf("open service state v3: current catalog lease: %w", ErrConflict)
	}
	read.Root = root
	summary, err := reader.source.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("open service state v3: summary: %w", err)
	}
	if !sameServiceStateV3Fence(pointer, summary) {
		return nil, fmt.Errorf("open service state v3: unreconciled summary: %w", ErrConflict)
	}
	read.Summary = summary
	state, err := reader.source.GetServiceStateV3Point(ctx, repository, serviceKey)
	if err != nil {
		return nil, fmt.Errorf("open service state v3: state: %w", err)
	}
	read.Entry.State = state
	desired, err := current.Service(ctx, reader.source, serviceKey)
	if err == nil {
		if validateErr := servicecatalogv3.ValidateStateProjection(
			state, desired, false,
		); validateErr != nil {
			return nil, fmt.Errorf("open service state v3: desired projection: %w", validateErr)
		}
		read.Entry.Projection = cloneServiceStateV3Projection(&desired)
	} else if !errors.Is(err, os.ErrNotExist) || !state.Removed {
		return nil, fmt.Errorf("open service state v3: desired projection: %w", err)
	}

	if state.ActiveCatalogGeneration == "" {
		return read, nil
	}
	activeLease := current
	activeRoot := root
	if state.ActiveCatalogGeneration != pointer.RootDigest {
		activeLease, err = reader.cache.Open(
			ctx, reader.source, repository, state.ActiveCatalogGeneration,
		)
		if err != nil {
			return nil, fmt.Errorf("open service state v3: active catalog: %w", err)
		}
		read.activeLease = activeLease
		activeRoot, valid = activeLease.Root()
		if !valid {
			return nil, fmt.Errorf("open service state v3: active catalog lease: %w", ErrConflict)
		}
	}
	active, err := activeLease.Service(ctx, reader.source, serviceKey)
	if err != nil {
		return nil, fmt.Errorf("open service state v3: active projection: %w", err)
	}
	if err := servicecatalogv3.ValidateStateProjection(state, active, true); err != nil {
		return nil, fmt.Errorf("open service state v3: active projection: %w", err)
	}
	read.ActiveRoot = activeRoot
	read.ActiveProjection = cloneServiceStateV3Projection(&active)
	return read, nil
}

func (reader *ServiceStateV3Reader) ListServices(
	ctx context.Context,
	repository string,
	filter ServiceStateFilter,
	after ServiceStatePosition,
	limit int,
) (_ *ServiceStateV3Page, retErr error) {
	if reader == nil || validateCandidateRepository(repository) != nil ||
		validateServiceStateFilter(filter) != nil || limit < 1 ||
		limit > MaxServiceStateReadPage ||
		(after.ServiceKey == "") != (after.Incarnation == 0) {
		return nil, fmt.Errorf("list service states v3: %w", ErrInvalidServiceStateV3)
	}
	pointer, err := reader.source.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("list service states v3: catalog pointer: %w", err)
	}
	lease, err := reader.cache.Open(ctx, reader.source, repository, pointer.RootDigest)
	if err != nil {
		return nil, fmt.Errorf("list service states v3: catalog: %w", err)
	}
	defer lease.Close()
	root, valid := lease.Root()
	if !valid {
		return nil, fmt.Errorf("list service states v3: catalog lease: %w", ErrConflict)
	}
	summary, err := reader.source.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("list service states v3: summary: %w", err)
	}
	if !sameServiceStateV3Fence(pointer, summary) {
		return nil, fmt.Errorf("list service states v3: unreconciled summary: %w", ErrConflict)
	}
	if after.ServiceKey != "" {
		anchor, anchorErr := reader.source.GetServiceStateV3Point(
			ctx, repository, after.ServiceKey,
		)
		if anchorErr != nil || anchor.Incarnation != after.Incarnation {
			return nil, fmt.Errorf("list service states v3: seek changed: %w", ErrConflict)
		}
	}
	rows, err := reader.source.ListServiceStateV3Rows(
		ctx, repository, after.ServiceKey, maxServiceStateScanPage+1,
	)
	if err != nil {
		return nil, fmt.Errorf("list service states v3: rows: %w", err)
	}
	if len(rows) > maxServiceStateScanPage+1 {
		return nil, fmt.Errorf("list service states v3: row bound: %w", ErrConflict)
	}
	hasMore := len(rows) > maxServiceStateScanPage
	if hasMore {
		rows = rows[:maxServiceStateScanPage]
	}
	entries := make([]ServiceStateEntry, 0, min(limit, len(rows)))
	prior := after.ServiceKey
	var continuation *ServiceStatePosition
	for index, state := range rows {
		if state.Repository != repository || state.ServiceKey <= prior {
			return nil, fmt.Errorf("list service states v3: row order: %w", ErrInvalidServiceStateV3)
		}
		prior = state.ServiceKey
		projection, projectionErr := lease.Service(ctx, reader.source, state.ServiceKey)
		var projected *servicecatalog.ServiceProjection
		switch {
		case projectionErr == nil:
			if err := servicecatalogv3.ValidateStateProjection(
				state, projection, false,
			); err != nil {
				return nil, fmt.Errorf("list service states v3: projection: %w", err)
			}
			projected = cloneServiceStateV3Projection(&projection)
		case errors.Is(projectionErr, os.ErrNotExist) && state.Removed:
		default:
			return nil, fmt.Errorf("list service states v3: projection: %w", projectionErr)
		}
		matches := (filter.Status == "" || state.Status == filter.Status) &&
			(filter.Disposition == "" || state.Disposition == filter.Disposition) &&
			(filter.IncludeRemoved || !state.Removed)
		if !matches {
			continue
		}
		entries = append(entries, ServiceStateEntry{
			State: state, Projection: projected,
		})
		if len(entries) == limit {
			if index < len(rows)-1 || hasMore {
				continuation = &ServiceStatePosition{
					ServiceKey: state.ServiceKey, Incarnation: state.Incarnation,
				}
			}
			break
		}
	}
	if len(entries) < limit && hasMore && len(rows) != 0 {
		last := rows[len(rows)-1]
		continuation = &ServiceStatePosition{
			ServiceKey: last.ServiceKey, Incarnation: last.Incarnation,
		}
	}
	if err := reader.source.ConfirmServiceStateV3Snapshot(
		ctx, pointer, summary,
	); err != nil {
		return nil, fmt.Errorf("list service states v3: final fence: %w", err)
	}
	return &ServiceStateV3Page{
		Pointer: pointer, Root: root, Summary: summary,
		Entries: entries, Continuation: continuation,
	}, nil
}

// AcceptedSnapshot opens the current v3 root once, reads accepted live state
// rows once, and streams every service member once. It is the dark batch
// boundary for relationship builders and final-fences the complete snapshot.
func (reader *ServiceStateV3Reader) AcceptedSnapshot(
	ctx context.Context,
	repository string,
) (_ ServiceStateV3Snapshot, retErr error) {
	if reader == nil || validateCandidateRepository(repository) != nil {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: %w", ErrInvalidServiceStateV3,
		)
	}
	pointer, err := reader.source.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: catalog pointer: %w", err,
		)
	}
	lease, err := reader.cache.Open(ctx, reader.source, repository, pointer.RootDigest)
	if err != nil {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: catalog: %w", err,
		)
	}
	defer lease.Close()
	root, valid := lease.Root()
	if !valid {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: catalog lease: %w", ErrConflict,
		)
	}
	summary, err := reader.source.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil || !sameServiceStateV3Fence(pointer, summary) {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: summary: %w",
			errors.Join(err, ErrConflict),
		)
	}
	rows, err := reader.source.ListAcceptedServiceStateV3Rows(
		ctx, repository, servicecatalogv3.MaxTotalServices+1,
	)
	if err != nil {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: rows: %w", err,
		)
	}
	if len(rows) != root.Dispositions.Accepted {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: row count: %w", ErrConflict,
		)
	}
	byKey := make(map[string]servicecatalog.ServiceState, len(rows))
	prior := ""
	for _, state := range rows {
		if state.Repository != repository || state.ServiceKey <= prior || state.Removed ||
			state.Disposition != servicecatalog.DispositionAccepted {
			return ServiceStateV3Snapshot{}, fmt.Errorf(
				"accepted service state v3 snapshot: row identity: %w",
				ErrInvalidServiceStateV3,
			)
		}
		prior = state.ServiceKey
		byKey[state.ServiceKey] = state
	}
	states := make([]servicecatalog.ServiceState, 0, len(rows))
	err = lease.StreamServices(
		ctx, reader.source,
		func(projections []servicecatalog.ServiceProjection) error {
			for _, projection := range projections {
				if projection.Service.Disposition != servicecatalog.DispositionAccepted {
					continue
				}
				state, ok := byKey[projection.Service.Key]
				if !ok || servicecatalogv3.ValidateStateProjection(
					state, projection, false,
				) != nil {
					return ErrInvalidServiceStateV3
				}
				states = append(states, cloneServiceStateV3(state))
			}
			return nil
		},
	)
	if err != nil || len(states) != len(rows) {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: catalog/state join: %w",
			errors.Join(err, ErrInvalidServiceStateV3),
		)
	}
	if err := reader.source.ConfirmServiceStateV3Snapshot(
		ctx, pointer, summary,
	); err != nil {
		return ServiceStateV3Snapshot{}, fmt.Errorf(
			"accepted service state v3 snapshot: final fence: %w", err,
		)
	}
	return ServiceStateV3Snapshot{
		Pointer: pointer, Root: root, Summary: summary, States: states,
	}, nil
}

func (reader *ServiceStateV3Reader) Confirm(
	ctx context.Context,
	read *ServiceStateV3Read,
) error {
	if reader == nil || read == nil {
		return fmt.Errorf("confirm service state v3 read: %w", ErrInvalidServiceStateV3)
	}
	if read.currentLease == nil {
		return fmt.Errorf("confirm service state v3 read: missing lease: %w", ErrConflict)
	}
	if _, valid := read.currentLease.Root(); !valid {
		return fmt.Errorf("confirm service state v3 read: closed lease: %w", ErrConflict)
	}
	if err := reader.source.ConfirmServiceStateV3Snapshot(
		ctx, read.Pointer, read.Summary,
	); err != nil {
		return fmt.Errorf("confirm service state v3 read: %w", err)
	}
	return nil
}

func (s *Surreal) GetServiceStateV3SummaryPoint(
	ctx context.Context,
	repository string,
) (servicecatalog.RepositoryState, error) {
	if validateCandidateRepository(repository) != nil {
		return servicecatalog.RepositoryState{}, ErrInvalidServiceStateV3
	}
	summary, err := s.getRawServiceStateV3Summary(ctx, repository)
	if err != nil {
		return servicecatalog.RepositoryState{}, err
	}
	return *summary, nil
}

func (s *Surreal) GetServiceStateV3Point(
	ctx context.Context,
	repository, serviceKey string,
) (servicecatalog.ServiceState, error) {
	if validateCandidateRepository(repository) != nil || serviceKey == "" {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	results, err := surrealdb.Query[[]serviceStateRec](
		ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3ID(repository, serviceKey)},
	)
	if err != nil {
		return servicecatalog.ServiceState{}, err
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return servicecatalog.ServiceState{}, ErrNotFound
	}
	if len(rows) != 1 {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	state, err := serviceStateV3FromRec(rows[0])
	identifier, _ := serviceStateV3ID(repository, serviceKey).ID.(string)
	if err != nil || state.Repository != repository || state.ServiceKey != serviceKey ||
		!validServiceCatalogV3RecordID(
			rows[0].RecID, "service_state_v3_current", identifier,
		) {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	return cloneServiceStateV3(*state), nil
}

func (s *Surreal) ListServiceStateV3Rows(
	ctx context.Context,
	repository, after string,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	if validateCandidateRepository(repository) != nil || limit < 1 ||
		limit > maxServiceStateScanPage+1 {
		return nil, ErrInvalidServiceStateV3
	}
	results, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository AND service_key > $after
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": repository, "after": after, "limit": limit,
	})
	if err != nil {
		return nil, err
	}
	return decodeServiceStateV3Rows(repository, firstDomainRows(results), limit)
}

func (s *Surreal) ListAcceptedServiceStateV3Rows(
	ctx context.Context,
	repository string,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	if validateCandidateRepository(repository) != nil || limit < 1 ||
		limit > servicecatalogv3.MaxTotalServices+1 {
		return nil, ErrInvalidServiceStateV3
	}
	results, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository AND removed = false AND disposition = $accepted
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": repository, "accepted": servicecatalog.DispositionAccepted,
		"limit": limit,
	})
	if err != nil {
		return nil, err
	}
	return decodeServiceStateV3Rows(repository, firstDomainRows(results), limit)
}

func (s *Surreal) ConfirmServiceStateV3Snapshot(
	ctx context.Context,
	expected ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) error {
	current, err := s.GetServiceCatalogV3CandidatePointer(ctx, expected.Repository)
	if err != nil {
		return fmt.Errorf("confirm service state v3 snapshot: catalog pointer: %w", err)
	}
	currentSummary, err := s.GetServiceStateV3SummaryPoint(ctx, expected.Repository)
	if err != nil {
		return fmt.Errorf("confirm service state v3 snapshot: summary: %w", err)
	}
	if current.RootDigest != expected.RootDigest ||
		current.ControlRevision != expected.ControlRevision ||
		currentSummary.CatalogGeneration != summary.CatalogGeneration ||
		currentSummary.CatalogControlRevision != summary.CatalogControlRevision ||
		currentSummary.ControlRevision != summary.ControlRevision ||
		currentSummary.SummaryDigest != summary.SummaryDigest {
		return fmt.Errorf("confirm service state v3 snapshot: changed: %w", ErrConflict)
	}
	return nil
}

func decodeServiceStateV3Rows(
	repository string,
	rows []serviceStateRec,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	if len(rows) > limit {
		return nil, ErrInvalidServiceStateV3
	}
	states := make([]servicecatalog.ServiceState, 0, len(rows))
	prior := ""
	for _, row := range rows {
		state, err := serviceStateV3FromRec(row)
		if err != nil || state.Repository != repository || state.ServiceKey <= prior {
			return nil, ErrInvalidServiceStateV3
		}
		identifier, _ := serviceStateV3ID(repository, state.ServiceKey).ID.(string)
		if !validServiceCatalogV3RecordID(
			row.RecID, "service_state_v3_current", identifier,
		) {
			return nil, ErrInvalidServiceStateV3
		}
		prior = state.ServiceKey
		states = append(states, cloneServiceStateV3(*state))
	}
	return states, nil
}

func sameServiceStateV3Fence(
	pointer ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) bool {
	return pointer.Repository == summary.Repository &&
		pointer.RootDigest == summary.CatalogGeneration &&
		pointer.ControlRevision == summary.CatalogControlRevision
}

func cloneServiceStateV3(state servicecatalog.ServiceState) servicecatalog.ServiceState {
	state.Successors = slices.Clone(state.Successors)
	return state
}

func cloneServiceStateV3Projection(
	projection *servicecatalog.ServiceProjection,
) *servicecatalog.ServiceProjection {
	if projection == nil {
		return nil
	}
	cloned := *projection
	cloned.Service.Successors = slices.Clone(cloned.Service.Successors)
	cloned.Memberships = slices.Clone(cloned.Memberships)
	return &cloned
}
