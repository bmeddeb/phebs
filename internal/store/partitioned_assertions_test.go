package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidateid"
)

// This fixture uses the same staged evidence and atomic root-promotion APIs as
// partitioned workers. In particular, it never relabels a run as published.
func partitionedAssertionRun(
	t *testing.T, s *Surreal, scope ExtractionScope, candidate CandidateManifestPublication,
	ordinal, count int,
) PartitionedExtractionDomain {
	t.Helper()
	planDigest := fmt.Sprintf("sha256:%064x", ordinal*10+1)
	run, err := s.BeginPartitionedExtractionRun(t.Context(), scope, "t40.7-test-v1",
		planDigest, candidate.ManifestDigest, "", PartitionedExtractionRunLimits{
			Facts: int64(count), Rows: int64(count * 2), References: int64(count),
		})
	if err != nil {
		t.Fatal(err)
	}
	for index := range count {
		atoms, associations, assertions := t407Batch(scope.Repository, scope.Commit, index)
		assertions[0].Predicate = "DECLARES_OPERATION"
		assertions[0].Lineage = fmt.Sprintf("lineage-%d", index)
		if err := s.AddEvidenceChunk(t.Context(), run.ID,
			fmt.Sprintf("sha256:%064x", index+1), 1, atoms, associations, assertions); err != nil {
			t.Fatal(err)
		}
	}
	publication := PartitionedExtractionDomain{
		Schema: PartitionedExtractionDomainSchema, Repository: scope.Repository,
		Domain: scope.Domain, RunID: run.ID, PlanDigest: planDigest,
		RootDigest:        fmt.Sprintf("sha256:%064x", ordinal*10+2),
		CandidateDigest:   candidate.ManifestDigest,
		SourceDigest:      fmt.Sprintf("sha256:%064x", ordinal*10+3),
		ObservationDigest: fmt.Sprintf("sha256:%064x", ordinal*10+4),
		Facts:             int64(count), Rows: int64(count * 2), References: int64(count),
		Plan: `{}`, Root: `{}`,
	}
	if err := s.PublishPartitionedExtractionDomain(t.Context(), publication); err != nil {
		t.Fatal(err)
	}
	return publication
}

