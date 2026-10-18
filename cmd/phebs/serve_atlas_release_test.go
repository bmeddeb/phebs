package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// TestProtobufAtlasReleaseArtifactsSignAndLoad binds the proposed artifact
// bytes through the actual signer/census/loader using a disposable test key.
// Only binary facts are synthetic: go test has no VCS source revision.
func TestProtobufAtlasReleaseArtifactsSignAndLoad(t *testing.T) {
	tmp := t.TempDir()
	artifacts := filepath.Join(tmp, "artifacts")
	selection := filepath.Join(tmp, "selection")
	for _, path := range []string{artifacts, selection} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"protobuf-atlas-card-1.0.0.md", "protobuf-atlas-manifest-1.0.0.json", "protobuf-atlas-validation-1.0.0.json"} {
		raw, err := os.ReadFile(filepath.Join("../../spike/t494/release/artifacts", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifacts, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(tmp, "integration-only.pem")
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	facts := releaseFacts{
		executable: writeReleaseExecutable(t, tmp, "fixture-phebs", "integration-binary", 0o700),
		goVersion:  "go1.26.5", sourceCommit: strings.Repeat("a", 40),
	}
	rawTemplate, err := os.ReadFile("../../spike/t494/release/receipts/template.json")
	if err != nil {
		t.Fatal(err)
	}
	var template packrelease.PackRelease
	if err := json.Unmarshal(rawTemplate, &template); err != nil {
		t.Fatal(err)
	}
	if template.PackID != protobufContractPackID {
		t.Fatalf("Atlas template names %q", template.PackID)
	}
	// Keep this integration fixture valid after the proposed release expires.
	template.ApprovedAt, template.Validation.ExpiresAt = "2000-01-01T00:00:00Z", "2999-12-31T23:59:59Z"
	rawTemplate, err = json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(tmp, "template.json")
	if err := os.WriteFile(templatePath, rawTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	public, err := signRelease(t.Context(), facts, templatePath,
		key, "integration-only", artifacts, filepath.Join(selection, "protobuf-atlas.json"))
	if err != nil {
		t.Fatal(err)
	}
	restore := releaseLoadBindings
	releaseLoadBindings = func(_ context.Context, path string) (packrelease.Options, error) {
		return releaseLoadBindingsFor(path, facts)
	}
	t.Cleanup(func() { releaseLoadBindings = restore })
	cfg := &config.Config{}
	cfg.Server.DataDir = tmp
	cfg.ReleaseSelection.Path = selection
	cfg.ReleaseSelection.ArtifactsPath = artifacts
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "integration-only", PublicKey: base64.StdEncoding.EncodeToString(public)}}
	want := servedCallerSurfaces{capabilities: []string{"contract-atlas", "service-catalog-v2"}, atlasTools: 2}
	if got := serveCallerSurfaces(t, cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("signed integration-qualified Atlas surface = %+v, want %+v", got, want)
	}
	cfg.ReleaseSelection.Revoked = []string{template.ReleaseID}
	want = servedCallerSurfaces{capabilities: []string{"service-catalog-v2"}}
	if got := serveCallerSurfaces(t, cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("revoked Atlas surface = %+v, want %+v", got, want)
	}
	cfg.ReleaseSelection.Revoked = nil
	// A valid signature cannot admit a release for another source or changed
	// artifact bytes, even though the configured keys and record are unchanged.
	facts.sourceCommit = strings.Repeat("b", 40)
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("signed Atlas admitted a different source revision")
	}
	facts.sourceCommit = strings.Repeat("a", 40)
	if err := os.WriteFile(filepath.Join(artifacts, "protobuf-atlas-card-1.0.0.md"), []byte("changed claim"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("signed Atlas admitted changed claim artifacts")
	}
}
