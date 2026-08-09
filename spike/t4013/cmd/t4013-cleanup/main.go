// Command t4013-cleanup destroys one exact plan-bound prepared custody.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	root := flag.String("root", "", "absolute module root at the frozen source commit")
	plan := flag.String("plan", "", "absolute frozen T40.13 plan")
	prepared := flag.String("prepared", "", "absolute private prepared-custody manifest")
	confirm := flag.String("confirm", "", "required cleanup confirmation phrase")
	flag.Parse()
	if flag.NArg() != 0 || *root == "" || *plan == "" || *prepared == "" {
		fail("-root, -plan, and -prepared are required")
	}
	if err := t4013.CleanupPrepared(*root, *plan, *prepared, *confirm); err != nil {
		fail("cleanup: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-cleanup: "+format+"\n", args...)
	os.Exit(1)
}
