package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/compat"
)

const (
	WorkbenchCompatibilityAnalysisSchemaVersion = "workbench-compatibility-analysis-v1"
	workbenchCompatibilityArtifactScope         = "workbench-compatibility-v1"
)

type WorkbenchCompatibilityAnalysisRequest struct {
	InvestigationID string        `json:"investigation_id"`
	RevisionID      string        `json:"revision_id"`
	IdempotencyKey  string        `json:"idempotency_key"`
	Before          []compat.File `json:"before" maxItems:"256"`
	After           []compat.File `json:"after" maxItems:"256"`
}

type WorkbenchCompatibilityAnalysis struct {
	SchemaVersion   string                      `json:"schema_version"`
	Status          string                      `json:"status"` // published | failed
	InvestigationID string                      `json:"investigation_id"`
	RevisionID      string                      `json:"revision_id"`
	Run             Run                         `json:"run"`
	Artifact        RunArtifact                 `json:"artifact"`
	Endpoints       []WorkbenchEndpointSnapshot `json:"endpoints"`
	Compatibility   *compat.CompatibilityResult `json:"compatibility,omitempty"`
	Failure         string                      `json:"failure,omitempty"`
}

type workbenchCompatibilitySnapshotManifest struct {
	SchemaVersion       string                        `json:"schema_version"`
	InvestigationID     string                        `json:"investigation_id"`
	RevisionID          string                        `json:"revision_id"`
	AuthorizationDigest string                        `json:"authorization_digest"`
	Repositories        []WorkbenchRepositorySnapshot `json:"repositories"`
	Endpoints           []WorkbenchEndpointSnapshot   `json:"endpoints"`
	Capabilities        []WorkbenchCapabilitySnapshot `json:"capabilities"`
}

type workbenchCompatibilityInputManifest struct {
	SchemaVersion string               `json:"schema_version"`
	Proposal      ChangeBriefProposal  `json:"proposal"`
	Before        compat.InputSnapshot `json:"before"`
	After         compat.InputSnapshot `json:"after"`
}

type workbenchCompatibilityDiagnostic struct {
	SchemaVersion string                      `json:"schema_version"`
	Compatibility *compat.CompatibilityResult `json:"compatibility,omitempty"`
	Failure       string                      `json:"failure,omitempty"`
}

