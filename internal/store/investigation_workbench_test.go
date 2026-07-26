package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

const workbenchOwner = "user:workbench-owner"

type fixtureWorkbenchResolver struct {
	mu                  sync.Mutex
	authorizationDigest string
	repositoryCommit    string
	endpointCommit      string
	endpointDigest      string
	capabilityDigest    string
	capabilityAvailable bool
	omitRepository      bool
	omitEndpoint        bool
}

func newFixtureWorkbenchResolver() *fixtureWorkbenchResolver {
	return &fixtureWorkbenchResolver{
		authorizationDigest: "sha256:" + strings.Repeat("a", 64),
		repositoryCommit:    strings.Repeat("b", 40),
		endpointCommit:      strings.Repeat("b", 40),
		endpointDigest:      "sha256:" + strings.Repeat("d", 64),
		capabilityDigest:    "sha256:" + strings.Repeat("e", 64),
		capabilityAvailable: true,
	}
}

func (resolver *fixtureWorkbenchResolver) ResolveWorkbench(
	_ context.Context,
	_ string,
	request WorkbenchResolutionRequest,
) (WorkbenchResolution, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	result := WorkbenchResolution{
		AuthorizationDigest: resolver.authorizationDigest,
		Repositories:        []WorkbenchRepositorySnapshot{},
		Endpoints:           []WorkbenchEndpointSnapshot{},
		Capabilities:        []WorkbenchCapabilitySnapshot{},
	}
	if !resolver.omitRepository {
		for _, repository := range request.Repositories {
			result.Repositories = append(result.Repositories, WorkbenchRepositorySnapshot{
				Name: repository, Commit: resolver.repositoryCommit,
			})
		}
	}
	if !resolver.omitEndpoint {
		for _, selection := range request.Selections {
			result.Endpoints = append(result.Endpoints, WorkbenchEndpointSnapshot{
				Selection:         selection,
				DeclarationCommit: resolver.endpointCommit,
				DeclarationDigest: resolver.endpointDigest,
			})
		}
	}
	for _, capability := range request.Capabilities {
		snapshot := WorkbenchCapabilitySnapshot{
			ID:        capability,
			Available: resolver.capabilityAvailable,
		}
		if snapshot.Available {
			snapshot.Version = "v1"
			snapshot.ContentDigest = resolver.capabilityDigest
		}
		result.Capabilities = append(result.Capabilities, snapshot)
	}
	return result, nil
}

func workbenchPlanFixture() WorkbenchPlan {
	return WorkbenchPlan{
		Referent:    "grpc:/shop.v1.Checkout/Submit",
		ClaimFamily: "change-workbench",
		Title:       "Checkout receipt identifier",
		Revision: WorkbenchRevisionDraft{
			NormalizedQuestion: "what changes are required for checkout receipts?",
			DecisionSought:     "approve the bounded contract change",
			SnapshotPolicy:     "exact indexed commit",
			BuildConfiguration: "linux/amd64",
			EnumerationMethod:  "exact contract selection",
		},
		Brief: WorkbenchChangeBriefDraft{
			TicketKind:     ChangeBriefModify,
			Problem:        "Checkout lacks a stable receipt identifier.",
			DesiredOutcome: "Checkout returns a stable receipt identifier.",
			SuccessCriteria: []string{
				"The response contains the new identifier.",
				"Existing field numbers remain stable.",
			},
			NonGoals:          []string{"Changing authorization behavior."},
			Assumptions:       []string{"The current endpoint remains available."},
			OpenQuestions:     []string{"Which clients regenerate their SDK?"},
			ExternalReference: "CHANGE-42",
			What: WorkbenchWhatDraft{
				Selections: []ChangeBriefContractSelection{{
					Role:               ChangeBriefCurrent,
					Protocol:           "protobuf",
					Repository:         "example/contracts",
					DeclarationLineage: "proto/shop.proto:shop.v1.Checkout",
					CanonicalOperation: "/shop.v1.Checkout/Submit",
				}},
				ProposalProtocol: "protobuf",
				ProposalSources: []WorkbenchProposalSource{{
					Path:    "proto/shop.proto",
					Content: "syntax = \"proto3\";\nservice Checkout {}\n",
				}},
			},
		},
		Repositories: []string{"example/contracts"},
		Capabilities: []string{"contract-atlas"},
	}
}

