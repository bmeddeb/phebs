// Command t4013-receipt validates the frozen plan and source-free observation.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	planPath := flag.String("plan", "", "frozen plan path")
	planDigest := flag.String("plan-digest", "", "exact sha256 digest of the plan bytes")
	observationPath := flag.String("observation", "", "source-free observation path")
	output := flag.String("output", "", "new receipt path")
	flag.Parse()
	if flag.NArg() != 0 || *planPath == "" || *planDigest == "" || *observationPath == "" || *output == "" {
		fail("-plan, -plan-digest, -observation, and -output are required")
	}
	plan, decodedPlan, err := t4013.ReadPlanControl(*planPath)
	if err != nil {
		fail("read plan: %v", err)
	}
	var observation []byte
	if decodedPlan.Schema == t4013.PlanSchemaV25 {
		observation, err = t4013.ResumeObservation(*observationPath, plan, *planDigest)
	} else {
		observation, err = os.ReadFile(*observationPath)
	}
	if err != nil {
		fail("read or resume observation: %v", err)
	}
	receipt, err := t4013.BuildReceipt(plan, observation, *planDigest)
	if err != nil {
		fail("build receipt: %v", err)
	}
	if decodedPlan.Schema == t4013.PlanSchemaV25 {
		if err := t4013.WriteReceipt(*output, receipt); err != nil {
			fail("write output: %v", err)
		}
	} else {
		writeNew(*output, receipt)
	}
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
	_, _ = fmt.Fprintf(os.Stderr, "t4013-receipt: "+format+"\n", args...)
	os.Exit(1)
}
