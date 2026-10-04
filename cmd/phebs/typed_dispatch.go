package main

import (
	"context"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/typedbazel/launcher"
	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

// Internal roles run before ordinary command bootstrap. The supervisor's caller
// must immediately exit: namespace PID1 exit is its descendant shutdown fence.
// These roles neither register a provider nor accept a public execution option.
func runTypedCommand() (handled bool, code int) {
	adapter := os.Args[0] == provider.NativeAdapterPath
	role := ""
	if len(os.Args) > 1 {
		role = os.Args[1]
	}
	if !adapter {
		switch role {
		case typedsandbox.SupervisorCommand, typedsandbox.WorkerCommand, "__native_bazel", "__plan_helper":
		default:
			return false, 0
		}
	}
	// Presence, including an empty selector, forbids bypassing exact production
	// accounting. This is checked before any control read, child or worker work.
	if _, selected := os.LookupEnv(dispatchadmission.ProductionEnvironment); selected {
		typedsandbox.RefuseSite(typedsandbox.SiteSelector)
		return true, 125
	}
	if adapter {
		return true, typedExit(provider.RunAdapter(context.Background()))
	}
	switch role {
	case typedsandbox.SupervisorCommand:
		if len(os.Args) != 6 {
			typedsandbox.RefuseSite(typedsandbox.SiteArgv)
			return true, 125
		}
		return true, typedsandbox.Supervisor()
	case typedsandbox.WorkerCommand:
		ctx := context.Background()
		invocation, err := typedsandbox.ReadWorkerInvocation(ctx)
		if err != nil {
			return true, 125
		}
		controls, err := typedworkspace.LoadWorkerControls(ctx, invocation)
		if err != nil {
			return true, 125
		}
		raw, err := provider.Run(ctx, provider.Invocation{
			Parent: controls.Parent, Execution: controls.Execution, Profile: controls.Profile,
			Inventory: controls.Inventory, Plan: controls.Plan, Allowance: controls.Allowance, Phase: controls.Phase,
		})
		// A classified failure may carry private evidence. Preserve it, but never
		// turn a nonzero worker outcome into successful allowance/publication proof.
		if len(raw) != 0 {
			if n, writeErr := os.Stdout.Write(raw); writeErr != nil || n != len(raw) {
				return true, 125
			}
		}
		return true, typedExit(err)
	default:
		// Child roles intentionally have distinct environments from the primary
		// worker. Their owned implementations validate their exact inputs.
		if !typedindex.AdmittedNativeWorker() || os.Getuid() != 65534 || os.Getgid() != 65534 || os.Getpid() == 1 {
			return true, 125
		}
		if role == "__plan_helper" {
			return true, typedExit(planner.RunHelper(os.Args[2:]))
		}
		if len(os.Args) < 4 || (os.Args[2] != "load" && os.Args[2] != "scip") {
			return true, 125
		}
		return true, typedExit(launcher.RunNativeCompatibilityBazel(context.Background(), os.Args[2], os.Args[3:]))
	}
}

// Helper diagnostics are private structured evidence. Do not echo raw errors
// containing target paths, source or daemon output on the command log boundary.
func typedExit(err error) int {
	if err != nil {
		return 125
	}
	return 0
}
