//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// legacyAccountingReply scripts one native reply. SQL is a required substring
// of the submitted statement; operands are exact bound values that must reach
// the native marshaler unchanged in both modes.
type legacyAccountingReply struct {
	sql      string
	rows     any
	operands map[string]any
}

func legacyAccountingOperands(vars, want map[string]any) error {
	for key, expected := range want {
		actual, present := vars[key]
		if !present {
			return fmt.Errorf("operand %q is absent", key)
		}
		if rid, ok := expected.(models.RecordID); ok {
			if !reflect.DeepEqual(actual, rid) {
				return fmt.Errorf("operand %q changed its record: %v", key, actual)
			}
			continue
		}
		if fmt.Sprint(actual) != fmt.Sprint(expected) {
			return fmt.Errorf("operand %q changed: %v", key, actual)
		}
	}
	return nil
}

// legacyAccountingScript replays one reply sequence at the native marshaler.
// Selected mode additionally proves the read never submitted a write prefix.
func legacyAccountingScript(
	t *testing.T,
	native *storeSDKTestConnection,
	controller *storeaccounting.Controller,
	replies []legacyAccountingReply,
) {
	t.Helper()
	index := 0
	native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
		if request.Method != "query" || len(request.Params) != 2 || index >= len(replies) {
			return nil, errors.New("unexpected legacy submission")
		}
		reply := replies[index]
		index++
		sql, ok := request.Params[0].(string)
		if !ok || !strings.Contains(sql, reply.sql) {
			return nil, fmt.Errorf("legacy statement %d changed: %q", index, sql)
		}
		if len(reply.operands) != 0 {
			vars, err := callerAccountingVars(native, request)
			if err != nil {
				return nil, err
			}
			if err := legacyAccountingOperands(vars, reply.operands); err != nil {
				return nil, fmt.Errorf("legacy statement %d: %w", index, err)
			}
		}
		if controller != nil {
			prefix, err := controller.Snapshot()
			if err != nil || prefix.Transactions != 0 || prefix.Rows != 0 {
				return nil, errors.New("legacy read forwarded a write prefix")
			}
		}
		return queueAccountingOK(reply.rows), nil
	}
}

func legacyEvidenceScope() ExtractionScope {
	return ExtractionScope{
		Repository: "example.invalid/mono",
		Commit:     strings.Repeat("a", 40),
		Domain:     "proto-contract",
	}
}

