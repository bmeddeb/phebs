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
func ExtractionPolicyDigest(
	candidatePolicy string,
	storeAccounting,
	typedScopeReservations bool,
) (string, error) {
	if !validDigest(candidatePolicy) {
		return "", errors.New("invalid candidate policy for extraction grouping")
	}
	policy := candidatePolicy
	if storeAccounting {
		policy = digest("phebs-extraction-evidence-chunks-169-v1\x00", []byte(policy))
	}
	if typedScopeReservations {
		policy = digest("phebs-extraction-typed-scope-reservations-v1\x00", []byte(policy))
	}
	return policy, nil
}
