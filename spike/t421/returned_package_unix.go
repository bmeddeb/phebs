//go:build darwin || linux

package t421

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"
)

func createExecutionReturnedPackage(
	ctx context.Context,
	plan Plan,
	receipt Receipt,
	binding ExecutionFreezeBinding,
	seal *executionSignerSealCustody,
) ([]byte, ReturnedPackageBinding, error) {
	key := seal.key
	key.mu.Lock()
	defer key.mu.Unlock()
	key.claim.mu.Lock()
	defer key.claim.mu.Unlock()
	key.signer.mu.Lock()
	defer key.signer.mu.Unlock()
	owner := key.namespace.owner
	owner.mu.Lock()
	defer owner.mu.Unlock()
	refuse := func(err error) ([]byte, ReturnedPackageBinding, error) { return nil, ReturnedPackageBinding{}, err }
	if err := checkExecutionSignerSealLocked(ctx, seal); err != nil ||
		!reflect.DeepEqual(binding.freeze, seal.freeze) || binding.freezeSHA256 != SHA256(seal.candidateRaw) ||
		binding.expectedSignerFingerprint != key.fingerprint || binding.expectedSignerNamespaceSHA256 != key.namespace.digest ||
		binding.admissionEventOrdinal != 1 {
		return refuse(ErrExecutionEpochOne)
	}
	if err := verifyExecutionSignerCandidateLocked(ctx, key, seal.candidate, seal.signature, seal.candidateRaw); err != nil {
		return refuse(err)
	}
	planRaw, err := MarshalCanonical(plan)
	if err != nil {
		return refuse(err)
	}
	if decoded, decodeErr := DecodePlan(planRaw); decodeErr != nil || !reflect.DeepEqual(decoded, plan) {
		return refuse(ErrExecutionEpochOne)
	}
	sourceRaw, err := executionSourceVerificationBytes(plan, binding, receipt.RevisionResults)
	if err != nil || receipt.Authority.SourceVerificationSHA256 != SHA256(sourceRaw) {
		return refuse(errors.New("T42.2 receipt does not bind its source verification"))
	}
	seal.sourceSignature, err = signExecutionReturnedPayloadLocked(ctx, key, sourceRaw,
		plan.SealPolicy.SourceVerificationSignatureNamespace, key.claim.names.sourceSignatureStage, key.claim.names.sourceSignature)
	if err != nil {
		return refuse(err)
	}
	sourceSignatureRaw, err := readExecutionSignerFile(seal.sourceSignature, maxExecutionSignerSignatureBytes)
	if err != nil {
		return refuse(err)
	}
	receiptRaw, err := MarshalCanonical(receipt)
	if err != nil || len(receiptRaw) == 0 || len(receiptRaw) > MaxReceiptBytes || rejectSourceBearingReceipt(receiptRaw) != nil {
		return refuse(ErrExecutionEpochOne)
	}
	manifestRaw, err := MarshalCanonical(executionReturnedManifestV1{
		Schema: plan.SealPolicy.ManifestSchema, PlanSHA256: binding.planSHA256,
		ExecutionFreezeSHA256: binding.freezeSHA256, ResultsSHA256: SHA256(receiptRaw),
		SignerFingerprint: key.fingerprint, SourceFree: true,
	})
	if err != nil || len(manifestRaw) > maxExecutionReturnedControlBytes {
		return refuse(ErrExecutionEpochOne)
	}
	freezeSignatureRaw, err := readExecutionSignerFile(seal.signature, maxExecutionSignerSignatureBytes)
	if err != nil {
		return refuse(err)
	}
	files := map[string][]byte{
		"allowed_signers": executionSignerAllowlist(key.canonicalPublic), "execution-freeze.json": seal.candidateRaw,
		"execution-freeze.json.sig": freezeSignatureRaw, "manifest.json": manifestRaw,
		"plan.json": planRaw, "results.json": receiptRaw, "signer.pub": key.canonicalPublic,
		"source-verification.json": sourceRaw, "source-verification.json.sig": sourceSignatureRaw,
	}
	checksums, err := executionReturnedChecksums(files, plan.SealPolicy.ChecksumCoverage)
	if err != nil || len(checksums) > maxExecutionReturnedControlBytes {
		return refuse(ErrExecutionEpochOne)
	}
	seal.returnedSignature, err = signExecutionReturnedPayloadLocked(ctx, key, checksums,
		plan.SealPolicy.ReturnedSignatureNamespace, key.claim.names.returnedSignatureStage, key.claim.names.returnedSignature)
	if err != nil {
		return refuse(err)
	}
	returnedSignatureRaw, err := readExecutionSignerFile(seal.returnedSignature, maxExecutionSignerSignatureBytes)
	if err != nil {
		return refuse(err)
	}
	files["SHA256SUMS"], files["SHA256SUMS.sig"] = checksums, returnedSignatureRaw
	packageRaw, err := marshalExecutionReturnedPackage(files, plan)
	if err != nil {
		return refuse(err)
	}
	verified, err := inspectExecutionReturnedPackage(packageRaw, plan)
	if err != nil || !reflect.DeepEqual(files, verified) ||
		!validExecutionReturnedChecksum(verified["SHA256SUMS"], verified, plan.SealPolicy.ChecksumCoverage) {
		return refuse(ErrExecutionEpochOne)
	}
	if err := validateExecutionSourceVerification(verified["source-verification.json"], plan, receipt, binding); err != nil ||
		!bytes.Equal(verified["source-verification.json.sig"], sourceSignatureRaw) ||
		verifyExecutionSignerPayloadLocked(ctx, key, seal.sourceSignature, verified["source-verification.json"], plan.SealPolicy.SourceVerificationSignatureNamespace) != nil ||
		!bytes.Equal(verified["SHA256SUMS.sig"], returnedSignatureRaw) ||
		verifyExecutionSignerPayloadLocked(ctx, key, seal.returnedSignature, verified["SHA256SUMS"], plan.SealPolicy.ReturnedSignatureNamespace) != nil {
		return refuse(ErrExecutionEpochOne)
	}
	packageBinding, err := executionReturnedBinding(plan, receipt, binding, packageRaw, checksums, sourceRaw, key.fingerprint)
	if err != nil {
		return refuse(err)
	}
	decodedReceipt, err := DecodeReceipt(verified["results.json"], plan, binding, packageBinding)
	if err != nil || !reflect.DeepEqual(decodedReceipt, receipt) {
		return refuse(ErrExecutionEpochOne)
	}
	return packageRaw, packageBinding, nil
}