func workbenchTableCount(
	t *testing.T,
	s *Surreal,
	table string,
) int {
	t.Helper()
	results, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](context.Background(), s.db,
		"SELECT count() AS count FROM type::table($table) GROUP ALL",
		map[string]any{"table": table})
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return 0
	}
	return rows[0].Count
}

func TestWorkbenchPreviewIsCanonicalBoundedAndSideEffectFree(t *testing.T) {
	s := newRunnerStore(t)
	resolver := newFixtureWorkbenchResolver()
	service := InvestigationWorkbenchService{Store: s, Resolver: resolver}
	plan := workbenchPlanFixture()
	plan.Brief.What.Selections = append(
		plan.Brief.What.Selections,
		ChangeBriefContractSelection{
			Role:               ChangeBriefAnalogous,
			Protocol:           "thrift",
			Repository:         "example/contracts",
			DeclarationLineage: "thrift/shop.thrift:Checkout",
			CanonicalOperation: "/Checkout/Submit",
		},
	)
	plan.Brief.What.ProposalSources = append(
		plan.Brief.What.ProposalSources,
		WorkbenchProposalSource{
			Path:    "proto/types.proto",
			Content: "syntax = \"proto3\";\nmessage Receipt {}\n",
		},
	)

	first, err := service.Preview(context.Background(), workbenchOwner, plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Repositories = []string{"example/contracts"}
	plan.Capabilities = []string{"contract-atlas"}
	plan.Brief.What.Selections[0], plan.Brief.What.Selections[1] =
		plan.Brief.What.Selections[1], plan.Brief.What.Selections[0]
	plan.Brief.What.ProposalSources[0], plan.Brief.What.ProposalSources[1] =
		plan.Brief.What.ProposalSources[1], plan.Brief.What.ProposalSources[0]
	second, err := service.Preview(context.Background(), workbenchOwner, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Ready || first.PreviewDigest == "" ||
		first.PreviewDigest != second.PreviewDigest ||
		first.Estimate.RepositoryCount != 1 ||
		first.Estimate.EndpointCount != 2 ||
		first.Estimate.ProposalFiles != 2 {
		t.Fatalf("canonical preview = %+v / %+v", first, second)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "syntax =") ||
		first.Brief.What.Proposal == nil ||
		first.Brief.What.Proposal.SourceSetDigest == "" {
		t.Fatalf("preview leaked bytes or omitted commitment: %s", encoded)
	}
	for _, table := range []string{
		"investigation",
		"investigation_revision",
		"investigation_change_brief",
		"investigation_workbench_mutation",
		"audit_event",
	} {
		if count := workbenchTableCount(t, s, table); count != 0 {
			t.Fatalf("preview wrote %d rows to %s", count, table)
		}
	}

	tests := []struct {
		name   string
		mutate func(*WorkbenchPlan)
	}{
		{"repositories required", func(value *WorkbenchPlan) {
			value.Repositories = nil
		}},
		{"capabilities required", func(value *WorkbenchPlan) {
			value.Capabilities = nil
		}},
		{"duplicate repository", func(value *WorkbenchPlan) {
			value.Repositories = append(value.Repositories, value.Repositories[0])
		}},
		{"invalid capability", func(value *WorkbenchPlan) {
			value.Capabilities = []string{"Contract-Atlas"}
		}},
		{"selection outside scope", func(value *WorkbenchPlan) {
			value.Brief.What.Selections[0].Repository = "example/other"
		}},
		{"oversized proposal", func(value *WorkbenchPlan) {
			value.Brief.What.ProposalSources[0].Content = strings.Repeat(
				"x",
				maxChangeBriefProposalFileBytes+1,
			)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := workbenchPlanFixture()
			test.mutate(&candidate)
			if _, err := service.Preview(
				context.Background(),
				workbenchOwner,
				candidate,
			); !errors.Is(err, ErrInvalidWorkbench) {
				t.Fatalf("preview error = %v, want ErrInvalidWorkbench", err)
			}
		})
	}
}

func TestWorkbenchCreateIsAtomicConcurrentAndIdempotent(t *testing.T) {
	s := newRunnerStore(t)
	resolver := newFixtureWorkbenchResolver()
	service := InvestigationWorkbenchService{Store: s, Resolver: resolver}
	ctx := context.Background()
	plan := workbenchPlanFixture()
	preview, err := service.Preview(ctx, workbenchOwner, plan)
	if err != nil {
		t.Fatal(err)
	}
	request := WorkbenchMutationRequest{
		Plan:           plan,
		PreviewDigest:  preview.PreviewDigest,
		IdempotencyKey: "workbench-create-1",
	}

	const callers = 12
	results := make(chan *WorkbenchView, callers)
	errs := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, createErr := service.Create(ctx, workbenchOwner, request)
			if createErr != nil {
				errs <- createErr
				return
			}
			results <- value
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	if len(errs) != 0 || len(results) != callers {
		t.Fatalf("concurrent create results=%d errors=%d", len(results), len(errs))
	}
	var first *WorkbenchView
	for value := range results {
		if first == nil {
			first = value
			continue
		}
		if value.Investigation.ID != first.Investigation.ID ||
			value.Revision.ID != first.Revision.ID ||
			value.Brief.ID != first.Brief.ID {
			t.Fatalf("idempotent create diverged: %+v / %+v", first, value)
		}
	}
	if first == nil || first.Revision.Seq != 1 ||
		first.Investigation.CurrentRevisionID != first.Revision.ID {
		t.Fatalf("created workbench = %+v", first)
	}
	createdBytes, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(createdBytes), "syntax =") ||
		strings.Contains(string(createdBytes), "proto3") {
		t.Fatalf("persisted Workbench retained proposal bytes: %s", createdBytes)
	}
	for table, want := range map[string]int{
		"investigation":                    1,
		"investigation_revision":           1,
		"investigation_change_brief":       1,
		"investigation_workbench_mutation": 1,
	} {
		if count := workbenchTableCount(t, s, table); count != want {
			t.Fatalf("%s rows = %d, want %d", table, count, want)
		}
	}
	events, err := s.ListAuditEvents(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, event := range events {
		if event.Action == "investigation.workbench.create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("workbench create audit events = %d, want 1", creates)
	}
	read, err := service.Read(ctx, workbenchOwner, first.Investigation.ID)
	if err != nil || read.Brief.ID != first.Brief.ID {
		t.Fatalf("read workbench = %+v, %v", read, err)
	}
	if count := workbenchTableCount(t, s, "audit_event"); count != 1 {
		t.Fatalf("read appended an audit event; count = %d", count)
	}

	changed := plan
	changed.Title = "Different title"
	changedPreview, err := service.Preview(ctx, workbenchOwner, changed)
	if err != nil {
		t.Fatal(err)
	}
	request.Plan = changed
	request.PreviewDigest = changedPreview.PreviewDigest
	if _, err := service.Create(
		ctx,
		workbenchOwner,
		request,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused idempotency key = %v, want ErrConflict", err)
	}
}

func TestWorkbenchCreateRejectsPreviewDriftWithoutPartialWrite(t *testing.T) {
	s := newRunnerStore(t)
	resolver := newFixtureWorkbenchResolver()
	service := InvestigationWorkbenchService{Store: s, Resolver: resolver}
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate func(*WorkbenchPlan)
		drift  func()
		reset  func()
	}{
		{
			name: "permissions",
			drift: func() {
				resolver.authorizationDigest = "sha256:" + strings.Repeat("1", 64)
			},
			reset: func() {
				resolver.authorizationDigest = "sha256:" + strings.Repeat("a", 64)
			},
		},
		{
			name: "repository",
			drift: func() {
				resolver.repositoryCommit = strings.Repeat("2", 40)
				resolver.endpointCommit = strings.Repeat("2", 40)
			},
			reset: func() {
				resolver.repositoryCommit = strings.Repeat("b", 40)
				resolver.endpointCommit = strings.Repeat("b", 40)
			},
		},
		{
			name: "endpoint identity",
			drift: func() {
				resolver.endpointDigest = "sha256:" + strings.Repeat("3", 64)
			},
			reset: func() {
				resolver.endpointDigest = "sha256:" + strings.Repeat("d", 64)
			},
		},
		{
			name: "proposal bytes",
			mutate: func(value *WorkbenchPlan) {
				value.Brief.What.ProposalSources[0].Content += "// drift\n"
			},
			drift: func() {},
			reset: func() {},
		},
		{
			name: "capability availability",
			drift: func() {
				resolver.capabilityAvailable = false
			},
			reset: func() {
				resolver.capabilityAvailable = true
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := workbenchPlanFixture()
			preview, err := service.Preview(ctx, workbenchOwner, plan)
			if err != nil {
				t.Fatal(err)
			}
			test.drift()
			if test.mutate != nil {
				test.mutate(&plan)
			}
			_, err = service.Create(ctx, workbenchOwner, WorkbenchMutationRequest{
				Plan:           plan,
				PreviewDigest:  preview.PreviewDigest,
				IdempotencyKey: fmt.Sprintf("drift-%d", index),
			})
			test.reset()
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("drifted create = %v, want ErrConflict", err)
			}
		})
	}
	for _, table := range []string{
		"investigation",
		"investigation_revision",
		"investigation_change_brief",
		"investigation_workbench_mutation",
		"audit_event",
	} {
		if count := workbenchTableCount(t, s, table); count != 0 {
			t.Fatalf("drifted previews left %d partial rows in %s", count, table)
		}
	}
}

