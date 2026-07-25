package main

import (
	"fmt"
	"os"
)

// main fetches and pins the spike corpus: `go run ./spike/t191`. The gate
// tests skip without it (or run them with T191_FETCH=1 to fetch inline).
func main() {
	entries, err := loadCorpusLock("spike/t191/corpus.lock.json")
	if err != nil {
		entries, err = loadCorpusLock("corpus.lock.json")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root := "spike/t191/corpus"
	if _, statErr := os.Stat("corpus.lock.json"); statErr == nil {
		root = "corpus"
	}
	for _, entry := range entries {
		dir, err := ensureCorpus(root, entry)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s pinned at %s (%s)\n", entry.Name, entry.Commit, dir)
	}
}
