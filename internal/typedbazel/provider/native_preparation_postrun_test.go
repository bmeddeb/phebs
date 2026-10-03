package provider

// Phase B of the T45.4 neutral preparation: the host-side identity chain the
// operator reassembles AFTER the one finite native preparation run returned its
// raw cquery/aquery/projection bytes. The run itself is leaf-4.82/4.83; this
// file never executes a container, a build, a driver or an indexer. It
// substitutes the final cmd helper into the inventory the run staged with the
// test helper, seals the run-produced Selection-v1, the FINAL inventory that
// carries it, and the reduced Profile-v1 bound to that inventory, then (only in
// the opt-in live role) installs the profile into an isolated real store,
// exports the pristine seed and verifies the acceptance predicates.
//
// The two identities leaf-4.85 removed from the staged config — the run-produced
// Selection-v1 digest and the real-store profile epoch — are exactly what this
// chain observes and records in the post-run receipt, so a truthful config can
// exist at stage time without fabricating a run output.
//
// The pure seal functions are fixture-tested by TestPreparationPostRunChain on
// every ordinary run. The live store/export/verify entrypoint is opt-in and skips
// by default and requires an isolated engine fixture (execution-plan B4).

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

// preparationSeedCeiling bounds the exported pristine seed. It is the same 4 MiB
// neutral transport ceiling the protocol fixes for the official engine export; it
// is never a change to a production backup bound.
const preparationSeedCeiling int64 = 4 << 20

// preparationExportOutputCeiling bounds captured export child stdout/stderr so a
// verbose or looping engine cannot grow host memory without limit.
const preparationExportOutputCeiling = 64 << 10

const preparationSeedFile = "seed.surql"
const preparationPostRunFile = "postrun.json"
const preparationPostRunReceiptSchema = "phebs-typed-native-preparation-postrun-v1"

// preparationNativeDriverPath is the second inventory path that must carry the
// managed helper identity. Production verifyTools requires the helper digest at
// BOTH this path and typedindex.ManagedHelperFile; the literal mirrors tools.go.
const preparationNativeDriverPath = "tools/bin/phebs-t451b-native-driver"

// The neutral preparation store namespace/database and the exact derived-table
// exclusion set. The exclusion mirrors recovery.derivedExportTables: typed leases
// and restartable generation controls regenerate on restore and must never ride
// through the pristine seed as generic tables.
const preparationExportNamespace = "t454"
const preparationExportDatabase = "neutral"
const preparationDerivedExportTables = "typed_index_request,typed_index_plan,typed_index_attempt,typed_index_current,typed_index_state,typed_index_job,generation_schedule,generation_schedule_current,generation_schedule_repository,generation_schedule_chunk"

// The post-run role reuses the single opt-in preparation role flag and adds its
// own in/out directory and final-helper flags. Ordinary runs leave them empty and
// skip.
var preparationPostRunIn = flag.String("typed-preparation-postrun-in", "", "opt-in post-run role: directory holding config.json/result.json/receipt.json/provenance.json/inventory.json")
var preparationPostRunOut = flag.String("typed-preparation-postrun-out", "", "opt-in post-run role: create-only directory for seed.surql and postrun.json")
var preparationProfileEpoch = flag.Int64("typed-preparation-profile-epoch", 1, "opt-in post-run role: independently expected successor profile epoch; CAS refuses any other predecessor")
var preparationCmdHelper = flag.String("typed-preparation-cmd-helper", "", "opt-in post-run role: path to the frozen final cmd helper binary substituted into the FINAL inventory")

// preparationFinalTools builds the exact pinned tool identity set for the reduced
// profile. Six identities are the frozen native constants; the planner and
// launcher are the one final cmd helper the operator built and hashed, so its
// digest is the only argument. pinnedProfile re-checks the frozen subset
// independently; the helper coherence guard re-checks planner/launcher against the
// FINAL inventory.
func preparationFinalTools(helperDigest string) typedindex.Tools {
	helper := typedindex.Tool{Version: "cmd", Digest: helperDigest}
	return typedindex.Tools{
		Bazel:    typedindex.Tool{Version: "9.0.0", Digest: BazelDigest},
		RulesGo:  typedindex.Tool{Version: "0.59.0", Digest: "sha256:" + rulesArchive},
		Go:       typedindex.Tool{Version: "1.25.0", Digest: GoDigest},
		Driver:   typedindex.Tool{Version: "0.59.0", Digest: "sha256:" + launcher.NativeDriverSHA256},
		Indexer:  typedindex.Tool{Version: "0.2.7", Digest: SCIPDigest},
		Planner:  helper,
		Launcher: helper,
	}
}

