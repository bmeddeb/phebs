package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/packrelease"
)

// swapPackRecipes installs a recipe registry for the rest of the test and
// restores the shipping registry afterwards.
func swapPackRecipes(t *testing.T, recipes map[string]func() []extract.Extractor) {
	t.Helper()
	restore := packRecipes
	packRecipes = recipes
	t.Cleanup(func() { packRecipes = restore })
}

func cleanBuildInfo(commit string) *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: "go1.26.1",
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: commit},
			{Key: "vcs.modified", Value: "false"},
		},
	}
}

func TestBuildFactsDerivesExactIdentity(t *testing.T) {
	commit := strings.Repeat("a", 40)
	facts, err := buildFacts(cleanBuildInfo(commit), "/usr/local/bin/phebs")
	if err != nil {
		t.Fatalf("buildFacts: %v", err)
	}
	want := releaseFacts{executable: "/usr/local/bin/phebs", goVersion: "go1.26.1", sourceCommit: commit}
	if facts != want {
		t.Fatalf("facts = %#v, want %#v", facts, want)
	}
}

// TestBuildFactsRefusesIncompleteMetadata pins that a binary which cannot
// state the exact commit it was built from refuses rather than naming an
// approximate identity.
func TestBuildFactsRefusesIncompleteMetadata(t *testing.T) {
	commit := strings.Repeat("a", 40)
	cases := map[string]struct {
		info *debug.BuildInfo
		exe  string
	}{
		"no build metadata":     {nil, "/usr/local/bin/phebs"},
		"no toolchain version":  {&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: commit}}}, "/usr/local/bin/phebs"},
		"duplicate setting":     {&debug.BuildInfo{GoVersion: "go1.26.1", Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: commit}, {Key: "vcs.revision", Value: commit}}}, "/usr/local/bin/phebs"},
		"no vcs.revision":       {cleanBuildInfo(""), "/usr/local/bin/phebs"},
		"short revision":        {cleanBuildInfo(commit[:39]), "/usr/local/bin/phebs"},
		"upper-case hex":        {cleanBuildInfo(strings.Repeat("A", 40)), "/usr/local/bin/phebs"},
		"non-hex revision":      {cleanBuildInfo("g" + commit[1:]), "/usr/local/bin/phebs"},
		"modified working tree": {&debug.BuildInfo{GoVersion: "go1.26.1", Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: "true"}}}, "/usr/local/bin/phebs"},
		"empty executable path": {cleanBuildInfo(commit), ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := buildFacts(tc.info, tc.exe); err == nil {
				t.Fatal("incomplete build metadata must refuse")
			}
		})
	}
}

// TestDeriveReleaseLoadBindingsUnboundWithoutArtifactsPath pins the dark
// default: with no artifact directory configured the selection stays unbound
// before any fact is read, so the refusal surfaces downstream as
// unresolved_reference rather than as an error here. A test binary carries no
// vcs.revision, so reading facts first would refuse and this would fail.
func TestDeriveReleaseLoadBindingsUnboundWithoutArtifactsPath(t *testing.T) {
	opts, err := deriveReleaseLoadBindings(context.Background(), "")
	if err != nil {
		t.Fatalf("deriveReleaseLoadBindings: %v", err)
	}
	if opts.Implementation != nil || opts.ReferencedArtifactsRootDigest != "" ||
		opts.Resolver != nil || opts.Keys != nil || opts.Revoked != nil {
		t.Fatalf("an empty artifacts path must stay unbound, got %#v", opts)
	}
}

// TestPackImplementationDigestIsContentIdentity pins the digest's exact byte
// format (one newline-terminated "pack_id domain@version" row per extractor,
// globally sorted) and its order- and content-sensitivity.
func TestPackImplementationDigestIsContentIdentity(t *testing.T) {
	swapPackRecipes(t, nil)
	empty := packImplementationDigest(packRecipes)
	if empty != testReleaseDigest("") {
		t.Fatalf("empty registry digest = %s, want the zero-row digest", empty)
	}

	swapPackRecipes(t, map[string]func() []extract.Extractor{
		"phebs.alpha.pack": func() []extract.Extractor {
			return []extract.Extractor{stubExtractor{domain: "a", version: "1"}}
		},
		"phebs.beta.pack": func() []extract.Extractor {
			return []extract.Extractor{stubExtractor{domain: "b", version: "2"}}
		},
		"phebs.gamma.pack": func() []extract.Extractor {
			return []extract.Extractor{
				stubExtractor{domain: "c", version: "1"},
				stubExtractor{domain: "c", version: "3"},
			}
		},
	})
	want := testReleaseDigest("phebs.alpha.pack a@1\nphebs.beta.pack b@2\n" +
		"phebs.gamma.pack c@1\nphebs.gamma.pack c@3\n")
	for i := 0; i < 20; i++ {
		if got := packImplementationDigest(packRecipes); got != want {
			t.Fatalf("digest %d = %s, want %s (one sorted row per extractor)", i, got, want)
		}
	}
	if empty == want {
		t.Fatal("binding a recipe must change the digest a release has to name")
	}
}

