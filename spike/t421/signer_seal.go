//go:build darwin || linux

package t421

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

const (
	executionSignerIdentity           = "phebs-t422-ceremony"
	executionFreezeSignatureNamespace = "phebs-t422-freeze"
)

type executionFreezeCandidatePreparation struct {
	raw              []byte
	commits          ExecutionCommits
	checkout         CheckoutAdmissionBinding
	profile          ExecutionProfile
	profileAdmission ExecutionProfileAdmissionBinding
	namespace        executionSignerNamespaceBinding
}

// executionSignerSealCustody retains the exact candidate and promoted
// signatures. It is not a launcher capability, checkout handoff, or receipt
// binding.
type executionSignerSealCustody struct {
	mu                sync.Mutex
	key               *executionSignerKeyCustody
	candidate         *executionSignerHeldFile
	signatureStage    *executionSignerHeldFile
	signature         *executionSignerHeldFile
	candidateRaw      []byte
	signatureRaw      []byte
	freeze            ExecutionFreeze
	firstVerifiedAt   time.Time
	admissionUsed     bool
	packageUsed       bool
	sourceSignature   *executionSignerHeldFile
	returnedSignature *executionSignerHeldFile
	closed            bool
}

func sealExecutionFreezeCandidate(
	ctx context.Context,
	key *executionSignerKeyCustody,
	plan Plan,
	prepared executionFreezeCandidatePreparation,
) (*executionSignerSealCustody, error) {
	if key == nil {
		return nil, ErrExecutionEpochOne
	}
	key.mu.Lock()
	defer key.mu.Unlock()
	if ctx == nil || key.closed || key.sealUsed || !processAccountingPlanSemantics(plan.Schema) ||
		len(prepared.raw) == 0 || len(prepared.raw) > MaxExecutionFreezeBytes {
		return nil, ErrExecutionEpochOne
	}
	key.sealUsed = true
	seal := &executionSignerSealCustody{key: key, candidateRaw: slices.Clone(prepared.raw)}
	freeze, err := createExecutionSignerSeal(ctx, seal, plan, prepared)
	if err != nil {
		return seal, err
	}
	seal.freeze = cloneExecutionFreezeForBinding(freeze)
	return seal, nil
}

// verifyAndIssueAdmission is the post-authorization seam. Its first eligible
// call spends the only promoted-signature verification attempt; only a
// successful verification may create the private admission binding.
func (seal *executionSignerSealCustody) verifyAndIssueAdmission(ctx context.Context, plan Plan) (ExecutionFreezeAdmissionBinding, error) {
	if seal == nil {
		return ExecutionFreezeAdmissionBinding{}, ErrExecutionEpochOne
	}
	seal.mu.Lock()
	defer seal.mu.Unlock()
	if seal.closed || seal.admissionUsed || !processAccountingPlanSemantics(plan.Schema) || seal.freeze.Schema == "" || seal.signature == nil {
		return ExecutionFreezeAdmissionBinding{}, ErrExecutionEpochOne
	}
	seal.admissionUsed = true
	if ctx == nil || ctx.Err() != nil {
		return ExecutionFreezeAdmissionBinding{}, ErrExecutionEpochOne
	}
	return verifyExecutionSignerSealAndIssue(ctx, seal, plan)
}

func issueExecutionFreezeAdmission(
	plan Plan,
	freeze ExecutionFreeze,
	key *executionSignerKeyCustody,
) (ExecutionFreezeAdmissionBinding, error) {
	if !processAccountingPlanSemantics(plan.Schema) || key == nil || freeze.SignerFingerprint != key.fingerprint ||
		freeze.SignerNamespaceSHA256 != key.namespace.digest {
		return ExecutionFreezeAdmissionBinding{}, errors.New("T42.2 signer cannot issue freeze admission")
	}
	planRaw, err := MarshalCanonical(plan)
	if err != nil || freeze.PlanSHA256 != SHA256(planRaw) {
		return ExecutionFreezeAdmissionBinding{}, errors.New("T42.2 signer admission plan differs from the sealed freeze")
	}
	freezeSHA256, err := receiptSHA256(freeze)
	if err != nil {
		return ExecutionFreezeAdmissionBinding{}, err
	}
	eventSHA256, err := executionAdmissionEventDigest(plan, freezeSHA256, key.fingerprint, key.namespace.digest)
	if err != nil || plan.SealPolicy.FreezeSignatureNamespace != executionFreezeSignatureNamespace {
		return ExecutionFreezeAdmissionBinding{}, errors.New("T42.2 freeze admission policy is invalid")
	}
	return ExecutionFreezeAdmissionBinding{
		schema: plan.ReceiptContract.ExecutionAdmissionSchema, freezeSHA256: freezeSHA256,
		signatureNamespace: plan.SealPolicy.FreezeSignatureNamespace,
		signerFingerprint:  key.fingerprint, signerNamespaceSHA256: key.namespace.digest,
		signerNamespace: key.namespace, admissionEventSHA256: eventSHA256, admissionEventOrdinal: 1,
		signatureVerified: true, verifiedBeforeOperationalWork: true,
	}, nil
}

func (seal *executionSignerSealCustody) check(ctx context.Context) error {
	if seal == nil || ctx == nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	seal.mu.Lock()
	defer seal.mu.Unlock()
	if seal.closed {
		return ErrExecutionEpochOne
	}
	return checkExecutionSignerSeal(ctx, seal)
}

func (seal *executionSignerSealCustody) Close() error {
	if seal == nil {
		return nil
	}
	seal.mu.Lock()
	defer seal.mu.Unlock()
	if seal.closed {
		return nil
	}
	if err := closeExecutionSignerSeal(seal); err != nil {
		return err
	}
	seal.closed = true
	return nil
}
