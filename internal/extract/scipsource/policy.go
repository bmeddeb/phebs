// Package scipsource owns the source-path boundary shared by candidate
// planning and SCIP-backed extractors.
package scipsource

import "strings"

// Policy identifies one bounded family of source paths that a SCIP consumer
// may resolve against the candidate publication.
type Policy uint8

const (
	// Go admits Go source documents only.
	Go Policy = iota + 1
	// ProtoField admits Go consumers/generated sources and protobuf
	// declarations used by the protobuf-field extractor.
	ProtoField
)

// Eligible reports whether filePath is a source document admitted by policy.
// Root index files and ancillary module/attribution inputs are deliberately
// outside this predicate and are enumerated separately by their owners.
func Eligible(policy Policy, filePath string) bool {
	switch policy {
	case Go:
		return strings.HasSuffix(filePath, ".go")
	case ProtoField:
		return strings.HasSuffix(filePath, ".go") ||
			strings.HasSuffix(filePath, ".proto")
	default:
		return false
	}
}
