// Command t322-receipt verifies a frozen private plan and emits only the
// closed, source-free T32.2 receipt shape.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t322"
)

func main() {
	planPath := flag.String("plan", "", "private frozen plan JSON path")
	planDigest := flag.String("plan-digest", "", "SHA-256 recorded when the plan was frozen")
	observationPath := flag.String("observation", "", "source-free observation JSON path")
	outputPath := flag.String("output", "", "new public receipt path")
	flag.Parse()
	if *planPath == "" || *planDigest == "" || *observationPath == "" || *outputPath == "" {
		fail("-plan, -plan-digest, -observation, and -output are required")
	}
	plan, err := os.ReadFile(*planPath)
	if err != nil {
		fail("read private plan: %v", err)
	}
	observation, err := os.ReadFile(*observationPath)
	if err != nil {
		fail("read observation: %v", err)
	}
	receipt, err := t322.BuildReceipt(plan, observation, *planDigest)
	if err != nil {
		fail("build receipt: %v", err)
	}
	file, err := os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fail("create receipt: %v", err)
	}
	if _, err := file.Write(receipt); err != nil {
		_ = file.Close()
		fail("write receipt: %v", err)
	}
	if err := file.Close(); err != nil {
		fail("close receipt: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t322-receipt: "+format+"\n", args...)
	os.Exit(1)
}
