package t421

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestExecutionSetupTokenDiagnosticCollision(t *testing.T) {
	plan := accountingTestPlan(t)
	header := attemptTestBindings() + "IXB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	events := "A2j1\nSR1:2:2\nOP1:2:2\nIb2\nI2\nIe2:1\nA4c0\nSR1:2:4\nOP1:2:4\nIb4\nI4\nIe4:1\n"
	for _, marker := range []string{"Ib4opaque", "Ie4opaque", "If4opaque", "IXB", "ZIB", "ZIE", "TFE", "ATB", "OPB", "SRB1", "SR1", "I4", "Ib4", "A2c0"} {
		t.Run(marker, func(t *testing.T) {
			token := marker + strings.Repeat("A", 43-len(marker))
			if marker == "I4" || marker == "Ib4" || marker == "A2c0" {
				token = strings.Repeat("A", 43-len(marker)) + marker
			}
			if decoded, err := base64.RawURLEncoding.Strict().DecodeString(token); err != nil || len(decoded) != 32 {
				t.Fatal("synthetic token must match production encoding")
			}
			line := "2026/09/08 04:24:42 first-run setup token: " + token + "\n"
			raw := header + line + events
			output := &checkoutCommandOutput{remaining: int64(len(raw)), cancel: func() {}}
			if _, err := output.Write([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, output: output, attemptInput: [32]byte{1},
				physicalUsed: true, retainParent: true, epoch: ExecutionEpochConfig{Epoch: 1}}
			result := ExecutionEpochOneResult{RootJoined: true}
			if err := run.finishAttemptObservation(t.Context(), &result, executionProcessDeath{}, nil); err != nil || !result.Attempts.Complete || !result.IndexOffers.Complete {
				t.Fatal("ordinary setup payload refused joined metrics", err)
			}
			for _, phase := range []int{1, 3} {
				if result.Attempts.Phases[phase] != (ExecutionAttemptCount{JobAttempts: 1, SourceBlobAttempts: 1, ObservationParses: 1}) ||
					result.IndexOffers.Phases[phase] != (ExecutionIndexOfferCount{Offers: 1, StartedChildren: 1, EndedChildren: 1, SettledOffers: 1}) {
					t.Fatal("diagnostic payload changed counts")
				}
			}
			if got, err := observeExecutionAttempts([]byte(line), plan, 2, [32]byte{1}, true); err == nil || got != (ExecutionAttemptObservation{}) {
				t.Fatal("setup payload supplied attempt/source authority")
			}
			if got, err := observeExecutionIndexOffers([]byte(line), plan, 2, [32]byte{1}, true, true); err == nil || got != (ExecutionIndexObservation{}) {
				t.Fatal("setup payload supplied index authority")
			}
			prefix, footer := terminalPrefixTestBytes()
			for _, raw := range []string{prefix + line + footer, prefix + footer + line} {
				if seen, err := executionTerminalFooter([]byte(raw), [32]byte{1}); !seen || err != nil {
					t.Fatal("ordinary diagnostic hid or invalidated footer", seen, err)
				}
			}
		})
	}
}

func TestExecutionSetupTokenDiagnosticBoundary(t *testing.T) {
	line := "2026/09/08 04:24:42 first-run setup token: TFE" + strings.Repeat("A", 40) + "\n"
	if !executionSetupTokenDiagnostic([]byte(line)) {
		t.Fatal("valid source envelope refused")
	}
	for _, test := range []struct{ name, raw string }{
		{"timestamp", strings.Replace(line, "/09/", "/99/", 1)},
		{"noncanonical timestamp", strings.Replace(line, " 04:", "  4:", 1)},
		{"label", strings.Replace(line, "first-run", "first-rum", 1)},
		{"short", strings.TrimSuffix(line, "A\n") + "\n"},
		{"long", strings.TrimSuffix(line, "\n") + "A\n"},
		{"partial", strings.TrimSuffix(line, "\n")},
		{"padding bits", strings.TrimSuffix(line, "A\n") + "B\n"},
		{"padding", strings.TrimSuffix(line, "A\n") + "=\n"},
		{"alphabet", strings.TrimSuffix(line, "A\n") + "+\n"},
		{"cr", strings.TrimSuffix(line, "A\n") + "\r\n"},
		{"tab", strings.TrimSuffix(line, "A\n") + "\t\n"},
		{"embedded newline", strings.Replace(line, "TFE", "TFE\n", 1)},
		{"colon", strings.Replace(line, "TFEA", "TFE:", 1)},
		{"prepended", "I4" + line},
		{"appended compact", strings.TrimSuffix(line, "\n") + "I4\n"},
		{"split compact", strings.TrimSuffix(line, "\n") + strings.Repeat("z", maxExecutionAttemptLine) + "I4\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if executionSetupTokenDiagnostic([]byte(test.raw)) {
				t.Fatal("non-native diagnostic envelope accepted")
			}
			if _, err := executionTerminalFooter([]byte(test.raw), [32]byte{1}); err == nil {
				t.Fatal("malformed or embedded record escaped strict matcher")
			}
		})
	}
	prefix, footer := terminalPrefixTestBytes()
	for _, record := range []string{"I8\n", "Ib8\n", "Ie8:1\n", "If8:0\n", "ATB1:4:bad\n", "SR1:4:8\n", "OP1:4:8\n", "IXB1:4:bad\n", footer} {
		if _, err := executionTerminalFooter([]byte(prefix+footer+line+record), [32]byte{1}); err == nil {
			t.Fatal("ordinary diagnostic hid subsequent reserved record")
		}
	}
}

func terminalPrefixTestBytes() (string, string) {
	input := "sha256:01" + strings.Repeat("00", 31) + "\n"
	return "ATB1:4:" + input + "SRB1:4:" + input + "OPB1:4:" + input + "IXB1:4:" + input +
		"A6j1\nSR1:4:6\nOP1:4:6\nIb6\nI6\nIe6:1\n", "TFE1:4:8:" + input
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
			if mode != "unjoined" && mode != "missing binding" && (result.Attempts.Phases[5].JobAttempts != 1 || result.Attempts.Phases[5].SourceBlobAttempts != 1 || result.Attempts.Phases[5].ObservationParses != 1 || result.IndexOffers.Phases[5].Offers != 1) {
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
