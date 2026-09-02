package t4110

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type queryResult struct {
	Cost       PhaseCost
	Queries    int
	Matches    int
	Missing    int
	Unexpected int
}

func verifySelectedService(
	ctx context.Context,
	state *store.Surreal,
	selector store.ServiceRuntimeSelector,
	catalog servicecatalog.Catalog,
	serviceKey string,
	wantIncarnation uint64,
) (PhaseCost, error) {
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := store.NewServiceStateV3Reader(state, cache)
	if err != nil {
		return PhaseCost{}, err
	}
	read, err := reader.OpenServiceSelected(ctx, selector, serviceKey)
	if err != nil {
		return PhaseCost{}, fmt.Errorf("open exact selected service: %w", err)
	}
	defer read.Close()
	if read.Pointer.RootDigest != selector.CatalogRootDigest ||
		read.Pointer.ControlRevision != selector.CatalogControlRevision ||
		read.Root.Digest != selector.CatalogRootDigest ||
		read.Summary.ControlRevision != selector.StateControlRevision ||
		read.Summary.SummaryDigest != selector.StateSummaryDigest ||
		read.Entry.State.ServiceKey != serviceKey ||
		wantIncarnation != 0 && read.Entry.State.Incarnation != wantIncarnation {
		return PhaseCost{}, errors.New("selected service authority differs from its selector")
	}
	var expected *servicecatalog.Service
	for index := range catalog.Services {
		if catalog.Services[index].Key == serviceKey {
			expected = &catalog.Services[index]
			break
		}
	}
	if expected == nil {
		if !read.Entry.State.Removed || read.Entry.State.Status != servicecatalog.StatusRemoved ||
			read.Entry.Projection != nil {
			return PhaseCost{}, errors.New("selected removed service is not an exact tombstone")
		}
	} else {
		expectedMemberships := make([]servicecatalog.Membership, 0)
		for _, membership := range catalog.Memberships {
			if membership.ServiceKey == serviceKey {
				expectedMemberships = append(expectedMemberships, membership)
			}
		}
		row := read.Entry.State
		wantRemoved := expected.Disposition == servicecatalog.DispositionRejected
		wantStatus := map[string]string{
			servicecatalog.DispositionAccepted: servicecatalog.StatusCurrent,
			servicecatalog.DispositionProposal: servicecatalog.StatusUnavailable,
			servicecatalog.DispositionConflict: servicecatalog.StatusConflict,
			servicecatalog.DispositionRejected: servicecatalog.StatusRemoved,
		}[expected.Disposition]
		if wantStatus == "" || read.Entry.Projection == nil ||
			read.Entry.Projection.Removed != wantRemoved ||
			!reflect.DeepEqual(read.Entry.Projection.Service, *expected) ||
			!reflect.DeepEqual(read.Entry.Projection.Memberships, expectedMemberships) ||
			row.Removed != wantRemoved || row.Status != wantStatus ||
			row.DisplayName != expected.DisplayName || row.Disposition != expected.Disposition ||
			row.Origin != expected.Origin || row.Reason != expected.Reason ||
			!reflect.DeepEqual(row.Successors, expected.Successors) {
			return PhaseCost{}, errors.New("selected service values differ from the exact catalog")
		}
	}
	if err := reader.Confirm(ctx, read); err != nil {
		return PhaseCost{}, fmt.Errorf("confirm exact selected service: %w", err)
	}
	read.Close()
	stats := cache.Stats()
	if stats.RootLeases != 0 || stats.MemberLeases != 0 {
		return PhaseCost{}, errors.New("exact selected service retained cache leases")
	}
	return PhaseCost{
		SelectedStateRootReads:         stats.RootReads,
		SelectedStateMemberReads:       stats.MemberReads,
		SelectedStateRootValidations:   stats.RootValidations,
		SelectedStateMemberValidations: stats.MemberValidations,
		ProductQueries:                 1,
	}, nil
}

func querySelectedServices(
	ctx context.Context,
	state *store.Surreal,
	selector store.ServiceRuntimeSelector,
	serviceKeys []string,
	includePage bool,
) (queryResult, error) {
	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := store.NewServiceStateV3Reader(state, cache)
	if err != nil {
		return queryResult{}, err
	}
	var result queryResult
	for _, serviceKey := range serviceKeys {
		read, readErr := reader.OpenServiceSelected(ctx, selector, serviceKey)
		result.Queries++
		result.Cost.ProductQueries++
		if readErr != nil {
			if errors.Is(readErr, store.ErrNotFound) {
				result.Missing++
				continue
			}
			return queryResult{}, fmt.Errorf("open selected service: %w", readErr)
		}
		matches := read.Entry.State.ServiceKey == serviceKey &&
			read.Entry.Projection != nil && read.Entry.Projection.Service.Key == serviceKey
		if matches {
			result.Matches++
		} else {
			result.Unexpected++
		}
		if err := reader.Confirm(ctx, read); err != nil {
			read.Close()
			return queryResult{}, fmt.Errorf("confirm selected service: %w", err)
		}
		read.Close()
	}
	if includePage {
		page, err := reader.ListServicesSelected(
			ctx,
			selector,
			store.ServiceStateFilter{},
			store.ServiceStatePosition{},
			store.MaxServiceStateReadPage,
		)
		result.Cost.ProductQueries++
		if err != nil {
			return queryResult{}, fmt.Errorf("list selected services: %w", err)
		}
		if len(page.Entries) == 0 {
			page.Close()
			return queryResult{}, errors.New("selected service page is empty")
		}
		if err := reader.ConfirmPage(ctx, page); err != nil {
			page.Close()
			return queryResult{}, fmt.Errorf("confirm selected service page: %w", err)
		}
		page.Close()
	}
	stats := cache.Stats()
	if stats.RootLeases != 0 || stats.MemberLeases != 0 {
		return queryResult{}, errors.New("selected service reader retained leases")
	}
	result.Cost.SelectedStateRootReads = stats.RootReads
	result.Cost.SelectedStateMemberReads = stats.MemberReads
	result.Cost.SelectedStateRootValidations = stats.RootValidations
	result.Cost.SelectedStateMemberValidations = stats.MemberValidations
	return result, nil
}
