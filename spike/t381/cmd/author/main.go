package main

import (
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t381"
)

func main() {
	encoded, err := t381.Marshal(t381.Build())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(encoded)
}