func signExecutionReturnedPayloadLocked(
	ctx context.Context,
	key *executionSignerKeyCustody,
	raw []byte,
	namespace, stageName, finalName string,
) (*executionSignerHeldFile, error) {
	stdout, _, err := runExecutionSignerPayloadCommandLocked(ctx, key,
		[]*executionSignerHeldFile{key.privateKey, key.canonicalFile, key.allowlist}, raw,
		maxExecutionSignerSignatureBytes, "-Y", "sign", "-f", key.privateKey.path, "-n", namespace)
	if err != nil || len(stdout) == 0 || len(stdout) > maxExecutionSignerSignatureBytes {
		return nil, ErrExecutionEpochOne
	}
	stage, err := createExecutionSignerHeldFileLocked(ctx, key.namespace.owner, stageName, stdout, maxExecutionSignerSignatureBytes)
	if err != nil {
		return stage, err
	}
	if err := verifyExecutionSignerPayloadLocked(ctx, key, stage, raw, namespace); err != nil {
		return stage, err
	}
	if err := promoteExecutionSignerFileLocked(ctx, key.namespace.owner, stage, finalName); err != nil {
		return stage, err
	}
	return stage, nil
}

type executionSourceVerificationV1 struct {
	Schema                string                                `json:"schema"`
	PlanSHA256            string                                `json:"plan_sha256"`
	ExecutionFreezeSHA256 string                                `json:"execution_freeze_sha256"`
	RevisionResultsSHA256 string                                `json:"revision_results_sha256"`
	ExactInventorySHA256  string                                `json:"exact_inventory_sha256"`
	Revisions             []executionSourceVerificationRevision `json:"revisions"`
	SourceFree            bool                                  `json:"source_free"`
}

