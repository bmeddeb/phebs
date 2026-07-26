// Command glossary-gen writes or verifies every canonical glossary projection.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/internal/glossary/genrepo"
)

func main() {
	root := flag.String("root", "", "repository root")
	write := flag.Bool("write", false, "write generated projections")
	verify := flag.Bool("verify", false, "verify checked-in projections")
	flag.Parse()

	if *root == "" || *write == *verify {
		fmt.Fprintln(os.Stderr, "-root is required and exactly one of -write or -verify must be selected")
		os.Exit(2)
	}
	var err error
	if *write {
		err = genrepo.WriteRepository(*root)
	} else {
		err = genrepo.VerifyRepository(*root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
