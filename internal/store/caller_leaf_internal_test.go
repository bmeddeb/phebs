package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/bmeddeb/phebs/internal/callerleafid"
)

func internalCallerDigest(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

func internalCallerGeneration() CallerGenerationIdentity {
	return CallerGenerationIdentity{
		Repository: "github.com/acme/cap", HeadCommit: strings.Repeat("1", 40),
		DeclarationSetDigest:     internalCallerDigest('1'),
		CandidateManifestDigest:  internalCallerDigest('2'),
		CandidatePolicyDigest:    internalCallerDigest('3'),
		CandidateControlRevision: 1,
		ResolverGenerationDigest: internalCallerDigest('4'),
		ResolverManifestDigest:   internalCallerDigest('5'),
		ResolverControlRevision:  1,
		ResolverWriterSchema:     resolverCatalogWriterSchema,
		SourceLanePolicy:         CallerBaseSourceLanePolicy,
		CallerPolicyDigest:       internalCallerDigest('6'),
		ExtractorSetDigest:       internalCallerDigest('7'),
	}
}

func TestValidateCallerJobRejectsRelabeledForeignQueueLease(t *testing.T) {
	job := Job{
		ID: "extraction_job:foreign", Kind: JobCallerLeaf,
		Target: "github.com/acme/cap", Status: StatusRunning,
		LeaseToken: "lease", ClaimedBy: "worker",
	}
	if err := validateCallerJob(job, job.Target); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("relabeled foreign queue lease = %v, want ErrLeaseLost", err)
	}
	job.ID = "caller_leaf_job:owned"
	if err := validateCallerJob(job, job.Target); err != nil {
		t.Fatalf("valid caller lease: %v", err)
	}
}

