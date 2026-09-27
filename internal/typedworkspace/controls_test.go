//go:build linux

package typedworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

func controlsFixture(t *testing.T) (publicationFixture, OwnerIdentity, OwnerManifest, ControlSpec, Receipt) {
	t.Helper()
	f, source, raw, inventory, id, m := ownerFixture(t)
	attempt := filepath.Join(f.dir, id.RelativeName())
	copied, err := Copy(t.Context(), source, attempt, inventory, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	m, err = SaveOwnerInputs(t.Context(), f.dir, id, m.Digest(), raw, copied, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	tool := typedindex.Tool{Version: "0.2.7", Digest: digest([]byte("tool"))}
	profile, err := typedindex.DecodeProfile(t.Context(), publicationJSON(t, typedindex.ProfileDefinition{Schema: typedindex.ProfileSchema, Name: "reduced", Provider: typedindex.ProviderID, Tools: typedindex.Tools{Bazel: tool, RulesGo: tool, Go: tool, Driver: tool, Indexer: tool, Planner: tool, Launcher: tool}, Config: typedindex.ReducedConfig(), Policy: typedindex.MeasuredPolicy(), BundleDigest: inventory.Digest(), ImageDigest: digest([]byte("image"))}))
	if err != nil {
		t.Fatal(err)
	}
	root, err := typedsandbox.HostScratchRootName(id.PlanningDigest, id.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic trusted receipt tests only binding. No formatter, mount or device
	// has run; this is explicitly not a VerifyHostScratch/native proof.
	host := typedsandbox.HostScratchReceipt{Schema: "phebs-typed-host-scratch-v3", Options: typedsandbox.HostScratchOptions{Base: typedsandbox.HostBaseIdentity{Device: m.Directory.Device, Inode: m.Directory.Inode, BlockSize: 4096}, RequestDigest: id.PlanningDigest, AttemptDigest: id.AttemptDigest, Socket: "/run/docker.sock", MkfsDigest: digest([]byte("formatter"))}, Authority: typedsandbox.ScratchAuthority{Source: typedsandbox.HostScratchBase + "/" + root + "/scratch", DeviceMajor: 7, DeviceMinor: 3, BlockSize: 4096, Blocks: 100, Inodes: typedsandbox.ScratchInodes, ImageBytes: typedsandbox.ScratchBytes / 4096 * 4096}, ObservedDirectIO: true}
	allowance, err := typedsandbox.BeginAllowance(t.Context(), id.PlanningDigest, id.AttemptDigest)
	if err != nil {
		t.Fatal(err)
	}
	return f, id, m, ControlSpec{Allowance: allowance, Phase: ControlsPlanning, Parent: f.parent, Profile: profile, Scratch: host}, copied
}
func executionControls(f publicationFixture, s ControlSpec) ControlSpec {
	s.Phase = ControlsExecution
	s.Execution = f.execution
	s.Plan = f.plan
	return s
}

func TestControlsBothPhasesAndInputImmutability(t *testing.T) {
	f, id, m, s, input := controlsFixture(t)
	inv, _, err := LoadOwnerInputs(t.Context(), f.dir, id)
	if err != nil {
		t.Fatal(err)
	}
	attempt := filepath.Join(f.dir, id.RelativeName())
	original, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []ControlSpec{s, executionControls(f, s)} {
		ref, err := InstallControls(t.Context(), f.dir, id, spec, f.gate)
		if err != nil {
			t.Fatal(err)
		}
		if ref.Identity.Phase != spec.Phase || ref.Seal.Bytes > MaxControlSealBytes {
			t.Fatal(ref)
		}
		handle, err := OpenControls(t.Context(), f.dir, id, spec, ref)
		if err != nil {
			t.Fatal(err)
		}
		if handle.Reference() != ref || handle.Path() != filepath.Join(attempt, controlName(spec.Phase)) {
			t.Fatal("scalar adapter")
		}
		// A live child pin must not hold the global namespace lock.
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		release, e := AcquirePublicationMutation(ctx, f.dir)
		cancel()
		if e != nil {
			t.Fatal("global namespace retained", e)
		}
		release()
		a, err := RetirementAuthority(m)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel = context.WithTimeout(t.Context(), 40*time.Millisecond)
		r, e := DrainOwner(ctx, f.dir, a)
		cancel()
		if e == nil || r.Deleted != 0 {
			t.Fatal("live control pin bypassed", r, e)
		}
		if err = handle.Close(); err != nil {
			t.Fatal(err)
		}
		if handle.Path() != "" || handle.Reference() != (ControlRef{}) || handle.Close() != nil {
			t.Fatal("closed handle")
		}
		if _, err = InstallControls(t.Context(), f.dir, id, spec, f.gate); err == nil {
			t.Fatal("phase overwritten")
		}
		if err = Verify(t.Context(), attempt, inv, input); err != nil {
			t.Fatal("inputs changed", err)
		}
	}
	_, after, err := LoadOwnerInputs(t.Context(), f.dir, id)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(after)
	if !bytes.Equal(original, got) {
		t.Fatal("input receipt changed")
	}
	if loaded, err := LoadOwner(t.Context(), f.dir, id); err != nil || loaded.Digest() != m.Digest() {
		t.Fatal("owner revision changed", err)
	}
	// Current publication can advance with both snapshots retained and charged.
	publication, err := InstallPublication(t.Context(), attempt, f.parent, f.execution, f.plan, f.bundle, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	m, err = SaveOwnerPublication(t.Context(), f.dir, id, m.Digest(), f.parent, f.execution, publication, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := RetirementAuthority(m)
	if err != nil {
		t.Fatal(err)
	}
	finishDrain(t, f.dir, authority)
}

func TestControlsTrustAndBoundsBeforeGrowth(t *testing.T) {
	f, id, _, s, _ := controlsFixture(t)
	for _, tc := range []string{"phase", "parent", "profile", "plan-in-planning", "execution-parent", "host-attempt", "host-base", "host-DIO", "host-source", "nil-gate", "cancel", "allowance-attempt", "allowance-deadline", "allowance-planning-output"} {
		t.Run(tc, func(t *testing.T) {
			spec := s
			gate := f.gate
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch tc {
			case "allowance-attempt":
				spec.Allowance.AttemptDigest = digest([]byte("other"))
			case "allowance-deadline":
				spec.Allowance.Deadline++
			case "allowance-planning-output":
				spec.Allowance.WorkerBytesUsed = 1
			case "phase":
				spec.Phase = "other"
			case "parent":
				spec.Parent = typedindex.Admission{}
			case "profile":
				spec.Profile = typedindex.Profile{}
			case "plan-in-planning":
				spec.Plan = f.plan
			case "execution-parent":
				spec = executionControls(f, s)
				spec.Execution = f.parent
			case "host-attempt":
				spec.Scratch.Options.AttemptDigest = digest([]byte("other"))
			case "host-base":
				spec.Scratch.Options.Base.Inode = 0
			case "host-DIO":
				spec.Scratch.ObservedDirectIO = false
			case "host-source":
				spec.Scratch.Authority.Source = typedsandbox.HostScratchBase + "/other/scratch"
			case "nil-gate":
				gate = nil
			case "cancel":
				cancel()
			}
			if _, err := InstallControls(ctx, f.dir, id, spec, gate); err == nil {
				t.Fatal("bad authority admitted")
			}
			for _, name := range []string{"controls-plan", "controls-plan.stage", "controls-execute", "controls-execute.stage"} {
				if _, err := os.Lstat(filepath.Join(f.dir, id.RelativeName(), name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("refusal grew", name, err)
				}
			}
		})
	}
}

func TestControlsExactReopen(t *testing.T) {
	for _, kind := range []string{"corrupt", "missing", "extra", "symlink", "hardlink", "inode", "mode", "directory", "seal", "ref", "phase", "fresh-host", "allowance"} {
		t.Run(kind, func(t *testing.T) {
			f, id, _, s, _ := controlsFixture(t)
			ref, err := InstallControls(t.Context(), f.dir, id, s, f.gate)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.dir, id.RelativeName(), "controls-plan")
			target := filepath.Join(dir, "parent.json")
			if err = os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "allowance":
				s.Allowance.Start++
				s.Allowance.Deadline++
			case "corrupt":
				if err = os.Chmod(target, 0600); err == nil {
					err = os.WriteFile(target, bytes.Repeat([]byte("x"), len(raw)), 0444)
				}
				if err == nil {
					err = os.Chmod(target, 0444)
				}
			case "missing":
				err = os.Remove(target)
			case "extra":
				err = os.WriteFile(filepath.Join(dir, "extra"), []byte("x"), 0444)
			case "symlink":
				if err = os.Remove(target); err == nil {
					err = os.Symlink("profile.json", target)
				}
			case "hardlink":
				err = os.Link(target, filepath.Join(t.TempDir(), "alias"))
			case "inode":
				if err = os.Rename(target, filepath.Join(t.TempDir(), "original")); err == nil {
					err = os.WriteFile(target, raw, 0444)
				}
			case "mode":
				err = os.Chmod(target, 0644)
			case "seal":
				path := filepath.Join(dir, ControlSealFile)
				if err = os.Chmod(path, 0600); err == nil {
					err = os.WriteFile(path, []byte(strings.Repeat("x", MaxControlSealBytes+1)), 0444)
				}
				if err == nil {
					err = os.Chmod(path, 0444)
				}
			case "ref":
				ref.Seal.Digest = digest([]byte("wrong"))
			case "phase":
				s = executionControls(f, s)
			case "fresh-host":
				s.Scratch.Authority.DeviceMinor++
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind != "directory" {
				if err = os.Chmod(dir, 0555); err != nil {
					t.Fatal(err)
				}
			}
			if h, err := OpenControls(t.Context(), f.dir, id, s, ref); err == nil {
				_ = h.Close()
				t.Fatal("untrusted snapshot admitted", kind)
			}
		})
	}
}

func TestControlsInterruptedPrefixesDrain(t *testing.T) {
	for _, stage := range []string{"stage", "parent.json", "profile.json", typedsandbox.ScratchAuthorityFile, "sealed", "published"} {
		t.Run(stage, func(t *testing.T) {
			f, id, m, s, _ := controlsFixture(t)
			stopped := errors.New("injected interruption")
			ref, err := installControls(t.Context(), f.dir, id, s, f.gate, capacity, func(event string) error {
				if event == stage {
					return stopped
				}
				return nil
			})
			if !errors.Is(err, stopped) {
				t.Fatal(err)
			}
			if stage == "published" {
				h, e := OpenControls(t.Context(), f.dir, id, s, ref)
				if e != nil {
					t.Fatal(e)
				}
				_ = h.Close()
			} else {
				if _, err = LoadOwner(t.Context(), f.dir, id); err == nil {
					t.Fatal("interrupted stage not held")
				}
				if _, err = InstallControls(t.Context(), f.dir, id, s, f.gate); err == nil {
					t.Fatal("interrupted stage resumed")
				}
			}
			a, err := RetirementAuthority(m)
			if err != nil {
				t.Fatal(err)
			}
			finishDrain(t, f.dir, a)
		})
	}
}

func TestControlsCapacityAndCumulativeBudget(t *testing.T) {
	f, id, m, s, _ := controlsFixture(t)
	attempt := filepath.Join(f.dir, id.RelativeName())
	dir, err := openDirectory(attempt, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	gate := lifecycle.NewGate(attempt)
	probe := func(file *os.File) (space, error) {
		available, e := capacity(file)
		available.bytes = 0
		return available, e
	}
	if _, err = installControls(t.Context(), f.dir, id, s, gate, probe, nil); !errors.Is(err, lifecycle.ErrPressureRefusal) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(attempt, "controls-plan.stage")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("capacity refusal grew")
	}
	if _, err = InstallControls(t.Context(), f.dir, id, s, f.gate); err != nil {
		t.Fatal(err)
	}
	charged, err := installedControlsBytes(t.Context(), dir, id)
	if err != nil || charged <= 0 {
		t.Fatal(charged, err)
	}
	previous := int64(16*MaxOwnerBytes) + m.Inventory.Bytes + m.Inputs.Bytes
	m.Budget.Bytes = previous + charged
	if err = ownerControlsBudget(t.Context(), dir, m, 1); err == nil {
		t.Fatal("installed controls escaped cumulative budget")
	}
	if err = ownerControlsBudget(t.Context(), dir, m, 0); err != nil {
		t.Fatal(err)
	}
}

func TestControlsCanonicalSealAndCancellation(t *testing.T) {
	f, id, m, s, _ := controlsFixture(t)
	ref, err := InstallControls(t.Context(), f.dir, id, s, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, id.RelativeName(), "controls-plan", ControlSealFile))
	if err != nil {
		t.Fatal(err)
	}
	seal, err := decodeControlSeal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if seal.Identity != ref.Identity || len(seal.Files) != 3 {
		t.Fatal("planning seal shape")
	}
	for _, bad := range [][]byte{append(bytes.Clone(raw), ' '), append([]byte(`{"unknown":1,`), raw[1:]...), bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"duplicate","schema":`), 1), bytes.Repeat([]byte(" "), MaxControlSealBytes+1)} {
		if _, err = decodeControlSeal(bad); err == nil {
			t.Fatal("noncanonical seal")
		}
	}
	seal.Files[0].Control.Device++
	if _, err = encodeControlSeal(seal); err == nil {
		t.Fatal("other device in seal")
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, err = installControls(ctx, f.dir, id, executionControls(f, s), f.gate, capacity, func(event string) error {
		if event == "parent.json" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	a, err := RetirementAuthority(m)
	if err != nil {
		t.Fatal(err)
	}
	finishDrain(t, f.dir, a)
}

func TestControlsAllowanceMaximumSeal(t *testing.T) {
	f, id, _, spec, _ := controlsFixture(t)
	spec = executionControls(f, spec)
	spec.Allowance.Start = math.MaxInt64 - int64(typedsandbox.WallLimit)
	spec.Allowance.Deadline = math.MaxInt64
	spec.Allowance.TimeDevice = math.MaxUint64
	spec.Allowance.TimeInode = math.MaxUint64
	spec.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes
	spec.Allowance.WireBytesUsed = 24 << 20
	identity, data, err := controlData(t.Context(), id, spec)
	if err != nil {
		t.Fatal(err)
	}
	seal := controlSeal{Schema: controlSealSchema, Identity: identity, Directory: Node{Path: "controls-execute", Device: math.MaxUint64, Inode: math.MaxUint64, Directory: true}}
	for _, name := range controlFiles(ControlsExecution) {
		seal.Files = append(seal.Files, controlFile{name, OwnerControl{Digest: digest(data[name]), Bytes: int64(controlLimit(name)), Device: math.MaxUint64, Inode: math.MaxUint64}})
	}
	raw, err := encodeControlSeal(seal)
	if err != nil || len(raw) > MaxControlSealBytes {
		t.Fatal("allowance grew seal bound", len(raw), err)
	}
	decoded, err := decodeControlSeal(raw)
	if err != nil || decoded.Identity.Allowance != spec.Allowance {
		t.Fatal("allowance round trip", err)
	}
	t.Logf("maximum-shaped execution seal: %d / %d bytes", len(raw), MaxControlSealBytes)
}
