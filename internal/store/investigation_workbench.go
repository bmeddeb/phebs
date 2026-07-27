package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/idlpreview"
)

const (
	WorkbenchPreviewSchemaVersion = "change-workbench-preview-v1"

	maxWorkbenchRepositories = 1_000
	maxWorkbenchCapabilities = 64
	maxWorkbenchIDBytes      = 256
	workbenchReceiptRecovery = 5 * time.Second
)

var ErrInvalidWorkbench = errors.New("invalid workbench input")

func invalidWorkbench(err error) error {
	return fmt.Errorf("%v: %w", err, ErrInvalidWorkbench)
}

type WorkbenchRevisionDraft struct {
	NormalizedQuestion string `json:"normalized_question" maxLength:"65536"`
	DecisionSought     string `json:"decision_sought" maxLength:"65536"`
	SnapshotPolicy     string `json:"snapshot_policy" maxLength:"65536"`
	BuildConfiguration string `json:"build_configuration" maxLength:"65536"`
	EnumerationMethod  string `json:"enumeration_method" maxLength:"65536"`
}

type WorkbenchProposalSource struct {
	Path    string `json:"path" maxLength:"4096"`
	Content string `json:"content" maxLength:"4194304"`
}

type WorkbenchWhatDraft struct {
	Selections       []ChangeBriefContractSelection `json:"selections" maxItems:"3"`
	ProposalProtocol string                         `json:"proposal_protocol,omitempty"`
	ProposalSources  []WorkbenchProposalSource      `json:"proposal_sources,omitempty" maxItems:"256"`
}

type WorkbenchChangeBriefDraft struct {
	TicketKind        ChangeBriefTicketKind `json:"ticket_kind"`
	Problem           string                `json:"problem" maxLength:"16384"`
	DesiredOutcome    string                `json:"desired_outcome" maxLength:"16384"`
	SuccessCriteria   []string              `json:"success_criteria" minItems:"1" maxItems:"32"`
	NonGoals          []string              `json:"non_goals" maxItems:"32"`
	Assumptions       []string              `json:"assumptions" maxItems:"32"`
	OpenQuestions     []string              `json:"open_questions" maxItems:"32"`
	ExternalReference string                `json:"external_reference,omitempty" maxLength:"2048"`
	What              WorkbenchWhatDraft    `json:"what"`
}

// WorkbenchPlan is the complete caller input shared by preview and mutation.
// Proposal source bytes are accepted only to compute commitments; previews
// and persisted Change Briefs never echo or retain them.
type WorkbenchPlan struct {
	InvestigationID    string                    `json:"investigation_id,omitempty" maxLength:"26"`
	ExpectedRevisionID string                    `json:"expected_revision_id,omitempty" maxLength:"68"`
	Referent           string                    `json:"referent" maxLength:"65536"`
	ClaimFamily        string                    `json:"claim_family" maxLength:"65536"`
	Title              string                    `json:"title" maxLength:"65536"`
	Revision           WorkbenchRevisionDraft    `json:"revision"`
	Brief              WorkbenchChangeBriefDraft `json:"brief"`
	Repositories       []string                  `json:"repositories" minItems:"1" maxItems:"1000"`
	Capabilities       []string                  `json:"capabilities" minItems:"1" maxItems:"64"`
}

type WorkbenchRepositorySnapshot struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type WorkbenchEndpointSnapshot struct {
	Selection          ChangeBriefContractSelection `json:"selection"`
	DeclarationCommit  string                       `json:"declaration_commit"`
	DeclarationDigest  string                       `json:"declaration_digest"`
	DeclarationSources []WorkbenchDeclarationSource `json:"declaration_sources"`
	SourcesTruncated   bool                         `json:"sources_truncated"`
}

// WorkbenchDeclarationSource is an immutable Atlas declaration citation.
// The commit and byte span, not a mutable branch or line-only URL, bind the
// link rendered by later Workbench adapters.
type WorkbenchDeclarationSource struct {
	Repository  string `json:"repository"`
	Commit      string `json:"commit"`
	Path        string `json:"path"`
	StartByte   int    `json:"start_byte"`
	EndByte     int    `json:"end_byte"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	AssertionID string `json:"assertion_id"`
	RunID       string `json:"run_id"`
	AtomID      string `json:"atom_id"`
}

type WorkbenchCapabilitySnapshot struct {
	ID            string `json:"id"`
	Available     bool   `json:"available"`
	Version       string `json:"version,omitempty"`
	ContentDigest string `json:"content_digest,omitempty"`
}

type WorkbenchResolutionRequest struct {
	Repositories []string                       `json:"repositories"`
	Selections   []ChangeBriefContractSelection `json:"selections"`
	Capabilities []string                       `json:"capabilities"`
}

type WorkbenchResolution struct {
	AuthorizationDigest string                        `json:"authorization_digest"`
	Repositories        []WorkbenchRepositorySnapshot `json:"repositories"`
	Endpoints           []WorkbenchEndpointSnapshot   `json:"endpoints"`
	Capabilities        []WorkbenchCapabilitySnapshot `json:"capabilities"`
}

type WorkbenchBaselineRequest struct {
	Repository string   `json:"repository"`
	Commit     string   `json:"commit"`
	Paths      []string `json:"paths"`
}

type WorkbenchSourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WorkbenchResolver projects only the principal's currently authorized
// repository, endpoint, and capability state. Missing and unauthorized inputs
// must both be represented by omission; the service emits one refusal shape.
type WorkbenchResolver interface {
	ResolveWorkbench(
		context.Context,
		string,
		WorkbenchResolutionRequest,
	) (WorkbenchResolution, error)
}

// WorkbenchBaselineResolver is the source-binding extension required by
// retained compatibility. The implementation must reauthorize Repository and
// read every requested path from exactly Commit; caller-supplied source bytes
// are never authority for the retained baseline.
type WorkbenchBaselineResolver interface {
	ResolveWorkbenchBaseline(
		context.Context,
		string,
		WorkbenchBaselineRequest,
	) ([]WorkbenchSourceFile, error)
}

type WorkbenchBlocker struct {
	Code  string `json:"code"`
	Input string `json:"input,omitempty"`
}

type WorkbenchEstimate struct {
	RepositoryCount int `json:"repository_count"`
	EndpointCount   int `json:"endpoint_count"`
	ProposalFiles   int `json:"proposal_files"`
	ProposalBytes   int `json:"proposal_bytes"`
	CapabilityCount int `json:"capability_count"`
	AnalysisUnits   int `json:"analysis_units"`
}

type WorkbenchRevisionCommitment struct {
	NormalizedQuestion string `json:"normalized_question"`
	DecisionSought     string `json:"decision_sought"`
	DeclaredUniverse   string `json:"declared_universe"`
	SnapshotPolicy     string `json:"snapshot_policy"`
	BuildConfiguration string `json:"build_configuration"`
	PackSelection      string `json:"pack_selection"`
	EnumerationMethod  string `json:"enumeration_method"`
}

type WorkbenchBriefCommitment struct {
	SchemaVersion     string                `json:"schema_version"`
	TicketKind        ChangeBriefTicketKind `json:"ticket_kind"`
	Problem           string                `json:"problem"`
	DesiredOutcome    string                `json:"desired_outcome"`
	SuccessCriteria   []string              `json:"success_criteria"`
	NonGoals          []string              `json:"non_goals"`
	Assumptions       []string              `json:"assumptions"`
	OpenQuestions     []string              `json:"open_questions"`
	ExternalReference string                `json:"external_reference,omitempty"`
	What              ChangeBriefWhat       `json:"what"`
	Creator           string                `json:"creator"`
}

type WorkbenchPreview struct {
	SchemaVersion        string                        `json:"schema_version"`
	Operation            string                        `json:"operation"`
	Ready                bool                          `json:"ready"`
	PreviewDigest        string                        `json:"preview_digest,omitempty"`
	InvestigationID      string                        `json:"investigation_id,omitempty"`
	ExpectedRevisionID   string                        `json:"expected_revision_id,omitempty"`
	NextRevisionSequence int                           `json:"next_revision_sequence"`
	Referent             string                        `json:"referent"`
	ClaimFamily          string                        `json:"claim_family"`
	Title                string                        `json:"title"`
	Revision             WorkbenchRevisionCommitment   `json:"revision"`
	Brief                WorkbenchBriefCommitment      `json:"brief"`
	AuthorizationDigest  string                        `json:"authorization_digest,omitempty"`
	Repositories         []WorkbenchRepositorySnapshot `json:"repositories"`
	Endpoints            []WorkbenchEndpointSnapshot   `json:"endpoints"`
	Capabilities         []WorkbenchCapabilitySnapshot `json:"capabilities"`
	Compatibility        WorkbenchCompatibilityPreview `json:"compatibility"`
	Estimate             WorkbenchEstimate             `json:"estimate"`
	Blockers             []WorkbenchBlocker            `json:"blockers"`
}

type WorkbenchCompatibilityPreview struct {
	Status   string         `json:"status"` // available | unavailable
	Protocol string         `json:"protocol,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Limits   *compat.Limits `json:"limits,omitempty"`
}

