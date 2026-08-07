package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	t401 "github.com/bmeddeb/phebs/spike/t401"
)

func main() {
	moduleRoot := flag.String("module-root", "", "absolute phebs module root")
	binary := flag.String("binary", "", "absolute pinned zoekt-git-index binary")
	repository := flag.String("repository", "", "absolute authored projection repository.git")
	workDir := flag.String("work-dir", "", "absolute new comparison output directory")
	output := flag.String("output", "", "optional path for the canonical source-free receipt")
	timeout := flag.Duration("timeout", 10*time.Minute, "bounded comparison timeout")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	receipt, err := t401.CompareBlobReaders(ctx, t401.ComparisonRequest{
		ModuleRoot: *moduleRoot, Binary: *binary, Repository: *repository,
		WorkDir: *workDir, Queries: t401.FrozenBlobReaderTrial().Queries,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := t401.MarshalCanonical(receipt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *output != "" {
		if err := os.WriteFile(*output, encoded, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
