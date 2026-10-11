package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// stubExtractor is a minimal in-tree extractor identity used only to exercise
// the released-admission merge and recipe plumbing.
type stubExtractor struct {
	domain  string
	version string
}

func (s stubExtractor) Domain() string        { return s.domain }
func (s stubExtractor) Version() string       { return s.version }
func (s stubExtractor) Candidate(string) bool { return false }
func (s stubExtractor) Extract(context.Context, sdk.Corpus, sdk.Emit) (sdk.Coverage, error) {
	return sdk.Coverage{}, nil
}

func testReleaseDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

type releaseArtifacts map[string]string

func (r releaseArtifacts) Resolve(_ context.Context, id string) (string, bool, error) {
	digest, ok := r[id]
	return digest, ok, nil
}

// bindReleaseLoad makes releaseLoadBindings supply exactly the implementation
// and artifact root the first record names, and every artifact each record
// names, for the rest of the test. The selection has one running binding, so a
// later record whose implementation or root differs from the first's refuses.
func bindReleaseLoad(t *testing.T, release *packrelease.PackRelease, more ...*packrelease.PackRelease) {
	t.Helper()
	artifacts := releaseArtifacts{}
	for _, named := range append([]*packrelease.PackRelease{release}, more...) {
		artifacts[named.Card.ArtifactID] = named.Card.Digest
		artifacts[named.Manifest.ArtifactID] = named.Manifest.Digest
		artifacts[named.Validation.ArtifactID] = named.Validation.Digest
	}
	restore := releaseLoadBindings
	releaseLoadBindings = func(context.Context, string) (packrelease.Options, error) {
		implementation := release.Implementation
		return packrelease.Options{
			Implementation:                &implementation,
			ReferencedArtifactsRootDigest: release.ReferencedArtifactsRootDigest,
			Resolver:                      artifacts,
		}, nil
	}
	t.Cleanup(func() { releaseLoadBindings = restore })
}

// writeReleasedRecord signs a well-formed released PackRelease for packID into
// dir and returns the base64 public key that admits it and the record.
func writeReleasedRecord(t *testing.T, dir, packID, keyID string) (string, *packrelease.PackRelease) {
	t.Helper()
	return writeStatusRecord(t, dir, packID, keyID, packrelease.StatusReleased)
}

// writeStatusRecord signs a well-formed PackRelease for packID carrying status
// into dir and returns the base64 public key that admits it and the record. A
// non-released status (for example a suspension) still verifies against the
// trust anchors; it withdraws its pack instead of admitting it.
func writeStatusRecord(t *testing.T, dir, packID, keyID string, status string) (string, *packrelease.PackRelease) {
	t.Helper()
	return writeShapedRecord(t, dir, packID, keyID, func(release *packrelease.PackRelease) {
		release.DerivedStatus = status
	})
}

// writeShapedRecord signs a well-formed released PackRelease for packID into
// dir after shape adjusts it, and returns the base64 public key that admits it
// and the record. shape may change any field Sign still accepts, such as the
// lifecycle status, the validation clock or the implementation identity.
func writeShapedRecord(
	t *testing.T, dir, packID, keyID string, shape func(*packrelease.PackRelease),
) (string, *packrelease.PackRelease) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	release := &packrelease.PackRelease{
		ReleaseSchemaVersion: packrelease.ReleaseSchemaVersion,
		ReleaseID:            "rel-" + packID,
		ReleaseVersion:       "1.0.0",
		PackID:               packID,
		PackClaimVersion:     "1.0.0",
		Card:                 packrelease.ArtifactRef{ArtifactID: "card." + packID, Digest: testReleaseDigest("card" + packID)},
		Manifest:             packrelease.ArtifactRef{ArtifactID: "manifest." + packID, Digest: testReleaseDigest("manifest" + packID)},
		Implementation: packrelease.Implementation{
			PhebsSourceCommit:        strings.Repeat("c", 40),
			PhebsBinaryDigest:        testReleaseDigest("binary"),
			PackImplementationDigest: testReleaseDigest("pack" + packID),
			ToolchainDigest:          testReleaseDigest("toolchain"),
		},
		ReferencedArtifactsRootDigest: testReleaseDigest("root" + packID),
		Validation: packrelease.Validation{
			ArtifactID: "validation." + packID,
			Digest:     testReleaseDigest("validation" + packID),
			Applies:    true,
			ExpiresAt:  "2999-12-31T23:59:59Z",
		},
		DerivedStatus:   packrelease.StatusReleased,
		ApprovedAt:      "2026-07-17T20:00:00Z",
		ApprovalRecords: []string{"approval-" + packID},
	}
	shape(release)
	if err := packrelease.Sign(release, keyID, private); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := packrelease.CanonicalPayload(release)
	if err != nil {
		t.Fatalf("CanonicalPayload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, packID+".json"), raw, 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
	return base64.StdEncoding.EncodeToString(public), release
}

func TestReleasedExtractorsEmptyPathAdmitsNothing(t *testing.T) {
	cfg := &config.Config{}
	extractors, err := releasedExtractors(context.Background(), cfg)
	if err != nil {
		t.Fatalf("releasedExtractors: %v", err)
	}
	if extractors != nil {
		t.Fatalf("empty path must add no pack work, got %d extractors", len(extractors))
	}
}

func TestReleasedExtractorsRefusesWithoutLoadBindings(t *testing.T) {
	dir := t.TempDir()
	public, _ := writeReleasedRecord(t, dir, "phebs.unbound.pack", "key-1")
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}

	// With no artifacts directory configured the production bindings are
	// unbound, so a governing released record cannot be bound to this binary and
	// must refuse startup.
	_, err := releasedExtractors(context.Background(), cfg)
	if reason, _ := packrelease.ReasonOf(err); reason != packrelease.ReasonUnresolvedReference {
		t.Fatalf("reason = %q (%v), want %q", reason, err, packrelease.ReasonUnresolvedReference)
	}
}

