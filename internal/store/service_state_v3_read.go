package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sync"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/readaccounting"
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

// serviceStateV3SnapshotReadSource is the production selected-runtime seam.
// The narrower assertion keeps existing in-memory read sources usable for
// current-only tests while Surreal resolves immutable selected revisions.
type serviceStateV3SnapshotReadSource interface {
	GetServiceStateV3SummarySnapshot(
		context.Context,
		string,
		uint64,
		string,
	) (servicecatalog.RepositoryState, error)
	GetServiceStateV3PointSnapshot(
		context.Context,
		string,
		string,
		uint64,
		string,
	) (servicecatalog.ServiceState, error)
	ListServiceStateV3RowsSnapshot(
		context.Context,
		string,
		string,
		int,
		uint64,
		string,
	) ([]servicecatalog.ServiceState, error)
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
	selector     *ServiceRuntimeSelector
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

// ServiceStateV3Page pins every verified member used by the page until Close.
type ServiceStateV3Page struct {
	Pointer      ServiceCatalogV3Pointer
	Root         servicecatalogv3.Root
	Summary      servicecatalog.RepositoryState
	Entries      []ServiceStateEntry
	Continuation *ServiceStatePosition

	lease     *servicecatalogv3.ReadLease
	selector  *ServiceRuntimeSelector
	closeOnce sync.Once
}

func (page *ServiceStateV3Page) Close() {
	if page == nil {
		return
	}
	page.closeOnce.Do(func() {
		if page.lease != nil {
			page.lease.Close()
		}
	})
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
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			err = serviceStateV3AuthorityError(err)
		}
		return nil, fmt.Errorf("open service state v3: catalog pointer: %w", err)
	}
	if pointer.Repository != repository {
		return nil, fmt.Errorf("open service state v3: catalog pointer: %w", ErrConflict)
	}
	return reader.openService(ctx, pointer, nil, serviceKey)
}

// OpenServiceSelected opens the exact catalog and state authority named by a
// selected v3 runtime. It deliberately does not consult the independently
// advancing dark-candidate pointer.
func (reader *ServiceStateV3Reader) OpenServiceSelected(
	ctx context.Context,
	selector ServiceRuntimeSelector,
	serviceKey string,
) (_ *ServiceStateV3Read, retErr error) {
	pointer, err := serviceStateV3SelectorPointer(selector)
	if reader == nil || err != nil || serviceKey == "" {
		return nil, fmt.Errorf("open selected service state v3: %w", ErrInvalidServiceStateV3)
	}
	return reader.openService(ctx, pointer, &selector, serviceKey)
}