type executionSourceVerificationRevision struct {
	Name                      string               `json:"name"`
	BaseCommit                string               `json:"base_commit"`
	BaseTree                  string               `json:"base_tree"`
	TreeInventory             SetIdentity          `json:"tree_inventory"`
	TreeOID                   string               `json:"tree_oid"`
	ObservationInputInventory SetIdentity          `json:"observation_input_inventory"`
	CandidateInventories      []CandidateInventory `json:"candidate_inventories"`
	TypedInputKind            string               `json:"typed_input_kind"`
	TypedInputPath            string               `json:"typed_input_path"`
	TypedInputMode            string               `json:"typed_input_mode"`
	TypedInputBytes           uint64               `json:"typed_input_bytes"`
	TypedInputSHA256          string               `json:"typed_input_sha256"`
	TypedInputBlobOID         string               `json:"typed_input_blob_oid"`
	RawCommitSHA256           string               `json:"raw_commit_sha256"`
	CommitOID                 string               `json:"commit_oid"`
}

type executionReturnedManifestV1 struct {
	Schema                string `json:"schema"`
	PlanSHA256            string `json:"plan_sha256"`
	ExecutionFreezeSHA256 string `json:"execution_freeze_sha256"`
	ResultsSHA256         string `json:"results_sha256"`
	SignerFingerprint     string `json:"signer_fingerprint"`
	SourceFree            bool   `json:"source_free"`
}

// buildExecutionReturnedPackage spends the sealed freeze's sole package
// attempt. It returns bytes only after the complete archive and Receipt have
// been independently decoded and authenticated.
func buildExecutionReturnedPackage(
	ctx context.Context,
	plan Plan,
	receipt Receipt,
	binding ExecutionFreezeBinding,
	seal *executionSignerSealCustody,
) ([]byte, ReturnedPackageBinding, error) {
	if seal == nil {
		return nil, ReturnedPackageBinding{}, ErrExecutionEpochOne
	}
	seal.mu.Lock()
	defer seal.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || seal.closed || seal.packageUsed || seal.key == nil || !processAccountingPlanSemantics(plan.Schema) {
		return nil, ReturnedPackageBinding{}, ErrExecutionEpochOne
	}
	seal.packageUsed = true
	return createExecutionReturnedPackage(ctx, plan, receipt, binding, seal)
}

func executionSourceVerificationBytes(plan Plan, binding ExecutionFreezeBinding, revisions []RevisionResult) ([]byte, error) {
	return executionSourceVerificationBytesForFreeze(plan, binding.planSHA256, binding.freezeSHA256, revisions)
}

// This serializer consumes digests, not an operational admission capability.
// Both the admitted signer and the outer authenticated-byte verifier use it.
func executionSourceVerificationBytesForFreeze(plan Plan, expectedPlanSHA256, freezeSHA256 string, revisions []RevisionResult) ([]byte, error) {
	if !processAccountingPlanSemantics(plan.Schema) || len(revisions) != len(plan.Revisions.Physical) {
		return nil, ErrExecutionEpochOne
	}
	rows := make([]executionSourceVerificationRevision, len(revisions))
	for index, result := range revisions {
		physical, ok := namedPhysicalRevision(plan.Revisions.Physical, result.Name)
		if !ok || physical.Name != result.Name {
			return nil, ErrExecutionEpochOne
		}
		manifest := result.AuthoredManifest
		rows[index] = executionSourceVerificationRevision{
			Name: result.Name, BaseCommit: physical.BaseCommit, BaseTree: physical.BaseTree,
			TreeInventory: manifest.TreeInventory, TreeOID: result.PhysicalTree,
			ObservationInputInventory: physical.ExpectedObservationInputInventory,
			CandidateInventories:      slices.Clone(physical.ExpectedCandidateInventories),
			TypedInputKind:            manifest.TypedInputKind, TypedInputPath: manifest.TypedInputPath,
			TypedInputMode: manifest.TypedInputMode, TypedInputBytes: manifest.TypedInputBytes,
			TypedInputSHA256: manifest.TypedInputSHA256, TypedInputBlobOID: manifest.TypedInputBlobOID,
			RawCommitSHA256: manifest.CommitBytesSHA256, CommitOID: result.PhysicalCommit,
		}
	}
	planSHA256, err := receiptSHA256(plan)
	if err != nil || expectedPlanSHA256 != planSHA256 || !validDigest(freezeSHA256) {
		return nil, ErrExecutionEpochOne
	}
	revisionSHA256, err := receiptSHA256(revisions)
	if err != nil {
		return nil, err
	}
	inventorySHA256, err := receiptSHA256(plan.SealPolicy.ExactInventory)
	if err != nil {
		return nil, err
	}
	raw, err := MarshalCanonical(executionSourceVerificationV1{
		Schema: plan.SealPolicy.SourceVerificationSchema, PlanSHA256: planSHA256,
		ExecutionFreezeSHA256: freezeSHA256, RevisionResultsSHA256: revisionSHA256,
		ExactInventorySHA256: inventorySHA256, Revisions: rows, SourceFree: true,
	})
	if err != nil || len(raw) == 0 || len(raw) > maxExecutionSourceVerificationBytes {
		return nil, ErrExecutionEpochOne
	}
	return raw, nil
}

