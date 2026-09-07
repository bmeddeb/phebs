package extract

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

type policyChunkEvidence struct {
	*memoryEvidence
	ids    []string
	counts []int
}

func (evidence *policyChunkEvidence) AddEvidenceChunk(ctx context.Context, runID, chunkID string, facts int,
	atoms []store.EvidenceAtom, assocs []store.SnapshotEvidence, asserts []store.Assertion,
) error {
	evidence.ids = append(evidence.ids, chunkID)
	evidence.counts = append(evidence.counts, facts)
	return evidence.memoryEvidence.AddEvidenceChunk(ctx, runID, chunkID, facts, atoms, assocs, asserts)
}

func TestEvidencePartitionVersionedGrouping(t *testing.T) {
	var encoded [2]int64
	var ids [2][]string
	for index, selected := range []bool{false, true} {
		t.Run(fmt.Sprint(selected), func(t *testing.T) {
			const content = "message Fixture {}\n"
			plan, path, commit := partitionExecutorPlanForAccounting(t, "proto-contract", "partition-test-v1", ".proto", content, selected)
			evidence := &policyChunkEvidence{memoryEvidence: newMemoryEvidence()}
			run, err := evidence.BeginExtractionRun(t.Context(), store.ExtractionScope{Repository: plan.Repository, Commit: commit, Domain: plan.Domain}, plan.ExtractorVersion)
			if err != nil {
				t.Fatal(err)
			}
			extractor := unitExtractor{
				domain: plan.Domain, version: plan.ExtractorVersion,
				candidate: func(path string) bool { return strings.HasSuffix(path, ".proto") },
				extract: func(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
					blob, err := corpus.Read(ctx, path)
					if err != nil {
						return sdk.Coverage{}, err
					}
					for index := range 340 {
						fact := sdk.Fact{
							Path: path, StartLine: 1, EndLine: 1,
							Atom:      sdk.AtomInput{SchemaVersion: "partition-v1", BlobDigest: blob.Digest, EndByte: len(blob.Content), RuleID: "partition-rule", AdapterConfigDigest: "none", FactFingerprint: fmt.Sprintf("fact-%04d", index)},
							Assertion: sdk.AssertionInput{Predicate: "DECLARES", Subject: path, Object: fmt.Sprintf("Object%04d", index), Tier: store.TierExact, CodeRole: "production"},
						}
						if err := emit(fact); err != nil {
							return sdk.Coverage{}, err
						}
					}
					return sdk.Coverage{}, nil
				},
			}
			lease := partitionExecutorLease{corpus: partitionExecutorCorpus{repository: plan.Repository, commit: commit, path: path, content: content}, records: 1, memberBytes: int64(len(content)), members: 1}
			executor := EvidencePartitionExecutor{Evidence: evidence, Extractors: []Extractor{extractor}, StoreAccounting: selected}
			result, err := executor.ExecutePartition(t.Context(), plan, 0, lease, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := []int{256, 84}
			if selected {
				want = []int{169, 169, 2}
			}
			if !reflect.DeepEqual(evidence.counts, want) || result.Totals.Facts != 340 || result.Totals.CanonicalBytes != result.Totals.EncodedBytes {
				t.Fatalf("grouping/result = %v, %+v", evidence.counts, result)
			}
			encoded[index] = result.Totals.EncodedBytes
			ids[index] = append([]string(nil), evidence.ids...)
			evidence.ids, evidence.counts = nil, nil
			again, err := executor.ExecutePartition(t.Context(), plan, 0, lease, run.ID)
			if err != nil || !reflect.DeepEqual(ids[index], evidence.ids) || !reflect.DeepEqual(result, again) {
				t.Fatalf("same-policy replay identity changed: %+v, %v", again, err)
			}
			executor.StoreAccounting = !selected
			before := evidence.batchCount
			if _, err := executor.ExecutePartition(t.Context(), plan, 0, partitionExecutorLease{}, run.ID); !errors.Is(err, extractionpublication.ErrStale) || evidence.batchCount != before {
				t.Fatalf("opposite-policy plan reached inventory/staging: %v", err)
			}
		})
	}
	if encoded[1]-encoded[0] != 132 {
		t.Fatalf("real production serializer grouping delta = %d, want 132", encoded[1]-encoded[0])
	}
	for _, ordinary := range ids[0] {
		for _, selected := range ids[1] {
			if ordinary == selected {
				t.Fatal("cross-policy chunk identity collision")
			}
		}
	}
}

func TestVersionedPartialChunkIdentity(t *testing.T) {
	policy := "sha256:" + strings.Repeat("a", 64)
	selected, err := candidate.ExtractionPolicyDigest(policy, true)
	if err != nil {
		t.Fatal(err)
	}
	facts := []sdk.Fact{unitFact("same.proto", "one-fact")}
	ordinary := buildFactChunkForNamespace(0, facts, policy+":0")
	accounted := buildFactChunkForNamespace(0, facts, selected+":0")
	if ordinary.ID == accounted.ID || accounted.ID != buildFactChunkForNamespace(0, facts, selected+":0").ID {
		t.Fatal("partial chunk identity does not bind policy namespace")
	}
}
