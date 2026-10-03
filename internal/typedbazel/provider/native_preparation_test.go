package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

// Neutral preparation control identities. These bind the one finite preparation
// invocation described in the leaf-4.82 protocol; they are test-only and ship
// no config. Planning/attempt digests are domain-separated from managed request
// identifiers so a preparation control can never be replayed as an admission.
const preparationSchema = "phebs-typed-native-preparation-v1"
const preparationResultSchema = "phebs-typed-native-preparation-result-v1"
const preparationReceiptSchema = "phebs-typed-native-preparation-receipt-v1"
const preparationSeedProvenanceSchema = "phebs-typed-native-preparation-seed-v1"
const preparationRepo = "example.invalid/phebs-native-neutral"
const preparationRemote = "https://example.invalid/phebs-native-neutral"
const preparationRoot = "//lib:lib"
const preparationPlanningDomain = "phebs-typed-preparation-planning-v1\x00"
const preparationAttemptDomain = "phebs-typed-preparation-attempt-v1\x00"
const preparationCorpusSchema = "phebs-typed-native-corpus-preparation-v1"
const preparationCorpusResultSchema = "phebs-typed-native-corpus-preparation-result-v1"
const preparationCorpusRepo = "github.com/bazelbuild/remote-apis-sdks"
const preparationCorpusCommit = "d5824b1a2286806b07efd030aa3a139c4f540157"
const preparationCorpusArchive = "sha256:c9ecf680cd7bd0d88d8a6d1a0084a09c0a9dc45145fc28fbdcda888586d54bcc"

const preparationMaxConfig = 16384
const preparationMaxReceipt = 128 << 10

// preparationRetainedCeiling bounds raw cquery+aquery+projection bytes retained
// by one preparation. It is a smaller neutral transport limit, never a change to
// a production planner bound. preparationMaxResult mirrors the existing worker
// output ceiling that the JSON/base64 expansion must also fit.
const preparationRetainedCeiling = 8 << 20
const preparationMaxResult = typedsandbox.OutputBytes

// The corpus result retains the same fields, without compression or omission.
// Raw base64 payload alone cannot exceed floor((physical ceiling - LF)*3/4).
// JSON keys, padding and metadata also consume that frame, so encode still
// refuses any complete JSON+LF that exceeds the unchanged physical ceiling.
const preparationCorpusRetainedCeiling = (preparationMaxResult - 1) * 3 / 4

var preparationID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// The host-role config flag (typed-preparation-config) is declared beside its only
// consumer in native_preparation_linux_test.go. The portable seed role uses the
// commit/provenance flags below and never a full pre-validated config.
var preparationRole = flag.String("typed-preparation-role", "", "opt-in preparation role: seed or host")
var preparationCommit = flag.String("typed-preparation-commit", "", "opt-in seed role: exact 40-hex neutral source commit to provision and observe")
var preparationCorpus = flag.String("typed-preparation-corpus", "", "opt-in seed role: frozen ordinary, proto or fanout cohort; empty preserves neutral")
var preparationCorpusArchivePath = flag.String("typed-preparation-corpus-archive", "", "optional read-only frozen Apache-2.0 corpus archive proof; no target execution")
var preparationSeedOut = flag.String("typed-preparation-seed-out", "", "opt-in seed role: create-only path for the observed source provenance record")

// nativePreparationConfig binds exactly one preparation ID, the fixed neutral
// source/module/remote/sole-root, and the exact source, inventory, profile,
// helper, image and formatter identities. It accepts no command, environment,
// path, root or cap override; the fixed caps come from typedsandbox constants.
// It deliberately carries only identities knowable BEFORE the run: the
// run-produced Selection-v1 digest and the real-store profile epoch are recorded
// in the post-run final identity chain, never staged here, so a truthful config
// can exist at stage time without fabricating a run output.
type nativePreparationConfig struct {
	Schema           string            `json:"schema"`
	ID               string            `json:"id"`
	SourceCommit     string            `json:"source_commit"`
	Source           typedindex.Source `json:"source"`
	Module           string            `json:"module"`
	Remote           string            `json:"remote"`
	Root             string            `json:"root"`
	TestSHA256       string            `json:"test_sha256"`
	HelperSHA256     string            `json:"helper_sha256"`
	EngineSHA256     string            `json:"engine_sha256"`
	InventorySHA256  string            `json:"inventory_sha256"`
	ProfileSHA256    string            `json:"profile_sha256"`
	ImageSHA256      string            `json:"image_sha256"`
	MkfsSHA256       string            `json:"mkfs_sha256"`
	DeploymentSHA256 string            `json:"deployment_sha256"`
	Cohort           string            `json:"cohort,omitempty"`
}

// Corpus preparation never accepts caller-authored roots or another revision.
// The neutral wire omits Cohort and retains its original bytes and identities.
func preparationCorpusRoots(cohort string) ([]string, error) {
	switch cohort {
	case "ordinary":
		return []string{"//go/pkg/moreflag:moreflag", "//go/pkg/cache:cache", "//go/pkg/outerr:outerr"}, nil
	case "proto":
		return []string{"//go/api/command:command", "//go/pkg/command:command"}, nil
	case "fanout":
		return []string{"//go/pkg/client:client", "//go/pkg/cas:cas", "//go/pkg/rexec:rexec"}, nil
	default:
		return nil, errors.New("unknown frozen corpus cohort")
	}
}

