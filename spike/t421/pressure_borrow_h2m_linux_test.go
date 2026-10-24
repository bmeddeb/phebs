//go:build linux

package t421

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxPressureBorrowObservedEarlyRefusal(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	observation := &executionPreworkObservation{}
	volume, err := prepareLinuxPressureVolumeObserved(ctx, LinuxPressureRequest{}, observation)
	if err == nil || volume != nil || !observation.settle(ctx) {
		t.Fatal("pre-owner refusal fabricated retained volume/close failure")
	}
}

func TestLinuxPressureBorrowFailurePublicationAttemptOutcome(t *testing.T) {
	for _, existing := range []bool{false, true} {
		root := t.TempDir()
		if os.Chmod(root, 0o700) != nil {
			t.Fatal("mode")
		}
		value := linuxExecutorPreworkSelection{PreparationRoot: root, SourceCommit: strings.Repeat("b", 40), Attempt: strings.Repeat("a", 32)}
		observation := &executionPreworkObservation{}
		if existing {
			observation.retainWorkspace(filepath.Join(root, "retained-original"))
		}
		attempted, err := attemptLinuxExecutorPreworkFailureRecord(value, observation, nil)
		if err != nil || attempted != existing {
			t.Fatal("healthy no-record and actual first attempt were conflated", attempted, err)
		}
		path := filepath.Join(root, "executor-prework-"+value.Attempt+"-failure.json")
		var first []byte
		if existing {
			first, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
		} else if entries, readErr := os.ReadDir(root); readErr != nil || len(entries) != 0 {
			t.Fatal("healthy observation performed filesystem work")
		}
		file, err := os.CreateTemp(t.TempDir(), "owned-close-")
		if err != nil || file.Close() != nil {
			t.Fatal("controlled own close fixture")
		}
		if closeInputTreeSources([]*inputTreeSourceRoot{{file: file, observation: observation}}) == nil {
			t.Fatal("missing actual owned Close refusal")
		}
		if !attempted {
			attempted, err = attemptLinuxExecutorPreworkFailureRecord(value, observation, nil)
			if !attempted || err != nil {
				t.Fatal("new first close-only fact lost", err)
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var record linuxExecutorPreworkFailure
		if json.Unmarshal(raw, &record) != nil {
			t.Fatal("record")
		}
		if existing {
			if string(raw) != string(first) || record.NativeCloseFailed {
				t.Fatal("first record overwritten by later close-only facts")
			}
		} else if !record.NativeCloseFailed || record.WorkspaceNotClean || record.HelperPID != 0 {
			t.Fatal("new close-only record absent or invented authority")
		}
	}
	observation := &executionPreworkObservation{}
	observation.retainWorkspace("/retained")
	if attempted, err := attemptLinuxExecutorPreworkFailureRecord(linuxExecutorPreworkSelection{}, observation, nil); !attempted || err == nil {
		t.Fatal("failed nonhealthy publication incorrectly claimed no attempt")
	}
}

func TestLinuxPressureBorrowOriginalFirstClose(t *testing.T) {
	for _, mode := range []string{"healthy", "preclosed-owned"} {
		t.Run(mode, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "owned-first-close-")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "preclosed-owned" && file.Close() != nil {
				t.Fatal("controlled own close fixture")
			}
			observation := &executionPreworkObservation{}
			// Private owner shape only: no volume/mount/provenance is issued.
			volume := &LinuxPressureVolume{image: file, observation: observation, closeResults: make(map[*os.File]error, 9)}
			first := volume.Close()
			if volume.Close() != nil {
				t.Fatal("public idempotent Close behavior changed")
			}
			if mode == "healthy" {
				if first != nil || volume.closeFile(file) != nil || observation.nativeCloseFailed.Load() || !linuxPressureVolumeDescriptorsRetired(volume) {
					t.Fatal("known successful first close became unknown on repetition")
				}
			} else if first == nil || volume.closeFile(file) == nil || !observation.nativeCloseFailed.Load() || linuxPressureVolumeDescriptorsRetired(volume) {
				t.Fatal("later idempotent nil erased original actual Close refusal")
			}
		})
	}
}

func TestLinuxPressureBorrowFailureOnceBeforeOriginalLoanClose(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0o700) != nil {
		t.Fatal("private root")
	}
	value := linuxExecutorPreworkSelection{PreparationRoot: root, SourceCommit: strings.Repeat("b", 40), Attempt: strings.Repeat("a", 32)}
	observation := &executionPreworkObservation{}
	observation.retainWorkspace(filepath.Join(root, "retained-original"))
	input := linuxInputFixture(t)
	custody := requireLinuxInputCustody(t, input)
	// This tool/inner shape grants no provenance. Its sealed input, complete
	// borrowed FD loan and blocking original Close are actual owned resources.
	inner := &linuxExecutorInnerPrework{tool: &LinuxExecutionToolCustody{input: custody}, observation: observation}
	loanEntered, releaseLoan := make(chan struct{}), make(chan struct{})
	loanDone := make(chan error, 1)
	go func() {
		loanDone <- custody.WithInput(t.Context(), input.Name, func(*os.File) error {
			close(loanEntered)
			<-releaseLoan
			return nil
		})
	}()
	published, closeDone := make(chan error, 1), make(chan error, 1)
	loanJoined, closeStarted := false, false
	// Install release/join before awaiting callback entry: an early WithInput
	// refusal must report its error instead of stranding the fixture.
	defer func() {
		close(releaseLoan)
		if !loanJoined {
			if err := <-loanDone; err != nil {
				t.Error("original loan retirement", err)
			}
		}
		if closeStarted {
			if err := <-closeDone; err != nil {
				t.Error("original close retirement", err)
			}
		}
	}()
	select {
	case <-loanEntered:
	case err := <-loanDone:
		loanJoined = true
		t.Fatal("original complete input loan refused before callback", err)
	}
	closeStarted = true
	go func() {
		attempted := false
		published <- writeLinuxExecutorPreworkFailureOnce(&attempted, value, observation, nil)
		closeErr := inner.Close(context.Background())
		closeDone <- errors.Join(closeErr, writeLinuxExecutorPreworkFailureOnce(&attempted, value, observation, nil))
	}()
	if err := <-published; err != nil {
		t.Fatal("first facts must precede blocked Close", err)
	}
	path := filepath.Join(root, "executor-prework-"+value.Attempt+"-failure.json")
	first, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(first), "retained-original") {
		t.Fatal("first facts absent while original complete loan is held", err)
	}
	select {
	case err := <-closeDone:
		// Preserve the single join value for unconditional cleanup.
		closeDone <- err
		t.Fatal("original Close bypassed the held complete input loan")
	default:
	}
	// A failed first attempt is spent even if its invalid selection is later
	// corrected; the local state never turns an incomplete attempt into retry.
	attempted := false
	if writeLinuxExecutorPreworkFailureOnce(&attempted, linuxExecutorPreworkSelection{}, observation, nil) == nil || !attempted {
		t.Fatal("failed first attempt not recorded")
	}
	if writeLinuxExecutorPreworkFailureOnce(&attempted, value, observation, nil) != nil {
		t.Fatal("spent publication attempted another exclusive write")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(first) != string(after) {
		t.Fatal("first immutable facts changed", err)
	}
}
