//go:build darwin || linux

package t421

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutionReturnedSignatureNative(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	key := filepath.Join(t.TempDir(), "key")
	if output, err := exec.CommandContext(ctx, "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generate test signer: %v: %s", err, output)
	}
	public, err := exec.CommandContext(ctx, "/usr/bin/ssh-keygen", "-y", "-f", key).Output()
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("exact source-free returned bytes\n")
	for _, namespace := range []string{"phebs-t422-freeze", "phebs-t422-source-verification", "phebs-t422-returned"} {
		command := exec.CommandContext(ctx, "/usr/bin/ssh-keygen", "-Y", "sign", "-f", key, "-n", namespace)
		command.Stdin = bytes.NewReader(message)
		signature, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name                       string
			signature, message, public []byte
			namespace                  string
			valid                      bool
		}{
			{"native", signature, message, public, namespace, true},
			{"changed_payload", signature, append(bytes.Clone(message), 'x'), public, namespace, false},
			{"wrong_namespace", signature, message, public, namespace + "-wrong", false},
			{"trailing", append(bytes.Clone(signature), 'x'), message, public, namespace, false},
			{"preamble", append([]byte("x"), signature...), message, public, namespace, false},
			{"missing_key", signature, message, nil, namespace, false},
			{"truncated", signature[:len(signature)-10], message, public, namespace, false},
		} {
			t.Run(namespace+"/"+test.name, func(t *testing.T) {
				if err := verifyExecutionReturnedSignature(test.signature, test.message, test.public, test.namespace); (err == nil) != test.valid {
					t.Fatalf("signature validity = %v, want %v", err, test.valid)
				}
			})
		}
	}
}
