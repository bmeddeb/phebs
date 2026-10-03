package typedindex

import "context"

// InputReceipt retains bounded provenance after input custody is collected.
// DeclaredProducer is operator attestation, never executed-tool evidence. The
// digests audit derivation; they grant no source, route or publication authority.
type InputReceipt struct {
	Schema           string             `json:"schema"`
	Provider         string             `json:"provider"`
	SelectionDigest  string             `json:"selection_digest"`
	ScopeDigest      string             `json:"scope_digest"`
	DeclaredProducer *Tool              `json:"declared_producer,omitempty"`
	ProvenanceDigest string             `json:"provenance_digest"`
	Members          []ContentReference `json:"members"`
}

const InputReceiptSchema = "phebs-typed-input-receipt-v1"

func validateInputReceipt(p Profile, r *InputReceipt) error {
	if p.Provider() == ProviderID {
		if r != nil {
			return Invalid
		}
		return nil
	}
	if r == nil || p.definition.Schema != InputProfileSchema || r.Schema != InputReceiptSchema || r.Provider != p.Provider() || !digest(r.SelectionDigest) || !digest(r.ScopeDigest) || len(r.Members) == 0 || len(r.Members) > MaxSCIPMembers {
		return Invalid
	}
	if r.Provider == ImportProviderID {
		if r.DeclaredProducer == nil || *r.DeclaredProducer != p.definition.Tools.Indexer || !digest(r.ProvenanceDigest) {
			return Invalid
		}
	} else if r.Provider != ModuleProviderID || r.DeclaredProducer != nil || r.ProvenanceDigest != "" {
		return Invalid
	}
	var total int
	previous := ""
	for _, member := range r.Members {
		if !token(member.Name) || member.Name <= previous || !digest(member.Digest) || member.Bytes <= 0 || member.Bytes > MaxSCIPMemberBytes || member.Bytes > MaxSCIPAggregateBytes-total {
			return Invalid
		}
		total += member.Bytes
		previous = member.Name
	}
	return nil
}

// BuildInputBundle shares all complete-bundle and routing validation. Receipt
// derivation is proved by the trusted finalizer while it holds original bytes.
func BuildInputBundle(ctx context.Context, a Admission, p PackagePlan, outcomes []UnitOutcome, members []MemberInput, receipt InputReceipt, scipGo *SCIPGoAdapterReceipt) (Bundle, error) {
	return buildBundle(ctx, a, p, outcomes, members, nil, scipGo, &receipt)
}