type WorkbenchMutationRequest struct {
	Plan           WorkbenchPlan `json:"plan"`
	PreviewDigest  string        `json:"preview_digest" maxLength:"71"`
	IdempotencyKey string        `json:"idempotency_key" maxLength:"256"`
}

type WorkbenchView struct {
	Investigation Investigation `json:"investigation"`
	Revision      Revision      `json:"revision"`
	Brief         ChangeBrief   `json:"brief"`
}

type InvestigationWorkbench interface {
	Preview(context.Context, string, WorkbenchPlan) (*WorkbenchPreview, error)
	Create(context.Context, string, WorkbenchMutationRequest) (*WorkbenchView, error)
	Revise(context.Context, string, WorkbenchMutationRequest) (*WorkbenchView, error)
	Read(context.Context, string, string) (*WorkbenchView, error)
}

// InvestigationWorkbenchService is the shared T21.3 boundary. HTTP, UI, and
// MCP adapters may project it but cannot resolve evidence or write around it.
type InvestigationWorkbenchService struct {
	Store         *Surreal
	Resolver      WorkbenchResolver
	Compatibility compat.Service
}

var _ InvestigationWorkbench = InvestigationWorkbenchService{}

func canonicalWorkbenchStrings(name string, values []string, limit int) ([]string, error) {
	if len(values) == 0 || len(values) > limit {
		return nil, fmt.Errorf("%s must contain 1..%d entries", name, limit)
	}
	values = slices.Clone(values)
	for _, value := range values {
		if strings.TrimSpace(value) != value || value == "" ||
			!utf8.ValidString(value) || len(value) > maxWorkbenchIDBytes {
			return nil, fmt.Errorf("%s contains an invalid value", name)
		}
	}
	slices.Sort(values)
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return nil, fmt.Errorf("%s contains duplicate %q", name, values[index])
		}
	}
	return values, nil
}

