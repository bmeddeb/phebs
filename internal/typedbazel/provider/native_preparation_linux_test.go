//go:build linux

package provider

// Neutral preparation dispatch and the one finite host preparation. This file is
// test-only: it adds nothing to production cmd dispatch or provider
// registration. TestMain intercepts the exact internal role tokens the managed
// sandbox already uses and otherwise runs ordinary tests, so its default
// absence changes no existing behavior. The host preparation is opt-in through
// the reviewed config/role flags and skips by default; it has never run here and
// is cross-compile verified only on this darwin development host.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"golang.org/x/sys/unix"
)

// preparationSocket is the designated dedicated daemon socket. Preparation never
// substitutes a default daemon or changes the VM/mount posture.
const preparationSocket = "/var/run/docker.sock"

// preparationControlsSchema is the existing supervisor seal schema. The
// preparation seal carries only the supervisor's required v3 identity/allowance
// subset plus strictly decoded preparation-owned references; it is not a
// typedworkspace.ControlSpec or a production complete worker snapshot.
const preparationControlsSchema = "phebs-typed-worker-controls-v3"

// preparationSeal is the bounded control seal. The host marshals it and the
// worker strictly decodes the identical shape, so the canonical round trip binds
// the preparation references under the transferred supervisor seal hash.
type preparationSeal struct {
	Schema   string `json:"schema"`
	Identity struct {
		Allowance       json.RawMessage `json:"allowance"`
		Phase           string          `json:"phase"`
		PlanningDigest  string          `json:"planning_digest"`
		AttemptDigest   string          `json:"attempt_digest"`
		RequestDigest   string          `json:"request_digest"`
		ConfigSHA256    string          `json:"config_sha256"`
		InventorySHA256 string          `json:"inventory_sha256"`
		ProfileSHA256   string          `json:"profile_sha256"`
		TestSHA256      string          `json:"test_sha256"`
		ImageSHA256     string          `json:"image_sha256"`
	} `json:"identity"`
}

// TestMain dispatches the exact internal roles the managed sandbox re-execs into
// the helper binary. Only the three preparation roles are handled; every other
// internal role token is rejected, and ordinary test arguments fall through to
// the default runner. The supervisor's caller must immediately exit with its
// code: namespace PID1 exit is the descendant shutdown fence.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case typedsandbox.SupervisorCommand:
			os.Exit(typedsandbox.Supervisor())
		case typedsandbox.WorkerCommand:
			os.Exit(preparationWorkerMain())
		case "__plan_helper":
			// Child roles intentionally have distinct environments from the
			// primary worker; the linux build tag supplies the OS guard and this
			// retains the existing arm64/UID65534/no-PID1 child guard.
			if runtime.GOARCH != "arm64" || os.Getuid() != 65534 || os.Getgid() != 65534 || os.Getpid() == 1 {
				os.Exit(125)
			}
			os.Exit(preparationExit(planner.RunHelper(os.Args[2:])))
		default:
			if strings.HasPrefix(os.Args[1], "__") {
				os.Exit(125)
			}
		}
	}
	os.Exit(m.Run())
}

func preparationExit(err error) int {
	if err != nil {
		return 125
	}
	return 0
}

// preparationWorkerMain is the exact __typed_worker role. It authenticates the
// opaque process token, then loads only the closed preparation controls. It
// never mints authority from a request and never runs provider.Run.
func preparationWorkerMain() int {
	ctx := context.Background()
	invocation, err := typedsandbox.ReadWorkerInvocation(ctx)
	if err != nil {
		return 125
	}
	allowance, phase, request, seal, err := invocation.Binding(ctx)
	if err != nil {
		return 125
	}
	workCtx, cancel, err := typedsandbox.AllowanceContext(ctx, allowance)
	if err != nil {
		return 125
	}
	defer cancel()
	raw, err := runPreparationWorker(workCtx, allowance, phase, request, seal)
	if err != nil {
		return 125
	}
	if int64(len(raw))+1 > int64(typedsandbox.OutputBytes) {
		return 125
	}
	out := append(raw, '\n')
	if n, writeErr := os.Stdout.Write(out); writeErr != nil || n != len(out) {
		return 125
	}
	return 0
}

