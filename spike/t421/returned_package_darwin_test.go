//go:build darwin

package t421

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestBuildExecutionReturnedPackageAuthenticatesExactInventoryOnce(t *testing.T) {
	for _, schema := range []string{PlanV3Schema, PlanV5Schema} {
		t.Run(schema, func(t *testing.T) {
			plan := clonePlan(t, correctedTestPlan(t))
			if schema == PlanV5Schema {
				plan = completeV5ReceiptTestPlan(t)
			} else if err := applyProcessAccountingCorrection(&plan); err != nil {
				t.Fatal(err)
			}
			testBuildExecutionReturnedPackage(t, plan)
		})
	}
}

func testBuildExecutionReturnedPackage(t *testing.T, plan Plan) {
	t.Helper()
	commits := executionFreezeTestCommits()
	tools, host := executionFreezeTestTools(plan, commits), executionFreezeTestHost()
	namespace := newExecutionSignerNamespaceTestBinding(t)
	profileAdmission := executionProfileTestAdmission(t, plan, tools, host, namespace.digest)
	profile, err := expectedExecutionProfile(plan, tools, host, profileAdmission)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := claimExecutionSignerCeremony(t.Context(), namespace, "t422-returned-package", canonicalSignerClaimTestRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = claim.Close() })
	signer, err := HoldExecutionSystemTool(t.Context(), "ssh-keygen")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = signer.Close() })
	key, err := prepareExecutionSignerKey(t.Context(), claim, signer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = key.Close() })
	raw, err := assembleExecutionFreezeCandidate(plan, commits, tools, host, key.fingerprint, namespace, profile, profileAdmission)
	if err != nil {
		t.Fatal(err)
	}
	seal, err := sealExecutionFreezeCandidate(t.Context(), key, plan, executionFreezeCandidatePreparation{
		raw: raw, commits: commits, checkout: executionFreezeTestCheckout(t, commits, tools),
		profile: profile, profileAdmission: profileAdmission, namespace: namespace,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = seal.Close() })
	admission, err := seal.verifyAndIssueAdmission(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	freezeBinding, err := bindExecutionFreezeForReceipt(seal.freeze, plan, commits, key.fingerprint, namespace.digest, admission)
	if err != nil {
		t.Fatal(err)
	}
	receipt := completeTestReceipt(t, plan, freezeBinding)
	sourceVerification, err := executionSourceVerificationBytes(plan, freezeBinding, receipt.RevisionResults)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Authority.SourceVerificationSHA256 = SHA256(sourceVerification)
	if err := validateReceiptEvidence(receipt, plan, freezeBinding, receipt.Authority.SourceVerificationSHA256); err != nil {
		t.Fatalf("%s modeled receipt before package signing: %v", plan.Schema, err)
	}

	packageRaw, packageBinding, err := buildExecutionReturnedPackage(t.Context(), plan, receipt, freezeBinding, seal)
	if err != nil {
		t.Fatalf("%s returned package creation: %v", plan.Schema, err)
	}
	files, err := inspectExecutionReturnedPackage(packageRaw, plan)
	if err != nil || len(files) != 11 || packageBinding.packageSHA256 != SHA256(packageRaw) ||
		!reflect.DeepEqual(packageBinding.exactInventory, plan.SealPolicy.ExactInventory) {
		t.Fatal("returned package did not retain the exact authenticated inventory", err)
	}
	t.Logf("%s signed fixture receipt bytes=%d/%d package bytes=%d/%d", plan.Schema,
		len(files["results.json"]), plan.ReceiptContract.MaximumBytes, len(packageRaw), plan.SealPolicy.MaximumPackageBytes)
	if _, _, err := buildExecutionReturnedPackage(t.Context(), plan, receipt, freezeBinding, seal); err == nil {
		t.Fatal("returned-package authority was reusable")
	}
	selection, _ := testExecutionSelection(t)
	selection.PlanSourceCommit = plan.SourceCommit
	selection.IntegratedMainCommit = commits.IntegratedMainCommit
	selection.SourceCommit = commits.T422SourceCommit
	freezeDigest := strings.TrimPrefix(freezeBinding.freezeSHA256, "sha256:")
	executionOuterSignedFixture(t, selection, packageRaw)
	verified, err := verifyExecutionReturnedPackage(t.Context(), packageRaw, selection, freezeDigest)
	if err != nil || verified.digest != SHA256(packageRaw) || !bytes.Equal(verified.raw, packageRaw) ||
		!reflect.DeepEqual(verified.receipt, receipt) {
		t.Fatal("outer did not independently authenticate the exact returned bytes", err)
	}
	frame, err := frameExecutionReturnedPackage(packageRaw, packageBinding)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureExecutionReturnedPackage(bytes.NewReader(frame))
	if err != nil || !bytes.Equal(captured, packageRaw) {
		t.Fatal("framed package changed", err)
	}
	for _, name := range []string{"results.json", "source-verification.json.sig", "SHA256SUMS.sig", "allowed_signers", "signer.pub"} {
		original := files[name]
		files[name] = append(bytes.Clone(original), 'x')
		changed, err := marshalExecutionReturnedPackage(files, plan)
		files[name] = original
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyExecutionReturnedPackage(t.Context(), changed, selection, freezeDigest); err == nil {
			t.Fatalf("outer accepted changed %s", name)
		}
	}
}