func validWorkbenchCapability(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func validWorkbenchCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func changeBriefFromWorkbenchDraft(
	ctx context.Context,
	principal string,
	draft WorkbenchChangeBriefDraft,
) (ChangeBrief, WorkbenchEstimate, error) {
	proposalBytes := 0
	var proposal *ChangeBriefProposal
	if len(draft.What.ProposalSources) > 0 {
		if len(draft.What.ProposalSources) > maxChangeBriefProposalFiles {
			return ChangeBrief{}, WorkbenchEstimate{}, fmt.Errorf(
				"proposal files exceed %d entries",
				maxChangeBriefProposalFiles,
			)
		}
		files := make([]ChangeBriefProposalFile, len(draft.What.ProposalSources))
		for index, source := range draft.What.ProposalSources {
			if !utf8.ValidString(source.Content) {
				return ChangeBrief{}, WorkbenchEstimate{}, fmt.Errorf(
					"proposal file %d is not valid UTF-8",
					index,
				)
			}
			size := len(source.Content)
			if size < 1 || size > maxChangeBriefProposalFileBytes {
				return ChangeBrief{}, WorkbenchEstimate{}, fmt.Errorf(
					"proposal file %d size is outside 1..%d",
					index,
					maxChangeBriefProposalFileBytes,
				)
			}
			proposalBytes += size
			if proposalBytes > maxChangeBriefProposalBytes {
				return ChangeBrief{}, WorkbenchEstimate{}, fmt.Errorf(
					"proposal source set exceeds %d bytes",
					maxChangeBriefProposalBytes,
				)
			}
			hash := sha256.Sum256([]byte(source.Content))
			files[index] = ChangeBriefProposalFile{
				Path:        source.Path,
				ContentHash: "sha256:" + hex.EncodeToString(hash[:]),
				SizeBytes:   size,
			}
		}
		proposal = &ChangeBriefProposal{
			Protocol: draft.What.ProposalProtocol,
			Files:    files,
		}
	} else if draft.What.ProposalProtocol != "" {
		return ChangeBrief{}, WorkbenchEstimate{}, errors.New(
			"proposal protocol requires proposal sources",
		)
	}
	brief, err := normalizeChangeBriefPayload(ChangeBrief{
		TicketKind:        draft.TicketKind,
		Problem:           draft.Problem,
		DesiredOutcome:    draft.DesiredOutcome,
		SuccessCriteria:   draft.SuccessCriteria,
		NonGoals:          draft.NonGoals,
		Assumptions:       draft.Assumptions,
		OpenQuestions:     draft.OpenQuestions,
		ExternalReference: draft.ExternalReference,
		What: ChangeBriefWhat{
			Selections: draft.What.Selections,
			Proposal:   proposal,
		},
		Creator: principal,
	})
	if err != nil {
		return ChangeBrief{}, WorkbenchEstimate{}, err
	}
	if brief.What.Proposal != nil {
		sources := make(
			[]idlpreview.Source,
			len(draft.What.ProposalSources),
		)
		for index, source := range draft.What.ProposalSources {
			sources[index] = idlpreview.Source{
				Path: source.Path, Content: source.Content,
			}
		}
		if err := idlpreview.Validate(
			ctx,
			brief.What.Proposal.Protocol,
			sources,
		); err != nil {
			return ChangeBrief{}, WorkbenchEstimate{}, fmt.Errorf(
				"proposal preview: %w",
				err,
			)
		}
	}
	if err := validateWorkbenchWhatCardinality(
		brief.TicketKind,
		brief.What,
	); err != nil {
		return ChangeBrief{}, WorkbenchEstimate{}, err
	}
	return brief, WorkbenchEstimate{
		ProposalFiles: len(draft.What.ProposalSources),
		ProposalBytes: proposalBytes,
	}, nil
}

func workbenchExactIdentityKey(
	value ChangeBriefContractSelection,
) string {
	return strings.Join([]string{
		value.Protocol,
		value.Repository,
		value.DeclarationLineage,
		value.CanonicalOperation,
	}, "\x00")
}

func validateWorkbenchWhatCardinality(
	kind ChangeBriefTicketKind,
	what ChangeBriefWhat,
) error {
	byRole := make(map[ChangeBriefSelectionRole]int)
	exactIdentities := make(map[string]ChangeBriefSelectionRole)
	for _, selection := range what.Selections {
		byRole[selection.Role]++
		key := workbenchExactIdentityKey(selection)
		if previous, duplicate := exactIdentities[key]; duplicate {
			return fmt.Errorf(
				"exact contract identity repeats roles %q and %q",
				previous,
				selection.Role,
			)
		}
		exactIdentities[key] = selection.Role
	}
	current := byRole[ChangeBriefCurrent]
	replacement := byRole[ChangeBriefReplacement]
	switch kind {
	case ChangeBriefAdd:
		if current != 0 || replacement != 0 || what.Proposal == nil {
			return errors.New(
				"add requires a proposal and no current or replacement endpoint",
			)
		}
	case ChangeBriefModify:
		if current != 1 || replacement != 0 || what.Proposal == nil {
			return errors.New(
				"modify requires one current endpoint and one proposal",
			)
		}
	case ChangeBriefMigrate:
		if current != 1 || replacement != 1 || what.Proposal != nil {
			return errors.New(
				"migrate requires distinct current and replacement endpoints and no proposal",
			)
		}
	case ChangeBriefRetire:
		if current != 1 || replacement != 0 || what.Proposal != nil {
			return errors.New(
				"retire requires one current endpoint and no replacement or proposal",
			)
		}
	default:
		return fmt.Errorf("invalid change brief ticket kind %q", kind)
	}
	return nil
}

func workbenchBriefCommitment(brief ChangeBrief) WorkbenchBriefCommitment {
	return WorkbenchBriefCommitment{
		SchemaVersion:     brief.SchemaVersion,
		TicketKind:        brief.TicketKind,
		Problem:           brief.Problem,
		DesiredOutcome:    brief.DesiredOutcome,
		SuccessCriteria:   brief.SuccessCriteria,
		NonGoals:          brief.NonGoals,
		Assumptions:       brief.Assumptions,
		OpenQuestions:     brief.OpenQuestions,
		ExternalReference: brief.ExternalReference,
		What:              brief.What,
		Creator:           brief.Creator,
	}
}

func changeBriefFromWorkbenchCommitment(value WorkbenchBriefCommitment) ChangeBrief {
	return ChangeBrief{
		SchemaVersion:     value.SchemaVersion,
		TicketKind:        value.TicketKind,
		Problem:           value.Problem,
		DesiredOutcome:    value.DesiredOutcome,
		SuccessCriteria:   value.SuccessCriteria,
		NonGoals:          value.NonGoals,
		Assumptions:       value.Assumptions,
		OpenQuestions:     value.OpenQuestions,
		ExternalReference: value.ExternalReference,
		What:              value.What,
		Creator:           value.Creator,
	}
}

func validateWorkbenchRevisionDraft(value WorkbenchRevisionDraft) error {
	for name, field := range map[string]string{
		"normalized question": value.NormalizedQuestion,
		"decision sought":     value.DecisionSought,
		"snapshot policy":     value.SnapshotPolicy,
		"build configuration": value.BuildConfiguration,
		"enumeration method":  value.EnumerationMethod,
	} {
		if err := validateDomainString(name, field, true); err != nil ||
			strings.TrimSpace(field) != field {
			if err != nil {
				return err
			}
			return fmt.Errorf("%s must be canonical", name)
		}
	}
	return nil
}

func workbenchSelectionKey(value ChangeBriefContractSelection) string {
	return strings.Join([]string{
		string(value.Role),
		value.Protocol,
		value.Repository,
		value.DeclarationLineage,
		value.CanonicalOperation,
	}, "\x00")
}

func workbenchBlocker(code, input string) WorkbenchBlocker {
	return WorkbenchBlocker{Code: code, Input: input}
}

func sortWorkbenchBlockers(values []WorkbenchBlocker) {
	slices.SortFunc(values, func(left, right WorkbenchBlocker) int {
		if left.Code != right.Code {
			return strings.Compare(left.Code, right.Code)
		}
		return strings.Compare(left.Input, right.Input)
	})
}

func (service InvestigationWorkbenchService) Preview(
	ctx context.Context,
	principal string,
	plan WorkbenchPlan,
) (*WorkbenchPreview, error) {
	return service.preview(ctx, principal, plan, "")
}

func (service InvestigationWorkbenchService) preview(
	ctx context.Context,
	principal string,
	plan WorkbenchPlan,
	committedRevisionID string,
) (*WorkbenchPreview, error) {
	if service.Store == nil || service.Resolver == nil {
		return nil, errors.New("workbench store and resolver are required")
	}
	if !validInvestigationPrincipal(principal) {
		return nil, ErrNotFound
	}
	operation := "create"
	nextSequence := 1
	if plan.InvestigationID != "" || plan.ExpectedRevisionID != "" {
		operation = "revise"
		if !validULID(plan.InvestigationID) ||
			!strings.HasPrefix(plan.ExpectedRevisionID, "ivr_") {
			return nil, ErrNotFound
		}
		parent, err := service.Store.GetInvestigationAs(
			ctx,
			principal,
			plan.InvestigationID,
		)
		if err != nil || parent.Owner != principal {
			return nil, ErrNotFound
		}
		if plan.Referent != parent.Referent ||
			plan.ClaimFamily != parent.ClaimFamily ||
			plan.Title != parent.Title {
			return nil, fmt.Errorf(
				"workbench plan changes parent identity: %w",
				ErrConflict,
			)
		}
		revision, err := service.Store.GetRevisionAs(
			ctx,
			principal,
			plan.ExpectedRevisionID,
		)
		if err != nil || revision.InvestigationID != parent.ID {
			return nil, fmt.Errorf(
				"workbench expected revision is unavailable: %w",
				ErrConflict,
			)
		}
		nextSequence = revision.Seq + 1
	}
	for name, value := range map[string]string{
		"referent":     plan.Referent,
		"claim family": plan.ClaimFamily,
		"title":        plan.Title,
	} {
		if err := validateDomainString(name, value, true); err != nil ||
			strings.TrimSpace(value) != value {
			if err != nil {
				return nil, invalidWorkbench(err)
			}
			return nil, invalidWorkbench(fmt.Errorf("%s must be canonical", name))
		}
	}
	if err := validateWorkbenchRevisionDraft(plan.Revision); err != nil {
		return nil, invalidWorkbench(err)
	}
	var err error
	plan.Repositories, err = canonicalWorkbenchStrings(
		"workbench repositories",
		plan.Repositories,
		maxWorkbenchRepositories,
	)
	if err != nil {
		return nil, invalidWorkbench(err)
	}
	plan.Capabilities, err = canonicalWorkbenchStrings(
		"workbench capabilities",
		plan.Capabilities,
		maxWorkbenchCapabilities,
	)
	if err != nil {
		return nil, invalidWorkbench(err)
	}
	for _, capability := range plan.Capabilities {
		if !validWorkbenchCapability(capability) {
			return nil, invalidWorkbench(fmt.Errorf(
				"invalid workbench capability %q",
				capability,
			))
		}
	}
	brief, estimate, err := changeBriefFromWorkbenchDraft(
		ctx,
		principal,
		plan.Brief,
	)
	if err != nil {
		return nil, invalidWorkbench(fmt.Errorf("workbench brief: %w", err))
	}
	repositorySet := make(map[string]struct{}, len(plan.Repositories))
	for _, repository := range plan.Repositories {
		repositorySet[repository] = struct{}{}
	}
	for _, selection := range brief.What.Selections {
		if _, ok := repositorySet[selection.Repository]; !ok {
			return nil, invalidWorkbench(fmt.Errorf(
				"selection repository %q is outside the requested repositories",
				selection.Repository,
			))
		}
	}
	resolution, err := service.Resolver.ResolveWorkbench(
		ctx,
		principal,
		WorkbenchResolutionRequest{
			Repositories: plan.Repositories,
			Selections:   brief.What.Selections,
			Capabilities: plan.Capabilities,
		},
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve workbench preview: %w", err)
	}
	if !validSHA256Digest(resolution.AuthorizationDigest) {
		return nil, errors.New("resolve workbench preview: invalid authorization digest")
	}

	preview := &WorkbenchPreview{
		SchemaVersion:        WorkbenchPreviewSchemaVersion,
		Operation:            operation,
		InvestigationID:      plan.InvestigationID,
		ExpectedRevisionID:   plan.ExpectedRevisionID,
		NextRevisionSequence: nextSequence,
		Referent:             plan.Referent,
		ClaimFamily:          plan.ClaimFamily,
		Title:                plan.Title,
		Brief:                workbenchBriefCommitment(brief),
		AuthorizationDigest:  resolution.AuthorizationDigest,
		Repositories:         []WorkbenchRepositorySnapshot{},
		Endpoints:            []WorkbenchEndpointSnapshot{},
		Capabilities:         []WorkbenchCapabilitySnapshot{},
		Blockers:             []WorkbenchBlocker{},
	}
	if operation == "revise" {
		parent, readErr := service.Store.GetInvestigationAs(ctx, principal, plan.InvestigationID)
		if readErr != nil || parent.Owner != principal {
			return nil, ErrNotFound
		}
		if committedRevisionID == "" && parent.Lifecycle == "archived" {
			preview.Blockers = append(preview.Blockers, workbenchBlocker(
				"INVESTIGATION_ARCHIVED",
				"",
			))
		}
		if committedRevisionID == "" &&
			parent.CurrentRevisionID != plan.ExpectedRevisionID {
			preview.Blockers = append(preview.Blockers, workbenchBlocker(
				"CURRENT_REVISION_CHANGED",
				"",
			))
		}
	}

	resolvedRepositories := make(map[string]WorkbenchRepositorySnapshot)
	for _, repository := range resolution.Repositories {
		if _, requested := repositorySet[repository.Name]; !requested ||
			!validWorkbenchCommit(repository.Commit) {
			return nil, errors.New("resolve workbench preview: invalid repository snapshot")
		}
		if _, duplicate := resolvedRepositories[repository.Name]; duplicate {
			return nil, errors.New("resolve workbench preview: duplicate repository snapshot")
		}
		resolvedRepositories[repository.Name] = repository
	}
	for _, name := range plan.Repositories {
		repository, ok := resolvedRepositories[name]
		if !ok {
			preview.Blockers = append(preview.Blockers, workbenchBlocker(
				"REPOSITORY_NOT_AVAILABLE",
				name,
			))
			continue
		}
		preview.Repositories = append(preview.Repositories, repository)
	}

	requestedSelections := make(map[string]ChangeBriefContractSelection)
	for _, selection := range brief.What.Selections {
		requestedSelections[workbenchSelectionKey(selection)] = selection
	}
	resolvedEndpoints := make(map[string]WorkbenchEndpointSnapshot)
	for _, endpoint := range resolution.Endpoints {
		normalized, normalizeErr := normalizeChangeBriefSelection(endpoint.Selection)
		if normalizeErr != nil ||
			!validWorkbenchCommit(endpoint.DeclarationCommit) ||
			!validSHA256Digest(endpoint.DeclarationDigest) {
			return nil, errors.New("resolve workbench preview: invalid endpoint snapshot")
		}
		endpoint.Selection = normalized
		if err := validateWorkbenchDeclarationSources(endpoint); err != nil {
			return nil, fmt.Errorf(
				"resolve workbench preview: %w",
				err,
			)
		}
		key := workbenchSelectionKey(normalized)
		if _, requested := requestedSelections[key]; !requested {
			return nil, errors.New("resolve workbench preview: unexpected endpoint snapshot")
		}
		repository, available := resolvedRepositories[normalized.Repository]
		if !available || endpoint.DeclarationCommit != repository.Commit {
			return nil, errors.New(
				"resolve workbench preview: endpoint snapshot is outside the repository snapshot",
			)
		}
		if _, duplicate := resolvedEndpoints[key]; duplicate {
			return nil, errors.New("resolve workbench preview: duplicate endpoint snapshot")
		}
		resolvedEndpoints[key] = endpoint
	}
	for _, selection := range brief.What.Selections {
		key := workbenchSelectionKey(selection)
		endpoint, ok := resolvedEndpoints[key]
		if !ok {
			preview.Blockers = append(preview.Blockers, workbenchBlocker(
				"ENDPOINT_NOT_AVAILABLE",
				strings.Join([]string{
					string(selection.Role),
					selection.Protocol,
					selection.Repository,
					selection.DeclarationLineage,
					selection.CanonicalOperation,
				}, ":"),
			))
			continue
		}
		preview.Endpoints = append(preview.Endpoints, endpoint)
	}

	requestedCapabilities := make(map[string]struct{}, len(plan.Capabilities))
	for _, capability := range plan.Capabilities {
		requestedCapabilities[capability] = struct{}{}
	}
	resolvedCapabilities := make(map[string]WorkbenchCapabilitySnapshot)
	for _, capability := range resolution.Capabilities {
		if _, requested := requestedCapabilities[capability.ID]; !requested ||
			!validWorkbenchCapability(capability.ID) {
			return nil, errors.New("resolve workbench preview: unexpected capability snapshot")
		}
		if _, duplicate := resolvedCapabilities[capability.ID]; duplicate {
			return nil, errors.New("resolve workbench preview: duplicate capability snapshot")
		}
		if capability.Available &&
			(strings.TrimSpace(capability.Version) == "" ||
				strings.TrimSpace(capability.Version) != capability.Version ||
				len(capability.Version) > maxWorkbenchIDBytes ||
				!validSHA256Digest(capability.ContentDigest)) {
			return nil, errors.New("resolve workbench preview: invalid available capability")
		}
		if !capability.Available {
			capability.Version = ""
			capability.ContentDigest = ""
		}
		resolvedCapabilities[capability.ID] = capability
	}
	for _, id := range plan.Capabilities {
		capability, ok := resolvedCapabilities[id]
		if !ok || !capability.Available {
			preview.Blockers = append(preview.Blockers, workbenchBlocker(
				"CAPABILITY_NOT_AVAILABLE",
				id,
			))
			continue
		}
		preview.Capabilities = append(preview.Capabilities, capability)
	}
	preview.Compatibility = workbenchCompatibilityPreview(
		brief,
		preview.Capabilities,
		service.Compatibility != nil,
	)

	declaredUniverseBytes, err := json.Marshal(struct {
		Repositories []WorkbenchRepositorySnapshot `json:"repositories"`
	}{Repositories: preview.Repositories})
	if err != nil {
		return nil, err
	}
	if len(declaredUniverseBytes) > maxInvestigationFieldBytes {
		preview.Blockers = append(preview.Blockers, workbenchBlocker(
			"DECLARED_UNIVERSE_TOO_LARGE",
			"",
		))
	}
	preview.Revision = WorkbenchRevisionCommitment{
		NormalizedQuestion: plan.Revision.NormalizedQuestion,
		DecisionSought:     plan.Revision.DecisionSought,
		DeclaredUniverse:   string(declaredUniverseBytes),
		SnapshotPolicy:     plan.Revision.SnapshotPolicy,
		BuildConfiguration: plan.Revision.BuildConfiguration,
		PackSelection:      strings.Join(plan.Capabilities, ","),
		EnumerationMethod:  plan.Revision.EnumerationMethod,
	}
	estimate.RepositoryCount = len(preview.Repositories)
	estimate.EndpointCount = len(preview.Endpoints)
	estimate.CapabilityCount = len(preview.Capabilities)
	estimate.AnalysisUnits = estimate.RepositoryCount +
		estimate.EndpointCount + estimate.ProposalFiles
	preview.Estimate = estimate
	sortWorkbenchBlockers(preview.Blockers)
	preview.Ready = len(preview.Blockers) == 0
	if !preview.Ready {
		preview.AuthorizationDigest = ""
		return preview, nil
	}
	preview.PreviewDigest, err = contentDigest(struct {
		SchemaVersion string            `json:"schema_version"`
		Principal     string            `json:"principal"`
		Preview       *WorkbenchPreview `json:"preview"`
	}{
		SchemaVersion: WorkbenchPreviewSchemaVersion,
		Principal:     principal,
		Preview:       preview,
	})
	if err != nil {
		return nil, err
	}
	return preview, nil
}

func validateWorkbenchDeclarationSources(
	endpoint WorkbenchEndpointSnapshot,
) error {
	if len(endpoint.DeclarationSources) == 0 ||
		len(endpoint.DeclarationSources) > 16 {
		return errors.New("endpoint snapshot has invalid declaration sources")
	}
	previous := ""
	for _, source := range endpoint.DeclarationSources {
		if source.Repository != endpoint.Selection.Repository ||
			source.Commit != endpoint.DeclarationCommit ||
			source.Path == "" || len(source.Path) > 4_096 ||
			strings.TrimSpace(source.Path) != source.Path ||
			strings.Contains(source.Path, "\\") ||
			source.StartByte < 0 || source.EndByte <= source.StartByte ||
			source.StartLine < 1 || source.EndLine < source.StartLine ||
			source.AssertionID == "" || source.RunID == "" ||
			source.AtomID == "" {
			return errors.New("endpoint snapshot has invalid declaration source")
		}
		key := strings.Join([]string{
			source.Repository,
			source.Commit,
			source.Path,
			fmt.Sprintf("%010d", source.StartByte),
			fmt.Sprintf("%010d", source.EndByte),
			source.AtomID,
		}, "\x00")
		if previous != "" && key <= previous {
			return errors.New(
				"endpoint declaration sources are not unique and sorted",
			)
		}
		previous = key
	}
	return nil
}

func workbenchCompatibilityPreview(
	brief ChangeBrief,
	capabilities []WorkbenchCapabilitySnapshot,
	checkerAvailable bool,
) WorkbenchCompatibilityPreview {
	proposal := brief.What.Proposal
	if proposal == nil {
		return WorkbenchCompatibilityPreview{
			Status: "unavailable", Reason: "proposal_required",
		}
	}
	result := WorkbenchCompatibilityPreview{
		Status: "unavailable", Protocol: proposal.Protocol,
	}
	if proposal.Protocol != "protobuf" {
		result.Reason = "compatibility_protocol_unsupported"
		return result
	}
	limits := compat.WireLimits()
	result.Limits = &limits
	if brief.TicketKind != ChangeBriefModify {
		result.Reason = "current_endpoint_required"
		return result
	}
	currentProtocol := ""
	currentCount := 0
	for _, selection := range brief.What.Selections {
		if selection.Role == ChangeBriefCurrent {
			currentCount++
			currentProtocol = selection.Protocol
		}
	}
	if currentCount != 1 || currentProtocol != "protobuf" {
		result.Reason = "current_endpoint_protocol_unsupported"
		return result
	}
	capabilityAvailable := false
	for _, capability := range capabilities {
		if capability.ID == "contract-compatibility" &&
			capability.Available {
			capabilityAvailable = true
			break
		}
	}
	if !checkerAvailable || !capabilityAvailable {
		result.Reason = "compatibility_capability_unavailable"
		return result
	}
	result.Status = "available"
	return result
}

func validateWorkbenchMutationRequest(request WorkbenchMutationRequest) error {
	if strings.TrimSpace(request.IdempotencyKey) != request.IdempotencyKey ||
		request.IdempotencyKey == "" ||
		len(request.IdempotencyKey) > maxWorkbenchIDBytes ||
		!utf8.ValidString(request.IdempotencyKey) {
		return errors.New("workbench idempotency key is invalid")
	}
	if !validSHA256Digest(request.PreviewDigest) {
		return errors.New("workbench preview digest is invalid")
	}
	return nil
}

func (service InvestigationWorkbenchService) commit(
	ctx context.Context,
	principal string,
	request WorkbenchMutationRequest,
	operation string,
) (*WorkbenchView, error) {
	if service.Store == nil || service.Resolver == nil {
		return nil, errors.New("workbench store and resolver are required")
	}
	if err := validateWorkbenchMutationRequest(request); err != nil {
		return nil, invalidWorkbench(err)
	}
	committedRevisionID := ""
	if existing, getErr := service.Store.getWorkbenchMutationRecord(
		ctx,
		principal,
		request.IdempotencyKey,
	); getErr == nil &&
		workbenchMutationMatchesPlan(existing, operation, request.Plan) {
		committedRevisionID = existing.RevisionID
	} else if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return nil, getErr
	}
	preview, err := service.preview(
		ctx,
		principal,
		request.Plan,
		committedRevisionID,
	)
	if err != nil {
		return nil, err
	}
	if committedRevisionID == "" &&
		(!preview.Ready || preview.PreviewDigest != request.PreviewDigest) {
		if existing, getErr := service.Store.getWorkbenchMutationRecord(
			ctx,
			principal,
			request.IdempotencyKey,
		); getErr == nil &&
			workbenchMutationMatchesPlan(existing, operation, request.Plan) {
			committedRevisionID = existing.RevisionID
			preview, err = service.preview(
				ctx,
				principal,
				request.Plan,
				committedRevisionID,
			)
			if err != nil {
				return nil, err
			}
		} else if getErr != nil && !errors.Is(getErr, ErrNotFound) {
			return nil, getErr
		}
	}
	if preview.Operation != operation {
		return nil, fmt.Errorf("workbench operation does not match plan: %w", ErrConflict)
	}
	if !preview.Ready || preview.PreviewDigest != request.PreviewDigest {
		return nil, fmt.Errorf("workbench preview changed: %w", ErrConflict)
	}
	return service.Store.commitWorkbenchMutation(ctx, workbenchCommitRequest{
		Principal:            principal,
		IdempotencyKey:       request.IdempotencyKey,
		PreviewDigest:        request.PreviewDigest,
		Operation:            operation,
		InvestigationID:      preview.InvestigationID,
		ExpectedRevisionID:   preview.ExpectedRevisionID,
		NextRevisionSequence: preview.NextRevisionSequence,
		Referent:             preview.Referent,
		ClaimFamily:          preview.ClaimFamily,
		Title:                preview.Title,
		Revision:             preview.Revision,
		Brief:                preview.Brief,
	})
}

