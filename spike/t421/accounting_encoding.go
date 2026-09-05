package t421

import (
	"encoding/json"
	"errors"
)

// The V3 wire contract omits superseded native-history fields altogether.
// Historical V1/V2 values take the original field order and representation.
func (value WorkEnvelope) MarshalJSON() ([]byte, error) {
	type plain WorkEnvelope
	if value.Schema != WorkEnvelopeV3Schema {
		return json.Marshal(plain(value))
	}
	if value.MaximumChildProcessesPerPhase != 0 || value.ChildProcessRoles != nil {
		return nil, errors.New("V3 work envelope contains legacy child bounds")
	}
	return json.Marshal(struct {
		plain
		MaximumChildProcessesPerPhase *uint64  `json:"maximum_child_processes_per_phase,omitempty"`
		ChildProcessRoles             []string `json:"child_process_roles,omitempty"`
	}{plain: plain(value)})
}

func (value PhaseWorkBounds) MarshalJSON() ([]byte, error) {
	type plain PhaseWorkBounds
	if value.ControlledDispatchRoles == nil {
		return json.Marshal(plain(value))
	}
	if value.ChildProcessRoles != nil {
		return nil, errors.New("V3 phase contains legacy child bounds")
	}
	return json.Marshal(struct {
		plain
		ChildProcessRoles []RoleBound `json:"child_process_roles,omitempty"`
	}{plain: plain(value)})
}

func (value TeardownRule) MarshalJSON() ([]byte, error) {
	type plain TeardownRule
	if value.Scope == "" {
		return json.Marshal(plain(value))
	}
	if value.StopDescendants || value.RequireZeroChildren {
		return nil, errors.New("scoped teardown contains a global descendant claim")
	}
	return json.Marshal(struct {
		plain
		StopDescendants     *bool `json:"stop_descendants,omitempty"`
		RequireZeroChildren *bool `json:"require_zero_children,omitempty"`
	}{plain: plain(value)})
}
