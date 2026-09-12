//go:build darwin

package t421

import (
	"context"
	"testing"
	"time"
)

// Only the flow's prework bookkeeping is modeled. Image Hold/Check/Close use
// the actual fixed native files; no engine, mounted teardown, issuer or signer
// command is exercised here.
func modeledSystemProfileFlow() *ExecutionEpochOne {
	return &ExecutionEpochOne{
		plan:    Plan{Schema: PlanV3Schema},
		epochs:  &ExecutionEpochConfigCustody{author: &ExecutionAuthorCustody{}},
		release: func() {},
	}
}

func holdSystemProfilePair(t *testing.T) [2]*ExecutionSystemToolCustody {
	t.Helper()
	requireExternalToolFrozenHost(t)
	var tools [2]*ExecutionSystemToolCustody
	t.Cleanup(func() {
		for _, tool := range tools {
			if err := tool.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	for i, role := range []string{"sh", "ssh-keygen"} {
		var err error
		tools[i], err = HoldExecutionSystemTool(t.Context(), role)
		if err != nil {
			t.Fatal(err)
		}
	}
	return tools
}

func TestExecutionProfileSystemToolsActualBorrowedLifetime(t *testing.T) {
	tools := holdSystemProfilePair(t)
	flow := modeledSystemProfileFlow()
	if err := flow.prepareProfileSystemTools(t.Context(), tools[0], tools[1]); err != nil {
		t.Fatal(err)
	}
	if flow.profileSystemTools != tools || flow.profileTools != ([2]*ExecutionToolCustody{}) {
		t.Fatal("borrowed fixed images entered mounted inputs")
	}
	observed := flow.profileSystemImages
	for i, role := range []string{"sh", "ssh-keygen"} {
		identity, path, err := tools[i].Check(t.Context(), role)
		if err != nil || observed[i] != (executionProfileSystemImage{Identity: identity, Path: path}) {
			t.Fatal("observation was not actual held-image evidence", err)
		}
	}
	if flow.prepareProfileSystemTools(t.Context(), tools[0], tools[1]) == nil {
		t.Fatal("repeat observation admitted")
	}
	if err := flow.Close(); err != nil {
		t.Fatal(err)
	}
	// Real flow.Close, but unused controller/store bookkeeping: not native
	// operational teardown. It must not own either of these borrowed handles.
	for i, role := range []string{"sh", "ssh-keygen"} {
		if _, _, err := tools[i].Check(t.Context(), role); err != nil {
			t.Fatal("flow close released outer custody", err)
		}
		if err := tools[i].Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := tools[i].Check(t.Context(), role); err == nil {
			t.Fatal("closed outer custody accepted")
		}
	}
	if flow.profileSystemImages != observed {
		t.Fatal("outer release rewrote detached observations")
	}
	observed[0].Identity.Role = "caller mutation"
	if flow.profileSystemImages[0].Identity.Role != "sh" {
		t.Fatal("observation aliases caller value")
	}
}

func TestExecutionProfileSystemToolsPreworkRefusals(t *testing.T) {
	tools := holdSystemProfilePair(t)
	for _, mode := range []string{"nil_context", "canceled", "v1", "v2", "closed", "started", "used", "authored", "author_closed", "author_active", "epoch_closed", "epoch_active", "released", "repeat"} {
		t.Run(mode, func(t *testing.T) {
			flow := modeledSystemProfileFlow()
			ctx := t.Context()
			switch mode {
			case "nil_context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "v1":
				flow.plan.Schema = PlanSchema
			case "v2":
				flow.plan.Schema = PlanV2Schema
			case "closed":
				flow.closed = true
			case "started":
				flow.authorStarted = time.Now()
			case "used":
				flow.used = true
			case "authored":
				flow.authored = true
			case "author_closed":
				flow.epochs.author.closed = true
			case "author_active":
				flow.epochs.author.active = true
			case "epoch_closed":
				flow.epochs.closed = true
			case "epoch_active":
				flow.epochs.active = true
			case "released":
				flow.epochs.released = 1
			case "repeat":
				flow.profileSystemUsed = true
			}
			if flow.prepareProfileSystemTools(ctx, tools[0], tools[1]) == nil || flow.profileSystemTools != ([2]*ExecutionSystemToolCustody{}) ||
				flow.profileSystemImages != ([2]executionProfileSystemImage{}) {
				t.Fatal("invalid prework returned observations")
			}
			if flow.profileSystemUsed != (mode == "repeat") {
				t.Fatal("prework refusal consumed observation attempt")
			}
		})
	}
}

func TestExecutionProfileSystemToolsOwnedCheckFailureSticks(t *testing.T) {
	for _, mode := range []string{"closed", "drift", "role", "close_error"} {
		t.Run(mode, func(t *testing.T) {
			tools := holdSystemProfilePair(t)
			switch mode {
			case "closed":
				if err := tools[1].Close(); err != nil {
					t.Fatal(err)
				}
			case "drift":
				tools[1].volume[0] ^= 1 // Change only owned fixture metadata, never the system file.
			case "role":
				tools[1].identity.Role = "sh"
			case "close_error":
				if err := tools[1].file.Close(); err != nil {
					t.Fatal(err)
				}
				if tools[1].Close() == nil {
					t.Fatal("actual descriptor-close error was hidden")
				}
			}
			flow := modeledSystemProfileFlow()
			if flow.prepareProfileSystemTools(t.Context(), tools[0], tools[1]) == nil || !flow.profileSystemUsed ||
				flow.profileSystemTools != ([2]*ExecutionSystemToolCustody{}) || flow.profileSystemImages != ([2]executionProfileSystemImage{}) {
				t.Fatal("failed actual check retained a partial pair or renewed attempt")
			}
			if flow.prepareProfileSystemTools(t.Context(), tools[0], tools[1]) == nil {
				t.Fatal("failed observation retried")
			}
		})
	}
}
