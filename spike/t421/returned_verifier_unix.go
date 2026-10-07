//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
)

// executionVerifiedReturnedPackage is only an output capability. It grants no
// checkout, profile or operational admission and cannot resume an execution.
type executionVerifiedReturnedPackage struct {
	raw     []byte
	receipt Receipt
	digest  string
	used    bool
}

// verifyExecutionReturnedPackage authenticates child output against the exact
// freeze already handed to the operator by the held inner image. It never
// reconstructs the inner's private admission bindings from returned JSON.
func verifyExecutionReturnedPackage(ctx context.Context, raw []byte, selection executionSelectionV1, freezeSHA256 string) (*executionVerifiedReturnedPackage, error) {
	if ctx == nil || ctx.Err() != nil || !validExecutionSelection(selection) || !validExecutionHexSHA256(freezeSHA256) {
		return nil, ErrExecutionLauncher
	}
	files, err := inspectExecutionReturnedPackage(raw, Plan{SealPolicy: frozenSealPolicy()})
	if err != nil || SHA256(files["execution-freeze.json"]) != "sha256:"+freezeSHA256 || ctx.Err() != nil {
		return nil, ErrExecutionLauncher
	}
	freeze, err := decodeCanonicalExecutionFreeze(files["execution-freeze.json"], MaxExecutionFreezeBytes)
	if err != nil || freeze.Commits.IntegratedMainCommit != selection.IntegratedMainCommit ||
		freeze.Commits.T422SourceCommit != selection.SourceCommit || !freeze.Commits.CleanTree {
		return nil, ErrExecutionLauncher
	}
	public, fingerprint, _, err := deriveExecutionSignerPublic(files["signer.pub"])
	if err != nil || fingerprint != freeze.SignerFingerprint || !bytes.Equal(files["allowed_signers"], executionSignerAllowlist(public)) {
		return nil, ErrExecutionLauncher
	}
	policy := frozenSealPolicy()
	for _, signature := range []struct{ name, message, namespace string }{
		{"execution-freeze.json.sig", "execution-freeze.json", policy.FreezeSignatureNamespace},
		{"source-verification.json.sig", "source-verification.json", policy.SourceVerificationSignatureNamespace},
		{"SHA256SUMS.sig", "SHA256SUMS", policy.ReturnedSignatureNamespace},
	} {
		if ctx.Err() != nil || verifyExecutionReturnedSignature(files[signature.name], files[signature.message], public, signature.namespace) != nil {
			return nil, ErrExecutionLauncher
		}
	}
	if !validExecutionReturnedChecksum(files["SHA256SUMS"], files, policy.ChecksumCoverage) {
		return nil, ErrExecutionLauncher
	}
	// DecodePlan independently regenerates and compares the complete frozen
	// plan; cancellation is checked around that existing contextless operation.
	plan, err := DecodePlan(files["plan.json"])
	if err != nil || ctx.Err() != nil || !processAccountingPlanSemantics(plan.Schema) || plan.SourceCommit != selection.PlanSourceCommit ||
		!reflect.DeepEqual(plan.SealPolicy, policy) || freeze.PlanSHA256 != SHA256(files["plan.json"]) ||
		freeze.Schema != plan.ToolPolicy.ExecutionFreezeSchema {
		return nil, ErrExecutionLauncher
	}
	var receipt Receipt
	if decodeExecutionReturnedCanonical(files["results.json"], &receipt) != nil ||
		rejectSourceBearingReceipt(files["results.json"]) != nil || !receipt.SourceFree ||
		receipt.Schema != plan.ReceiptContract.Schema || receipt.ExecutionFreeze.SHA256 != "sha256:"+freezeSHA256 ||
		receipt.ExecutionFreeze.Commits != freeze.Commits || receipt.ExecutionFreeze.SignerFingerprint != fingerprint ||
		receipt.Authority.PlanSHA256 != freeze.PlanSHA256 || receipt.Authority.SourceCommit != selection.PlanSourceCommit {
		return nil, ErrExecutionLauncher
	}
	var manifest executionReturnedManifestV1
	if decodeExecutionReturnedCanonical(files["manifest.json"], &manifest) != nil || manifest != (executionReturnedManifestV1{
		Schema: policy.ManifestSchema, PlanSHA256: freeze.PlanSHA256, ExecutionFreezeSHA256: "sha256:" + freezeSHA256,
		ResultsSHA256: SHA256(files["results.json"]), SignerFingerprint: fingerprint, SourceFree: true,
	}) {
		return nil, ErrExecutionLauncher
	}
	source, err := executionSourceVerificationBytesForFreeze(plan, freeze.PlanSHA256, "sha256:"+freezeSHA256, receipt.RevisionResults)
	if err != nil || !bytes.Equal(source, files["source-verification.json"]) ||
		receipt.Authority.SourceVerificationSHA256 != SHA256(source) ||
		validateReceiptFrozenEvidence(receipt, plan, freeze, SHA256(source)) != nil || ctx.Err() != nil {
		return nil, ErrExecutionLauncher
	}
	return &executionVerifiedReturnedPackage{raw: bytes.Clone(raw), receipt: receipt, digest: SHA256(raw)}, nil
}

func decodeExecutionReturnedCanonical(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || !executionJSONEOF(decoder) {
		return ErrExecutionLauncher
	}
	canonical, err := MarshalCanonical(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ErrExecutionLauncher
	}
	return nil
}
