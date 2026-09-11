package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/observationpublication"
)

// Actual collecting-residue files exercise the production deletion path.
// The pointer routes lifecycle to the repository; this is not a publication
// build or reader-admission fixture.
func TestObservationOwnerRetainsFailedDeletionPrefix(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid_entry", true: "symlink"}[symlink], func(t *testing.T) {
			root := t.TempDir()
			repository := "example.com/acme/observation-prefix"
			digest := sha256.Sum256([]byte(repository))
			repo := filepath.Join(root, hex.EncodeToString(digest[:]))
			collecting := filepath.Join(repo, "collecting-"+strings.Repeat("a", 64))
			objects := filepath.Join(collecting, "objects")
			if err := os.MkdirAll(objects, 0o700); err != nil {
				t.Fatal(err)
			}
			object := filepath.Join(objects, strings.Repeat("0", 16)+"-"+strings.Repeat("b", 64)+".json")
			if err := os.WriteFile(object, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			pointer, err := json.Marshal(observationpublication.Pointer{
				Schema: observationpublication.PointerSchema, Repository: repository,
				GenerationDigest: "sha256:" + strings.Repeat("c", 64),
				ManifestDigest:   "sha256:" + strings.Repeat("d", 64),
				ManifestName:     strings.Repeat("c", 64) + "/manifest.json",
			})
			if err != nil {
				t.Fatal(err)
			}
			pointerPath := filepath.Join(repo, "current.json")
			if err := os.WriteFile(pointerPath, pointer, 0o600); err != nil {
				t.Fatal(err)
			}
			invalid := filepath.Join(collecting, "invalid-entry")
			if symlink {
				if err := os.Symlink(pointerPath, invalid); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(invalid, []byte("retained"), 0o600); err != nil {
				t.Fatal(err)
			}
			released := false
			owner := ObservationGenerationOwner{
				Root: root, Pins: &observationpublication.Cache{},
				Acquire: func(context.Context) (func(), error) { return func() { released = true }, nil },
			}
			result := owner.Sweep(t.Context(), time.Now(), "prior", DefaultLimits())
			if result.Err == nil || result.Completeness != Unavailable || result.Deleted != 2 || result.Scanned != 1 ||
				!result.More || result.Cursor != "prior" || result.AdvanceOnError || !released {
				t.Fatalf("lost committed prefix or changed retry: %+v, released=%t", result, released)
			}
			if _, err := os.Lstat(objects); !os.IsNotExist(err) {
				t.Fatalf("object and directory removals not observed: %v", err)
			}
			if _, err := os.Lstat(invalid); err != nil {
				t.Fatalf("refused residue changed: %v", err)
			}
			if raw, err := os.ReadFile(pointerPath); err != nil || string(raw) != string(pointer) {
				t.Fatalf("pointer/symlink target changed: %q %v", raw, err)
			}
		})
	}
}