func writeReleaseExecutable(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write executable %s: %v", name, err)
	}
	return path
}

func writeCensusArtifacts(t *testing.T, dir string, contents map[string]string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create artifact directory: %v", err)
	}
	for name, content := range contents {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
	}
}

// censusContents is the artifact set the identity fixtures hash: the card, the
// manifest and the validation, each a single flat file the census admits.
var censusContents = map[string]string{
	"card.json":       "card-content",
	"manifest.json":   "manifest-content",
	"validation.json": "validation-content",
}

// censusRootDigest computes the root the census derives from censusContents
// independently of the census code path: the sorted rows' sha256.
func censusRootDigest() string {
	return testReleaseDigest("card.json " + testReleaseDigest("card-content") + "\n" +
		"manifest.json " + testReleaseDigest("manifest-content") + "\n" +
		"validation.json " + testReleaseDigest("validation-content") + "\n")
}

// TestReleaseLoadBindingsForDerivesEveryField pins each binding to the present
// fact it follows from: the executable's own digest, the toolchain digest, the
// in-tree registry digest and the census root of the bytes actually present.
func TestReleaseLoadBindingsForDerivesEveryField(t *testing.T) {
	swapPackRecipes(t, nil)
	tmp := t.TempDir()
	executable := writeReleaseExecutable(t, tmp, "phebs-under-test", "binary-bytes", 0o700)
	artifacts := filepath.Join(tmp, "artifacts")
	writeCensusArtifacts(t, artifacts, censusContents)

	facts := releaseFacts{executable: executable, goVersion: "go1.26.1", sourceCommit: strings.Repeat("a", 40)}
	opts, err := releaseLoadBindingsFor(artifacts, facts)
	if err != nil {
		t.Fatalf("releaseLoadBindingsFor: %v", err)
	}
	if opts.Implementation == nil {
		t.Fatal("bindings must carry the derived implementation identity")
	}
	if opts.Implementation.PhebsSourceCommit != facts.sourceCommit {
		t.Fatalf("PhebsSourceCommit = %q, want %q", opts.Implementation.PhebsSourceCommit, facts.sourceCommit)
	}
	if want := testReleaseDigest("binary-bytes"); opts.Implementation.PhebsBinaryDigest != want {
		t.Fatalf("PhebsBinaryDigest = %s, want the executable's own digest %s", opts.Implementation.PhebsBinaryDigest, want)
	}
	if want := testReleaseDigest("go-toolchain go1.26.1\n"); opts.Implementation.ToolchainDigest != want {
		t.Fatalf("ToolchainDigest = %s, want %s", opts.Implementation.ToolchainDigest, want)
	}
	if want := testReleaseDigest(""); opts.Implementation.PackImplementationDigest != want {
		t.Fatalf("PackImplementationDigest = %s, want the empty-registry digest %s", opts.Implementation.PackImplementationDigest, want)
	}
	if want := censusRootDigest(); opts.ReferencedArtifactsRootDigest != want {
		t.Fatalf("ReferencedArtifactsRootDigest = %s, want %s", opts.ReferencedArtifactsRootDigest, want)
	}
	if opts.Resolver == nil {
		t.Fatal("bindings must carry the census resolver")
	}
	if digest, ok, err := opts.Resolver.Resolve(context.Background(), "card.json"); err != nil || !ok || digest != testReleaseDigest("card-content") {
		t.Fatalf("resolve card.json = (%q, %v, %v), want the census digest", digest, ok, err)
	}
	if _, ok, err := opts.Resolver.Resolve(context.Background(), "absent.json"); err != nil || ok {
		t.Fatalf("resolve absent.json = (ok=%v, err=%v), want a clean absence", ok, err)
	}
}

