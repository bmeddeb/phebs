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
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// fixturePackage lays out <root>/runtime/phebs and returns root, the selection
// directory and the artifacts directory. withSelection and withArtifacts choose
// which packs/ directories exist. The running executable is pointed at the
// fixture for the rest of the test.
func fixturePackage(t *testing.T, withSelection, withArtifacts bool) (root, selection, artifacts string) {
	t.Helper()
	root = t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(runtimeDir, "phebs")
	if err := os.WriteFile(executable, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	selection = filepath.Join(root, releasePackageSelectionDir)
	artifacts = filepath.Join(root, releasePackageArtifactsDir)
	if withSelection {
		if err := os.MkdirAll(selection, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if withArtifacts {
		if err := os.MkdirAll(artifacts, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pointExecutable(t, executable)
	return root, selection, artifacts
}

func pointExecutable(t *testing.T, path string) {
	t.Helper()
	restore := releasePackageExecutable
	releasePackageExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { releasePackageExecutable = restore })
}

func trustAnchors(t *testing.T, keys ...config.ReleaseKey) {
	t.Helper()
	restore := releaseAnchors
	releaseAnchors = keys
	t.Cleanup(func() { releaseAnchors = restore })
}

// recordingBindings binds release's identity like bindReleaseLoad and records
// the artifacts directory startup asked to census.
func recordingBindings(t *testing.T, release *packrelease.PackRelease) *string {
	t.Helper()
	bindReleaseLoad(t, release)
	bound := releaseLoadBindings
	seen := new(string)
	releaseLoadBindings = func(ctx context.Context, path string) (packrelease.Options, error) {
		*seen = path
		return bound(ctx, path)
	}
	return seen
}

func TestPackageDefaultAdmitsShippedSelection(t *testing.T) {
	_, selection, artifacts := fixturePackage(t, true, true)
	public, release := writeReleasedRecord(t, selection, "phebs.package.pack", "package-key")
	trustAnchors(t, config.ReleaseKey{ID: "package-key", PublicKey: public})
	seen := recordingBindings(t, release)
	bindRecipes(t, map[string][]extract.Extractor{
		"phebs.package.pack": {stubExtractor{domain: "package", version: "1"}},
	})

	var extractors []extract.Extractor
	var err error
	output := captureLogDuring(t, func() { extractors, _, err = releasedExtractors(t.Context(), &config.Config{}) })
	if err != nil {
		t.Fatalf("releasedExtractors: %v", err)
	}
	if len(extractors) != 1 || extractors[0].Domain() != "package" {
		t.Fatalf("package default must admit the shipped recipe, got %#v", extractors)
	}
	if *seen != artifacts {
		t.Fatalf("artifacts census = %q, want package %q", *seen, artifacts)
	}
	if !strings.Contains(output, "package default "+selection) {
		t.Fatalf("startup must name the package default, log = %q", output)
	}
}

func TestPackageDefaultFollowsInstalledSymlink(t *testing.T) {
	root, selection, _ := fixturePackage(t, true, true)
	link := filepath.Join(t.TempDir(), "phebs")
	if err := os.Symlink(filepath.Join(root, "runtime", "phebs"), link); err != nil {
		t.Fatal(err)
	}
	pointExecutable(t, link)
	public, release := writeReleasedRecord(t, selection, "phebs.package.pack", "package-key")
	trustAnchors(t, config.ReleaseKey{ID: "package-key", PublicKey: public})
	bindReleaseLoad(t, release)
	bindRecipes(t, map[string][]extract.Extractor{
		"phebs.package.pack": {stubExtractor{domain: "package", version: "1"}},
	})

	extractors, _, err := releasedExtractors(t.Context(), &config.Config{})
	if err != nil || len(extractors) != 1 {
		t.Fatalf("a symlinked install must find its package: %d extractors, %v", len(extractors), err)
	}
}

func TestPackageDefaultAbsentLayoutStaysDark(t *testing.T) {
	fixturePackage(t, false, false)
	extractors, claims, err := releasedExtractors(t.Context(), &config.Config{})
	if err != nil || extractors != nil || claims != nil {
		t.Fatalf("a build without packs/ must stay dark: %v %v %v", extractors, claims, err)
	}
}

func TestPackageDefaultOffStaysDarkWithoutReading(t *testing.T) {
	_, selection, _ := fixturePackage(t, true, true)
	public, _ := writeReleasedRecord(t, selection, "phebs.package.pack", "package-key")
	trustAnchors(t, config.ReleaseKey{ID: "package-key", PublicKey: public})
	pointExecutable(t, "")
	releasePackageExecutable = func() (string, error) {
		t.Fatal("package_default: off must not resolve the executable")
		return "", nil
	}
	cfg := &config.Config{}
	cfg.ReleaseSelection.PackageDefault = config.ReleasePackageDefaultOff
	extractors, _, err := releasedExtractors(t.Context(), cfg)
	if err != nil || extractors != nil {
		t.Fatalf("package_default: off must stay dark: %v %v", extractors, err)
	}
}

func TestPackageDefaultRevocationWithdraws(t *testing.T) {
	_, selection, _ := fixturePackage(t, true, true)
	public, release := writeReleasedRecord(t, selection, "phebs.package.pack", "package-key")
	trustAnchors(t, config.ReleaseKey{ID: "package-key", PublicKey: public})
	bindReleaseLoad(t, release)
	bindRecipes(t, map[string][]extract.Extractor{
		"phebs.package.pack": {stubExtractor{domain: "package", version: "1"}},
	})
	cfg := &config.Config{}
	cfg.ReleaseSelection.Revoked = []string{release.ReleaseID}

	var extractors []extract.Extractor
	var err error
	output := captureLogDuring(t, func() { extractors, _, err = releasedExtractors(t.Context(), cfg) })
	if err != nil || extractors != nil {
		t.Fatalf("a revoked package record must withdraw its pack: %v %v", extractors, err)
	}
	if !strings.Contains(output, "phebs.package.pack=revoked") {
		t.Fatalf("withdrawal must be logged, log = %q", output)
	}
}

func TestPackageDefaultRefusesUntrustedSigner(t *testing.T) {
	_, selection, _ := fixturePackage(t, true, true)
	_, release := writeReleasedRecord(t, selection, "phebs.package.pack", "foreign-key")
	other, _ := writeReleasedRecord(t, t.TempDir(), "phebs.other.pack", "package-key")
	trustAnchors(t, config.ReleaseKey{ID: "package-key", PublicKey: other})
	bindReleaseLoad(t, release)
	if _, _, err := releasedExtractors(t.Context(), &config.Config{}); err == nil {
		t.Fatal("a package record signed outside the in-tree anchor must refuse startup")
	}
}

func TestPackageDefaultRefusesTamperedRecord(t *testing.T) {
	_, selection, _ := fixturePackage(t, true, true)
	public, release := writeReleasedRecord(t, selection, "phebs.package.pack", "package-key")
	trustAnchors(t, config.ReleaseKey{ID: "package-key", PublicKey: public})
	bindReleaseLoad(t, release)
	path := filepath.Join(selection, "phebs.package.pack.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), "2999-12-31T23:59:59Z", "2999-12-31T23:59:58Z", 1)
	if tampered == string(raw) {
		t.Fatal("fixture record did not contain its expiry")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := releasedExtractors(t.Context(), &config.Config{}); err == nil {
		t.Fatal("a tampered package record must refuse startup")
	}
}

func TestPackageDefaultRefusesSelectionWithoutArtifacts(t *testing.T) {
	fixturePackage(t, true, false)
	if _, _, err := releasedExtractors(t.Context(), &config.Config{}); err == nil {
		t.Fatal("packs/selection without packs/artifacts must refuse startup")
	}
}

func TestPackageDefaultRefusesSymlinkedSelection(t *testing.T) {
	root, _, _ := fixturePackage(t, false, true)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, releasePackageSelectionDir)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := releasedExtractors(t.Context(), &config.Config{}); err == nil {
		t.Fatal("a symlinked packs/selection must refuse startup")
	}
}

func TestConfiguredPathOverridesPackageDefault(t *testing.T) {
	_, packageSelection, _ := fixturePackage(t, true, true)
	_, _ = writeReleasedRecord(t, packageSelection, "phebs.package.pack", "package-key")
	trustAnchors(t) // an empty anchor would refuse the package record if it were read
	configured := t.TempDir()
	public, release := writeReleasedRecord(t, configured, "phebs.bound.pack", "key-1")
	bindReleaseLoad(t, release)
	bindRecipes(t, map[string][]extract.Extractor{
		"phebs.bound.pack": {stubExtractor{domain: "bound", version: "1"}},
	})
	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = configured
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
	extractors, _, err := releasedExtractors(t.Context(), cfg)
	if err != nil || len(extractors) != 1 || extractors[0].Domain() != "bound" {
		t.Fatalf("a configured path must govern over the package: %#v %v", extractors, err)
	}
}

func TestReleaseAnchorsParse(t *testing.T) {
	ring, err := releaseKeyRing(releaseAnchors)
	if err != nil {
		t.Fatalf("in-tree release anchor: %v", err)
	}
	for _, id := range []string{"ben-phebs-release-2026-10", "phebs-release-2026-11"} {
		if _, ok := ring[id]; !ok {
			t.Fatalf("in-tree release anchor lacks %q", id)
		}
	}
}

// TestPackageDefaultAdmitsReleaseSignedPackage runs the release path end to end
// on a fixture package: release-sign writes the record into packs/selection for
// the package's runtime/phebs, and an empty configuration then admits it through
// the real artifact census and executable digest, with only the in-tree anchor
// trusted. Swapping one artifact byte afterwards refuses startup.
func TestPackageDefaultAdmitsReleaseSignedPackage(t *testing.T) {
	root, selection, artifacts := fixturePackage(t, true, false)
	executable := writeReleaseExecutable(t, filepath.Join(root, "runtime"), "phebs", "package-binary", 0o700)
	pointExecutable(t, executable)
	writeCensusArtifacts(t, artifacts, censusContents)
	facts := releaseFacts{executable: executable, goVersion: "go1.26.5", sourceCommit: strings.Repeat("b", 40)}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	template := packrelease.PackRelease{
		ReleaseSchemaVersion: packrelease.ReleaseSchemaVersion, ReleaseID: "rel-wire-package", ReleaseVersion: "1.0.0",
		PackID: wireCompatibilityPackID, PackClaimVersion: "1.0.0",
		Card:          packrelease.ArtifactRef{ArtifactID: "card.json"},
		Manifest:      packrelease.ArtifactRef{ArtifactID: "manifest.json"},
		Validation:    packrelease.Validation{ArtifactID: "validation.json", Applies: true, ExpiresAt: "2999-12-31T23:59:59Z"},
		DerivedStatus: packrelease.StatusReleased, ApprovedAt: "2026-11-22T00:00:00Z", ApprovalRecords: []string{"approval-package"},
	}
	raw, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(t.TempDir(), "template.json")
	if err := os.WriteFile(templatePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	public, err := signRelease(t.Context(), facts, templatePath, keyPath, "phebs-release-test",
		artifacts, filepath.Join(selection, "wire.json"))
	if err != nil {
		t.Fatalf("signRelease: %v", err)
	}
	trustAnchors(t, config.ReleaseKey{ID: "phebs-release-test", PublicKey: base64.StdEncoding.EncodeToString(public)})
	restore := releaseLoadBindings
	releaseLoadBindings = func(_ context.Context, path string) (packrelease.Options, error) {
		return releaseLoadBindingsFor(path, facts)
	}
	t.Cleanup(func() { releaseLoadBindings = restore })

	_, claims, err := releasedExtractors(t.Context(), &config.Config{})
	if err != nil {
		t.Fatalf("an empty configuration must admit the signed package: %v", err)
	}
	if !claims[wireCompatibilityPackID] {
		t.Fatalf("claims = %v, want %s admitted from the package", claims, wireCompatibilityPackID)
	}

	card := filepath.Join(artifacts, "card.json")
	if err := os.WriteFile(card, []byte("altered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := releasedExtractors(t.Context(), &config.Config{}); err == nil {
		t.Fatal("an altered package artifact must refuse startup")
	}
}
