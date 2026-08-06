package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t385"
)

func main() {
	raw, err := t385.Marshal(t385.Build())
	if err != nil {
		panic(err)
	}
	path := filepath.Join("spike", "t385", "results.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("%s  %s\n", t385.Digest(raw), path)
}
