//go:build linux

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
)

const maxGoReferencePlanBytes = 16 << 10

// This private recipe names an implemented reference build, never an arbitrary
// command or a ceremony plan. Its caller owns source and candidate admission.
type linuxGoReferencePlan struct {
	Role            string `json:"role"`
	Schema          string `json:"schema"`
	Source          string `json:"source"`
	Tree            string `json:"tree"`
	ConfigSHA256    string `json:"config_sha256"`
	CandidateSHA256 string `json:"candidate_sha256"`
	OriginalSDK     string `json:"original_sdk"`
	OriginalModules string `json:"original_modules"`
	OriginalSource  string `json:"original_source"`
}

func (plan linuxGoReferencePlan) valid() bool {
	if plan.Schema != "" && plan.Schema != PlanV3Schema || !validCommit(plan.Source) || !validCommit(plan.Tree) ||
		!validExecutionSHA256(plan.ConfigSHA256) || !validExecutionSHA256(plan.CandidateSHA256) {
		return false
	}
	if _, _, _, _, _, err := referenceToolRole(plan.Role); err != nil {
		return false
	}
	for _, path := range []string{plan.OriginalSDK, plan.OriginalModules, plan.OriginalSource} {
		if !executionGitAbsolutePath(path) {
			return false
		}
	}
	return true
}

// A second INIT is unambiguous only in this distinct helper recipe. Preserve
// the historical wire's closed message kinds and ordinal accounting.
func sendGoReferencePlan(ctx context.Context, wire *inputTreeSocket, plan linuxGoReferencePlan) error {
	if !plan.valid() {
		return ErrExecutionGoBuildCustody
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > maxGoReferencePlanBytes {
		return ErrExecutionGoBuildCustody
	}
	return wire.send(ctx, treeWireInit, raw, nil)
}

func receiveGoReferencePlan(ctx context.Context, wire *inputTreeSocket) (linuxGoReferencePlan, error) {
	raw, _, err := wire.receive(ctx, treeWireInit, false)
	if err != nil {
		return linuxGoReferencePlan{}, ErrExecutionGoBuildCustody
	}
	return decodeGoReferencePlan(raw)
}

func decodeGoReferencePlan(raw []byte) (linuxGoReferencePlan, error) {
	var plan linuxGoReferencePlan
	if len(raw) == 0 || len(raw) > maxGoReferencePlanBytes {
		return plan, ErrExecutionGoBuildCustody
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(new(any)) != io.EOF || !plan.valid() {
		return linuxGoReferencePlan{}, ErrExecutionGoBuildCustody
	}
	canonical, err := json.Marshal(plan)
	if err != nil || !bytes.Equal(raw, canonical) {
		return linuxGoReferencePlan{}, ErrExecutionGoBuildCustody
	}
	return plan, nil
}
