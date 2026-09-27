package provider

import (
	"bytes"
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
const preparationRepo = "example.invalid/phebs-native-neutral"
const preparationRemote = "https://example.invalid/phebs-native-neutral"
const preparationRoot = "//lib:lib"
const preparationPlanningDomain = "phebs-typed-preparation-planning-v1\x00"
const preparationAttemptDomain = "phebs-typed-preparation-attempt-v1\x00"

const preparationMaxConfig = 16384
const preparationMaxReceipt = 128 << 10

// preparationRetainedCeiling bounds raw cquery+aquery+projection bytes retained
// by one preparation. It is a smaller neutral transport limit, never a change to
// a production planner bound. preparationMaxResult mirrors the existing worker
// output ceiling that the JSON/base64 expansion must also fit.
const preparationRetainedCeiling = 8 << 20
const preparationMaxResult = typedsandbox.OutputBytes

var preparationID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var preparationConfigPath = flag.String("typed-preparation-config", "", "reviewed private neutral preparation config")
var preparationRole = flag.String("typed-preparation-role", "", "opt-in preparation role: seed or host")

// nativePreparationConfig binds exactly one preparation ID, the fixed neutral
// source/module/remote/sole-root, and the exact source, inventory, profile,
// helper, image and formatter identities. It accepts no command, environment,
// path, root or cap override; the fixed caps come from typedsandbox constants.
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
	SelectionSHA256  string            `json:"selection_sha256"`
	ImageSHA256      string            `json:"image_sha256"`
	MkfsSHA256       string            `json:"mkfs_sha256"`
	DeploymentSHA256 string            `json:"deployment_sha256"`
	ProfileEpoch     int64             `json:"profile_epoch"`
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
	ScratchDevice  uint64 `json:"scratch_device"`
	ScratchInode   uint64 `json:"scratch_inode"`
	ContainerID    string `json:"container_id"`
	ResultSHA256   string `json:"result_sha256"`
	ExitCode       int    `json:"exit_code"`
	Removed        bool   `json:"removed"`
	Complete       bool   `json:"complete"`
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
	return preparationDigest([]byte(preparationPlanningDomain + c.ID))
}
func (c nativePreparationConfig) attemptDigest() string {
	return preparationDigest([]byte(preparationAttemptDomain + c.ID))
}

func parsePreparationConfig(raw []byte) (nativePreparationConfig, error) {
	var c nativePreparationConfig
	if e := preparationDecode(raw, preparationMaxConfig, &c); e != nil {
		return c, e
	}
	if c.Schema != preparationSchema || !preparationID.MatchString(c.ID) {
		return c, errors.New("neutral preparation contract")
	}
	if c.Source.Repository != preparationRepo || c.Source.Validate() != nil {
		return c, errors.New("neutral preparation source")
	}
	if c.Module != preparationRepo || c.Remote != preparationRemote || c.Root != preparationRoot {
		return c, errors.New("neutral preparation module/remote/root")
	}
	if c.ProfileEpoch < 1 {
		return c, errors.New("neutral preparation epoch")
	}
	b, e := hex.DecodeString(c.SourceCommit)
	if e != nil || len(b) != 20 || hex.EncodeToString(b) != c.SourceCommit {
		return c, errors.New("neutral preparation source commit")
	}
	for _, h := range []string{c.TestSHA256, c.HelperSHA256, c.EngineSHA256, c.InventorySHA256, c.ProfileSHA256, c.SelectionSHA256, c.ImageSHA256, c.MkfsSHA256, c.DeploymentSHA256} {
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

func parsePreparationResult(raw []byte) (nativePreparationResult, error) {
	var r nativePreparationResult
	if e := preparationDecode(raw, preparationMaxResult, &r); e != nil {
		return r, e
	}
	if r.Schema != preparationResultSchema || !preparationID.MatchString(r.ID) {
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
	if retained > preparationRetainedCeiling {
		return r, errors.New("neutral preparation retained ceiling")
	}
	return r, nil
}

// encodePreparationResult refuses a marshaled result that would not fit the
// existing worker output ceiling, returning only a bounded classified error.
func encodePreparationResult(r nativePreparationResult) ([]byte, error) {
	raw := preparationJSON(r)
	if int64(len(raw)) > preparationMaxResult {
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
	if rc.ScratchDevice == 0 || rc.ScratchInode == 0 {
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
		SelectionSHA256: h, ImageSHA256: h, MkfsSHA256: h, DeploymentSHA256: h, ProfileEpoch: 1,
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
		{"epoch", func(c *nativePreparationConfig) { c.ProfileEpoch = 0 }},
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
	if _, e = parsePreparationResult(raw); e != nil {
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
			if _, e = parsePreparationResult(raw); e == nil {
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
	if _, e = parsePreparationResult(raw); e != nil {
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

func TestNativePreparationReceiptExclusive(t *testing.T) {
	rc := nativePreparationReceipt{
		Schema: preparationReceiptSchema, ID: "neutral-prep-1",
		PlanningDigest: preparationDigest([]byte("p")), AttemptDigest: preparationDigest([]byte("a")),
		ConfigSHA256: preparationDigest([]byte("c")), TestSHA256: preparationDigest([]byte("t")),
		ImageSHA256: preparationDigest([]byte("i")), ScratchDevice: 7, ScratchInode: 3,
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

// TestNativePreparationSeed is the opt-in pristine real-store seed entrypoint.
// Ordinary runs skip it; it executes only when an operator supplies a reviewed
// config and the seed role. It provisions indexed source metadata and installs
// the final profile through the real store; it is not a claim that a search
// index was built and adds no fake incarnation/epoch setter.
func TestNativePreparationSeed(t *testing.T) {
	if *preparationRole != "seed" || *preparationConfigPath == "" {
		t.Skip("neutral preparation seed is opt-in")
	}
	raw, e := os.ReadFile(*preparationConfigPath)
	if e != nil {
		t.Fatal(e)
	}
	c, e := parsePreparationConfig(raw)
	if e != nil {
		t.Fatal(e)
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
	if e = seedPreparationSource(ctx, s, c); e != nil {
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
func seedPreparationSource(ctx context.Context, s *store.Surreal, c nativePreparationConfig) error {
	commit := c.Source.Commit
	if err := s.UpsertRepo(ctx, store.Repo{Name: c.Source.Repository, CloneURL: c.Remote, DefaultBranch: "main", IsPublic: true}); err != nil {
		return err
	}
	if err := s.SetRepoIndexed(ctx, c.Source.Repository, commit, time.Now()); err != nil {
		return err
	}
	got, err := s.GetTypedSource(ctx, c.Source.Repository)
	if err != nil {
		return err
	}
	if got.Repository != c.Source.Repository || got.Commit != commit || got.Validate() != nil {
		return errors.New("seed source identity mismatch")
	}
	return nil
}
