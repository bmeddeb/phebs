package typedsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func testControlIdentity() ControlIdentity {
	return ControlIdentity{PlanningDigest: testImage, AttemptDigest: "sha256:" + strings.Repeat("c", 64), RequestDigest: testImage, Phase: ControlPlan, SealDigest: testImage, Device: 1, Inode: 2}
}

// The fixture seal is opaque trusted metadata for sandbox-only tests, not a
// workspace admission or an actual snapshot readiness proof.
func testControlOptions(t *testing.T, o Options) Options {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Unix control descriptor fixture")
	}
	o.Controls = filepath.Join(filepath.Dir(o.Inputs), "controls-plan")
	if err := os.Mkdir(o.Controls, 0700); err != nil {
		t.Fatal(err)
	}
	o.Allowance = testAllowance()
	seal := testControlSeal(t, o.Allowance, testControlIdentity())
	raw, err := EncodeScratchAuthority(*o.scratch)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{ControlSealFile: seal, ScratchAuthorityFile: raw} {
		if err := os.WriteFile(filepath.Join(o.Controls, name), body, 0444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(o.Controls, 0555); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(o.Controls)
	if err != nil {
		t.Fatal(err)
	}
	st := reflect.ValueOf(info.Sys()).Elem()
	o.Control = testControlIdentity()
	o.Control.Device = st.FieldByName("Dev").Convert(reflect.TypeFor[uint64]()).Uint()
	o.Control.Inode = st.FieldByName("Ino").Convert(reflect.TypeFor[uint64]()).Uint()
	o.Control.SealDigest = controlDigest(seal)
	return o
}

func TestControlDirectoryProof(t *testing.T) {
	for _, fault := range []string{"good", "writable directory", "directory inode", "directory symlink", "seal symlink", "seal hardlink", "seal writable", "seal digest", "seal oversized", "scratch changed", "scratch noncanonical", "scratch writable", "missing seal", "missing scratch", "wrong phase", "wrong attempt", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			_, o := fakeDaemon(t, "")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			seal := filepath.Join(o.Controls, ControlSealFile)
			scratch := filepath.Join(o.Controls, ScratchAuthorityFile)
			write := func(name string, raw []byte) {
				t.Helper()
				if err := os.Chmod(name, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(name, 0444); err != nil {
					t.Fatal(err)
				}
			}
			mutateDir := func() {
				t.Helper()
				if err := os.Chmod(o.Controls, 0755); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "writable directory":
				must(os.Chmod(o.Controls, 0755))
			case "directory inode":
				o.Control.Inode++
			case "directory symlink":
				must(os.Rename(o.Controls, o.Controls+"-moved"))
				must(os.Symlink(o.Controls+"-moved", o.Controls))
			case "seal symlink":
				mutateDir()
				must(os.Rename(seal, seal+"-original"))
				must(os.Symlink(seal+"-original", seal))
				must(os.Chmod(o.Controls, 0555))
			case "seal hardlink":
				mutateDir()
				if err := os.Link(seal, seal+"-alias"); err != nil {
					t.Fatal(err)
				}
				must(os.Chmod(o.Controls, 0555))
			case "seal writable":
				must(os.Chmod(seal, 0644))
			case "seal digest":
				write(seal, []byte("changed"))
			case "seal oversized":
				write(seal, []byte(strings.Repeat("x", MaxControlSealBytes+1)))
			case "scratch changed":
				a := *o.scratch
				a.DeviceMinor++
				raw, _ := EncodeScratchAuthority(a)
				write(scratch, raw)
			case "scratch noncanonical":
				raw, _ := os.ReadFile(scratch)
				write(scratch, append([]byte(" "), raw...))
			case "scratch writable":
				must(os.Chmod(scratch, 0644))
			case "missing seal":
				mutateDir()
				must(os.Remove(seal))
				must(os.Chmod(o.Controls, 0555))
			case "missing scratch":
				mutateDir()
				must(os.Remove(scratch))
				must(os.Chmod(o.Controls, 0555))
			case "wrong phase":
				o.Control.Phase = ControlExecute
			case "wrong attempt":
				o.Control.AttemptDigest = "bad"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if fault == "canceled" {
				cancel()
			}
			fd, err := openControls(ctx, o)
			if fd != nil {
				_ = fd.Close()
			}
			if (err == nil) != (fault == "good") {
				t.Fatalf("control proof: %v", err)
			}
		})
	}
}

func TestRecoverRecordedWithoutControls(t *testing.T) {
	for _, fault := range []string{"missing controls", "damaged seal", "missing inputs", "wrong planning", "wrong attempt", "wrong image", "wrong inputs", "journal phase", "journal seal", "pending phase", "legacy journal"} {
		t.Run(fault, func(t *testing.T) {
			d, o := fakeDaemon(t, "cleanup error")
			if _, err := runFake(t.Context(), o); !errors.Is(err, ErrCustody) {
				t.Fatal(err)
			}
			owner, err := readJournal(o)
			if err != nil {
				t.Fatal(err)
			}
			recovery := RecoveryOptions{Socket: o.Socket, ImageID: o.ImageID, Inputs: o.Inputs, PlanningDigest: o.Control.PlanningDigest, AttemptDigest: o.Control.AttemptDigest}
			want := fault == "missing controls" || fault == "damaged seal" || fault == "missing inputs"
			switch fault {
			case "missing controls":
				_ = os.Chmod(o.Controls, 0700)
				if err := os.RemoveAll(o.Controls); err != nil {
					t.Fatal(err)
				}
			case "damaged seal":
				name := filepath.Join(o.Controls, ControlSealFile)
				_ = os.Chmod(name, 0600)
				_ = os.WriteFile(name, []byte("damaged"), 0600)
			case "missing inputs":
				if err := os.RemoveAll(o.Inputs); err != nil {
					t.Fatal(err)
				}
			case "wrong planning":
				recovery.PlanningDigest = "sha256:" + strings.Repeat("d", 64)
			case "wrong attempt":
				recovery.AttemptDigest = "sha256:" + strings.Repeat("d", 64)
			case "wrong image":
				recovery.ImageID = "sha256:" + strings.Repeat("d", 64)
			case "wrong inputs":
				recovery.Inputs += "-other"
			case "journal phase":
				owner.Control.Phase = ControlExecute
			case "journal seal":
				owner.Control.SealDigest = "bad"
			case "pending phase":
				owner.Control.Phase = ControlExecute
				owner.Control.RequestDigest = "sha256:" + strings.Repeat("d", 64)
				owner.Controls = filepath.Join(filepath.Dir(o.Inputs), "controls-execute")
			case "legacy journal":
				owner.Schema = "phebs-typed-container-owner-v1"
			}
			if strings.HasPrefix(fault, "journal") || fault == "pending phase" || fault == "legacy journal" {
				raw, _ := json.Marshal(owner)
				name := journalPath(o)
				if fault == "pending phase" {
					name += ".next"
				}
				if err := os.WriteFile(name, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			d.mu.Lock()
			d.fault = ""
			d.inspections = nil
			d.mu.Unlock()
			result, err := recoverRecorded(t.Context(), recovery)
			if (err == nil) != want || result.Removed != want {
				t.Fatalf("recovery %+v %v", result, err)
			}
			if !want {
				d.mu.Lock()
				defer d.mu.Unlock()
				if len(d.inspections) != 0 {
					t.Fatal("changed journal reached daemon")
				}
			}
		})
	}
}

func TestControlIdentityAndJournalAdvance(t *testing.T) {
	good := testControlIdentity()
	if good.Validate() != nil {
		t.Fatal("valid plan identity")
	}
	for _, change := range []func(*ControlIdentity){func(c *ControlIdentity) { c.PlanningDigest = "bad" }, func(c *ControlIdentity) { c.AttemptDigest = "bad" }, func(c *ControlIdentity) { c.RequestDigest = "bad" }, func(c *ControlIdentity) { c.Phase = "other" }, func(c *ControlIdentity) { c.SealDigest = "bad" }, func(c *ControlIdentity) { c.Device = 0 }, func(c *ControlIdentity) { c.Inode = 0 }} {
		bad := good
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid identity accepted")
		}
	}
	good.Phase = ControlExecute
	good.RequestDigest = "sha256:" + strings.Repeat("d", 64)
	if good.Validate() != nil {
		t.Fatal("valid execute identity")
	}
	before := journal{Schema: ownerSchema, Control: good}
	for _, change := range []func(*journal){func(j *journal) { j.Control.Phase = ControlPlan }, func(j *journal) { j.Control.AttemptDigest = testImage }, func(j *journal) { j.Control.SealDigest = "sha256:" + strings.Repeat("e", 64) }, func(j *journal) { j.Control.Inode++ }, func(j *journal) { j.Control.Device++ }, func(j *journal) { j.Controls += "other" }} {
		after := before
		change(&after)
		if journalAdvance(before, after) {
			t.Fatal("immutable journal authority advanced")
		}
	}
}

func TestJournalMetadataCustody(t *testing.T) {
	for _, fault := range []string{"good", "symlink", "hardlink", "fifo", "writable", "replaced selection", "ancestor symlink"} {
		t.Run(fault, func(t *testing.T) {
			d, o := fakeDaemon(t, "cleanup error")
			if _, err := runFake(t.Context(), o); !errors.Is(err, ErrCustody) {
				t.Fatal(err)
			}
			selected, err := readJournal(o)
			if err != nil {
				t.Fatal(err)
			}
			name := journalPath(o)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "symlink":
				must(os.Rename(name, name+"-old"))
				must(os.Symlink(name+"-old", name))
			case "hardlink":
				must(os.Link(name, name+"-alias"))
			case "fifo":
				must(os.Remove(name))
				must(exec.CommandContext(t.Context(), "mkfifo", name).Run())
			case "writable":
				must(os.Chmod(name, 0644))
			case "replaced selection":
				replacement := selected
				replacement.Name = "phebs-typed-index-" + strings.Repeat("b", 32)
				raw, e := json.Marshal(replacement)
				must(e)
				must(os.WriteFile(name, raw, 0600))
			case "ancestor symlink":
				alias := filepath.Join(filepath.Dir(filepath.Dir(name)), "journal-alias")
				must(os.Symlink(filepath.Dir(name), alias))
				t.Cleanup(func() { _ = os.Remove(alias) })
				if _, e := readJournalMetadata(filepath.Join(alias, filepath.Base(name))); e == nil {
					t.Fatal("ancestor symlink admitted")
				}
				return
			}
			d.mu.Lock()
			d.fault = ""
			d.inspections = nil
			d.mu.Unlock()
			result, e := recoverSelected(t.Context(), o, selected)
			if (e == nil) != (fault == "good") || result.Removed != (fault == "good") {
				t.Fatalf("custody %+v %v", result, e)
			}
			if fault != "good" {
				d.mu.Lock()
				defer d.mu.Unlock()
				if len(d.inspections) != 0 {
					t.Fatal("invalid journal contacted daemon")
				}
			}
		})
	}
}

func TestContainerJournalBoundBeforeGrowth(t *testing.T) {
	for _, size := range []int{MaxContainerJournalBytes, MaxContainerJournalBytes + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			_, o := fakeDaemon(t, "")
			owner := journal{Schema: ownerSchema, Name: "phebs-typed-index-" + strings.Repeat("a", 32), DaemonID: "d", ImageID: o.ImageID, Socket: o.Socket, Inputs: o.Inputs, Controls: o.Controls, Control: o.Control, Allowance: o.Allowance, Scratch: o.scratch}
			raw, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			owner.DaemonID = strings.Repeat("d", size-len(raw)+1)
			raw, err = json.Marshal(owner)
			if err != nil || len(raw) != size {
				t.Fatal("fixture size", len(raw), err)
			}
			err = writeJournal(o, owner, true)
			if size == MaxContainerJournalBytes {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = readJournal(o); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("oversized journal created")
				}
				if _, err = os.Lstat(journalPath(o)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("oversized journal grew", err)
				}
			}
		})
	}
}
