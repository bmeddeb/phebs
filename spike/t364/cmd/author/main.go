package main

import (
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t364"
)

func main() {
	receipt, err := t364.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := t364.Marshal(receipt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(encoded)
}
