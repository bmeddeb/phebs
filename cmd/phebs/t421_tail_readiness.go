package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/store"
)

const t421TailReadinessSchema = "t421-tail-readiness-source-free-v1"

type t421TailReadinessResponse struct {
	Schema                       string `json:"schema"`
	Status                       string `json:"status"`
	SelectedRuntimeSHA256        string `json:"selected_runtime_sha256,omitempty"`
	RelationshipGenerationSHA256 string `json:"relationship_generation_sha256,omitempty"`
	RelationshipRootSHA256       string `json:"relationship_root_sha256,omitempty"`
	CallerGenerationSHA256       string `json:"caller_generation_sha256,omitempty"`
	CallerRootSHA256             string `json:"caller_root_sha256,omitempty"`
}

func t421TailReadinessLimits() readaccounting.Counts {
	return readaccounting.Counts{ControlFileReads: 4, StoreReadAttempts: 4}
}

func (reader *t421FinalAuthorityReader) ReadTailReadiness(
	ctx context.Context,
) ([]byte, func() error, error) {
	if reader == nil || ctx == nil {
		return nil, nil, errors.New("T42.1 tail-readiness reader is nil")
	}
	selector, err := reader.store.GetServiceRuntimeSelector(ctx, reader.repository)
	if err != nil {
		return t421TailReadinessResult(err)
	}
	if selector.Repository != reader.repository || selector.Backend != store.ServiceRuntimeV3 {
		return nil, nil, errors.New("T42.1 tail-readiness selected runtime is invalid")
	}

	root := filepath.Join(reader.dataDir, "relationships")
	pointer, err := relationshippublication.ReadPointerV3(ctx, root, reader.repository)
	if err != nil {
		return t421TailReadinessResult(err)
	}
	if pointer.GenerationDigest != selector.RelationshipGenerationDigest ||
		pointer.RootDigest != selector.RelationshipRootDigest {
		return t421TailReadinessMarshal("pending", selector, relationshippublication.RootV3{}, nil)
	}
	relationship, pending, err := reader.readTailRelationship(ctx, root, pointer)
	if err != nil {
		return nil, nil, err
	}
	if pending {
		return t421TailReadinessMarshal("pending", selector, relationshippublication.RootV3{}, nil)
	}
	if !t421TailRelationshipMatchesSelector(selector, relationship) {
		return nil, nil, errors.New("T42.1 tail-readiness selected relationship is incoherent")
	}
	resolver, pending, err := reader.readTailResolver(ctx, root, pointer, relationship)
	if err != nil {
		return nil, nil, err
	}
	if pending {
		return t421TailReadinessMarshal("pending", selector, relationshippublication.RootV3{}, nil)
	}

	caller, err := reader.store.GetCallerGenerationPublicationSummary(ctx, reader.repository)
	if err != nil {
		return t421TailReadinessResult(err)
	}
	current, err := reader.store.CallerGenerationPublicationSummaryCurrent(ctx, *caller)
	if err != nil {
		return nil, nil, err
	}
	if !current || !t421TailCallerMatchesRelationship(*caller, relationship, resolver) {
		return t421TailReadinessMarshal("pending", selector, relationship, nil)
	}

	confirmed, err := relationshippublication.ReadPointerV3(ctx, root, reader.repository)
	if err != nil {
		return t421TailReadinessResult(err)
	}
	if confirmed != pointer {
		return t421TailReadinessMarshal("pending", selector, relationship, nil)
	}
	if err := reader.store.ConfirmServiceRuntimeSelector(ctx, selector); err != nil {
		return t421TailReadinessResult(err)
	}
	return t421TailReadinessMarshal("ready", selector, relationship, caller)
}

func (reader *t421FinalAuthorityReader) readTailResolver(
	ctx context.Context,
	relationshipRoot string,
	pointer relationshippublication.PointerV3,
	relationship relationshippublication.RootV3,
) (resolvernamespace.Root, bool, error) {
	authority := relationship.Authority
	publication, err := resolvernamespace.OpenGeneration(
		ctx, filepath.Join(reader.dataDir, "relationship-resolver-namespaces"),
		reader.repository, authority.ResolverGenerationDigest, authority.ResolverRootDigest,
	)
	if err == nil {
		return publication.Root(), false, nil
	}
	if !errors.Is(err, resolvernamespace.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
		return resolvernamespace.Root{}, false, err
	}
	confirmed, confirmErr := relationshippublication.ReadPointerV3(
		ctx, relationshipRoot, reader.repository,
	)
	if confirmErr != nil {
		if t421TailReadinessPollable(confirmErr) {
			return resolvernamespace.Root{}, true, nil
		}
		return resolvernamespace.Root{}, false, confirmErr
	}
	if confirmed != pointer {
		return resolvernamespace.Root{}, true, nil
	}
	return resolvernamespace.Root{}, false, resolvernamespace.ErrNotFound
}