// runPreparationWorker calls the production planning functions directly with
// only the profile/inventory fields of an internal Invocation. Zero parent or
// execution values are admitted or treated as authority. It captures the raw
// cquery/aquery/projection bytes under the neutral retained ceiling and emits
// the bounded result; it executes no packages.Load, driver or indexer.
func runPreparationWorker(ctx context.Context, allowance typedsandbox.Allowance, phase, request, seal string) ([]byte, error) {
	cfg, inventory, profile, err := loadPreparationControls(ctx, allowance, phase, request, seal)
	if err != nil {
		return nil, err
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil || info.Path == "" {
		return nil, errors.New("neutral preparation helper identity")
	}
	inv := Invocation{Profile: profile, Inventory: inventory}
	roots := []string{cfg.Root}
	started := time.Now()
	stages := map[string]int64{}
	mark := func(name string, at time.Time) { stages[name] = time.Since(at).Milliseconds() }

	capture := &preparationCapture{ceiling: preparationRetainedCeiling}
	defer capture.close()

	at := time.Now()
	if err = verifyToolsWithHelperMain(ctx, inv, info.Path); err != nil {
		return nil, err
	}
	helper, err := inventoryFile(inventory, typedindex.ManagedHelperFile)
	if err != nil || helper.Digest != cfg.TestSHA256 {
		return nil, errors.New("neutral preparation helper binding")
	}
	mark("tools", at)

	at = time.Now()
	originals, err := materialize(ctx, inv)
	if err != nil {
		return nil, err
	}
	mark("materialize", at)

	at = time.Now()
	if err = setupCompiler(ctx, inventory); err != nil {
		return nil, err
	}
	mark("compiler", at)

	at = time.Now()
	plan, err := buildPlan(ctx, roots, capture.command, ensureQuiescentWorker, evictCompilerCache, capture.read)
	if err != nil {
		return nil, err
	}
	mark("plan", at)

	at = time.Now()
	if err = verifyRules(ctx, inventory); err != nil {
		return nil, err
	}
	if err = verifyWorkspace(ctx, plan, originals); err != nil {
		return nil, err
	}
	if err = verifySDK(plan); err != nil {
		return nil, err
	}
	mark("verify", at)

	at = time.Now()
	if err = ensureQuiescentWorker(); err != nil {
		return nil, err
	}
	if err = verifyRules(ctx, inventory); err != nil {
		return nil, err
	}
	if err = verifyWorkspace(ctx, planner.Plan{}, originals); err != nil {
		return nil, err
	}
	mark("final", at)

	result, err := capture.result(cfg, allowance, stages, time.Since(started))
	if err != nil {
		return nil, err
	}
	return encodePreparationResult(result)
}

// loadPreparationControls reads the fixed read-only /controls mount under the
// transferred supervisor seal hash. It binds the complete fixed
// config/inventory/profile reference set and exact digests; a mismatch is a
// preparation STOP, never a fallback.
func loadPreparationControls(ctx context.Context, allowance typedsandbox.Allowance, phase, request, seal string) (nativePreparationConfig, typedindex.Inventory, typedindex.Profile, error) {
	var (
		empty    nativePreparationConfig
		emptyInv typedindex.Inventory
		emptyPro typedindex.Profile
	)
	if phase != typedsandbox.ControlPlan || request != allowance.PlanningDigest {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation control phase")
	}
	sealRaw, err := readBounded("/controls/"+typedsandbox.ControlSealFile, typedsandbox.MaxControlSealBytes)
	if err != nil || preparationDigest(sealRaw) != seal {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation seal digest")
	}
	var s preparationSeal
	if err = preparationDecode(sealRaw, typedsandbox.MaxControlSealBytes, &s); err != nil {
		return empty, emptyInv, emptyPro, err
	}
	transferred, err := typedsandbox.DecodeAllowance(s.Identity.Allowance)
	if err != nil || transferred != allowance || s.Schema != preparationControlsSchema {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation seal allowance")
	}
	if s.Identity.Phase != phase || s.Identity.RequestDigest != request || s.Identity.PlanningDigest != allowance.PlanningDigest || s.Identity.AttemptDigest != allowance.AttemptDigest {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation seal identity")
	}
	cfgRaw, err := readBounded("/controls/config.json", preparationMaxConfig)
	if err != nil || preparationDigest(cfgRaw) != s.Identity.ConfigSHA256 {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation config reference")
	}
	cfg, err := parsePreparationConfig(cfgRaw)
	if err != nil {
		return empty, emptyInv, emptyPro, err
	}
	if cfg.planningDigest() != allowance.PlanningDigest || cfg.attemptDigest() != allowance.AttemptDigest {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation control derivation")
	}
	invRaw, err := readBounded("/controls/inventory.json", typedindex.MaxInventoryBytes)
	if err != nil || preparationDigest(invRaw) != s.Identity.InventorySHA256 || preparationDigest(invRaw) != cfg.InventorySHA256 {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation inventory reference")
	}
	inventory, err := typedindex.DecodeInventory(ctx, invRaw, cfg.InventorySHA256)
	if err != nil {
		return empty, emptyInv, emptyPro, err
	}
	profRaw, err := readBounded("/controls/profile.json", typedindex.MaxProfileBytes)
	if err != nil || preparationDigest(profRaw) != s.Identity.ProfileSHA256 || preparationDigest(profRaw) != cfg.ProfileSHA256 {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation profile reference")
	}
	profile, err := typedindex.DecodeProfile(ctx, profRaw)
	if err != nil || profile.Digest() != cfg.ProfileSHA256 {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation profile identity")
	}
	if s.Identity.TestSHA256 != cfg.TestSHA256 || s.Identity.ImageSHA256 != cfg.ImageSHA256 {
		return empty, emptyInv, emptyPro, errors.New("neutral preparation executable reference")
	}
	return cfg, inventory, profile, ctx.Err()
}

// preparationCapture observes buildPlan's existing callbacks without changing
// flags or running an extra planner. Execution delegates to the unchanged
// runCommand and projection reads to the same bounded, no-special-file,
// root-relative reader nativePlan uses; returned bytes are captured under the
// neutral retained ceiling.
type preparationCapture struct {
	ceiling  int64
	retained int64
	index    int
	exe      [3]string
	args     [3][]string
	stdout   [2][]byte
	proj     map[string][]byte
	buildOut struct {
		sha   string
		bytes int64
	}
	buildErr struct {
		sha   string
		bytes int64
	}
	root *os.Root
}

func (c *preparationCapture) close() {
	if c.root != nil {
		_ = c.root.Close()
	}
}

func (c *preparationCapture) command(ctx context.Context, executable string, args, env []string, budget *outputBudget) ([]byte, []byte, error) {
	stdout, stderr, err := runCommand(ctx, executable, args, env, budget)
	if err != nil {
		return stdout, stderr, err
	}
	i := c.index
	c.index++
	if i >= 3 {
		return nil, nil, errors.New("neutral preparation command count")
	}
	c.exe[i], c.args[i] = executable, args
	if i == 2 {
		c.buildOut.sha, c.buildOut.bytes = preparationDigest(stdout), int64(len(stdout))
		c.buildErr.sha, c.buildErr.bytes = preparationDigest(stderr), int64(len(stderr))
		return stdout, stderr, nil
	}
	if c.retained+int64(len(stdout)) > c.ceiling {
		return nil, nil, typedindex.Capacity
	}
	c.retained += int64(len(stdout))
	c.stdout[i] = stdout
	return stdout, stderr, nil
}

func (c *preparationCapture) read(name string, limit int64) ([]byte, error) {
	if c.root == nil {
		root, err := os.OpenRoot(launcher.ExecRoot)
		if err != nil {
			return nil, err
		}
		c.root = root
	}
	st, err := c.root.Lstat(name)
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, typedindex.Invalid
	}
	f, err := c.root.Open(name)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	err = errors.Join(err, f.Close())
	if err != nil || int64(len(b)) > limit {
		return nil, typedindex.Invalid
	}
	if c.retained+int64(len(b)) > c.ceiling {
		return nil, typedindex.Capacity
	}
	c.retained += int64(len(b))
	if c.proj == nil {
		c.proj = map[string][]byte{}
	}
	c.proj[name] = b
	return b, nil
}

