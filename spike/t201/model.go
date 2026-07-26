// Package t201 contains the generated, neutral validation corpus for T20.1.
//
// It is deliberately a spike, not a production extractor. The package freezes
// candidate-independent inputs and expected relationships so later Epic 20
// tickets can be measured against the same population.
package t201

const (
	SmallProfileName = "small"
	ScaleProfileName = "scale-10000"

	ScaleLogicalUnits        = 10_000
	ScaleCallsPerFile        = 100
	ScaleCallFiles           = ScaleLogicalUnits / ScaleCallsPerFile
	ScaleCalibrationCalls    = 10
	ScaleTotalCalls          = ScaleLogicalUnits + ScaleCalibrationCalls
	ScaleCalibrationMappings = 5
	ScaleTotalMappings       = ScaleLogicalUnits + ScaleCalibrationMappings
	HighFanoutReferences     = 4_097
	RepeatedPlacements       = 101
)

// Resolution is an oracle classification, not an extractor conclusion.
type Resolution string

const (
	ResolutionKnown      Resolution = "known"
	ResolutionUnresolved Resolution = "unresolved"
)

// CallExpectation is one source occurrence in the candidate-independent
// oracle. EndByte and EndLine are exclusive.
type CallExpectation struct {
	ID                 string     `json:"id"`
	Protocol           string     `json:"protocol"`
	CanonicalOperation string     `json:"canonical_operation"`
	Path               string     `json:"path"`
	StartByte          int        `json:"start_byte"`
	EndByte            int        `json:"end_byte"`
	StartLine          int        `json:"start_line"`
	EndLine            int        `json:"end_line"`
	CodeRole           string     `json:"code_role"`
	Resolution         Resolution `json:"resolution"`
	Reason             string     `json:"reason"`
	GeneratedSymbol    string     `json:"generated_symbol,omitempty"`
}

// GeneratedFromExpectation binds one checked-in generated artifact to one
// declaration lineage. The snapshot, not path adjacency, supplies the claim.
type GeneratedFromExpectation struct {
	GeneratedPath      string `json:"generated_path"`
	DeclarationPath    string `json:"declaration_path"`
	DeclarationLineage string `json:"declaration_lineage"`
	Protocol           string `json:"protocol"`
	State              string `json:"state"`
	Reason             string `json:"reason,omitempty"`
}

// UnitMappingExpectation keeps build target, deployable, logical service, and
// owner as separate fields with an explicit source.
type UnitMappingExpectation struct {
	CallID         string   `json:"call_id"`
	SourcePath     string   `json:"source_path"`
	StartLine      int      `json:"start_line"`
	BuildTargets   []string `json:"build_targets,omitempty"`
	Deployables    []string `json:"deployables,omitempty"`
	LogicalService []string `json:"logical_services,omitempty"`
	Owners         []string `json:"owners,omitempty"`
	State          string   `json:"state"`
	Source         string   `json:"source"`
}

// Oracle is checked independently of any extractor output.
type Oracle struct {
	Profile       string                       `json:"profile"`
	Calls         []CallExpectation            `json:"calls"`
	Fanouts       []OperationFanoutExpectation `json:"operation_fanouts,omitempty"`
	GeneratedFrom []GeneratedFromExpectation   `json:"generated_from"`
	UnitMappings  []UnitMappingExpectation     `json:"unit_mappings"`
}

// OperationFanoutExpectation is the candidate-independent assertion frame:
// every listed call supports review of one endpoint identity. It intentionally
// exceeds today's per-assertion storage limit in the scale profile.
type OperationFanoutExpectation struct {
	Protocol           string   `json:"protocol"`
	CanonicalOperation string   `json:"canonical_operation"`
	SupportingCallIDs  []string `json:"supporting_call_ids"`
}