func TestPartitionedAssertionsExactRootVisibility(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	scope := ExtractionScope{
		Repository: "synthetic.invalid/t421r2-declarations",
		Commit:     strings.Repeat("a", 40), Domain: "proto-contract",
	}
	if err := s.UpsertRepo(ctx, Repo{Name: scope.Repository, CloneURL: "https://example.invalid/declarations.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, scope.Repository, scope.Commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	candidate := CandidateManifestPublication{
		Repository: scope.Repository, HeadCommit: scope.Commit,
		PolicyDigest:     "sha256:" + strings.Repeat("1", 64),
		ManifestDigest:   "sha256:" + strings.Repeat("2", 64),
		GenerationDigest: "sha256:" + strings.Repeat("3", 64),
		ManifestPath:     candidateid.ManifestName(scope.Repository),
	}
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	publication := partitionedAssertionRun(t, s, scope, candidate, 1, 3)
	authority := PartitionedAssertionAuthority{
		Repository: scope.Repository, Domain: scope.Domain, RunID: publication.RunID,
		PlanDigest: publication.PlanDigest, RootDigest: publication.RootDigest,
		Commit: scope.Commit, UnitDigest: scope.UnitDigest,
		CandidateManifestDigest: candidate.ManifestDigest, CandidatePolicyDigest: candidate.PolicyDigest,
	}
	query := AssertionQuery{
		Repo: scope.Repository, RunID: publication.RunID,
		Predicate: "DECLARES_OPERATION", Scope: &scope,
	}
	sealed, err := s.getRun(ctx, publication.RunID)
	if err != nil || sealed.Status != "staged" || !sealed.PartitionSealed || sealed.PartitionActive {
		t.Fatalf("partitioned-native publication = %+v, %v", sealed, err)
	}
	legacy, err := s.ListAssertions(ctx, query)
	if err != nil || len(legacy) != 0 {
		t.Fatalf("legacy reader exposed partitioned staged run: %+v, %v", legacy, err)
	}
	rows, err := s.ListPartitionedAssertions(ctx, query, authority)
	if err != nil || len(rows) != 3 {
		t.Fatalf("current exact partitioned assertions = %+v, %v", rows, err)
	}
	for _, row := range rows {
		if row.RunID != publication.RunID || row.Repo != scope.Repository || row.Predicate != query.Predicate || len(row.Supporting) != 1 {
			t.Fatalf("assertion escaped exact domain run: %+v", row)
		}
	}
	atomID := rows[0].Supporting[0]
	requireNoResolution := func(t *testing.T, selected PartitionedAssertionAuthority, atom string, cause error) {
		t.Helper()
		resolved, err := s.ResolvePartitionedEvidence(ctx, selected, atom)
		if err == nil || cause != nil && !errors.Is(err, cause) || resolved != nil {
			t.Fatalf("ineligible supporting evidence = %+v, %v", resolved, err)
		}
	}
	cursor := func(row Assertion) *AssertionCursor {
		return &AssertionCursor{Predicate: row.Predicate, Subject: row.Subject,
			Object: row.Object, ID: row.ID, RunID: row.RunID}
	}

	t.Run("supporting evidence uses exact native root", func(t *testing.T) {
		for _, row := range rows {
			resolved, err := s.ResolvePartitionedEvidence(ctx, authority, row.Supporting[0])
			if err != nil || resolved == nil || resolved.Atom.ID != row.Supporting[0] || len(resolved.Occurrences) != 1 {
				t.Fatalf("native supporting evidence = %+v, %v", resolved, err)
			}
			occurrence := resolved.Occurrences[0]
			if occurrence.RunID != publication.RunID || occurrence.Repo != scope.Repository ||
				occurrence.Commit != scope.Commit || occurrence.Path != row.Subject ||
				occurrence.VisibilityScope != "repo:"+scope.Repository || occurrence.AtomID != resolved.Atom.ID {
				t.Fatalf("native evidence escaped exact run: %+v", resolved)
			}
		}
		resolved, err := s.ResolveEvidence(ctx, scope.Repository, publication.RunID, atomID)
		if !errors.Is(err, ErrNotFound) || resolved != nil {
			t.Fatalf("legacy resolver exposed partitioned staged evidence: %+v, %v", resolved, err)
		}
		requireNoResolution(t, authority, "absent-atom", ErrNotFound)
	})

	t.Run("legacy publication visibility is unchanged", func(t *testing.T) {
		legacyScope := scope
		legacyScope.Domain = "legacy-declarations"
		run, err := s.BeginExtractionRun(ctx, legacyScope, "t40.7-test-v1")
		if err != nil {
			t.Fatal(err)
		}
		atoms, associations, assertions := t407Batch(scope.Repository, scope.Commit, 8)
		assertions[0].Predicate = query.Predicate
		if err := s.AddEvidenceChunk(ctx, run.ID, "sha256:"+strings.Repeat("7", 64),
			1, atoms, associations, assertions); err != nil {
			t.Fatal(err)
		}
		legacyQuery := query
		legacyQuery.RunID, legacyQuery.Scope = run.ID, &legacyScope
		stagedRows, err := s.ListAssertions(ctx, legacyQuery)
		if err != nil || len(stagedRows) != 0 {
			t.Fatalf("legacy staged assertions = %+v, %v", stagedRows, err)
		}
		if err := s.PublishExtractionRun(ctx, run.ID, CoverageManifest{
			AssertionCount: 1, AtomCount: 1, CorpusFileCount: 1,
			CandidateFileCount: 1, ReadFileCount: 1, ReadBytes: 1,
			SourceScopeDigest: candidate.PolicyDigest,
		}); err != nil {
			t.Fatal(err)
		}
		publishedRows, err := s.ListAssertions(ctx, legacyQuery)
		if err != nil || len(publishedRows) != 1 || publishedRows[0].RunID != run.ID {
			t.Fatalf("legacy published assertions = %+v, %v", publishedRows, err)
		}
		resolved, err := s.ResolveEvidence(ctx, scope.Repository, run.ID, atoms[0].ID)
		if err != nil || resolved == nil || resolved.Atom.ID != atoms[0].ID || len(resolved.Occurrences) != 1 {
			t.Fatalf("legacy published supporting evidence = %+v, %v", resolved, err)
		}
		requireNoResolution(t, authority, atoms[0].ID, ErrNotFound)
		legacyAuthority := authority
		legacyAuthority.Domain, legacyAuthority.RunID = legacyScope.Domain, run.ID
		got, err := s.ListPartitionedAssertions(ctx, legacyQuery, legacyAuthority)
		if !errors.Is(err, ErrNotFound) || len(got) != 0 {
			t.Fatalf("partitioned reader accepted legacy publication: %+v, %v", got, err)
		}
		requireNoResolution(t, legacyAuthority, atoms[0].ID, ErrNotFound)
	})

	t.Run("bounded stable pages", func(t *testing.T) {
		paged := query
		paged.Limit, paged.AllowTruncate = 1, true
		for index := range rows {
			page, err := s.ListPartitionedAssertions(ctx, paged, authority)
			want := min(2, len(rows)-index)
			if err != nil || len(page) != want || page[0].ID != rows[index].ID {
				t.Fatalf("page %d = %+v, %v", index, page, err)
			}
			paged.After = cursor(page[0])
		}
		page, err := s.ListPartitionedAssertions(ctx, paged, authority)
		if err != nil || len(page) != 0 {
			t.Fatalf("terminal page = %+v, %v", page, err)
		}
		paged.After, paged.AllowTruncate = nil, false
		if _, err := s.ListPartitionedAssertions(ctx, paged, authority); !errors.Is(err, ErrResultLimit) {
			t.Fatalf("unacknowledged truncation = %v", err)
		}
		paged.Limit = 5001
		if _, err := s.ListPartitionedAssertions(ctx, paged, authority); !errors.Is(err, ErrResultLimit) {
			t.Fatalf("oversized page = %v", err)
		}
		paged.Limit, paged.After = 1, cursor(rows[0])
		paged.After.RunID = "foreign-run"
		if _, err := s.ListPartitionedAssertions(ctx, paged, authority); !errors.Is(err, ErrConflict) {
			t.Fatalf("foreign-run continuation = %v", err)
		}
	})

	t.Run("query filters stay bounded", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*AssertionQuery)
			want   int
		}{
			{"predicate", func(q *AssertionQuery) { q.Predicate = "CALLS_OPERATION" }, 0},
			{"subject", func(q *AssertionQuery) { q.Subject = rows[1].Subject }, 1},
			{"object", func(q *AssertionQuery) { q.Object = rows[1].Object }, 1},
			{"object prefix", func(q *AssertionQuery) { q.ObjectPrefix = "/demo.v1.Service/Call1" }, 1},
			{"lineage", func(q *AssertionQuery) { q.Lineage = rows[1].Lineage }, 1},
		} {
			t.Run(test.name, func(t *testing.T) {
				filtered := query
				test.mutate(&filtered)
				got, err := s.ListPartitionedAssertions(ctx, filtered, authority)
				if err != nil || len(got) != test.want {
					t.Fatalf("filtered page = %+v, %v", got, err)
				}
			})
		}
	})

	t.Run("authority components cannot widen selection", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*PartitionedAssertionAuthority)
		}{
			{"repository", func(a *PartitionedAssertionAuthority) { a.Repository += "-foreign" }},
			{"domain", func(a *PartitionedAssertionAuthority) { a.Domain = "thrift-contract" }},
			{"run", func(a *PartitionedAssertionAuthority) { a.RunID = "absent-run" }},
			{"plan", func(a *PartitionedAssertionAuthority) { a.PlanDigest = candidate.PolicyDigest }},
			{"root", func(a *PartitionedAssertionAuthority) { a.RootDigest = candidate.PolicyDigest }},
			{"commit", func(a *PartitionedAssertionAuthority) { a.Commit = strings.Repeat("b", 40) }},
			{"unit", func(a *PartitionedAssertionAuthority) { a.UnitDigest = candidate.PolicyDigest }},
			{"candidate manifest", func(a *PartitionedAssertionAuthority) { a.CandidateManifestDigest = candidate.PolicyDigest }},
			{"candidate policy", func(a *PartitionedAssertionAuthority) { a.CandidatePolicyDigest = candidate.ManifestDigest }},
		} {
			t.Run(test.name, func(t *testing.T) {
				changed := authority
				test.mutate(&changed)
				got, err := s.ListPartitionedAssertions(ctx, query, changed)
				if err == nil || len(got) != 0 {
					t.Fatalf("mismatched authority exposed assertions: %+v, %v", got, err)
				}
				requireNoResolution(t, changed, atomID, nil)
			})
		}
	})

	t.Run("unpublished staged run is not a fallback", func(t *testing.T) {
		staged, err := s.BeginPartitionedExtractionRun(ctx, scope, "t40.7-test-v1",
			"sha256:"+strings.Repeat("9", 64), candidate.ManifestDigest, "",
			PartitionedExtractionRunLimits{Facts: 1, Rows: 2, References: 1})
		if err != nil {
			t.Fatal(err)
		}
		atoms, associations, assertions := t407Batch(scope.Repository, scope.Commit, 4)
		assertions[0].Predicate = query.Predicate
		if err := s.AddEvidenceChunk(ctx, staged.ID, "sha256:"+strings.Repeat("8", 64),
			1, atoms, associations, assertions); err != nil {
			t.Fatal(err)
		}
		changed, stagedQuery := authority, query
		changed.RunID, stagedQuery.RunID = staged.ID, staged.ID
		changed.PlanDigest = staged.PartitionPlanDigest
		got, err := s.ListPartitionedAssertions(ctx, stagedQuery, changed)
		if !errors.Is(err, ErrNotFound) || len(got) != 0 {
			t.Fatalf("unpublished staged assertions = %+v, %v", got, err)
		}
		requireNoResolution(t, changed, atoms[0].ID, ErrNotFound)
	})

	t.Run("foreign domain remains isolated", func(t *testing.T) {
		foreignScope := scope
		foreignScope.Domain = "thrift-contract"
		foreign := partitionedAssertionRun(t, s, foreignScope, candidate, 2, 1)
		changed, foreignQuery := authority, query
		changed.RunID, foreignQuery.RunID = foreign.RunID, foreign.RunID
		changed.PlanDigest, changed.RootDigest = foreign.PlanDigest, foreign.RootDigest
		got, err := s.ListPartitionedAssertions(ctx, foreignQuery, changed)
		if !errors.Is(err, ErrNotFound) || len(got) != 0 {
			t.Fatalf("foreign-domain assertions = %+v, %v", got, err)
		}
		requireNoResolution(t, changed, atomID, ErrNotFound)
	})

	t.Run("supporting occurrence ceiling is not truncation", func(t *testing.T) {
		boundedScope := scope
		boundedScope.Domain = "bounded-occurrences"
		for _, count := range []int{maxEvidenceOccurrences, maxEvidenceOccurrences + 1} {
			t.Run(fmt.Sprint(count), func(t *testing.T) {
				planDigest := fmt.Sprintf("sha256:%064x", count*10+1)
				run, err := s.BeginPartitionedExtractionRun(ctx, boundedScope, "t40.7-test-v1",
					planDigest, candidate.ManifestDigest, "", PartitionedExtractionRunLimits{
						Facts: int64(count), Rows: int64(count * 2), References: int64(count),
					})
				if err != nil {
					t.Fatal(err)
				}
				atoms, occurrences, assertions := t407Batch(scope.Repository, scope.Commit, 0)
				seedAtom, seedOccurrence, seedAssertion := atoms[0], occurrences[0], assertions[0]
				atoms = make([]EvidenceAtom, count)
				occurrences = make([]SnapshotEvidence, count)
				assertions = make([]Assertion, count)
				for index := range count {
					atoms[index] = seedAtom
					occurrences[index] = seedOccurrence
					occurrences[index].Path = fmt.Sprintf("src/occurrence_%03d.go", index)
					assertions[index] = seedAssertion
					assertions[index].Predicate, assertions[index].Subject = query.Predicate, occurrences[index].Path
				}
				if err := s.AddEvidenceChunk(ctx, run.ID, fmt.Sprintf("sha256:%064x", count),
					count, atoms, occurrences, assertions); err != nil {
					t.Fatal(err)
				}
				boundedPublication := publication
				boundedPublication.Domain, boundedPublication.RunID = boundedScope.Domain, run.ID
				boundedPublication.PlanDigest = planDigest
				boundedPublication.RootDigest = fmt.Sprintf("sha256:%064x", count*10+2)
				boundedPublication.Facts, boundedPublication.Rows, boundedPublication.References = int64(count), int64(count*2), int64(count)
				if err := s.PublishPartitionedExtractionDomain(ctx, boundedPublication); err != nil {
					t.Fatal(err)
				}
				boundedAuthority := authority
				boundedAuthority.Domain, boundedAuthority.RunID = boundedScope.Domain, run.ID
				boundedAuthority.PlanDigest, boundedAuthority.RootDigest = planDigest, boundedPublication.RootDigest
				if count > maxEvidenceOccurrences {
					requireNoResolution(t, boundedAuthority, atoms[0].ID, ErrResultLimit)
					return
				}
				resolved, err := s.ResolvePartitionedEvidence(ctx, boundedAuthority, atoms[0].ID)
				if err != nil || resolved == nil || len(resolved.Occurrences) != count {
					t.Fatalf("bounded supporting occurrences = %+v, %v", resolved, err)
				}
			})
		}
	})

	t.Run("sealed run metadata is rechecked", func(t *testing.T) {
		for _, test := range []struct{ name, mutate, restore string }{
			{"quarantine", "retention_quarantined = true", "retention_quarantined = false"},
			{"unsealed", "partition_sealed = false", "partition_sealed = true"},
			{"active", "partition_active = true", "partition_active = false"},
			{"wrong root", "partition_root_digest = $other", "partition_root_digest = $root"},
			{"wrong plan", "partition_plan_digest = $other", "partition_plan_digest = $plan"},
			{"wrong candidate", "partition_candidate_digest = $other", "partition_candidate_digest = $candidate"},
			{"foreign repository", "repo = 'synthetic.invalid/foreign'", "repo = $repo"},
			{"foreign domain", "domain = 'thrift-contract'", "domain = $domain"},
			{"foreign unit", "unit_digest = $other", "unit_digest = ''"},
			{"ambiguous identity", "evidence_migration_ambiguous_run_id = 'foreign-run'", "evidence_migration_ambiguous_run_id = NONE"},
			{"legacy status", "status = 'published'", "status = 'staged'"},
		} {
			t.Run(test.name, func(t *testing.T) {
				vars := map[string]any{
					"rid": extractionRunID(publication.RunID), "other": candidate.PolicyDigest,
					"root": publication.RootDigest, "plan": publication.PlanDigest,
					"candidate": candidate.ManifestDigest, "repo": scope.Repository, "domain": scope.Domain,
				}
				requireRetentionStatement(t, ctx, s, "UPDATE $rid SET "+test.mutate, vars)
				t.Cleanup(func() {
					requireRetentionStatement(t, ctx, s, "UPDATE $rid SET "+test.restore, vars)
				})
				got, err := s.ListPartitionedAssertions(ctx, query, authority)
				if !errors.Is(err, ErrNotFound) || len(got) != 0 {
					t.Fatalf("invalid sealed run exposed assertions: %+v, %v", got, err)
				}
				requireNoResolution(t, authority, atomID, ErrNotFound)
			})
		}
	})

	t.Run("indexed repository remains current", func(t *testing.T) {
		for _, test := range []struct{ name, mutate, restore string }{
			{"deleting", "deleting = true", "deleting = false"},
			{"indexed commit", "indexed_commit_hash = $other", "indexed_commit_hash = $commit"},
		} {
			t.Run(test.name, func(t *testing.T) {
				vars := map[string]any{"rid": repoID(scope.Repository),
					"other": strings.Repeat("b", 40), "commit": scope.Commit}
				requireRetentionStatement(t, ctx, s, "UPDATE $rid SET "+test.mutate, vars)
				t.Cleanup(func() {
					requireRetentionStatement(t, ctx, s, "UPDATE $rid SET "+test.restore, vars)
				})
				got, err := s.ListPartitionedAssertions(ctx, query, authority)
				if !errors.Is(err, ErrNotFound) || len(got) != 0 {
					t.Fatalf("stale repository exposed assertions: %+v, %v", got, err)
				}
				requireNoResolution(t, authority, atomID, ErrNotFound)
			})
		}
	})

	t.Run("superseded root refuses continuation", func(t *testing.T) {
		oldPage := query
		oldPage.Limit, oldPage.AllowTruncate = 1, true
		first, err := s.ListPartitionedAssertions(ctx, oldPage, authority)
		if err != nil || len(first) != 2 {
			t.Fatalf("first page = %+v, %v", first, err)
		}
		replacement := partitionedAssertionRun(t, s, scope, candidate, 3, 1)
		got, err := s.ListPartitionedAssertions(ctx, query, authority)
		if !errors.Is(err, ErrNotFound) || len(got) != 0 {
			t.Fatalf("superseded first page = %+v, %v", got, err)
		}
		requireNoResolution(t, authority, atomID, ErrNotFound)
		oldPage.After = cursor(first[0])
		got, err = s.ListPartitionedAssertions(ctx, oldPage, authority)
		if !errors.Is(err, ErrConflict) || len(got) != 0 {
			t.Fatalf("superseded continuation = %+v, %v", got, err)
		}
		current, currentQuery := authority, query
		current.RunID, currentQuery.RunID = replacement.RunID, replacement.RunID
		current.PlanDigest, current.RootDigest = replacement.PlanDigest, replacement.RootDigest
		got, err = s.ListPartitionedAssertions(ctx, currentQuery, current)
		if err != nil || len(got) != 1 || got[0].RunID != replacement.RunID {
			t.Fatalf("replacement exact page = %+v, %v", got, err)
		}
		publication, authority, query = replacement, current, currentQuery
	})

	t.Run("absent root cannot expose sealed assertions", func(t *testing.T) {
		requireRetentionStatement(t, ctx, s, "DELETE $rid", map[string]any{
			"rid": partitionedDomainID(scope.Repository, scope.Domain),
		})
		got, err := s.ListPartitionedAssertions(ctx, query, authority)
		if !errors.Is(err, ErrNotFound) || len(got) != 0 {
			t.Fatalf("absent root exposed assertions: %+v, %v", got, err)
		}
		requireNoResolution(t, authority, atomID, ErrNotFound)
		if err := s.PublishPartitionedExtractionDomain(ctx, publication); err != nil {
			t.Fatal(err)
		}
		got, err = s.ListPartitionedAssertions(ctx, query, authority)
		if err != nil || len(got) != 1 {
			t.Fatalf("restored exact root = %+v, %v", got, err)
		}
	})

	t.Run("candidate transition invalidates old root", func(t *testing.T) {
		replacement := candidate
		replacement.PolicyDigest = "sha256:" + strings.Repeat("4", 64)
		replacement.ManifestDigest = "sha256:" + strings.Repeat("5", 64)
		replacement.GenerationDigest = "sha256:" + strings.Repeat("6", 64)
		if err := s.PublishCandidateManifest(ctx, replacement); err != nil {
			t.Fatal(err)
		}
		got, err := s.ListPartitionedAssertions(ctx, query, authority)
		if !errors.Is(err, ErrNotFound) || len(got) != 0 {
			t.Fatalf("stale candidate binding exposed assertions: %+v, %v", got, err)
		}
		requireNoResolution(t, authority, atomID, ErrNotFound)
	})
}