// TestReleaseLoadBindingsForRefusesUnstableInputs pins the fail-closed side:
// a binding is derived only from a stable executable and a present census.
func TestReleaseLoadBindingsForRefusesUnstableInputs(t *testing.T) {
	tmp := t.TempDir()
	stable := writeReleaseExecutable(t, tmp, "phebs-stable", "binary-bytes", 0o700)
	noExec := writeReleaseExecutable(t, tmp, "phebs-noexec", "binary-bytes", 0o600)
	symlink := filepath.Join(tmp, "phebs-symlink")
	if err := os.Symlink(stable, symlink); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	artifacts := filepath.Join(tmp, "artifacts")
	writeCensusArtifacts(t, artifacts, censusContents)
	facts := func(exe string) releaseFacts {
		return releaseFacts{executable: exe, goVersion: "go1.26.1", sourceCommit: strings.Repeat("a", 40)}
	}

	cases := map[string]struct {
		artifactsPath string
		facts         releaseFacts
	}{
		"non-executable file":       {artifacts, facts(noExec)},
		"symlinked executable":      {artifacts, facts(symlink)},
		"absent executable":         {artifacts, facts(filepath.Join(tmp, "phebs-missing"))},
		"absent artifact directory": {filepath.Join(tmp, "absent-artifacts"), facts(stable)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := releaseLoadBindingsFor(tc.artifactsPath, tc.facts); err == nil {
				t.Fatal("unstable present fact must refuse rather than bind approximately")
			}
		})
	}
}

// writeFactsBoundRecord signs a released record whose implementation identity,
// referenced-artifacts root and artifact digests are exactly the ones derived
// from present fact, into dir, and returns the base64 public key that admits it.
func writeFactsBoundRecord(t *testing.T, dir, packID, keyID string, implementation packrelease.Implementation, root string) string {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	release := &packrelease.PackRelease{
		ReleaseSchemaVersion:          packrelease.ReleaseSchemaVersion,
		ReleaseID:                     "rel-" + packID,
		ReleaseVersion:                "1.0.0",
		PackID:                        packID,
		PackClaimVersion:              "1.0.0",
		Card:                          packrelease.ArtifactRef{ArtifactID: "card.json", Digest: testReleaseDigest("card-content")},
		Manifest:                      packrelease.ArtifactRef{ArtifactID: "manifest.json", Digest: testReleaseDigest("manifest-content")},
		Implementation:                implementation,
		ReferencedArtifactsRootDigest: root,
		Validation: packrelease.Validation{
			ArtifactID: "validation.json",
			Digest:     testReleaseDigest("validation-content"),
			Applies:    true,
			ExpiresAt:  "2999-12-31T23:59:59Z",
		},
		DerivedStatus:   packrelease.StatusReleased,
		ApprovedAt:      "2026-07-17T20:00:00Z",
		ApprovalRecords: []string{"approval-" + packID},
	}
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
	return base64.StdEncoding.EncodeToString(public)
}

// identityFixture derives the bindings a signed record must match and wires
// releaseLoadBindings to rederive them at admission, asserting the call site
// threads the configured artifact directory through.
func identityFixture(t *testing.T, facts releaseFacts) (artifactsPath string, opts packrelease.Options) {
	t.Helper()
	tmp := t.TempDir()
	executable := writeReleaseExecutable(t, tmp, "phebs-under-test", "binary-bytes", 0o700)
	facts.executable = executable
	artifactsPath = filepath.Join(tmp, "artifacts")
	writeCensusArtifacts(t, artifactsPath, censusContents)
	opts, err := releaseLoadBindingsFor(artifactsPath, facts)
	if err != nil {
		t.Fatalf("releaseLoadBindingsFor: %v", err)
	}
	restore := releaseLoadBindings
	releaseLoadBindings = func(_ context.Context, path string) (packrelease.Options, error) {
		if path != artifactsPath {
			return packrelease.Options{}, fmt.Errorf("bindings derived for %q, want %q", path, artifactsPath)
		}
		return releaseLoadBindingsFor(path, facts)
	}
	t.Cleanup(func() { releaseLoadBindings = restore })
	return artifactsPath, opts
}

