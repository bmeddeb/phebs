// Command t4013-freeze emits the prospective neutral convergence plan.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	root := flag.String("root", "", "absolute module root containing the frozen T40.1 inputs")
	sourceCommit := flag.String("source-commit", "", "exact 40-hex phebs source commit to measure")
	dataParent := flag.String("data-parent", "", "absolute ceremony filesystem root to preflight")
	output := flag.String("output", "", "new output path")
	bindHostToolchain := flag.Bool("bind-host-toolchain", false, "bind exact Go, Git, and SurrealDB host executables")
	flag.Parse()
	if flag.NArg() != 0 || *root == "" || *sourceCommit == "" || *output == "" {
		fail("-root, -source-commit, and -output are required")
	}
	if err := t4013.VerifyInputs(*root); err != nil {
		fail("verify inputs: %v", err)
	}
	var plan t4013.Plan
	var err error
	if *bindHostToolchain {
		plan, err = t4013.FrozenHostPlanAtCheckout(context.Background(), *root, *sourceCommit)
	} else {
		plan, err = t4013.FrozenPlan(*sourceCommit)
	}
	if err != nil {
		fail("build plan: %v", err)
	}
	if *bindHostToolchain {
		if *dataParent == "" {
			fail("-data-parent is required with -bind-host-toolchain")
		}
		if _, err := t4013.HostPreflight(context.Background(), *dataParent, plan); err != nil {
			fail("preflight ceremony host: %v", err)
		}
	}
	encoded, err := t4013.MarshalPlan(plan)
	if err != nil {
		fail("encode plan: %v", err)
	}
	writeNew(*output, encoded)
	fmt.Printf("%s\n", t4013.PlanDigest(encoded))
}

func writeNew(path string, content []byte) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail("create output: %v", err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		fail("write output: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		fail("sync output: %v", err)
	}
	if err := file.Close(); err != nil {
		fail("close output: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-freeze: "+format+"\n", args...)
	os.Exit(1)
}