func (reader *ServiceStateV3Reader) openService(
	ctx context.Context,
	pointer ServiceCatalogV3Pointer,
	selector *ServiceRuntimeSelector,
	serviceKey string,
) (_ *ServiceStateV3Read, retErr error) {
	repository := pointer.Repository
	current, err := reader.cache.Open(
		ctx, reader.source, repository, pointer.RootDigest,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"open service state v3: current catalog: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	read := &ServiceStateV3Read{
		Pointer: pointer, currentLease: current,
		selector: cloneServiceRuntimeSelectorPointer(selector),
	}
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
	summary, err := reader.getSummary(ctx, repository, selector)
	if err != nil {
		return nil, fmt.Errorf(
			"open service state v3: summary: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	if !sameServiceStateV3Fence(pointer, summary) {
		return nil, fmt.Errorf("open service state v3: unreconciled summary: %w", ErrConflict)
	}
	if selector != nil && !sameSelectedServiceStateV3Fence(*selector, summary) {
		return nil, fmt.Errorf("open service state v3: selector summary: %w", ErrConflict)
	}
	read.Summary = summary
	state, err := reader.getPoint(ctx, repository, serviceKey, selector)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			err = serviceStateV3AuthorityError(err)
		}
		return nil, fmt.Errorf("open service state v3: state: %w", err)
	}
	read.Entry.State = state
	desired, err := current.Service(ctx, reader.source, serviceKey)
	if err == nil {
		if validateErr := servicecatalogv3.ValidateStateProjection(
			state, desired, false,
		); validateErr != nil {
			return nil, fmt.Errorf(
				"open service state v3: desired projection: %w",
				serviceStateV3AuthorityError(validateErr),
			)
		}
		read.Entry.Projection = cloneServiceStateV3Projection(&desired)
	} else if !errors.Is(err, os.ErrNotExist) || !state.Removed {
		return nil, fmt.Errorf(
			"open service state v3: desired projection: %w",
			serviceStateV3AuthorityError(err),
		)
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
			return nil, fmt.Errorf(
				"open service state v3: active catalog: %w",
				serviceStateV3AuthorityError(err),
			)
		}
		read.activeLease = activeLease
		activeRoot, valid = activeLease.Root()
		if !valid {
			return nil, fmt.Errorf("open service state v3: active catalog lease: %w", ErrConflict)
		}
	}
	active, err := activeLease.Service(ctx, reader.source, serviceKey)
	if err != nil {
		return nil, fmt.Errorf(
			"open service state v3: active projection: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	if err := servicecatalogv3.ValidateStateProjection(state, active, true); err != nil {
		return nil, fmt.Errorf(
			"open service state v3: active projection: %w",
			serviceStateV3AuthorityError(err),
		)
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
		(after.ServiceKey == "") != (after.Incarnation == 0) ||
		(after.ServiceKey == "") != (after.MemberRangeDigest == "") {
		return nil, fmt.Errorf("list service states v3: %w", ErrInvalidServiceStateV3)
	}
	pointer, err := reader.source.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			err = serviceStateV3AuthorityError(err)
		}
		return nil, fmt.Errorf("list service states v3: catalog pointer: %w", err)
	}
	if pointer.Repository != repository {
		return nil, fmt.Errorf("list service states v3: catalog pointer: %w", ErrConflict)
	}
	return reader.listServices(ctx, pointer, nil, filter, after, limit)
}

// ListServicesSelected reads the exact catalog root named by the selected v3
// runtime while joining it to the still-selected current state summary/rows.
// A newer dark candidate pointer is intentionally irrelevant.
func (reader *ServiceStateV3Reader) ListServicesSelected(
	ctx context.Context,
	selector ServiceRuntimeSelector,
	filter ServiceStateFilter,
	after ServiceStatePosition,
	limit int,
) (_ *ServiceStateV3Page, retErr error) {
	pointer, err := serviceStateV3SelectorPointer(selector)
	if reader == nil || err != nil || validateServiceStateFilter(filter) != nil ||
		limit < 1 || limit > MaxServiceStateReadPage ||
		(after.ServiceKey == "") != (after.Incarnation == 0) ||
		(after.ServiceKey == "") != (after.MemberRangeDigest == "") {
		return nil, fmt.Errorf("list selected service states v3: %w", ErrInvalidServiceStateV3)
	}
	return reader.listServices(ctx, pointer, &selector, filter, after, limit)
}

