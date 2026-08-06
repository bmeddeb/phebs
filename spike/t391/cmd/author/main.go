package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t391"
)

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "spike/t391/results.json", "receipt output relative to root")
	flag.Parse()
	receipt, err := t391.Build(*root)
	if err != nil {
		fatal(err)
	}
	encoded, err := t391.Marshal(receipt)
	if err != nil {
		fatal(err)
	}
	target := filepath.Join(*root, filepath.FromSlash(*out))
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
