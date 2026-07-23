// Command verify-dossier verifies a sealed Investigation dossier without
// contacting a phebs instance or any network service.
//
// Usage:
//
//	go run ./scripts/verify-dossier.go [flags] dossier.json
//
// Supply -trusted-key-id and -trusted-public-key (base64 Ed25519) when the
// signer must be checked against an independently distributed trust anchor.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/bmeddeb/phebs/internal/dossier"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("verify-dossier", flag.ContinueOnError)
	flags.SetOutput(stderr)
	trustedKeyID := flags.String(
		"trusted-key-id",
		"",
		"expected signer key identity",
	)
	trustedPublic := flags.String(
		"trusted-public-key",
		"",
		"expected base64 Ed25519 public key",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: verify-dossier [flags] dossier.json")
		return 2
	}
	var publicKey ed25519.PublicKey
	if *trustedPublic != "" {
		decoded, err := base64.StdEncoding.DecodeString(*trustedPublic)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			_, _ = fmt.Fprintln(stderr, "invalid -trusted-public-key")
			return 2
		}
		publicKey = ed25519.PublicKey(decoded)
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "read dossier: %v\n", err)
		return 1
	}
	content, readErr := io.ReadAll(io.LimitReader(file, dossier.MaxSealedBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		_, _ = fmt.Fprintf(stderr, "read dossier: %v\n", readErr)
		return 1
	}
	if closeErr != nil {
		_, _ = fmt.Fprintf(stderr, "close dossier: %v\n", closeErr)
		return 1
	}
	if len(content) > dossier.MaxSealedBytes {
		_, _ = fmt.Fprintln(stderr, "read dossier: file exceeds 64 MiB")
		return 1
	}
	sealed, err := dossier.Decode(content)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	verification, err := dossier.Verify(*sealed, dossier.VerifyOptions{
		TrustedKeyID: *trustedKeyID, TrustedPublic: publicKey,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "verification failed: %v\n", err)
		return 1
	}
	output := struct {
		Status          string `json:"status"`
		DossierID       string `json:"dossier_id"`
		RootDigest      string `json:"root_digest"`
		KeyID           string `json:"key_id"`
		PublicKeySHA256 string `json:"public_key_sha256"`
		EntryCount      int    `json:"entry_count"`
		Validity        string `json:"validity"`
	}{
		Status: "verified", DossierID: verification.DossierID,
		RootDigest: verification.RootDigest, KeyID: verification.KeyID,
		PublicKeySHA256: verification.PublicKeySHA256,
		EntryCount:      verification.EntryCount,
		Validity:        dossier.ValidityStatement(),
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "encode verification: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, string(encoded))
	return 0
}
