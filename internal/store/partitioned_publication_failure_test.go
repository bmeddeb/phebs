package store

import (
	"context"
	"errors"
	"github.com/bmeddeb/phebs/internal/candidateid"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"strings"
	"testing"
	"time"
)

func TestPartitionedPublicationFailureDiagnostics(t *testing.T) {
	for _, mode := range []string{"input", "query", "count", "identity", "run", "candidate", "ownership", "success"} {
		t.Run(mode, func(t *testing.T) {
			s := newRetentionTestStore(t)
			ctx := t.Context()
			repo := "synthetic.invalid/publication-diagnostics"
			commit := strings.Repeat("b", 40)
			digest := func(v string) string { return "sha256:" + strings.Repeat(v, 64) }
			if err := s.UpsertRepo(ctx, Repo{Name: repo, CloneURL: "https://example.invalid/diagnostic.git"}); err != nil {
				t.Fatal(err)
			}
			if err := s.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if err := s.PublishCandidateManifest(ctx, CandidateManifestPublication{Repository: repo, HeadCommit: commit, PolicyDigest: digest("2"), ManifestDigest: digest("1"), GenerationDigest: digest("3"), ManifestPath: candidateid.ManifestName(repo)}); err != nil {
				t.Fatal(err)
			}
			run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{Repository: repo, Commit: commit, Domain: "proto-contract"}, "publication-diagnostics-v1", digest("4"), digest("1"), "", PartitionedExtractionRunLimits{})
			if err != nil {
				t.Fatal(err)
			}
			pub := PartitionedExtractionDomain{Schema: PartitionedExtractionDomainSchema, Repository: repo, Domain: "proto-contract", RunID: run.ID, PlanDigest: digest("4"), RootDigest: digest("5"), CandidateDigest: digest("1"), SourceDigest: digest("6"), ObservationDigest: digest("7"), Plan: `{}`, Root: `{}`}
			want := readaccounting.PublicationFailureStoreInput
			switch mode {
			case "input":
				pub.RunID = ""
			case "query":
				want = readaccounting.PublicationFailureStoreQuery
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "count":
				want = readaccounting.PublicationFailureStoreCount
				pub.Facts = 1
			case "identity":
				want = readaccounting.PublicationFailureStoreIdentity
				if err := s.PublishPartitionedExtractionDomain(ctx, pub); err != nil {
					t.Fatal(err)
				}
				if _, err := surrealdb.Query[any](ctx, s.db, "UPDATE $rid SET source_digest = $digest RETURN NONE", map[string]any{"rid": partitionedDomainID(repo, pub.Domain), "digest": digest("8")}); err != nil {
					t.Fatal(err)
				}
			case "run":
				want = readaccounting.PublicationFailureStoreCount
				if err := s.AbortExtractionRun(ctx, run.ID); err != nil {
					t.Fatal(err)
				}
			case "candidate":
				want = readaccounting.PublicationFailureStoreCount
				if err := s.PublishPartitionedExtractionDomain(ctx, pub); err != nil {
					t.Fatal(err)
				}
				if err := s.PublishCandidateManifest(ctx, CandidateManifestPublication{Repository: repo, HeadCommit: commit, PolicyDigest: digest("8"), ManifestDigest: digest("9"), GenerationDigest: digest("0"), ManifestPath: candidateid.ManifestName(repo)}); err != nil {
					t.Fatal(err)
				}
			case "ownership":
				want = readaccounting.PublicationFailureStoreCount
				pub.PlanDigest = digest("8")
			}
			var stages []readaccounting.PublicationFailureStage
			var recorded error
			var flags readaccounting.PublicationFailureFlags
			observed := readaccounting.WithPublicationFailureObserver(ctx, func(stage readaccounting.PublicationFailureStage, err error, f readaccounting.PublicationFailureFlags) {
				stages = append(stages, stage)
				recorded = err
				flags = f
			})
			err = s.PublishPartitionedExtractionDomain(observed, pub)
			if mode == "success" {
				if err != nil || len(stages) != 0 {
					t.Fatal("healthy publication changed", err, stages)
				}
				return
			}
			if err == nil || len(stages) != 1 || stages[0] != want || err != recorded {
				t.Fatal("missing native publication failure diagnostic", stages, want, err)
			}
			if (mode == "count" || mode == "identity" || mode == "run" || mode == "candidate" || mode == "ownership") && !errors.Is(err, ErrConflict) {
				t.Fatal("conflict taxonomy changed", err)
			}
			if want == readaccounting.PublicationFailureStoreCount || want == readaccounting.PublicationFailureStoreIdentity {
				expected := readaccounting.PublicationFailureFlags{ResultsPresent: true, StorePredicatesAvailable: true,
					RunEligible: true, CandidateCurrent: true, CountsExact: true, RunOwned: true}
				switch mode {
				case "count":
					expected.CountsExact = false
				case "identity":
					expected.SingleResult, expected.MarkedSingle = true, true
				case "run":
					expected.RunEligible, expected.CandidateCurrent, expected.CountsExact, expected.RunOwned = false, false, false, false
				case "candidate":
					expected.CandidateCurrent = false
				case "ownership":
					expected.RunOwned = false
				}
				if flags != expected {
					t.Fatalf("missing or incorrect same-transaction predicate diagnostic: got=%+v want=%+v", flags, expected)
				}
			}
		})
	}
}
