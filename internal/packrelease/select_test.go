package packrelease

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"reflect"
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

// revokedSet builds the operator revocation list LoadSelection judges against
// the governing record. An empty list yields nil, matching production, which
// allocates nothing when the operator revoked no release.
func revokedSet(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
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

// TestLoadSelectionRevocation pins the operator revocation control. Revocation
// is judged once, against the record that governs its pack, so revoking a
// superseded record withdraws nothing instead of refusing the whole selection,
// and revoking the governing record withdraws the pack instead of silently
// falling back to an older record.
func TestLoadSelectionRevocation(t *testing.T) {
	onlyBaseline := func(t *testing.T, dir string, private ed25519.PrivateKey) {
		writeSigned(t, dir, "a.json", "key-1", private, nil)
	}
	baselinePlusNewer := func(t *testing.T, dir string, private ed25519.PrivateKey) {
		onlyBaseline(t, dir, private)
		writeSigned(t, dir, "b.json", "key-1", private, func(r *PackRelease) {
			r.ReleaseID, r.ReleaseVersion = "rel-0002", "1.10.0"
		})
	}
	baselinePlusSuspension := func(t *testing.T, dir string, private ed25519.PrivateKey) {
		onlyBaseline(t, dir, private)
		writeSigned(t, dir, "b.json", "key-1", private, func(r *PackRelease) {
			r.ReleaseID, r.ReleaseVersion, r.DerivedStatus = "rel-0002", "1.0.1", StatusSuspended
		})
	}
	tests := []struct {
		name     string
		build    func(t *testing.T, dir string, private ed25519.PrivateKey)
		revoked  []string
		admits   bool
		version  string
		withdraw []Withdrawal
	}{
		{
			name:     "revoking the governing record withdraws its pack",
			build:    onlyBaseline,
			revoked:  []string{"rel-0001"},
			withdraw: []Withdrawal{{PackID: validRelease().PackID, Cause: CauseRevoked}},
		},
		{
			name:    "revoking a superseded record withdraws nothing",
			build:   baselinePlusNewer,
			revoked: []string{"rel-0001"},
			admits:  true,
			version: "1.10.0",
		},
		{
			// Revocation carries precedence over the status gate, exactly as
			// Verify orders it, so the operator sees the cause they configured.
			name:     "revocation is the cause even when the governing record is suspended",
			build:    baselinePlusSuspension,
			revoked:  []string{"rel-0002"},
			withdraw: []Withdrawal{{PackID: validRelease().PackID, Cause: CauseRevoked}},
		},
		{
			name:     "revoking the governing record never falls back to an older release",
			build:    baselinePlusNewer,
			revoked:  []string{"rel-0002"},
			withdraw: []Withdrawal{{PackID: validRelease().PackID, Cause: CauseRevoked}},
		},
		{
			name:    "an unrelated revocation withdraws nothing",
			build:   onlyBaseline,
			revoked: []string{"rel-other"},
			admits:  true,
			version: "1.0.0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			public, private := testKey(t)
			test.build(t, dir, private)
			opts := loadOptions(public)
			opts.Revoked = revokedSet(test.revoked)
			selection, err := LoadSelection(context.Background(), dir, opts)
			if err != nil {
				t.Fatalf("LoadSelection: %v", err)
			}
			release, ok := selection.Released(validRelease().PackID)
			if ok != test.admits || ok && release.ReleaseVersion != test.version {
				t.Fatalf("admitted = %t (%v), want %t at %q", ok, release, test.admits, test.version)
			}
			if got := selection.Withdrawn(); !reflect.DeepEqual(got, test.withdraw) {
				t.Fatalf("withdrawn = %#v, want %#v", got, test.withdraw)
			}
		})
	}
}

// TestLoadSelectionWithdrawnNamesEveryUnadmittedPack pins that a mixed
// directory reports each configured-but-unadmitted pack with a bounded
// lifecycle cause, in sorted pack order, without refusing the healthy admission
// beside them: one suspended pack never fails an otherwise good startup.
func TestLoadSelectionWithdrawnNamesEveryUnadmittedPack(t *testing.T) {
	dir := t.TempDir()
	public, private := testKey(t)
	writeSigned(t, dir, "released.json", "key-1", private, func(r *PackRelease) {
		r.PackID = "phebs.admitted.pack"
	})
	writeSigned(t, dir, "suspended.json", "key-1", private, func(r *PackRelease) {
		r.PackID, r.ReleaseID, r.DerivedStatus = "phebs.suspended.pack", "rel-0002", StatusSuspended
	})
	writeSigned(t, dir, "retired.json", "key-1", private, func(r *PackRelease) {
		r.PackID, r.ReleaseID, r.DerivedStatus = "phebs.retired.pack", "rel-0003", StatusRetired
	})
	writeSigned(t, dir, "revoked.json", "key-1", private, func(r *PackRelease) {
		r.PackID, r.ReleaseID = "phebs.revoked.pack", "rel-0004"
	})

	opts := loadOptions(public)
	opts.Revoked = revokedSet([]string{"rel-0004"})
	selection, err := LoadSelection(context.Background(), dir, opts)
	if err != nil {
		t.Fatalf("LoadSelection: %v", err)
	}
	want := []Withdrawal{
		{PackID: "phebs.retired.pack", Cause: StatusRetired},
		{PackID: "phebs.revoked.pack", Cause: CauseRevoked},
		{PackID: "phebs.suspended.pack", Cause: StatusSuspended},
	}
	got := selection.Withdrawn()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withdrawn = %#v, want %#v", got, want)
	}
	if selection.Count() != 1 {
		t.Fatalf("admitted %d pack(s), want exactly the released one: %v", selection.Count(), selection.PackIDs())
	}
	if _, ok := selection.Released("phebs.admitted.pack"); !ok {
		t.Fatal("the released pack beside the withdrawals must still be admitted")
	}
	if _, ok := selection.Released("phebs.revoked.pack"); ok {
		t.Fatal("a revoked pack must never be admitted")
	}
	// Withdrawn copies, so an operator-facing consumer cannot mutate the
	// selection it was handed.
	got[0].Cause = "mutated"
	if again := selection.Withdrawn(); !reflect.DeepEqual(again, want) {
		t.Fatalf("Withdrawn() must copy, second call = %#v", again)
	}
}

