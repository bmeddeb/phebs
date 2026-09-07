package t421

import (
	"context"
	"strings"
	"testing"
)

func terminalPrefixTestBytes() (string, string) {
	input := "sha256:01" + strings.Repeat("00", 31) + "\n"
	return "ATB1:4:" + input + "SRB1:4:" + input + "IXB1:4:" + input +
		"A6j1\nSR1:4:6\nIb6\nI6\nIe6:1\n", "TFE1:4:8:" + input
}

func TestExecutionTerminalFooterFraming(t *testing.T) {
	prefix, footer := terminalPrefixTestBytes()
	if len(footer) != 81 {
		t.Fatal("footer changed fixed wire size")
	}
	for _, test := range []struct {
		name, raw string
		seen, ok  bool
	}{
		{"valid", prefix + footer, true, true},
		{"ordinary tail", prefix + footer + "2026/09/07 stopped\n", true, true},
		{"long ordinary tail", prefix + footer + strings.Repeat("z", maxExecutionAttemptLine*3) + "\n", true, true},
		{"no footer", prefix, false, true}, // Only the genuine terminal caller requires it.
		{"duplicate", prefix + footer + footer, true, false},
		{"wrong input", prefix + strings.Replace(footer, "sha256:01", "sha256:02", 1), false, false},
		{"wrong producer", prefix + strings.Replace(footer, "TFE1:4:", "TFE1:5:", 1), false, false},
		{"wrong phase", prefix + strings.Replace(footer, ":8:", ":7:", 1), false, false},
		{"wrong version", prefix + strings.Replace(footer, "TFE1", "TFE2", 1), false, false},
		{"uppercase input", prefix + strings.Replace(footer, "sha256:01", "sha256:AF", 1), false, false},
		{"partial footer", prefix + strings.TrimSuffix(footer, "\n"), false, false},
		{"partial predecessor", prefix + "unfinished" + footer, false, false},
		{"partial tail", prefix + footer + "diagnostic", true, false},
		{"post source", prefix + footer + "SR1:4:8\n", true, false},
		{"post job", prefix + footer + "A8j1\n", true, false},
		{"post index", prefix + footer + "Ib8\n", true, false},
		{"post index binding", prefix + footer + "IXB1:4:bad\n", true, false},
		{"post raw child", prefix + footer + "ZIE1:0\n", true, false},
		{"post legacy", prefix + footer + "2026/09/07 job lifecycle: {}\n", true, false},
		{"post parse", prefix + footer + "OP1:4:8\n", true, false},
		{"post parse binding", prefix + footer + "OPB1:4:bad\n", true, false},
		{"embedded index", prefix + "diagnostic I6\n" + footer, false, false},
		{"split index", prefix + strings.Repeat("z", maxExecutionAttemptLine-1) + "I6\n" + footer, false, false},
		{"split post source", prefix + footer + strings.Repeat("z", maxExecutionAttemptLine-2) + "SR1:4:8\n", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			seen, err := executionTerminalFooter([]byte(test.raw), [32]byte{1})
			if seen != test.seen || (err == nil) != test.ok {
				t.Fatal("footer framing", seen, err)
			}
		})
	}
}

func TestExecutionTerminalFooterCannotMintProof(t *testing.T) {
	prefix, footer := terminalPrefixTestBytes()
	plan := accountingTestPlan(t)
	for _, mode := range []string{"ordinary", "entered", "requested", "unjoined", "missing", "malformed index", "missing binding"} {
		t.Run(mode, func(t *testing.T) {
			raw := prefix + footer
			switch mode {
			case "missing":
				raw = prefix
			case "malformed index":
				raw = strings.Replace(raw, "Ie6:1\n", "Ie6:0\n", 1)
			case "missing binding":
				raw = strings.Replace(raw, "ATB1:4:sha256:01"+strings.Repeat("00", 31)+"\n", "", 1)
			}
			output := &checkoutCommandOutput{remaining: int64(len(raw)), cancel: func() {}}
			if _, err := output.Write([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, epoch: ExecutionEpochConfig{Epoch: 3}, output: output,
				attemptInput: [32]byte{1}, terminalEntered: mode != "ordinary", terminalRequested: mode != "entered", checkpointAllowed: true}
			result := ExecutionEpochOneResult{RootStarted: true, RootJoined: mode != "unjoined", SessionEmpty: true}
			if err := run.finishAttemptObservation(t.Context(), &result, executionProcessDeath{}, nil); err == nil || result.Attempts.Complete || result.IndexOffers.Complete {
				t.Fatal("source-free footer or flags manufactured terminal proof")
			}
			if mode != "unjoined" && mode != "missing binding" && (result.Attempts.Phases[5].JobAttempts != 1 || result.Attempts.Phases[5].SourceBlobAttempts != 1 || result.IndexOffers.Phases[5].Offers != 1) {
				t.Fatal("failed terminal proof erased observed prefix", result.Attempts, result.IndexOffers)
			}
		})
	}
}

func TestExecutionTerminalOutputStickyRefusal(t *testing.T) {
	// Cancellation need not be visible in this caller: native SIGKILL can
	// mask a copy-pump error, and a newline-ended buffer can appear complete.
	raw := attemptTestBindings() + "IXB1:2:sha256:01" + strings.Repeat("00", 31) + "\nA2j1\n"
	output := &checkoutCommandOutput{remaining: int64(len(raw)), cancel: func() {}}
	if _, err := output.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	_, refusal := output.Write([]byte("lost line\n"))
	if refusal == nil || output.err != refusal {
		t.Fatal("missing sticky writer refusal")
	}
	if n, err := output.Write(nil); n != 0 || err != refusal || output.buffer.String() != raw {
		t.Fatal("failed writer resumed or mutated retained prefix")
	}
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: accountingTestPlan(t)}, output: output, attemptInput: [32]byte{1}}
	result := ExecutionEpochOneResult{RootJoined: true}
	if err := run.finishAttemptObservation(context.Background(), &result, executionProcessDeath{}, nil); err == nil || result.Attempts.Complete || result.IndexOffers.Complete || result.Attempts.Phases[1].JobAttempts != 1 {
		t.Fatal("stable newline prefix hid failed writer", result.Attempts)
	}
}
