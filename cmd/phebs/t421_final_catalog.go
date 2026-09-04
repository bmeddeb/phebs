package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sync"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/t421catalogprojection"
)

const (
	t421FinalCatalogSourceSchema      = "t421-canonical-catalog-source-v1"
	t421FinalCatalogSemanticAuthority = "semantic-v1"
)

type t421FinalSetIdentity = t421catalogprojection.SetIdentity

type t421FinalCatalogSource struct {
	Schema  string `json:"schema"`
	Records uint64 `json:"records"`
	Bytes   uint64 `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type t421FinalCatalogProjection struct {
	CatalogLogicalSHA256 string                 `json:"catalog_logical_sha256"`
	SemanticSHA256       string                 `json:"semantic_sha256"`
	CatalogSource        t421FinalCatalogSource `json:"catalog_source"`
	Catalog              t421FinalSetIdentity   `json:"catalog"`
	MembershipSet        t421FinalSetIdentity   `json:"membership_set"`
	Placements           t421FinalSetIdentity   `json:"placements"`
	UnownedPrefixes      t421FinalSetIdentity   `json:"unowned_prefixes"`
	ServiceQueries       t421FinalSetIdentity   `json:"service_queries"`
}

type t421FinalCatalogSnapshot struct {
	Selector   store.ServiceRuntimeSelector
	Root       servicecatalogv3.Root
	Projection t421FinalCatalogProjection
}

// t421FinalCatalogCache is deliberately one slot and process-local. F's later
// outer authority pass owns final confirmation and commits a prepared miss;
// Q's shared catalog cache is never consulted or warmed here.
type t421FinalCatalogCache struct {
	mu         sync.Mutex
	valid      bool
	key        store.ServiceRuntimeSelector
	projection t421FinalCatalogProjection
}

type t421FinalCatalogPending struct {
	cache      *t421FinalCatalogCache
	key        store.ServiceRuntimeSelector
	projection t421FinalCatalogProjection
}

func (cache *t421FinalCatalogCache) prepare(
	ctx context.Context,
	source servicecatalogv3.ReadSource,
	selector store.ServiceRuntimeSelector,
	root servicecatalogv3.Root,
) (t421FinalCatalogSnapshot, *t421FinalCatalogPending, error) {
	if cache == nil || ctx == nil || source == nil ||
		!matchesT421FinalCatalogSelection(selector, root) {
		return t421FinalCatalogSnapshot{}, nil, store.ErrInvalidServiceRuntimeSelector
	}
	if err := ctx.Err(); err != nil {
		return t421FinalCatalogSnapshot{}, nil, err
	}
	cache.mu.Lock()
	if cache.valid && cache.key == selector {
		if err := ctx.Err(); err != nil {
			cache.mu.Unlock()
			return t421FinalCatalogSnapshot{}, nil, err
		}
		projection := cache.projection
		cache.mu.Unlock()
		if err := servicecatalogv3.ValidateRoot(root); err != nil {
			return t421FinalCatalogSnapshot{}, nil, err
		}
		if err := ctx.Err(); err != nil {
			return t421FinalCatalogSnapshot{}, nil, err
		}
		return t421FinalCatalogSnapshot{
			Selector: selector, Root: cloneT421FinalCatalogRoot(root), Projection: projection,
		}, nil, nil
	}
	cache.mu.Unlock()

	catalog, err := servicecatalogv3.ReadCatalogContext(ctx, source, root)
	if err != nil {
		return t421FinalCatalogSnapshot{}, nil, err
	}
	projection, err := projectT421FinalCatalog(ctx, root.LogicalDigest, catalog)
	if err != nil {
		return t421FinalCatalogSnapshot{}, nil, err
	}
	pending := &t421FinalCatalogPending{
		cache: cache, key: selector, projection: projection,
	}
	return t421FinalCatalogSnapshot{
		Selector: selector, Root: cloneT421FinalCatalogRoot(root), Projection: projection,
	}, pending, nil
}

// commitAfterFinalFence is called only after the whole F pass has reauthorized
// and confirmed every selected authority. Exact mode admits one active request,
// so a one-slot replacement needs no second fill coordinator.
func (pending *t421FinalCatalogPending) commitAfterFinalFence(ctx context.Context) error {
	if pending == nil {
		return nil
	}
	if ctx == nil || pending.cache == nil {
		return store.ErrInvalidServiceRuntimeSelector
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pending.cache.mu.Lock()
	defer pending.cache.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	pending.cache.valid = true
	pending.cache.key = pending.key
	pending.cache.projection = pending.projection
	return nil
}

// matchesT421FinalCatalogSelection checks only the supplied store-selected
// tuple. F's outer reader still owns the initial/final production validations.
func matchesT421FinalCatalogSelection(
	selector store.ServiceRuntimeSelector,
	root servicecatalogv3.Root,
) bool {
	return selector.Schema == store.ServiceRuntimeSelectorSchema &&
		selector.Backend == store.ServiceRuntimeV3 &&
		selector.Repository != "" && selector.Repository == root.Binding.Repository &&
		selector.CatalogGenerationDigest == "" &&
		selector.CatalogRootDigest == root.Digest &&
		selector.CatalogControlRevision != 0 && selector.StateControlRevision != 0 &&
		selector.StateSummaryDigest != "" && selector.SearchGenerationDigest != "" &&
		selector.RelationshipGenerationDigest != "" && selector.RelationshipRootDigest != "" &&
		selector.ControlRevision != 0 && selector.Digest != "" && !selector.ChangedAt.IsZero()
}

func projectT421FinalCatalog(
	ctx context.Context,
	catalogLogicalSHA256 string,
	catalog servicecatalog.Catalog,
) (t421FinalCatalogProjection, error) {
	if ctx == nil {
		return t421FinalCatalogProjection{}, servicecatalogv3.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return t421FinalCatalogProjection{}, err
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		return t421FinalCatalogProjection{}, err
	}
	semantic := catalog
	semantic.Authority.Version = t421FinalCatalogSemanticAuthority
	semanticSHA256, err := servicecatalogv3.NormalizedCatalogLogicalDigest(ctx, semantic)
	if err != nil {
		return t421FinalCatalogProjection{}, err
	}

	sets, err := t421catalogprojection.Derive(ctx, catalog)
	if err != nil {
		return t421FinalCatalogProjection{}, err
	}
	return t421FinalCatalogProjection{
		CatalogLogicalSHA256: catalogLogicalSHA256,
		SemanticSHA256:       semanticSHA256,
		CatalogSource: t421FinalCatalogSource{
			Schema:  t421FinalCatalogSourceSchema,
			Records: uint64(len(catalog.Services) + len(catalog.Memberships) + len(catalog.Unowned)),
			Bytes:   uint64(len(raw)), SHA256: t421FinalSHA256(raw),
		},
		Catalog: sets.Catalog, MembershipSet: sets.Memberships,
		Placements: sets.Placements, UnownedPrefixes: sets.UnownedPrefixes,
		ServiceQueries: sets.ServiceQueries,
	}, nil
}

func t421FinalSHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cloneT421FinalCatalogRoot(root servicecatalogv3.Root) servicecatalogv3.Root {
	root.ServiceMembers = slices.Clone(root.ServiceMembers)
	root.PlacementMembers = slices.Clone(root.PlacementMembers)
	if root.Binding.Override != nil {
		override := *root.Binding.Override
		root.Binding.Override = &override
	}
	return root
}
