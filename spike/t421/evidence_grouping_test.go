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
	if actual, err := candidate.ExtractionPolicyDigest(policy, true, false); err != nil || actual != expectedPolicy {
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
	if len(plan.WorkEnvelope.Phases) != len(legacy.Phases) {
		t.Fatal("grouping changed the phase inventory")
	}
	for index, phase := range plan.WorkEnvelope.Phases {
		prior := legacy.Phases[index]
		if phase.Phase != prior.Phase {
			t.Fatal("grouping changed the phase order")
		}
		// Normalize only the separately approved selected-cleanup reserve,
		// using independent arithmetic rather than the production helper.
		// Grouping leaves every minimum and all other phase ceilings exact.
		switch phase.Phase {
		case "pressure_80", "pressure_75", "lifecycle_collection":
			prior.StoreTransactions.Maximum += 4096 * (2 + 65)
			prior.StoreRows.Maximum += 4096 * (2 + 512)
		}
		if phase.StoreTransactions != prior.StoreTransactions || phase.StoreRows != prior.StoreRows {
			t.Fatalf("%s: grouping changed phase admission bounds: transactions=%+v want=%+v rows=%+v want=%+v",
				phase.Phase, phase.StoreTransactions, prior.StoreTransactions, phase.StoreRows, prior.StoreRows)
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
	for _, mode := range []string{"old_oracle", "changed_byte", "changed_policy", "expanded_transaction_ceiling", "removed_cleanup_row_reserve"} {
		t.Run(mode, func(t *testing.T) {
			mutated := accountingTestPlan(t)
			switch mode {
			case "old_oracle":
				mutated.Profile.Pipeline.ExtractionDomains = frozenExtractionDomains()
			case "changed_byte":
				mutated.Profile.Pipeline.ExtractionDomains[0].Partitions[0].Expected.CanonicalBytes++
			case "changed_policy":
				mutated.Correction.EvidenceGroupingPolicy = "unversioned-169"
			case "expanded_transaction_ceiling":
				for index := range mutated.WorkEnvelope.Phases {
					if mutated.WorkEnvelope.Phases[index].Phase == "cold" {
						mutated.WorkEnvelope.Phases[index].StoreTransactions.Maximum++
					}
				}
			case "removed_cleanup_row_reserve":
				for index := range mutated.WorkEnvelope.Phases {
					if mutated.WorkEnvelope.Phases[index].Phase == "pressure_80" {
						mutated.WorkEnvelope.Phases[index].StoreRows.Maximum -= 4096 * (2 + 512)
					}
				}
			}
			if err := validatePlan(mutated, &mutated.Revisions); err == nil {
				t.Fatal("mutated V3 grouping contract accepted")
			}
		})
	}
}
