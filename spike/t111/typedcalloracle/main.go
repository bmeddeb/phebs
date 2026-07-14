// Command typedcalloracle emits an extractor-independent census of typed Go
// client calls in an exact, clean Git checkout.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	var opts options
	var rawTags string
	flag.StringVar(&opts.root, "root", "", "root of the pinned Git checkout (required)")
	flag.StringVar(&opts.commit, "commit", "", "exact full commit SHA (required)")
	flag.StringVar(&rawTags, "tags", "", "optional comma-separated Go build tags")
	flag.StringVar(&opts.output, "output", "", "optional JSONL output path (default: stdout)")
	flag.Parse()
	if flag.NArg() != 0 {
		fatalf("unexpected positional arguments: %s", strings.Join(flag.Args(), " "))
	}
	opts.tags = splitTags(rawTags)
	if err := execute(opts, os.Stdout, os.Stderr); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "typedcalloracle: "+format+"\n", args...)
	os.Exit(1)
}