func TestReleasedExtractorsRefusesUnboundReleasedPack(t *testing.T) {
	dir := t.TempDir()
	public, release := writeReleasedRecord(t, dir, "phebs.unbound.pack", "key-1")
	bindReleaseLoad(t, release)
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}

	// packRecipes ships empty, so a verified released pack has no in-tree recipe
	// and must refuse startup rather than silently admit nothing.
	if _, err := releasedExtractors(context.Background(), cfg); err == nil {
		t.Fatal("a released pack with no fixed in-tree recipe must refuse startup")
	}
}

func TestReleasedExtractorsResolvesBoundRecipe(t *testing.T) {
	dir := t.TempDir()
	public, release := writeReleasedRecord(t, dir, "phebs.bound.pack", "key-1")
	bindReleaseLoad(t, release)
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}

	bindRecipes(t, map[string][]extract.Extractor{
		"phebs.bound.pack": {stubExtractor{domain: "bound", version: "1"}},
	})

	extractors, err := releasedExtractors(context.Background(), cfg)
	if err != nil {
		t.Fatalf("releasedExtractors: %v", err)
	}
	if len(extractors) != 1 || extractors[0].Domain() != "bound" {
		t.Fatalf("expected the bound recipe's extractor, got %#v", extractors)
	}
}

func TestReleasedExtractorsRefusesMalformedKey(t *testing.T) {
	dir := t.TempDir()
	_, _ = writeReleasedRecord(t, dir, "phebs.any.pack", "key-1")
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = dir
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: "not-base64!"}}
	if _, err := releasedExtractors(context.Background(), cfg); err == nil {
		t.Fatal("a malformed trust-anchor key must refuse startup")
	}
}

func TestMergeExtractorsReleasedGovernsDomain(t *testing.T) {
	dark := []extract.Extractor{
		stubExtractor{domain: "a", version: "1"},
		stubExtractor{domain: "b", version: "1"}, // governed by the released b
	}
	released := []extract.Extractor{
		stubExtractor{domain: "b", version: "2"},
		stubExtractor{domain: "c", version: "1"},
	}
	merged := mergeExtractors(dark, released)
	var order []string
	for _, extractor := range merged {
		order = append(order, extractor.Domain()+"@"+extractor.Version())
	}
	if strings.Join(order, ",") != "a@1,b@2,c@1" {
		t.Fatalf("merged = %v, want a@1,b@2,c@1 (one extractor per domain, released wins)", order)
	}
}

func TestMergeExtractorsNoReleasedReturnsDarkUnchanged(t *testing.T) {
	dark := []extract.Extractor{stubExtractor{domain: "a", version: "1"}}
	if merged := mergeExtractors(dark, nil); len(merged) != 1 || merged[0].Domain() != "a" {
		t.Fatalf("no released extractors must leave the dark set unchanged, got %#v", merged)
	}
}
