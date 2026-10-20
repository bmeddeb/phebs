//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestLinuxInputCustodyAtomicJoinedInputs(t *testing.T) {
	data := linuxInputFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image := linuxInputSelection(t, executable, "reader", true)
	custody := requireLinuxInputCustody(t, image, data)
	if err := os.WriteFile(data.Path, []byte("changed source"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := linuxInputFDCount(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	names := []string{image.Name, data.Name}
	if err := custody.WithInputs(ctx, names, func(files []*os.File) error {
		if len(files) != 2 || linuxInputFDCount(t) != before+2 {
			t.Fatal("bundle must lend exactly the requested descriptor set")
		}
		cmd := exec.CommandContext(ctx, "/proc/self/fd/3", "-test.run=^TestLinuxInputCustodyAtomicChild$")
		cmd.ExtraFiles = files
		cmd.Env = []string{"LC_ALL=C", "LANG=C", "GOMAXPROCS=2", "PHEBS_TEST_ATOMIC_INPUT_CHILD=1"}
		output, err := cmd.CombinedOutput() // Actual join before the atomic loan returns.
		if err != nil || string(output) != "neutral sealed input\n" {
			t.Fatalf("joint executable/data use: %q, %v", output, err)
		}
		// Caller slice mutations cannot replace the private cleanup/check lists.
		names[0], files[0] = "not-a-selection", nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if linuxInputFDCount(t) != before {
		t.Fatal("bundle leaked a descriptor")
	}
	for range 2 {
		if err := custody.WithInput(ctx, data.Name, func(file *os.File) error {
			value, err := io.ReadAll(file)
			if err != nil || string(value) != "neutral sealed input\n" {
				t.Fatalf("loan offset/identity drift: %q, %v", value, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLinuxInputCustodyAtomicChild(t *testing.T) {
	if os.Getenv("PHEBS_TEST_ATOMIC_INPUT_CHILD") != "1" {
		return
	}
	file := os.NewFile(4, "atomic-input-data")
	value, err := io.ReadAll(io.LimitReader(file, 64))
	if err != nil || file.Close() != nil || string(value) != "neutral sealed input\n" {
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(value); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestLinuxInputCustodyAtomicSelectionRefusals(t *testing.T) {
	for _, fault := range []string{"empty", "too-many", "duplicate", "unknown-later", "long", "nil-use", "nil-context", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			input := linuxInputFixture(t)
			custody := requireLinuxInputCustody(t, input)
			names := []string{input.Name}
			ctx := context.Background()
			called := false
			use := func([]*os.File) error { called = true; return nil }
			switch fault {
			case "empty":
				names = nil
			case "too-many":
				names = make([]string, maxInputCustodyFiles+1)
			case "duplicate":
				names = append(names, input.Name)
			case "unknown-later":
				names = append(names, "missing")
			case "long":
				names = []string{string(make([]byte, 65))}
			case "nil-use":
				use = nil
			case "nil-context":
				ctx = nil
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			before := linuxInputFDCount(t)
			if err := custody.WithInputs(ctx, names, use); !errors.Is(err, ErrExecutionInputCustody) || called {
				t.Fatal("invalid bundle reached callback", err)
			}
			if linuxInputFDCount(t) != before {
				t.Fatal("refused bundle leaked descriptors")
			}
			if _, err := custody.Check(context.Background(), input.Name); !errors.Is(err, ErrExecutionInputCustody) {
				t.Fatal("bundle refusal did not poison owner", err)
			}
		})
	}
	var absent *LinuxExecutionInputCustody
	if err := absent.WithInputs(context.Background(), []string{"input"}, func([]*os.File) error { t.Fatal("nil owner callback"); return nil }); !errors.Is(err, ErrExecutionInputCustody) {
		t.Fatal(err)
	}
}

func TestLinuxInputCustodyAtomicCleanup(t *testing.T) {
	for _, fault := range []string{"close-first", "close-last", "cancel", "panic", "callback-error"} {
		t.Run(fault, func(t *testing.T) {
			first, second := linuxInputFixture(t), linuxInputFixture(t)
			second.Name = "second"
			custody := requireLinuxInputCustody(t, first, second)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			before := linuxInputFDCount(t)
			callbackErr := errors.New("trusted consumer failed")
			var err error
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				err = custody.WithInputs(ctx, []string{first.Name, second.Name}, func(files []*os.File) error {
					switch fault {
					case "close-first":
						return files[0].Close()
					case "close-last":
						return files[1].Close()
					case "cancel":
						cancel()
					case "panic":
						panic("trusted consumer panic")
					case "callback-error":
						return callbackErr
					}
					return nil
				})
			}()
			if linuxInputFDCount(t) != before {
				t.Fatal("bundle cleanup leaked descriptors")
			}
			_, checkErr := custody.Check(context.Background(), first.Name)
			if fault == "callback-error" {
				if err != callbackErr || checkErr != nil {
					t.Fatal("callback error changed or poisoned protection", err, checkErr)
				}
			} else if !errors.Is(checkErr, ErrExecutionInputCustody) || fault == "panic" && !panicked || fault != "panic" && !errors.Is(err, ErrExecutionInputCustody) {
				t.Fatal("unsafe bundle remained usable", err, checkErr, panicked)
			}
		})
	}
}

func TestLinuxInputCustodyAtomicGoexit(t *testing.T) {
	first, second := linuxInputFixture(t), linuxInputFixture(t)
	second.Name = "second"
	custody := requireLinuxInputCustody(t, first, second)
	before := linuxInputFDCount(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = custody.WithInputs(context.Background(), []string{first.Name, second.Name}, func([]*os.File) error { runtime.Goexit(); return nil })
	}()
	<-done
	if linuxInputFDCount(t) != before {
		t.Fatal("Goexit leaked bundle descriptors")
	}
	if _, err := custody.Check(context.Background(), first.Name); !errors.Is(err, ErrExecutionInputCustody) {
		t.Fatal("Goexit left usable custody", err)
	}
}

func TestLinuxInputCustodyAtomicMaximumAndCloseWait(t *testing.T) {
	input := linuxInputFixture(t)
	copies := make([]ExecutionInputCopy, maxInputCustodyFiles)
	names := make([]string, len(copies))
	for i := range copies {
		copies[i] = input
		copies[i].Name = fmt.Sprintf("input%d", i)
		names[i] = copies[i].Name
	}
	custody := requireLinuxInputCustody(t, copies...)
	before := linuxInputFDCount(t)
	entered, release, used, closed := make(chan struct{}), make(chan struct{}), make(chan error, 1), make(chan error, 1)
	go func() {
		used <- custody.WithInputs(context.Background(), names, func(files []*os.File) error {
			if len(files) != len(names) || linuxInputFDCount(t) != before+len(names) {
				return errors.New("maximum bundle descriptor count mismatch")
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-used:
		t.Fatal("maximum bundle refused", err)
	}
	go func() { closed <- custody.Close() }()
	select {
	case <-closed:
		t.Fatal("close overtook the atomic loan")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-used; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil || linuxInputFDCount(t) != before-len(names) {
		t.Fatal("close did not release complete bundle and keepers", err)
	}
}
