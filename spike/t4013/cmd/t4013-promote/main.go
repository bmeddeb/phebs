// Command t4013-promote durably stages or publishes one bounded source-free shell artifact.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	discard := flag.String("discard", "", "incomplete stage to remove durably")
	stage := flag.String("stage", "", "existing stage to sync without publication")
	temporary := flag.String("temporary", "", "existing staged file")
	output := flag.String("output", "", "new authority file")
	root := flag.String("root", "", "existing durability root")
	flag.Parse()
	discardMode := *discard != "" && *stage == "" && *temporary == "" && *output == ""
	stageMode := *discard == "" && *stage != "" && *temporary == "" && *output == ""
	promoteMode := *discard == "" && *stage == "" && *temporary != "" && *output != ""
	if flag.NArg() != 0 || *root == "" || !discardMode && !stageMode && !promoteMode {
		fail("exactly one of -discard, -stage, or the -temporary/-output pair is required with -root")
	}
	if discardMode {
		if err := t4013.DiscardStagedFile(*discard, *root); err != nil {
			fail("discard staged file: %v", err)
		}
		return
	}
	if *stage != "" {
		if err := t4013.SyncStagedFile(*stage, *root); err != nil {
			fail("sync staged file: %v", err)
		}
		return
	}
	if err := t4013.PromoteStagedFile(*temporary, *output, *root); err != nil {
		fail("promote staged file: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-promote: "+format+"\n", args...)
	os.Exit(1)
}
