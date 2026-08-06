package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t394"
)

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "spike/t394/results.json", "new absolute path or path relative to root")
	flag.Parse()
	receipt, err := t394.Build(*root)
	if err != nil {
		fatal(err)
	}
	encoded, err := t394.Marshal(receipt)
	if err != nil {
		fatal(err)
	}
	target := filepath.FromSlash(*out)
	if !filepath.IsAbs(target) {
		target = filepath.Join(*root, target)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fatal(err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		fatal(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
