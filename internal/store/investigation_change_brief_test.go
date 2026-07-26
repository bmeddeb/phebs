package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

const (
	changeBriefOwner  = "user:change-brief-owner"
	changeBriefReader = "user:change-brief-reader"
)

func changeBriefFixture(investigationID string, seq int) ChangeBrief {
	return ChangeBrief{
		InvestigationID: investigationID,
		RevisionID:      revisionIdentity(investigationID, seq),
		TicketKind:      ChangeBriefModify,
		Problem:         "The checkout contract does not expose a stable receipt identifier.",
		DesiredOutcome:  "Callers can consume a stable receipt identifier.",
		SuccessCriteria: []string{
			"The identifier is present in the response.",
			"Existing wire fields retain their numbers.",
		},
		NonGoals:          []string{"Changing payment authorization behavior."},
		Assumptions:       []string{"The current endpoint remains available during rollout."},
		OpenQuestions:     []string{"Which clients require a regenerated SDK?"},
		ExternalReference: "https://tickets.invalid/CHANGE-42",
		What: ChangeBriefWhat{
			Selections: []ChangeBriefContractSelection{
				{
					Role: ChangeBriefCurrent, Protocol: "protobuf",
					Repository:         "example/contracts",
					DeclarationLineage: "proto/shop/checkout.proto:shop.v1.Checkout",
					CanonicalOperation: "/shop.v1.Checkout/Submit",
				},
			},
		},
		Creator: changeBriefOwner,
	}
}

func changeBriefRevisionFixture(investigationID string, seq int, creator string) Revision {
	return Revision{
		InvestigationID: investigationID,
		Seq:             seq,
		NormalizedQuestion: fmt.Sprintf(
			"what changes are required for checkout contract revision %d?",
			seq,
		),
		DecisionSought:     "approve the bounded contract change",
		DeclaredUniverse:   "repo:example/contracts@0123456789abcdef",
		SnapshotPolicy:     "exact commit",
		BuildConfiguration: "linux/amd64",
		PackSelection:      "protobuf-contract@1",
		EnumerationMethod:  "exact contract selection",
		Creator:            creator,
	}
}

