//go:build darwin

package t421

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestExecutionEpochConfigRefusesMissingAuthority(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, author := range []*ExecutionAuthorCustody{nil, {}} {
		for _, selected := range []context.Context{ctx, t.Context()} {
			if custody, err := PrepareExecutionEpochConfigs(selected, author); err == nil || custody != nil {
				t.Fatal("missing genuine author inputs acquired custody")
			}
		}
	}
	for _, custody := range []*ExecutionEpochConfigCustody{nil, {}} {
		if result, err := custody.Check(t.Context(), 1); err == nil || !reflect.DeepEqual(result, ExecutionEpochConfig{}) {
			t.Fatal("zero owner admitted")
		}
		if custody.ReleaseListener(t.Context(), 1) == nil {
			t.Fatal("zero owner released listener")
		}
		if custody.Close() != nil {
			t.Fatal("zero owner cleanup failed")
		}
	}
}

func TestExecutionEpochConfigCloseRefusesActiveUser(t *testing.T) {
	custody := &ExecutionEpochConfigCustody{active: true}
	if err := custody.Close(); err != ErrExecutionEpochConfigs || custody.closed {
		t.Fatal("active native user lost configuration custody", err)
	}
	custody.active = false // The native owner must establish its join first.
	if err := custody.Close(); err != nil || !custody.closed {
		t.Fatal("joined native user could not release configuration custody", err)
	}
}

// This isolates the real existing copy/flag/hash and descriptor lifecycle;
// the partial private author below is only a staging path, never admission.
func TestExecutionEpochConfigProtectedStagingAndClose(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil || os.Chmod(parent, 0o700) != nil {
		t.Fatal("private fixture root unavailable")
	}
	custody := &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{parent: parent}}
	t.Cleanup(func() { _ = custody.Close() })
	var specs []ExecutionInputCopy
	inputs := []epochGeneratedInput{{"catalog-a", []byte("a")}, {"catalog-b", []byte("b")}, {"catalog-a-return", []byte("a-return")}}
	protected, err := custody.protectInputs(t.Context(), inputs)
	if protected != nil {
		for _, input := range inputs {
			specs = append(specs, ExecutionInputCopy{Name: input.name})
		}
		inputCustodyTestCleanup(t, protected, specs)
	}
	if err != nil {
		t.Fatal(err)
	}
	custody.catalogs = protected
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || len(custody.stages) != 0 {
		t.Fatal("temporary input bytes escaped exact cleanup")
	}
	for _, input := range inputs {
		path, err := protected.Check(t.Context(), input.name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, input.raw) {
			t.Fatal("protected bytes differ")
		}
		if writer, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = writer.Close()
			t.Fatal("new copy is writable")
		}
	}
	var addresses []string
	for index := range custody.listeners {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		custody.listeners[index] = listener
		address := listener.Addr().String()
		if slices.Contains(addresses, address) {
			t.Fatal("two reservations share one address")
		}
		addresses = append(addresses, address)
		if competing, err := net.Listen("tcp4", address); err == nil {
			_ = competing.Close()
			t.Fatal("held listener failed to reserve address")
		}
	}
	retained := custody.RetainedPaths()
	if len(retained) != 1 || retained[0] != protected.Directory() {
		t.Fatal("retained ownership omitted protected input")
	}
	for range 2 {
		if custody.Close() != nil {
			t.Fatal("FD-only close failed")
		}
	}
	for _, address := range addresses {
		listener, err := net.Listen("tcp4", address)
		if err != nil {
			t.Fatal("close retained its reservation", err)
		}
		_ = listener.Close()
	}
	if _, err := os.Stat(protected.Directory()); err != nil {
		t.Fatal("close deleted retained input")
	}
	if _, err := protected.Check(t.Context(), inputs[0].name); err == nil {
		t.Fatal("closed input owner remained usable")
	}
	if result, err := custody.Check(t.Context(), 1); err == nil || result.ConfigPath != "" {
		t.Fatal("partial test setup became authority")
	}
}

func validateEpochConfigBytes(plan Plan, epoch ExecutionEpochConfig, source string, raw []byte) error {
	_, err := parseEpochConfigBytes(plan, epoch, source, raw)
	return err
}