func (reader *t421FinalAuthorityReader) readTailRelationship(
	ctx context.Context,
	root string,
	pointer relationshippublication.PointerV3,
) (relationshippublication.RootV3, bool, error) {
	publication, err := relationshippublication.OpenGenerationV3(
		ctx, root, reader.repository, pointer.GenerationDigest, pointer.RootDigest,
	)
	if err == nil {
		return publication.Root(), false, nil
	}
	if !errors.Is(err, relationshippublication.ErrNotFound) {
		return relationshippublication.RootV3{}, false, err
	}
	// Retirement may remove the generation after the first pointer read. It
	// is a healthy tail only if a second pointer proves that authority moved;
	// an unchanged pointer naming absent immutable bytes remains corruption.
	confirmed, confirmErr := relationshippublication.ReadPointerV3(
		ctx, root, reader.repository,
	)
	if confirmErr != nil {
		if t421TailReadinessPollable(confirmErr) {
			return relationshippublication.RootV3{}, true, nil
		}
		return relationshippublication.RootV3{}, false, confirmErr
	}
	if confirmed != pointer {
		return relationshippublication.RootV3{}, true, nil
	}
	return relationshippublication.RootV3{}, false, err
}

func t421TailReadinessResult(err error) ([]byte, func() error, error) {
	if t421TailReadinessPollable(err) {
		return t421TailReadinessMarshal(
			"pending", store.ServiceRuntimeSelector{}, relationshippublication.RootV3{}, nil,
		)
	}
	return nil, nil, err
}

func t421TailReadinessPollable(err error) bool {
	return errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) ||
		errors.Is(err, relationshippublication.ErrNotFound) ||
		errors.Is(err, relationshippublication.ErrPublishing)
}

func t421TailCallerMatchesRelationship(
	caller store.CallerGenerationPublicationSummary,
	relationship relationshippublication.RootV3,
	resolver resolvernamespace.Root,
) bool {
	return t421CallerResolverMatches(caller.Generation, relationship, resolver)
}

func t421TailRelationshipMatchesSelector(
	selector store.ServiceRuntimeSelector,
	relationship relationshippublication.RootV3,
) bool {
	authority := relationship.Authority
	return authority.Repository == selector.Repository &&
		relationship.GenerationDigest == selector.RelationshipGenerationDigest &&
		relationship.Digest == selector.RelationshipRootDigest &&
		authority.CatalogRootDigest == selector.CatalogRootDigest &&
		authority.CatalogControlRevision == selector.CatalogControlRevision &&
		authority.ServiceStateSummaryDigest == selector.StateSummaryDigest &&
		authority.ServiceStateControlRevision == selector.StateControlRevision
}

func t421CallerResolverMatches(
	generation store.CallerGenerationIdentity,
	relationship relationshippublication.RootV3,
	resolver resolvernamespace.Root,
) bool {
	authority := relationship.Authority
	return resolver.Authority.Repository == authority.Repository &&
		generation.Repository == authority.Repository &&
		generation.ResolverGenerationDigest == resolver.Authority.ResolverGenerationDigest &&
		generation.ResolverManifestDigest == resolver.Authority.ResolverManifestDigest
}

func t421TailReadinessMarshal(
	status string,
	selector store.ServiceRuntimeSelector,
	relationship relationshippublication.RootV3,
	caller *store.CallerGenerationPublicationSummary,
) ([]byte, func() error, error) {
	value := t421TailReadinessResponse{Schema: t421TailReadinessSchema, Status: status}
	switch status {
	case "pending":
	case "ready":
		if caller == nil || selector.Digest == "" || relationship.GenerationDigest == "" ||
			relationship.Digest == "" || caller.Generation.Digest == "" || caller.ManifestDigest == "" {
			return nil, nil, errors.New("T42.1 ready tail identity is incomplete")
		}
		value.SelectedRuntimeSHA256 = selector.Digest
		value.RelationshipGenerationSHA256 = relationship.GenerationDigest
		value.RelationshipRootSHA256 = relationship.Digest
		value.CallerGenerationSHA256 = caller.Generation.Digest
		value.CallerRootSHA256 = caller.ManifestDigest
	default:
		return nil, nil, errors.New("T42.1 tail-readiness status is invalid")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	return append(raw, '\n'), nil, nil
}