// substituteFinalHelper replaces the managed helper and native driver entries with
// the final cmd helper identity, so the FINAL inventory and the reduced profile pin
// the SAME executable. The preparation run stages the test helper at both paths
// (its worker is the test binary); production verifyTools instead requires
// Profile.Tools.Planner/Launcher to equal the inventory helper digest at both
// phebs-typed-worker and the native driver path, with the cmd/phebs build main.
// Sealing the profile against the unswapped inventory would name an unexecutable
// bundle, discovered only later at a privileged native run. It refuses a malformed
// digest, a non-positive or oversize length, or an inventory missing either path.
func substituteFinalHelper(ctx context.Context, inv typedindex.Inventory, helperBytes int64, helperDigest string) (typedindex.Inventory, error) {
	if !preparationHash(helperDigest) || helperBytes <= 0 || helperBytes > typedindex.MaxFileBytes {
		return typedindex.Inventory{}, typedindex.Invalid
	}
	files := inv.Files()
	seen := 0
	for i, f := range files {
		if f.Path == typedindex.ManagedHelperFile || f.Path == preparationNativeDriverPath {
			files[i] = typedindex.BundleFile{Path: f.Path, Bytes: helperBytes, Digest: helperDigest, Executable: true}
			seen++
		}
	}
	if seen != 2 {
		return typedindex.Inventory{}, typedindex.Unprepared
	}
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	raw, err := json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
	if err != nil {
		return typedindex.Inventory{}, err
	}
	return typedindex.DecodeInventory(ctx, raw, hash(raw))
}

// assertPreparationHelperCoherence is the chain-local replica of the digest half of
// production verifyTools: the FINAL inventory must carry the pinned planner/launcher
// helper digest, executable, at both helper paths. The build-main half is the native
// run's separate physical proof and cannot be checked against a manifest.
func assertPreparationHelperCoherence(final typedindex.Inventory, tools typedindex.Tools) error {
	if tools.Planner.Digest != tools.Launcher.Digest {
		return typedindex.Invalid
	}
	for _, path := range []string{typedindex.ManagedHelperFile, preparationNativeDriverPath} {
		f, err := inventoryFile(final, path)
		if err != nil || !f.Executable || f.Digest != tools.Planner.Digest {
			return typedindex.Unsupported
		}
	}
	return nil
}

// sealPreparationSelection rebuilds Selection-v1 from the run's raw outputs and
// the observed source, then re-decodes it so the sealed bytes are exactly the
// canonical admitted form. The returned universe digest is identity(targets), the
// independent argument InstallTypedProfile binds; it is not a profile field.
func sealPreparationSelection(ctx context.Context, in Provisioning) (Selection, []byte, string, error) {
	raw, universe, err := BuildSelection(ctx, in)
	if err != nil {
		return Selection{}, nil, "", err
	}
	s, err := decode[Selection](raw, MaxSelectionBytes)
	if err != nil {
		return Selection{}, nil, "", err
	}
	if s.Schema != SelectionSchema || s.Source != in.Source || len(s.Targets) == 0 || identity(s.Targets) != universe {
		return Selection{}, nil, "", typedindex.Invalid
	}
	return s, raw, universe, nil
}