func workbenchMutationMatchesPlan(
	record *workbenchMutationRecord,
	operation string,
	plan WorkbenchPlan,
) bool {
	if record == nil || record.Operation != operation {
		return false
	}
	if operation == "create" {
		return plan.InvestigationID == "" && plan.ExpectedRevisionID == ""
	}
	return record.InvestigationID == plan.InvestigationID
}

func (service InvestigationWorkbenchService) Create(
	ctx context.Context,
	principal string,
	request WorkbenchMutationRequest,
) (*WorkbenchView, error) {
	return service.commit(ctx, principal, request, "create")
}

func (service InvestigationWorkbenchService) Revise(
	ctx context.Context,
	principal string,
	request WorkbenchMutationRequest,
) (*WorkbenchView, error) {
	return service.commit(ctx, principal, request, "revise")
}

func (service InvestigationWorkbenchService) Read(
	ctx context.Context,
	principal, investigationID string,
) (*WorkbenchView, error) {
	if service.Store == nil || !validInvestigationPrincipal(principal) ||
		!validULID(investigationID) {
		return nil, ErrNotFound
	}
	investigation, err := service.Store.GetInvestigationAs(
		ctx,
		principal,
		investigationID,
	)
	if err != nil {
		return nil, err
	}
	if investigation.CurrentRevisionID == "" {
		return nil, ErrNotFound
	}
	revision, err := service.Store.GetRevisionAs(
		ctx,
		principal,
		investigation.CurrentRevisionID,
	)
	if err != nil {
		return nil, err
	}
	if revision.InvestigationID != investigation.ID {
		return nil, errors.New("read workbench: stored Revision binding is inconsistent")
	}
	brief, err := service.Store.GetChangeBriefForRevisionAs(
		ctx,
		principal,
		revision.ID,
	)
	if err != nil {
		return nil, err
	}
	if brief.InvestigationID != investigation.ID {
		return nil, errors.New("read workbench: stored Change Brief binding is inconsistent")
	}
	return &WorkbenchView{
		Investigation: *investigation,
		Revision:      *revision,
		Brief:         *brief,
	}, nil
}