func (c *preparationCapture) result(cfg nativePreparationConfig, allowance typedsandbox.Allowance, stages map[string]int64, wall time.Duration) (nativePreparationResult, error) {
	if c.index != 3 || len(c.stdout[0]) == 0 || len(c.stdout[1]) == 0 || len(c.proj) == 0 || c.buildOut.sha == "" {
		return nativePreparationResult{}, errors.New("neutral preparation incomplete capture")
	}
	stages["wall"] = wall.Milliseconds()
	r := nativePreparationResult{
		Schema: preparationResultSchema, ID: cfg.ID,
		PlanningDigest: allowance.PlanningDigest, AttemptDigest: allowance.AttemptDigest,
		Cquery: c.stdout[0], Aquery: c.stdout[1], Projections: c.proj,
		BuildStdoutSHA256: c.buildOut.sha, BuildStdoutBytes: c.buildOut.bytes,
		BuildStderrSHA256: c.buildErr.sha, BuildStderrBytes: c.buildErr.bytes,
		RetainedBytes:   c.retained,
		SourceSHA256:    preparationDigest(preparationJSON(cfg.Source)),
		InventorySHA256: cfg.InventorySHA256, ProfileSHA256: cfg.ProfileSHA256,
		StageMillis: stages, ExitCode: 0,
	}
	for i := range 3 {
		r.CommandDigests[i] = preparationDigest([]byte(c.exe[i] + "\x00" + strings.Join(c.args[i], "\x00")))
	}
	return r, nil
}

