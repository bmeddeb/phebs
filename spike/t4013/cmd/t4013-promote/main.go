// Command t4013-promote durably publishes one bounded source-free shell artifact.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	temporary := flag.String("temporary", "", "existing staged file")
	output := flag.String("output", "", "new authority file")
	root := flag.String("root", "", "existing durability root")
	flag.Parse()
	if flag.NArg() != 0 || *temporary == "" || *output == "" || *root == "" {
		fail("-temporary, -output, and -root are required")
	}
	if err := t4013.PromoteStagedFile(*temporary, *output, *root); err != nil {
		fail("promote staged file: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-promote: "+format+"\n", args...)
	os.Exit(1)
}
