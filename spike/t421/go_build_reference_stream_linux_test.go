//go:build linux

package t421

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxGoReferenceHeldInputInventory(t *testing.T) {
	for _, test := range []string{"valid", "symlink", "hardlink", "canceled"} {
		t.Run(test, func(t *testing.T) {
			base := t.TempDir()
			if os.MkdirAll(filepath.Join(base, "inputs/source"), 0o700) != nil || os.Mkdir(filepath.Join(base, "inputs/modules"), 0o700) != nil {
				t.Fatal("input roots")
			}
			for _, name := range []string{"z", "a", "middle"} {
				if os.WriteFile(filepath.Join(base, "inputs/source", name), []byte(name), 0o400) != nil {
					t.Fatal("input")
				}
			}
			switch test {
			case "symlink":
				if os.Symlink("a", filepath.Join(base, "inputs/source/link")) != nil {
					t.Fatal("symlink")
				}
			case "hardlink":
				if os.Link(filepath.Join(base, "inputs/source/a"), filepath.Join(base, "inputs/source/link")) != nil {
					t.Fatal("hardlink")
				}
			}
			file, err := os.Open(base)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			ctx := t.Context()
			if test == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			tree, rows, err := gatherGoReferenceHeldInputs(ctx, file)
			if test != "valid" {
				if err != ErrExecutionGoBuildCustody || tree != nil {
					t.Fatal("irregular held inventory admitted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tree.Close() }()
			var census inputTreeCensus
			for _, row := range rows {
				if census.add(row.entry) != nil || row.root.file != file || row.root.tree != tree || !strings.HasPrefix(row.relative, "inputs") {
					t.Fatal("held inventory ordering/descriptor identity differs")
				}
			}
			if census.inventory.entries != 6 || census.inventory.files != 3 || census.inventory.bytes != 8 {
				t.Fatal("held input inventory differs", census.inventory)
			}
			// This models descriptor discovery only. It grants no H2k protection
			// to this mutable test root; real callers must hold WithRoot.
		})
	}
}
