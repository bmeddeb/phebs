package t421

import (
	"strings"
	"testing"
)

func TestSourceAttemptCompactParser(t *testing.T) {
	plan := accountingTestPlan(t)
	header := "SRB1:2:sha256:01" + strings.Repeat("00", 31) + "\n"
	for _, test := range []struct {
		name, raw string
		count     uint64
		complete  bool
	}{
		{"zero", header, 0, true}, {"repeat", header + "SR1:2:2\nSR1:2:2\n", 2, true},
		{"missing", "", 0, false}, {"unbound", "SR1:2:2\n", 0, false}, {"duplicate", header + header, 0, false},
		{"unknown version", header + "SR2:2:2\n", 0, false}, {"unknown binding", strings.Replace(header, "SRB1:", "SRB2:", 1), 0, false},
		{"wrong binding", strings.Replace(header, "01", "02", 1), 0, false},
		{"partial", header + "SR1:2:2\nSR1:2:", 1, false}, {"producer", header + "SR1:3:2\n", 0, false},
		{"phase", header + "SR1:2:5\n", 0, false}, {"zero ceiling", header + "SR1:2:3\n", 0, false},
		{"prefix", header + "junkSR1:2:2\n", 0, false}, {"long split", header + strings.Repeat("x", maxExecutionAttemptLine-2) + "SR1:2:2\n", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := observeExecutionAttempts([]byte(test.raw), plan, 2, [32]byte{1}, true)
			if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[1].SourceBlobAttempts != test.count {
				t.Fatal(got, err)
			}
		})
	}
}

func TestSourceAttemptCompactHeadroom(t *testing.T) {
	plan := accountingTestPlan(t)
	var attempts uint64
	for _, phase := range executionProducerPhases(2) {
		attempts += plan.WorkEnvelope.Phases[phase-1].GitReads.Maximum
	}
	const headerBytes = 79
	bytes := attempts*8 + headerBytes
	if attempts != 2_057_768 || bytes != 16_462_223 || (64<<20)-bytes != 50_646_641 {
		t.Fatal(attempts, bytes)
	}
	// The remaining bytes are shared with real job/chunk/candidate/ordinary
	// logs, not a proof that every simultaneous allowed maximum fits.
}
