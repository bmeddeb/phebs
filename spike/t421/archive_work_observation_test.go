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

func TestArchiveWorkspaceJoined(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, producer := range []uint32{10, 11} {
		for _, mode := range []string{"complete", "failed_native", "failed_walk", "missing_success", "wrong_phase", "over_limit"} {
			t.Run(fmt.Sprintf("%d/%s", producer, mode), func(t *testing.T) {
				raw := archiveWorkTestBindings(producer) + workspaceTestBinding(producer) + workspaceTestPair(producer, 12, 1, 4, 8)
				switch mode {
				case "failed_walk":
					raw += workspaceTestEvent(producer, 12, 'B', 2, 0, 0) + workspaceTestEvent(producer, 12, 'F', 2, 0, 0)
				case "missing_success":
					raw += workspaceTestEvent(producer, 12, 'B', 2, 0, 0)
				case "wrong_phase":
					raw += workspaceTestPair(producer, 13, 2, 4, 8)
				case "over_limit":
					raw += workspaceTestPair(producer, 12, 2, 4, plan.SafetyEnvelope.MaximumDataAllocatedBytes+1)
				}
				output := &checkoutCommandOutput{}
				output.buffer.WriteString(raw)
				got, err := observeArchiveWork(output, plan, producer, [32]byte{1}, true, mode != "failed_native")
				row := got.WorkspaceBytes.Phases[11]
				if (err == nil) != (mode == "complete") || got.WorkspaceBytes.Complete != (mode == "complete") || !got.WorkspaceBytes.Bound || row.Completed == 0 || row.Maximum.LogicalBytes != 4 {
					t.Fatal(mode, got.WorkspaceBytes, err)
				}
				if mode == "over_limit" {
					if !got.WorkspaceBytes.LimitExceeded || got.WorkspaceBytes.Unavailable || row.Maximum.AllocatedBytes != plan.SafetyEnvelope.MaximumDataAllocatedBytes+1 {
						t.Fatal("actual excess lost", got.WorkspaceBytes)
					}
				} else if row.Maximum.AllocatedBytes != 8 || got.WorkspaceBytes.Unavailable != (mode != "complete") {
					t.Fatal("positive prefix lost", got.WorkspaceBytes)
				}
			})
		}
		maximum, err := archiveCheckpointMaximum(plan, producer)
		if err != nil || maximum < 2 {
			t.Fatal(maximum, err)
		}
		out := ExecutionWorkspaceByteObservation{Bound: true, phase: 12, sequence: uint64(maximum - 1)}
		out.Phases[11].Attempts, out.Phases[11].Completed = uint64(maximum-1), uint64(maximum-1)
		for _, line := range []string{workspaceTestEvent(producer, 12, 'B', uint64(maximum), 0, 0), workspaceTestEvent(producer, 12, 'S', uint64(maximum), 4, 8)} {
			if _, err := observeWorkspaceByteEvent([]byte(line), plan, producer, "", &out); err != nil {
				t.Fatal("last archive slot", out, err)
			}
		}
		if _, err := observeWorkspaceByteEvent([]byte(workspaceTestEvent(producer, 12, 'B', uint64(maximum)+1, 0, 0)), plan, producer, "", &out); err == nil || out.Phases[11].Completed != uint64(maximum) {
			t.Fatal("extra archive slot", out, err)
		}
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
	backupSamples, err := archiveCheckpointMaximum(plan, 10)
	if err != nil {
		t.Fatal(err)
	}
	restoreSamples, err := archiveCheckpointMaximum(plan, 11)
	if err != nil {
		t.Fatal(err)
	}
	workspaceReports := uint64(2*80) + 86*(uint64(backupSamples)+uint64(restoreSamples))
	// The previously derived epoch-four WB stream has its own one binding;
	// include it alongside both actual offline streams under the same cap.
	workspaceReports += 79 + 86*(workspaceCheckpointMaximum(5, 9)+workspaceCheckpointMaximum(5, 10)+workspaceCheckpointMaximum(5, 11))
	combined := uint64(11_453_721+2*79) + 2*perProducer + workspaceReports
	if combined >= 64<<20 {
		t.Fatal("known accepted compact subtotal exceeds shared cap", combined)
	}
	t.Logf("offline accepted subtotal per producer=%d; epoch-four plus both offline=%d; census bodies/ordinary logs unproved", perProducer, combined)
}
