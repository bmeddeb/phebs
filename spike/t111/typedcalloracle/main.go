// Command typedcalloracle emits an extractor-independent census of typed Go
// client calls in an exact, clean Git checkout.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	var opts options
	var rawTags string
	includeTests := true
	opts.moduleCache = filepath.Join("spike", "t111", ".module-cache")
	flag.StringVar(&opts.root, "root", "", "root of the pinned Git checkout (required)")
	flag.StringVar(&opts.commit, "commit", "", "exact full commit SHA (required)")
	flag.StringVar(&opts.moduleCache, "module-cache", opts.moduleCache, "sealed dedicated module cache root")
	flag.StringVar(&opts.goos, "goos", "", "Go analysis target operating system (defaults to oracle host)")
	flag.StringVar(&opts.goarch, "goarch", "", "Go analysis target architecture (defaults to oracle host)")
	flag.BoolVar(&includeTests, "include-tests", true, "include package test variants in the analysis population")
	flag.StringVar(&rawTags, "tags", "", "optional comma-separated Go build tags")
	flag.StringVar(&opts.output, "output", "", "optional JSONL output path (default: stdout)")
	flag.Parse()
	if flag.NArg() != 0 {
		fatalf("unexpected positional arguments: %s", strings.Join(flag.Args(), " "))
	}
	opts.tags = splitTags(rawTags)
	opts.includeTests = &includeTests
	if err := execute(opts, os.Stdout, os.Stderr); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "typedcalloracle: "+format+"\n", args...)
	os.Exit(1)
}
