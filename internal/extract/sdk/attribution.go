package sdk

import "context"

const (
	AttributionStateResolved    = "resolved"
	AttributionStateAmbiguous   = "ambiguous"
	AttributionStateUnavailable = "unavailable"
)

// AttributionCorpus is the optional, read-only capability for immutable
// monorepo layout, generated-from, and consumer-unit metadata. The trusted
// worker owns parsing, provenance, bounds, and digest verification; extractors
// receive only deterministic lookups over already-validated derived names.
type AttributionCorpus interface {
	AttributionSource(ctx context.Context) (AttributionSource, error)
}

// AttributionSource is a protocol-neutral snapshot projection. It performs no
// I/O and returns defensive copies. Digest binds the complete selected
// attribution input so later result cursors can bind the same snapshot without
// making that digest part of source assertion identity.
type AttributionSource interface {
	Available() bool
	Digest() string
	Provenance() []SnapshotProvenance
	ClassifyPath(path string) PathClassification
	ConsumerUnits(path string, line int) ConsumerUnitAttribution
	GeneratedFrom(protocol, generatedPath, generatorRelativePath string) GeneratedFromAttribution
}

// SnapshotProvenance identifies one immutable attribution input. The initial
// adapter uses repository_revision; external_digest is reserved for a future
// adapter that is handed independently verified bytes and their expected
// digest, never a network or catalog client.
type SnapshotProvenance struct {
	Kind       string
	Repository string
	Commit     string
	Path       string
	Digest     string
}

// PathClassification reports optional layout metadata. State is resolved for
// one classified root and unavailable for an unmatched path. Overlapping roots
// are rejected when the snapshot is loaded, so ambiguity is never guessed
// here.
type PathClassification struct {
	State    string
	Kind     string
	Root     string
	Protocol string
	Reason   string
}

// ConsumerUnitCandidate keeps each attribution hop typed and separate.
// Multiple values are preserved instead of selecting a winner.
type ConsumerUnitCandidate struct {
	ID              string
	BuildTargets    []string
	Deployables     []string
	LogicalServices []string
	Owners          []string
}

// ConsumerUnitAttribution explicitly distinguishes zero, one, and ambiguous
// mappings for one source location.
type ConsumerUnitAttribution struct {
	State      string
	Reason     string
	Candidates []ConsumerUnitCandidate
}

// GeneratedFromCandidate is one admitted generated-artifact-to-declaration
// relation. DeclarationLineage is repository/path identity; protocol remains
// explicit so equal wire spellings cannot cross protocol packs.
type GeneratedFromCandidate struct {
	Protocol              string
	GeneratedPath         string
	GeneratorRelativePath string
	DeclarationPath       string
	DeclarationLineage    string
}

// GeneratedFromAttribution preserves missing and conflicting relations.
// Layout classification by itself never creates a candidate.
type GeneratedFromAttribution struct {
	State      string
	Reason     string
	Candidates []GeneratedFromCandidate
}