func (c nativePreparationConfig) roots() []string {
	if c.Schema == preparationCorpusSchema {
		roots, _ := preparationCorpusRoots(c.Cohort) // parsePreparationConfig already admitted this closed selector.
		return roots
	}
	return []string{c.Root}
}

func (c nativePreparationConfig) profileName() string {
	if c.Schema == preparationCorpusSchema {
		return "corpus-" + c.Cohort
	}
	return "neutral"
}

func (c nativePreparationConfig) resultSchema() string {
	if c.Schema == preparationCorpusSchema {
		return preparationCorpusResultSchema
	}
	return preparationResultSchema
}

func (c nativePreparationConfig) retainedCeiling() int64 {
	if c.Schema == preparationCorpusSchema {
		return preparationCorpusRetainedCeiling
	}
	return preparationRetainedCeiling
}

// The commit is bound to the unchanged archived corpus, not merely asserted by
// the operator's source metadata. This test-only verifier never executes source.
func preparationCorpusFiles(ctx context.Context, archive []byte) ([]typedindex.BundleFile, error) {
	if len(archive) != 249496 || preparationDigest(archive) != preparationCorpusArchive {
		return nil, typedindex.Invalid
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	r := tar.NewReader(io.LimitReader(gz, 2<<20))
	prefix := "remote-apis-sdks-" + preparationCorpusCommit
	files := []typedindex.BundleFile{}
	seen := map[string]bool{}
	var total int64
	for records := 0; ; records++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		h, nextErr := r.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		if records == 0 && h.Typeflag == tar.TypeXGlobalHeader && h.Name == "pax_global_header" && len(h.PAXRecords) == 1 && h.PAXRecords["comment"] == preparationCorpusCommit {
			continue
		}
		if records >= 168 || h.Name != prefix && !strings.HasPrefix(h.Name, prefix+"/") {
			return nil, typedindex.Invalid
		}
		name := strings.TrimPrefix(h.Name, prefix+"/")
		if h.Typeflag == tar.TypeDir {
			if h.Size != 0 {
				return nil, typedindex.Invalid
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || !safeRelative(name) || seen[name] || h.Size < 0 || h.Size > 1079184-total {
			return nil, typedindex.Invalid
		}
		b, readErr := io.ReadAll(io.LimitReader(r, h.Size+1))
		if readErr != nil || int64(len(b)) != h.Size {
			return nil, typedindex.Invalid
		}
		seen[name] = true
		total += h.Size
		files = append(files, typedindex.BundleFile{Path: "source/" + name, Bytes: h.Size, Digest: preparationDigest(b), Executable: h.Mode&0111 != 0})
	}
	if len(files) != 128 || total != 1079184 {
		return nil, typedindex.Invalid
	}
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func verifyPreparationCorpusInventory(ctx context.Context, inventory typedindex.Inventory, archive []byte) error {
	expected, err := preparationCorpusFiles(ctx, archive)
	if err != nil {
		return err
	}
	actual := []typedindex.BundleFile{}
	for _, f := range inventory.Files() {
		if strings.HasPrefix(f.Path, "source/") {
			actual = append(actual, f)
		}
	}
	if !slices.Equal(actual, expected) {
		return typedindex.Stale
	}
	return ctx.Err()
}

// nativePreparationResult carries the raw provenance the host independently
// reassembles: raw cquery/aquery, exact projection names/bytes, build stream
// digests/lengths, command identity digests, echoed source/inventory/profile
// identities, stage timings and the bounded sandbox outcome. It never carries
// raw stderr bytes, only their digest and length.
type nativePreparationResult struct {
	Schema            string            `json:"schema"`
	ID                string            `json:"id"`
	PlanningDigest    string            `json:"planning_digest"`
	AttemptDigest     string            `json:"attempt_digest"`
	Cquery            []byte            `json:"cquery"`
	Aquery            []byte            `json:"aquery"`
	Projections       map[string][]byte `json:"projections"`
	CommandDigests    [3]string         `json:"command_digests"`
	BuildStdoutSHA256 string            `json:"build_stdout_sha256"`
	BuildStdoutBytes  int64             `json:"build_stdout_bytes"`
	BuildStderrSHA256 string            `json:"build_stderr_sha256"`
	BuildStderrBytes  int64             `json:"build_stderr_bytes"`
	RetainedBytes     int64             `json:"retained_bytes"`
	SourceSHA256      string            `json:"source_sha256"`
	InventorySHA256   string            `json:"inventory_sha256"`
	ProfileSHA256     string            `json:"profile_sha256"`
	StageMillis       map[string]int64  `json:"stage_millis"`
	ExitCode          int               `json:"exit_code"`
	Removed           bool              `json:"removed"`
	StopReason        string            `json:"stop_reason"`
}

// nativePreparationReceipt is the small source-free completion record. It binds
// the config/executable/image/scratch/container identities and the result
// digest; it never embeds raw source or captured planner bytes. The "config"
// key carries the reviewed config SHA256, matching the operator transport's
// staged/dispatch/receipt binding convention.
type nativePreparationReceipt struct {
	Schema         string `json:"schema"`
	ID             string `json:"id"`
	PlanningDigest string `json:"planning_digest"`
	AttemptDigest  string `json:"attempt_digest"`
	ConfigSHA256   string `json:"config"`
	TestSHA256     string `json:"test_sha256"`
	ImageSHA256    string `json:"image_sha256"`
	// Historical wire names carry the scratch device major/minor, not an inode.
	ScratchDevice uint64 `json:"scratch_device"`
	ScratchInode  uint64 `json:"scratch_inode"`
	ContainerID   string `json:"container_id"`
	ResultSHA256  string `json:"result_sha256"`
	ExitCode      int    `json:"exit_code"`
	Removed       bool   `json:"removed"`
	Complete      bool   `json:"complete"`
}

// preparationSeedProvenance is the source-free observed identity the seed step
// returns. It carries only store-minted metadata (repository, the lazily
// CAS-minted incarnation, the generation computed over repository/incarnation/
// commit/epoch, and the indexed commit), never raw source bytes. It is the exact
// provenance the later config authoring step binds, so the seed observes it back
// rather than requiring it up front.
type preparationSeedProvenance struct {
	Schema string            `json:"schema"`
	Source typedindex.Source `json:"source"`
}

func preparationDigest(raw []byte) string {
	s := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(s[:])
}
func preparationHash(s string) bool {
	if len(s) != 71 || s[:7] != "sha256:" {
		return false
	}
	b, e := hex.DecodeString(s[7:])
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s[7:]
}
func preparationDecode(raw []byte, limit int, out any) error {
	if len(raw) == 0 || len(raw) > limit {
		return errors.New("preparation control bound")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing preparation control")
	}
	canonical, e := json.Marshal(out)
	if e != nil || !bytes.Equal(canonical, raw) {
		return errors.New("noncanonical or duplicate preparation control")
	}
	return nil
}
func preparationJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

// planningDigest and attemptDigest derive the domain-separated control scalars
// from the preparation ID. They are not typedindex admissions and never collide
// with a managed request/plan/attempt digest because of the fixed domain prefix.
func (c nativePreparationConfig) planningDigest() string {
	if c.Schema == preparationCorpusSchema {
		return preparationDigest([]byte("phebs-typed-corpus-preparation-planning-v1\x00" + c.ID))
	}
	return preparationDigest([]byte(preparationPlanningDomain + c.ID))
}
func (c nativePreparationConfig) attemptDigest() string {
	if c.Schema == preparationCorpusSchema {
		return preparationDigest([]byte("phebs-typed-corpus-preparation-attempt-v1\x00" + c.ID))
	}
	return preparationDigest([]byte(preparationAttemptDomain + c.ID))
}

func parsePreparationConfig(raw []byte) (nativePreparationConfig, error) {
	var c nativePreparationConfig
	if e := preparationDecode(raw, preparationMaxConfig, &c); e != nil {
		return c, e
	}
	if !preparationID.MatchString(c.ID) || c.Source.Validate() != nil {
		return c, errors.New("neutral preparation contract")
	}
	switch c.Schema {
	case preparationSchema:
		if c.Source.Repository != preparationRepo || c.Module != preparationRepo || c.Remote != preparationRemote || c.Root != preparationRoot || c.Cohort != "" {
			return c, errors.New("neutral preparation source/module/remote/root")
		}
	case preparationCorpusSchema:
		if _, e := preparationCorpusRoots(c.Cohort); e != nil || c.Source.Repository != preparationCorpusRepo || c.Source.Commit != preparationCorpusCommit || c.Module != preparationCorpusRepo || c.Remote != "https://"+preparationCorpusRepo || c.Root != "" {
			return c, errors.New("frozen corpus source/module/remote/cohort")
		}
	default:
		return c, errors.New("preparation schema")
	}
	b, e := hex.DecodeString(c.SourceCommit)
	if e != nil || len(b) != 20 || hex.EncodeToString(b) != c.SourceCommit {
		return c, errors.New("neutral preparation source commit")
	}
	for _, h := range []string{c.TestSHA256, c.HelperSHA256, c.EngineSHA256, c.InventorySHA256, c.ProfileSHA256, c.ImageSHA256, c.MkfsSHA256, c.DeploymentSHA256} {
		if !preparationHash(h) {
			return c, errors.New("unfilled preparation identity")
		}
	}
	return c, nil
}

func preparationRetained(r *nativePreparationResult) int64 {
	total := int64(len(r.Cquery)) + int64(len(r.Aquery))
	for _, v := range r.Projections {
		total += int64(len(v))
	}
	return total
}

func parsePreparationResult(raw []byte, cfg nativePreparationConfig) (nativePreparationResult, error) {
	var r nativePreparationResult
	if _, e := parsePreparationConfig(preparationJSON(cfg)); e != nil {
		return r, e
	}
	if e := preparationDecode(raw, preparationMaxResult-1, &r); e != nil {
		return r, e
	}
	ceiling := int64(preparationRetainedCeiling)
	switch r.Schema {
	case preparationResultSchema:
		// Retained neutral and corpus receipts both used this exact schema.
	case preparationCorpusResultSchema:
		if cfg.Schema != preparationCorpusSchema {
			return r, errors.New("corpus preparation result requires corpus config")
		}
		if r.ID != cfg.ID || r.PlanningDigest != cfg.planningDigest() || r.AttemptDigest != cfg.attemptDigest() ||
			r.SourceSHA256 != preparationDigest(preparationJSON(cfg.Source)) || r.InventorySHA256 != cfg.InventorySHA256 || r.ProfileSHA256 != cfg.ProfileSHA256 {
			return r, errors.New("corpus preparation result config binding")
		}
		ceiling = preparationCorpusRetainedCeiling
	default:
		return r, errors.New("preparation result schema")
	}
	if !preparationID.MatchString(r.ID) {
		return r, errors.New("neutral preparation result contract")
	}
	if !preparationHash(r.PlanningDigest) || !preparationHash(r.AttemptDigest) {
		return r, errors.New("neutral preparation result control digest")
	}
	for _, h := range []string{r.BuildStdoutSHA256, r.BuildStderrSHA256, r.SourceSHA256, r.InventorySHA256, r.ProfileSHA256} {
		if !preparationHash(h) {
			return r, errors.New("neutral preparation result identity")
		}
	}
	for i, h := range r.CommandDigests {
		if !preparationHash(h) {
			return r, fmt.Errorf("neutral preparation command digest %d", i)
		}
	}
	if r.BuildStdoutBytes < 0 || r.BuildStderrBytes < 0 {
		return r, errors.New("neutral preparation stream length")
	}
	for name := range r.Projections {
		if !safeRelative(name) {
			return r, errors.New("neutral preparation projection name")
		}
	}
	retained := preparationRetained(&r)
	if r.RetainedBytes != retained {
		return r, errors.New("neutral preparation retained mismatch")
	}
	if retained > ceiling {
		return r, errors.New("neutral preparation retained ceiling")
	}
	return r, nil
}

// encodePreparationResult refuses a marshaled result that would not fit the
// existing worker output ceiling, returning only a bounded classified error.
func encodePreparationResult(r nativePreparationResult) ([]byte, error) {
	raw := preparationJSON(r)
	if len(raw) > preparationMaxResult-1 {
		return nil, errors.New("neutral preparation result overflow")
	}
	return raw, nil
}

func parsePreparationReceipt(raw []byte) (nativePreparationReceipt, error) {
	var rc nativePreparationReceipt
	if e := preparationDecode(raw, preparationMaxReceipt, &rc); e != nil {
		return rc, e
	}
	if rc.Schema != preparationReceiptSchema || !preparationID.MatchString(rc.ID) {
		return rc, errors.New("neutral preparation receipt contract")
	}
	for _, h := range []string{rc.PlanningDigest, rc.AttemptDigest, rc.ConfigSHA256, rc.TestSHA256, rc.ImageSHA256, rc.ResultSHA256} {
		if !preparationHash(h) {
			return rc, errors.New("neutral preparation receipt identity")
		}
	}
	if rc.ScratchDevice == 0 {
		return rc, errors.New("neutral preparation receipt scratch")
	}
	return rc, nil
}

// preparationReceipt writes a create-only, fsynced receipt. A lost SSH reply
// preserves the original; a second write to the same path refuses.
func preparationReceipt(path string, v any) error {
	raw := preparationJSON(v)
	if len(raw) > preparationMaxReceipt {
		return errors.New("neutral preparation receipt overflow")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if e = errors.Join(writeErr, syncErr, closeErr); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	return errors.Join(d.Sync(), d.Close())
}

func fixturePreparationConfig(t *testing.T) nativePreparationConfig {
	t.Helper()
	h := preparationDigest([]byte("fixed"))
	return nativePreparationConfig{
		Schema: preparationSchema, ID: "neutral-prep-1",
		SourceCommit: fmt.Sprintf("%040x", 1),
		Source:       typedindex.Source{Repository: preparationRepo, Incarnation: "fixture", Generation: h, Commit: fmt.Sprintf("%040x", 2)},
		Module:       preparationRepo, Remote: preparationRemote, Root: preparationRoot,
		TestSHA256: h, HelperSHA256: h, EngineSHA256: h, InventorySHA256: h, ProfileSHA256: h,
		ImageSHA256: h, MkfsSHA256: h, DeploymentSHA256: h,
	}
}

func TestNativePreparationConfig(t *testing.T) {
	c := fixturePreparationConfig(t)
	if _, e := parsePreparationConfig(preparationJSON(c)); e != nil {
		t.Fatal(e)
	}
	if c.planningDigest() == c.attemptDigest() {
		t.Fatal("preparation control digests are not domain-separated")
	}
	if !preparationHash(c.planningDigest()) || !preparationHash(c.attemptDigest()) {
		t.Fatal("preparation control digest shape")
	}
	for _, tc := range []struct {
		name   string
		change func(*nativePreparationConfig)
	}{
		{"target", func(c *nativePreparationConfig) { c.Source.Repository = "github.com/public/target" }},
		{"path", func(c *nativePreparationConfig) { c.ID = "../escape" }},
		{"module", func(c *nativePreparationConfig) { c.Module = "example.invalid/other" }},
		{"remote", func(c *nativePreparationConfig) { c.Remote = "https://example.invalid/other" }},
		{"root", func(c *nativePreparationConfig) { c.Root = "//other:other" }},
		{"empty", func(c *nativePreparationConfig) { c.HelperSHA256 = "" }},
		{"commit", func(c *nativePreparationConfig) { c.SourceCommit = "deadbeef" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := c
			tc.change(&v)
			if _, e := parsePreparationConfig(preparationJSON(v)); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, raw := range [][]byte{
		append(preparationJSON(c), '\n'),
		bytes.Replace(preparationJSON(c), []byte(`"schema":`), []byte(`"unknown":1,"schema":`), 1),
		bytes.Replace(preparationJSON(c), []byte(`"id":"neutral-prep-1"`), []byte(`"id":"neutral-prep-1","id":"neutral-prep-1"`), 1),
	} {
		if _, e := parsePreparationConfig(raw); e == nil {
			t.Fatal("ambiguous config")
		}
	}
}

func TestNativePreparationCorpusConfig(t *testing.T) {
	neutral := fixturePreparationConfig(t)
	if bytes.Contains(preparationJSON(neutral), []byte(`"cohort"`)) {
		t.Fatal("neutral wire changed")
	}
	for _, cohort := range []string{"ordinary", "proto", "fanout"} {
		t.Run(cohort, func(t *testing.T) {
			c := neutral
			c.Schema, c.Cohort, c.Root = preparationCorpusSchema, cohort, ""
			c.Source.Repository, c.Source.Commit = preparationCorpusRepo, preparationCorpusCommit
			c.Module, c.Remote = preparationCorpusRepo, "https://"+preparationCorpusRepo
			if _, err := parsePreparationConfig(preparationJSON(c)); err != nil {
				t.Fatal(err)
			}
			if len(c.roots()) < 2 || c.profileName() != "corpus-"+cohort || c.planningDigest() == neutral.planningDigest() || c.attemptDigest() == neutral.attemptDigest() {
				t.Fatal("corpus selection/domain")
			}
			if c.resultSchema() != preparationCorpusResultSchema || c.retainedCeiling() != preparationCorpusRetainedCeiling {
				t.Fatal("corpus preparation transport selection")
			}
			if repo, remote, err := preparationSeedLocation(preparationCorpusCommit, cohort); err != nil || repo != c.Source.Repository || remote != c.Remote {
				t.Fatal("corpus seed selector", err)
			}
			for _, change := range []func(*nativePreparationConfig){
				func(v *nativePreparationConfig) { v.Cohort = "all" },
				func(v *nativePreparationConfig) { v.Source.Commit = neutral.Source.Commit },
				func(v *nativePreparationConfig) { v.Source.Repository = preparationRepo },
				func(v *nativePreparationConfig) { v.Root = "//..." },
				func(v *nativePreparationConfig) { v.Module = preparationRepo },
				func(v *nativePreparationConfig) { v.Remote = preparationRemote },
				func(v *nativePreparationConfig) { v.Schema = preparationSchema },
			} {
				v := c
				change(&v)
				if _, err := parsePreparationConfig(preparationJSON(v)); err == nil {
					t.Fatal("foreign corpus authority accepted")
				}
			}
		})
	}
	for _, cohort := range []string{"all", "ordinary"} {
		if _, _, err := preparationSeedLocation(neutral.Source.Commit, cohort); err == nil {
			t.Fatal("unfrozen corpus seed")
		}
	}
}

func TestNativePreparationCorpusArchive(t *testing.T) {
	if *preparationCorpusArchivePath == "" {
		t.Skip("explicit read-only frozen corpus archive path required")
	}
	ctx := t.Context()
	raw, err := readBounded(*preparationCorpusArchivePath, 249496)
	if err != nil {
		t.Fatal(err)
	}
	files, err := preparationCorpusFiles(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	invRaw := preparationJSON(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
	inv, err := typedindex.DecodeInventory(ctx, invRaw, preparationDigest(invRaw))
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyPreparationCorpusInventory(ctx, inv, raw); err != nil {
		t.Fatal(err)
	}
	files[0].Bytes++
	badRaw := preparationJSON(typedindex.InventoryDefinition{Schema: typedindex.InventorySchema, Files: files})
	bad, err := typedindex.DecodeInventory(ctx, badRaw, preparationDigest(badRaw))
	if err != nil || verifyPreparationCorpusInventory(ctx, bad, raw) == nil {
		t.Fatal("changed corpus source admitted", err)
	}
	raw[len(raw)-1] ^= 1
	if _, err = preparationCorpusFiles(ctx, raw); err == nil {
		t.Fatal("changed corpus archive admitted")
	}
}

func fixturePreparationResult(t *testing.T) nativePreparationResult {
	t.Helper()
	c := fixturePreparationConfig(t)
	h := preparationDigest([]byte("stream"))
	return nativePreparationResult{
		Schema: preparationResultSchema, ID: c.ID,
		PlanningDigest: c.planningDigest(), AttemptDigest: c.attemptDigest(),
		Cquery: []byte("cquery"), Aquery: []byte("aquery"),
		Projections:       map[string][]byte{"lib/lib.x": []byte("projection")},
		CommandDigests:    [3]string{h, h, h},
		BuildStdoutSHA256: h, BuildStdoutBytes: 3, BuildStderrSHA256: h, BuildStderrBytes: 0,
		RetainedBytes: int64(len("cquery") + len("aquery") + len("projection")),
		SourceSHA256:  h, InventorySHA256: h, ProfileSHA256: h,
		StageMillis: map[string]int64{"plan": 1}, ExitCode: 0, Removed: true,
	}
}

func TestNativePreparationResult(t *testing.T) {
	r := fixturePreparationResult(t)
	raw, e := encodePreparationResult(r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = parsePreparationResult(raw, fixturePreparationConfig(t)); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		change func(*nativePreparationResult)
	}{
		{"retained-mismatch", func(r *nativePreparationResult) { r.RetainedBytes++ }},
		{"retained-ceiling", func(r *nativePreparationResult) {
			r.Cquery = make([]byte, preparationRetainedCeiling)
			r.RetainedBytes = preparationRetainedCeiling + int64(len(r.Aquery)) + int64(len(r.Projections["lib/lib.x"]))
		}},
		{"projection-name", func(r *nativePreparationResult) {
			r.Projections = map[string][]byte{"../escape": []byte("x")}
			r.RetainedBytes = preparationRetained(r)
		}},
		{"command-digest", func(r *nativePreparationResult) { r.CommandDigests[1] = "" }},
		{"stream-length", func(r *nativePreparationResult) { r.BuildStderrBytes = -1 }},
		{"control-digest", func(r *nativePreparationResult) { r.PlanningDigest = "sha256:" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := r
			tc.change(&v)
			raw, e := encodePreparationResult(v)
			if e != nil {
				return
			}
			if _, e = parsePreparationResult(raw, fixturePreparationConfig(t)); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	// An exactly-at-ceiling result is admitted; one byte over is refused.
	at := r
	at.Cquery = make([]byte, preparationRetainedCeiling-int64(len(r.Aquery))-int64(len(r.Projections["lib/lib.x"])))
	at.RetainedBytes = preparationRetained(&at)
	if raw, e = encodePreparationResult(at); e != nil {
		t.Fatal(e)
	}
	if _, e = parsePreparationResult(raw, fixturePreparationConfig(t)); e != nil {
		t.Fatal("at-ceiling refused", e)
	}
}

func TestNativePreparationResultOverflow(t *testing.T) {
	r := fixturePreparationResult(t)
	r.Cquery = make([]byte, preparationRetainedCeiling-int64(len(r.Aquery))-int64(len(r.Projections["lib/lib.x"])))
	r.RetainedBytes = preparationRetained(&r)
	// Inflate a projection beyond the retained ceiling so the marshaled form
	// cannot fit the worker output ceiling; encode must refuse, not truncate.
	r.Projections["lib/big.x"] = make([]byte, preparationMaxResult)
	r.RetainedBytes = preparationRetained(&r)
	if _, e := encodePreparationResult(r); e == nil {
		t.Fatal("oversize result encoded")
	}
}

func TestNativePreparationResultLegacyBytes(t *testing.T) {
	raw, err := encodePreparationResult(fixturePreparationResult(t))
	if err != nil || len(raw) != 1191 || preparationDigest(raw) != "sha256:b5a015aa5bcebb1939958da3255c6a992fa7e20008add0339c2f1d0f097d4ce5" {
		t.Fatal("legacy result bytes changed", err)
	}
	cfg := fixturePreparationConfig(t)
	if cfg.resultSchema() != preparationResultSchema || cfg.retainedCeiling() != 8<<20 {
		t.Fatal("neutral result selection changed")
	}
}

func fixtureCorpusPreparationResult(t *testing.T) (nativePreparationConfig, nativePreparationResult) {
	t.Helper()
	cfg := fixturePreparationConfig(t)
	cfg.Schema, cfg.Cohort, cfg.Root, cfg.ID = preparationCorpusSchema, "proto", "", "corpus-proto-prep-2"
	cfg.Source.Repository, cfg.Source.Commit = preparationCorpusRepo, preparationCorpusCommit
	cfg.Module, cfg.Remote = preparationCorpusRepo, "https://"+preparationCorpusRepo
	r := fixturePreparationResult(t)
	r.Schema, r.ID = cfg.resultSchema(), cfg.ID
	r.PlanningDigest, r.AttemptDigest = cfg.planningDigest(), cfg.attemptDigest()
	r.SourceSHA256 = preparationDigest(preparationJSON(cfg.Source))
	r.InventorySHA256, r.ProfileSHA256 = cfg.InventorySHA256, cfg.ProfileSHA256
	return cfg, r
}

func TestNativePreparationCorpusResult(t *testing.T) {
	cfg, r := fixtureCorpusPreparationResult(t)
	if preparationCorpusRetainedCeiling != 12582911 || cfg.retainedCeiling() != preparationCorpusRetainedCeiling {
		t.Fatal("corpus retention is not derived from the existing base64 frame ceiling")
	}
	for _, tc := range []struct {
		name     string
		schema   string
		retained int64
		want     bool
	}{
		{"legacy-corpus-at-neutral-limit", preparationResultSchema, preparationRetainedCeiling, true},
		{"legacy-corpus-over-neutral-limit", preparationResultSchema, preparationRetainedCeiling + 1, false},
		{"corpus-over-neutral-limit", preparationCorpusResultSchema, preparationRetainedCeiling + 1, true},
		{"corpus-at-raw-ceiling-needs-metadata", preparationCorpusResultSchema, preparationCorpusRetainedCeiling, false},
		{"corpus-over-raw-ceiling", preparationCorpusResultSchema, preparationCorpusRetainedCeiling + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := r
			value.Schema = tc.schema
			value.Cquery = make([]byte, tc.retained-int64(len(value.Aquery))-int64(len(value.Projections["lib/lib.x"])))
			value.RetainedBytes = preparationRetained(&value)
			raw, err := encodePreparationResult(value)
			if err == nil {
				_, err = parsePreparationResult(raw, cfg)
			}
			if (err == nil) != tc.want {
				t.Fatal("corpus retention/frame boundary", err)
			}
		})
	}
	raw, err := encodePreparationResult(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parsePreparationResult(raw, fixturePreparationConfig(t)); err == nil {
		t.Fatal("corpus result admitted under neutral config")
	}
	badConfig := cfg
	badConfig.Cohort = "all"
	if _, err = parsePreparationResult(raw, badConfig); err == nil {
		t.Fatal("corpus result admitted under open cohort")
	}
	for _, tc := range []struct {
		name   string
		change func(*nativePreparationResult)
	}{
		{"schema", func(v *nativePreparationResult) { v.Schema = "unknown" }},
		{"id", func(v *nativePreparationResult) { v.ID = "other-prep" }},
		{"planning", func(v *nativePreparationResult) { v.PlanningDigest = cfg.attemptDigest() }},
		{"attempt", func(v *nativePreparationResult) { v.AttemptDigest = cfg.planningDigest() }},
		{"source", func(v *nativePreparationResult) { v.SourceSHA256 = cfg.TestSHA256 }},
		{"inventory", func(v *nativePreparationResult) { v.InventorySHA256 = preparationDigest([]byte("other")) }},
		{"profile", func(v *nativePreparationResult) { v.ProfileSHA256 = preparationDigest([]byte("other")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := r
			tc.change(&value)
			if _, err = parsePreparationResult(preparationJSON(value), cfg); err == nil {
				t.Fatal("unbound corpus result admitted")
			}
		})
	}
	for _, invalid := range [][]byte{
		append(slices.Clone(raw), '\n'),
		bytes.Replace(raw, []byte(`"schema":`), []byte(`"unknown":1,"schema":`), 1),
		bytes.Replace(raw, []byte(`"id":"corpus-proto-prep-2"`), []byte(`"id":"corpus-proto-prep-2","id":"corpus-proto-prep-2"`), 1),
	} {
		if _, err = parsePreparationResult(invalid, cfg); err == nil {
			t.Fatal("ambiguous corpus result admitted")
		}
	}
}

func TestNativePreparationCorpusResultFrameLF(t *testing.T) {
	cfg, r := fixtureCorpusPreparationResult(t)
	r.StageMillis = map[string]int64{"m": 1}
	base := len(preparationJSON(r))
	for _, tc := range []struct {
		name  string
		bytes int
		want  bool
	}{
		{"JSON-plus-LF-at-physical-limit", preparationMaxResult - 1, true},
		{"JSON-alone-at-physical-limit", preparationMaxResult, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := r
			value.StageMillis = map[string]int64{strings.Repeat("m", tc.bytes-base+1): 1}
			raw := preparationJSON(value)
			if len(raw) != tc.bytes {
				t.Fatal("frame boundary fixture")
			}
			encoded, err := encodePreparationResult(value)
			if (err == nil) != tc.want || tc.want && !bytes.Equal(encoded, raw) {
				t.Fatal("physical frame encode boundary", err)
			}
			if _, err = parsePreparationResult(raw, cfg); (err == nil) != tc.want {
				t.Fatal("physical frame parse boundary", err)
			}
		})
	}
}

func TestNativePreparationReceiptExclusive(t *testing.T) {
	rc := nativePreparationReceipt{
		Schema: preparationReceiptSchema, ID: "neutral-prep-1",
		PlanningDigest: preparationDigest([]byte("p")), AttemptDigest: preparationDigest([]byte("a")),
		ConfigSHA256: preparationDigest([]byte("c")), TestSHA256: preparationDigest([]byte("t")),
		ImageSHA256: preparationDigest([]byte("i")), ScratchDevice: 7, ScratchInode: 0,
		ContainerID: fmt.Sprintf("%064x", 9), ResultSHA256: preparationDigest([]byte("r")),
		ExitCode: 0, Removed: true, Complete: true,
	}
	raw, e := json.Marshal(rc)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = parsePreparationReceipt(raw); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "receipt")
	if e := preparationReceipt(p, rc); e != nil {
		t.Fatal(e)
	}
	if e := preparationReceipt(p, rc); e == nil {
		t.Fatal("overwrote receipt")
	}
	b, e := os.ReadFile(p)
	if e != nil || !bytes.Equal(b, raw) {
		t.Fatal("receipt bytes", e)
	}
}

// parseSeedCommit accepts exactly the 40 lowercase-hex authoritative commit the
// seed provisions. The commit is the only variable seed input: repository and
// remote are the fixed neutral constants, so the seed never needs a full
// pre-validated config. Requiring one would be circular, because that config's
// source.incarnation/source.generation are exactly what the seed observes back.
func parseSeedCommit(s string) (string, error) {
	if len(s) != 40 {
		return "", errors.New("seed commit must be exactly 40 lowercase hex characters")
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", errors.New("seed commit must be exactly 40 lowercase hex characters")
		}
	}
	return s, nil
}

// TestNativePreparationSeed is the opt-in pristine real-store seed entrypoint.
// Ordinary runs skip it; it executes only when an operator supplies the seed
// role, the reviewed neutral source commit, a create-only provenance path, and an
// isolated PHEBS_PREPARATION_STORE endpoint. It provisions indexed source
// metadata and OBSERVES the store-minted incarnation and computed generation back
// to the operator; it installs no profile, builds no search index, and invents no
// incarnation or epoch. The observed Source is the provenance the later config
// authoring step binds, which is why the seed takes only the commit and never a
// full config.
func TestNativePreparationSeed(t *testing.T) {
	if *preparationRole != "seed" {
		t.Skip("neutral preparation seed is opt-in")
	}
	commit, e := parseSeedCommit(*preparationCommit)
	if e != nil {
		t.Fatal(e)
	}
	repo, remote, e := preparationSeedLocation(commit, *preparationCorpus)
	if e != nil {
		t.Fatal(e)
	}
	if *preparationSeedOut == "" {
		t.Fatal("seed role requires -typed-preparation-seed-out provenance path")
	}
	endpoint := os.Getenv("PHEBS_PREPARATION_STORE")
	if endpoint == "" {
		t.Fatal("seed role requires PHEBS_PREPARATION_STORE isolated endpoint")
	}
	ctx := t.Context()
	s, e := openPreparationStore(ctx, endpoint)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(context.Background()) }()
	got, e := seedPreparationSource(ctx, s, repo, remote, commit)
	if e != nil {
		t.Fatal(e)
	}
	// Record the observed source-free provenance create-only and fsynced; a lost
	// reply or a second seed refuses rather than overwriting the first observation.
	if e = preparationReceipt(*preparationSeedOut, preparationSeedProvenance{
		Schema: preparationSeedProvenanceSchema, Source: got,
	}); e != nil {
		t.Fatal(e)
	}
}

// openPreparationStore connects only to an operator-supplied isolated endpoint.
// It never starts an engine, substitutes a default daemon or rewrites a schema.
func openPreparationStore(ctx context.Context, endpoint string) (*store.Surreal, error) {
	return store.Open(ctx, endpoint, "root", os.Getenv("PHEBS_PREPARATION_STORE_PASS"), "t454", "neutral")
}

// seedPreparationSource establishes and observes the real neutral fixture source
// identity through the existing store setters. It provisions indexed source
// metadata only; it builds no search index and invents no incarnation or epoch.
// It returns the store-minted Source: GetTypedSource lazily CAS-mints the
// incarnation and computes the generation over repository/incarnation/commit/
// epoch, so neither is offline-authorable and the observed record is the only
// authoritative provenance. Re-seeding the same repository can advance the store
// epoch and therefore the computed generation.
func preparationSeedLocation(commit, cohort string) (string, string, error) {
	if _, e := parseSeedCommit(commit); e != nil {
		return "", "", e
	}
	if cohort == "" {
		return preparationRepo, preparationRemote, nil
	}
	if _, e := preparationCorpusRoots(cohort); e != nil || commit != preparationCorpusCommit {
		return "", "", errors.New("frozen corpus seed selector")
	}
	return preparationCorpusRepo, "https://" + preparationCorpusRepo, nil
}

func seedPreparationSource(ctx context.Context, s *store.Surreal, repo, remote, commit string) (typedindex.Source, error) {
	if err := s.UpsertRepo(ctx, store.Repo{Name: repo, CloneURL: remote, DefaultBranch: "main", IsPublic: true}); err != nil {
		return typedindex.Source{}, err
	}
	if err := s.SetRepoIndexed(ctx, repo, commit, time.Now()); err != nil {
		return typedindex.Source{}, err
	}
	got, err := s.GetTypedSource(ctx, repo)
	if err != nil {
		return typedindex.Source{}, err
	}
	if got.Repository != repo || got.Commit != commit || got.Validate() != nil {
		return typedindex.Source{}, errors.New("seed source identity mismatch")
	}
	return got, nil
}
