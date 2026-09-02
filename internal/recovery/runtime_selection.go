package recovery

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/store"
)

// ValidateServiceRuntimeSelections fail-closes startup and backup/restore when
// any durable selector names an authority that can no longer be strict-opened.
func ValidateServiceRuntimeSelections(
	ctx context.Context,
	dataDir string,
	st *store.Surreal,
) error {
	selectors, err := st.ListServiceRuntimeSelectors(ctx)
	if err != nil {
		return fmt.Errorf("list service runtime selectors: %w", err)
	}
	return validateServiceRuntimeSelections(ctx, dataDir, st, selectors)
}

func validateServiceRuntimeSelections(
	ctx context.Context,
	dataDir string,
	st *store.Surreal,
	selectors []store.ServiceRuntimeSelector,
) error {
	for _, selector := range selectors {
		target := store.ServiceRuntimeTarget{
			CatalogGenerationDigest:      selector.CatalogGenerationDigest,
			CatalogRootDigest:            selector.CatalogRootDigest,
			CatalogControlRevision:       selector.CatalogControlRevision,
			StateControlRevision:         selector.StateControlRevision,
			StateSummaryDigest:           selector.StateSummaryDigest,
			SearchGenerationDigest:       selector.SearchGenerationDigest,
			RelationshipGenerationDigest: selector.RelationshipGenerationDigest,
			RelationshipRootDigest:       selector.RelationshipRootDigest,
		}
		if err := validateServiceRuntimeTarget(
			ctx, dataDir, st,
			selector.Repository, selector.Backend, target,
		); err != nil {
			return fmt.Errorf(
				"validate service runtime selector for %q: %w",
				selector.Repository, err,
			)
		}
		if err := st.ConfirmServiceRuntimeSelector(ctx, selector); err != nil {
			return fmt.Errorf(
				"confirm service runtime selector for %q: %w",
				selector.Repository, err,
			)
		}
	}
	return nil
}

// ValidateServiceRuntimeTarget strict-opens a prospective target before its
// selector CAS. Callers must hold the shared mutation/backup exclusion.
func ValidateServiceRuntimeTarget(
	ctx context.Context,
	dataDir string,
	st *store.Surreal,
	repository, backend string,
	target store.ServiceRuntimeTarget,
) error {
	return validateServiceRuntimeTarget(
		ctx, dataDir, st, repository, backend, target,
	)
}

