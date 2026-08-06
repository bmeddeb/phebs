package main

import (
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t375"
)

func main() {
	encoded, err := t375.Marshal(t375.Build())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(encoded)
}
