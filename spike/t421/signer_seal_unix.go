//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"
)

func createExecutionSignerSeal(
	ctx context.Context,
	seal *executionSignerSealCustody,
	plan Plan,
	prepared executionFreezeCandidatePreparation,
) (ExecutionFreeze, error) {
	key := seal.key
	key.claim.mu.Lock()
	defer key.claim.mu.Unlock()
	key.signer.mu.Lock()
	defer key.signer.mu.Unlock()
	freeze, err := validateExecutionFreezeCandidate(
		prepared.raw, plan, prepared.commits, key.fingerprint, key.namespace,
		prepared.profile, prepared.profileAdmission,
	)
	if err != nil {
		return ExecutionFreeze{}, err
	}
	owner := key.namespace.owner
	owner.mu.Lock()
	defer owner.mu.Unlock()
	refuse := func(err error) (ExecutionFreeze, error) {
		return ExecutionFreeze{}, err
	}
	if err := checkExecutionSignerKeyLocked(ctx, key); err != nil ||
		prepared.namespace.owner != key.namespace.owner || prepared.namespace.token != key.namespace.token ||
		prepared.namespace.digest != key.namespace.digest {
		return refuse(ErrExecutionEpochOne)
	}
	canonicalName := "canonical-public-" + key.publicSHA256 + ".pub"
	for _, name := range []string{canonicalName, key.claim.names.allowlist, key.claim.names.candidate,
		key.claim.names.signatureStage, key.claim.names.signature} {
		path, err := executionSignerJoinedPath(owner.path, name, maxExecutionSignerPathBytes)
		if err != nil {
			return refuse(err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return refuse(errors.New("T42.2 signer seal destination already exists or cannot be inspected"))
		}
	}
	key.canonicalFile, err = createExecutionSignerHeldFileLocked(ctx, owner, canonicalName, key.canonicalPublic, maxExecutionSignerKeyBytes)
	if err != nil {
		return refuse(err)
	}
	key.allowlist, err = createExecutionSignerHeldFileLocked(ctx, owner, key.claim.names.allowlist,
		executionSignerAllowlist(key.canonicalPublic), maxExecutionSignerKeyBytes)
	if err != nil {
		return refuse(err)
	}
	if err := checkExecutionSignerKeyLocked(ctx, key); err != nil {
		return refuse(err)
	}
	seal.candidate, err = createExecutionSignerHeldFileLocked(ctx, owner, key.claim.names.candidate,
		prepared.raw, MaxExecutionFreezeBytes)
	if err != nil {
		return refuse(err)
	}
	candidateRaw, err := readExecutionSignerFile(seal.candidate, MaxExecutionFreezeBytes)
	if err != nil || !bytes.Equal(candidateRaw, prepared.raw) {
		return refuse(errors.New("T42.2 held execution freeze differs from the canonical candidate"))
	}
	stdout, _, err := runExecutionSignerPayloadCommandLocked(ctx, key,
		[]*executionSignerHeldFile{key.privateKey, key.canonicalFile, key.allowlist, seal.candidate},
		candidateRaw, maxExecutionSignerSignatureBytes,
		"-Y", "sign", "-f", key.privateKey.path, "-n", executionFreezeSignatureNamespace,
	)
	if err != nil || len(stdout) == 0 || len(stdout) > maxExecutionSignerSignatureBytes {
		return refuse(errors.New("T42.2 execution freeze signing failed"))
	}
	seal.signatureRaw = append([]byte(nil), stdout...)
	seal.signatureStage, err = createExecutionSignerHeldFileLocked(ctx, owner, key.claim.names.signatureStage,
		seal.signatureRaw, maxExecutionSignerSignatureBytes)
	if err != nil {
		return refuse(err)
	}
	if err := verifyExecutionSignerCandidateLocked(ctx, key, seal.candidate, seal.signatureStage, candidateRaw); err != nil {
		return refuse(err)
	}
	seal.firstVerifiedAt = time.Now()
	if seal.firstVerifiedAt.UnixNano() <= 0 || ctx.Err() != nil {
		return refuse(ErrExecutionEpochOne)
	}
	if err := promoteExecutionSignerFileLocked(ctx, owner, seal.signatureStage, key.claim.names.signature); err != nil {
		return refuse(err)
	}
	seal.signature, seal.signatureStage = seal.signatureStage, nil
	return freeze, nil
}

func verifyExecutionSignerCandidateLocked(
	ctx context.Context,
	key *executionSignerKeyCustody,
	candidate, signature *executionSignerHeldFile,
	raw []byte,
) error {
	if candidate == nil || signature == nil {
		return ErrExecutionEpochOne
	}
	candidateRaw, err := readExecutionSignerFile(candidate, MaxExecutionFreezeBytes)
	if err != nil || !bytes.Equal(candidateRaw, raw) {
		return ErrExecutionEpochOne
	}
	err = verifyExecutionSignerPayloadLocked(ctx, key, signature, raw, executionFreezeSignatureNamespace, candidate)
	if err != nil {
		return errors.New("T42.2 execution freeze signature verification failed")
	}
	return nil
}

func verifyExecutionSignerPayloadLocked(
	ctx context.Context,
	key *executionSignerKeyCustody,
	signature *executionSignerHeldFile,
	raw []byte,
	namespace string,
	extra ...*executionSignerHeldFile,
) error {
	inputs := []*executionSignerHeldFile{key.privateKey, key.canonicalFile, key.allowlist, signature}
	inputs = append(inputs, extra...)
	_, _, err := runExecutionSignerPayloadCommandLocked(ctx, key, inputs, raw, maxExecutionSignerSignatureBytes,
		"-Y", "verify", "-f", key.allowlist.path, "-I", executionSignerIdentity,
		"-n", namespace, "-s", signature.path)
	return err
}

func sealCandidateBytes(candidate *executionSignerHeldFile, expected []byte) []byte {
	raw, err := readExecutionSignerFile(candidate, MaxExecutionFreezeBytes)
	if err != nil || !bytes.Equal(raw, expected) {
		return nil
	}
	return raw
}

func checkExecutionSignerSeal(ctx context.Context, seal *executionSignerSealCustody) error {
	key := seal.key
	if key == nil {
		return ErrExecutionEpochOne
	}
	key.mu.Lock()
	defer key.mu.Unlock()
	key.claim.mu.Lock()
	defer key.claim.mu.Unlock()
	key.signer.mu.Lock()
	defer key.signer.mu.Unlock()
	owner := key.namespace.owner
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return checkExecutionSignerSealLocked(ctx, seal)
}

func checkExecutionSignerSealLocked(ctx context.Context, seal *executionSignerSealCustody) error {
	key := seal.key
	if err := checkExecutionSignerKeyLocked(ctx, key); err != nil || seal.candidate == nil || seal.signature == nil ||
		seal.signatureStage != nil || seal.firstVerifiedAt.UnixNano() <= 0 ||
		!bytes.Equal(sealCandidateBytes(seal.candidate, seal.candidateRaw), seal.candidateRaw) {
		return ErrExecutionEpochOne
	}
	if err := checkExecutionSignerHeldFile(ctx, seal.signature); err != nil {
		return err
	}
	signatureRaw, err := readExecutionSignerFile(seal.signature, maxExecutionSignerSignatureBytes)
	if err != nil || !bytes.Equal(signatureRaw, seal.signatureRaw) {
		return ErrExecutionEpochOne
	}
	canonical, err := canonicalExecutionFreezeBytes(seal.freeze, MaxExecutionFreezeBytes)
	if err != nil || !bytes.Equal(canonical, seal.candidateRaw) {
		return ErrExecutionEpochOne
	}
	return nil
}

func verifyExecutionSignerSealAndIssue(
	ctx context.Context,
	seal *executionSignerSealCustody,
	plan Plan,
) (ExecutionFreezeAdmissionBinding, error) {
	key := seal.key
	if key == nil {
		return ExecutionFreezeAdmissionBinding{}, ErrExecutionEpochOne
	}
	key.mu.Lock()
	defer key.mu.Unlock()
	key.claim.mu.Lock()
	defer key.claim.mu.Unlock()
	key.signer.mu.Lock()
	defer key.signer.mu.Unlock()
	owner := key.namespace.owner
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if err := checkExecutionSignerSealLocked(ctx, seal); err != nil {
		return ExecutionFreezeAdmissionBinding{}, err
	}
	if err := verifyExecutionSignerCandidateLocked(ctx, key, seal.candidate, seal.signature, seal.candidateRaw); err != nil {
		return ExecutionFreezeAdmissionBinding{}, err
	}
	return issueExecutionFreezeAdmission(plan, seal.freeze, key)
}

func closeExecutionSignerSeal(seal *executionSignerSealCustody) error {
	for _, held := range []*executionSignerHeldFile{
		seal.candidate, seal.signatureStage, seal.signature,
		seal.sourceSignature, seal.returnedSignature,
	} {
		if held != nil && held.file != nil && held.file.Close() != nil {
			return ErrExecutionEpochOne
		}
	}
	return nil
}
