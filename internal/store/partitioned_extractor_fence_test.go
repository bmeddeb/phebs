package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidateid"
)

func TestPartitionedExpectedExtractorFence(t *testing.T) {
	s := newRetentionTestStore(t)
	ctx := t.Context()
	scope := ExtractionScope{Repository: "example.invalid/t512-field-fence", Commit: strings.Repeat("a", 40), Domain: "scip-proto-field"}
	if err := s.UpsertRepo(ctx, Repo{Name: scope.Repository, CloneURL: "https://example.invalid/field.git"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoIndexed(ctx, scope.Repository, scope.Commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	candidate := CandidateManifestPublication{Repository: scope.Repository, HeadCommit: scope.Commit, PolicyDigest: "sha256:" + strings.Repeat("1", 64), ManifestDigest: "sha256:" + strings.Repeat("2", 64), GenerationDigest: "sha256:" + strings.Repeat("3", 64), ManifestPath: candidateid.ManifestName(scope.Repository)}
	if err := s.PublishCandidateManifest(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	root := partitionedAssertionRun(t, s, scope, candidate, 1, 2)
	authority := PartitionedAssertionAuthority{Repository: scope.Repository, Domain: scope.Domain, RunID: root.RunID, PlanDigest: root.PlanDigest, RootDigest: root.RootDigest, Commit: scope.Commit, CandidateManifestDigest: candidate.ManifestDigest, CandidatePolicyDigest: candidate.PolicyDigest}
	query := AssertionQuery{Repo: scope.Repository, RunID: root.RunID}
	for _, version := range []string{"", "t40.7-test-v1", "1.4.0-released.1"} {
		t.Run(version, func(t *testing.T) {
			a := authority
			a.ExpectedExtractor = version
			rows, err := s.ListPartitionedAssertions(ctx, query, a)
			if version == "1.4.0-released.1" {
				if !errors.Is(err, ErrNotFound) || len(rows) != 0 {
					t.Fatalf("foreign version served: %+v %v", rows, err)
				}
				return
			}
			if err != nil || len(rows) != 2 {
				t.Fatalf("admitted version: %+v %v", rows, err)
			}
			atom := rows[0].Supporting[0]
			if resolved, err := s.ResolvePartitionedEvidence(ctx, a, atom); err != nil || resolved == nil {
				t.Fatalf("admitted locator: %+v %v", resolved, err)
			}
			wrong := a
			wrong.ExpectedExtractor = "1.4.0-released.1"
			if resolved, err := s.ResolvePartitionedEvidence(ctx, wrong, atom); !errors.Is(err, ErrNotFound) || resolved != nil {
				t.Fatalf("foreign version locator: %+v %v", resolved, err)
			}
			continued := query
			row := rows[0]
			continued.After = &AssertionCursor{Predicate: row.Predicate, Subject: row.Subject, Object: row.Object, ID: row.ID, RunID: row.RunID}
			if got, err := s.ListPartitionedAssertions(ctx, continued, wrong); !errors.Is(err, ErrConflict) || len(got) != 0 {
				t.Fatalf("foreign version continuation: %+v %v", got, err)
			}
		})
	}
}
