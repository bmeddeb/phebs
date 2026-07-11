package repowork

import (
	"sync"
	"testing"
)

func TestCanonicalKeyNormalizesCaseAndUnicodeComposition(t *testing.T) {
	left := CanonicalKey("/data/CAF\u00c9/repo.git")
	right := CanonicalKey("/data/cafe\u0301/repo.git")
	if left != right {
		t.Fatalf("canonical keys differ: %q != %q", left, right)
	}
}

func TestCanonicalKeyConcurrent(t *testing.T) {
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if got := CanonicalKey("/data/CAF\u00c9/repo.git"); got != "/data/caf\u00e9/repo.git" {
					errs <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for got := range errs {
		t.Fatalf("concurrent canonical key = %q", got)
	}
}
