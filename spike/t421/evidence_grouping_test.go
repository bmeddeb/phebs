package t421

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

func TestStoreBoundEvidenceGroupingFraming(t *testing.T) {
	// Serialize the actual transport type independently of the closed oracle
	// arithmetic. Escaped and varying-size fact content cancels between chunks.
	for _, count := range []int{0, 1, 168, 169, 170, 256, 340, 2047, 8292} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			facts := make([]sdk.Fact, count)
			var factBytes int64
			for index := range facts {
				facts[index].Path = fmt.Sprintf("neutral/\"%d\n.go", index)
				raw, err := json.Marshal(facts[index])
				if err != nil {
					t.Fatal(err)
				}
				factBytes += int64(len(raw))
			}
			for _, size := range []int{169, 256} {
				var got int64
				for offset := 0; offset < count; offset += size {
					raw, err := json.Marshal(sdk.FactChunk{
						Schema: "t20-fact-chunk-v1", Sequence: uint64(offset / size),
						ID: "sha256:" + strings.Repeat("a", 64), Facts: facts[offset:min(offset+size, count)],
					})
					if err != nil {
						t.Fatal(err)
					}
					got += int64(len(raw))
				}
				want := factBytes + int64(count) + evidenceChunkFraming(int64(count), int64(size))
				if got != want {
					t.Fatalf("%d facts, size %d: serialized=%d derived=%d", count, size, got, want)
				}
			}
		})
	}
}

func TestStoreBoundEvidenceGroupingOracle(t *testing.T) {
	if candidate.AccountedEvidenceChunkFacts != 169 {
		t.Fatal("production grouping differs from the closed V3 oracle")
	}
	policy := "sha256:" + strings.Repeat("a", 64)
	expectedPolicy := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("phebs-extraction-evidence-chunks-169-v1\x00"+policy)))
	if actual, err := candidate.ExtractionPolicyDigest(policy, true); err != nil || actual != expectedPolicy {
		t.Fatalf("production policy identity differs from V3: %s %v", actual, err)
	}
	old, next := frozenExtractionDomains(), storeBoundExtractionDomains()
	var facts, oldChunks, chunks, byteDelta int64
	for index, domain := range next {
		if err := validateFrozenExtractionDomain(domain); err != nil {
			t.Fatalf("%s: %v", domain.Domain, err)
		}
		if domain.Reserved != old[index].Reserved {
			t.Fatal("grouping expanded a reservation")
		}
		for ordinal, partition := range domain.Partitions {
			prior := old[index].Partitions[ordinal]
			facts += partition.Expected.Facts
			oldChunks += (partition.Expected.Facts + 255) / 256
			chunks += (partition.Expected.Facts + 168) / 169
			delta := partition.Expected.CanonicalBytes - prior.Expected.CanonicalBytes
			byteDelta += delta
			if delta < 0 || partition.Expected.EncodedBytes-prior.Expected.EncodedBytes != delta {
				t.Fatal("canonical and encoded changes disagree")
			}
			partition.Expected.CanonicalBytes = prior.Expected.CanonicalBytes
			partition.Expected.EncodedBytes = prior.Expected.EncodedBytes
			if partition != prior {
				t.Fatal("grouping changed facts, physical rows, references, or source shape")
			}
		}
	}
	if facts != 61215 || oldChunks != 248 || chunks != 375 || byteDelta != 16866 {
		t.Fatalf("facts=%d chunks=%d->%d byte delta=%d", facts, oldChunks, chunks, byteDelta)
	}
	plan := accountingTestPlan(t)
	if !reflect.DeepEqual(plan.Profile.Pipeline.ExtractionDomains, next) || plan.Correction.EvidenceGroupingPolicy != storeBoundEvidenceGroupingPolicy {
		t.Fatal("V3 omitted the prospective grouping oracle")
	}
	legacy := frozenWorkEnvelope(plan.Profile)
	for index, phase := range plan.WorkEnvelope.Phases {
		if phase.StoreTransactions != legacy.Phases[index].StoreTransactions || phase.StoreRows != legacy.Phases[index].StoreRows {
			t.Fatal("grouping changed phase admission bounds")
		}
		if phase.Phase == "cold" || phase.Phase == "physical_delta_b" || phase.Phase == "return_a" {
			// Append-only component, not admission of all pipeline work or retries.
			if uint64(chunks)*64 > phase.StoreTransactions.Maximum || uint64(3*facts+3*chunks)*64 > phase.StoreRows.Maximum {
				t.Fatal("append component exceeds an unchanged full-pass ceiling")
			}
		}
	}
	if plan.WorkEnvelope.MaximumStoreRowsPerTransaction != 512 || 3*169+3 != 510 {
		t.Fatal("submitted operand ceiling changed")
	}
	for _, mode := range []string{"old_oracle", "changed_byte", "changed_policy"} {
		t.Run(mode, func(t *testing.T) {
			mutated := accountingTestPlan(t)
			switch mode {
			case "old_oracle":
				mutated.Profile.Pipeline.ExtractionDomains = frozenExtractionDomains()
			case "changed_byte":
				mutated.Profile.Pipeline.ExtractionDomains[0].Partitions[0].Expected.CanonicalBytes++
			case "changed_policy":
				mutated.Correction.EvidenceGroupingPolicy = "unversioned-169"
			}
			if err := validatePlan(mutated, &mutated.Revisions); err == nil {
				t.Fatal("mutated V3 grouping contract accepted")
			}
		})
	}
}
