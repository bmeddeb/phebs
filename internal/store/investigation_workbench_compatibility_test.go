package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/compat"
)

type fixtureWorkbenchCompatibility struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (checker *fixtureWorkbenchCompatibility) Check(
	ctx context.Context,
	request compat.Request,
) (*compat.CompatibilityResult, error) {
	prepared, err := compat.Prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	checker.mu.Lock()
	checker.calls++
	checkErr := checker.err
	checker.mu.Unlock()
	if checkErr != nil {
		return nil, checkErr
	}
	return &compat.CompatibilityResult{
		Compatible:     true,
		Before:         prepared.Before,
		After:          prepared.After,
		Violations:     []compat.Violation{},
		AffectedFields: []compat.FieldIdentity{},
		Run: compat.Run{
			Engine: "buf", Version: compat.Version, Policy: compat.Policy,
			Arguments: []string{"breaking"}, ExitCode: 0, Result: "compatible",
		},
	}, nil
}

func (checker *fixtureWorkbenchCompatibility) callCount() int {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return checker.calls
}

func TestWorkbenchRetainedCompatibilityFailureIsTerminalAndIdempotent(
	t *testing.T,
) {
	s := newRunnerStore(t)
	resolver := newFixtureWorkbenchResolver()
	checker := &fixtureWorkbenchCompatibility{
		err: fmt.Errorf("%w: fixture refusal", compat.ErrUnavailable),
	}
	service := InvestigationWorkbenchService{
		Store: s, Resolver: resolver, Compatibility: checker,
	}
	ctx := context.Background()
	plan := workbenchPlanFixture()
	plan.Brief.What.Selections[0].DeclarationLineage =
		"provisional_repo_path_v1_checkout"
	plan.Capabilities = append(
		plan.Capabilities,
		"contract-compatibility",
	)
	preview, err := service.Preview(ctx, workbenchOwner, plan)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(
		ctx,
		workbenchOwner,
		WorkbenchMutationRequest{
			Plan:           plan,
			PreviewDigest:  preview.PreviewDigest,
			IdempotencyKey: "create-for-failed-compatibility",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := WorkbenchCompatibilityAnalysisRequest{
		InvestigationID: created.Investigation.ID,
		RevisionID:      created.Revision.ID,
		IdempotencyKey:  "retain-failed-compatibility-1",
		Before: []compat.File{{
			Path:    "proto/shop.proto",
			Content: "syntax = \"proto3\";\nservice Checkout {}\n",
		}},
		After: []compat.File{{
			Path:    plan.Brief.What.ProposalSources[0].Path,
			Content: plan.Brief.What.ProposalSources[0].Content,
		}},
	}
	first, err := service.RetainCompatibility(
		ctx,
		workbenchOwner,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RetainCompatibility(
		ctx,
		workbenchOwner,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "failed" ||
		first.Run.State != RunFailed ||
		first.Artifact.TerminalStatus != RunFailed ||
		first.Failure != "compatibility engine unavailable" ||
		strings.Contains(first.Failure, "fixture refusal") ||
		first.Run.ID != second.Run.ID ||
		first.Artifact.ID != second.Artifact.ID ||
		checker.callCount() != 1 ||
		!strings.Contains(
			first.Artifact.CoverageLedger,
			`"code":"COMPATIBILITY_CHECK_FAILED","retryable":true`,
		) {
		t.Fatalf(
			"failed analyses = %+v / %+v; calls=%d",
			first,
			second,
			checker.callCount(),
		)
	}
}

func TestWorkbenchTicketCardinalityAndCompatibilityAvailability(t *testing.T) {
	s := newRunnerStore(t)
	resolver := newFixtureWorkbenchResolver()
	checker := &fixtureWorkbenchCompatibility{}
	service := InvestigationWorkbenchService{
		Store: s, Resolver: resolver, Compatibility: checker,
	}
	valid := []struct {
		name   string
		mutate func(*WorkbenchPlan)
		status string
		reason string
	}{
		{
			name: "add",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.TicketKind = ChangeBriefAdd
				plan.Brief.What.Selections = nil
			},
			status: "unavailable", reason: "current_endpoint_required",
		},
		{
			name: "modify",
			mutate: func(plan *WorkbenchPlan) {
				plan.Capabilities = append(
					plan.Capabilities,
					"contract-compatibility",
				)
			},
			status: "available",
		},
		{
			name: "migrate same spelling exact dimensions",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.TicketKind = ChangeBriefMigrate
				plan.Brief.What.ProposalProtocol = ""
				plan.Brief.What.ProposalSources = nil
				plan.Brief.What.Selections = append(
					plan.Brief.What.Selections,
					ChangeBriefContractSelection{
						Role:               ChangeBriefReplacement,
						Protocol:           "thrift",
						Repository:         "example/contracts",
						DeclarationLineage: "thrift/shop.thrift:Checkout",
						CanonicalOperation: "/shop.v1.Checkout/Submit",
					},
				)
			},
			status: "unavailable", reason: "proposal_required",
		},
		{
			name: "retire",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.TicketKind = ChangeBriefRetire
				plan.Brief.What.ProposalProtocol = ""
				plan.Brief.What.ProposalSources = nil
			},
			status: "unavailable", reason: "proposal_required",
		},
		{
			name: "thrift modify",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.What.Selections[0].Protocol = "thrift"
				plan.Brief.What.Selections[0].DeclarationLineage =
					"thrift/shop.thrift:Checkout"
				plan.Brief.What.Selections[0].CanonicalOperation =
					"/Checkout/Submit"
				plan.Brief.What.ProposalProtocol = "thrift"
				plan.Brief.What.ProposalSources = []WorkbenchProposalSource{{
					Path: "thrift/shop.thrift",
					Content: "service Checkout {" +
						" string Submit(1: string id) }\n",
				}}
				plan.Capabilities = append(
					plan.Capabilities,
					"contract-compatibility",
				)
			},
			status: "unavailable",
			reason: "compatibility_protocol_unsupported",
		},
		{
			name: "thrift current with protobuf proposal",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.What.Selections[0].Protocol = "thrift"
				plan.Brief.What.Selections[0].DeclarationLineage =
					"thrift/shop.thrift:Checkout"
				plan.Brief.What.Selections[0].CanonicalOperation =
					"/Checkout/Submit"
				plan.Capabilities = append(
					plan.Capabilities,
					"contract-compatibility",
				)
			},
			status: "unavailable",
			reason: "current_endpoint_protocol_unsupported",
		},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			plan := workbenchPlanFixture()
			test.mutate(&plan)
			preview, err := service.Preview(
				context.Background(),
				workbenchOwner,
				plan,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !preview.Ready ||
				preview.Compatibility.Status != test.status ||
				preview.Compatibility.Reason != test.reason {
				t.Fatalf("preview = %+v", preview)
			}
			if preview.Brief.What.Proposal != nil &&
				preview.Brief.What.Proposal.Protocol == "protobuf" &&
				preview.Compatibility.Limits == nil {
				t.Fatal("protobuf preview omitted visible WIRE limits")
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*WorkbenchPlan)
	}{
		{
			name: "add with current",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.TicketKind = ChangeBriefAdd
			},
		},
		{
			name: "modify without proposal",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.What.ProposalProtocol = ""
				plan.Brief.What.ProposalSources = nil
			},
		},
		{
			name: "migrate missing replacement",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.TicketKind = ChangeBriefMigrate
				plan.Brief.What.ProposalProtocol = ""
				plan.Brief.What.ProposalSources = nil
			},
		},
		{
			name: "migrate duplicate exact identity",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.TicketKind = ChangeBriefMigrate
				plan.Brief.What.ProposalProtocol = ""
				plan.Brief.What.ProposalSources = nil
				replacement := plan.Brief.What.Selections[0]
				replacement.Role = ChangeBriefReplacement
				plan.Brief.What.Selections = append(
					plan.Brief.What.Selections,
					replacement,
				)
			},
		},
		{
			name: "retire with proposal",
			mutate: func(plan *WorkbenchPlan) {
				plan.Brief.TicketKind = ChangeBriefRetire
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := workbenchPlanFixture()
			test.mutate(&plan)
			if _, err := service.Preview(
				context.Background(),
				workbenchOwner,
				plan,
			); !errors.Is(err, ErrInvalidWorkbench) {
				t.Fatalf("preview error = %v, want invalid", err)
			}
		})
	}
}