func TestEvidenceLegacyAccountingReads(t *testing.T) {
	scope := legacyEvidenceScope()
	runRID := extractionRunID("run")
	now := time.Now().UTC().Truncate(time.Millisecond)
	digest := "sha256:" + strings.Repeat("f", 64)
	published := extractionRunRec{
		RunID: "run", Repo: scope.Repository, Commit: scope.Commit, Domain: scope.Domain,
		Extractor: "v1", Status: "published", StartedAt: now,
		Coverage: CoverageManifest{SourceScopeDigest: digest},
	}
	attemptSQL := "SELECT * FROM $rid WHERE repo = $repo AND commit = $commit"
	publishedSQL := "SELECT * FROM extraction_run WHERE repo = $repo AND commit = $commit"
	for _, test := range []struct {
		name    string
		call    func(context.Context, *Surreal) error
		replies []legacyAccountingReply
		want    error
	}{
		{"run_exists", func(ctx context.Context, s *Surreal) error {
			exists, err := s.extractionRunExists(ctx, "run")
			if err == nil && !exists {
				return errors.New("identity row was not reported")
			}
			return err
		}, []legacyAccountingReply{
			{"SELECT id FROM $rid", []extractionRunIdentityRec{{RecID: &runRID}}, map[string]any{"rid": runRID}},
		}, nil},
		{"run_absent", func(ctx context.Context, s *Surreal) error {
			exists, err := s.extractionRunExists(ctx, "run")
			if err == nil && exists {
				return errors.New("absent identity was reported")
			}
			return err
		}, []legacyAccountingReply{
			{"SELECT id FROM $rid", []extractionRunIdentityRec{}, map[string]any{"rid": runRID}},
		}, nil},
		{"attempt", func(ctx context.Context, s *Surreal) error {
			attempt, err := s.LatestExtractionAttempt(ctx, scope)
			if err == nil && attempt.RunID != "run" {
				return errors.New("attempt row changed")
			}
			return err
		}, []legacyAccountingReply{
			{attemptSQL, []extractionAttemptRec{{
				RunID: "run", Repo: scope.Repository, Commit: scope.Commit, Domain: scope.Domain,
				Extractor: "v1", Status: "staged", StartedAt: now,
			}}, map[string]any{"rid": extractionAttemptID(scope), "repo": scope.Repository}},
		}, nil},
		{"attempt_fallback", func(ctx context.Context, s *Surreal) error {
			_, err := s.LatestExtractionAttempt(ctx, scope)
			return err
		}, []legacyAccountingReply{
			{attemptSQL, []extractionAttemptRec{}, map[string]any{"rid": extractionAttemptID(scope)}},
			{publishedSQL, []extractionRunRec{}, map[string]any{"published_key": publishedKey(scope), "domain": scope.Domain}},
		}, ErrNotFound},
		{"published", func(ctx context.Context, s *Surreal) error {
			run, err := s.LatestPublishedRun(ctx, scope)
			if err == nil && (run.ID != "run" || run.Status != "published") {
				return errors.New("published run changed")
			}
			return err
		}, []legacyAccountingReply{
			{publishedSQL, []extractionRunRec{published}, map[string]any{"published_key": publishedKey(scope), "commit": scope.Commit}},
		}, nil},
		{"assertions", func(ctx context.Context, s *Surreal) error {
			rows, err := s.ListAssertions(ctx, AssertionQuery{Repo: scope.Repository, RunID: "run"})
			if err == nil && (len(rows) != 1 || rows[0].ID != "assertion") {
				return errors.New("assertion page changed")
			}
			return err
		}, []legacyAccountingReply{
			{"SELECT * FROM assertion WHERE run_id = $run_id", []assertionRec{{
				AssertionID: "assertion", RunID: "run", Repo: scope.Repository, Predicate: "declares",
			}}, map[string]any{"run_id": "run", "run_rid": runRID, "limit": 1001}},
		}, nil},
		{"reverse", func(ctx context.Context, s *Surreal) error {
			page, err := s.ListReverseAssertions(ctx, ReverseAssertionQuery{
				Repo: scope.Repository, RunID: "run", Predicate: "declares", Object: "object",
			})
			if err == nil && (len(page.Assertions) != 0 || page.Next != nil) {
				return errors.New("reverse page changed")
			}
			return err
		}, []legacyAccountingReply{
			{"INFO FOR INDEX " + reverseAssertionIndexName + " ON TABLE assertion", map[string]any{"building": false}, nil},
			{"SELECT * FROM assertion WITH INDEX " + reverseAssertionIndexName, []assertionRec{}, map[string]any{
				"run_rid": runRID, "predicate": "declares", "object": "object", "limit": defaultReverseAssertionPage + 1,
			}},
		}, nil},
		{"resolve", func(ctx context.Context, s *Surreal) error {
			_, err := s.ResolveEvidence(ctx, scope.Repository, "run", "atom")
			return err
		}, []legacyAccountingReply{
			{"SELECT * FROM snapshot_evidence", []evidenceResolutionRec{}, map[string]any{
				"repo": scope.Repository, "run": "run", "run_rid": runRID, "atom": "atom", "limit": maxEvidenceOccurrences + 1,
			}},
		}, ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, selected := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
					ctx := t.Context()
					var owner *storeCallOwner
					var controller *storeaccounting.Controller
					if selected {
						ctx, owner, controller = storeAccountingFixture(t, 40, 2)
					}
					db, native := storeAccountingDB(t, ctx, owner)
					legacyAccountingScript(t, native, controller, test.replies)
					s := &Surreal{db: db, accounting: owner}
					err := test.call(ctx, s)
					if !errors.Is(err, test.want) || native.calls != len(test.replies) {
						t.Fatalf("calls=%d error=%v; want %d/%v", native.calls, err, len(test.replies), test.want)
					}
					if !selected {
						return
					}
					prefix, snapshotErr := controller.Snapshot()
					if snapshotErr != nil || prefix.Transactions != 0 || prefix.Rows != 0 || prefix.Producers[0].Calls != 0 {
						t.Fatalf("legacy reads invented accounting: %+v %v", prefix, snapshotErr)
					}
					if err := controller.Fence(); err != nil {
						t.Fatal(err)
					}
					if err := owner.checkpoint(ctx); err != nil {
						t.Fatalf("legacy read left an active typed SDK call: %v", err)
					}
				})
			}
		})
	}
}