// TestLoadSelectionRevocationDoesNotSkipVerification pins that revocation is
// never a shortcut past authenticity. Every record present is still fully
// verified before any of them can govern, so a foreign-keyed record the
// operator revoked refuses the selection with its verification cause rather
// than being quietly withdrawn.
func TestLoadSelectionRevocationDoesNotSkipVerification(t *testing.T) {
	dir := t.TempDir()
	public, private := testKey(t)
	writeSigned(t, dir, "good.json", "key-1", private, nil)
	writeSigned(t, dir, "foreign.json", "key-unknown", private, func(r *PackRelease) {
		r.PackID, r.ReleaseID = "phebs.foreign.pack", "rel-9999"
	})
	opts := loadOptions(public)
	opts.Revoked = revokedSet([]string{"rel-9999"})
	if _, err := LoadSelection(context.Background(), dir, opts); err == nil {
		t.Fatal("a revoked foreign-keyed record must still refuse, not be silently withdrawn")
	} else if reason, _ := ReasonOf(err); reason != ReasonUnknownKey {
		t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnknownKey)
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

// TestLoadSelectionExpiryWithdraws pins validation expiry as automatic
// suspension: an expired or not-yet-approved governing record withdraws its
// pack without refusing startup, and a superseded record expiring later
// changes nothing.
func TestLoadSelectionExpiryWithdraws(t *testing.T) {
	expired := func(r *PackRelease) { r.Validation.ExpiresAt = "2020-01-01T00:00:00Z" }
	tests := []struct {
		name     string
		build    func(t *testing.T, dir string, private ed25519.PrivateKey)
		admits   string
		withdraw []Withdrawal
	}{
		{"expired governing record withdraws", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "a.json", "key-1", private, expired)
		}, "", []Withdrawal{{PackID: validRelease().PackID, Cause: string(ReasonExpired)}}},
		{"future approval withdraws", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "a.json", "key-1", private, func(r *PackRelease) { r.ApprovedAt = "2030-01-01T00:00:00Z" })
		}, "", []Withdrawal{{PackID: validRelease().PackID, Cause: string(ReasonFutureApproval)}}},
		{"expired superseded record changes nothing", func(t *testing.T, dir string, private ed25519.PrivateKey) {
			writeSigned(t, dir, "a.json", "key-1", private, expired)
			writeSigned(t, dir, "b.json", "key-1", private, func(r *PackRelease) { r.ReleaseID, r.ReleaseVersion = "rel-0002", "1.1.0" })
		}, "1.1.0", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			public, private := testKey(t)
			test.build(t, dir, private)
			selection, err := LoadSelection(context.Background(), dir, loadOptions(public))
			if err != nil {
				t.Fatalf("LoadSelection refused startup: %v", err)
			}
			release, ok := selection.Released(validRelease().PackID)
			if ok != (test.admits != "") || ok && release.ReleaseVersion != test.admits {
				t.Fatalf("admitted = %t (%v), want %q", ok, release, test.admits)
			}
			if got := selection.Withdrawn(); !reflect.DeepEqual(got, test.withdraw) {
				t.Fatalf("withdrawn = %#v, want %#v", got, test.withdraw)
			}
		})
	}
}

func TestLoadSelectionDerivesBindingsOnceAndKeepsTrustOptions(t *testing.T) {
	dir := t.TempDir()
	public, private := testKey(t)
	for _, id := range []string{"phebs.a", "phebs.b"} {
		writeSigned(t, dir, id+".json", "key-1", private, func(r *PackRelease) { r.PackID = id })
	}
	opts := loadOptions(public)
	unbound := Options{Keys: opts.Keys, Now: opts.Now}
	calls := 0
	selection, err := LoadSelectionWithBindings(t.Context(), dir, unbound, func(context.Context) (Options, error) {
		calls++
		// An unrelated revocation supplied by the binding must not replace
		// the operator's trust/lifecycle configuration for the second pack.
		bound := opts
		bound.Revoked = map[string]struct{}{validRelease().ReleaseID: {}}
		return bound, nil
	})
	if err != nil || selection.Count() != 2 || calls != 1 {
		t.Fatalf("selection count = %d, calls = %d, error = %v", selection.Count(), calls, err)
	}
}
