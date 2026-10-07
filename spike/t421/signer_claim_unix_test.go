//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func canonicalSignerClaimTestRoot(t *testing.T) string {
	t.Helper()
	created, err := os.MkdirTemp("/tmp", "t422-op-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(created) })
	root, err := filepath.EvalSymlinks(created)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestExecutionSignerCeremonyClaimIsExclusiveAndRetained(t *testing.T) {
	namespace := newExecutionSignerNamespaceTestBinding(t)
	operational := canonicalSignerClaimTestRoot(t)
	claim, err := claimExecutionSignerCeremony(t.Context(), namespace, "t422-claim-test", operational)
	if err != nil {
		t.Fatal(err)
	}
	if claim.idSHA256 != "6d811e0d699509c861adaed9d395d4160b2c07f064927d3dde089bbce7573c47" ||
		claim.name != "ceremony-"+claim.idSHA256+".claim.json" || claim.namespace.digest != namespace.digest {
		t.Fatal("ceremony claim identity differs from its exact ID or namespace")
	}
	want, err := MarshalCanonical(executionSignerCeremonyClaimV1{
		Schema: executionSignerCeremonyClaimSchema, SignerNamespaceSHA256: namespace.digest, CeremonyID: "t422-claim-test",
	})
	if err != nil || !bytes.Equal(claim.raw, want) || len(claim.raw) > maxExecutionSignerClaimBytes {
		t.Fatal("ceremony claim bytes are not the exact bounded canonical preimage", err)
	}
	if err := claim.check(t.Context()); err != nil {
		t.Fatal("fresh ceremony claim did not revalidate", err)
	}
	info, err := os.Lstat(claim.path)
	if err != nil || info.Mode().Perm() != 0o600 || !info.Mode().IsRegular() {
		t.Fatal("ceremony claim is not a retained mode-0600 regular file", err)
	}
	path := claim.path
	if err := claim.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("closing custody removed the burned ceremony claim", err)
	}
	if retry, err := claimExecutionSignerCeremony(t.Context(), namespace, "t422-claim-test", operational); err == nil || retry != nil {
		t.Fatal("a claimed ceremony ID was reusable in the same namespace")
	}
}

func TestExecutionSignerCeremonyRefusesKnownDestinationsBeforeClaim(t *testing.T) {
	for _, field := range []struct {
		name string
		pick func(executionSignerRegistryNames) string
	}{
		{"temporary private", func(n executionSignerRegistryNames) string { return n.temporaryKey }},
		{"temporary public", func(n executionSignerRegistryNames) string { return n.temporaryPublic }},
		{"final private", func(n executionSignerRegistryNames) string { return n.privateKey }},
		{"final generated public", func(n executionSignerRegistryNames) string { return n.generatedPublic }},
		{"allowlist", func(n executionSignerRegistryNames) string { return n.allowlist }},
		{"candidate", func(n executionSignerRegistryNames) string { return n.candidate }},
		{"signature stage", func(n executionSignerRegistryNames) string { return n.signatureStage }},
		{"signature final", func(n executionSignerRegistryNames) string { return n.signature }},
		{"source signature stage", func(n executionSignerRegistryNames) string { return n.sourceSignatureStage }},
		{"source signature final", func(n executionSignerRegistryNames) string { return n.sourceSignature }},
		{"returned signature stage", func(n executionSignerRegistryNames) string { return n.returnedSignatureStage }},
		{"returned signature final", func(n executionSignerRegistryNames) string { return n.returnedSignature }},
	} {
		t.Run(field.name, func(t *testing.T) {
			namespace := newExecutionSignerNamespaceTestBinding(t)
			names := executionSignerNames("t422-collision")
			if err := os.WriteFile(filepath.Join(namespace.owner.path, field.pick(names)), []byte("occupied"), 0o600); err != nil {
				t.Fatal(err)
			}
			claim, err := claimExecutionSignerCeremony(t.Context(), namespace, "t422-collision", canonicalSignerClaimTestRoot(t))
			if err == nil || claim != nil {
				t.Fatal("known destination collision reached ceremony claim creation")
			}
			if _, err := os.Lstat(filepath.Join(namespace.owner.path, names.claim)); !os.IsNotExist(err) {
				t.Fatal("preflight refusal created a ceremony claim", err)
			}
		})
	}
}

func TestExecutionSignerCeremonyRefusesBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*testing.T, executionSignerNamespaceBinding) (*executionSignerCeremonyClaimCustody, error)
	}{
		{"canceled", func(t *testing.T, namespace executionSignerNamespaceBinding) (*executionSignerCeremonyClaimCustody, error) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return claimExecutionSignerCeremony(ctx, namespace, "t422-canceled", canonicalSignerClaimTestRoot(t))
		}},
		{"invalid ID", func(t *testing.T, namespace executionSignerNamespaceBinding) (*executionSignerCeremonyClaimCustody, error) {
			return claimExecutionSignerCeremony(t.Context(), namespace, "../escape", canonicalSignerClaimTestRoot(t))
		}},
		{"auth socket overrun", func(t *testing.T, namespace executionSignerNamespaceBinding) (*executionSignerCeremonyClaimCustody, error) {
			root := canonicalSignerClaimTestRoot(t)
			for len(filepath.Join(root, "auth.sock")) <= maxExecutionAuthSocketPathBytes {
				root = filepath.Join(root, "long-segment")
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			return claimExecutionSignerCeremony(t.Context(), namespace, "t422-long-socket", root)
		}},
		{"namespace drift", func(t *testing.T, namespace executionSignerNamespaceBinding) (*executionSignerCeremonyClaimCustody, error) {
			if err := os.Chmod(namespace.owner.path, 0o755); err != nil {
				t.Fatal(err)
			}
			return claimExecutionSignerCeremony(t.Context(), namespace, "t422-root-drift", canonicalSignerClaimTestRoot(t))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			namespace := newExecutionSignerNamespaceTestBinding(t)
			claim, err := test.call(t, namespace)
			if err == nil || claim != nil {
				t.Fatal("invalid preclaim state created a ceremony claim")
			}
			name := executionSignerNames(map[string]string{
				"canceled": "t422-canceled", "invalid ID": "../escape",
				"auth socket overrun": "t422-long-socket", "namespace drift": "t422-root-drift",
			}[test.name]).claim
			if _, err := os.Lstat(filepath.Join(namespace.owner.path, name)); !os.IsNotExist(err) {
				t.Fatal("preclaim refusal mutated the signer namespace", err)
			}
		})
	}
}