func (reader *ServiceStateV3Reader) listServices(
	ctx context.Context,
	pointer ServiceCatalogV3Pointer,
	selector *ServiceRuntimeSelector,
	filter ServiceStateFilter,
	after ServiceStatePosition,
	limit int,
) (_ *ServiceStateV3Page, retErr error) {
	repository := pointer.Repository
	lease, err := reader.cache.Open(ctx, reader.source, repository, pointer.RootDigest)
	if err != nil {
		return nil, fmt.Errorf(
			"list service states v3: catalog: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	defer func() {
		if retErr != nil {
			lease.Close()
		}
	}()
	root, valid := lease.Root()
	if !valid {
		return nil, fmt.Errorf("list service states v3: catalog lease: %w", ErrConflict)
	}
	summary, err := reader.getSummary(ctx, repository, selector)
	if err != nil {
		return nil, fmt.Errorf(
			"list service states v3: summary: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	if !sameServiceStateV3Fence(pointer, summary) {
		return nil, fmt.Errorf("list service states v3: unreconciled summary: %w", ErrConflict)
	}
	if selector != nil && !sameSelectedServiceStateV3Fence(*selector, summary) {
		return nil, fmt.Errorf("list service states v3: selector summary: %w", ErrConflict)
	}
	if after.ServiceKey != "" {
		anchor, anchorErr := reader.getPoint(
			ctx, repository, after.ServiceKey, selector,
		)
		if anchorErr != nil {
			anchorErr = serviceStateV3AuthorityError(anchorErr)
			if errors.Is(anchorErr, ErrConflict) {
				return nil, fmt.Errorf("list service states v3: seek changed: %w", ErrConflict)
			}
			return nil, fmt.Errorf("list service states v3: seek: %w", anchorErr)
		}
		position, positionErr := serviceStateV3Position(root, anchor)
		if positionErr != nil || anchor.Incarnation != after.Incarnation || position != after {
			return nil, fmt.Errorf("list service states v3: seek changed: %w", ErrConflict)
		}
	}
	rows, err := reader.listRows(
		ctx, repository, after.ServiceKey, maxServiceStateScanPage+1, selector,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"list service states v3: rows: %w",
			serviceStateV3AuthorityError(err),
		)
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
			return nil, fmt.Errorf(
				"list service states v3: row order: %w",
				errors.Join(ErrInvalidServiceStateV3, ErrConflict),
			)
		}
		prior = state.ServiceKey
		projection, projectionErr := lease.Service(ctx, reader.source, state.ServiceKey)
		var projected *servicecatalog.ServiceProjection
		switch {
		case projectionErr == nil:
			if err := servicecatalogv3.ValidateStateProjection(
				state, projection, false,
			); err != nil {
				return nil, fmt.Errorf(
					"list service states v3: projection: %w",
					serviceStateV3AuthorityError(err),
				)
			}
			projected = cloneServiceStateV3Projection(&projection)
		case errors.Is(projectionErr, os.ErrNotExist) && state.Removed:
		default:
			return nil, fmt.Errorf(
				"list service states v3: projection: %w",
				serviceStateV3AuthorityError(projectionErr),
			)
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
				position, positionErr := serviceStateV3Position(root, state)
				if positionErr != nil {
					return nil, fmt.Errorf(
						"list service states v3: continuation: %w", positionErr,
					)
				}
				continuation = &position
			}
			break
		}
	}
	if len(entries) < limit && hasMore && len(rows) != 0 {
		last := rows[len(rows)-1]
		position, positionErr := serviceStateV3Position(root, last)
		if positionErr != nil {
			return nil, fmt.Errorf(
				"list service states v3: continuation: %w", positionErr,
			)
		}
		continuation = &position
	}
	if err := reader.confirmSnapshot(ctx, pointer, summary, selector); err != nil {
		return nil, fmt.Errorf(
			"list service states v3: final fence: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	return &ServiceStateV3Page{
		Pointer: pointer, Root: root, Summary: summary,
		Entries: entries, Continuation: continuation, lease: lease,
		selector: cloneServiceRuntimeSelectorPointer(selector),
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
			"accepted service state v3 snapshot: final fence: %w",
			serviceStateV3AuthorityError(err),
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
	if err := reader.confirmSnapshot(
		ctx, read.Pointer, read.Summary, read.selector,
	); err != nil {
		return fmt.Errorf(
			"confirm service state v3 read: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	return nil
}

func (reader *ServiceStateV3Reader) ConfirmPage(
	ctx context.Context,
	page *ServiceStateV3Page,
) error {
	if reader == nil || page == nil {
		return fmt.Errorf("confirm service state v3 page: %w", ErrInvalidServiceStateV3)
	}
	if page.lease == nil {
		return fmt.Errorf("confirm service state v3 page: missing lease: %w", ErrConflict)
	}
	if _, valid := page.lease.Root(); !valid {
		return fmt.Errorf("confirm service state v3 page: closed lease: %w", ErrConflict)
	}
	if err := reader.confirmSnapshot(
		ctx, page.Pointer, page.Summary, page.selector,
	); err != nil {
		return fmt.Errorf(
			"confirm service state v3 page: %w",
			serviceStateV3AuthorityError(err),
		)
	}
	return nil
}

func (reader *ServiceStateV3Reader) confirmSnapshot(
	ctx context.Context,
	pointer ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
	selector *ServiceRuntimeSelector,
) error {
	if selector == nil {
		return reader.source.ConfirmServiceStateV3Snapshot(ctx, pointer, summary)
	}
	if !sameServiceStateV3Fence(pointer, summary) ||
		!sameSelectedServiceStateV3Fence(*selector, summary) {
		return ErrConflict
	}
	current, err := reader.getSummary(ctx, pointer.Repository, selector)
	if err != nil {
		return fmt.Errorf("selected summary: %w", err)
	}
	if !sameServiceStateV3Summary(summary, current) {
		return ErrConflict
	}
	selectors, ok := reader.source.(ServiceRuntimeSelectorReader)
	if !ok {
		return ErrInvalidServiceRuntimeSelector
	}
	// This is deliberately last: a successful return linearizes the catalog,
	// state, and selector snapshot after every selected state read.
	return selectors.ConfirmServiceRuntimeSelector(ctx, *selector)
}

func (reader *ServiceStateV3Reader) getSummary(
	ctx context.Context,
	repository string,
	selector *ServiceRuntimeSelector,
) (servicecatalog.RepositoryState, error) {
	if selector == nil {
		return reader.source.GetServiceStateV3SummaryPoint(ctx, repository)
	}
	snapshots, ok := reader.source.(serviceStateV3SnapshotReadSource)
	if !ok {
		return servicecatalog.RepositoryState{}, ErrInvalidServiceStateV3
	}
	return snapshots.GetServiceStateV3SummarySnapshot(
		ctx,
		repository,
		selector.StateControlRevision,
		selector.StateSummaryDigest,
	)
}

func (reader *ServiceStateV3Reader) getPoint(
	ctx context.Context,
	repository, serviceKey string,
	selector *ServiceRuntimeSelector,
) (servicecatalog.ServiceState, error) {
	if selector == nil {
		return reader.source.GetServiceStateV3Point(ctx, repository, serviceKey)
	}
	snapshots, ok := reader.source.(serviceStateV3SnapshotReadSource)
	if !ok {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	return snapshots.GetServiceStateV3PointSnapshot(
		ctx,
		repository,
		serviceKey,
		selector.StateControlRevision,
		selector.StateSummaryDigest,
	)
}

func (reader *ServiceStateV3Reader) listRows(
	ctx context.Context,
	repository, after string,
	limit int,
	selector *ServiceRuntimeSelector,
) ([]servicecatalog.ServiceState, error) {
	if selector == nil {
		return reader.source.ListServiceStateV3Rows(ctx, repository, after, limit)
	}
	snapshots, ok := reader.source.(serviceStateV3SnapshotReadSource)
	if !ok {
		return nil, ErrInvalidServiceStateV3
	}
	return snapshots.ListServiceStateV3RowsSnapshot(
		ctx,
		repository,
		after,
		limit,
		selector.StateControlRevision,
		selector.StateSummaryDigest,
	)
}

func serviceStateV3Position(
	root servicecatalogv3.Root,
	state servicecatalog.ServiceState,
) (ServiceStatePosition, error) {
	descriptor, found := servicecatalogv3.ServiceMemberDescriptor(root, state.ServiceKey)
	if !found && !state.Removed {
		return ServiceStatePosition{}, errors.Join(ErrInvalidServiceStateV3, ErrConflict)
	}
	binding := struct {
		Schema      string                            `json:"schema"`
		RootDigest  string                            `json:"root_digest"`
		ServiceKey  string                            `json:"service_key"`
		Incarnation uint64                            `json:"incarnation"`
		Present     bool                              `json:"present"`
		Member      servicecatalogv3.MemberDescriptor `json:"member"`
	}{
		Schema: "phebs-service-state-v3-member-range-v1", RootDigest: root.Digest,
		ServiceKey: state.ServiceKey, Incarnation: state.Incarnation,
		Present: found, Member: descriptor,
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return ServiceStatePosition{}, err
	}
	sum := sha256.Sum256(raw)
	return ServiceStatePosition{
		ServiceKey: state.ServiceKey, Incarnation: state.Incarnation,
		MemberRangeDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func serviceStateV3AuthorityError(err error) error {
	if errors.Is(err, ErrNotFound) || errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, servicecatalogv3.ErrInvalid) ||
		errors.Is(err, ErrInvalidServiceStateV3) ||
		errors.Is(err, ErrInvalidServiceCatalogV3Candidate) {
		return errors.Join(err, ErrConflict)
	}
	return err
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

// GetServiceStateV3SummarySnapshot resolves one selected immutable state
// summary. Current dark authority remains on GetServiceStateV3SummaryPoint.
func (s *Surreal) GetServiceStateV3SummarySnapshot(
	ctx context.Context,
	repository string,
	snapshotRevision uint64,
	snapshotDigest string,
) (servicecatalog.RepositoryState, error) {
	if validateCandidateRepository(repository) != nil || snapshotRevision == 0 ||
		snapshotRevision > math.MaxInt64 || !validSHA256Digest(snapshotDigest) {
		return servicecatalog.RepositoryState{}, ErrInvalidServiceStateV3
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return servicecatalog.RepositoryState{}, fmt.Errorf(
			"get service state v3 summary snapshot: %w", err,
		)
	}
	currentResults, err := surrealdb.Query[[]serviceRepositoryStateRec](
		ctx,
		s.db,
		"SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3RepositoryID(repository)},
	)
	if err != nil {
		return servicecatalog.RepositoryState{}, err
	}
	currentRows := firstDomainRows(currentResults)
	if len(currentRows) > 1 {
		return servicecatalog.RepositoryState{}, ErrInvalidServiceStateV3
	}
	if len(currentRows) == 1 &&
		currentRows[0].ControlRevision == snapshotRevision &&
		currentRows[0].SummaryDigest == snapshotDigest {
		summary, summaryErr := serviceStateV3RepositoryFromRec(currentRows[0])
		if summaryErr != nil || summary.Repository != repository ||
			!validServiceCatalogV3RecordID(
				currentRows[0].RecID,
				"service_state_v3_repository",
				repository,
			) {
			return servicecatalog.RepositoryState{}, ErrInvalidServiceStateV3
		}
		return *summary, nil
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return servicecatalog.RepositoryState{}, fmt.Errorf(
			"get service state v3 summary snapshot: %w", err,
		)
	}
	preimageResults, err := surrealdb.Query[[]serviceRepositoryStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_repository_preimage
	WHERE repository = $repository AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 2`, map[string]any{
		"repository": repository, "snapshot_revision": snapshotRevision,
		"snapshot_digest": snapshotDigest,
	})
	if err != nil {
		return servicecatalog.RepositoryState{}, err
	}
	preimages := firstDomainRows(preimageResults)
	if len(preimages) == 0 {
		return servicecatalog.RepositoryState{}, ErrNotFound
	}
	if len(preimages) != 1 ||
		!validServiceStateV3PreimageRecord(preimages[0].RecID, "service_state_v3_repository_preimage") ||
		preimages[0].Repository != repository ||
		preimages[0].SnapshotRevision != snapshotRevision ||
		preimages[0].SnapshotDigest != snapshotDigest ||
		preimages[0].ControlRevision != snapshotRevision ||
		preimages[0].SummaryDigest != snapshotDigest {
		return servicecatalog.RepositoryState{}, ErrInvalidServiceStateV3
	}
	summary, err := serviceStateV3RepositoryFromRec(preimages[0])
	if err != nil {
		return servicecatalog.RepositoryState{}, ErrInvalidServiceStateV3
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

// GetServiceStateV3PointSnapshot resolves a selected row without consulting a
// successor's replacement. An unchanged current row is its own sparse image.
func (s *Surreal) GetServiceStateV3PointSnapshot(
	ctx context.Context,
	repository, serviceKey string,
	snapshotRevision uint64,
	snapshotDigest string,
) (servicecatalog.ServiceState, error) {
	if validateCandidateRepository(repository) != nil || serviceKey == "" ||
		snapshotRevision == 0 || snapshotRevision > math.MaxInt64 ||
		!validSHA256Digest(snapshotDigest) {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return servicecatalog.ServiceState{}, fmt.Errorf(
			"get service state v3 point snapshot: %w", err,
		)
	}
	results, err := surrealdb.Query[[]serviceStateRec](
		ctx,
		s.db,
		"SELECT * FROM $rid",
		map[string]any{"rid": serviceStateV3ID(repository, serviceKey)},
	)
	if err != nil {
		return servicecatalog.ServiceState{}, err
	}
	rows := firstDomainRows(results)
	if len(rows) > 1 {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	if len(rows) == 1 && rows[0].VisibleFrom > 0 &&
		rows[0].VisibleFrom <= snapshotRevision {
		state, stateErr := serviceStateV3FromRec(rows[0])
		identifier, _ := serviceStateV3ID(repository, serviceKey).ID.(string)
		if stateErr != nil || state.Repository != repository ||
			state.ServiceKey != serviceKey || !validServiceCatalogV3RecordID(
			rows[0].RecID,
			"service_state_v3_current",
			identifier,
		) {
			return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
		}
		return cloneServiceStateV3(*state), nil
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return servicecatalog.ServiceState{}, fmt.Errorf(
			"get service state v3 point snapshot: %w", err,
		)
	}
	preimageResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_preimage
	WHERE repository = $repository AND service_key = $service_key
		AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest LIMIT 2`, map[string]any{
		"repository": repository, "service_key": serviceKey,
		"snapshot_revision": snapshotRevision, "snapshot_digest": snapshotDigest,
	})
	if err != nil {
		return servicecatalog.ServiceState{}, err
	}
	preimages := firstDomainRows(preimageResults)
	if len(preimages) == 0 {
		return servicecatalog.ServiceState{}, ErrNotFound
	}
	if len(preimages) != 1 {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	state, err := decodeServiceStateV3Preimage(
		repository,
		serviceKey,
		snapshotRevision,
		snapshotDigest,
		preimages[0],
	)
	if err != nil {
		return servicecatalog.ServiceState{}, err
	}
	return state, nil
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

// ListServiceStateV3RowsSnapshot merges the unchanged current subset with the
// exact sparse preimages written for one selected revision.
func (s *Surreal) ListServiceStateV3RowsSnapshot(
	ctx context.Context,
	repository, after string,
	limit int,
	snapshotRevision uint64,
	snapshotDigest string,
) ([]servicecatalog.ServiceState, error) {
	if validateCandidateRepository(repository) != nil || limit < 1 ||
		limit > maxServiceStateScanPage+1 || snapshotRevision == 0 ||
		snapshotRevision > math.MaxInt64 || !validSHA256Digest(snapshotDigest) {
		return nil, ErrInvalidServiceStateV3
	}
	currentResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_current
	WHERE repository = $repository AND service_key > $after
		AND visible_from <= $snapshot_revision
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": repository, "after": after, "limit": limit,
		"snapshot_revision": snapshotRevision,
	})
	if err != nil {
		return nil, err
	}
	current, err := decodeServiceStateV3Rows(
		repository,
		firstDomainRows(currentResults),
		limit,
	)
	if err != nil {
		return nil, err
	}
	preimageResults, err := surrealdb.Query[[]serviceStateRec](ctx, s.db, `
SELECT * FROM service_state_v3_preimage
	WHERE repository = $repository AND service_key > $after
		AND snapshot_revision = $snapshot_revision
		AND snapshot_digest = $snapshot_digest
	ORDER BY service_key LIMIT $limit`, map[string]any{
		"repository": repository, "after": after, "limit": limit,
		"snapshot_revision": snapshotRevision, "snapshot_digest": snapshotDigest,
	})
	if err != nil {
		return nil, err
	}
	preimages, err := decodeServiceStateV3PreimageRows(
		repository,
		snapshotRevision,
		snapshotDigest,
		firstDomainRows(preimageResults),
		limit,
	)
	if err != nil {
		return nil, err
	}
	return mergeServiceStateV3SnapshotRows(current, preimages, limit)
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

func decodeServiceStateV3PreimageRows(
	repository string,
	snapshotRevision uint64,
	snapshotDigest string,
	rows []serviceStateRec,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	if len(rows) > limit {
		return nil, ErrInvalidServiceStateV3
	}
	states := make([]servicecatalog.ServiceState, 0, len(rows))
	prior := ""
	for _, row := range rows {
		state, err := decodeServiceStateV3Preimage(
			repository,
			row.ServiceKey,
			snapshotRevision,
			snapshotDigest,
			row,
		)
		if err != nil || state.ServiceKey <= prior {
			return nil, ErrInvalidServiceStateV3
		}
		prior = state.ServiceKey
		states = append(states, state)
	}
	return states, nil
}

func decodeServiceStateV3Preimage(
	repository, serviceKey string,
	snapshotRevision uint64,
	snapshotDigest string,
	row serviceStateRec,
) (servicecatalog.ServiceState, error) {
	state, err := serviceStateV3FromRec(row)
	if err != nil || state.Repository != repository || state.ServiceKey != serviceKey ||
		row.VisibleFrom == 0 || row.VisibleFrom > snapshotRevision ||
		row.SnapshotRevision != snapshotRevision ||
		row.SnapshotDigest != snapshotDigest ||
		!validServiceStateV3PreimageRecord(row.RecID, "service_state_v3_preimage") {
		return servicecatalog.ServiceState{}, ErrInvalidServiceStateV3
	}
	return cloneServiceStateV3(*state), nil
}

func validServiceStateV3PreimageRecord(record *models.RecordID, table string) bool {
	if record == nil || record.Table != table {
		return false
	}
	identifier, ok := record.ID.(string)
	return ok && identifier != ""
}

func mergeServiceStateV3SnapshotRows(
	current, preimages []servicecatalog.ServiceState,
	limit int,
) ([]servicecatalog.ServiceState, error) {
	rows := make([]servicecatalog.ServiceState, 0, min(limit, len(current)+len(preimages)))
	currentIndex := 0
	preimageIndex := 0
	for len(rows) < limit &&
		(currentIndex < len(current) || preimageIndex < len(preimages)) {
		switch {
		case preimageIndex == len(preimages):
			rows = append(rows, current[currentIndex])
			currentIndex++
		case currentIndex == len(current):
			rows = append(rows, preimages[preimageIndex])
			preimageIndex++
		case current[currentIndex].ServiceKey < preimages[preimageIndex].ServiceKey:
			rows = append(rows, current[currentIndex])
			currentIndex++
		case current[currentIndex].ServiceKey > preimages[preimageIndex].ServiceKey:
			rows = append(rows, preimages[preimageIndex])
			preimageIndex++
		default:
			if current[currentIndex].StateDigest != preimages[preimageIndex].StateDigest ||
				current[currentIndex].ControlRevision !=
					preimages[preimageIndex].ControlRevision {
				return nil, ErrInvalidServiceStateV3
			}
			rows = append(rows, preimages[preimageIndex])
			currentIndex++
			preimageIndex++
		}
	}
	return rows, nil
}

func sameServiceStateV3Fence(
	pointer ServiceCatalogV3Pointer,
	summary servicecatalog.RepositoryState,
) bool {
	return pointer.Repository == summary.Repository &&
		pointer.RootDigest == summary.CatalogGeneration &&
		pointer.ControlRevision == summary.CatalogControlRevision
}

func serviceStateV3SelectorPointer(
	selector ServiceRuntimeSelector,
) (ServiceCatalogV3Pointer, error) {
	if validateServiceRuntimeSelector(selector) != nil ||
		selector.Backend != ServiceRuntimeV3 {
		return ServiceCatalogV3Pointer{}, ErrInvalidServiceRuntimeSelector
	}
	return ServiceCatalogV3Pointer{
		Repository: selector.Repository, RootDigest: selector.CatalogRootDigest,
		ControlRevision: selector.CatalogControlRevision,
		PublishedAt:     selector.ChangedAt,
	}, nil
}

func sameSelectedServiceStateV3Fence(
	selector ServiceRuntimeSelector,
	summary servicecatalog.RepositoryState,
) bool {
	return selector.Backend == ServiceRuntimeV3 &&
		selector.Repository == summary.Repository &&
		selector.CatalogRootDigest == summary.CatalogGeneration &&
		selector.CatalogControlRevision == summary.CatalogControlRevision &&
		selector.StateControlRevision == summary.ControlRevision &&
		selector.StateSummaryDigest == summary.SummaryDigest
}

func sameServiceStateV3Summary(
	expected, current servicecatalog.RepositoryState,
) bool {
	return expected.Repository == current.Repository &&
		expected.CatalogGeneration == current.CatalogGeneration &&
		expected.CatalogControlRevision == current.CatalogControlRevision &&
		expected.ControlRevision == current.ControlRevision &&
		expected.SummaryDigest == current.SummaryDigest
}

func cloneServiceRuntimeSelectorPointer(
	selector *ServiceRuntimeSelector,
) *ServiceRuntimeSelector {
	if selector == nil {
		return nil
	}
	cloned := *selector
	return &cloned
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