// The three legacy evidence writers are bypassed by the partitioned worker and
// keep their exact ordinary transaction. Selected mode refuses each before
// native write submission, latches the owner and names the site in the private
// diagnostic. Publication retains its existing run-lookup preflight read.
func TestEvidenceLegacyAccountingUnsupportedWriters(t *testing.T) {
	scope := legacyEvidenceScope()
	now := time.Now().UTC().Truncate(time.Millisecond)
	digest := "sha256:" + strings.Repeat("f", 64)
	staged := extractionRunRec{
		RunID: "run", Repo: scope.Repository, Commit: scope.Commit, Domain: scope.Domain,
		Extractor: "v1", Status: "staged", StartedAt: now,
	}
	outcome := validOutcome()
	outcome.Scope, outcome.RunID = scope, ""
	outcome.Disposition = DomainOutcomeRetryableFailure
	for _, test := range []struct {
		name      string
		site      string
		sql       string
		call      func(context.Context, *Surreal) error
		preflight []legacyAccountingReply
		write     legacyAccountingReply
	}{
		{"pin", "PinRun", pinRunSQL, func(ctx context.Context, s *Surreal) error {
			return s.PinRun(ctx, "run", "kind")
		}, nil, legacyAccountingReply{pinRunSQL, []evidencePinRec{{RunID: "run", Kind: "kind"}}, map[string]any{
			"rid": extractionRunID("run"), "pin": evidencePinRecordID("run", "kind"), "kind": "kind",
		}}},
		{"publish", "publishExtractionRun", publishExtractionRunSQL, func(ctx context.Context, s *Surreal) error {
			return s.PublishExtractionRun(ctx, "run", CoverageManifest{SourceScopeDigest: digest})
		}, []legacyAccountingReply{
			{"SELECT * FROM $rid", []extractionRunRec{staged}, map[string]any{"rid": extractionRunID("run")}},
		}, legacyAccountingReply{publishExtractionRunSQL, []extractionRunRec{staged}, map[string]any{
			"rid": extractionRunID("run"), "attempt_rid": extractionAttemptID(scope),
			"outcome_rid": extractionDomainOutcomeID(scope.Repository, scope.Domain), "published_key": publishedKey(scope),
		}}},
		{"record", "RecordExtractionDomainOutcome", recordExtractionDomainOutcomeSQL, func(ctx context.Context, s *Surreal) error {
			return s.RecordExtractionDomainOutcome(ctx, outcome)
		}, nil, legacyAccountingReply{recordExtractionDomainOutcomeSQL, []extractionDomainOutcomeRec{{Repo: scope.Repository}}, map[string]any{
			"outcome_rid": extractionDomainOutcomeID(scope.Repository, scope.Domain), "attempt_rid": extractionAttemptID(scope),
			"disposition": string(DomainOutcomeRetryableFailure),
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, selected := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
					ctx := t.Context()
					var owner *storeCallOwner
					var controller *storeaccounting.Controller
					if selected {
						ctx, owner, controller = storeAccountingFixture(t, 40, 2)
					}
					db, native := storeAccountingDB(t, ctx, owner)
					replies := slices.Clone(test.preflight)
					if !selected {
						replies = append(replies, test.write)
					}
					legacyAccountingScript(t, native, controller, replies)
					s := &Surreal{db: db, accounting: owner}
					err := test.call(ctx, s)
					if native.calls != len(replies) {
						t.Fatalf("calls=%d error=%v; want %d", native.calls, err, len(replies))
					}
					if !selected {
						if err != nil {
							t.Fatalf("ordinary legacy writer changed: %v", err)
						}
						return
					}
					if !errors.Is(err, storeaccounting.ErrDescriptor) {
						t.Fatalf("legacy writer was not refused: %v", err)
					}
					// Failure delivery may already have reached the parent; only the
					// retained prefix matters here, not the controller's own error.
					prefix, _ := controller.Snapshot()
					if prefix.Transactions != 0 || prefix.Rows != 0 || prefix.Complete {
						t.Fatalf("refusal invented a write prefix: %+v", prefix)
					}
					diagnostic, ok := owner.PrivateRefusal()
					if !ok || diagnostic.Method != "query" || diagnostic.SQLPrefix != test.sql[:min(len(test.sql), 120)] {
						t.Fatalf("declared refusal lost its private diagnostic: %+v", diagnostic)
					}
					named := false
					frames := runtime.CallersFrames(diagnostic.Callers[:])
					for {
						frame, more := frames.Next()
						named = named || strings.Contains(frame.Function, "(*Surreal)."+test.site)
						if !more {
							break
						}
					}
					if !named {
						t.Fatalf("private diagnostic does not name %s", test.site)
					}
					before := native.calls
					if _, err := s.getRun(ctx, "run"); err == nil || native.calls != before {
						t.Fatal("legacy writer refusal did not latch later reads")
					}
				})
			}
		})
	}
}