// sealPreparationFinalInventory appends selection and fixed formatter metadata to the
// final-helper pre-selection inventory and re-decodes it, so the FINAL inventory
// digest is the reduced profile's BundleDigest. It refuses an empty selection, an
// oversize selection, or a pre-selection inventory that already carries
// SelectionFile or host-tool metadata.
func sealPreparationFinalInventory(ctx context.Context, pre typedindex.Inventory, selection []byte, mkfsDigest string) (typedindex.Inventory, error) {
	if len(selection) == 0 || !preparationHash(mkfsDigest) {
		return typedindex.Inventory{}, typedindex.Invalid
	}
	if int64(len(selection)) > typedindex.MaxFileBytes {
		return typedindex.Inventory{}, typedindex.Capacity
	}
	files := pre.Files()
	for _, f := range files {
		if f.Path == SelectionFile || f.Path == typedindex.HostToolsFile {
			return typedindex.Inventory{}, typedindex.Invalid
		}
	}
	metadata, err := json.Marshal(typedindex.HostToolsDefinition{Schema: typedindex.HostToolsSchema, MkfsDigest: mkfsDigest})
	if err != nil {
		return typedindex.Inventory{}, err
	}
	files = append(files, typedindex.BundleFile{Path: SelectionFile, Bytes: int64(len(selection)), Digest: hash(selection)},
		typedindex.BundleFile{Path: typedindex.HostToolsFile, Bytes: int64(len(metadata)), Digest: hash(metadata)})
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	raw, err := json.Marshal(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
	if err != nil {
		return typedindex.Inventory{}, err
	}
	return typedindex.DecodeInventory(ctx, raw, hash(raw))
}

// sealPreparationProfile emits the reduced Profile-v1 bound to the FINAL
// inventory digest. The profile carries no universe field: the universe digest is
// a separate InstallTypedProfile argument, which is why the closed profile codec
// stays unchanged. DecodeProfile re-validates schema/config/policy/tools, so a
// generated-lane masquerade under ProfileSchema is refused here, not downstream;
// pinnedProfile then refuses a well-formed but non-pinned frozen tool identity.
func sealPreparationProfile(ctx context.Context, final typedindex.Inventory, tools typedindex.Tools, name, imageDigest string) (typedindex.Profile, error) {
	if final.Digest() == "" {
		return typedindex.Profile{}, typedindex.Invalid
	}
	def := typedindex.ProfileDefinition{
		Schema: typedindex.ProfileSchema, Name: name, Provider: typedindex.ProviderID,
		Tools: tools, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(),
		BundleDigest: final.Digest(), ImageDigest: imageDigest,
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return typedindex.Profile{}, err
	}
	profile, err := typedindex.DecodeProfile(ctx, raw)
	if err != nil {
		return typedindex.Profile{}, err
	}
	if !pinnedProfile(profile) {
		return typedindex.Profile{}, typedindex.Unsupported
	}
	return profile, nil
}

// preparationPostRun is the sealed identity chain one post-run reassembly
// produces. Raw is the canonical Selection-v1 bytes; Universe is identity of the
// selection targets and the InstallTypedProfile argument; Final and Profile are
// the re-decoded admitted values, never the input bytes.
type preparationPostRun struct {
	Selection Selection
	Raw       []byte
	Universe  string
	Final     typedindex.Inventory
	Profile   typedindex.Profile
}

// runPreparationPostRunChain seals the whole host chain in protocol order:
// substitute the final cmd helper into the staged inventory, BuildSelection over
// the run's raw outputs, append selection and host-tool metadata, assert
// the profile/inventory helper coherence, then seal the reduced Profile-v1. The
// helper digest is taken from the pinned tools so the inventory and profile cannot
// drift; helperBytes is the observed length of that same cmd helper binary.
func runPreparationPostRunChain(ctx context.Context, in Provisioning, helperBytes int64, tools typedindex.Tools, profileName, imageDigest, mkfsDigest string) (preparationPostRun, error) {
	if tools.Planner.Digest != tools.Launcher.Digest {
		return preparationPostRun{}, typedindex.Invalid
	}
	pre, err := substituteFinalHelper(ctx, in.Inventory, helperBytes, tools.Planner.Digest)
	if err != nil {
		return preparationPostRun{}, err
	}
	in.Inventory = pre
	sel, raw, universe, err := sealPreparationSelection(ctx, in)
	if err != nil {
		return preparationPostRun{}, err
	}
	final, err := sealPreparationFinalInventory(ctx, pre, raw, mkfsDigest)
	if err != nil {
		return preparationPostRun{}, err
	}
	if err = assertPreparationHelperCoherence(final, tools); err != nil {
		return preparationPostRun{}, err
	}
	profile, err := sealPreparationProfile(ctx, final, tools, profileName, imageDigest)
	if err != nil {
		return preparationPostRun{}, err
	}
	return preparationPostRun{Selection: sel, Raw: raw, Universe: universe, Final: final, Profile: profile}, nil
}

// installPreparationProfile CASes only the independently declared predecessor.
// The default first install expects zero; a reviewed reseal explicitly declares
// its successor epoch and refuses any other current epoch.
func installPreparationProfile(ctx context.Context, s *store.Surreal, repository string, profile typedindex.Profile, universe string, epoch int64) (store.TypedIndexIntent, error) {
	if epoch < 1 {
		return store.TypedIndexIntent{}, typedindex.Invalid
	}
	return s.InstallTypedProfile(ctx, repository, profile, universe, epoch-1)
}

// verifyPreparationSeed reproduces the acceptance seed predicate set exactly: the
// observed source authority, the installed profile/epoch/universe intent with no
// desire or cancel or restore flag, one intent and zero of every other control
// kind within a two-row page, and no growth record. It runs against the pristine
// live store BEFORE the export, so it proves the pre-export authority the export
// then captures (the intent table is not in the excluded derived set, so it rides
// the seed); it does not re-open or content-verify the exported file. The epoch is
// an independent literal (the pristine first install mints 1), not the install's
// own echo; an explicit reseal independently declares its successor epoch.
func verifyPreparationSeed(ctx context.Context, s *store.Surreal, source typedindex.Source, profile typedindex.Profile, universe string, epoch int64) error {
	got, err := s.GetTypedSource(ctx, source.Repository)
	if err != nil || got != source {
		return errors.New("post-run seed source authority")
	}
	intent, err := s.GetTypedIndexIntent(ctx, source.Repository)
	if err != nil || !preparationSeedIntentValid(intent, profile.Digest(), universe, epoch) {
		return errors.New("post-run seed profile authority")
	}
	for _, kind := range []store.TypedIndexControlKind{store.TypedIndexIntents, store.TypedIndexRequests, store.TypedIndexPlans, store.TypedIndexAttempts, store.TypedIndexStates, store.TypedIndexCurrents} {
		page, err := s.ScanTypedIndexControls(ctx, kind, "", 2)
		want := 0
		if kind == store.TypedIndexIntents {
			want = 1
		}
		if err != nil || len(page.Rows) != want || page.Next != "" {
			return errors.New("post-run seed execution history")
		}
	}
	if _, err = s.GetTypedIndexGrowth(ctx); !errors.Is(err, store.ErrNotFound) {
		return errors.New("post-run seed growth")
	}
	return nil
}

// Both predecessor and successor seed checks require an idle, unrestored intent.
func preparationSeedIntentValid(intent store.TypedIndexIntent, profile, universe string, epoch int64) bool {
	return epoch > 0 && intent.ProfileDigest == profile && intent.ProfileEpoch == epoch && intent.UniverseDigest == universe && intent.Desired == "" && !intent.Canceled && !intent.RestoreRequired
}

func TestPreparationSeedIntent(t *testing.T) {
	profile, universe := hash([]byte("profile")), hash([]byte("universe"))
	valid := store.TypedIndexIntent{ProfileDigest: profile, ProfileEpoch: 1, UniverseDigest: universe}
	if !preparationSeedIntentValid(valid, profile, universe, 1) {
		t.Fatal("idle predecessor refused")
	}
	for _, mutate := range []func(*store.TypedIndexIntent){
		func(v *store.TypedIndexIntent) { v.RestoreRequired = true },
		func(v *store.TypedIndexIntent) { v.Canceled = true },
		func(v *store.TypedIndexIntent) { v.Desired = hash([]byte("request")) },
		func(v *store.TypedIndexIntent) { v.ProfileEpoch = 2 },
	} {
		v := valid
		mutate(&v)
		if preparationSeedIntentValid(v, profile, universe, 1) {
			t.Fatal("non-pristine predecessor accepted")
		}
	}
}

// preparationHTTPEndpoint translates the SDK WebSocket endpoint into the HTTP form
// the SurrealDB export CLI requires, mirroring recovery.cliEndpoint and adding the
// TLS variant so an isolated wss:// store exports correctly.
func preparationHTTPEndpoint(ws string) string {
	if strings.HasPrefix(ws, "wss://") {
		return "https://" + strings.TrimPrefix(ws, "wss://")
	}
	return "http://" + strings.TrimPrefix(ws, "ws://")
}

// postRunLimitWriter captures a bounded prefix of the export child's streams. It
// always reports the full incoming length so io.Copy never sees a short write the
// child would surface as a spurious failure, while host memory stays under the
// limit. It is mutex-guarded because os/exec drives stdout and stderr from two
// separate copy goroutines.
type postRunLimitWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (w *postRunLimitWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	want := len(p)
	if remain := w.limit - w.buf.Len(); remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		_, _ = w.buf.Write(p)
	}
	return want, nil
}

