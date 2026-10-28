package t421

import (
	"github.com/bmeddeb/phebs/internal/recovery"
	"slices"
)

// Every state identity is independently inventoried by the native archive
// owner. The manifest describes component transport bytes, not restored state.
//
//nolint:unused // Darwin execution flow only; unused in Linux builds.
type executionArchiveReceiptObservation struct {
	Manifest                                                       recovery.ArchiveTransitionManifest
	StateInventories                                               [3]SetIdentity
	Before, After                                                  executionArchiveSemanticObservation
	InstallationDestroyed, RestoreTargetEmpty, ScratchSourceAbsent bool
}
type executionArchiveSemanticObservation struct {
	Projection       PhaseStateProjection
	CallerLeaves     []CallerPublicationLeafResult
	CallerProjection SetIdentity
}

//nolint:unused // Darwin execution flow only; unused in Linux builds.
func composeExecutionArchiveTransition(plan Plan, measurement PhaseMeasurement, authority map[string]AuthorityPhaseResult,
	observed executionArchiveReceiptObservation, events map[string]uint64,
) (ArchiveTransition, error) {
	refuse := func() (ArchiveTransition, error) { return ArchiveTransition{}, errExecutionReceiptTransition }
	if !processAccountingPlanSemantics(plan.Schema) || measurement.Phase != "archive_restore" || validateEpochArchiveManifest(observed.Manifest) != nil ||
		!observed.InstallationDestroyed || !observed.RestoreTargetEmpty || !observed.ScratchSourceAbsent {
		return refuse()
	}
	before, after := authority["pressure_75"], authority["archive_restore"]
	if before.Outcome != "passed" || after.Outcome != "passed" {
		return refuse()
	}
	value := ArchiveTransition{Schema: plan.ReceiptContract.TransitionSchema + "/archive-v1",
		ManifestSchema: observed.Manifest.ManifestSchema, ManifestSHA256: observed.Manifest.ManifestSHA256,
		InventoryCanonicalization: archiveInventoryCanonicalization,
		StateInventoryBefore:      observed.StateInventories[0], StateInventoryArchived: observed.StateInventories[1], StateInventoryAfter: observed.StateInventories[2],
		RelationshipGenerationBefore: before.RelationshipGenerationSHA256, RelationshipGenerationAfter: after.RelationshipGenerationSHA256,
		RelationshipRootBefore: before.RelationshipRootSHA256, RelationshipRootAfter: after.RelationshipRootSHA256,
		RelationshipRuntimeIdentityDisposition: relationshipRuntimeIdentityDisposition(before, after),
		ArchiveCreatedEventOrdinal:             events["archive:created"], InstallationDestroyedEventOrdinal: events["archive:installation-destroyed"],
		EmptyRestoreTargetEventOrdinal: events["archive:empty-target"], RestoreStartedEventOrdinal: events["archive:restore-started"], ComparisonEventOrdinal: events["archive:comparison"],
	}
	for _, component := range observed.Manifest.Components {
		if component.Bytes > ^uint64(0)-value.ArchiveBytes {
			return refuse()
		}
		value.ArchiveBytes += component.Bytes
		value.Components = append(value.Components, ArchiveComponent(component))
	}
	// The native backup is a directory of components. Its committed manifest
	// binds their exact hashes; it is the archive identity, not an outer tar.
	value.ArchiveSHA256 = observed.Manifest.ManifestSHA256
	for _, report := range observed.Manifest.Reports {
		value.Reports = append(value.Reports, ArchiveReportProjection{Name: report.Name, Schema: report.Schema, Publications: report.Publications,
			V1Publications: report.V1Publications, V2Publications: report.V2Publications, Files: report.Files, Bytes: report.Bytes})
	}
	var err error
	value.ManifestInventory, err = archiveManifestInventory(value.Components)
	if err != nil {
		return refuse()
	}
	value.ReportInventory, err = archiveReportInventory(value.Reports)
	if err != nil {
		return refuse()
	}
	value.AuthoritySnapshotBeforeSHA256, err = authoritySnapshotSHA256(before.AuthorityState)
	if err != nil {
		return refuse()
	}
	value.AuthoritySnapshotAfterSHA256, err = authoritySnapshotSHA256(after.AuthorityState)
	if err != nil {
		return refuse()
	}
	var beforeRelationships, afterRelationships string
	value.PreRestoreStateSHA256, beforeRelationships, err = executionArchiveSemanticDigests(plan, observed.Before)
	if err != nil {
		return refuse()
	}
	value.RestoredStateSHA256, afterRelationships, err = executionArchiveSemanticDigests(plan, observed.After)
	if err != nil || beforeRelationships != afterRelationships {
		return refuse()
	}
	value.RelationshipSemanticSHA256 = beforeRelationships
	value.ArchiveBindingSHA256, err = archiveBindingSHA256(value)
	if err != nil {
		return refuse()
	}
	if validateArchiveTransition(value, measurement.StartEventOrdinal, measurement.FinishEventOrdinal, authority, plan) != nil {
		return refuse()
	}
	return value, nil
}