// legacySourceRecipes closes one store file: every listed function submits
// exactly its declared recipes through the actual owner and connection, and no
// raw SDK submission remains. Files that no longer need the SDK import must
// not carry it; evidence.go keeps QueryResult for typed row helpers only.
func legacySourceRecipes(t *testing.T, path string, want map[string][]string, allowResults bool) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	sdkName := ""
	for _, imported := range file.Imports {
		if imported.Path.Value != `"github.com/surrealdb/surrealdb.go"` {
			continue
		}
		sdkName = "surrealdb"
		if imported.Name != nil {
			sdkName = imported.Name.Name
		}
	}
	if sdkName == "." {
		t.Fatalf("%s: dot SDK import defeats closed source coverage", path)
	}
	if sdkName != "" && !allowResults {
		t.Fatalf("%s still imports the raw SDK", path)
	}
	if sdkName != "" {
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if ok && receiver.Name == sdkName && selector.Sel.Name != "QueryResult" {
				t.Errorf("%s: raw SDK escape %s.%s", path, sdkName, selector.Sel.Name)
			}
			return true
		})
	}
	field := func(expr ast.Expr, name string) bool {
		selector, ok := expr.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != name {
			return false
		}
		receiver, ok := selector.X.(*ast.Ident)
		return ok && receiver.Name == "s"
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		expected, listed := want[function.Name.Name]
		var got []string
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			target := call.Fun
			if indexed, ok := target.(*ast.IndexExpr); ok {
				target = indexed.X
			}
			name, ok := target.(*ast.Ident)
			if !ok || name.Name != "storeQuery" {
				return true
			}
			if !listed {
				got = append(got, "storeQuery")
				return true
			}
			if len(call.Args) != 6 || !field(call.Args[1], "accounting") || !field(call.Args[2], "db") {
				t.Errorf("%s: %s lost its actual Surreal accounting owner or connection", path, function.Name)
				return true
			}
			recipe, ok := call.Args[5].(*ast.CallExpr)
			if !ok {
				t.Errorf("%s: %s recipe is not source-owned", path, function.Name)
				return true
			}
			kind, ok := recipe.Fun.(*ast.Ident)
			if !ok {
				t.Errorf("%s: %s recipe is not a closed constructor", path, function.Name)
				return true
			}
			got = append(got, kind.Name)
			return true
		})
		if !listed {
			if !allowResults && len(got) != 0 {
				t.Errorf("%s: %s submits %d undeclared queries", path, function.Name, len(got))
			}
			continue
		}
		if !slices.Equal(got, expected) {
			t.Errorf("%s: %s recipes %v, want %v", path, function.Name, got, expected)
		}
		delete(want, function.Name.Name)
	}
	if len(want) != 0 {
		t.Fatalf("%s: missing source functions: %v", path, want)
	}
}

func TestEvidenceLegacyAccountingSourceCoverage(t *testing.T) {
	legacySourceRecipes(t, "evidence.go", map[string][]string{
		"extractionRunExists":           {"storeRead"},
		"publishExtractionRun":          {"storeUnsupported"},
		"RecordExtractionDomainOutcome": {"storeUnsupported"},
		"LatestExtractionAttempt":       {"storeRead"},
		"LatestPublishedRun":            {"storeRead"},
		"ListAssertions":                {"storeRead"},
		"requireReverseAssertionIndex":  {"storeRead"},
		"ListReverseAssertions":         {"storeRead"},
		"ResolveEvidence":               {"storeRead"},
		"PinRun":                        {"storeUnsupported"},
	}, true)
}
