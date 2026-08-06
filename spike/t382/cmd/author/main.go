package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t382"
)

func main() {
	raw, err := t382.Marshal(t382.Build())
	if err != nil {
		panic(err)
	}
	path := filepath.Join("spike", "t382", "results.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("%s  %s\n", t382.Digest(raw), path)
}