func TestCallerGenerationAdmissionRetainsTerminalCapPlusOne(t *testing.T) {
	generation, err := prepareCallerGeneration(internalCallerGeneration())
	if err != nil {
		t.Fatal(err)
	}
	pairs := make([]CallerLeafPair, 9)
	outcomes := make([]CallerLeafOutcome, 9)
	for index := range pairs {
		resultCount := maxCallerPairResults
		if index == len(pairs)-1 {
			resultCount = 1
		}
		pair := CallerLeafPair{
			Domain:           fmt.Sprintf("caller-%02d", index),
			ExtractorVersion: "1.0.0", LeafAdapterVersion: "direct-syntax-base-v1",
			LeafOrdinal: index, LeafPrefix: "00", LeafPrefixBits: 2,
			CandidateMemberName:  fmt.Sprintf("candidate-%02d.ndjson", index),
			CandidateRecordCount: 1, CandidateDeclaredBytes: 1,
			CandidateContentBytes: 1, CandidateContentDigest: internalCallerDigest('8'),
		}
		pair, err = prepareCallerLeafPair(generation, pair)
		if err != nil {
			t.Fatal(err)
		}
		contentDigest := internalCallerDigest('9')
		outcome, err := prepareCallerLeafOutcome(CallerLeafOutcome{
			Generation: generation, Pair: pair, Disposition: CallerLeafSucceeded,
			Receipt: &CallerLeafArtifactReceipt{
				ArtifactName: callerleafid.ArtifactName(pair.PairDigest, contentDigest), ArtifactCount: 1,
				ResultCount: resultCount, CanonicalBytes: 0, StagingBytes: 0,
				ContentDigest: contentDigest, MetadataDigest: internalCallerDigest('a'),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		pairs[index] = pair
		outcomes[index] = outcome
	}
	if _, _, err := prepareCallerGenerationAdmission(
		CallerGenerationAdmission{
			Generation: generation, Disposition: CallerGenerationTerminalGenerationRefusal,
		}, pairs[:1], outcomes[:1],
	); err == nil {
		t.Fatal("terminal admission accepted an all-successful under-cap generation")
	}
	if _, _, err := prepareCallerGenerationAdmission(
		CallerGenerationAdmission{
			Generation: generation, Disposition: CallerGenerationAdmitted,
		}, pairs, outcomes,
	); err == nil {
		t.Fatal("admitted aggregate accepted result cap+1")
	}
	terminal, _, err := prepareCallerGenerationAdmission(
		CallerGenerationAdmission{
			Generation:  generation,
			Disposition: CallerGenerationTerminalGenerationRefusal,
		}, pairs, outcomes,
	)
	if err != nil {
		t.Fatalf("terminal cap+1 receipt: %v", err)
	}
	if terminal.ResultCount != maxCallerGenerationResults+1 ||
		terminal.PairCount != len(pairs) || terminal.ArtifactCount != len(pairs) {
		t.Fatalf("terminal cap+1 aggregate = %+v", terminal)
	}
	forged := terminal
	forged.PairSetDigest = internalCallerDigest('f')
	if err := ValidateCallerGenerationAdmission(forged, pairs, outcomes); err == nil {
		t.Fatal("canonical-but-forged admission aggregate was accepted")
	}
}

func TestCallerLeafOutcomeRejectsForgedSiblingArtifactName(t *testing.T) {
	generation, err := prepareCallerGeneration(internalCallerGeneration())
	if err != nil {
		t.Fatal(err)
	}
	pair := CallerLeafPair{
		Domain: "grpc-caller", ExtractorVersion: "1.0.0",
		LeafAdapterVersion: "direct-syntax-base-v1",
		LeafOrdinal:        0, LeafPrefix: "00", LeafPrefixBits: 2,
		CandidateMemberName: "candidate-00.ndjson", CandidateRecordCount: 1,
		CandidateDeclaredBytes: 1, CandidateContentBytes: 1,
		CandidateContentDigest: internalCallerDigest('8'),
	}
	pair, err = prepareCallerLeafPair(generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	sibling := pair
	sibling.LeafOrdinal = 1
	sibling.LeafPrefix = "01"
	sibling.CandidateMemberName = "candidate-01.ndjson"
	sibling, err = prepareCallerLeafPair(generation, sibling)
	if err != nil {
		t.Fatal(err)
	}
	contentDigest := internalCallerDigest('9')
	_, err = prepareCallerLeafOutcome(CallerLeafOutcome{
		Generation: generation, Pair: pair, Disposition: CallerLeafSucceeded,
		Receipt: &CallerLeafArtifactReceipt{
			ArtifactName:  callerleafid.ArtifactName(sibling.PairDigest, contentDigest),
			ArtifactCount: 1, ContentDigest: contentDigest,
			MetadataDigest: internalCallerDigest('a'),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "outside its exact pair") {
		t.Fatalf("forged sibling artifact receipt = %v", err)
	}
}

func TestCallerLeafReceiptRejectsSourceByteCapPlusOne(t *testing.T) {
	receipt := CallerLeafArtifactReceipt{
		ArtifactName: "leaf-v1-" + strings.Repeat("1", 64) + "-" +
			strings.Repeat("2", 64) + ".ndjson",
		ArtifactCount: 1, ContentDigest: internalCallerDigest('2'),
		MetadataDigest:  internalCallerDigest('3'),
		SourceBlobBytes: maxCallerPairSourceBytes + 1,
	}
	if err := validateCallerReceipt(receipt); err == nil {
		t.Fatal("caller receipt accepted source-byte cap+1")
	}
}

func TestCallerLeafReceiptAllowsEmptyAndRejectsOutOfLeafRead(t *testing.T) {
	receipt := CallerLeafArtifactReceipt{
		ArtifactName: "leaf-empty.ndjson", ArtifactCount: 1,
		ContentDigest: internalCallerDigest('1'), MetadataDigest: internalCallerDigest('2'),
	}
	if err := validateCallerReceipt(receipt); err != nil {
		t.Fatalf("explicit empty receipt: %v", err)
	}
	receipt.OutOfLeafReads = 1
	if err := validateCallerReceipt(receipt); err == nil {
		t.Fatal("out-of-leaf read was accepted")
	}
}

func TestCallerLeafWriterMarkerAndGenerationGuard(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	marker, err := surrealdb.Query[[]struct {
		Version string `json:"version"`
	}](ctx, s.db, "SELECT version FROM $rid", map[string]any{
		"rid": callerLeafMigrationID(),
	})
	if err != nil || len(firstDomainRows(marker)) != 1 ||
		firstDomainRows(marker)[0].Version != callerLeafWriterMigrationVersion {
		t.Fatalf("writer marker = %+v, %v", marker, err)
	}

	guarded, queryErr := surrealdb.Query[any](ctx, s.db, `
CREATE caller_leaf_outcome CONTENT {
	writer_schema: 'phebs-caller-leaf-store-retired'
};`, nil)
	failed := queryErr != nil
	if guarded != nil {
		for _, result := range *guarded {
			failed = failed || result.Error != nil
		}
	}
	if !failed {
		t.Fatal("retired caller-leaf writer bypassed generation guard")
	}

	updated, err := surrealdb.Query[any](ctx, s.db,
		"UPDATE $rid SET version = 'future-writer' RETURN NONE",
		map[string]any{"rid": callerLeafMigrationID()})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *updated {
		if result.Error != nil {
			t.Fatal(result.Error.Message)
		}
	}
	if err := s.ClearAllCallerLeafStateForRestore(ctx); err == nil ||
		!strings.Contains(err.Error(), "writer generation is not active") {
		t.Fatalf("restore clear with inactive writer = %v", err)
	}
	if err := s.migrateCallerLeafWriter(ctx); err == nil ||
		!strings.Contains(err.Error(), "unsupported caller-leaf writer generation") {
		t.Fatalf("future writer marker = %v", err)
	}
}

func TestCallerLeafRestoreClearDoesNotDecodeMalformedRows(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	s, err := OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	generation, err := prepareCallerGeneration(internalCallerGeneration())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := prepareCallerLeafPair(generation, CallerLeafPair{
		Domain: "grpc-caller", ExtractorVersion: "1.0.0",
		LeafAdapterVersion: "direct-syntax-base-v1",
		LeafOrdinal:        0, LeafPrefix: "00", LeafPrefixBits: 2,
		CandidateMemberName: "candidate.ndjson", CandidateRecordCount: 1,
		CandidateDeclaredBytes: 1, CandidateContentBytes: 1,
		CandidateContentDigest: internalCallerDigest('8'),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireCandidateRawQuery(t, ctx, s, `
		CREATE $outcome_rid CONTENT {
			repository: $repository,
			generation: {},
			generation_digest: $generation_digest,
			pair: {},
			domain: 'grpc-caller',
			leaf_prefix: '00',
			disposition: 'succeeded',
			receipt: NONE,
			writer_schema: $writer_schema,
			recorded_at: time::now()
		};
		CREATE $admission_rid CONTENT {
			repository: $repository,
			generation: {},
			generation_digest: $generation_digest,
			disposition: 'admitted',
			pair_set_digest: 'not-a-digest',
			pair_count: 0,
			artifact_count: 0,
			result_count: 0,
			abstention_count: 0,
			canonical_bytes: 0,
			staging_bytes: 0,
			peak_open_files: 0,
			writer_schema: $writer_schema,
			recorded_at: time::now()
		};`, map[string]any{
		"repository":        generation.Repository,
		"generation_digest": generation.Digest,
		"outcome_rid":       callerLeafOutcomeID(generation, pair),
		"admission_rid":     callerGenerationAdmissionID(generation),
		"writer_schema":     CallerLeafWriterSchema,
	})

	if _, err := s.GetCallerLeafOutcome(ctx, generation, pair); !errors.Is(
		err, ErrInvalidCallerLeafState,
	) {
		t.Fatalf("malformed caller outcome = %v, want invalid state", err)
	}
	if _, err := s.GetCallerGenerationAdmission(ctx, generation); !errors.Is(
		err, ErrInvalidCallerLeafState,
	) {
		t.Fatalf("malformed caller admission = %v, want invalid state", err)
	}
	if err := s.ClearAllCallerLeafStateForRestore(ctx); err != nil {
		t.Fatalf("raw restore clear: %v", err)
	}
	if _, err := s.GetCallerLeafOutcome(ctx, generation, pair); !errors.Is(
		err, ErrNotFound,
	) {
		t.Fatalf("outcome after restore clear = %v, want not found", err)
	}
	if _, err := s.GetCallerGenerationAdmission(ctx, generation); !errors.Is(
		err, ErrNotFound,
	) {
		t.Fatalf("admission after restore clear = %v, want not found", err)
	}

	rows, err := surrealdb.Query[[]struct {
		ID any `json:"id"`
	}](ctx, s.db, "SELECT id FROM caller_leaf_outcome", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := firstDomainRows(rows); len(got) != 0 {
		t.Fatalf("outcomes after restore clear = %+v", got)
	}
}
