package main

import (
	"flag"
	"fmt"
	"os"

	t401 "github.com/bmeddeb/phebs/spike/t401"
)

func main() {
	structuralFirst := flag.String("structural-first", "", "absolute first frozen structural author output")
	structuralSecond := flag.String("structural-second", "", "absolute second frozen structural author output")
	semanticFirst := flag.String("semantic-first", "", "absolute first frozen semantic author output")
	semanticSecond := flag.String("semantic-second", "", "absolute second frozen semantic author output")
	output := flag.String("output", "", "optional canonical receipt output path")
	flag.Parse()
	receipt, err := t401.BuildReproducibilityReceipt(t401.ReproducibilityRequest{
		Structural: [2]string{*structuralFirst, *structuralSecond},
		Semantic:   [2]string{*semanticFirst, *semanticSecond},
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
