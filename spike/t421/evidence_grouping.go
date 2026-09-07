package t421

import "strconv"

// V3 alone uses the versioned store-bound extraction policy. The facts and
// production reservations are unchanged; only chunk framing and IDs differ.
const storeBoundEvidenceGroupingPolicy = "extraction-policy=sha256(utf8(phebs-extraction-evidence-chunks-169-v1)+NUL+candidate-policy);durable-reuse-binding;169-facts-per-chunk;3*169+3=510<=512-submitted-operands;ordinary-256-and-retained-V1-V2-exact;new-policy-never-reuses-old-run;bytes=frozen-256-bytes+framing(169)-framing(256);framing=sum(131+decimal-digits(zero-based-sequence));same-ordered-facts-and-fixed-length-sha256-identities;no-admission-bound-change"

func storeBoundExtractionDomains() []ExtractionDomainProfile {
	domains := frozenExtractionDomains()
	for index := range domains {
		domain := &domains[index]
		domain.Expected = ResultTotals{}
		shape := newIdentityBuilder("t421-extraction-partition-shape-v1/" + domain.Domain)
		for ordinal := range domain.Partitions {
			partition := &domain.Partitions[ordinal]
			delta := evidenceChunkFraming(partition.Expected.Facts, 169) - evidenceChunkFraming(partition.Expected.Facts, 256)
			partition.Expected.CanonicalBytes += delta
			partition.Expected.EncodedBytes += delta
			addResultTotals(&domain.Expected, partition.Expected)
			_ = shape.add(*partition)
		}
		domain.PartitionShape = shape.finish()
	}
	return domains
}

// A nonempty t20-fact-chunk-v1 JSON wrapper contributes 131 bytes plus
// sequence digits after separating one comma per fact. Fact JSON cancels
// between groupings; each chunk's SHA256 ID has the same encoded length.
// Inputs come only from the closed frozen partition table (at most 8,292 facts).
func evidenceChunkFraming(facts, chunkSize int64) int64 {
	var framing int64
	for sequence := int64(0); sequence*chunkSize < facts; sequence++ {
		framing += 131 + int64(len(strconv.FormatInt(sequence, 10)))
	}
	return framing
}