func TestWorkbenchRetainedCompatibilityIsAdditiveIdempotentAndRevisionBound(
	t *testing.T,
) {
	s := newRunnerStore(t)
	resolver := newFixtureWorkbenchResolver()
	checker := &fixtureWorkbenchCompatibility{}
	service := InvestigationWorkbenchService{
		Store: s, Resolver: resolver, Compatibility: checker,
	}
	ctx := context.Background()
	plan := workbenchPlanFixture()
	plan.Brief.What.Selections[0].DeclarationLineage =
		"provisional_repo_path_v1_checkout"
	plan.Capabilities = append(
		plan.Capabilities,
		"contract-compatibility",
	)
	preview, err := service.Preview(ctx, workbenchOwner, plan)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(
		ctx,
		workbenchOwner,
		WorkbenchMutationRequest{
			Plan:           plan,
			PreviewDigest:  preview.PreviewDigest,
			IdempotencyKey: "create-for-compatibility",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	before := []compat.File{{
		Path:    "proto/shop.proto",
		Content: "syntax = \"proto3\";\nservice Checkout {}\n",
	}}
	after := []compat.File{{
		Path:    plan.Brief.What.ProposalSources[0].Path,
		Content: plan.Brief.What.ProposalSources[0].Content,
	}}
	request := WorkbenchCompatibilityAnalysisRequest{
		InvestigationID: created.Investigation.ID,
		RevisionID:      created.Revision.ID,
		IdempotencyKey:  "retain-compatibility-1",
		Before:          before,
		After:           after,
	}
	first, err := service.RetainCompatibility(
		ctx,
		workbenchOwner,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RetainCompatibility(
		ctx,
		workbenchOwner,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "published" ||
		first.Compatibility == nil ||
		!first.Compatibility.Compatible ||
		first.Run.ID != second.Run.ID ||
		first.Artifact.ID != second.Artifact.ID ||
		first.Run.RevisionID != created.Revision.ID ||
		checker.callCount() != 1 {
		t.Fatalf("retained analyses = %+v / %+v; calls=%d", first, second, checker.callCount())
	}

	tampered := request
	tampered.IdempotencyKey = "retain-compatibility-tampered"
	tampered.Before = slices.Clone(request.Before)
	tampered.Before[0].Content =
		"syntax = \"proto3\";\nservice Checkout { rpc Hidden(Empty) returns (Empty); }\n"
	if _, err := service.RetainCompatibility(
		ctx,
		workbenchOwner,
		tampered,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("fabricated baseline = %v, want ErrConflict", err)
	}
	if checker.callCount() != 1 {
		t.Fatalf("fabricated baseline reached checker; calls=%d", checker.callCount())
	}
	if len(first.Endpoints) != 1 ||
		len(first.Endpoints[0].DeclarationSources) != 1 ||
		first.Endpoints[0].DeclarationSources[0].Commit !=
			strings.Repeat("b", 40) {
		t.Fatalf("pinned endpoint = %+v", first.Endpoints)
	}
	if workbenchTableCount(t, s, "proof_bundle") != 0 {
		t.Fatal("retained Workbench analysis created a proof bundle")
	}
	if workbenchTableCount(t, s, "investigation_run") != 1 ||
		workbenchTableCount(t, s, "investigation_run_artifact") != 1 {
		t.Fatal("idempotent retained analysis duplicated run or artifact")
	}
	events, err := s.ListAuditEvents(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	publishedAudits := 0
	for _, event := range events {
		if event.Action == "investigation.run.published" &&
			event.Target == first.Run.ID {
			publishedAudits++
		}
	}
	if publishedAudits != 1 {
		t.Fatalf("published audit count = %d", publishedAudits)
	}

	changed := request
	changed.After = []compat.File{{
		Path:    "proto/moved.proto",
		Content: after[0].Content,
	}}
	if _, err := service.RetainCompatibility(
		ctx,
		workbenchOwner,
		changed,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed proposal = %v, want conflict", err)
	}

	revisedPlan := plan
	revisedPlan.InvestigationID = created.Investigation.ID
	revisedPlan.ExpectedRevisionID = created.Revision.ID
	revisedPlan.Brief.Problem = "The bounded problem changed."
	revisedPreview, err := service.Preview(
		ctx,
		workbenchOwner,
		revisedPlan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revise(
		ctx,
		workbenchOwner,
		WorkbenchMutationRequest{
			Plan:           revisedPlan,
			PreviewDigest:  revisedPreview.PreviewDigest,
			IdempotencyKey: "revise-after-compatibility",
		},
	); err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = "stale-revision"
	if _, err := service.RetainCompatibility(
		ctx,
		workbenchOwner,
		request,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v, want conflict", err)
	}
}
