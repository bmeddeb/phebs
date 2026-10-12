// Offline canonicalization only; no store, network, or publication mutation.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

func canonicalize(input io.Reader, output io.Writer) error {
	var bundle api.ProofBundle
	limited := &io.LimitedReader{R: input, N: (64 << 20) + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("extra input or oversized bundle")
	}
	if limited.N == 0 {
		return fmt.Errorf("proof input exceeds 64 MiB")
	}
	if bundle.SchemaVersion != "proof-bundle-v1" {
		return fmt.Errorf("unexpected proof schema")
	}
	if err := bundle.Coverage.ValidateCanonicalShape(); err != nil {
		return err
	}
	bundle.Coverage.Digest = ""
	coverage, err := json.Marshal(bundle.Coverage)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(coverage)
	bundle.Coverage.Digest = "sha256:" + hex.EncodeToString(digest[:])
	content, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(api.ProofBundleEnvelope{ID: store.ComputeProofBundleID(string(content)), Bundle: bundle})
}

func main() {
	if err := canonicalize(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "canonicalize proof:", err)
		os.Exit(1)
	}
}
