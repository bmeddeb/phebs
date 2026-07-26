package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

func TestT204ReverseAssertionPagesAreStableAndScopeBound(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const (
		repo      = "synthetic.invalid/t204-pages"
		commit    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		domain    = "t20-caller-pagination"
		predicate = "CALLS_OPERATION"
		object    = "/synthetic.orders.v1.Orders/Get"
	)
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "file:///synthetic/t204-pages.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginExtractionRun(ctx, repo, commit, domain, "t204-pages-v1")
	if err != nil {
		t.Fatal(err)
	}
	atoms, associations, assertions := t201EvidenceBatch(run, 0, 7)
	for index := range assertions {
		assertions[index].Lineage = "idl/b.proto"
		if index < 4 {
			assertions[index].Lineage = "idl/a.proto"
		}
		// Equal subjects across lineages pin the complete lineage/subject/id
		// ordering rather than relying on subject uniqueness.
		assertions[index].Subject = fmt.Sprintf("src/caller_%02d.go#L1", index%4)
	}
	if err := s.AddEvidence(ctx, run.ID, atoms, associations, assertions); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishExtractionRun(ctx, run.ID, t203Coverage(len(assertions))); err != nil {
		t.Fatal(err)
	}

	query := ReverseAssertionQuery{
		Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 2,
	}
	var (
		gotKeys []string
		seen    = make(map[string]bool)
	)
	for {
		page, pageErr := s.ListReverseAssertions(ctx, query)
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		if len(page.Assertions) == 0 {
			t.Fatal("continuation produced an empty page")
		}
		for _, assertion := range page.Assertions {
			if seen[assertion.ID] {
				t.Fatalf("assertion %s appeared on more than one page", assertion.ID)
			}
			seen[assertion.ID] = true
			gotKeys = append(gotKeys, reverseAssertionTestKey(assertion))
		}
		if page.Next == nil {
			break
		}
		query.After = page.Next
	}
	if len(gotKeys) != len(assertions) {
		t.Fatalf("paged rows = %d, want %d", len(gotKeys), len(assertions))
	}
	sortedKeys := slices.Clone(gotKeys)
	slices.Sort(sortedKeys)
	if !slices.Equal(gotKeys, sortedKeys) {
		t.Fatalf("reverse page order is unstable:\n got %q\nwant %q", gotKeys, sortedKeys)
	}

	lineagePage, err := s.ListReverseAssertions(ctx, ReverseAssertionQuery{
		Repo: repo, RunID: run.ID, Predicate: predicate, Object: object,
		Lineage: "idl/a.proto", Limit: 100,
	})
	if err != nil || len(lineagePage.Assertions) != 4 || lineagePage.Next != nil {
		t.Fatalf("lineage-exact page = %+v, %v", lineagePage, err)
	}
	for _, assertion := range lineagePage.Assertions {
		if assertion.Lineage != "idl/a.proto" {
			t.Fatalf("lineage-exact page leaked %+v", assertion)
		}
	}

	first, err := s.ListReverseAssertions(ctx, ReverseAssertionQuery{
		Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 1,
	})
	if err != nil || first.Next == nil {
		t.Fatalf("first cursor page = %+v, %v", first, err)
	}
	tests := []struct {
		name   string
		query  ReverseAssertionQuery
		mutate func(*ReverseAssertionCursor)
	}{
		{
			name: "repo", query: ReverseAssertionQuery{
				Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 1,
			},
			mutate: func(cursor *ReverseAssertionCursor) { cursor.Repo = "synthetic.invalid/other" },
		},
		{
			name: "run", query: ReverseAssertionQuery{
				Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 1,
			},
			mutate: func(cursor *ReverseAssertionCursor) { cursor.RunID = "other" },
		},
		{
			name: "predicate", query: ReverseAssertionQuery{
				Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 1,
			},
			mutate: func(cursor *ReverseAssertionCursor) { cursor.Predicate = "OTHER" },
		},
		{
			name: "object", query: ReverseAssertionQuery{
				Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 1,
			},
			mutate: func(cursor *ReverseAssertionCursor) { cursor.Object = "/other.Service/Call" },
		},
		{
			name: "query lineage", query: ReverseAssertionQuery{
				Repo: repo, RunID: run.ID, Predicate: predicate, Object: object,
				Lineage: "idl/a.proto", Limit: 1,
			},
			mutate: func(cursor *ReverseAssertionCursor) {
				cursor.QueryLineage = ""
				cursor.RowLineage = "idl/a.proto"
			},
		},
		{
			name: "row lineage", query: ReverseAssertionQuery{
				Repo: repo, RunID: run.ID, Predicate: predicate, Object: object,
				Lineage: "idl/a.proto", Limit: 1,
			},
			mutate: func(cursor *ReverseAssertionCursor) {
				cursor.QueryLineage = "idl/a.proto"
				cursor.RowLineage = "idl/b.proto"
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cursor := *first.Next
			testCase.mutate(&cursor)
			testCase.query.After = &cursor
			if _, queryErr := s.ListReverseAssertions(ctx, testCase.query); queryErr == nil {
				t.Fatal("cross-scope continuation was accepted")
			}
		})
	}
	for _, invalid := range []ReverseAssertionQuery{
		{},
		{Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 101},
		{
			Repo: repo, RunID: run.ID, Predicate: predicate, Object: object,
			After: &ReverseAssertionCursor{},
		},
	} {
		if _, queryErr := s.ListReverseAssertions(ctx, invalid); queryErr == nil {
			t.Fatalf("invalid reverse query was accepted: %+v", invalid)
		}
	}

	removed, err := surrealdb.Query[any](
		ctx,
		s.db,
		fmt.Sprintf("REMOVE INDEX IF EXISTS %s ON TABLE assertion", reverseAssertionIndexName),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range *removed {
		if result.Error != nil {
			t.Fatalf("remove reverse index statement %d: %s", i, result.Error.Message)
		}
	}
	if _, err := s.ListReverseAssertions(ctx, ReverseAssertionQuery{
		Repo: repo, RunID: run.ID, Predicate: predicate, Object: object,
	}); err == nil {
		t.Fatal("reverse page fell back to a scan after its required index was removed")
	}
	if err := s.applySchema(ctx); err != nil {
		t.Fatalf("idempotent reverse-index reinstall: %v", err)
	}
	restored, err := s.ListReverseAssertions(ctx, ReverseAssertionQuery{
		Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 100,
	})
	if err != nil || len(restored.Assertions) != len(assertions) {
		t.Fatalf("restored reverse page = %+v, %v", restored, err)
	}
}

func TestT204ReverseTargetUsesCompositeIndexWithinBudget(t *testing.T) {
	s := newRunnerStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	const (
		repo      = "synthetic.invalid/t204-scale"
		commit    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		domain    = "t20-caller-reverse-index"
		predicate = "CALLS_OPERATION"
		object    = "/synthetic.orders.v1.Orders/Get"
	)
	if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "file:///synthetic/t204-scale.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginExtractionRun(ctx, repo, commit, domain, "t204-scale-v1")
	if err != nil {
		t.Fatal(err)
	}
	const stageFacts = 2_000
	for offset := 0; offset < t201TargetFacts; offset += stageFacts {
		end := min(offset+stageFacts, t201TargetFacts)
		atoms, associations, assertions := t201EvidenceBatch(run, offset, end)
		if err := s.AddEvidence(ctx, run.ID, atoms, associations, assertions); err != nil {
			t.Fatalf("stage target batch %d: %v", offset/stageFacts, err)
		}
	}
	if err := s.PublishExtractionRun(ctx, run.ID, t203Coverage(t201TargetFacts)); err != nil {
		t.Fatal(err)
	}

	query := ReverseAssertionQuery{
		Repo: repo, RunID: run.ID, Predicate: predicate, Object: object, Limit: 100,
	}
	started := time.Now()
	first, err := s.ListReverseAssertions(ctx, query)
	firstWall := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if firstWall > 250*time.Millisecond {
		t.Fatalf("first reverse page took %v; frozen ceiling is 250ms", firstWall)
	}
	if len(first.Assertions) != 100 || first.Next == nil {
		t.Fatalf("first reverse page = %d rows, next=%v", len(first.Assertions), first.Next)
	}
	query.After = first.Next
	second, err := s.ListReverseAssertions(ctx, query)
	if err != nil || len(second.Assertions) != 100 {
		t.Fatalf("second reverse page = %+v, %v", second, err)
	}
	firstIDs := make(map[string]bool, len(first.Assertions))
	for _, assertion := range first.Assertions {
		firstIDs[assertion.ID] = true
	}
	for _, assertion := range second.Assertions {
		if firstIDs[assertion.ID] {
			t.Fatalf("assertion %s overlapped first and second pages", assertion.ID)
		}
	}

	planResults, err := surrealdb.Query[any](
		ctx,
		s.db,
		reverseAssertionQuerySQL(false, false, true),
		reverseAssertionQueryVars(ReverseAssertionQuery{
			Repo: repo, RunID: run.ID, Predicate: predicate, Object: object,
		}, 100),
	)
	if err != nil {
		t.Fatal(err)
	}
	encodedPlan, _ := json.Marshal(planResults)
	inspection := t204InspectPlan(planResults)
	if !inspection.SelectedReverseIndex || inspection.ReverseIndexRows < 1 ||
		inspection.ReverseIndexRows > 101 || inspection.SawAssertionRunIndex ||
		inspection.SawAssertionTableScan {
		t.Fatalf("unsupported reverse plan: %+v\n%s", inspection, encodedPlan)
	}
	t.Logf("T20.4 reverse first page: %d rows in %v; index rows=%d",
		len(first.Assertions), firstWall, inspection.ReverseIndexRows)
}

func reverseAssertionTestKey(assertion Assertion) string {
	return assertion.Lineage + "\x00" + assertion.Subject + "\x00" + assertion.ID
}

type t204PlanInspection struct {
	SelectedReverseIndex  bool
	ReverseIndexRows      int64
	SawAssertionRunIndex  bool
	SawAssertionTableScan bool
}

func t204InspectPlan(value any) t204PlanInspection {
	var inspection t204PlanInspection
	var visit func(any)
	visit = func(node any) {
		switch typed := node.(type) {
		case *[]surrealdb.QueryResult[any]:
			for _, result := range *typed {
				visit(result.Result)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		case map[string]any:
			operator, _ := typed["operator"].(string)
			if operator == "TableScan" {
				attributes, _ := typed["attributes"].(map[string]any)
				table, _ := attributes["table"].(string)
				inspection.SawAssertionTableScan = table == "assertion"
			}
			if operator == "IndexScan" {
				attributes, _ := typed["attributes"].(map[string]any)
				index, _ := attributes["index"].(string)
				switch index {
				case reverseAssertionIndexName:
					inspection.SelectedReverseIndex = true
					metrics, _ := typed["metrics"].(map[string]any)
					inspection.ReverseIndexRows = t204PlanInt(metrics["output_rows"])
				case "assertion_run":
					inspection.SawAssertionRunIndex = true
				}
			}
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return inspection
}

func t204PlanInt(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		number, _ := typed.Int64()
		return number
	default:
		return 0
	}
}

func TestT204ReverseQueryValidationErrorsClassifyLimits(t *testing.T) {
	_, _, err := normalizeReverseAssertionQuery(ReverseAssertionQuery{
		Repo: "repo", RunID: "run", Predicate: "P", Object: "O", Limit: 101,
	})
	if !errors.Is(err, ErrResultLimit) {
		t.Fatalf("oversized reverse page = %v, want ErrResultLimit", err)
	}
}
