package authorityvalidate

import "encoding/json"

// WithoutRunProvenance validates an exact current authority, then returns its
// canonical continuity projection. Only extraction run IDs and their derived
// provenance digest are removed. The result is not a publishable authority.
func WithoutRunProvenance(raw []byte) ([]byte, error) {
	if result, err := Canonical(raw); err != nil || !result.Usable {
		return nil, ErrInvalid
	}
	var value authority
	if json.Unmarshal(raw, &value) != nil || value.Schema != Schema {
		return nil, ErrInvalid
	}
	value.ProvenanceDigest = ""
	for index := range value.Domains {
		value.Domains[index].RunID = ""
	}
	return json.Marshal(value)
}
