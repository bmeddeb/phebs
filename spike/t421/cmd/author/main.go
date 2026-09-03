package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t421"
)

func main() {
	var destination, repositoryRoot, sourceCommit string
	flag.StringVar(&destination, "out", "", "new source-free plan path (must not exist)")
	flag.StringVar(&repositoryRoot, "repository-root", ".", "exact clean Phebs checkout")
	flag.StringVar(&sourceCommit, "source-commit", "", "exact clean implementation commit")
	flag.Parse()
	if destination == "" || sourceCommit == "" || flag.NArg() != 0 {
		fail(errors.New("-out and -source-commit are required; positional arguments are not accepted"))
	}
	repositoryRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		fail(err)
	}
	identity, err := t421.Author(context.Background(), destination, repositoryRoot, sourceCommit)
	if err != nil {
		fail(err)
	}
	fmt.Printf("T42.1 source-free plan: bytes=%d sha256=%s\n", identity.Bytes, identity.SHA256)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "t421-author:", err)
	os.Exit(1)
}
