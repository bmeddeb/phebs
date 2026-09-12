package recovery

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestReadArchiveTransitionManifestFile(t *testing.T) {
	for _, mode := range []string{"complete", "digest", "gap", "unknown", "trailing", "oversize", "directory", "canceled", "nil-file"} {
		t.Run(mode, func(t *testing.T) {
			manifest := archiveTransitionManifestFixture(t)
			if mode == "gap" {
				manifest.Relationship.Omitted = 1
				var err error
				manifest.ManifestSHA256, err = manifestDigest(manifest)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown" {
				raw = append([]byte(`{"unknown":0,`), raw[1:]...)
			}
			if mode == "trailing" {
				raw = append(raw, []byte(`{}`)...)
			}
			if mode == "oversize" {
				raw = make([]byte, maxManifestBytes+1)
			}
			path := filepath.Join(t.TempDir(), ManifestName)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if mode == "directory" {
				path = filepath.Dir(path)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			borrowed := file
			if mode == "nil-file" {
				borrowed = nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{ControlFileReads: 1})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				cancel()
			}
			digest := manifest.ManifestSHA256
			if mode == "digest" {
				digest = archiveTransitionDigest("b")
			}
			got, readErr := ReadArchiveTransitionManifestFile(ctx, borrowed, digest, digest)
			counts, ledgerErr := ledger.Finish()
			if (readErr == nil) != (mode == "complete") {
				t.Fatalf("mode %s read: %v", mode, readErr)
			}
			want := readaccounting.Counts{ControlFileReads: 1}
			if mode == "canceled" || mode == "nil-file" {
				want.ControlFileReads = 0
			}
			if counts != want || ledgerErr != nil {
				t.Fatalf("accounting %+v: %v", counts, ledgerErr)
			}
			if mode == "complete" {
				want, err := projectArchiveTransitionManifest(t.Context(), manifest, digest, digest)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("projection differs: %v", err)
				}
				offset, err := file.Seek(0, io.SeekCurrent)
				if err != nil || offset != 0 {
					t.Fatalf("borrowed offset %d: %v", offset, err)
				}
				// No artifact files or engine exist in this fixture.
				if _, err := file.Stat(); err != nil {
					t.Fatal("borrowed descriptor closed", err)
				}
			}
		})
	}
}
