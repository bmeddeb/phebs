package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/dossier"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

const maxInvestigationDossierBytes = dossier.MaxSealedBytes

// DossierScopeProjection is supplied by the same current authorization
// boundary used for source reads. VisibleUnitIDs must be a subset of the
// candidate units; additions are rejected.
type DossierScopeProjection struct {
	VisibilityProjectionID string
	VisibleUnitIDs         []string
}

type DossierScopeResolver func(
	context.Context,
	string,
	[]string,
) (DossierScopeProjection, error)

type DossierSigner struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

type DossierExportRequest struct {
	Principal              string
	SourceSnapshotID       string
	BaselineID             string
	DecisionID             string
	PackCardIdentities     []string
	SupportedClaims        []string
	Blockers               []string
	EligibilityResult      string
	FreshnessState         string
	ValidationState        string
	ReviewDueRule          string
	ExpiresAt              *time.Time
	HandlingClassification string
	PredecessorID          string
}

// InvestigationDossierService seals and reopens recipient-scoped dossiers.
// The scope resolver is mandatory: export must never reuse the creator's
// historic universe as if it were the recipient's current authorization.
type InvestigationDossierService struct {
	Store        *Surreal
	ResolveScope DossierScopeResolver
	Signer       DossierSigner
}

type investigationDossierRecord struct {
	ID               string    `json:"dossier_id"`
	InvestigationID  string    `json:"investigation_id"`
	Principal        string    `json:"principal"`
	SourceSnapshotID string    `json:"source_snapshot_id"`
	RevisionID       string    `json:"revision_id"`
	ArtifactID       string    `json:"artifact_id"`
	BaselineID       string    `json:"baseline_id,omitempty"`
	DecisionID       string    `json:"decision_id,omitempty"`
	PredecessorID    string    `json:"predecessor_id,omitempty"`
	RootDigest       string    `json:"root_digest"`
	SigningKeyID     string    `json:"signing_key_id"`
	SigningPublicKey string    `json:"signing_public_key"`
	Content          string    `json:"content"`
	CreatedAt        time.Time `json:"created_at"`
}

func dossierRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_dossier", id)
}

func normalizeDossierScope(
	candidates []string,
	projection DossierScopeProjection,
) (DossierScopeProjection, error) {
	if err := validateDomainString(
		"dossier visibility projection",
		projection.VisibilityProjectionID,
		true,
	); err != nil {
		return DossierScopeProjection{}, err
	}
	var err error
	projection.VisibleUnitIDs, err = canonicalStrings(
		"dossier visible unit",
		projection.VisibleUnitIDs,
	)
	if err != nil {
		return DossierScopeProjection{}, err
	}
	candidates, err = canonicalStrings("dossier candidate unit", candidates)
	if err != nil {
		return DossierScopeProjection{}, err
	}
	if !isStringSubset(projection.VisibleUnitIDs, candidates) {
		return DossierScopeProjection{}, errors.New(
			"dossier scope resolver returned a unit outside the candidate universe",
		)
	}
	return projection, nil
}

func canonicalDossierRequest(request DossierExportRequest) (DossierExportRequest, error) {
	if !validInvestigationPrincipal(request.Principal) ||
		!strings.HasPrefix(request.SourceSnapshotID, "ics_") {
		return DossierExportRequest{}, ErrNotFound
	}
	var err error
	request.PackCardIdentities, err = canonicalStrings(
		"dossier pack-card identity",
		request.PackCardIdentities,
	)
	if err != nil {
		return DossierExportRequest{}, err
	}
	request.SupportedClaims, err = canonicalStrings(
		"dossier supported claim",
		request.SupportedClaims,
	)
	if err != nil {
		return DossierExportRequest{}, err
	}
	request.Blockers, err = canonicalStrings("dossier blocker", request.Blockers)
	if err != nil {
		return DossierExportRequest{}, err
	}
	for name, value := range map[string]string{
		"freshness state":         request.FreshnessState,
		"validation state":        request.ValidationState,
		"review due rule":         request.ReviewDueRule,
		"handling classification": request.HandlingClassification,
		"eligibility result":      request.EligibilityResult,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return DossierExportRequest{}, err
		}
	}
	if request.ExpiresAt != nil {
		value := storeTimestamp(*request.ExpiresAt)
		request.ExpiresAt = &value
	}
	if request.BaselineID != "" && !validULID(request.BaselineID) {
		return DossierExportRequest{}, errors.New("invalid dossier baseline")
	}
	if request.DecisionID != "" && !validULID(request.DecisionID) {
		return DossierExportRequest{}, errors.New("invalid dossier decision")
	}
	if request.PredecessorID != "" && !validULID(request.PredecessorID) {
		return DossierExportRequest{}, errors.New("invalid predecessor dossier")
	}
	return request, nil
}

