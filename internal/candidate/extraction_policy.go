package candidate

import "errors"

// AccountedEvidenceChunkFacts keeps three evidence rows per fact plus the
// three fixed transaction rows within the selected 512-row write admission.
const AccountedEvidenceChunkFacts = 169

// ExtractionPolicyDigest preserves the historical candidate policy for ordinary
// 256-fact grouping and gives selected accounting a distinct durable identity.
func ExtractionPolicyDigest(candidatePolicy string, storeAccounting bool) (string, error) {
	if !validDigest(candidatePolicy) {
		return "", errors.New("invalid candidate policy for extraction grouping")
	}
	if !storeAccounting {
		return candidatePolicy, nil
	}
	return digest("phebs-extraction-evidence-chunks-169-v1\x00", []byte(candidatePolicy)), nil
}
