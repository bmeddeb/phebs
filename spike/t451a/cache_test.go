package t451a

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuiescenceRefusalDiagnostic(t *testing.T) {
	for _, state := range []string{"S", "Z", "missing"} {
		t.Run(state, func(t *testing.T) {
			proc := t.TempDir()
			for _, pid := range []string{"1", "2"} {
				if err := os.Mkdir(filepath.Join(proc, pid), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := ensureQuiescentWorkerAt(proc, 2); err != nil {
				t.Fatal("healthy census changed", err)
			}
			child := filepath.Join(proc, "3")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			if state != "missing" {
				stat := fmt.Sprintf("3 (child) %s 2 3 3%s 101 0 0\n", state, strings.Repeat(" 0", 15))
				status := "Pid:\t3\nUid:\t0\t0\t0\t0\nGid:\t0\t0\t0\t0\nNoNewPrivs:\t1\nCapPrm:\t0000000000000000\n"
				for name, data := range map[string]string{"stat": stat, "status": status} {
					if err := os.WriteFile(filepath.Join(child, name), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := ensureQuiescentWorkerAt(proc, 2)
			var refusal *QuiescenceError
			if !errors.As(err, &refusal) || err.Error() != "tool process remains before cache eviction" {
				t.Fatal("refusal changed", err)
			}
			if state == "missing" {
				if refusal.Process != nil {
					t.Fatal("invented missing process diagnostic")
				}
			} else if refusal.Process == nil || refusal.Process.State != state || refusal.Process.Comm != "child" || refusal.Process.PPID != 2 {
				t.Fatalf("lost offending process: %+v", refusal.Process)
			}
		})
	}
	t.Run("census overflow still refuses without an offender", func(t *testing.T) {
		proc := t.TempDir()
		for i := range 385 {
			if err := os.WriteFile(filepath.Join(proc, fmt.Sprintf("entry%d", i)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		err := ensureQuiescentWorkerAt(proc, 2)
		if err == nil || err.Error() != "process census refused before cache eviction" {
			t.Fatal("census bound changed", err)
		}
	})
}

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
