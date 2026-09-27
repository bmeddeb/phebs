package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// This prospective adapter is deliberately not installed by serve. Its sole
// path comes from trusted installation configuration, never a navigation query.
// Every open re-resolves exact current custody; no binding→authority map survives.
type typedCodeNavigationResolver struct {
	store     *store.Surreal
	workspace string
}

func newTypedCodeNavigationResolver(s *store.Surreal, workspace string) (*typedCodeNavigationResolver, error) {
	if s == nil || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return nil, codenav.ErrTypedIndexBinding
	}
	return &typedCodeNavigationResolver{s, workspace}, nil
}
func typedNavigationHash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}
func typedNavigationAuthority(c store.TypedIndexCurrentCustody) (typedworkspace.RoutedAuthority, codenav.RoutedBinding, error) {
	id := typedworkspace.OwnerIdentity{PlanningDigest: c.PlanningDigest, AttemptDigest: c.AttemptDigest, ChunkIdentity: c.ChunkIdentity, LeaseDigest: c.LeaseDigest, Request: c.Parent.Request()}
	a := typedworkspace.RoutedAuthority{Owner: id, ManifestDigest: c.Custody.ManifestDigest, DirectoryDevice: c.Custody.DirectoryDevice, DirectoryInode: c.Custody.DirectoryInode, PublicationReceiptDigest: c.Custody.PublicationReceiptDigest, Parent: c.Parent, Execution: c.Admission, RootDigest: c.Pointer.RootDigest}
	identity := a.Identity()
	if identity == "" || c.Pointer.Epoch == 0 || c.Custody.Revision != 3 || c.Custody.PlanningDigest != c.PlanningDigest || c.Custody.AttemptDigest != c.AttemptDigest || c.Custody.PublicationRootDigest != c.Pointer.RootDigest || c.Custody.PublicationRequestDigest != c.Admission.Digest() || c.Custody.PublicationPlanDigest != c.Admission.Request().PlanDigest {
		return typedworkspace.RoutedAuthority{}, codenav.RoutedBinding{}, codenav.ErrTypedIndexBinding
	}
	b := codenav.RoutedBinding{Selected: true, Identity: typedNavigationHash(struct {
		Custody string
		Epoch   uint64
	}{identity, c.Pointer.Epoch}), RootDigest: c.Pointer.RootDigest, Source: c.Parent.Request().Source}
	return a, b, nil
}
func (r *typedCodeNavigationResolver) ResolveRoutedIndex(ctx context.Context, repository, revision string) (codenav.RoutedBinding, error) {
	intent, err := r.store.ReadTypedIndexIntent(ctx, repository)
	if errors.Is(err, store.ErrNotFound) {
		return codenav.RoutedBinding{}, nil
	}
	if err != nil {
		return codenav.RoutedBinding{}, err
	}
	unavailable := codenav.RoutedBinding{Selected: true, Identity: typedNavigationHash(intent)}
	current, err := r.store.ReadTypedIndexCurrentCustody(ctx, repository)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, typedindex.Stale) || errors.Is(err, typedindex.Disabled) || errors.Is(err, typedindex.Unprepared) {
		return unavailable, nil
	}
	if err != nil {
		return codenav.RoutedBinding{}, err
	}
	_, binding, err := typedNavigationAuthority(current)
	if err != nil {
		return codenav.RoutedBinding{}, err
	}
	if binding.Source.Repository != repository || binding.Source.Commit != revision {
		return unavailable, nil
	}
	return binding, nil
}
func (r *typedCodeNavigationResolver) OpenRoutedIndex(ctx context.Context, b codenav.RoutedBinding, cached codenav.RoutedMetadata) (codenav.RoutedReader, codenav.RoutedMetadata, error) {
	if !b.Selected || b.RootDigest == "" {
		return nil, nil, codenav.ErrTypedIndexBinding
	}
	current, err := r.store.ReadTypedIndexCurrentCustody(ctx, b.Source.Repository)
	if err != nil {
		return nil, nil, err
	}
	authority, actual, err := typedNavigationAuthority(current)
	if err != nil {
		return nil, nil, err
	}
	if actual != b {
		return nil, nil, codenav.ErrBindingChanged
	}
	var metadata *typedworkspace.RoutedMetadata
	if cached != nil {
		var ok bool
		metadata, ok = cached.(*typedworkspace.RoutedMetadata)
		if !ok {
			return nil, nil, codenav.ErrTypedIndexBinding
		}
	}
	p, err := typedworkspace.OpenRoutedPublication(ctx, r.workspace, authority, metadata)
	if err != nil {
		return nil, nil, err
	}
	return p, p.Metadata(), nil
}