func (w *postRunLimitWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// exportPristineSeed runs the official engine export once against the isolated
// store, excluding the derived tables, into a create-only destination under the
// 4 MiB seed ceiling. The database password reaches the child only through the
// closed SURREAL_PASS environment channel — the surreal engine's own contract,
// never argv. It returns the seed digest, its byte length and the retained surreal
// tool identity.
func exportPristineSeed(ctx context.Context, endpoint, pass, dest string) (string, int64, store.SurrealIdentity, error) {
	id, err := store.FindSurrealBinary()
	if err != nil {
		return "", 0, store.SurrealIdentity{}, err
	}
	if _, err = os.Stat(dest); err == nil {
		return "", 0, id, errors.New("post-run seed export destination exists")
	} else if !os.IsNotExist(err) {
		return "", 0, id, err
	}
	args := []string{"export",
		"--endpoint", preparationHTTPEndpoint(endpoint),
		"--namespace", preparationExportNamespace,
		"--database", preparationExportDatabase,
		"--log", "none",
		"--tables-exclude", preparationDerivedExportTables,
		dest}
	cmd := exec.CommandContext(ctx, id.Path, args...)
	cmd.Env = append(os.Environ(), "SURREAL_USER=root", "SURREAL_PASS="+pass)
	out := &postRunLimitWriter{limit: preparationExportOutputCeiling}
	cmd.Stdout, cmd.Stderr = out, out
	if err = cmd.Run(); err != nil {
		return "", 0, id, fmt.Errorf("post-run seed export: %w: %s", err, strings.TrimSpace(out.String()))
	}
	st, err := os.Stat(dest)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > preparationSeedCeiling {
		return "", 0, id, errors.New("post-run seed export bound")
	}
	raw, err := readBounded(dest, preparationSeedCeiling)
	if err != nil {
		return "", 0, id, err
	}
	return hash(raw), int64(len(raw)), id, nil
}

// preparationPostRunReceipt is the small source-free completion record for the
// post-run chain. It binds the reviewed config and the observed source by digest
// (never raw source bytes, the store-minted incarnation/generation, or surreal
// host layout), records the test→cmd helper substitution, and carries the
// run-produced selection digest and the real-store profile epoch — the two
// identities the staged config deliberately omits — plus the final inventory,
// profile, universe, exported seed and retained surreal tool identity.
type preparationPostRunReceipt struct {
	Schema               string `json:"schema"`
	ID                   string `json:"id"`
	ConfigSHA256         string `json:"config"`
	SourceSHA256         string `json:"source_sha256"`
	TestSHA256           string `json:"test_sha256"`
	HelperSHA256         string `json:"helper_sha256"`
	UniverseSHA256       string `json:"universe_sha256"`
	SelectionSHA256      string `json:"selection_sha256"`
	FinalInventorySHA256 string `json:"final_inventory_sha256"`
	ProfileSHA256        string `json:"profile_sha256"`
	ProfileEpoch         int64  `json:"profile_epoch"`
	SeedSHA256           string `json:"seed_sha256"`
	SeedBytes            int64  `json:"seed_bytes"`
	SurrealSHA256        string `json:"surreal_sha256"`
	SurrealVersion       string `json:"surreal_version"`
}

// TestPreparationPostRunChain is the always-run fixture proof of the pure seal
// chain. It stages an inventory carrying the TEST helper at both helper paths
// (consistent with the neutral wire fixture's source document), runs the chain
// with a DIFFERENT cmd helper, and asserts the FINAL inventory substitutes the cmd
// helper at both paths, the reduced Profile-v1 pins it coherently and binds the
// FINAL inventory digest, carries no universe field, stays pinned, and that the
// FINAL inventory adds SelectionFile at the returned bytes. Every negative control
// must be refused with the exact typed refusal.
func TestPreparationPostRunChain(t *testing.T) {
	ctx := t.Context()
	testHelper := hash([]byte("test-helper"))
	cmdHelper := hash([]byte("cmd-helper"))
	image := hash([]byte("image"))
	const cmdHelperBytes int64 = 8192
	pre := provisionInventory(t, []typedindex.BundleFile{
		{Path: typedindex.ManagedHelperFile, Bytes: 4096, Digest: testHelper, Executable: true},
		{Path: preparationNativeDriverPath, Bytes: 4096, Digest: testHelper, Executable: true},
		{Path: "source/lib/lib.go", Bytes: 12, Digest: hash([]byte("package lib\n"))},
	})
	in := provisionFixture(t)
	in.Inventory = pre
	tools := preparationFinalTools(cmdHelper)

	out, err := runPreparationPostRunChain(ctx, in, cmdHelperBytes, tools, "neutral", image, hash([]byte("formatter")))
	if err != nil {
		t.Fatal(err)
	}
	if out.Selection.Schema != SelectionSchema || out.Selection.Source != in.Source || len(out.Selection.Targets) == 0 {
		t.Fatal("selection identity", out.Selection.Schema)
	}
	if out.Universe == "" || out.Universe != identity(out.Selection.Targets) {
		t.Fatal("universe digest", out.Universe)
	}
	def := out.Profile.Definition()
	if def.Schema != typedindex.ProfileSchema || def.Provider != typedindex.ProviderID || def.Name != "neutral" {
		t.Fatal("profile identity", def.Schema, def.Provider, def.Name)
	}
	if def.Config != typedindex.ReducedConfig() || def.Policy != typedindex.MeasuredPolicy() {
		t.Fatal("profile config/policy")
	}
	if def.BundleDigest != out.Final.Digest() || def.ImageDigest != image || def.RCDigest != "" {
		t.Fatal("profile bundle binding", def.BundleDigest, out.Final.Digest())
	}
	if !pinnedProfile(out.Profile) {
		t.Fatal("reduced profile is not pinned")
	}
	defRaw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(defRaw)), "universe") {
		t.Fatal("reduced Profile-v1 carries a universe field")
	}
	// The FINAL inventory must carry the cmd helper at both paths, executable, and
	// the profile must pin the same digest — the coherence production verifyTools
	// requires and the unswapped inventory would violate.
	for _, path := range []string{typedindex.ManagedHelperFile, preparationNativeDriverPath} {
		f, ferr := inventoryFile(out.Final, path)
		if ferr != nil || f.Digest != cmdHelper || f.Bytes != cmdHelperBytes || !f.Executable {
			t.Fatal("final inventory did not substitute the cmd helper at", path)
		}
	}
	if def.Tools.Planner.Digest != cmdHelper || def.Tools.Launcher.Digest != cmdHelper {
		t.Fatal("reduced profile does not pin the substituted cmd helper")
	}
	preFiles := pre.Files()
	if slices.ContainsFunc(preFiles, func(f typedindex.BundleFile) bool { return f.Path == SelectionFile }) {
		t.Fatal("pre-selection inventory already carries the selection file")
	}
	finalFiles := out.Final.Files()
	if len(finalFiles) != len(preFiles)+2 {
		t.Fatal("final inventory file count", len(finalFiles), len(preFiles))
	}
	idx := slices.IndexFunc(finalFiles, func(f typedindex.BundleFile) bool { return f.Path == SelectionFile })
	if idx < 0 {
		t.Fatal("final inventory missing the selection file")
	}
	if sf := finalFiles[idx]; sf.Bytes != int64(len(out.Raw)) || sf.Digest != hash(out.Raw) || sf.Executable {
		t.Fatal("selection file binding", sf.Bytes, sf.Digest, sf.Executable)
	}
	metadata := preparationJSON(typedindex.HostToolsDefinition{Schema: typedindex.HostToolsSchema, MkfsDigest: hash([]byte("formatter"))})
	meta, err := inventoryFile(out.Final, typedindex.HostToolsFile)
	if err != nil || meta.Bytes != int64(len(metadata)) || meta.Digest != hash(metadata) || meta.Executable {
		t.Fatal("host formatter metadata binding")
	}
	request := typedindex.NewRequest(in.Source, out.Profile, 1, out.Universe, "run")
	admitted, err := typedindex.Admit(ctx, typedindex.Authority{Enabled: true, Administrator: true, Source: in.Source, Profile: typedindex.Epoch{Number: 1, Digest: out.Profile.Digest()}, UniverseDigest: out.Universe}, out.Profile, preparationJSON(request))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := typedindex.BindHostTools(ctx, admitted, out.Final, metadata)
	if err != nil || bound.MkfsDigest != hash([]byte("formatter")) || bound.Helper.Digest != cmdHelper {
		t.Fatal("production host-tool binding", err)
	}
	if out.Final.Digest() == pre.Digest() {
		t.Fatal("final inventory digest did not advance past the pre-selection digest")
	}

	for _, tc := range []struct {
		name string
		want error
		run  func(*testing.T, context.Context) error
	}{
		{"source-mutated", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			v := provisionFixture(t)
			v.Inventory = pre
			v.Source.Commit = "not-a-commit"
			_, e := runPreparationPostRunChain(ctx, v, cmdHelperBytes, tools, "neutral", image, hash([]byte("formatter")))
			return e
		}},
		{"inventory-source-mutated", typedindex.Stale, func(t *testing.T, ctx context.Context) error {
			v := provisionFixture(t)
			v.Inventory = provisionInventory(t, []typedindex.BundleFile{
				{Path: typedindex.ManagedHelperFile, Bytes: 4096, Digest: testHelper, Executable: true},
				{Path: preparationNativeDriverPath, Bytes: 4096, Digest: testHelper, Executable: true},
				{Path: "source/lib/lib.go", Bytes: 12, Digest: hash([]byte("changed"))},
			})
			_, e := runPreparationPostRunChain(ctx, v, cmdHelperBytes, tools, "neutral", image, hash([]byte("formatter")))
			return e
		}},
		{"driver-path-absent", typedindex.Unprepared, func(t *testing.T, ctx context.Context) error {
			v := provisionFixture(t)
			v.Inventory = provisionInventory(t, []typedindex.BundleFile{
				{Path: typedindex.ManagedHelperFile, Bytes: 4096, Digest: testHelper, Executable: true},
				{Path: "source/lib/lib.go", Bytes: 12, Digest: hash([]byte("package lib\n"))},
			})
			_, e := runPreparationPostRunChain(ctx, v, cmdHelperBytes, tools, "neutral", image, hash([]byte("formatter")))
			return e
		}},
		{"tool-helper-mismatch", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			bad := tools
			bad.Launcher.Digest = hash([]byte("other-helper"))
			_, e := runPreparationPostRunChain(ctx, in, cmdHelperBytes, bad, "neutral", image, hash([]byte("formatter")))
			return e
		}},
		{"helper-bad-digest", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			_, e := substituteFinalHelper(ctx, pre, cmdHelperBytes, "not-a-digest")
			return e
		}},
		{"helper-oversize", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			_, e := substituteFinalHelper(ctx, pre, typedindex.MaxFileBytes+1, cmdHelper)
			return e
		}},
		{"helper-incoherent", typedindex.Unsupported, func(t *testing.T, ctx context.Context) error {
			return assertPreparationHelperCoherence(out.Final, preparationFinalTools(hash([]byte("mismatched-helper"))))
		}},
		{"formatter-bad-digest", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			_, e := sealPreparationFinalInventory(ctx, pre, out.Raw, "not-a-digest")
			return e
		}},
		{"duplicate-host-tools", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			dup := provisionInventory(t, append(pre.Files(), meta))
			_, e := sealPreparationFinalInventory(ctx, dup, out.Raw, image)
			return e
		}},
		{"empty-selection", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			_, e := sealPreparationFinalInventory(ctx, pre, nil, image)
			return e
		}},
		{"duplicate-selection", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			dup := provisionInventory(t, append(pre.Files(), typedindex.BundleFile{Path: SelectionFile, Bytes: 4, Digest: hash([]byte("seln"))}))
			_, e := sealPreparationFinalInventory(ctx, dup, []byte("seln"), image)
			return e
		}},
		{"profile-empty-bundle", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			_, e := sealPreparationProfile(ctx, typedindex.Inventory{}, tools, "neutral", image)
			return e
		}},
		{"profile-bad-image", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			_, e := sealPreparationProfile(ctx, out.Final, tools, "neutral", "not-a-digest")
			return e
		}},
		{"profile-bad-tool-shape", typedindex.Invalid, func(t *testing.T, ctx context.Context) error {
			bad := tools
			bad.Planner.Digest = "x"
			_, e := sealPreparationProfile(ctx, out.Final, bad, "neutral", image)
			return e
		}},
		{"profile-non-pinned-tool", typedindex.Unsupported, func(t *testing.T, ctx context.Context) error {
			bad := tools
			bad.Bazel.Digest = hash([]byte("not-the-pinned-bazel"))
			_, e := sealPreparationProfile(ctx, out.Final, bad, "neutral", image)
			return e
		}},
		{"generated-masquerade", typedindex.Unsupported, func(t *testing.T, ctx context.Context) error {
			d := out.Profile.Definition()
			d.Config = typedindex.GeneratedConfig()
			raw, e := json.Marshal(d)
			if e != nil {
				return e
			}
			_, e = typedindex.DecodeProfile(ctx, raw)
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(t, t.Context()); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

// TestNativePreparationPostRun is the opt-in live post-run chain. Ordinary runs
// skip it. It executes only when an operator supplies the post-run role, the in/out
// directories, the frozen final cmd helper and an isolated PHEBS_PREPARATION_STORE
// endpoint, and only after the native preparation run produced result.json and the
// seed produced provenance.json. It reassembles the identity chain from those
// retained private artifacts, observes the cmd helper identity, installs the
// reduced profile into the pristine store, exports and verifies the seed, and
// writes the create-only source-free receipt. It requires an isolated engine
// fixture (execution-plan B4).
func TestNativePreparationPostRun(t *testing.T) {
	if *preparationRole != "postrun" || *preparationPostRunIn == "" || *preparationPostRunOut == "" || *preparationCmdHelper == "" {
		t.Skip("neutral preparation post-run chain is opt-in")
	}
	ctx := t.Context()

	cfgRaw, err := readBounded(filepath.Join(*preparationPostRunIn, "config.json"), preparationMaxConfig)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parsePreparationConfig(cfgRaw)
	if err != nil {
		t.Fatal(err)
	}
	resRaw, err := readBounded(filepath.Join(*preparationPostRunIn, "result.json"), preparationMaxResult)
	if err != nil {
		t.Fatal(err)
	}
	res, err := parsePreparationResult(resRaw, cfg)
	if err != nil {
		t.Fatal(err)
	}
	receiptRaw, err := readBounded(filepath.Join(*preparationPostRunIn, "receipt.json"), preparationMaxReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyPreparationCompletion(cfg, cfgRaw, resRaw, receiptRaw); err != nil {
		t.Fatal(err)
	}
	provRaw, err := readBounded(filepath.Join(*preparationPostRunIn, "provenance.json"), preparationMaxReceipt)
	if err != nil {
		t.Fatal(err)
	}
	var prov preparationSeedProvenance
	if err = preparationDecode(provRaw, preparationMaxReceipt, &prov); err != nil {
		t.Fatal(err)
	}
	if prov.Schema != preparationSeedProvenanceSchema || prov.Source != cfg.Source || prov.Source.Validate() != nil {
		t.Fatal("post-run observed source provenance mismatch")
	}
	// Re-bind every identity the run echoed before trusting its raw outputs: the
	// result must agree with the reviewed config, the observed source and a
	// complete, removed, no-stop outcome. id alone is a weak human token.
	if res.ID != cfg.ID ||
		res.SourceSHA256 != preparationDigest(preparationJSON(prov.Source)) ||
		res.InventorySHA256 != cfg.InventorySHA256 ||
		res.ProfileSHA256 != cfg.ProfileSHA256 ||
		res.PlanningDigest != cfg.planningDigest() ||
		res.AttemptDigest != cfg.attemptDigest() ||
		res.ExitCode != 0 || res.StopReason != "" {
		t.Fatal("post-run result identity/completion mismatch")
	}
	invRaw, err := readBounded(filepath.Join(*preparationPostRunIn, "inventory.json"), typedindex.MaxInventoryBytes)
	if err != nil || preparationDigest(invRaw) != cfg.InventorySHA256 {
		t.Fatal("post-run staged inventory identity")
	}
	pre, err := typedindex.DecodeInventory(ctx, invRaw, cfg.InventorySHA256)
	if err != nil {
		t.Fatal(err)
	}
	// Observe the frozen final cmd helper rather than trusting its declared digest:
	// read it bounded, require the hash to equal the reviewed config identity, and
	// take its true length as the FINAL inventory helper byte count.
	cmdHelperRaw, err := readBounded(*preparationCmdHelper, typedindex.MaxFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if preparationDigest(cmdHelperRaw) != cfg.HelperSHA256 {
		t.Fatal("post-run cmd helper identity mismatch")
	}
	// Observe the other half of production verifyTools on the actual file: the helper
	// must carry the production build identity, or native-acceptance would later reject
	// a coherent-digest but wrong-build bundle. This is host-checkable and needs no run.
	helperInfo, err := buildinfo.ReadFile(*preparationCmdHelper)
	if err != nil || checkBuild(helperInfo, productionHelperMain, false) != nil {
		t.Fatal("post-run cmd helper build identity")
	}

	in := Provisioning{
		Source: prov.Source, Inventory: pre, Roots: cfg.roots(),
		Module: cfg.Module, Remote: cfg.Remote,
		Cquery: res.Cquery, Aquery: res.Aquery, Projections: res.Projections,
	}
	out, err := runPreparationPostRunChain(ctx, in, int64(len(cmdHelperRaw)), preparationFinalTools(cfg.HelperSHA256), cfg.profileName(), cfg.ImageSHA256, cfg.MkfsSHA256)
	if err != nil {
		t.Fatal(err)
	}

	endpoint := os.Getenv("PHEBS_PREPARATION_STORE")
	if endpoint == "" {
		t.Fatal("post-run role requires PHEBS_PREPARATION_STORE isolated endpoint")
	}
	pass := os.Getenv("PHEBS_PREPARATION_STORE_PASS")
	s, err := openPreparationStore(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()

	currentSource, err := s.GetTypedSource(ctx, cfg.Source.Repository)
	if err != nil || currentSource != prov.Source {
		t.Fatal("post-run source authority changed before install")
	}
	if *preparationProfileEpoch > 1 {
		predecessor, err := s.GetTypedIndexIntent(ctx, cfg.Source.Repository)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := typedindex.DecodeProfile(ctx, []byte(predecessor.ProfileJSON))
		if err != nil || profile.Digest() != predecessor.ProfileDigest {
			t.Fatal("post-run predecessor profile identity")
		}
		if err = verifyPreparationSeed(ctx, s, prov.Source, profile, predecessor.UniverseDigest, *preparationProfileEpoch-1); err != nil {
			t.Fatal(err)
		}
	}
	intent, err := installPreparationProfile(ctx, s, cfg.Source.Repository, out.Profile, out.Universe, *preparationProfileEpoch)
	if err != nil {
		t.Fatal(err)
	}
	// Assert the independently declared successor, never the install return as its own oracle.
	if intent.ProfileEpoch != *preparationProfileEpoch {
		t.Fatal("post-run seed successor epoch mismatch", intent.ProfileEpoch)
	}
	if err = verifyPreparationSeed(ctx, s, prov.Source, out.Profile, out.Universe, *preparationProfileEpoch); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(*preparationPostRunOut, 0700); err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(*preparationPostRunOut, preparationSeedFile)
	seedSHA, seedBytes, surreal, err := exportPristineSeed(ctx, endpoint, pass, seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = preparationReceipt(filepath.Join(*preparationPostRunOut, preparationPostRunFile), preparationPostRunReceipt{
		Schema: preparationPostRunReceiptSchema, ID: cfg.ID,
		ConfigSHA256:         preparationDigest(cfgRaw),
		SourceSHA256:         preparationDigest(preparationJSON(prov.Source)),
		TestSHA256:           cfg.TestSHA256,
		HelperSHA256:         cfg.HelperSHA256,
		UniverseSHA256:       out.Universe,
		SelectionSHA256:      hash(out.Raw),
		FinalInventorySHA256: out.Final.Digest(),
		ProfileSHA256:        out.Profile.Digest(),
		ProfileEpoch:         intent.ProfileEpoch,
		SeedSHA256:           seedSHA, SeedBytes: seedBytes,
		SurrealSHA256: surreal.SHA256, SurrealVersion: surreal.Version,
	}); err != nil {
		t.Fatal(err)
	}
}

// Container teardown is host authority; the worker cannot observe its removal.
func verifyPreparationCompletion(cfg nativePreparationConfig, cfgRaw, resultRaw, receiptRaw []byte) error {
	rc, err := parsePreparationReceipt(receiptRaw)
	if err != nil {
		return err
	}
	if rc.ID != cfg.ID || rc.ConfigSHA256 != preparationDigest(cfgRaw) ||
		rc.PlanningDigest != cfg.planningDigest() || rc.AttemptDigest != cfg.attemptDigest() ||
		rc.TestSHA256 != cfg.TestSHA256 || rc.ImageSHA256 != cfg.ImageSHA256 ||
		rc.ResultSHA256 != preparationDigest(resultRaw) || !preparationHash("sha256:"+rc.ContainerID) ||
		rc.ExitCode != 0 || !rc.Removed || !rc.Complete {
		return errors.New("post-run host completion receipt mismatch")
	}
	return nil
}

func TestPreparationHostCompletion(t *testing.T) {
	cfg := fixturePreparationConfig(t)
	cfgRaw, resultRaw := preparationJSON(cfg), []byte("unaltered worker bytes")
	good := nativePreparationReceipt{Schema: preparationReceiptSchema, ID: cfg.ID,
		PlanningDigest: cfg.planningDigest(), AttemptDigest: cfg.attemptDigest(),
		ConfigSHA256: preparationDigest(cfgRaw), TestSHA256: cfg.TestSHA256, ImageSHA256: cfg.ImageSHA256,
		ScratchDevice: 7, ScratchInode: 0, ContainerID: strings.Repeat("a", 64),
		ResultSHA256: preparationDigest(resultRaw), ExitCode: 0, Removed: true, Complete: true}
	if err := verifyPreparationCompletion(cfg, cfgRaw, resultRaw, preparationJSON(good)); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*nativePreparationReceipt){
		func(r *nativePreparationReceipt) { r.Removed = false },
		func(r *nativePreparationReceipt) { r.Complete = false },
		func(r *nativePreparationReceipt) { r.ExitCode = 125 },
		func(r *nativePreparationReceipt) { r.ID = "other-run" },
		func(r *nativePreparationReceipt) { r.TestSHA256 = preparationDigest([]byte("other executable")) },
		func(r *nativePreparationReceipt) { r.ScratchDevice = 0 },
		func(r *nativePreparationReceipt) { r.ContainerID = "not-a-container-id" },
	} {
		bad := good
		change(&bad)
		if err := verifyPreparationCompletion(cfg, cfgRaw, resultRaw, preparationJSON(bad)); err == nil {
			t.Fatal("unsubstantiated completion accepted")
		}
	}
	if err := verifyPreparationCompletion(cfg, cfgRaw, append(resultRaw, 'x'), preparationJSON(good)); err == nil {
		t.Fatal("changed worker bytes accepted")
	}
}
