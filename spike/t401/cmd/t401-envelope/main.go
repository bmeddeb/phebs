package main

import (
	"flag"
	"fmt"
	"os"

	t401 "github.com/bmeddeb/phebs/spike/t401"
)

func main() {
	output := flag.String("output", "", "optional path for the canonical retained envelope")
	flag.Parse()
	envelope, err := t401.BuildEnvelope()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := t401.MarshalCanonical(envelope)
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