func validateExecutionSourceVerification(raw []byte, plan Plan, receipt Receipt, binding ExecutionFreezeBinding) error {
	want, err := executionSourceVerificationBytes(plan, binding, receipt.RevisionResults)
	if err != nil || !bytes.Equal(raw, want) || receipt.Authority.SourceVerificationSHA256 != SHA256(raw) {
		return errors.New("T42.2 source verification differs from exact receipt authority")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value executionSourceVerificationV1
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("T42.2 source verification is not one canonical value")
	}
	return nil
}

func executionReturnedChecksums(files map[string][]byte, coverage []string) ([]byte, error) {
	var result strings.Builder
	for _, name := range coverage {
		raw, ok := files[name]
		if !ok || len(raw) == 0 {
			return nil, ErrExecutionEpochOne
		}
		digest := strings.TrimPrefix(SHA256(raw), "sha256:")
		fmt.Fprintf(&result, "%s  %s\n", digest, name)
	}
	return []byte(result.String()), nil
}

func marshalExecutionReturnedPackage(files map[string][]byte, plan Plan) ([]byte, error) {
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	gzipWriter.ModTime = time.Time{}
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	var expanded uint64
	for _, name := range plan.SealPolicy.ExactInventory {
		raw, ok := files[name]
		if !ok || len(raw) == 0 {
			return nil, ErrExecutionEpochOne
		}
		expanded += uint64(len(raw))
		if expanded > plan.SealPolicy.MaximumExpandedBytes {
			return nil, errors.New("T42.2 returned package exceeds its expanded-byte bound")
		}
		header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(raw)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := tarWriter.Write(raw); err != nil {
			return nil, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	if compressed.Len() == 0 || uint64(compressed.Len()) > plan.SealPolicy.MaximumPackageBytes {
		return nil, errors.New("T42.2 returned package exceeds its transfer bound")
	}
	return slices.Clone(compressed.Bytes()), nil
}

func executionReturnedBinding(plan Plan, receipt Receipt, binding ExecutionFreezeBinding, packageRaw, checksums, source []byte, signer string) (ReturnedPackageBinding, error) {
	receiptDigest, err := receiptSHA256(receipt)
	if err != nil {
		return ReturnedPackageBinding{}, err
	}
	revisionSHA256, err := receiptSHA256(receipt.RevisionResults)
	if err != nil {
		return ReturnedPackageBinding{}, err
	}
	exactInventorySHA256, err := receiptSHA256(plan.SealPolicy.ExactInventory)
	if err != nil {
		return ReturnedPackageBinding{}, err
	}
	return ReturnedPackageBinding{
		signerFingerprint: signer, receiptSHA256: receiptDigest, packageSHA256: SHA256(packageRaw),
		inventorySHA256: SHA256(checksums), exactInventory: slices.Clone(plan.SealPolicy.ExactInventory),
		returnedSignatureVerified: true, sourceSignatureVerified: true,
		returnedSignatureNamespace: plan.SealPolicy.ReturnedSignatureNamespace,
		sourceSignatureNamespace:   plan.SealPolicy.SourceVerificationSignatureNamespace,
		sourceVerificationSHA256:   SHA256(source), sourceVerificationSchema: plan.SealPolicy.SourceVerificationSchema,
		sourcePlanSHA256: binding.planSHA256, sourceFreezeSHA256: binding.freezeSHA256,
		sourceExactInventorySHA256: exactInventorySHA256, revisionResultsSHA256: revisionSHA256, sourceVerified: true,
	}, nil
}

func validExecutionReturnedChecksum(raw []byte, files map[string][]byte, coverage []string) bool {
	want, err := executionReturnedChecksums(files, coverage)
	if err != nil || !bytes.Equal(raw, want) {
		return false
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if len(line) < 67 || line[64:66] != "  " {
			return false
		}
		decoded, err := hex.DecodeString(line[:64])
		if err != nil || hex.EncodeToString(decoded) != line[:64] {
			return false
		}
	}
	return true
}
