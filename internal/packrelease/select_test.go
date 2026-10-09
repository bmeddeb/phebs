package packrelease

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

// writeSigned writes a canonically signed release record into dir under name,
// mutated from the well-formed released baseline.
func writeSigned(t *testing.T, dir, name, keyID string, private ed25519.PrivateKey, mutate func(*PackRelease)) {
	t.Helper()
	release := validRelease()
	if mutate != nil {
		mutate(release)
	}
	if err := Sign(release, keyID, private); err != nil {
		t.Fatalf("Sign(%s): %v", name, err)
	}
	raw, err := CanonicalPayload(release)
	if err != nil {
		t.Fatalf("CanonicalPayload(%s): %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// loadOptions admits records signed by public and binds them to the baseline
// implementation, artifact root and artifacts of validRelease.
func loadOptions(public ed25519.PublicKey) Options {
	baseline := validRelease()
	return Options{
		Now:                           fixedNow,
		Keys:                          KeyRing{"key-1": public},
		Implementation:                &baseline.Implementation,
		ReferencedArtifactsRootDigest: baseline.ReferencedArtifactsRootDigest,
		Resolver: mapResolver{
			baseline.Card.ArtifactID:       baseline.Card.Digest,
			baseline.Manifest.ArtifactID:   baseline.Manifest.Digest,
			baseline.Validation.ArtifactID: baseline.Validation.Digest,
		},
	}
}

func TestLoadSelectionEmptyPathAdmitsNothing(t *testing.T) {
	selection, err := LoadSelection(context.Background(), "", Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("LoadSelection(\"\"): %v", err)
	}
	if !selection.Empty() || selection.Count() != 0 || len(selection.PackIDs()) != 0 {
		t.Fatalf("empty path must admit nothing, got %#v", selection.PackIDs())
	}
}

func TestLoadSelectionAbsentDirectoryRefuses(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := LoadSelection(context.Background(), missing, Options{Now: fixedNow}); err == nil {
		t.Fatal("a configured but absent directory must refuse, not silently admit nothing")
	}
}

func TestLoadSelectionAdmitsOnlyReleased(t *testing.T) {
	dir := t.TempDir()
	public, private := testKey(t)
	writeSigned(t, dir, "released.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.released.pack"
	})
	writeSigned(t, dir, "shadow.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.shadow.pack"
		r.DerivedStatus = StatusShadow
	})
	writeSigned(t, dir, "dark.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.dark.pack"
		r.DerivedStatus = StatusExperimentalDark
	})
	// A non-.json sibling is ignored, not verified.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	selection, err := LoadSelection(context.Background(), dir, loadOptions(public))
	if err != nil {
		t.Fatalf("LoadSelection: %v", err)
	}
	if selection.Count() != 1 {
		t.Fatalf("only the released record must be admitted, got %d: %v", selection.Count(), selection.PackIDs())
	}
	if _, ok := selection.Released("phebs.released.pack"); !ok {
		t.Fatal("released pack not admitted")
	}
	if _, ok := selection.Released("phebs.shadow.pack"); ok {
		t.Fatal("shadow pack must never enter the released admission set")
	}
}

// TestLoadSelectionGoverningRecord: the highest release_version per pack
// governs, so a later suspension withdraws an older release and a later
// release supersedes an older one.
func TestLoadSelectionGoverningRecord(t *testing.T) {
	tests := []struct {
		name    string
		second  func(*PackRelease)
		admits  bool
		version string
	}{
		{"suspension withdraws", func(r *PackRelease) {
			r.ReleaseID, r.ReleaseVersion, r.DerivedStatus = "rel-0002", "1.0.1", StatusSuspended
		}, false, ""},
		{"newer release supersedes", func(r *PackRelease) {
			r.ReleaseID, r.ReleaseVersion = "rel-0002", "1.10.0"
		}, true, "1.10.0"},
		{"older suspension is superseded", func(r *PackRelease) {
			r.ReleaseID, r.ReleaseVersion, r.DerivedStatus = "rel-0002", "0.9.0", StatusSuspended
		}, true, "1.0.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			public, private := testKey(t)
			writeSigned(t, dir, "a.json", "key-1", private, nil)
			writeSigned(t, dir, "b.json", "key-1", private, test.second)
			selection, err := LoadSelection(context.Background(), dir, loadOptions(public))
			if err != nil {
				t.Fatalf("LoadSelection: %v", err)
			}
			release, ok := selection.Released(validRelease().PackID)
			if ok != test.admits || ok && release.ReleaseVersion != test.version {
				t.Fatalf("admitted = %t (%v), want %t at %q", ok, release, test.admits, test.version)
			}
		})
	}
}

func TestLoadSelectionRefuses(t *testing.T) {
	tests := []struct {
		name   string
		build  func(t *testing.T, dir string, private ed25519.PrivateKey)
		opts   func(Options) Options
		reason Reason
	}{
		{"unverifiable record", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "good.json", "key-1", private, nil)
			writeSigned(t, dir, "foreign.json", "key-unknown", private, func(r *PackRelease) {
				r.PackID = "phebs.foreign.pack"
			})
		}, nil, ReasonUnknownKey},
		{"expired released", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "expired.json", "key-1", private, func(r *PackRelease) {
				r.Validation.ExpiresAt = "2020-01-01T00:00:00Z"
			})
		}, nil, ReasonExpired},
		{"same pack and version twice", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "a.json", "key-1", private, nil)
			writeSigned(t, dir, "b.json", "key-1", private, func(r *PackRelease) { r.ReleaseID = "rel-0002" })
		}, nil, ""},
		{"released without load bindings", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "a.json", "key-1", private, nil)
		}, func(o Options) Options {
			o.Implementation = nil
			return o
		}, ReasonUnresolvedReference},
		{"foreign implementation", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "a.json", "key-1", private, func(r *PackRelease) {
				r.Implementation.PhebsBinaryDigest = testDigest("other-binary")
			})
		}, nil, ReasonDigestMismatch},
		{"symlinked record", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			outside := t.TempDir()
			writeSigned(t, outside, "a.json", "key-1", private, nil)
			if err := os.Symlink(filepath.Join(outside, "a.json"), filepath.Join(dir, "a.json")); err != nil {
				t.Fatalf("symlink: %v", err)
			}
		}, nil, ""},
		{"oversized record", func(t *testing.T, dir string, _ ed25519.PrivateKey) {
			if err := os.WriteFile(filepath.Join(dir, "big.json"), make([]byte, 2*MaxReleaseBytes), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
		}, nil, ReasonOversized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			public, private := testKey(t)
			test.build(t, dir, private)
			opts := loadOptions(public)
			if test.opts != nil {
				opts = test.opts(opts)
			}
			_, err := LoadSelection(context.Background(), dir, opts)
			if err == nil {
				t.Fatal("LoadSelection admitted a selection it must refuse")
			}
			if reason, _ := ReasonOf(err); test.reason != "" && reason != test.reason {
				t.Fatalf("reason = %q (%v), want %q", reason, err, test.reason)
			}
		})
	}
}
