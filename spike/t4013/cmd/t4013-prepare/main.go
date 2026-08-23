// Command t4013-prepare authors dedicated neutral custody outside the module.
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
	workspace := flag.String("workspace", "", "new absolute external custody directory")
	plan := flag.String("plan", "", "frozen T40.13 plan")
	output := flag.String("output", "", "new private prepared-custody manifest")
	confirm := flag.String("confirm", "", "required explicit confirmation phrase")
	basePort := flag.Int("base-port", 41731, "first of two loopback ports")
	flag.Parse()
	if flag.NArg() != 0 || *root == "" || *workspace == "" || *plan == "" || *output == "" {
		fail("-root, -workspace, -plan, and -output are required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	request := t4013.PrepareRequest{
		ModuleRoot: *root, Workspace: *workspace, PlanPath: *plan,
		Confirm: *confirm, BasePort: *basePort,
	}
	prepared, err := t4013.PrepareToOutput(ctx, request, *output)
	if err != nil {
		fail("prepare: %v", err)
	}
	if prepared.Schema == t4013.PreparedSchemaV2 {
		return
	}
	encoded, err := t4013.MarshalPrepared(prepared)
	if err != nil {
		_ = t4013.DestroyPrepared(prepared, *root)
		fail("encode custody: %v", err)
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = t4013.DestroyPrepared(prepared, *root)
		fail("create custody: %v", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(*output)
		_ = t4013.DestroyPrepared(prepared, *root)
		fail("write custody: %v", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(*output)
		_ = t4013.DestroyPrepared(prepared, *root)
		fail("sync custody: %v", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(*output)
		_ = t4013.DestroyPrepared(prepared, *root)
		fail("close custody: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-prepare: "+format+"\n", args...)
	os.Exit(1)
}