type workbenchCommitRequest struct {
	Principal            string
	IdempotencyKey       string
	PreviewDigest        string
	Operation            string
	InvestigationID      string
	ExpectedRevisionID   string
	NextRevisionSequence int
	Referent             string
	ClaimFamily          string
	Title                string
	Revision             WorkbenchRevisionCommitment
	Brief                WorkbenchBriefCommitment
}

type workbenchMutationRecord struct {
	MutationKey     string `json:"mutation_key"`
	Principal       string `json:"principal"`
	IdempotencyKey  string `json:"idempotency_key"`
	RequestDigest   string `json:"request_digest"`
	Operation       string `json:"operation"`
	InvestigationID string `json:"investigation_id"`
	RevisionID      string `json:"revision_id"`
	BriefID         string `json:"brief_id"`
}

func workbenchMutationRecordID(principal, idempotencyKey string) models.RecordID {
	return models.NewRecordID(
		"investigation_workbench_mutation",
		hashIdentity("iwm_", principal, idempotencyKey),
	)
}

func workbenchCommitCore(request workbenchCommitRequest) any {
	return struct {
		Principal, IdempotencyKey, PreviewDigest, Operation string
		InvestigationID, ExpectedRevisionID                 string
		NextRevisionSequence                                int
		Referent, ClaimFamily, Title                        string
		Revision                                            WorkbenchRevisionCommitment
		Brief                                               WorkbenchBriefCommitment
	}{
		request.Principal,
		request.IdempotencyKey,
		request.PreviewDigest,
		request.Operation,
		request.InvestigationID,
		request.ExpectedRevisionID,
		request.NextRevisionSequence,
		request.Referent,
		request.ClaimFamily,
		request.Title,
		request.Revision,
		request.Brief,
	}
}

