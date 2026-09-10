package t421

import (
	"fmt"
	"strings"
	"testing"
)

func archiveWorkTestBindings(producer uint32) string {
	var raw strings.Builder
	for _, family := range []string{"SR", "CC", "EP", "RM", "RL", "SB", "GC", "OP"} {
		fmt.Fprintf(&raw, "%sB1:%d:sha256:01%s\n", family, producer, strings.Repeat("00", 31))
	}
	return raw.String()
}

func TestArchiveWorkJoinedProfile(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, producer := range []uint32{10, 11} {
		binding := archiveWorkTestBindings(producer)
		if len(binding) != 8*80 {
			t.Fatal("offline bindings changed width")
		}
		for _, test := range []struct {
			name, tail                string
			joined, healthy, complete bool
			count                     uint64
		}{
			{"zero", "", true, true, true, 0},
			{"positive", fmt.Sprintf("RL1:%X:CP\n", producer), true, true, true, 1},
			{"failed-native", fmt.Sprintf("RL1:%X:CP\n", producer), true, false, false, 1},
			{"partial", fmt.Sprintf("RL1:%X:CP\npartial", producer), true, true, false, 1},
			{"unjoined", fmt.Sprintf("RL1:%X:CP\n", producer), false, true, false, 0},
			{"phase", fmt.Sprintf("RL1:%X:DP\n", producer), true, true, false, 0},
			{"legacy-byte", fmt.Sprintf("RL1:%c:CP\n", byte('0'+producer)), true, true, false, 0},
			{"server-profile", lifecycleTestBindings(6), true, true, false, 0},
			{"server-attempt", "ACj1\n", true, true, false, 0},
			{"duplicate", binding, true, true, false, 0},
			{"split-marker", strings.Repeat("z", maxExecutionAttemptLine-2) + fmt.Sprintf("RL1:%X:CP\n", producer), true, true, false, 0},
		} {
			t.Run(fmt.Sprintf("%d/%s", producer, test.name), func(t *testing.T) {
				output := &checkoutCommandOutput{}
				output.buffer.WriteString(binding + test.tail)
				got, err := observeArchiveWork(output, plan, producer, [32]byte{1}, test.joined, test.healthy)
				if (err == nil) != test.complete || got.Complete != test.complete || got.Phases[11].RelationshipProjections != test.count || got.AttemptBound || got.Lifecycle.Complete {
					t.Fatal(got, err)
				}
			})
		}
		for _, family := range []string{"SR", "CC", "EP", "RM", "RL", "SB", "GC", "OP"} {
			line := fmt.Sprintf("%sB1:%d:sha256:01%s\n", family, producer, strings.Repeat("00", 31))
			for _, raw := range []string{strings.Replace(binding, line, "", 1), strings.Replace(binding, line, strings.Replace(line, "sha256:01", "sha256:02", 1), 1)} {
				if got, err := observeExecutionAttempts([]byte(raw), plan, producer, [32]byte{1}, true); err == nil || got.Complete {
					t.Fatal("unbound zero accepted", producer, family, got, err)
				}
			}
		}
	}
}

func TestArchiveWorkExcessAndCoverage(t *testing.T) {
	plan := accountingTestPlan(t)
	plan.WorkEnvelope.Phases[11].ServiceReferences.Maximum = 1
	raw := archiveWorkTestBindings(11) + "RL1:B:CR:0000000000000003\n"
	got, err := observeExecutionAttempts([]byte(raw), plan, 11, [32]byte{1}, true)
	if err == nil || got.Complete || got.Phases[11].ServiceReferences != 3 {
		t.Fatal("full first excess batch lost", got, err)
	}
	output := &checkoutCommandOutput{err: errExecutionAttempts}
	output.buffer.WriteString(archiveWorkTestBindings(11) + "CC1:B:CR\n")
	got, err = observeArchiveWork(output, plan, 11, [32]byte{1}, true, true)
	if err == nil || got.Complete || got.Cache.Complete || got.Cache.Phases[11].RootReads != 1 {
		t.Fatal("incomplete cache prefix lost", got, err)
	}
}

func TestArchiveWorkCompactHeadroom(t *testing.T) {
	plan := accountingTestPlan(t)
	row := plan.WorkEnvelope.Phases[11]
	// Eight mandatory decimal producer bindings, even for measured zero.
	// Census invocation/body records and unrelated diagnostics remain outside
	// this accepted-work subtotal, as in the existing server headroom proof.
	perProducer := uint64(8*80) + 8*(row.GitReads.Maximum+row.ObservationParses.Maximum+row.PublicationWrites.Maximum) +
		25*row.ResolverBlobReads.Maximum + 9*(row.RelationshipBuildAttempts.Maximum+row.RelationshipProjections.Maximum) +
		26*row.ServiceReferences.Maximum + 9*(row.CacheLookups.Maximum+row.CacheMisses.Maximum)
	// Epoch four shares the one output allowance with both offline streams.
	// The server reference-family subtotal is independently fixed in the
	// existing simultaneous-headroom test, plus its two census bindings.
	combined := uint64(11_453_721+2*79) + 2*perProducer
	if combined >= 64<<20 {
		t.Fatal("known accepted compact subtotal exceeds shared cap", combined)
	}
	t.Logf("offline accepted subtotal per producer=%d; epoch-four plus both offline=%d; census bodies/ordinary logs unproved", perProducer, combined)
}