// RetainCompatibility is the explicit additive Workbench analysis action.
// It never calls the proof-bundle store and therefore cannot convert,
// retarget, delete, or mint a second identity for an existing bundle.
func (service InvestigationWorkbenchService) RetainCompatibility(
	ctx context.Context,
	principal string,
	request WorkbenchCompatibilityAnalysisRequest,
) (*WorkbenchCompatibilityAnalysis, error) {
	if service.Store == nil || service.Resolver == nil {
		return nil, errors.New("workbench store and resolver are required")
	}
	if service.Compatibility == nil {
		return nil, invalidWorkbench(errors.New(
			"workbench compatibility is unavailable",
		))
	}
	if !validInvestigationPrincipal(principal) ||
		!validULID(request.InvestigationID) ||
		!strings.HasPrefix(request.RevisionID, "ivr_") {
		return nil, ErrNotFound
	}
	if strings.TrimSpace(request.IdempotencyKey) != request.IdempotencyKey ||
		request.IdempotencyKey == "" ||
		len(request.IdempotencyKey) > maxWorkbenchIDBytes ||
		!utf8.ValidString(request.IdempotencyKey) {
		return nil, invalidWorkbench(errors.New(
			"workbench compatibility idempotency key is invalid",
		))
	}
	if len(request.Before) == 0 || len(request.After) == 0 {
		return nil, invalidWorkbench(errors.New(
			"workbench compatibility requires before and after source sets",
		))
	}
	investigation, err := service.Store.GetInvestigationAs(
		ctx,
		principal,
		request.InvestigationID,
	)
	if err != nil || investigation.Owner != principal {
		return nil, ErrNotFound
	}
	if investigation.Lifecycle == "archived" {
		return nil, fmt.Errorf(
			"workbench compatibility investigation is archived: %w",
			ErrConflict,
		)
	}
	if investigation.CurrentRevisionID != request.RevisionID {
		return nil, fmt.Errorf(
			"workbench compatibility revision changed: %w",
			ErrConflict,
		)
	}
	revision, err := service.Store.GetRevisionAs(
		ctx,
		principal,
		request.RevisionID,
	)
	if err != nil || revision.InvestigationID != investigation.ID {
		return nil, ErrNotFound
	}
	brief, err := service.Store.GetChangeBriefForRevisionAs(
		ctx,
		principal,
		revision.ID,
	)
	if err != nil || brief.InvestigationID != investigation.ID {
		return nil, ErrNotFound
	}
	if brief.TicketKind != ChangeBriefModify ||
		brief.What.Proposal == nil ||
		brief.What.Proposal.Protocol != "protobuf" {
		return nil, invalidWorkbench(errors.New(
			"compatibility is unavailable for this Workbench delta",
		))
	}
	var current ChangeBriefContractSelection
	foundCurrent := false
	for _, selection := range brief.What.Selections {
		if selection.Role == ChangeBriefCurrent {
			current = selection
			foundCurrent = true
			break
		}
	}
	if !foundCurrent || current.Protocol != "protobuf" {
		return nil, invalidWorkbench(errors.New(
			"protobuf compatibility requires one exact current endpoint",
		))
	}

	prepared, err := compat.Prepare(ctx, compat.Request{
		Lineage: current.DeclarationLineage,
		Before:  request.Before,
		After:   request.After,
	})
	if err != nil {
		return nil, invalidWorkbench(err)
	}
	proposal, err := compatibilityProposal(request.After)
	if err != nil {
		return nil, invalidWorkbench(err)
	}
	if !reflect.DeepEqual(*brief.What.Proposal, proposal) {
		return nil, fmt.Errorf(
			"workbench compatibility proposal changed: %w",
			ErrConflict,
		)
	}
	resolution, err := service.Resolver.ResolveWorkbench(
		ctx,
		principal,
		WorkbenchResolutionRequest{
			Repositories: []string{current.Repository},
			Selections:   []ChangeBriefContractSelection{current},
			Capabilities: []string{
				"contract-atlas",
				"contract-compatibility",
			},
		},
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf(
			"resolve workbench compatibility: %w",
			err,
		)
	}
	snapshot, err := workbenchCompatibilitySnapshot(
		investigation.ID,
		revision.ID,
		current,
		resolution,
	)
	if err != nil {
		return nil, err
	}
	input := workbenchCompatibilityInputManifest{
		SchemaVersion: WorkbenchCompatibilityAnalysisSchemaVersion,
		Proposal:      proposal,
		Before:        prepared.Before,
		After:         prepared.After,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	inputDigest, err := contentDigest(input)
	if err != nil {
		return nil, err
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	resolvedIdentities := make([]string, len(snapshot.Endpoints))
	for index, endpoint := range snapshot.Endpoints {
		resolvedIdentities[index] = "contract:" + strings.Join([]string{
			endpoint.Selection.Protocol,
			endpoint.Selection.Repository,
			endpoint.Selection.DeclarationLineage,
			endpoint.Selection.CanonicalOperation,
		}, "|")
	}
	run, err := service.Store.CreateRun(ctx, RunRequest{
		RevisionID:              revision.ID,
		IdempotencyKey:          request.IdempotencyKey,
		ResolvedInputIdentities: resolvedIdentities,
		RequestedSnapshot:       string(snapshotJSON),
		InputManifestDigest:     inputDigest,
		RequestedPackVersions: []string{
			"contract-atlas:" + workbenchCapabilityVersion(
				snapshot.Capabilities,
				"contract-atlas",
			),
			"contract-compatibility:" + workbenchCapabilityVersion(
				snapshot.Capabilities,
				"contract-compatibility",
			),
		},
		CreatedBy: principal,
	})
	if err != nil {
		return nil, err
	}
	if run.State == RunPublished || run.State == RunFailed {
		artifact, artifactErr := service.Store.GetRunArtifactForRunAs(
			ctx,
			principal,
			run.ID,
		)
		if artifactErr != nil {
			return nil, artifactErr
		}
		return decodeWorkbenchCompatibilityAnalysis(
			investigation.ID,
			revision.ID,
			*run,
			*artifact,
		)
	}
	if run.State != RunQueued {
		return nil, fmt.Errorf(
			"workbench compatibility analysis is already running: %w",
			ErrConflict,
		)
	}
	lease, err := service.Store.AcquireInvestigationRunLease(
		ctx,
		run.ID,
		principal,
	)
	if err != nil {
		return nil, err
	}
	if _, err := service.Store.AdvanceInvestigationRun(
		ctx,
		*lease,
		RunEnumerating,
		"exact Workbench target resolved",
	); err != nil {
		return nil, err
	}
	if _, err := service.Store.AdvanceInvestigationRun(
		ctx,
		*lease,
		RunAnalyzing,
		"bounded protobuf WIRE analysis started",
	); err != nil {
		return nil, err
	}
	result, checkErr := service.Compatibility.Check(ctx, compat.Request{
		Lineage: current.DeclarationLineage,
		Before:  request.Before,
		After:   request.After,
	})
	if checkErr != nil {
		return service.failWorkbenchCompatibility(
			ctx,
			principal,
			investigation.ID,
			revision.ID,
			current,
			*run,
			*lease,
			string(snapshotJSON),
			string(inputJSON),
			"COMPATIBILITY_CHECK_FAILED",
			errors.Is(checkErr, compat.ErrUnavailable) ||
				errors.Is(checkErr, compat.ErrCheckFailed),
			checkErr,
		)
	}
	if result == nil ||
		result.Before.Digest != prepared.Before.Digest ||
		result.After.Digest != prepared.After.Digest ||
		result.Run.Engine != "buf" ||
		result.Run.Version != compat.Version ||
		result.Run.Policy != compat.Policy {
		return service.failWorkbenchCompatibility(
			ctx,
			principal,
			investigation.ID,
			revision.ID,
			current,
			*run,
			*lease,
			string(snapshotJSON),
			string(inputJSON),
			"COMPATIBILITY_RESULT_INCONSISTENT",
			true,
			errors.New("checker result is inconsistent"),
		)
	}
	coverage, coverageErr := CanonicalInvestigationCoverageLedger(
		InvestigationCoverageLedger{
			EligibleUnits: 1,
			Analyzed:      1,
			Failures:      []InvestigationCoverageFailure{},
		},
	)
	if coverageErr != nil {
		return nil, coverageErr
	}
	if _, err := service.Store.AdvanceInvestigationRun(
		ctx,
		*lease,
		RunPublishing,
		"bounded protobuf WIRE analysis complete",
	); err != nil {
		return nil, err
	}
	diagnostic, err := json.Marshal(workbenchCompatibilityDiagnostic{
		SchemaVersion: WorkbenchCompatibilityAnalysisSchemaVersion,
		Compatibility: result,
	})
	if err != nil {
		return nil, err
	}
	artifact, err := service.Store.PublishInvestigationRun(
		ctx,
		*lease,
		RunArtifact{
			Scope:             workbenchCompatibilityArtifactScope,
			RunID:             run.ID,
			SnapshotManifest:  string(snapshotJSON),
			InputManifest:     string(inputJSON),
			CoverageLedger:    coverage,
			EligibilityResult: "compatibility_only",
			DiagnosticData:    string(diagnostic),
		},
	)
	if err != nil {
		return nil, err
	}
	publishedRun, err := service.Store.GetRunAs(ctx, principal, run.ID)
	if err != nil {
		return nil, err
	}
	return decodeWorkbenchCompatibilityAnalysis(
		investigation.ID,
		revision.ID,
		*publishedRun,
		*artifact,
	)
}

func (service InvestigationWorkbenchService) failWorkbenchCompatibility(
	ctx context.Context,
	principal, investigationID, revisionID string,
	current ChangeBriefContractSelection,
	run Run,
	lease InvestigationRunLease,
	snapshotJSON, inputJSON, code string,
	retryable bool,
	failure error,
) (*WorkbenchCompatibilityAnalysis, error) {
	coverage, err := CanonicalInvestigationCoverageLedger(
		InvestigationCoverageLedger{
			EligibleUnits: 1,
			Failed:        1,
			Failures: []InvestigationCoverageFailure{{
				Unit: strings.Join([]string{
					"contract",
					current.Protocol,
					current.Repository,
					current.DeclarationLineage,
					current.CanonicalOperation,
				}, "|"),
				Code:      code,
				Retryable: retryable,
			}},
		},
	)
	if err != nil {
		return nil, err
	}
	diagnostic, err := json.Marshal(workbenchCompatibilityDiagnostic{
		SchemaVersion: WorkbenchCompatibilityAnalysisSchemaVersion,
		Failure:       boundedCompatibilityFailure(failure),
	})
	if err != nil {
		return nil, err
	}
	artifact, err := service.Store.FailInvestigationRun(
		ctx,
		lease,
		RunArtifact{
			Scope:             workbenchCompatibilityArtifactScope,
			RunID:             run.ID,
			SnapshotManifest:  snapshotJSON,
			InputManifest:     inputJSON,
			CoverageLedger:    coverage,
			EligibilityResult: "compatibility_unavailable",
			DiagnosticData:    string(diagnostic),
		},
		"bounded compatibility analysis failed",
	)
	if err != nil {
		return nil, err
	}
	failedRun, err := service.Store.GetRunAs(ctx, principal, run.ID)
	if err != nil {
		return nil, err
	}
	return decodeWorkbenchCompatibilityAnalysis(
		investigationID,
		revisionID,
		*failedRun,
		*artifact,
	)
}

func compatibilityProposal(
	files []compat.File,
) (ChangeBriefProposal, error) {
	proposalFiles := make([]ChangeBriefProposalFile, len(files))
	for index, file := range files {
		sum := sha256.Sum256([]byte(file.Content))
		proposalFiles[index] = ChangeBriefProposalFile{
			Path:        file.Path,
			ContentHash: "sha256:" + hex.EncodeToString(sum[:]),
			SizeBytes:   len(file.Content),
		}
	}
	proposal, err := normalizeChangeBriefProposal(&ChangeBriefProposal{
		Protocol: "protobuf",
		Files:    proposalFiles,
	})
	if err != nil {
		return ChangeBriefProposal{}, err
	}
	return *proposal, nil
}

func workbenchCompatibilitySnapshot(
	investigationID, revisionID string,
	current ChangeBriefContractSelection,
	resolution WorkbenchResolution,
) (workbenchCompatibilitySnapshotManifest, error) {
	if !validSHA256Digest(resolution.AuthorizationDigest) {
		return workbenchCompatibilitySnapshotManifest{}, errors.New(
			"resolve workbench compatibility: invalid authorization digest",
		)
	}
	if len(resolution.Repositories) != 1 ||
		resolution.Repositories[0].Name != current.Repository ||
		!validWorkbenchCommit(resolution.Repositories[0].Commit) {
		return workbenchCompatibilitySnapshotManifest{}, fmt.Errorf(
			"resolve workbench compatibility: exact repository unavailable: %w",
			ErrConflict,
		)
	}
	if len(resolution.Endpoints) != 1 ||
		workbenchSelectionKey(resolution.Endpoints[0].Selection) !=
			workbenchSelectionKey(current) ||
		resolution.Endpoints[0].DeclarationCommit !=
			resolution.Repositories[0].Commit ||
		!validSHA256Digest(resolution.Endpoints[0].DeclarationDigest) {
		return workbenchCompatibilitySnapshotManifest{}, fmt.Errorf(
			"resolve workbench compatibility: exact endpoint unavailable: %w",
			ErrConflict,
		)
	}
	if err := validateWorkbenchDeclarationSources(
		resolution.Endpoints[0],
	); err != nil {
		return workbenchCompatibilitySnapshotManifest{}, fmt.Errorf(
			"resolve workbench compatibility: %w",
			err,
		)
	}
	capabilityByID := make(map[string]WorkbenchCapabilitySnapshot)
	for _, capability := range resolution.Capabilities {
		if (capability.ID == "contract-atlas" ||
			capability.ID == "contract-compatibility") &&
			capability.Available {
			capabilityByID[capability.ID] = capability
		}
	}
	capabilities := make([]WorkbenchCapabilitySnapshot, 0, 2)
	for _, id := range []string{
		"contract-atlas",
		"contract-compatibility",
	} {
		capability := capabilityByID[id]
		if capability.ID == "" || capability.Version == "" ||
			!validSHA256Digest(capability.ContentDigest) {
			return workbenchCompatibilitySnapshotManifest{}, invalidWorkbench(
				fmt.Errorf("workbench capability %q is unavailable", id),
			)
		}
		capabilities = append(capabilities, capability)
	}
	return workbenchCompatibilitySnapshotManifest{
		SchemaVersion:       WorkbenchCompatibilityAnalysisSchemaVersion,
		InvestigationID:     investigationID,
		RevisionID:          revisionID,
		AuthorizationDigest: resolution.AuthorizationDigest,
		Repositories:        slices.Clone(resolution.Repositories),
		Endpoints:           slices.Clone(resolution.Endpoints),
		Capabilities:        capabilities,
	}, nil
}

func workbenchCapabilityVersion(
	capabilities []WorkbenchCapabilitySnapshot,
	id string,
) string {
	for _, capability := range capabilities {
		if capability.ID == id {
			return capability.Version
		}
	}
	return "unavailable"
}

func boundedCompatibilityFailure(err error) string {
	value := strings.TrimSpace(err.Error())
	if len(value) > 4_096 {
		value = value[:4_096]
	}
	if value == "" || !utf8.ValidString(value) {
		return "compatibility analysis failed"
	}
	return value
}

func decodeWorkbenchCompatibilityAnalysis(
	investigationID, revisionID string,
	run Run,
	artifact RunArtifact,
) (*WorkbenchCompatibilityAnalysis, error) {
	if artifact.Scope != workbenchCompatibilityArtifactScope ||
		artifact.RunID != run.ID ||
		run.RevisionID != revisionID {
		return nil, errors.New(
			"read workbench compatibility: stored binding is inconsistent",
		)
	}
	var snapshot workbenchCompatibilitySnapshotManifest
	if err := json.Unmarshal(
		[]byte(artifact.SnapshotManifest),
		&snapshot,
	); err != nil ||
		snapshot.SchemaVersion !=
			WorkbenchCompatibilityAnalysisSchemaVersion ||
		snapshot.InvestigationID != investigationID ||
		snapshot.RevisionID != revisionID {
		return nil, errors.New(
			"read workbench compatibility: snapshot manifest is inconsistent",
		)
	}
	var diagnostic workbenchCompatibilityDiagnostic
	if err := json.Unmarshal(
		[]byte(artifact.DiagnosticData),
		&diagnostic,
	); err != nil ||
		diagnostic.SchemaVersion !=
			WorkbenchCompatibilityAnalysisSchemaVersion {
		return nil, errors.New(
			"read workbench compatibility: diagnostic is inconsistent",
		)
	}
	status := "published"
	if artifact.TerminalStatus == RunFailed {
		status = "failed"
	} else if artifact.TerminalStatus != RunPublished {
		return nil, errors.New(
			"read workbench compatibility: artifact is not terminal",
		)
	}
	return &WorkbenchCompatibilityAnalysis{
		SchemaVersion:   WorkbenchCompatibilityAnalysisSchemaVersion,
		Status:          status,
		InvestigationID: investigationID,
		RevisionID:      revisionID,
		Run:             run,
		Artifact:        artifact,
		Endpoints:       snapshot.Endpoints,
		Compatibility:   diagnostic.Compatibility,
		Failure:         diagnostic.Failure,
	}, nil
}
