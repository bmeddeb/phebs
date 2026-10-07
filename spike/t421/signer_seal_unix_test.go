//go:build darwin || linux

package t421

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutionSignerSealsCandidateAndIssuesAdmission(t *testing.T) {
	plan := accountingTestPlan(t)
	commits := executionFreezeTestCommits()
	tools, host := executionFreezeTestTools(plan, commits), executionFreezeTestHost()
	namespace := newExecutionSignerNamespaceTestBinding(t)
	profileAdmission := executionProfileTestAdmission(t, plan, tools, host, namespace.digest)
	profile, err := expectedExecutionProfile(plan, tools, host, profileAdmission)
	if err != nil {
		t.Fatal(err)
	}
	fixture := executionFreezeCandidateTestFixture{
		plan: plan, commits: commits, tools: tools, host: host,
		signer: executionFreezeTestSigner(), namespace: namespace, profile: profile, admission: profileAdmission,
	}
	claim, err := claimExecutionSignerCeremony(t.Context(), fixture.namespace, "t422-seal-success", canonicalSignerClaimTestRoot(t))
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
	fixture.signer = key.fingerprint
	raw, err := fixture.assemble()
	if err != nil {
		t.Fatal(err)
	}
	prepared := executionFreezeCandidatePreparation{
		raw: raw, commits: fixture.commits, checkout: executionFreezeTestCheckout(t, fixture.commits, fixture.tools),
		profile: fixture.profile, profileAdmission: fixture.admission, namespace: fixture.namespace,
	}
	verificationStarted := time.Now()
	seal, err := sealExecutionFreezeCandidate(t.Context(), key, fixture.plan, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if seal.firstVerifiedAt.Before(verificationStarted) || seal.firstVerifiedAt.After(time.Now()) {
		t.Fatal("first successful verification completion was not retained")
	}
	if err := seal.check(t.Context()); err != nil {
		t.Fatal("fresh signed-freeze custody did not revalidate", err)
	}
	admission, err := seal.verifyAndIssueAdmission(t.Context(), fixture.plan)
	if err != nil {
		t.Fatal("promoted signature did not issue post-authorization admission", err)
	}
	if !admission.signatureVerified || !admission.verifiedBeforeOperationalWork || admission.verifiedBeforeWork ||
		admission.admissionEventOrdinal != 1 || admission.signerFingerprint != key.fingerprint ||
		admission.signerNamespaceSHA256 != key.namespace.digest || admission.signerNamespace.owner != key.namespace.owner {
		t.Fatal("signer did not issue exact private V3 freeze admission")
	}
	if _, err := bindExecutionFreezeForReceipt(seal.freeze, fixture.plan, fixture.commits, key.fingerprint,
		key.namespace.digest, admission); err != nil {
		t.Fatal("issued signer admission did not satisfy the existing receipt binder", err)
	}
	if canonical, err := os.ReadFile(key.canonicalFile.path); err != nil || !bytes.Equal(canonical, key.canonicalPublic) {
		t.Fatal("canonical public-key custody differs", err)
	}
	if allowlist, err := os.ReadFile(key.allowlist.path); err != nil || !bytes.Equal(allowlist, executionSignerAllowlist(key.canonicalPublic)) {
		t.Fatal("signer allowlist custody differs", err)
	}
	if candidate, err := os.ReadFile(seal.candidate.path); err != nil || !bytes.Equal(candidate, raw) {
		t.Fatal("held candidate differs from exact canonical bytes", err)
	}
	if signature, err := os.ReadFile(seal.signature.path); err != nil || len(signature) == 0 || len(signature) > maxExecutionSignerSignatureBytes {
		t.Fatal("promoted signature custody is invalid", err)
	}
	if _, err := os.Lstat(filepath.Join(key.namespace.owner.path, claim.names.signatureStage)); !os.IsNotExist(err) {
		t.Fatal("signature stage survived exclusive promotion", err)
	}
	if _, err := seal.verifyAndIssueAdmission(t.Context(), fixture.plan); err == nil {
		t.Fatal("post-authorization signature verification was reusable")
	}
	retained := []string{key.canonicalFile.path, key.allowlist.path, seal.candidate.path, seal.signature.path}
	if err := seal.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range retained {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("closing signer seal removed retained evidence", path, err)
		}
	}
	if retry, err := sealExecutionFreezeCandidate(t.Context(), key, fixture.plan, prepared); err == nil || retry != nil {
		t.Fatal("signed-freeze transition was reusable")
	}
}
