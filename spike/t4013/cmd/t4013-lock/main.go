// Command t4013-lock enters one V25 run-root mutation lock and execs a command.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	runRoot := flag.String("run-root", "", "absolute V25 ceremony run root")
	adopt := flag.Bool("adopt", false, "validate and acquire an inherited lock descriptor")
	flag.Parse()
	arguments := flag.Args()
	if *runRoot == "" {
		fail("-run-root is required")
	}
	if *adopt {
		if len(arguments) != 0 {
			fail("-adopt does not accept a command")
		}
		if err := t4013.ValidateInheritedRunRootLock(*runRoot); err != nil {
			fail("adopt inherited lock: %v", err)
		}
		return
	}
	if len(arguments) == 0 {
		fail("a command after -- is required")
	}
	if err := t4013.ExecRunRootLocked(*runRoot, arguments[0], arguments[1:]); err != nil {
		fail("lock and exec: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-lock: "+format+"\n", args...)
	os.Exit(1)
}
