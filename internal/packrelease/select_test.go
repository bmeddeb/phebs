package packrelease

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

// writeSigned writes a canonically signed release record into dir under name,
// mutated from the well-formed released baseline, and returns the public key
// half of the signing key so the caller can build the admitting KeyRing.
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

func TestLoadSelectionEmptyPathAdmitsNothing(t *testing.T) {
	selection, err := LoadSelection("", Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("LoadSelection(\"\"): %v", err)
	}
	if !selection.Empty() || selection.Count() != 0 || len(selection.PackIDs()) != 0 {
		t.Fatalf("empty path must admit nothing, got %#v", selection.PackIDs())
	}
}

func TestLoadSelectionAbsentDirectoryRefuses(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := LoadSelection(missing, Options{Now: fixedNow}); err == nil {
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

	selection, err := LoadSelection(dir, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
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

func TestLoadSelectionRefusesInvalidRecord(t *testing.T) {
	dir := t.TempDir()
	public, private := testKey(t)
	writeSigned(t, dir, "good.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.good.pack"
	})
	// A record signed by an unknown key fails Verify and must refuse the whole
	// selection rather than being skipped.
	writeSigned(t, dir, "foreign.json", "key-unknown", private, func(r *PackRelease) {
		r.PackID = "phebs.foreign.pack"
	})

	_, err := LoadSelection(dir, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
	if err == nil {
		t.Fatal("an unverifiable record must refuse the selection")
	}
}

func TestLoadSelectionRefusesExpiredReleased(t *testing.T) {
	dir := t.TempDir()
	public, private := testKey(t)
	writeSigned(t, dir, "expired.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.expired.pack"
		r.Validation.ExpiresAt = "2020-01-01T00:00:00Z"
	})
	_, err := LoadSelection(dir, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
	if err == nil {
		t.Fatal("an expired released record must refuse the selection")
	}
	reason, ok := ReasonOf(err)
	if !ok || reason != ReasonExpired {
		t.Fatalf("expected an expired rejection unwrapping to ErrInvalidRelease, got %v (%v)", err, ok)
	}
}

func TestLoadSelectionRefusesDuplicatePackID(t *testing.T) {
	dir := t.TempDir()
	public, private := testKey(t)
	writeSigned(t, dir, "a.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.dup.pack"
		r.ReleaseID = "rel-a"
	})
	writeSigned(t, dir, "b.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.dup.pack"
		r.ReleaseID = "rel-b"
	})
	if _, err := LoadSelection(dir, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}}); err == nil {
		t.Fatal("two admitted records naming the same pack must refuse the selection")
	}
}
