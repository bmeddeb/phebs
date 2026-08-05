package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bmeddeb/phebs/spike/t324"
)

func main() {
	root := flag.String("root", ".", "repository root")
	zoekt := flag.String("zoekt", "bin/zoekt-git-index", "same-module zoekt-git-index binary")
	out := flag.String("out", "spike/t324/results.json", "source-free receipt path")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	receipt, err := t324.Measure(ctx, *root, *zoekt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := t324.MarshalCanonical(receipt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	target := *out
	if !filepath.IsAbs(target) {
		target = filepath.Join(*root, target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(target, encoded, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
