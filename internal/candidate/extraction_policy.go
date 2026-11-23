package candidate

import "errors"

// AccountedEvidenceChunkFacts keeps three evidence rows per fact plus the
// three fixed transaction rows within the selected 512-row write admission.
const AccountedEvidenceChunkFacts = 169

// ExtractionPolicyDigest composes the versioned policy-identity layers the
// selected execution modes require. With every mode off it returns the
// historical candidate policy unchanged, so inherited plans, bindings and
// generations reconstruct byte-exactly. TypedScopeReservations layers the
// typed-scope weight correction onto the same durable identity, so plans
// allocated under the historical weight-one sliver can never silently share a
// generation, binding or execution gate with the corrected allocation.
// ImplementationRevisions is the digest of the admitted extractors' positive
// implementation revisions (PHEBS-37), or empty when none is declared. It is
// layered last, so a behaviour fix that keeps a reader's signed version still
// yields a new generation, and the next start re-extracts every repository
// once instead of reusing results the previous implementation produced at an
// unchanged head.
func ExtractionPolicyDigest(
	candidatePolicy string,
	storeAccounting,
	typedScopeReservations bool,
	implementationRevisions string,
) (string, error) {
	if !validDigest(candidatePolicy) {
		return "", errors.New("invalid candidate policy for extraction grouping")
	}
	if implementationRevisions != "" && !validDigest(implementationRevisions) {
		return "", errors.New("invalid implementation revisions for extraction grouping")
	}
	policy := candidatePolicy
	if storeAccounting {
		policy = digest("phebs-extraction-evidence-chunks-169-v1\x00", []byte(policy))
	}
	if typedScopeReservations {
		policy = digest("phebs-extraction-typed-scope-reservations-v1\x00", []byte(policy))
	}
	if implementationRevisions != "" {
		policy = digest(
			"phebs-extraction-implementation-revisions-v1\x00",
			[]byte(policy+"\x00"+implementationRevisions),
		)
	}
	return policy, nil
}