func (s *Surreal) getWorkbenchMutationRecord(
	ctx context.Context,
	principal, idempotencyKey string,
) (*workbenchMutationRecord, error) {
	results, err := surrealdb.Query[[]workbenchMutationRecord](
		ctx,
		s.db,
		`SELECT mutation_key, principal, idempotency_key, request_digest,
			operation, investigation_id, revision_id, brief_id
			FROM $rid LIMIT 1`,
		map[string]any{
			"rid": workbenchMutationRecordID(principal, idempotencyKey),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("get workbench mutation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	if rows[0].Principal != principal ||
		rows[0].IdempotencyKey != idempotencyKey ||
		rows[0].MutationKey !=
			hashIdentity("iwm_", principal, idempotencyKey) {
		return nil, errors.New(
			"get workbench mutation: stored receipt binding is inconsistent",
		)
	}
	return &rows[0], nil
}

const createWorkbenchSQL = `
BEGIN;
LET $existing = SELECT mutation_key, principal, idempotency_key, request_digest,
	operation, investigation_id, revision_id, brief_id
	FROM $mutation_rid LIMIT 1;
LET $saved = IF array::len($existing) = 0 THEN
	(CREATE $mutation_rid SET mutation_key = $mutation_key,
		principal = $principal, idempotency_key = $idempotency_key,
		request_digest = $request_digest, operation = $operation,
		investigation_id = $investigation_id, revision_id = $revision_id,
		brief_id = $brief_id, created_at = $now RETURN AFTER)
	ELSE $existing END;
IF array::len($existing) = 0 {
	CREATE $investigation_rid SET investigation_id = $investigation_id,
		referent = $referent, claim_family = $claim_family, title = $title,
		owner = $principal, lifecycle = 'active',
		current_revision_id = $revision_id, authz_revision = 0,
		authz_write_revision = 0, created_at = $now, updated_at = $now
		RETURN NONE;
	CREATE $revision_rid SET revision_id = $revision_id,
		investigation_id = $investigation_id, seq = 1,
		normalized_question = $normalized_question,
		decision_sought = $decision_sought,
		declared_universe = $declared_universe,
		snapshot_policy = $snapshot_policy,
		build_configuration = $build_configuration,
		pack_selection = $pack_selection,
		enumeration_method = $enumeration_method, creator = $principal,
		created_at = $now, content_digest = $revision_digest RETURN NONE;
	CREATE $brief_rid SET brief_id = $brief_id,
		investigation_id = $investigation_id, revision_id = $revision_id,
		schema_version = $brief_schema_version, content = $brief_content,
		content_digest = $brief_digest, created_at = $now RETURN NONE;
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 201, created_at = $now RETURN NONE;
};
RETURN $saved;
COMMIT;`

const reviseWorkbenchSQL = `
BEGIN;
LET $existing = SELECT mutation_key, principal, idempotency_key, request_digest,
	operation, investigation_id, revision_id, brief_id
	FROM $mutation_rid LIMIT 1;
LET $locked = IF array::len($existing) = 0 THEN
	(UPDATE $investigation_rid SET current_revision_id = $revision_id,
		updated_at = $now
		WHERE investigation_id = $investigation_id AND owner = $principal
		  AND lifecycle != 'archived'
		  AND (current_revision_id ?? '') = $expected_revision_id
		  AND array::len(SELECT VALUE revision_id FROM $expected_revision_rid
			WHERE revision_id = $expected_revision_id
			  AND investigation_id = $investigation_id
			  AND seq = $previous_sequence LIMIT 1) = 1
		  AND array::len(SELECT VALUE revision_id FROM $revision_rid LIMIT 1) = 0
		  AND array::len(SELECT VALUE brief_id FROM $brief_rid LIMIT 1) = 0
		RETURN AFTER)
	ELSE [] END;
LET $created = IF array::len($locked) = 1 THEN
	(CREATE $mutation_rid SET mutation_key = $mutation_key,
		principal = $principal, idempotency_key = $idempotency_key,
		request_digest = $request_digest, operation = $operation,
		investigation_id = $investigation_id, revision_id = $revision_id,
		brief_id = $brief_id, created_at = $now RETURN AFTER)
	ELSE [] END;
LET $saved = IF array::len($existing) = 1 THEN $existing ELSE $created END;
IF array::len($locked) = 1 AND array::len($saved) = 1 {
	CREATE $revision_rid SET revision_id = $revision_id,
		investigation_id = $investigation_id, seq = $sequence,
		normalized_question = $normalized_question,
		decision_sought = $decision_sought,
		declared_universe = $declared_universe,
		snapshot_policy = $snapshot_policy,
		build_configuration = $build_configuration,
		pack_selection = $pack_selection,
		enumeration_method = $enumeration_method, creator = $principal,
		created_at = $now, content_digest = $revision_digest RETURN NONE;
	CREATE $brief_rid SET brief_id = $brief_id,
		investigation_id = $investigation_id, revision_id = $revision_id,
		schema_version = $brief_schema_version, content = $brief_content,
		content_digest = $brief_digest, created_at = $now RETURN NONE;
	CREATE $audit_rid SET action = $audit_action, target = $audit_target,
		actor_id = $audit_actor, status = 200, created_at = $now RETURN NONE;
};
RETURN $saved;
COMMIT;`

func (s *Surreal) readWorkbenchMutation(
	ctx context.Context,
	record *workbenchMutationRecord,
) (*WorkbenchView, error) {
	if record == nil {
		return nil, ErrNotFound
	}
	investigation, err := s.GetInvestigationAs(
		ctx,
		record.Principal,
		record.InvestigationID,
	)
	if err != nil {
		return nil, err
	}
	revision, err := s.GetRevisionAs(ctx, record.Principal, record.RevisionID)
	if err != nil {
		return nil, err
	}
	brief, err := s.GetChangeBriefAs(ctx, record.Principal, record.BriefID)
	if err != nil {
		return nil, err
	}
	if revision.InvestigationID != investigation.ID ||
		brief.InvestigationID != investigation.ID ||
		brief.RevisionID != revision.ID {
		return nil, errors.New("read workbench mutation: stored object graph is inconsistent")
	}
	return &WorkbenchView{
		Investigation: *investigation,
		Revision:      *revision,
		Brief:         *brief,
	}, nil
}

func (s *Surreal) commitWorkbenchMutation(
	ctx context.Context,
	request workbenchCommitRequest,
) (*WorkbenchView, error) {
	requestDigest, err := contentDigest(workbenchCommitCore(request))
	if err != nil {
		return nil, err
	}
	if existing, getErr := s.getWorkbenchMutationRecord(
		ctx,
		request.Principal,
		request.IdempotencyKey,
	); getErr == nil {
		if existing.RequestDigest != requestDigest {
			return nil, fmt.Errorf(
				"commit workbench mutation: idempotency key reused: %w",
				ErrConflict,
			)
		}
		return s.readWorkbenchMutation(ctx, existing)
	} else if !errors.Is(getErr, ErrNotFound) {
		return nil, getErr
	}

	now := storeTimestamp(time.Now())
	investigationID := request.InvestigationID
	if request.Operation == "create" {
		investigationID, err = newULID(now)
		if err != nil {
			return nil, err
		}
	}
	revision := Revision{
		ID:                 revisionIdentity(investigationID, request.NextRevisionSequence),
		InvestigationID:    investigationID,
		Seq:                request.NextRevisionSequence,
		NormalizedQuestion: request.Revision.NormalizedQuestion,
		DecisionSought:     request.Revision.DecisionSought,
		DeclaredUniverse:   request.Revision.DeclaredUniverse,
		SnapshotPolicy:     request.Revision.SnapshotPolicy,
		BuildConfiguration: request.Revision.BuildConfiguration,
		PackSelection:      request.Revision.PackSelection,
		EnumerationMethod:  request.Revision.EnumerationMethod,
		Creator:            request.Principal,
	}
	if err := validateRevision(&revision); err != nil {
		return nil, fmt.Errorf("commit workbench mutation: revision: %w", err)
	}
	brief := changeBriefFromWorkbenchCommitment(request.Brief)
	brief.InvestigationID = investigationID
	brief.RevisionID = revision.ID
	brief, briefContent, err := normalizeChangeBrief(brief)
	if err != nil {
		return nil, fmt.Errorf("commit workbench mutation: brief: %w", err)
	}
	mutationKey := hashIdentity(
		"iwm_",
		request.Principal,
		request.IdempotencyKey,
	)
	vars := map[string]any{
		"mutation_rid": workbenchMutationRecordID(
			request.Principal,
			request.IdempotencyKey,
		),
		"mutation_key":         mutationKey,
		"principal":            request.Principal,
		"idempotency_key":      request.IdempotencyKey,
		"request_digest":       requestDigest,
		"operation":            request.Operation,
		"investigation_id":     investigationID,
		"revision_id":          revision.ID,
		"brief_id":             brief.ID,
		"investigation_rid":    investigationRecordID(investigationID),
		"referent":             request.Referent,
		"claim_family":         request.ClaimFamily,
		"title":                request.Title,
		"revision_rid":         revisionRecordID(revision.ID),
		"sequence":             revision.Seq,
		"normalized_question":  revision.NormalizedQuestion,
		"decision_sought":      revision.DecisionSought,
		"declared_universe":    revision.DeclaredUniverse,
		"snapshot_policy":      revision.SnapshotPolicy,
		"build_configuration":  revision.BuildConfiguration,
		"pack_selection":       revision.PackSelection,
		"enumeration_method":   revision.EnumerationMethod,
		"revision_digest":      revision.ContentDigest,
		"brief_rid":            changeBriefRecordID(brief.ID),
		"brief_schema_version": brief.SchemaVersion,
		"brief_content":        string(briefContent),
		"brief_digest":         brief.ContentDigest,
		"now":                  now,
	}
	query := createWorkbenchSQL
	auditAction := "investigation.workbench.create"
	if request.Operation == "revise" {
		query = reviseWorkbenchSQL
		auditAction = "investigation.workbench.revise"
		vars["expected_revision_id"] = request.ExpectedRevisionID
		vars["expected_revision_rid"] = revisionRecordID(request.ExpectedRevisionID)
		vars["previous_sequence"] = revision.Seq - 1
	}
	if err := addInvestigationAuditVars(
		vars,
		auditAction,
		investigationID,
		request.Principal,
		now,
	); err != nil {
		return nil, fmt.Errorf("commit workbench mutation: audit identity: %w", err)
	}
	for attempt := 0; ; attempt++ {
		results, queryErr := surrealdb.Query[[]workbenchMutationRecord](
			ctx,
			s.db,
			query,
			vars,
		)
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil &&
				attempt+1 < maxQueueRetries {
				if existing, getErr := s.getWorkbenchMutationRecord(
					ctx,
					request.Principal,
					request.IdempotencyKey,
				); getErr == nil {
					if existing.RequestDigest != requestDigest {
						return nil, fmt.Errorf(
							"commit workbench mutation: idempotency key reused: %w",
							ErrConflict,
						)
					}
					return s.readWorkbenchMutation(ctx, existing)
				}
				continue
			}
			recoveryContext, cancelRecovery := context.WithTimeout(
				context.WithoutCancel(ctx),
				workbenchReceiptRecovery,
			)
			defer cancelRecovery()
			if existing, getErr := s.getWorkbenchMutationRecord(
				recoveryContext,
				request.Principal,
				request.IdempotencyKey,
			); getErr == nil {
				if existing.RequestDigest != requestDigest {
					return nil, fmt.Errorf(
						"commit workbench mutation: idempotency key reused: %w",
						ErrConflict,
					)
				}
				return s.readWorkbenchMutation(
					recoveryContext,
					existing,
				)
			}
			return nil, fmt.Errorf("commit workbench mutation: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			if request.Operation == "revise" {
				if _, readErr := s.GetInvestigationAs(
					ctx,
					request.Principal,
					investigationID,
				); readErr != nil {
					return nil, ErrNotFound
				}
				return nil, fmt.Errorf(
					"commit workbench mutation: current revision changed: %w",
					ErrConflict,
				)
			}
			return nil, errors.New("commit workbench mutation returned no row")
		}
		if rows[0].RequestDigest != requestDigest {
			return nil, fmt.Errorf(
				"commit workbench mutation: idempotency key reused: %w",
				ErrConflict,
			)
		}
		return s.readWorkbenchMutation(ctx, &rows[0])
	}
}
