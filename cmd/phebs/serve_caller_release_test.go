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
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// TestCallerMapReleaseArtifactsSignAndLoad uses the proposed release artifacts
// with an ephemeral test key. Binary facts are synthetic because go test has no
// source revision; artifact census, signing, loader and API/MCP assembly are real.
func TestCallerMapReleaseArtifactsSignAndLoad(t *testing.T) {
	tmp := t.TempDir()
	artifacts := filepath.Join(tmp, "artifacts")
	selection := filepath.Join(tmp, "selection")
	for _, path := range []string{artifacts, selection} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"caller-map-card-1.0.0.md", "caller-map-manifest-1.0.0.json", "caller-map-validation-1.0.0.json"} {
		raw, err := os.ReadFile(filepath.Join("../../spike/t475/release/artifacts", name))
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
	rawTemplate, err := os.ReadFile("../../spike/t475/release/receipts/template.json")
	if err != nil {
		t.Fatal(err)
	}
	var template packrelease.PackRelease
	if err := json.Unmarshal(rawTemplate, &template); err != nil {
		t.Fatal(err)
	}
	// Keep the fixture valid after the proposed production release expires.
	// Actual expiry/withdrawal remains covered by the selection matrix.
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
		key, "integration-only", artifacts, filepath.Join(selection, "caller-map.json"))
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
	served := serveCallerSurfaces(t, cfg)
	if !served.callerMapEnabled || !served.callerMapServiced || !served.callerMapTool || served.comparisonTool || served.proofTool {
		t.Fatalf("signed integration-qualified release surface = %+v", served)
	}
	// The startup census must detect artifact movement after signing, rather
	// than accepting the record's own assertions about its referenced bytes.
	if err := os.WriteFile(filepath.Join(artifacts, "caller-map-card-1.0.0.md"), []byte("changed claim"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := releasedExtractors(t.Context(), cfg); err == nil {
		t.Fatal("signed Caller Map admitted changed claim artifacts")
	}
}
