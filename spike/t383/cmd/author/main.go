package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t383"
)

func main() {
	raw, err := t383.Marshal(t383.Build())
	if err != nil {
		panic(err)
	}
	path := filepath.Join("spike", "t383", "results.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("%s  %s\n", t383.Digest(raw), path)
}