func TestCanonicalChangeBriefIdentityAndOrdering(t *testing.T) {
	const investigationID = "01J00000000000000000000000"
	first := changeBriefFixture(investigationID, 1)
	first.Problem = "  The first line.\r\nThe second line.  "
	first.NonGoals = nil
	first.What.Selections = append(first.What.Selections, ChangeBriefContractSelection{
		Role: ChangeBriefAnalogous, Protocol: " THRIFT ",
		Repository:         " example/legacy ",
		DeclarationLineage: " thrift/cart.thrift:Cart ",
		CanonicalOperation: " /Cart/Submit ",
	})
	first.What.Proposal = &ChangeBriefProposal{
		Protocol: "PROTOBUF",
		Files: []ChangeBriefProposalFile{
			{
				Path:        "proto/shop/types.proto",
				ContentHash: "sha256:" + strings.Repeat("b", 64),
				SizeBytes:   128,
			},
			{
				Path:        "proto/shop/checkout.proto",
				ContentHash: "sha256:" + strings.Repeat("a", 64),
				SizeBytes:   256,
			},
		},
	}
	second := first
	second.Problem = "The first line.\nThe second line."
	second.NonGoals = []string{}
	second.ExternalReference = " " + second.ExternalReference + " "
	second.What.Selections = []ChangeBriefContractSelection{
		first.What.Selections[1],
		first.What.Selections[0],
	}
	second.What.Proposal = &ChangeBriefProposal{
		Protocol: "protobuf",
		Files: []ChangeBriefProposalFile{
			first.What.Proposal.Files[1],
			first.What.Proposal.Files[0],
		},
	}

	canonicalFirst, firstBytes, err := CanonicalChangeBrief(first)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSecond, secondBytes, err := CanonicalChangeBrief(second)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalFirst.ID != canonicalSecond.ID ||
		canonicalFirst.ContentDigest != canonicalSecond.ContentDigest ||
		string(firstBytes) != string(secondBytes) {
		t.Fatalf(
			"canonical equal briefs diverged:\nfirst=%s\nsecond=%s",
			firstBytes,
			secondBytes,
		)
	}
	if canonicalFirst.What.Selections[0].Role != ChangeBriefAnalogous ||
		canonicalFirst.What.Proposal.Files[0].Path != "proto/shop/checkout.proto" {
		t.Fatal("unordered sets were not normalized")
	}

	reordered := first
	reordered.SuccessCriteria = []string{
		first.SuccessCriteria[1],
		first.SuccessCriteria[0],
	}
	canonicalReordered, _, err := CanonicalChangeBrief(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalReordered.ID == canonicalFirst.ID {
		t.Fatal("ordered success criteria did not affect content identity")
	}
}

func TestCanonicalChangeBriefRejectsInvalidAndUnboundedInput(t *testing.T) {
	const investigationID = "01J00000000000000000000000"
	tests := []struct {
		name   string
		mutate func(*ChangeBrief)
	}{
		{"schema", func(value *ChangeBrief) { value.SchemaVersion = "change-brief-v2" }},
		{"ticket kind", func(value *ChangeBrief) { value.TicketKind = "remove" }},
		{"problem", func(value *ChangeBrief) { value.Problem = "" }},
		{"desired outcome", func(value *ChangeBrief) { value.DesiredOutcome = "" }},
		{"success criteria", func(value *ChangeBrief) { value.SuccessCriteria = nil }},
		{"duplicate ordered item", func(value *ChangeBrief) {
			value.SuccessCriteria = []string{"same", " same "}
		}},
		{"list item bound", func(value *ChangeBrief) {
			value.Assumptions = []string{
				strings.Repeat("a", maxChangeBriefListItemBytes+1),
			}
		}},
		{"unsafe text", func(value *ChangeBrief) { value.Problem = "unsafe\x00text" }},
		{"problem bound", func(value *ChangeBrief) {
			value.Problem = strings.Repeat("p", maxChangeBriefProblemBytes+1)
		}},
		{"list count", func(value *ChangeBrief) {
			value.OpenQuestions = make([]string, maxChangeBriefListItems+1)
		}},
		{"empty what", func(value *ChangeBrief) { value.What = ChangeBriefWhat{} }},
		{"aggregate text bound", func(value *ChangeBrief) {
			items := make([]string, maxChangeBriefListItems)
			for index := range items {
				items[index] = fmt.Sprintf(
					"%02d%s",
					index,
					strings.Repeat("x", 1100),
				)
			}
			value.SuccessCriteria = append([]string(nil), items...)
			value.NonGoals = append([]string(nil), items...)
			value.Assumptions = append([]string(nil), items...)
			value.OpenQuestions = append([]string(nil), items...)
		}},
		{"selection count", func(value *ChangeBrief) {
			value.What.Selections = make(
				[]ChangeBriefContractSelection,
				maxChangeBriefSelections+1,
			)
		}},
		{"selection role", func(value *ChangeBrief) {
			value.What.Selections[0].Role = "source"
		}},
		{"selection protocol", func(value *ChangeBrief) {
			value.What.Selections[0].Protocol = "openapi"
		}},
		{"selection operation", func(value *ChangeBrief) {
			value.What.Selections[0].CanonicalOperation = "Checkout/Submit"
		}},
		{"duplicate selection role", func(value *ChangeBrief) {
			value.What.Selections = append(
				value.What.Selections,
				value.What.Selections[0],
			)
		}},
		{"proposal path", func(value *ChangeBrief) {
			value.What.Proposal = &ChangeBriefProposal{
				Protocol: "protobuf",
				Files: []ChangeBriefProposalFile{{
					Path:        "../checkout.proto",
					ContentHash: "sha256:" + strings.Repeat("a", 64),
					SizeBytes:   1,
				}},
			}
		}},
		{"proposal digest", func(value *ChangeBrief) {
			value.What.Proposal = &ChangeBriefProposal{
				Protocol: "protobuf",
				Files: []ChangeBriefProposalFile{{
					Path: "checkout.proto", ContentHash: "sha256:bad", SizeBytes: 1,
				}},
			}
		}},
		{"proposal file size", func(value *ChangeBrief) {
			value.What.Proposal = &ChangeBriefProposal{
				Protocol: "protobuf",
				Files: []ChangeBriefProposalFile{{
					Path:        "checkout.proto",
					ContentHash: "sha256:" + strings.Repeat("a", 64),
					SizeBytes:   maxChangeBriefProposalFileBytes + 1,
				}},
			}
		}},
		{"proposal file count", func(value *ChangeBrief) {
			value.What.Proposal = &ChangeBriefProposal{
				Protocol: "protobuf",
				Files: make(
					[]ChangeBriefProposalFile,
					maxChangeBriefProposalFiles+1,
				),
			}
		}},
		{"proposal duplicate path", func(value *ChangeBrief) {
			file := ChangeBriefProposalFile{
				Path:        "checkout.proto",
				ContentHash: "sha256:" + strings.Repeat("a", 64),
				SizeBytes:   1,
			}
			value.What.Proposal = &ChangeBriefProposal{
				Protocol: "protobuf",
				Files:    []ChangeBriefProposalFile{file, file},
			}
		}},
		{"proposal aggregate bound", func(value *ChangeBrief) {
			files := make([]ChangeBriefProposalFile, 33)
			for index := range files {
				files[index] = ChangeBriefProposalFile{
					Path:        fmt.Sprintf("proposal/%02d.proto", index),
					ContentHash: "sha256:" + strings.Repeat("a", 64),
					SizeBytes:   1 << 20,
				}
			}
			value.What.Proposal = &ChangeBriefProposal{
				Protocol: "protobuf",
				Files:    files,
			}
		}},
		{"proposal source-set digest", func(value *ChangeBrief) {
			value.What.Proposal = &ChangeBriefProposal{
				Protocol: "protobuf",
				Files: []ChangeBriefProposalFile{{
					Path:        "checkout.proto",
					ContentHash: "sha256:" + strings.Repeat("a", 64),
					SizeBytes:   1,
				}},
				SourceSetDigest: "sha256:" + strings.Repeat("f", 64),
			}
		}},
		{"external reference bound", func(value *ChangeBrief) {
			value.ExternalReference = strings.Repeat("x", maxChangeBriefExternalRefBytes+1)
		}},
		{"creator", func(value *ChangeBrief) { value.Creator = " user:owner " }},
		{"identity", func(value *ChangeBrief) { value.ID = "icb_wrong" }},
		{"content digest", func(value *ChangeBrief) {
			value.ContentDigest = "sha256:" + strings.Repeat("f", 64)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := changeBriefFixture(investigationID, 1)
			test.mutate(&value)
			if _, _, err := CanonicalChangeBrief(value); err == nil {
				t.Fatal("invalid change brief unexpectedly accepted")
			}
		})
	}
}

func createChangeBriefInvestigation(t *testing.T, s *Surreal) *Investigation {
	t.Helper()
	value, err := s.CreateInvestigation(context.Background(), Investigation{
		Referent:    "grpc:/shop.v1.Checkout/Submit",
		ClaimFamily: "change-workbench",
		Title:       "Checkout contract change",
		Owner:       changeBriefOwner,
	})
	if err != nil {
		t.Fatalf("create investigation: %v", err)
	}
	return value
}

func appendChangeBrief(
	t *testing.T,
	s *Surreal,
	investigationID string,
	seq int,
	expectedRevisionID string,
	problemSuffix string,
) *ChangeBriefRevision {
	t.Helper()
	brief := changeBriefFixture(investigationID, seq)
	brief.Problem += problemSuffix
	value, err := s.AppendChangeBriefRevision(context.Background(), AppendChangeBriefRevisionRequest{
		Principal:          changeBriefOwner,
		ExpectedRevisionID: expectedRevisionID,
		Revision:           changeBriefRevisionFixture(investigationID, seq, changeBriefOwner),
		Brief:              brief,
	})
	if err != nil {
		t.Fatalf("append change brief: %v", err)
	}
	return value
}

func TestChangeBriefAtomicAppendPreservesPriorBytesAndAudit(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	investigation := createChangeBriefInvestigation(t, s)

	first := appendChangeBrief(t, s, investigation.ID, 1, "", "")
	storedFirst, err := s.GetChangeBriefAs(ctx, changeBriefOwner, first.Brief.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, firstBytes, err := CanonicalChangeBrief(*storedFirst)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.GetInvestigation(ctx, investigation.ID)
	if err != nil || parent.CurrentRevisionID != first.Revision.ID {
		t.Fatalf("parent after first append = %+v, %v", parent, err)
	}

	second := appendChangeBrief(
		t,
		s,
		investigation.ID,
		2,
		first.Revision.ID,
		" The follow-up clarifies SDK generation.",
	)
	storedFirst, err = s.GetChangeBriefAs(ctx, changeBriefOwner, first.Brief.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, afterBytes, err := CanonicalChangeBrief(*storedFirst)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(afterBytes) || first.Brief.ID == second.Brief.ID {
		t.Fatal("new brief revision mutated or reused the prior immutable bytes")
	}
	byRevision, err := s.GetChangeBriefForRevisionAs(
		ctx,
		changeBriefOwner,
		second.Revision.ID,
	)
	if err != nil || byRevision.ID != second.Brief.ID {
		t.Fatalf("brief by revision = %+v, %v", byRevision, err)
	}

	staleBrief := changeBriefFixture(investigation.ID, 2)
	staleBrief.Problem += " stale"
	canonicalStale, _, err := CanonicalChangeBrief(staleBrief)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AppendChangeBriefRevision(ctx, AppendChangeBriefRevisionRequest{
		Principal:          changeBriefOwner,
		ExpectedRevisionID: first.Revision.ID,
		Revision:           changeBriefRevisionFixture(investigation.ID, 2, changeBriefOwner),
		Brief:              staleBrief,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale append = %v, want ErrConflict", err)
	}
	if _, err := s.GetChangeBriefAs(
		ctx,
		changeBriefOwner,
		canonicalStale.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale append left a partial brief: %v", err)
	}

	events, err := s.ListAuditEvents(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	appends := 0
	for _, event := range events {
		if event.Action == "investigation.change_brief.append" {
			appends++
		}
	}
	if appends != 2 {
		t.Fatalf("change-brief audit events = %d, want 2", appends)
	}
}

func TestChangeBriefConcurrentRevisionCAS(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	investigation := createChangeBriefInvestigation(t, s)
	first := appendChangeBrief(t, s, investigation.ID, 1, "", "")

	request := func(suffix string) AppendChangeBriefRevisionRequest {
		brief := changeBriefFixture(investigation.ID, 2)
		brief.Problem += suffix
		return AppendChangeBriefRevisionRequest{
			Principal:          changeBriefOwner,
			ExpectedRevisionID: first.Revision.ID,
			Revision: changeBriefRevisionFixture(
				investigation.ID,
				2,
				changeBriefOwner,
			),
			Brief: brief,
		}
	}
	requests := []AppendChangeBriefRevisionRequest{
		request(" writer one"),
		request(" writer two"),
	}
	results := make(chan *ChangeBriefRevision, len(requests))
	errs := make(chan error, len(requests))
	var wait sync.WaitGroup
	for _, candidate := range requests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := s.AppendChangeBriefRevision(ctx, candidate)
			if err != nil {
				errs <- err
				return
			}
			results <- value
		}()
	}
	wait.Wait()
	close(results)
	close(errs)
	if len(results) != 1 || len(errs) != 1 {
		t.Fatalf("concurrent outcomes: successes=%d errors=%d", len(results), len(errs))
	}
	if err := <-errs; !errors.Is(err, ErrConflict) {
		t.Fatalf("losing writer = %v, want ErrConflict", err)
	}
	winner := <-results
	parent, err := s.GetInvestigation(ctx, investigation.ID)
	if err != nil || parent.CurrentRevisionID != winner.Revision.ID {
		t.Fatalf("CAS parent = %+v, %v", parent, err)
	}
	var countRows struct {
		Count int `json:"count"`
	}
	query, err := surrealdb.Query[[]struct {
		Count int `json:"count"`
	}](ctx, s.db,
		"SELECT count() AS count FROM investigation_change_brief WHERE investigation_id = $id GROUP ALL",
		map[string]any{"id": investigation.ID})
	if err != nil {
		t.Fatal(err)
	}
	rows := firstDomainRows(query)
	if len(rows) == 1 {
		countRows = rows[0]
	}
	if countRows.Count != 2 {
		t.Fatalf("stored brief count = %d, want 2", countRows.Count)
	}
}

func TestChangeBriefAuthorizationLifecycleAndNonDisclosure(t *testing.T) {
	s := newRunnerStore(t)
	ctx := context.Background()
	investigation := createChangeBriefInvestigation(t, s)
	first := appendChangeBrief(t, s, investigation.ID, 1, "", "")

	unknownID := "icb_" + strings.Repeat("f", 64)
	_, unknownErr := s.GetChangeBriefAs(ctx, changeBriefReader, unknownID)
	_, unauthorizedErr := s.GetChangeBriefAs(
		ctx,
		changeBriefReader,
		first.Brief.ID,
	)
	if _, err := s.GrantInvestigationAccess(
		ctx,
		changeBriefOwner,
		investigation.ID,
		changeBriefReader,
		InvestigationRoleReader,
	); err != nil {
		t.Fatal(err)
	}
	if value, err := s.GetChangeBriefAs(
		ctx,
		changeBriefReader,
		first.Brief.ID,
	); err != nil || value.ID != first.Brief.ID {
		t.Fatalf("shared brief = %+v, %v", value, err)
	}
	if err := s.RevokeInvestigationAccess(
		ctx,
		changeBriefOwner,
		investigation.ID,
		changeBriefReader,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChangeBriefAs(
		ctx,
		changeBriefReader,
		first.Brief.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked brief read = %v, want ErrNotFound", err)
	}

	const nextOwner = "user:change-brief-next-owner"
	transferred, err := s.TransferInvestigationOwnership(
		ctx,
		changeBriefOwner,
		investigation.ID,
		nextOwner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChangeBriefAs(
		ctx,
		changeBriefOwner,
		first.Brief.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old owner brief read = %v, want ErrNotFound", err)
	}
	if _, err := s.GetChangeBriefAs(
		ctx,
		nextOwner,
		first.Brief.ID,
	); err != nil {
		t.Fatalf("new owner brief read: %v", err)
	}

	transferred.Lifecycle = "archived"
	if _, err := s.UpdateInvestigation(ctx, *transferred); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChangeBriefAs(
		ctx,
		nextOwner,
		first.Brief.ID,
	); err != nil {
		t.Fatalf("archived parent brief read: %v", err)
	}
	archivedBrief := changeBriefFixture(investigation.ID, 2)
	archivedBrief.Creator = nextOwner
	_, err = s.AppendChangeBriefRevision(ctx, AppendChangeBriefRevisionRequest{
		Principal:          nextOwner,
		ExpectedRevisionID: first.Revision.ID,
		Revision: changeBriefRevisionFixture(
			investigation.ID,
			2,
			nextOwner,
		),
		Brief: archivedBrief,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("archived append = %v, want ErrConflict", err)
	}

	if _, err := surrealdb.Query[[]changeBriefRecord](
		ctx,
		s.db,
		`UPDATE $rid SET content = '{"corrupt":true}' RETURN AFTER`,
		map[string]any{"rid": changeBriefRecordID(first.Brief.ID)},
	); err != nil {
		t.Fatal(err)
	}
	_, corruptDeniedErr := s.GetChangeBriefAs(
		ctx,
		changeBriefReader,
		first.Brief.ID,
	)
	if _, err := s.GetChangeBriefAs(
		ctx,
		nextOwner,
		first.Brief.ID,
	); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("authorized corrupt read = %v, want integrity error", err)
	}
	if _, err := surrealdb.Query[[]changeBriefRecord](
		ctx,
		s.db,
		"DELETE $rid RETURN BEFORE",
		map[string]any{"rid": changeBriefRecordID(first.Brief.ID)},
	); err != nil {
		t.Fatal(err)
	}
	_, deletedErr := s.GetChangeBriefAs(ctx, nextOwner, first.Brief.ID)

	for name, candidate := range map[string]error{
		"unknown":              unknownErr,
		"unauthorized":         unauthorizedErr,
		"corrupt under denial": corruptDeniedErr,
		"deleted":              deletedErr,
	} {
		if !errors.Is(candidate, ErrNotFound) ||
			candidate.Error() != ErrNotFound.Error() {
			t.Fatalf("%s error = %v, want indistinguishable ErrNotFound", name, candidate)
		}
	}
}

func TestChangeBriefOldInvestigationCompatibilityAcrossReopen(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dataDir := t.TempDir()
	s, err := OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	investigation := createChangeBriefInvestigation(t, s)
	legacyRevision := changeBriefRevisionFixture(
		investigation.ID,
		1,
		changeBriefOwner,
	)
	savedRevision, err := s.PutRevision(ctx, legacyRevision)
	if err != nil {
		t.Fatal(err)
	}
	investigation.CurrentRevisionID = savedRevision.ID
	investigation.Lifecycle = "active"
	if _, err := s.UpdateInvestigation(ctx, *investigation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChangeBriefForRevisionAs(
		ctx,
		changeBriefOwner,
		savedRevision.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy revision brief = %v, want ErrNotFound", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	s, err = OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	second := appendChangeBrief(
		t,
		s,
		investigation.ID,
		2,
		savedRevision.ID,
		" Added after schema reopen.",
	)
	if second.Revision.Seq != 2 {
		t.Fatalf("post-migration append = %+v", second)
	}
	if _, err := s.GetChangeBriefForRevisionAs(
		ctx,
		changeBriefOwner,
		savedRevision.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy revision gained a brief = %v", err)
	}
}
