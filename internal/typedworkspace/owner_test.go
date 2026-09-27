//go:build linux

package typedworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func ownerFixture(t *testing.T) (publicationFixture, string, []byte, typedindex.Inventory, OwnerIdentity, OwnerManifest) {
	t.Helper()
	return ownerFixtureInventory(t, false)
}
func ownerFixtureInventory(t *testing.T, whitespace bool) (publicationFixture, string, []byte, typedindex.Inventory, OwnerIdentity, OwnerManifest) {
	t.Helper()
	f := newPublicationFixture(t)
	src, _, inv, _ := fixture(t)
	raw := publicationJSON(t, typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: inv.Files()})
	if whitespace {
		raw = append(append([]byte(" \n"), raw...), '\n')
		var err error
		inv, err = typedindex.DecodeInventory(t.Context(), raw, digest(raw))
		if err != nil {
			t.Fatal(err)
		}
	}
	tool := typedindex.Tool{Version: "0.2.7", Digest: digest([]byte("tool"))}
	profile, err := typedindex.DecodeProfile(t.Context(), publicationJSON(t, typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: inv.Digest(), ImageDigest: digest([]byte("image"))}))
	if err != nil {
		t.Fatal(err)
	}
	auth := typedindex.Authority{Enabled: true, Administrator: true, Source: f.parent.Request().Source, Profile: typedindex.Epoch{Number: 1, Digest: profile.Digest()}, UniverseDigest: f.parent.Request().UniverseDigest}
	request := typedindex.NewRequest(auth.Source, profile, 1, auth.UniverseDigest, "owner")
	oldBundle := f.bundle
	oldPlan := f.plan.Bytes()
	f.parent, err = typedindex.Admit(t.Context(), auth, profile, publicationJSON(t, request))
	if err != nil {
		t.Fatal(err)
	}
	var def typedindex.PackagePlanDefinition
	if err = json.Unmarshal(oldPlan, &def); err != nil {
		t.Fatal(err)
	}
	def.ParentRequestDigest = f.parent.Digest()
	f.plan, err = typedindex.SealPackagePlan(t.Context(), f.parent, def)
	if err != nil {
		t.Fatal(err)
	}
	next, err := typedindex.PlannedSuccessor(t.Context(), f.parent, f.plan.Digest())
	if err != nil {
		t.Fatal(err)
	}
	auth.ParentRequestDigest, auth.PlanDigest = f.parent.Digest(), f.plan.Digest()
	f.execution, err = typedindex.Admit(t.Context(), auth, profile, publicationJSON(t, next))
	if err != nil {
		t.Fatal(err)
	}
	var scip []byte
	for _, name := range oldBundle.Names() {
		if strings.HasSuffix(name, ".scip") {
			scip = oldBundle.Content(name)
		}
	}
	f.bundle, err = typedindex.BuildBundle(t.Context(), f.execution, f.plan, []typedindex.UnitOutcome{{Unit: def.Units[0].ID, State: typedindex.UnitComplete}}, []typedindex.MemberInput{{Name: "a", SCIP: scip}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewOwnerIdentity(f.parent, digest([]byte("chunk")), "private-lease")
	if err != nil {
		t.Fatal(err)
	}
	m, err := CreateOwner(t.Context(), f.dir, ownerProvisionedObservation(t, f.dir), id, OwnerBudget{Bytes: 1 << 20, Inodes: 128}, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	return f, src, raw, inv, id, m
}
func TestOwnerCompletePersistenceAndPin(t *testing.T) {
	f, src, raw, inv, id, m := ownerFixture(t)
	base := f.dir
	attempt := filepath.Join(base, id.RelativeName())
	releaseFresh, lockErr := AcquirePublicationMutation(t.Context(), attempt)
	if lockErr != nil {
		t.Fatal("fresh attempt has no lifecycle guard", lockErr)
	}
	releaseFresh()
	if m.Digest() == "" || m.Identity.AttemptDigest != digest([]byte(id.ChunkIdentity+"\x00private-lease")) {
		t.Fatal("identity")
	}
	disk, err := os.ReadFile(filepath.Join(attempt, ownerManifest))
	if err != nil || strings.Contains(string(disk), "private-lease") {
		t.Fatal("lease leak", err)
	}
	if got, err := LoadOwner(t.Context(), base, id); err != nil || got.Digest() != m.Digest() {
		t.Fatal("fresh reopen", err)
	}
	if _, _, err := LoadOwnerInputs(t.Context(), base, id); err == nil {
		t.Fatal("absent input guessed ready")
	}
	receipt, err := Copy(t.Context(), src, attempt, inv, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOwner(t.Context(), base, id); err == nil {
		t.Fatal("unreferenced child not held")
	}
	next, err := SaveOwnerInputs(t.Context(), base, id, m.Digest(), raw, receipt, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveOwnerInputs(t.Context(), base, id, m.Digest(), raw, receipt, f.gate); err == nil {
		t.Fatal("stale CAS accepted")
	}
	if _, got, err := LoadOwnerInputs(t.Context(), base, id); err != nil || got.Name != receipt.Name {
		t.Fatal("input reopen", err)
	}
	f.dir = attempt
	pub := f.install(t)
	last, err := SaveOwnerPublication(t.Context(), base, id, next.Digest(), f.parent, f.execution, pub, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	if last.Revision != 3 {
		t.Fatal("revision")
	}
	opened, err := OpenOwnerPublication(t.Context(), base, id, f.parent, f.execution, f.plan.Digest(), f.bundle.RootDigest())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	// A retained child reader does not retain the global owner namespace pin.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := AcquirePublicationMutation(ctx, base)
	if err != nil {
		t.Fatal("reader holds namespace lock", err)
	}
	release()
	blocked, cancelBlocked := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancelBlocked()
	if release, err = AcquirePublicationMutation(blocked, attempt); err == nil {
		release()
		t.Fatal("reader lost attempt pin")
	}
	entries, err := CensusOwners(t.Context(), base, id.PlanningDigest)
	if err != nil || len(entries) != 1 || entries[0].Held {
		t.Fatal(entries, err)
	}
	// An equal-byte inode replacement is still refused.
	path := filepath.Join(attempt, "input-receipt.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, bytes, 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOwner(t.Context(), base, id); err == nil {
		t.Fatal("substituted receipt inode admitted")
	}
	entries, err = CensusOwners(t.Context(), base, id.PlanningDigest)
	if err != nil || len(entries) != 1 || !entries[0].Held {
		t.Fatal("census concealed swapped control", entries, err)
	}
}
func TestOwnerCrashAndIdentityRefusals(t *testing.T) {
	for _, kind := range []string{"pending", "unreferenced-control", "stage", "bad-json", "missing", "inode", "wrong-request", "unsafe-lock"} {
		t.Run(kind, func(t *testing.T) {
			f, _, _, _, id, _ := ownerFixture(t)
			path := filepath.Join(f.dir, id.RelativeName())
			switch kind {
			case "unsafe-lock":
				if err := os.WriteFile(filepath.Join(path, publicationLock), []byte("unexpected"), 0600); err != nil {
					t.Fatal(err)
				}
			case "pending":
				if err := os.WriteFile(filepath.Join(path, ownerPending), []byte("partial"), 0444); err != nil {
					t.Fatal(err)
				}
			case "unreferenced-control":
				if err := os.WriteFile(filepath.Join(path, "inventory.json"), []byte("{}"), 0444); err != nil {
					t.Fatal(err)
				}
			case "stage":
				if err := os.Mkdir(filepath.Join(path, "inputs-"+strings.Repeat("a", 32)+".stage"), 0700); err != nil {
					t.Fatal(err)
				}
			case "bad-json":
				if err := os.Chmod(filepath.Join(path, ownerManifest), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, ownerManifest), []byte("{}"), 0444); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(filepath.Join(path, ownerManifest)); err != nil {
					t.Fatal(err)
				}
			case "inode":
				if err := os.Rename(path, path+"-held"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(filepath.Join(path+"-held", ownerManifest))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(path, ownerManifest), raw, 0444); err != nil {
					t.Fatal(err)
				}
			case "wrong-request":
				id.Request.Source.Commit = strings.Repeat("b", 40)
			}
			if _, err := LoadOwner(t.Context(), f.dir, id); err == nil {
				t.Fatal("crash or mismatch admitted")
			}
		})
	}
}
func TestOwnerNamespaceBoundsAndHeldCensus(t *testing.T) {
	f, _, _, _, id, m := ownerFixture(t)
	req := filepath.Join(f.dir, id.PlanningDigest[7:])
	for i := 1; i < MaxOwnerAttempts; i++ {
		if err := os.Mkdir(filepath.Join(req, fmt.Sprintf("orphan-%02d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	next, err := NewOwnerIdentity(f.parent, id.ChunkIdentity, "next-lease")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CreateOwner(t.Context(), f.dir, ownerProvisionedObservation(t, f.dir), next, m.Budget, f.gate); err == nil {
		t.Fatal("orphan attempt cap bypass")
	}
	entries, err := CensusOwners(t.Context(), f.dir, id.PlanningDigest)
	if err != nil || len(entries) != MaxOwnerAttempts {
		t.Fatal(len(entries), err)
	}
	held := 0
	for _, e := range entries {
		if e.Held {
			held++
		}
	}
	if held != MaxOwnerAttempts-1 {
		t.Fatal("lost orphan census", held)
	}
	if err = os.Mkdir(filepath.Join(req, strings.Repeat("f", 64)), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(req, strings.Repeat("e", 64)), 0700); err != nil {
		t.Fatal(err)
	}
	entries, err = CensusOwners(t.Context(), f.dir, id.PlanningDigest)
	if err == nil || len(entries) != MaxOwnerAttempts+1 {
		t.Fatal("overflow not held", len(entries), err)
	}
	// Request names need not be valid to consume the admission ceiling.
	for i := 1; i < MaxOwnerRequests; i++ {
		if err = os.Mkdir(filepath.Join(f.dir, fmt.Sprintf("held-%04d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	other := newPublicationFixture(t)
	otherID, e := NewOwnerIdentity(other.parent, id.ChunkIdentity, "other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = CreateOwner(t.Context(), f.dir, ownerProvisionedObservation(t, f.dir), otherID, m.Budget, f.gate); e == nil {
		t.Fatal("full request namespace accepted another root")
	}
	if _, e = os.Stat(filepath.Join(f.dir, otherID.PlanningDigest[7:])); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("request cap grew root", e)
	}
	// Existing request is full; changing only lease still refuses before mutation.
	if _, err = CreateOwner(t.Context(), f.dir, ownerProvisionedObservation(t, f.dir), next, m.Budget, f.gate); err == nil {
		t.Fatal("full namespaces accepted")
	}
	if err = os.Mkdir(filepath.Join(f.dir, "overflow"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(f.dir, "second-overflow"), 0700); err != nil {
		t.Fatal(err)
	}
	entries, err = CensusOwners(t.Context(), f.dir, "")
	if err == nil || len(entries) != MaxOwnerRequests+1 {
		t.Fatal("request overflow hidden", len(entries), err)
	}
}
func TestOwnerCapacityAndCancellationBeforeGrowth(t *testing.T) {
	f, _, _, _, id, m := ownerFixture(t)
	next, err := NewOwnerIdentity(f.parent, id.ChunkIdentity, "new-lease")
	if err != nil {
		t.Fatal(err)
	}
	gate := lifecycle.NewGate(f.dir)
	for _, tc := range []struct {
		percent int
		inodes  uint64
		ok      bool
	}{{100, 1000, false}, {85, 1000, false}, {73, 1000, true}, {95, 1000, false}, {85, 1000, false}, {73, 0, false}} {
		total := uint64(1 << 30)
		probe := func(*os.File) (space, error) {
			return space{total: total, bytes: total * uint64(100-tc.percent) / 100, inodes: tc.inodes, block: 4096}, nil
		}
		name := next.RelativeName()
		_, err = createOwner(t.Context(), f.dir, ownerProvisionedObservation(t, f.dir), next, m.Budget, gate, probe)
		if (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
		if !tc.ok {
			if _, e := os.Stat(filepath.Join(f.dir, name)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("refusal grew attempt", e)
			}
		} else {
			next, _ = NewOwnerIdentity(f.parent, id.ChunkIdentity, "next-after-success")
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CreateOwner(canceled, f.dir, ownerProvisionedObservation(t, f.dir), next, m.Budget, gate); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, next.RelativeName())); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel grew attempt")
	}
}
func TestOwnerReceiptBindingAndStrictDecode(t *testing.T) {
	f, src, raw, inv, id, m := ownerFixture(t)
	attempt := filepath.Join(f.dir, id.RelativeName())
	receipt, err := Copy(t.Context(), src, attempt, inv, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte("{}"), append(append([]byte{}, raw...), ' ')} {
		if _, err := SaveOwnerInputs(t.Context(), f.dir, id, m.Digest(), bad, receipt, f.gate); err == nil {
			t.Fatal("wrong inventory accepted")
		}
	}
	good, err := encodeOwnerInputs(t.Context(), inv, receipt)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := decodeOwnerInputs(canceled, inv, good); !errors.Is(err, context.Canceled) {
		t.Fatalf("receipt decode cancellation lost: %v", err)
	}
	for _, bad := range [][]byte{append(append([]byte{}, good...), ' '), []byte(strings.Replace(string(good), `"schema":`, `"extra":0,"schema":`, 1)), []byte(strings.Replace(string(good), `"inode":`, `"inode":0,"inode":`, 1))} {
		if _, err := decodeOwnerInputs(t.Context(), inv, bad); err == nil {
			t.Fatal("noncanonical receipt")
		}
	}
	if err = os.Chmod(filepath.Join(attempt, receipt.Name, "data"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveOwnerInputs(t.Context(), f.dir, id, m.Digest(), raw, receipt, f.gate); err == nil {
		t.Fatal("mutable input recorded")
	}
	if _, err := os.Stat(filepath.Join(attempt, "inventory.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed verify wrote control")
	}
}

func TestOwnerUpdateChargesOnlyIncrementalControls(t *testing.T) {
	for _, tc := range []struct {
		name         string
		free, inodes uint64
		ok           bool
	}{{"no-inodes", 600 << 10, 0, false}, {"increment-only", 600 << 10, 100, true}, {"no-bytes", 8 << 10, 100, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f, src, raw, inv, id, m := ownerFixture(t)
			// Creation promised 1MiB. This actual observation admits the small controls
			// while adding the original whole promise again would exceed capacity.
			probe := func(*os.File) (space, error) {
				return space{total: 2 << 20, bytes: tc.free, block: 4096, inodes: tc.inodes}, nil
			}
			attempt := filepath.Join(f.dir, id.RelativeName())
			receipt, err := Copy(t.Context(), src, attempt, inv, f.gate)
			if err != nil {
				t.Fatal(err)
			}
			_, err = saveOwnerInputs(t.Context(), f.dir, id, m.Digest(), raw, receipt, lifecycle.NewGate(f.dir), probe)
			if !tc.ok {
				if err == nil {
					t.Fatal("no inodes admitted")
				}
				if _, e := os.Stat(filepath.Join(attempt, "inventory.json")); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("refused growth changed files", e)
				}
			} else if err != nil {
				t.Fatal("already allocated bytes counted twice", err)
			}
		})
	}
}

func TestOwnerEnvelopeRefusesBeforeGrowth(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		escaped               int
		initialFits, admitted bool
	}{
		{"ordinary", 0, true, true},
		{"later-receipt-overflow", 300, true, false},
		{"initial-overflow", 510, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, base, _, gate := fixture(t)
			hash := digest([]byte("owner-envelope"))
			tool := typedindex.Tool{Version: "1.0", Digest: hash}
			profile, err := typedindex.DecodeProfile(t.Context(), publicationJSON(t, typedindex.ProfileDefinition{
				Schema: typedindex.ProfileSchema, Name: strings.Repeat("p", 64), Provider: typedindex.ProviderID,
				Tools:  typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool},
				Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: hash, ImageDigest: hash,
			}))
			if err != nil {
				t.Fatal(err)
			}
			source := typedindex.Source{Repository: "x/" + strings.Repeat("<", tc.escaped) + strings.Repeat("a", 510-tc.escaped), Incarnation: strings.Repeat("i", 64), Generation: hash, Commit: strings.Repeat("a", 40)}
			authority := typedindex.Authority{Enabled: true, Administrator: true, Source: source, Profile: typedindex.Epoch{Number: 1, Digest: profile.Digest()}, UniverseDigest: hash}
			request := typedindex.NewRequest(source, profile, 1, hash, strings.Repeat("k", 64))
			admission, err := typedindex.Admit(t.Context(), authority, profile, publicationJSON(t, request))
			if err != nil {
				t.Fatal("request itself must be valid", err)
			}
			id, err := NewOwnerIdentity(admission, hash, "lease")
			if err != nil {
				t.Fatal(err)
			}
			budget := OwnerBudget{Bytes: 1 << 20, Inodes: 128}
			initial := OwnerManifest{Schema: OwnerSchema, Identity: id, Directory: Node{Path: id.RelativeName(), Device: 1, Inode: 1, Directory: true}, Budget: budget, Revision: 1}
			if (initial.Digest() != "") != tc.initialFits {
				t.Fatal("test must distinguish initial encoding from later receipt growth")
			}
			probed := false
			probe := func(f *os.File) (space, error) { probed = true; return capacity(f) }
			m, err := createOwner(t.Context(), base, ownerProvisionedObservation(t, base), id, budget, gate, probe)
			if tc.admitted {
				if err != nil || !probed || m.Digest() == "" {
					t.Fatal("positive owner failed", err)
				}
				if got, err := LoadOwner(t.Context(), base, id); err != nil || got.Digest() != m.Digest() {
					t.Fatal("positive reopen", err)
				}
				return
			}
			if !errors.Is(err, ErrCustody) || probed || m.RelativeName() != "" {
				t.Fatal("late refusal or side effect", err, probed)
			}
			entries, err := os.ReadDir(base)
			if err != nil || len(entries) != 1 || entries[0].Name() != publicationLock {
				t.Fatal("oversized owner grew namespace/lock", entries, err)
			}
			// No filesystem lookup is needed even when base cannot exist.
			if _, err = CreateOwner(t.Context(), filepath.Join(base, "absent"), ownerProvisionedObservation(t, base), id, budget, gate); !errors.Is(err, ErrCustody) {
				t.Fatal("filesystem checked before envelope", err)
			}
		})
	}
}

func ownerProvisionedObservation(t *testing.T, base string) CapacityObservation {
	t.Helper()
	// Explicit neutral fixture provisioning; production CreateOwner never creates
	// the base's lock. Existing locks are preserved, including their inode.
	name := filepath.Join(base, publicationLock)
	if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
		if err = os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := ObserveCapacity(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	return observed
}

func TestOwnerBoundFirstGrowth(t *testing.T) {
	_, _, _, _, id, m := ownerFixture(t)
	for _, tc := range []string{"device", "inode", "block", "nil-gate", "replaced"} {
		t.Run(tc, func(t *testing.T) {
			_, base, _, gate := fixture(t)
			expected := ownerProvisionedObservation(t, base)
			switch tc {
			case "device":
				expected.Device++
			case "inode":
				expected.Inode++
			case "block":
				expected.BlockSize *= 2
			case "nil-gate":
				gate = nil
			case "replaced":
				if err := os.Rename(base, base+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(base); _ = os.Rename(base+"-old", base) })
				if err := os.Mkdir(base, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := CreateOwner(t.Context(), base, expected, id, m.Budget, gate); !errors.Is(err, ErrCustody) {
				t.Fatal("mismatched root admitted", err)
			}
			entries, err := os.ReadDir(base)
			want := 1
			if tc == "replaced" {
				want = 0
			}
			if err != nil || len(entries) != want {
				t.Fatal("refusal grew namespace", entries, err)
			}
		})
	}
}

func TestOwnerBaseReplacementDuringAdmission(t *testing.T) {
	_, _, _, _, id, m := ownerFixture(t)
	_, base, _, gate := fixture(t)
	expected := ownerProvisionedObservation(t, base)
	swapped := false
	probe := func(f *os.File) (space, error) {
		if !swapped {
			swapped = true
			if err := os.Rename(base, base+"-old"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(base); _ = os.Rename(base+"-old", base) })
			if err := os.Mkdir(base, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return capacity(f)
	}
	if _, err := createOwner(t.Context(), base, expected, id, m.Budget, gate, probe); !errors.Is(err, ErrCustody) {
		t.Fatal("changed base admitted", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrong base grew", entries, err)
	}
	old, err := os.ReadDir(base + "-old")
	if err != nil || len(old) != 1 || old[0].Name() != publicationLock {
		t.Fatal("old base grew", old, err)
	}
}

func TestOwnerRequiresProvisionedLock(t *testing.T) {
	_, _, _, _, id, m := ownerFixture(t)
	_, base, _, gate := fixture(t)
	expected, err := ObserveCapacity(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CreateOwner(t.Context(), base, expected, id, m.Budget, gate); !errors.Is(err, ErrCustody) {
		t.Fatal("unprovisioned root admitted", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatal("missing lock was created", entries, err)
	}
}
