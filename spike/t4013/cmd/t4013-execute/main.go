// Command t4013-execute runs and destroys one exact frozen neutral custody.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	root := flag.String("root", "", "absolute module root at the frozen source commit")
	plan := flag.String("plan", "", "absolute frozen T40.13 plan")
	prepared := flag.String("prepared", "", "absolute private prepared-custody manifest")
	observation := flag.String("observation", "", "new absolute source-free observation path outside custody")
	confirm := flag.String("confirm", "", "required execution and teardown confirmation phrase")
	flag.Parse()
	if flag.NArg() != 0 || *root == "" || *plan == "" || *prepared == "" || *observation == "" {
		fail("-root, -plan, -prepared, and -observation are required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	result, err := t4013.Execute(ctx, t4013.ExecuteRequest{
		ModuleRoot: *root, PlanPath: *plan, Prepared: *prepared,
		Observation: *observation, Confirm: *confirm,
	})
	if result.Schema != "" {
		if writeErr := t4013.WriteReturnedObservation(*observation, result); writeErr != nil {
			fail("write observation: %v", writeErr)
		}
	}
	if err != nil {
		fail("execute: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-execute: "+format+"\n", args...)
	os.Exit(1)
}