func newDossierEntry(path string, value any) (dossier.Entry, error) {
	return dossier.NewEntry(path, "application/json", value)
}

type dossierRevisionView struct {
	ID                 string   `json:"revision_id"`
	InvestigationID    string   `json:"investigation_id"`
	Seq                int      `json:"seq"`
	NormalizedQuestion string   `json:"normalized_question"`
	DecisionSought     string   `json:"decision_sought"`
	DeclaredUniverse   []string `json:"declared_universe"`
	SnapshotPolicy     string   `json:"snapshot_policy"`
	BuildConfiguration string   `json:"build_configuration"`
	PackSelection      string   `json:"pack_selection"`
	EnumerationMethod  string   `json:"enumeration_method"`
	Creator            string   `json:"creator"`
}

type dossierArtifactView struct {
	ID                   string   `json:"artifact_id"`
	RunID                string   `json:"run_id"`
	TerminalStatus       RunState `json:"terminal_status"`
	SnapshotManifest     string   `json:"snapshot_manifest"`
	InputManifest        string   `json:"input_manifest"`
	EligibilityResult    string   `json:"eligibility_result"`
	AuthorizedFactIDs    []string `json:"authorized_fact_ids"`
	VisibleUnitSetDigest string   `json:"visible_unit_set_digest"`
	VisibleUnitCount     int      `json:"visible_unit_count"`
	AnalysisComplete     bool     `json:"analysis_complete"`
	InputsFresh          bool     `json:"inputs_fresh"`
}

type dossierComparisonView struct {
	ID                     string          `json:"comparison_id"`
	Comparable             bool            `json:"comparable"`
	RuleID                 string          `json:"rule_id"`
	RuleVersion            string          `json:"rule_version"`
	RuleDigest             string          `json:"rule_digest"`
	NonComparabilityCauses []string        `json:"non_comparability_causes"`
	PerSideCoverageOnly    bool            `json:"per_side_coverage_only"`
	Deltas                 []ConsumerDelta `json:"deltas"`
}

type dossierSnapshotView struct {
	ID              string                    `json:"consumer_snapshot_id"`
	Sequence        int                       `json:"sequence"`
	InvestigationID string                    `json:"investigation_id"`
	RevisionID      string                    `json:"revision_id"`
	ArtifactID      string                    `json:"artifact_id"`
	SourceSnapshot  string                    `json:"source_snapshot"`
	Semantics       ConsumerSnapshotSemantics `json:"semantics"`
	Facts           []ConsumerEdgeFact        `json:"facts"`
	Comparison      dossierComparisonView     `json:"comparison"`
}

func redactedDossierFacts(
	facts []ConsumerEdgeFact,
	visibleUnits []string,
) []ConsumerEdgeFact {
	visible := make(map[string]struct{}, len(visibleUnits))
	for _, unit := range visibleUnits {
		visible[unit] = struct{}{}
	}
	result := make([]ConsumerEdgeFact, 0, len(facts))
	for _, fact := range facts {
		if _, ok := visible[fact.UnitID]; fact.UnitID != "" && ok {
			result = append(result, fact)
		}
	}
	return result
}

func redactedDossierComparison(
	comparison ConsumerComparison,
	facts []ConsumerEdgeFact,
) dossierComparisonView {
	visibleFactIDs := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		visibleFactIDs[fact.FactID] = struct{}{}
	}
	deltas := make([]ConsumerDelta, 0, len(comparison.Deltas))
	for _, delta := range comparison.Deltas {
		if delta.After == nil {
			continue
		}
		if _, ok := visibleFactIDs[delta.After.FactID]; !ok {
			continue
		}
		if delta.Before != nil {
			if _, ok := visibleFactIDs[delta.Before.FactID]; !ok {
				continue
			}
		}
		deltas = append(deltas, delta)
	}
	return dossierComparisonView{
		ID: comparison.ComparisonID, Comparable: comparison.Comparable,
		RuleID: comparison.RuleID, RuleVersion: comparison.RuleVersion,
		RuleDigest:             comparison.RuleDigest,
		NonComparabilityCauses: slices.Clone(comparison.NonComparabilityCauses),
		PerSideCoverageOnly:    comparison.PerSideCoverageOnly,
		Deltas:                 deltas,
	}
}