func validateServiceRuntimeTarget(
	ctx context.Context,
	dataDir string,
	st *store.Surreal,
	repository, backend string,
	target store.ServiceRuntimeTarget,
) error {
	type relationshipComponents struct {
		resolverGeneration string
		resolverRoot       string
		rpcGeneration      string
		rpcRoot            string
		kafkaGeneration    string
		kafkaRoot          string
	}
	var components relationshipComponents
	relationshipRoot := filepath.Join(dataDir, "relationships")
	switch backend {
	case store.ServiceRuntimeV2:
		catalog, err := st.GetServiceCatalog(ctx, repository)
		if err != nil {
			return fmt.Errorf("open selected v2 catalog: %w", err)
		}
		if catalog.GenerationDigest != target.CatalogGenerationDigest ||
			catalog.ControlRevision != target.CatalogControlRevision {
			return fmt.Errorf("selected v2 catalog changed: %w", store.ErrConflict)
		}
		summary, err := st.GetServiceStateSummary(ctx, repository)
		if err != nil {
			return fmt.Errorf("open selected v2 state: %w", err)
		}
		if summary.CatalogGeneration != target.CatalogGenerationDigest ||
			summary.CatalogControlRevision != target.CatalogControlRevision ||
			summary.ControlRevision != target.StateControlRevision ||
			summary.SummaryDigest != target.StateSummaryDigest {
			return fmt.Errorf("selected v2 state changed: %w", store.ErrConflict)
		}
		publication, err := relationshippublication.OpenGeneration(
			ctx, relationshipRoot, repository,
			target.RelationshipGenerationDigest,
			target.RelationshipRootDigest,
		)
		if err != nil {
			return fmt.Errorf("open selected v2 relationship root: %w", err)
		}
		authority := publication.Root().Authority
		if authority.CatalogGenerationDigest != target.CatalogGenerationDigest ||
			authority.ServiceStateControlRevision != target.StateControlRevision ||
			authority.ServiceStateSummaryDigest != target.StateSummaryDigest {
			return fmt.Errorf("selected v2 relationship authority changed: %w", store.ErrConflict)
		}
		components = relationshipComponents{
			resolverGeneration: authority.ResolverGenerationDigest,
			resolverRoot:       authority.ResolverRootDigest,
			rpcGeneration:      authority.RPCGenerationDigest,
			rpcRoot:            authority.RPCRootDigest,
			kafkaGeneration:    authority.KafkaGenerationDigest,
			kafkaRoot:          authority.KafkaRootDigest,
		}
	case store.ServiceRuntimeV3:
		catalog, err := st.ReadServiceCatalogV3Root(
			ctx, repository, target.CatalogRootDigest,
		)
		if err != nil {
			return fmt.Errorf("open selected v3 catalog: %w", err)
		}
		if catalog.Digest != target.CatalogRootDigest {
			return fmt.Errorf("selected v3 catalog changed: %w", store.ErrConflict)
		}
		summary, err := st.GetServiceStateV3SummaryPoint(ctx, repository)
		if err != nil {
			return fmt.Errorf("open selected v3 state: %w", err)
		}
		if summary.CatalogGeneration != target.CatalogRootDigest ||
			summary.CatalogControlRevision != target.CatalogControlRevision ||
			summary.ControlRevision != target.StateControlRevision ||
			summary.SummaryDigest != target.StateSummaryDigest {
			return fmt.Errorf("selected v3 state changed: %w", store.ErrConflict)
		}
		publication, err := relationshippublication.ValidateGenerationV3(
			ctx, relationshipRoot, repository,
			target.RelationshipGenerationDigest,
			target.RelationshipRootDigest,
		)
		if err != nil {
			return fmt.Errorf("open selected v3 relationship root: %w", err)
		}
		authority := publication.Root().Authority
		if authority.CatalogRootDigest != target.CatalogRootDigest ||
			authority.CatalogControlRevision != target.CatalogControlRevision ||
			authority.ServiceStateControlRevision != target.StateControlRevision ||
			authority.ServiceStateSummaryDigest != target.StateSummaryDigest {
			return fmt.Errorf("selected v3 relationship authority changed: %w", store.ErrConflict)
		}
		components = relationshipComponents{
			resolverGeneration: authority.ResolverGenerationDigest,
			resolverRoot:       authority.ResolverRootDigest,
			rpcGeneration:      authority.RPCGenerationDigest,
			rpcRoot:            authority.RPCRootDigest,
			kafkaGeneration:    authority.KafkaGenerationDigest,
			kafkaRoot:          authority.KafkaRootDigest,
		}
	default:
		return fmt.Errorf("unsupported selected service runtime %q", backend)
	}
	if _, err := resolvernamespace.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-resolver-namespaces"),
		repository, components.resolverGeneration, components.resolverRoot,
	); err != nil {
		return fmt.Errorf("open selected relationship resolver evidence: %w", err)
	}
	if _, err := rpccallerposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-rpc-postings"),
		repository, components.rpcGeneration, components.rpcRoot,
	); err != nil {
		return fmt.Errorf("open selected relationship RPC evidence: %w", err)
	}
	if _, err := kafkatopicposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-kafka-postings"),
		repository, components.kafkaGeneration, components.kafkaRoot,
	); err != nil {
		return fmt.Errorf("open selected relationship Kafka evidence: %w", err)
	}
	if _, err := focusedindex.ValidateSearchGeneration(
		ctx, filepath.Join(dataDir, "index"), repository,
		target.SearchGenerationDigest,
	); err != nil {
		return fmt.Errorf("open selected search generation: %w", err)
	}
	if err := st.ValidateServiceRuntimeDatabaseTarget(
		ctx, repository, backend, target,
	); err != nil {
		return fmt.Errorf("confirm selected database target: %w", err)
	}
	return nil
}
