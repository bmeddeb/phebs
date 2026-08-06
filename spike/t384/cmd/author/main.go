package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t384"
)

func main() {
	raw, err := t384.Marshal(t384.Build())
	if err != nil {
		panic(err)
	}
	path := filepath.Join("spike", "t384", "results.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("%s  %s\n", t384.Digest(raw), path)
}