// TestReleasedExtractorsAdmitsRecordBoundToPresentFacts is the end-to-end
// chain: a signed record naming the machine-derived implementation identity,
// artifacts root and artifact digests is admitted and its pack recipe loads.
// The facts are synthetic on purpose: a test binary carries no vcs.revision.
func TestReleasedExtractorsAdmitsRecordBoundToPresentFacts(t *testing.T) {
	facts := releaseFacts{goVersion: "go1.26.1", sourceCommit: strings.Repeat("a", 40)}
	const packID = "phebs.identity.pack"
	swapPackRecipes(t, map[string]func() []extract.Extractor{
		packID: func() []extract.Extractor {
			return []extract.Extractor{stubExtractor{domain: "identity", version: "1"}}
		},
	})
	artifactsPath, opts := identityFixture(t, facts)

	recordDir := t.TempDir()
	public := writeFactsBoundRecord(t, recordDir, packID, "key-1", *opts.Implementation, opts.ReferencedArtifactsRootDigest)

	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = recordDir
	cfg.ReleaseSelection.ArtifactsPath = artifactsPath
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}

	extractors, err := releasedExtractors(context.Background(), cfg)
	if err != nil {
		t.Fatalf("releasedExtractors: %v", err)
	}
	if len(extractors) != 1 || extractors[0].Domain() != "identity" || extractors[0].Version() != "1" {
		t.Fatalf("expected the bound recipe's extractor, got %#v", extractors)
	}
}

// TestReleasedExtractorsRefusesRecordForAnotherBinary pins that a record
// signed for a different binary identity is refused: every implementation
// field is compared, so naming another source commit cannot bind.
func TestReleasedExtractorsRefusesRecordForAnotherBinary(t *testing.T) {
	facts := releaseFacts{goVersion: "go1.26.1", sourceCommit: strings.Repeat("a", 40)}
	const packID = "phebs.identity.pack"
	swapPackRecipes(t, map[string]func() []extract.Extractor{
		packID: func() []extract.Extractor {
			return []extract.Extractor{stubExtractor{domain: "identity", version: "1"}}
		},
	})
	artifactsPath, opts := identityFixture(t, facts)

	mismatched := *opts.Implementation
	mismatched.PhebsSourceCommit = strings.Repeat("b", 40)
	recordDir := t.TempDir()
	public := writeFactsBoundRecord(t, recordDir, packID, "key-1", mismatched, opts.ReferencedArtifactsRootDigest)

	cfg := &config.Config{}
	cfg.ReleaseSelection.Path = recordDir
	cfg.ReleaseSelection.ArtifactsPath = artifactsPath
	cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}

	_, err := releasedExtractors(context.Background(), cfg)
	if reason, _ := packrelease.ReasonOf(err); reason != packrelease.ReasonDigestMismatch {
		t.Fatalf("reason = %q (%v), want %q", reason, err, packrelease.ReasonDigestMismatch)
	}
}

func TestReleasedExtractorsWithdrawalNeedsNoBuildOrArtifactFacts(t *testing.T) {
	cases := []struct {
		name, status, cause string
		revoked, expired    bool
	}{
		{name: "suspended", status: packrelease.StatusSuspended, cause: "suspended"},
		{name: "retired", status: packrelease.StatusRetired, cause: "retired"},
		{name: "revoked", status: packrelease.StatusReleased, cause: "revoked", revoked: true},
		{name: "expired", status: packrelease.StatusReleased, cause: "expired", expired: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			public, record := writeShapedRecord(t, dir, "phebs.withdrawn", "key-1", func(record *packrelease.PackRelease) {
				record.DerivedStatus = tc.status
				if tc.expired {
					record.ApprovedAt = "2019-07-17T20:00:00Z"
					record.Validation.ExpiresAt = "2020-01-01T00:00:00Z"
				}
			})
			cfg := &config.Config{}
			cfg.ReleaseSelection.Path = dir
			cfg.ReleaseSelection.ArtifactsPath = filepath.Join(t.TempDir(), "missing")
			cfg.ReleaseSelection.Keys = []config.ReleaseKey{{ID: "key-1", PublicKey: public}}
			if tc.revoked {
				cfg.ReleaseSelection.Revoked = []string{record.ReleaseID}
			}
			restore := releaseLoadBindings
			releaseLoadBindings = func(context.Context, string) (packrelease.Options, error) {
				t.Error("withdrawal attempted to derive load bindings")
				return packrelease.Options{}, fmt.Errorf("unavailable build and artifact facts")
			}
			t.Cleanup(func() { releaseLoadBindings = restore })
			var extractors []extract.Extractor
			var err error
			output := captureLogDuring(t, func() { extractors, err = releasedExtractors(t.Context(), cfg) })
			if err != nil || len(extractors) != 0 {
				t.Fatalf("withdrawal = %v, %v; want no admission and no refusal", extractors, err)
			}
			want := "pack release selection: 1 configured pack(s) not admitted: phebs.withdrawn=" + tc.cause + "\n"
			if output != want {
				t.Fatalf("withdrawal log = %q, want %q", output, want)
			}
		})
	}
}
