//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newExecutionSignerKeyTestClaim(t *testing.T, ceremonyID string) (*executionSignerCeremonyClaimCustody, *ExecutionSystemToolCustody) {
	t.Helper()
	namespace := newExecutionSignerNamespaceTestBinding(t)
	claim, err := claimExecutionSignerCeremony(t.Context(), namespace, ceremonyID, canonicalSignerClaimTestRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = claim.Close() })
	signer, err := HoldExecutionSystemTool(t.Context(), "ssh-keygen")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = signer.Close() })
	return claim, signer
}

func TestExecutionSignerKeyGenerationClaimsFingerprintAndPromotes(t *testing.T) {
	claim, signer := newExecutionSignerKeyTestClaim(t, "t422-key-success")
	key, err := prepareExecutionSignerKey(t.Context(), claim, signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.check(t.Context()); err != nil {
		t.Fatal("fresh signer key custody did not revalidate", err)
	}
	if key.privateKey.name != claim.names.privateKey || key.generatedPublic.name != claim.names.generatedPublic ||
		!validExecutionHexSHA256(key.publicSHA256) || !validSSHSHA256Fingerprint(key.fingerprint) ||
		key.fingerprintClaim.name != "fingerprint-"+key.publicSHA256+".claim.json" {
		t.Fatal("signer key or fingerprint registry identity differs")
	}
	wantClaim, err := MarshalCanonical(executionSignerFingerprintClaimV1{
		Schema: executionSignerFingerprintClaimSchema, SignerNamespaceSHA256: claim.namespace.digest,
		CeremonyID: claim.ceremonyID, CanonicalPublicKey: string(key.canonicalPublic), SignerFingerprint: key.fingerprint,
	})
	if err != nil || !bytes.Equal(key.fingerprintRaw, wantClaim) || len(wantClaim) > maxExecutionSignerFingerprintClaimBytes {
		t.Fatal("fingerprint claim is not exact bounded canonical JSON", err)
	}
	for _, path := range []string{key.privateKey.path, key.generatedPublic.path, key.fingerprintClaim.path} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatal("promoted signer custody is not a mode-0600 regular file", path, err)
		}
	}
	for _, name := range []string{claim.names.temporaryKey, claim.names.temporaryPublic} {
		if _, err := os.Lstat(filepath.Join(claim.namespace.owner.path, name)); !os.IsNotExist(err) {
			t.Fatal("temporary key survived exclusive promotion", name, err)
		}
	}
	retained := []string{key.privateKey.path, key.generatedPublic.path, key.fingerprintClaim.path}
	if err := key.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range retained {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("closing signer custody removed retained registry state", path, err)
		}
	}
}

func TestExecutionSignerKeyAttemptIsOneShot(t *testing.T) {
	claim, signer := newExecutionSignerKeyTestClaim(t, "t422-key-canceled")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if key, err := prepareExecutionSignerKey(ctx, claim, signer); err == nil || key != nil {
		t.Fatal("canceled key attempt ran signer work")
	}
	if key, err := prepareExecutionSignerKey(t.Context(), claim, signer); err == nil || key != nil {
		t.Fatal("spent key preparation attempt was reusable")
	}
	if _, err := os.Lstat(claim.path); err != nil {
		t.Fatal("failed key preparation did not retain the burned ID claim", err)
	}
}

func TestExecutionSignerKeyRefusesPostClaimTempCollision(t *testing.T) {
	claim, signer := newExecutionSignerKeyTestClaim(t, "t422-key-collision")
	collision := filepath.Join(claim.namespace.owner.path, claim.names.temporaryKey)
	if err := os.WriteFile(collision, []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := prepareExecutionSignerKey(t.Context(), claim, signer)
	if err == nil || key == nil {
		t.Fatal("post-claim temporary collision was admitted")
	}
	if closeErr := key.Close(); closeErr != nil {
		t.Fatal("collision without owned temporary custody could not close", closeErr)
	}
	if raw, err := os.ReadFile(collision); err != nil || string(raw) != "not ours" {
		t.Fatal("cleanup touched an unowned colliding path", err)
	}
	if retry, err := prepareExecutionSignerKey(t.Context(), claim, signer); err == nil || retry != nil {
		t.Fatal("failed post-claim key attempt was reusable")
	}
}

func TestExecutionSignerKeyDetectsPromotedFileDrift(t *testing.T) {
	claim, signer := newExecutionSignerKeyTestClaim(t, "t422-key-drift")
	key, err := prepareExecutionSignerKey(t.Context(), claim, signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(key.generatedPublic.path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := key.check(t.Context()); err == nil {
		t.Fatal("changed promoted public-key mode remained admitted")
	}
	if err := key.Close(); err != nil {
		t.Fatal(err)
	}
}