func executionArchiveSemanticDigests(plan Plan, observed executionArchiveSemanticObservation) (string, string, error) {
	p := observed.Projection
	if (p.Phase != "pressure_75" && p.Phase != "archive_restore") || !validSetIdentity(observed.CallerProjection) || len(observed.CallerLeaves) == 0 {
		return "", "", errExecutionReceiptTransition
	}
	// Policies and generation seed labels describe the frozen canonicalization.
	// Every measured count and inventory below comes from the actual F snapshot.
	product := ProductRelationships{RPCProjections: p.ProductRelationship.RPCProjections, KafkaProducerProjections: p.ProductRelationship.KafkaProducerProjections,
		KafkaConsumerProjections: p.ProductRelationship.KafkaConsumerProjections, TotalProjections: p.ProductRelationship.TotalProjections,
		ServiceReferences: p.ProductRelationship.ServiceReferences, Canonicalization: p.ProductRelationship.Canonicalization,
		KafkaPairOraclePosture: plan.Oracle.ProductRelationships.KafkaPairOraclePosture, GlobalCallerPolicy: plan.Oracle.ProductRelationships.GlobalCallerPolicy,
		ExpectedRPCProjections: observed.CallerProjection,
		ExpectedProjections:    SetIdentity{Records: p.ProductRelationship.ProjectionRecords, FramedBytes: p.ProductRelationship.ProjectionFramedBytes, SHA256: p.ProductRelationship.ProjectionSHA256}}
	for _, leaf := range observed.CallerLeaves {
		if leaf.CandidateRecords > ^uint64(0)-product.CallerCandidateRecords {
			return "", "", errExecutionReceiptTransition
		}
		product.CallerCandidateRecords += leaf.CandidateRecords
		product.CallerLeaves = append(product.CallerLeaves, CallerLeafProfile{Prefix: leaf.Prefix, CandidateRecords: leaf.CandidateRecords, ResolvedPostings: leaf.ResolvedPostings,
			Abstentions: leaf.Abstentions, Records: leaf.Records, CanonicalBytes: leaf.CanonicalBytes, EncodedBytes: leaf.EncodedBytes})
	}
	families := make([]RelationshipFamily, len(p.RelationshipResults))
	for i, row := range p.RelationshipResults {
		index := slices.IndexFunc(plan.Oracle.Relationships, func(f RelationshipFamily) bool { return f.Name == row.Name })
		if index < 0 {
			return "", "", errExecutionReceiptTransition
		}
		metadata := plan.Oracle.Relationships[index]
		families[i] = RelationshipFamily{Name: row.Name, Seed: metadata.Seed, Protocols: slices.Clone(metadata.Protocols), SemanticPairEdges: row.SemanticPairEdges,
			MaxInDegree: row.MaxInDegree, MaxOutDegree: row.MaxOutDegree, Acyclic: row.Acyclic,
			ExpectedEdges: SetIdentity{Records: row.SemanticPairEdges, FramedBytes: row.ObservedEdgesFramedBytes, SHA256: row.ObservedEdgesSHA256}}
	}
	semantic, err := receiptSHA256(struct {
		Schema               string               `json:"schema"`
		PhysicalRevision     string               `json:"physical_revision"`
		LogicalRevision      string               `json:"logical_revision"`
		CatalogLogicalSHA256 string               `json:"catalog_logical_sha256"`
		SemanticSHA256       string               `json:"semantic_sha256"`
		CatalogSource        CatalogSourceProfile `json:"catalog_source"`
		Catalog              SetIdentity          `json:"catalog"`
		Memberships          SetIdentity          `json:"memberships"`
		Placements           SetIdentity          `json:"placements"`
		UnownedPrefixes      SetIdentity          `json:"unowned_prefixes"`
		Relationships        ProductRelationships `json:"relationships"`
	}{"t422-archive-semantic-state-v1", p.PhysicalRevision, p.LogicalRevision, p.CatalogLogicalSHA256, p.SemanticSHA256, p.CatalogSource, p.Catalog, p.MembershipSet, p.Placements, p.UnownedPrefixes, product})
	if err != nil {
		return "", "", err
	}
	relationship, err := receiptSHA256(struct {
		Schema   string               `json:"schema"`
		Families []RelationshipFamily `json:"families"`
		Product  ProductRelationships `json:"product"`
	}{"t422-relationship-semantic-state-v1", families, product})
	return semantic, relationship, err
}
