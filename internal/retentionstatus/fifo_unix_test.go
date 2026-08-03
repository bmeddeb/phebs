//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package retentionstatus

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFIFOReplacementCannotBlockDataRootOpen(t *testing.T) {
	dataDir := t.TempDir()
	collector := testCollectorAt(dataDir, emptyDerivedStoreCollection())
	var restore func()
	collector.hooks.beforeDirectoryOpen = func(path string) {
		if path == dataDir && restore == nil {
			restore = replacePathWithFIFO(t, dataDir)
		}
	}
	observed, err := collectWithFIFOTimeout(t, dataDir, collector)
	if restore != nil {
		restore()
	}
	if err != nil {
		t.Fatal(err)
	}
	if observed.Components[1].Count.Completeness != Unavailable ||
		observed.DataVolume.TotalBytes.Completeness != Unavailable {
		t.Fatalf("FIFO data-root result = %+v / %+v", observed.Components[1], observed.DataVolume)
	}
}

func TestFIFOReplacementCannotBlockSubrootOpen(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "candidates")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	prepareCandidateFiles(t, root, 1)
	collector := testCollectorAt(dataDir, emptyDerivedStoreCollection())
	var restore func()
	collector.hooks.beforeDirectoryOpen = func(path string) {
		if path == root && restore == nil {
			restore = replacePathWithFIFO(t, root)
		}
	}
	observed, err := collectWithFIFOTimeout(t, root, collector)
	if restore != nil {
		restore()
	}
	if err != nil {
		t.Fatal(err)
	}
	if observed.Components[1].Count.Completeness != Unavailable ||
		observed.Components[1].Err == nil {
		t.Fatalf("FIFO subroot result = %+v", observed.Components[1])
	}
}

func TestFIFOReplacementCannotBlockCanonicalFileOpen(t *testing.T) {
	fixture := prepareCanonicalRaceFixture(t, "resolver")
	collector := testCollectorAt(fixture.dataDir, fixture.collection)
	var restore func()
	collector.hooks.beforeRegularOpen = func(path string) {
		if path == fixture.manifestPath && restore == nil {
			restore = replacePathWithFIFO(t, path)
		}
	}
	observed, err := collectWithFIFOTimeout(t, fixture.manifestPath, collector)
	if restore != nil {
		restore()
	}
	if err != nil {
		t.Fatal(err)
	}
	metric := findByteMetric(t, observed.Components[4], CanonicalContent)
	if metric.Completeness != Unavailable || metric.Value != nil {
		t.Fatalf("FIFO canonical metric = %+v", metric)
	}
}

type fifoCollectionResult struct {
	collection Collection
	err        error
}

func collectWithFIFOTimeout(
	t *testing.T,
	fifoPath string,
	collector *Collector,
) (Collection, error) {
	t.Helper()
	done := make(chan fifoCollectionResult, 1)
	go func() {
		collection, err := collector.CollectRetention(t.Context(), derivedRequests())
		done <- fifoCollectionResult{collection: collection, err: err}
	}()
	select {
	case result := <-done:
		return result.collection, result.err
	case <-time.After(2 * time.Second):
		writer, err := os.OpenFile(fifoPath, os.O_RDWR|unix.O_NONBLOCK, 0)
		if err == nil {
			_ = writer.Close()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("retention collection blocked on a FIFO replacement")
		return Collection{}, nil
	}
}

func replacePathWithFIFO(t *testing.T, path string) func() {
	t.Helper()
	moved := path + "-retention-original"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	active := true
	return func() {
		if !active {
			return
		}
		active = false
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(moved, path); err != nil {
			t.Fatal(err)
		}
	}
}
