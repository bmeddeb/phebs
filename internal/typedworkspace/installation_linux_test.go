//go:build linux

package typedworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallationControlRefusalsAndDescriptorCleanup(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"control":"exact"}`)
	name := filepath.Join(base, "control.json")
	if err = os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		change   func()
		expected string
		limit    int64
	}{
		{"valid", func() {}, digest(data), int64(len(data))},
		{"wrong-digest", func() {}, digest([]byte("other")), 100},
		{"overflow", func() {}, digest(data), int64(len(data) - 1)},
		{"mode", func() { _ = os.Chmod(name, 0644) }, digest(data), 100},
		{"hardlink", func() {
			if err := os.Link(name, name+".link"); err != nil {
				t.Fatal(err)
			}
		}, digest(data), 100},
		{"symlink", func() {
			if err := os.Rename(name, name+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(name+".real", name); err != nil {
				t.Fatal(err)
			}
		}, digest(data), 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.change()
			before, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			for range 20 {
				raw, err := ReadInstallationControl(t.Context(), base, "control.json", tc.expected, tc.limit)
				if tc.name == "valid" {
					if err != nil || string(raw) != string(data) {
						t.Fatal(raw, err)
					}
				} else if err == nil {
					t.Fatal("invalid control admitted")
				}
			}
			after, err := os.ReadDir("/proc/self/fd")
			if err != nil || len(after) != len(before) {
				t.Fatal("descriptor growth", len(before), len(after), err)
			}
			if tc.name == "symlink" {
				_ = os.Remove(name)
				_ = os.Rename(name+".real", name)
			}
			_ = os.Remove(name + ".link")
			_ = os.Chmod(name, 0600)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadInstallationControl(ctx, base, "control.json", digest(data), 100); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}
