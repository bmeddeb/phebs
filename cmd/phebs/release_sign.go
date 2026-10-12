package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmeddeb/phebs/internal/executableidentity"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// releaseSign signs a PackRelease template with an operator ed25519 key. Every
// binding a load checks is derived exactly as startup derives it, never taken
// from the template: the implementation identity from this running binary's
// build metadata, executable digest and in-tree registries, and the artifact
// digests and root from one census of the artifact directory. Signing therefore
// runs the binary being released, and the written record is verified through
// the same selection loader startup uses before success is reported.
func releaseSign(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("release-sign", flag.ContinueOnError)
	templatePath := flags.String("template", "", "PackRelease JSON template: identity, artifact ids, validation expiry, status, approvals")
	keyPath := flags.String("key", "", "PKCS#8 PEM ed25519 private key")
	keyID := flags.String("key-id", "", "approved key identifier the operator configures beside the public key")
	artifacts := flags.String("artifacts", "", "flat artifact directory holding the card, manifest and validation")
	out := flags.String("out", "", "release record to write; its directory is the release selection")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *templatePath == "" || *keyPath == "" || *keyID == "" || *artifacts == "" || *out == "" || flags.NArg() != 0 {
		return errors.New("release-sign requires -template, -key, -key-id, -artifacts and -out")
	}
	facts, err := readReleaseBuildFacts()
	if err != nil {
		return err
	}
	public, err := signRelease(ctx, facts, *templatePath, *keyPath, *keyID, *artifacts, *out)
	if err != nil {
		return err
	}
	digest, err := executableidentity.Digest(facts.executable)
	if err != nil {
		return err
	}
	fmt.Printf("signed %s\nbinary %s\npublic key %s\n", *out, digest, base64.StdEncoding.EncodeToString(public))
	return nil
}

// signRelease signs the template for the binary facts describe and returns the
// public key, after the loader has admitted the written record.
func signRelease(ctx context.Context, facts releaseFacts, templatePath, keyPath, keyID, artifacts, out string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var release packrelease.PackRelease
	if err := decoder.Decode(&release); err != nil {
		return nil, fmt.Errorf("release template: %w", err)
	}
	private, err := readEd25519Key(keyPath)
	if err != nil {
		return nil, err
	}

	bindings, err := releaseLoadBindingsFor(artifacts, facts)
	if err != nil {
		return nil, err
	}
	release.Implementation = *bindings.Implementation
	release.ReferencedArtifactsRootDigest = bindings.ReferencedArtifactsRootDigest
	for _, ref := range []*string{&release.Card.Digest, &release.Manifest.Digest, &release.Validation.Digest} {
		*ref = ""
	}
	for id, digest := range map[string]*string{
		release.Card.ArtifactID: &release.Card.Digest, release.Manifest.ArtifactID: &release.Manifest.Digest,
		release.Validation.ArtifactID: &release.Validation.Digest,
	} {
		resolved, ok, err := bindings.Resolver.Resolve(ctx, id)
		if err != nil || !ok {
			return nil, fmt.Errorf("artifact %q is not in %s", id, artifacts)
		}
		*digest = resolved
	}
	if err := packrelease.Sign(&release, keyID, private); err != nil {
		return nil, err
	}
	payload, err := packrelease.CanonicalPayload(&release)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}

	// Self-check: the record must be admitted by the loader startup runs, under
	// a trust anchor holding only this key and the bindings derived above.
	public := private.Public().(ed25519.PublicKey)
	selection, err := packrelease.LoadSelectionWithBindings(ctx, filepath.Dir(out),
		packrelease.Options{Keys: packrelease.KeyRing{keyID: public}},
		func(context.Context) (packrelease.Options, error) { return bindings, nil })
	if err != nil {
		return nil, fmt.Errorf("signed record does not load: %w", err)
	}
	if !slices.Contains(selection.PackIDs(), release.PackID) {
		return nil, fmt.Errorf("signed record for %s is not admitted (withdrawn: %v)", release.PackID, selection.Withdrawn())
	}
	return public, nil
}

func readEd25519Key(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PKCS#8 PEM private key", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an ed25519 key", path)
	}
	return private, nil
}