// TestNativePreparationHost is the one finite host preparation. It is opt-in:
// ordinary runs skip it. It verifies the frozen staged artifacts and private
// custody, acquires the existing exclusive lifecycle guard, observes actual host
// capacity, provisions at most one exact scratch owner, begins the single
// original allowance, builds the bounded controls, runs one container, requires
// exact completion, independently reassembles the plan from the returned raw
// bytes, and retains the private result plus the small source-free receipt. It
// is a preparation step, never target evidence or permission to run acceptance.
func TestNativePreparationHost(t *testing.T) {
	if *preparationRole != "host" || *preparationConfigPath == "" {
		t.Skip("neutral preparation host is opt-in")
	}
	ctx := t.Context()
	cfgRaw, err := readBounded(*preparationConfigPath, preparationMaxConfig)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parsePreparationConfig(cfgRaw)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(*preparationConfigPath)
	invRaw, err := readBounded(filepath.Join(root, "inventory.json"), typedindex.MaxInventoryBytes)
	if err != nil || preparationDigest(invRaw) != cfg.InventorySHA256 {
		t.Fatal("neutral preparation staged inventory identity")
	}
	profRaw, err := readBounded(filepath.Join(root, "profile.json"), typedindex.MaxProfileBytes)
	if err != nil || preparationDigest(profRaw) != cfg.ProfileSHA256 {
		t.Fatal("neutral preparation staged profile identity")
	}
	planning, attempt := cfg.planningDigest(), cfg.attemptDigest()

	obs, err := typedsandbox.ObserveHostScratch(ctx, "")
	if err != nil || obs.Held || obs.Overflow || len(obs.Names) != 0 || obs.Selected != nil {
		t.Fatal("neutral preparation requires an empty native namespace")
	}
	gate := lifecycle.NewGate(typedsandbox.HostScratchBase)
	allowance, err := typedsandbox.BeginAllowance(ctx, planning, attempt)
	if err != nil {
		t.Fatal(err)
	}
	workCtx, cancel, err := typedsandbox.AllowanceContext(ctx, allowance)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	host := typedsandbox.HostScratchOptions{
		Base:          typedsandbox.HostBaseIdentity{Device: obs.Capacity.Device, Inode: obs.Capacity.Inode, BlockSize: obs.Capacity.BlockSize},
		RequestDigest: planning, AttemptDigest: attempt,
		Socket: preparationSocket, MkfsDigest: cfg.MkfsSHA256,
	}
	inputs := filepath.Join(root, "bundle")
	controls := filepath.Join(root, "controls-plan")
	recovery := typedsandbox.RecoveryOptions{Socket: preparationSocket, ImageID: cfg.ImageSHA256, Inputs: inputs, PlanningDigest: planning, AttemptDigest: attempt}
	defer func() {
		if err := cleanupPreparation(host, recovery, planning, attempt); err != nil {
			t.Error(err)
		}
	}()

	receipt, err := typedsandbox.PrepareHostScratch(workCtx, host, gate)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := typedsandbox.VerifyHostScratch(workCtx, host)
	if err != nil || observed != receipt {
		t.Fatal("neutral preparation scratch verification")
	}
	sealDigest, err := buildPreparationControls(controls, cfg, allowance, cfgRaw, invRaw, profRaw, receipt.Authority)
	if err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if unix.Stat(controls, &st) != nil {
		t.Fatal("neutral preparation control directory stat")
	}
	control := typedsandbox.ControlIdentity{
		Phase: typedsandbox.ControlPlan, PlanningDigest: planning, AttemptDigest: attempt,
		RequestDigest: planning, Device: uint64(st.Dev), Inode: st.Ino, SealDigest: sealDigest,
	}
	if control.Validate() != nil {
		t.Fatal("neutral preparation control identity")
	}
	options := typedsandbox.Options{Socket: preparationSocket, ImageID: cfg.ImageSHA256, Inputs: inputs, Controls: controls, Control: control, Allowance: allowance}

	result, err := typedsandbox.Run(workCtx, options, receipt.Authority)
	if result.StopReason == "wall_limit" {
		err = errors.Join(typedindex.WallLimit, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = typedsandbox.VerifyCompletion(allowance, control, result); err != nil {
		t.Fatal(err)
	}
	body, ok := trimPreparationFrame(result.Stdout)
	if !ok {
		t.Fatal("neutral preparation worker frame")
	}
	parsed, err := parsePreparationResult(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != cfg.ID || parsed.PlanningDigest != planning || parsed.AttemptDigest != attempt ||
		parsed.InventorySHA256 != cfg.InventorySHA256 || parsed.ProfileSHA256 != cfg.ProfileSHA256 ||
		parsed.SourceSHA256 != preparationDigest(preparationJSON(cfg.Source)) {
		t.Fatal("neutral preparation result identity")
	}
	if len(parsed.Cquery) == 0 || len(parsed.Aquery) == 0 || len(parsed.Projections) == 0 {
		t.Fatal("neutral preparation raw buffers absent")
	}
	// Independent host reassembly from the returned raw bytes; the parsed worker
	// Plan alone is not raw provenance.
	plan, err := planner.AssembleRootsV2(parsed.Cquery, parsed.Aquery, parsed.Projections, []string{cfg.Root})
	if err != nil || len(plan.Targets) == 0 {
		t.Fatal("neutral preparation host reassembly")
	}
	if err = writePreparationResult(filepath.Join(root, "result.json"), body); err != nil {
		t.Fatal(err)
	}
	complete := result.ExitCode == 0 && result.Removed && result.StopReason == "" && !result.OOMKilled
	record := nativePreparationReceipt{
		Schema: preparationReceiptSchema, ID: cfg.ID,
		PlanningDigest: planning, AttemptDigest: attempt,
		ConfigSHA256: preparationDigest(cfgRaw), TestSHA256: cfg.TestSHA256, ImageSHA256: cfg.ImageSHA256,
		ScratchDevice: uint64(receipt.Authority.DeviceMajor), ScratchInode: uint64(receipt.Authority.DeviceMinor),
		ContainerID: result.ContainerID, ResultSHA256: preparationDigest(body),
		ExitCode: result.ExitCode, Removed: result.Removed, Complete: complete,
	}
	if !complete {
		t.Fatal("neutral preparation did not complete")
	}
	if err = preparationReceipt(filepath.Join(root, "receipt.json"), record); err != nil {
		t.Fatal(err)
	}
}

// trimPreparationFrame strips exactly one trailing newline from the worker's
// framed stdout so the canonical result bytes round trip.
func trimPreparationFrame(stdout []byte) ([]byte, bool) {
	if len(stdout) == 0 || stdout[len(stdout)-1] != '\n' {
		return nil, false
	}
	return stdout[:len(stdout)-1], true
}

// buildPreparationControls creates the read-only controls-plan sibling directory
// with the bounded seal, canonical scratch authority and the fixed preparation
// references. It is create-only and refuses an existing directory.
func buildPreparationControls(dir string, cfg nativePreparationConfig, allowance typedsandbox.Allowance, cfgRaw, invRaw, profRaw []byte, authority typedsandbox.ScratchAuthority) (string, error) {
	allowanceRaw, err := typedsandbox.EncodeAllowance(allowance)
	if err != nil {
		return "", err
	}
	scratchRaw, err := typedsandbox.EncodeScratchAuthority(authority)
	if err != nil {
		return "", err
	}
	var s preparationSeal
	s.Schema = preparationControlsSchema
	s.Identity.Allowance = allowanceRaw
	s.Identity.Phase = typedsandbox.ControlPlan
	s.Identity.PlanningDigest = allowance.PlanningDigest
	s.Identity.AttemptDigest = allowance.AttemptDigest
	s.Identity.RequestDigest = allowance.PlanningDigest
	s.Identity.ConfigSHA256 = preparationDigest(cfgRaw)
	s.Identity.InventorySHA256 = preparationDigest(invRaw)
	s.Identity.ProfileSHA256 = preparationDigest(profRaw)
	s.Identity.TestSHA256 = cfg.TestSHA256
	s.Identity.ImageSHA256 = cfg.ImageSHA256
	sealRaw := preparationJSON(s)
	if len(sealRaw) > typedsandbox.MaxControlSealBytes {
		return "", errors.New("neutral preparation seal bound")
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	for _, f := range []struct {
		name string
		raw  []byte
	}{
		{typedsandbox.ControlSealFile, sealRaw},
		{typedsandbox.ScratchAuthorityFile, scratchRaw},
		{"config.json", cfgRaw},
		{"inventory.json", invRaw},
		{"profile.json", profRaw},
	} {
		if err = writeControlFile(filepath.Join(dir, f.name), f.raw); err != nil {
			return "", err
		}
	}
	if err = os.Chmod(dir, 0555); err != nil {
		return "", err
	}
	return preparationDigest(sealRaw), nil
}

// writeControlFile creates a read-only single-write control file. It is created
// at 0600, fsynced, then reduced to 0444 so the writer never depends on opening
// a read-only inode for writing.
func writeControlFile(name string, raw []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Chmod(name, 0444)
}

// writePreparationResult retains the private raw result create-only and fsynced.
// It is separate retained provisioning evidence and is never exported publicly.
func writePreparationResult(name string, raw []byte) error {
	if int64(len(raw)) > preparationMaxResult {
		return errors.New("neutral preparation result bound")
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// cleanupPreparation runs the exact recorded container recovery first and
// requires absence before the normal scratch unmount/detach/removal, mirroring
// the production cleanNative discipline. An ambiguous or held owner is left for
// the operator rather than force-removed.
func cleanupPreparation(host typedsandbox.HostScratchOptions, recovery typedsandbox.RecoveryOptions, planning, attempt string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 20*time.Second)
	defer cancel()
	if err := typedsandbox.QuiescentAttempt(ctx, recovery); err != nil {
		return err
	}
	name, err := typedsandbox.HostScratchRootName(planning, attempt)
	if err != nil {
		return err
	}
	obs, err := typedsandbox.ObserveHostScratch(ctx, name)
	if err != nil {
		return err
	}
	if len(obs.Names) == 0 && obs.Selected == nil && !obs.Held {
		return nil
	}
	if obs.Held || len(obs.Names) != 1 || obs.Names[0] != name || obs.Selected == nil || obs.Selected.Options != host {
		return errors.New("neutral preparation cleanup held ambiguous custody")
	}
	if err = typedsandbox.CleanupHostScratch(ctx, host); err != nil {
		return err
	}
	final, err := typedsandbox.ObserveHostScratch(ctx, "")
	if err != nil {
		return err
	}
	if final.Held || final.Overflow || len(final.Names) != 0 || final.Selected != nil {
		return errors.New("neutral preparation cleanup left residue")
	}
	return nil
}