func dossierEvidenceReferences(
	investigationID, snapshotID string,
	facts []ConsumerEdgeFact,
) ([]dossier.EvidenceReference, []dossier.Entry, error) {
	result := make([]dossier.EvidenceReference, len(facts))
	entries := make([]dossier.Entry, len(facts))
	for i, fact := range facts {
		digest, err := dossier.DigestValue(
			"phebs-dossier-fact-reference-v1",
			fact,
		)
		if err != nil {
			return nil, nil, err
		}
		entry, err := newDossierEntry(
			"evidence/"+hashIdentity("finding_", fact.FactID)+".json",
			fact,
		)
		if err != nil {
			return nil, nil, err
		}
		entries[i] = entry
		result[i] = dossier.EvidenceReference{
			FactID: fact.FactID, FindingDigest: entry.Digest,
			FindingEntryPath: entry.Path, Mode: "authorized_locator",
			MaterialDigest: digest,
			AuthorizedLocator: "phebs://investigations/" +
				url.PathEscape(investigationID) +
				"/consumer-snapshots/" + url.PathEscape(snapshotID) +
				"/facts/" + url.PathEscape(fact.FactID),
		}
	}
	slices.SortFunc(result, func(left, right dossier.EvidenceReference) int {
		return strings.Compare(left.FactID, right.FactID)
	})
	slices.SortFunc(entries, func(left, right dossier.Entry) int {
		return strings.Compare(left.Path, right.Path)
	})
	return result, entries, nil
}

func dossierObjectReference(
	kind, id string,
	entry dossier.Entry,
) dossier.ObjectReference {
	return dossier.ObjectReference{
		Kind: kind, ID: id, ContentDigest: entry.Digest, EntryPath: entry.Path,
	}
}

func (service InvestigationDossierService) validate() error {
	if service.Store == nil || service.ResolveScope == nil {
		return errors.New("dossier store and scope resolver are required")
	}
	if service.Signer.KeyID == "" ||
		len(service.Signer.PrivateKey) != ed25519.PrivateKeySize {
		return errors.New("dossier Ed25519 signer is required")
	}
	return nil
}

