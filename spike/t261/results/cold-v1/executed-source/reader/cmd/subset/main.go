package main

import (
	"fmt"
	"os"
	"phebs-spike-t261"
)

// Dialect is a build-time constant so the two pure lanes have separate binaries.
var Dialect string

func main() {
	if Dialect != "postgresql" && Dialect != "mysql" {
		fmt.Fprintln(os.Stderr, "invalid dialect")
		os.Exit(2)
	}
	if err := reader.Main(Dialect, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
