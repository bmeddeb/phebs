//go:build linux

package t421

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxGoBuildStagingSourceReservation(t *testing.T) {
	for _, test := range []string{"valid", "byte-budget", "entry-budget", "path-budget", "outside-destination"} {
		t.Run(test, func(t *testing.T) {
			fixture := newExecutionCheckoutFixture(t)
			legacy := referenceFixtureInspector(t, fixture)
			transport := &checkoutTransportFixture{legacy: legacy, roots: make(map[string]int)}
			origin := executionCheckoutInspector{root: fixture.root, digest: legacy.digest, transport: transport}
			staging := newLinuxGoStagingFixture(t)
			destination := filepath.Join(staging.directory, "source")
			switch test {
			case "byte-budget":
				staging.inventory.Bytes = maxGoBuildBytes - (8 << 20)
			case "entry-budget":
				staging.inventory.Entries = maxGoBuildEntries - 1
			case "path-budget":
				staging.pathBytes = maxGoBuildPathBytes - 1
			case "outside-destination":
				destination = filepath.Join(t.TempDir(), "source")
			}
			reference, err := staging.prepareSource(t.Context(), origin, fixture.source, destination)
			if test != "valid" {
				if err != ErrExecutionGoBuildCustody || staging.err == nil {
					t.Fatal("unreserved source writes accepted", err)
				}
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatal("source wrote before reservation/destination refusal", err)
				}
				return
			}
			if err != nil || reference.source != fixture.source || reference.root.transport != transport || reference.verify(t.Context()) != nil {
				t.Fatal("exact source staging/routing refused", err)
			}
			var files int
			var bytes int64
			paths := 0
			for name, info := range staging.names {
				if info == nil {
					t.Fatal("source entry not recorded")
				}
				paths += len("inputs")
				if name != "." {
					paths += len("/" + name)
				}
				if info.Mode().IsRegular() {
					files++
					bytes += info.Size()
				}
			}
			if staging.inventory != (ExecutionGoBuildInventory{Entries: len(staging.names), Files: files, Bytes: bytes}) || staging.pathBytes != paths || bytes <= 0 {
				t.Fatal("source reservation did not settle to actual bounded inventory", staging.inventory, paths)
			}
			if err := staging.removeContents(t.Context()); err != nil {
				t.Fatal("joined source cleanup", err)
			}
		})
	}
}