func (service InvestigationDossierService) Export(
	ctx context.Context,
	request DossierExportRequest,
) (*dossier.Sealed, error) {
	if err := service.validate(); err != nil {
		return nil, err
	}
	request, err := canonicalDossierRequest(request)
	if err != nil {
		return nil, fmt.Errorf("export dossier: %w", err)
	}
	snapshot, err := service.Store.GetConsumerSnapshotAs(
		ctx,
		request.Principal,
		request.SourceSnapshotID,
	)
	if err != nil {
		return nil, err
	}
	investigation, err := service.Store.GetInvestigationAs(
		ctx,
		request.Principal,
		snapshot.Snapshot.InvestigationID,
	)
	if err != nil {
		return nil, err
	}
	authorization, err := service.Store.readInvestigationProjection(
		ctx,
		request.Principal,
		investigation.ID,
	)
	if err != nil {
		return nil, ErrNotFound
	}
	revision, err := service.Store.GetRevisionAs(
		ctx,
		request.Principal,
		snapshot.Snapshot.RevisionID,
	)
	if err != nil {
		return nil, err
	}
	artifact, err := service.Store.GetRunArtifactAs(
		ctx,
		request.Principal,
		snapshot.Snapshot.ArtifactID,
	)
	if err != nil {
		return nil, err
	}
	if artifact.TerminalStatus != RunPublished ||
		revision.InvestigationID != investigation.ID ||
		snapshot.Snapshot.InvestigationID != investigation.ID {
		return nil, errors.New("export dossier: source object graph is inconsistent")
	}
	changeBrief, err := service.Store.GetChangeBriefForRevisionAs(
		ctx,
		request.Principal,
		revision.ID,
	)
	if errors.Is(err, ErrNotFound) {
		changeBrief = nil
	} else if err != nil {
		return nil, err
	}

	scope, err := service.ResolveScope(
		ctx,
		request.Principal,
		snapshot.Snapshot.Semantics.DeclaredUniverse,
	)
	if err != nil {
		return nil, ErrNotFound
	}
	scope, err = normalizeDossierScope(
		snapshot.Snapshot.Semantics.DeclaredUniverse,
		scope,
	)
	if err != nil {
		return nil, fmt.Errorf("export dossier: %w", err)
	}
	visibleFacts := redactedDossierFacts(snapshot.Snapshot.Facts, scope.VisibleUnitIDs)
	visibleFactIDs := make([]string, len(visibleFacts))
	for i, fact := range visibleFacts {
		visibleFactIDs[i] = fact.FactID
	}
	visibleUnitDigest, err := dossier.VisibleUnitSetDigest(scope.VisibleUnitIDs)
	if err != nil {
		return nil, err
	}
	redactedSemantics := snapshot.Snapshot.Semantics
	redactedSemantics.DeclaredUniverse = scope.VisibleUnitIDs
	redactedSemantics.VisibilityProjection = scope.VisibilityProjectionID
	recipientSnapshotManifest, err := dossier.DigestValue(
		"phebs-dossier-recipient-snapshot-manifest-v1",
		struct {
			Units []string
			Facts []ConsumerEdgeFact
		}{scope.VisibleUnitIDs, visibleFacts},
	)
	if err != nil {
		return nil, err
	}
	recipientInputManifest, err := dossier.DigestValue(
		"phebs-dossier-recipient-input-manifest-v1",
		struct {
			RevisionID string
			Semantics  ConsumerSnapshotSemantics
		}{revision.ID, redactedSemantics},
	)
	if err != nil {
		return nil, err
	}

	var baseline *BaselineDesignation
	if request.BaselineID != "" {
		baseline, err = service.Store.GetBaselineDesignationAs(
			ctx,
			request.Principal,
			request.BaselineID,
		)
		if err != nil {
			return nil, err
		}
		if baseline.InvestigationID != investigation.ID ||
			baseline.RevisionID != revision.ID ||
			baseline.RunArtifactID != artifact.ID {
			return nil, errors.New("export dossier: baseline is outside the selected object graph")
		}
	}
	var decision *Decision
	if request.DecisionID != "" {
		decision, err = service.Store.GetDecisionAs(
			ctx,
			request.Principal,
			request.DecisionID,
		)
		if err != nil {
			return nil, err
		}
		if decision.InvestigationID != investigation.ID ||
			decision.RevisionID != revision.ID ||
			decision.VisibleScope != snapshot.Snapshot.Semantics.VisibilityProjection ||
			(decision.RunArtifactID != "" && decision.RunArtifactID != artifact.ID) ||
			(decision.BaselineID != "" &&
				(baseline == nil || decision.BaselineID != baseline.ID)) {
			return nil, errors.New("export dossier: decision is outside the recipient scope")
		}
	}
	if request.PredecessorID != "" {
		if _, err := service.Reopen(ctx, request.Principal, request.PredecessorID); err != nil {
			return nil, fmt.Errorf("export dossier: predecessor: %w", err)
		}
	}

	now := storeTimestamp(time.Now())
	dossierID, err := newULID(now)
	if err != nil {
		return nil, err
	}
	redactedInvestigation := *investigation
	investigationEntry, err := newDossierEntry(
		"objects/investigation.json",
		redactedInvestigation,
	)
	if err != nil {
		return nil, err
	}
	revisionEntry, err := newDossierEntry(
		"objects/revision.json",
		dossierRevisionView{
			ID: revision.ID, InvestigationID: revision.InvestigationID,
			Seq: revision.Seq, NormalizedQuestion: revision.NormalizedQuestion,
			DecisionSought:     revision.DecisionSought,
			DeclaredUniverse:   scope.VisibleUnitIDs,
			SnapshotPolicy:     revision.SnapshotPolicy,
			BuildConfiguration: revision.BuildConfiguration,
			PackSelection:      revision.PackSelection,
			EnumerationMethod:  revision.EnumerationMethod,
			Creator:            revision.Creator,
		},
	)
	if err != nil {
		return nil, err
	}
	artifactEntry, err := newDossierEntry(
		"objects/run-artifact.json",
		dossierArtifactView{
			ID: artifact.ID, RunID: artifact.RunID,
			TerminalStatus:       artifact.TerminalStatus,
			SnapshotManifest:     recipientSnapshotManifest,
			InputManifest:        recipientInputManifest,
			EligibilityResult:    request.EligibilityResult,
			AuthorizedFactIDs:    visibleFactIDs,
			VisibleUnitSetDigest: visibleUnitDigest,
			VisibleUnitCount:     len(scope.VisibleUnitIDs),
			AnalysisComplete:     snapshot.Snapshot.Semantics.AnalysisComplete,
			InputsFresh:          snapshot.Snapshot.Semantics.InputsFresh,
		},
	)
	if err != nil {
		return nil, err
	}
	snapshotEntry, err := newDossierEntry(
		"objects/consumer-snapshot.json",
		dossierSnapshotView{
			ID: snapshot.ID, Sequence: snapshot.Sequence,
			InvestigationID: investigation.ID, RevisionID: revision.ID,
			ArtifactID: artifact.ID, SourceSnapshot: snapshot.Snapshot.SourceSnapshot,
			Semantics: redactedSemantics, Facts: visibleFacts,
			Comparison: redactedDossierComparison(snapshot.Comparison, visibleFacts),
		},
	)
	if err != nil {
		return nil, err
	}
	entries := []dossier.Entry{
		investigationEntry,
		revisionEntry,
		artifactEntry,
		snapshotEntry,
	}
	var changeBriefReference *dossier.ObjectReference
	if changeBrief != nil {
		entry, err := newDossierEntry(
			"objects/change-brief.json",
			changeBriefContentFromBrief(*changeBrief),
		)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		reference := dossierObjectReference("change_brief", changeBrief.ID, entry)
		changeBriefReference = &reference
	}
	var baselineReference *dossier.ObjectReference
	if baseline != nil {
		redactedBaseline := *baseline
		redactedBaseline.ContentDigest = ""
		entry, err := newDossierEntry("objects/baseline.json", redactedBaseline)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		reference := dossierObjectReference("baseline", baseline.ID, entry)
		baselineReference = &reference
	}
	var decisionReference *dossier.ObjectReference
	if decision != nil {
		redactedDecision := *decision
		redactedDecision.ContentDigest = ""
		entry, err := newDossierEntry("objects/decision.json", redactedDecision)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		reference := dossierObjectReference("decision", decision.ID, entry)
		decisionReference = &reference
	}
	evidence, evidenceEntries, err := dossierEvidenceReferences(
		investigation.ID,
		snapshot.ID,
		visibleFacts,
	)
	if err != nil {
		return nil, err
	}
	entries = append(entries, evidenceEntries...)
	packCards := slices.Clone(request.PackCardIdentities)
	if revision.PackSelection != "" {
		packCards = append(packCards, revision.PackSelection)
	}
	packCards, err = canonicalStrings("dossier pack-card identity", packCards)
	if err != nil {
		return nil, err
	}
	expiresAt := ""
	if request.ExpiresAt != nil {
		expiresAt = request.ExpiresAt.Format(time.RFC3339)
	}
	sealed, err := dossier.Seal(
		dossier.Manifest{
			DossierID: dossierID, Question: revision.NormalizedQuestion,
			Investigation: dossierObjectReference(
				"investigation",
				investigation.ID,
				investigationEntry,
			),
			Revision: dossierObjectReference("revision", revision.ID, revisionEntry),
			PrimaryArtifact: dossierObjectReference(
				"run_artifact",
				artifact.ID,
				artifactEntry,
			),
			ConsumerSnapshot: dossierObjectReference(
				"consumer_snapshot",
				snapshot.ID,
				snapshotEntry,
			),
			ChangeBrief: changeBriefReference,
			Baseline:    baselineReference, Decision: decisionReference,
			SnapshotManifests:  []string{recipientSnapshotManifest},
			InputManifests:     []string{recipientInputManifest},
			PackCardIdentities: packCards, Evidence: evidence,
			Authorization: dossier.AuthorizationScope{
				Principal:              request.Principal,
				VisibilityProjectionID: scope.VisibilityProjectionID,
				VisibleUnitIDs:         scope.VisibleUnitIDs,
				VisibleUnitSetDigest:   visibleUnitDigest,
				RedactionScope:         "recipient-current-visible-universe",
				HandlingClassification: request.HandlingClassification,
			},
			Validity: dossier.Validity{
				EligibilityResult: request.EligibilityResult,
				SupportedClaims:   request.SupportedClaims,
				Blockers:          request.Blockers,
				FreshnessState:    request.FreshnessState,
				ValidationState:   request.ValidationState,
				ReviewDueRule:     request.ReviewDueRule,
				ExpiresAt:         expiresAt, Statement: dossier.ValidityStatement(),
			},
			IssuedAt:      now.Format(time.RFC3339),
			PredecessorID: request.PredecessorID,
		},
		entries,
		service.Signer.KeyID,
		service.Signer.PrivateKey,
	)
	if err != nil {
		return nil, fmt.Errorf("export dossier: seal: %w", err)
	}
	content, err := dossier.CanonicalJSON(sealed)
	if err != nil {
		return nil, err
	}
	if len(content) > maxInvestigationDossierBytes {
		return nil, errors.New("export dossier: sealed content exceeds 64 MiB")
	}
	record := investigationDossierRecord{
		ID: dossierID, InvestigationID: investigation.ID,
		Principal: request.Principal, SourceSnapshotID: snapshot.ID,
		RevisionID: revision.ID, ArtifactID: artifact.ID,
		BaselineID: request.BaselineID, DecisionID: request.DecisionID,
		PredecessorID: request.PredecessorID,
		RootDigest:    sealed.RootDigest, SigningKeyID: sealed.Signature.KeyID,
		SigningPublicKey: sealed.Signature.PublicKey,
		Content:          string(content), CreatedAt: now,
	}
	if err := service.Store.confirmInvestigationProjection(
		ctx,
		request.Principal,
		authorization,
	); err != nil {
		return nil, err
	}
	if err := service.Store.putInvestigationDossier(ctx, record); err != nil {
		return nil, err
	}
	currentScope, err := service.ResolveScope(
		ctx,
		request.Principal,
		snapshot.Snapshot.Semantics.DeclaredUniverse,
	)
	if err != nil {
		return nil, ErrNotFound
	}
	currentScope, err = normalizeDossierScope(
		snapshot.Snapshot.Semantics.DeclaredUniverse,
		currentScope,
	)
	if err != nil ||
		currentScope.VisibilityProjectionID != scope.VisibilityProjectionID ||
		!slices.Equal(currentScope.VisibleUnitIDs, scope.VisibleUnitIDs) {
		return nil, ErrNotFound
	}
	if err := service.Store.confirmInvestigationProjection(
		ctx,
		request.Principal,
		authorization,
	); err != nil {
		return nil, err
	}
	return sealed, nil
}