func createWorkbenchFixture(
	t *testing.T,
	service InvestigationWorkbenchService,
) (*WorkbenchView, WorkbenchPlan) {
	t.Helper()
	plan := workbenchPlanFixture()
	preview, err := service.Preview(context.Background(), workbenchOwner, plan)
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Create(
		context.Background(),
		workbenchOwner,
		WorkbenchMutationRequest{
			Plan:           plan,
			PreviewDigest:  preview.PreviewDigest,
			IdempotencyKey: "fixture-create",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return value, plan
}

func reviseWorkbenchPlan(
	base WorkbenchPlan,
	current *WorkbenchView,
	suffix string,
) WorkbenchPlan {
	base.InvestigationID = current.Investigation.ID
	base.ExpectedRevisionID = current.Revision.ID
	base.Revision.NormalizedQuestion += suffix
	base.Brief.Problem += suffix
	return base
}

func TestWorkbenchReviseCASAndAuthorization(t *testing.T) {
	s := newRunnerStore(t)
	resolver := newFixtureWorkbenchResolver()
	service := InvestigationWorkbenchService{Store: s, Resolver: resolver}
	ctx := context.Background()
	created, basePlan := createWorkbenchFixture(t, service)

	revisionPlan := reviseWorkbenchPlan(basePlan, created, " Revision two.")
	preview, err := service.Preview(ctx, workbenchOwner, revisionPlan)
	if err != nil {
		t.Fatal(err)
	}
	request := WorkbenchMutationRequest{
		Plan:           revisionPlan,
		PreviewDigest:  preview.PreviewDigest,
		IdempotencyKey: "revision-two",
	}
	const reviseCallers = 8
	revisionResults := make(chan *WorkbenchView, reviseCallers)
	revisionErrors := make(chan error, reviseCallers)
	var wait sync.WaitGroup
	for range reviseCallers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, reviseErr := service.Revise(ctx, workbenchOwner, request)
			if reviseErr != nil {
				revisionErrors <- reviseErr
				return
			}
			revisionResults <- value
		}()
	}
	wait.Wait()
	close(revisionResults)
	close(revisionErrors)
	if len(revisionErrors) != 0 || len(revisionResults) != reviseCallers {
		t.Fatalf(
			"concurrent revise results=%d errors=%d",
			len(revisionResults),
			len(revisionErrors),
		)
	}
	var first *WorkbenchView
	for value := range revisionResults {
		if first == nil {
			first = value
			continue
		}
		if value.Revision.ID != first.Revision.ID ||
			value.Brief.ID != first.Brief.ID {
			t.Fatalf("concurrent revise diverged: %+v / %+v", first, value)
		}
	}
	retry, err := service.Revise(ctx, workbenchOwner, request)
	if err != nil || retry.Revision.ID != first.Revision.ID ||
		retry.Brief.ID != first.Brief.ID {
		t.Fatalf("idempotent revise = %+v, %v", retry, err)
	}
	if first.Revision.Seq != 2 ||
		first.Investigation.CurrentRevisionID != first.Revision.ID {
		t.Fatalf("revision two = %+v", first)
	}
	oldBrief, err := s.GetChangeBriefAs(ctx, workbenchOwner, created.Brief.ID)
	if err != nil || oldBrief.Problem != created.Brief.Problem {
		t.Fatalf("prior brief changed = %+v, %v", oldBrief, err)
	}

	stalePlan := reviseWorkbenchPlan(basePlan, first, " Revision three.")
	stalePreview, err := service.Preview(ctx, workbenchOwner, stalePlan)
	if err != nil {
		t.Fatal(err)
	}
	winner, err := service.Revise(ctx, workbenchOwner, WorkbenchMutationRequest{
		Plan:           stalePlan,
		PreviewDigest:  stalePreview.PreviewDigest,
		IdempotencyKey: "revision-three-winner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revise(ctx, workbenchOwner, WorkbenchMutationRequest{
		Plan:           stalePlan,
		PreviewDigest:  stalePreview.PreviewDigest,
		IdempotencyKey: "revision-three-stale",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v, want ErrConflict", err)
	}
	if count := workbenchTableCount(t, s, "investigation_revision"); count != 3 {
		t.Fatalf("revision count after stale write = %d, want 3", count)
	}
	if count := workbenchTableCount(
		t,
		s,
		"investigation_workbench_mutation",
	); count != 3 {
		t.Fatalf("mutation count after stale write = %d, want 3", count)
	}

	const reader = "user:workbench-reader"
	unknownID := "01J00000000000000000000000"
	_, unknownErr := service.Read(ctx, reader, unknownID)
	_, unauthorizedErr := service.Read(
		ctx,
		reader,
		created.Investigation.ID,
	)
	if unknownErr == nil || unauthorizedErr == nil ||
		unknownErr.Error() != unauthorizedErr.Error() ||
		!errors.Is(unknownErr, ErrNotFound) ||
		!errors.Is(unauthorizedErr, ErrNotFound) {
		t.Fatalf(
			"read refusals differ: unknown=%v unauthorized=%v",
			unknownErr,
			unauthorizedErr,
		)
	}
	if _, err := s.GrantInvestigationAccess(
		ctx,
		workbenchOwner,
		created.Investigation.ID,
		reader,
		InvestigationRoleReader,
	); err != nil {
		t.Fatal(err)
	}
	if read, err := service.Read(
		ctx,
		reader,
		created.Investigation.ID,
	); err != nil || read.Revision.ID != winner.Revision.ID {
		t.Fatalf("reader view = %+v, %v", read, err)
	}
	readerPlan := reviseWorkbenchPlan(basePlan, winner, " Reader edit.")
	if _, err := service.Preview(
		ctx,
		reader,
		readerPlan,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reader mutation preview = %v, want ErrNotFound", err)
	}
	if err := s.RevokeInvestigationAccess(
		ctx,
		workbenchOwner,
		created.Investigation.ID,
		reader,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(
		ctx,
		reader,
		created.Investigation.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked read = %v, want ErrNotFound", err)
	}
}
