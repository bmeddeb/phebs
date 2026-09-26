package t451a

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompilerCacheRefusesUnfamiliarTreeBeforeMutation(t *testing.T) {
	for _, kind := range []string{"link", "parent link", "nested", "sparse"} {
		t.Run(kind, func(t *testing.T) {
			parent, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(parent, "gocache")
			if err := os.MkdirAll(filepath.Join(cache, "ab"), 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(cache, "README")
			if err := os.WriteFile(marker, []byte("owned cache"), 0600); err != nil {
				t.Fatal(err)
			}
			candidate := cache
			switch kind {
			case "link":
				err = os.Symlink(marker, filepath.Join(cache, "ab/link"))
			case "parent link":
				link := filepath.Join(parent, "redirect")
				err = os.Symlink(parent, link)
				candidate = filepath.Join(link, "gocache")
			case "nested":
				err = os.MkdirAll(filepath.Join(cache, "ab/nested/deeper"), 0700)
			case "sparse":
				file, createErr := os.Create(filepath.Join(cache, "ab/large"))
				if createErr != nil {
					t.Fatal(createErr)
				}
				err = file.Truncate(2 << 30)
				_ = file.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := evictCompilerCache(candidate); err == nil {
				t.Fatal("accepted unexpected cache content")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("mutated cache before complete inventory")
			}
		})
	}
}