const putInvestigationDossierSQL = `
BEGIN;
LET $locked = UPDATE $artifact_rid
	SET retention_revision = (retention_revision ?? 0) + 1
	WHERE artifact_id = $artifact_id AND terminal_status = 'published'
	RETURN VALUE artifact_id;
LET $ready = array::len($locked) = 1
	AND array::len(SELECT VALUE dossier_id FROM $dossier_rid LIMIT 1) = 0
	AND array::len(SELECT VALUE owner_key FROM $owner_rid LIMIT 1) = 0;
LET $saved = IF $ready THEN
	(CREATE $dossier_rid CONTENT $dossier_content RETURN AFTER)
	ELSE [] END;
IF array::len($saved) = 1 {
	CREATE $owner_rid SET owner_key = $owner_key, artifact_id = $artifact_id,
		owner_kind = $owner_kind, owner_id = $owner_id,
		authorized_by = $authorized_by, reason = $reason,
		created_at = $created_at, content_digest = $owner_digest RETURN NONE
};
RETURN $saved;
COMMIT;`

func (s *Surreal) putInvestigationDossier(
	ctx context.Context,
	record investigationDossierRecord,
) error {
	owner, err := normalizeRunArtifactRetentionOwner(RunArtifactRetentionOwner{
		ArtifactID: record.ArtifactID, Kind: RunArtifactOwnerDossier,
		OwnerID: record.ID, AuthorizedBy: record.Principal,
		Reason: "sealed investigation dossier", CreatedAt: record.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("put investigation dossier: owner: %w", err)
	}
	results, err := surrealdb.Query[[]investigationDossierRecord](
		ctx,
		s.db,
		putInvestigationDossierSQL,
		map[string]any{
			"artifact_rid":    runArtifactRecordID(record.ArtifactID),
			"artifact_id":     record.ArtifactID,
			"dossier_rid":     dossierRecordID(record.ID),
			"dossier_content": record,
			"owner_rid":       retentionOwnerRecordID(owner.Key),
			"owner_key":       owner.Key, "owner_kind": string(owner.Kind),
			"owner_id": owner.OwnerID, "authorized_by": owner.AuthorizedBy,
			"reason": owner.Reason, "created_at": record.CreatedAt,
			"owner_digest": owner.ContentDigest,
		},
	)
	if err != nil {
		return fmt.Errorf("put investigation dossier: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].ID != record.ID ||
		rows[0].RootDigest != record.RootDigest {
		return fmt.Errorf("put investigation dossier: source artifact is unavailable: %w", ErrConflict)
	}
	return nil
}

func (s *Surreal) readInvestigationDossier(
	ctx context.Context,
	id string,
) (*investigationDossierRecord, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]investigationDossierRecord](
		ctx,
		s.db,
		`SELECT dossier_id, investigation_id, principal, source_snapshot_id,
			revision_id, artifact_id, baseline_id, decision_id, predecessor_id,
			root_digest, signing_key_id, signing_public_key, content, created_at
			FROM $rid LIMIT 1`,
		map[string]any{"rid": dossierRecordID(id)},
	)
	if err != nil {
		return nil, fmt.Errorf("read investigation dossier: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (service InvestigationDossierService) Reopen(
	ctx context.Context,
	principal, id string,
) (*dossier.Sealed, error) {
	if service.Store == nil || service.ResolveScope == nil ||
		!validInvestigationPrincipal(principal) || !validULID(id) {
		return nil, ErrNotFound
	}
	investigationID, resolutionErr := service.Store.lookupRecordString(
		ctx,
		"SELECT investigation_id AS value FROM $rid LIMIT 1",
		dossierRecordID(id),
	)
	projection, err := service.Store.readAuthorizedProjection(
		ctx,
		principal,
		investigationID,
		resolutionErr,
	)
	if err != nil {
		return nil, ErrNotFound
	}
	record, readErr := service.Store.readInvestigationDossier(ctx, id)
	if readErr != nil {
		return nil, readErr
	}
	if record.Principal != principal || record.InvestigationID != investigationID {
		return nil, ErrNotFound
	}
	sealed, err := dossier.Decode([]byte(record.Content))
	if err != nil {
		return nil, fmt.Errorf("reopen dossier: %w", err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(record.SigningPublicKey)
	if err != nil {
		return nil, errors.New("reopen dossier: stored signing key is invalid")
	}
	if _, err := dossier.Verify(*sealed, dossier.VerifyOptions{
		TrustedKeyID:  record.SigningKeyID,
		TrustedPublic: ed25519.PublicKey(publicKey),
	}); err != nil {
		return nil, fmt.Errorf("reopen dossier: %w", err)
	}
	if sealed.RootDigest != record.RootDigest ||
		sealed.Manifest.Authorization.Principal != principal ||
		sealed.Manifest.Investigation.ID != investigationID ||
		sealed.Manifest.Revision.ID != record.RevisionID ||
		sealed.Manifest.PrimaryArtifact.ID != record.ArtifactID ||
		sealed.Manifest.ConsumerSnapshot.ID != record.SourceSnapshotID {
		return nil, errors.New("reopen dossier: stored associations are inconsistent")
	}
	investigation, err := service.Store.GetInvestigationAs(ctx, principal, investigationID)
	if err != nil || investigation.ID != sealed.Manifest.Investigation.ID {
		return nil, ErrNotFound
	}
	revision, err := service.Store.GetRevisionAs(ctx, principal, record.RevisionID)
	if err != nil || revision.InvestigationID != investigationID {
		return nil, ErrNotFound
	}
	var changeBrief *ChangeBrief
	if sealed.Manifest.ChangeBrief != nil {
		changeBrief, err = service.Store.GetChangeBriefAs(
			ctx,
			principal,
			sealed.Manifest.ChangeBrief.ID,
		)
		if err != nil ||
			changeBrief.InvestigationID != investigationID ||
			changeBrief.RevisionID != revision.ID {
			return nil, ErrNotFound
		}
	}
	artifact, err := service.Store.GetRunArtifactAs(ctx, principal, record.ArtifactID)
	if err != nil || artifact.TerminalStatus != RunPublished {
		return nil, ErrNotFound
	}
	snapshot, err := service.Store.GetConsumerSnapshotAs(
		ctx,
		principal,
		record.SourceSnapshotID,
	)
	if err != nil ||
		snapshot.Snapshot.InvestigationID != investigationID ||
		snapshot.Snapshot.RevisionID != revision.ID ||
		snapshot.Snapshot.ArtifactID != artifact.ID {
		return nil, ErrNotFound
	}
	if record.BaselineID != "" {
		baseline, err := service.Store.GetBaselineDesignationAs(
			ctx,
			principal,
			record.BaselineID,
		)
		if err != nil || baseline.InvestigationID != investigationID ||
			sealed.Manifest.Baseline == nil ||
			sealed.Manifest.Baseline.ID != baseline.ID {
			return nil, ErrNotFound
		}
	}
	if record.DecisionID != "" {
		decision, err := service.Store.GetDecisionAs(ctx, principal, record.DecisionID)
		if err != nil || decision.InvestigationID != investigationID ||
			sealed.Manifest.Decision == nil ||
			sealed.Manifest.Decision.ID != decision.ID {
			return nil, ErrNotFound
		}
	}
	if record.PredecessorID != "" {
		predecessor, err := service.Store.readInvestigationDossier(
			ctx,
			record.PredecessorID,
		)
		if err != nil || predecessor.InvestigationID != investigationID ||
			predecessor.Principal != principal ||
			sealed.Manifest.PredecessorID != predecessor.ID {
			return nil, ErrNotFound
		}
	}
	currentScope, err := service.ResolveScope(
		ctx,
		principal,
		sealed.Manifest.Authorization.VisibleUnitIDs,
	)
	if err != nil {
		return nil, ErrNotFound
	}
	currentScope, err = normalizeDossierScope(
		sealed.Manifest.Authorization.VisibleUnitIDs,
		currentScope,
	)
	if err != nil ||
		!slices.Equal(
			currentScope.VisibleUnitIDs,
			sealed.Manifest.Authorization.VisibleUnitIDs,
		) {
		return nil, ErrNotFound
	}
	facts := make(map[string]ConsumerEdgeFact, len(snapshot.Snapshot.Facts))
	for _, fact := range snapshot.Snapshot.Facts {
		facts[fact.FactID] = fact
	}
	visibleUnits := make(map[string]struct{}, len(currentScope.VisibleUnitIDs))
	for _, unit := range currentScope.VisibleUnitIDs {
		visibleUnits[unit] = struct{}{}
	}
	entryContent := make(map[string][]byte, len(sealed.Entries))
	for _, entry := range sealed.Entries {
		entryContent[entry.Path] = entry.Content
	}
	if changeBrief != nil {
		expected, err := newDossierEntry(
			sealed.Manifest.ChangeBrief.EntryPath,
			changeBriefContentFromBrief(*changeBrief),
		)
		if err != nil || !bytes.Equal(
			expected.Content,
			entryContent[sealed.Manifest.ChangeBrief.EntryPath],
		) {
			return nil, ErrNotFound
		}
	}
	for _, evidence := range sealed.Manifest.Evidence {
		fact, ok := facts[evidence.FactID]
		if !ok || fact.UnitID == "" {
			return nil, ErrNotFound
		}
		if _, ok := visibleUnits[fact.UnitID]; !ok {
			return nil, ErrNotFound
		}
		wantDigest, err := dossier.DigestValue(
			"phebs-dossier-fact-reference-v1",
			fact,
		)
		if err != nil || !bytes.Equal(
			[]byte(wantDigest),
			[]byte(evidence.MaterialDigest),
		) || !dossierFindingMatchesFact(entryContent, evidence, fact) {
			return nil, ErrNotFound
		}
	}
	if err := service.Store.confirmInvestigationProjection(
		ctx,
		principal,
		projection,
	); err != nil {
		return nil, err
	}
	return sealed, nil
}

func dossierFindingMatchesFact(
	entryContent map[string][]byte,
	evidence dossier.EvidenceReference,
	fact ConsumerEdgeFact,
) bool {
	content, ok := entryContent[evidence.FindingEntryPath]
	if !ok {
		return false
	}
	expected, err := dossier.NewEntry(
		evidence.FindingEntryPath,
		"application/json",
		fact,
	)
	return err == nil && bytes.Equal(content, expected.Content)
}
