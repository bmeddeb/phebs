//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

// Discriminate whether the concrete SDK-owner boundary can erase the first
// store-attempt charge. This is an unprivileged component probe, not selected
// producer-5 bootstrap, checkpoint authority, or native qualification.
func TestGenerationTransitionSelectedSDKRefusalAccounting(t *testing.T) {
	setup, stopSetup := context.WithTimeout(t.Context(), 30*time.Second)
	defer stopSetup()
	dir := t.TempDir()
	ordinary, err := OpenLocalMemory(setup, dir)
	if err != nil {
		t.Fatal("start private component store refused")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ordinary.Close(ctx); err != nil {
			t.Error("private component engine closure refused")
		}
	})
	spec := generationSpec("example.invalid/sdk-transition-probe", "sha256:"+strings.Repeat("9", 64))
	spec.Stage, spec.ResourceClass, spec.TotalItems, spec.ChunkItems = "extraction-partitions", GenerationResourceExtraction, 1, 1
	schedule, err := ordinary.EnqueueGenerationSchedule(setup, spec)
	if err != nil || schedule == nil {
		t.Fatal("prepare actual component schedule refused")
	}
	if _, err := ordinary.ExpandGenerationSchedule(setup, spec.Repository, spec.Stage, spec.Generation); err != nil {
		t.Fatal("expand actual component schedule refused")
	}
	chunk, err := ordinary.ClaimGenerationChunk(setup, spec.ResourceClass, "sdk-transition-probe")
	if err != nil || chunk == nil {
		t.Fatal("claim actual component schedule refused")
	}
	local, err := ReadLocalRuntime(dir)
	if err != nil {
		t.Fatal("read private component descriptor refused")
	}
	// Existing concrete owner/client/SA transport fixture, with its original
	// five-second context and 40-call/two-transaction capacities. No synthetic
	// SDK sender, admission override, namespace implementation or policy change.
	ctx, owner, controller := storeAccountingFixture(t, 40, 2)
	selected, err := openWithOwner(ctx, local.Endpoint, "root", local.Pass, "phebs", "phebs", owner)
	if err != nil || selected == nil || selected.accounting != owner {
		t.Fatal("actual selected SDK connection binding refused")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := selected.Close(ctx); err != nil {
			t.Error("actual selected SDK connection closure refused")
		}
	})
	request := GenerationStaleLeaseTransitionRequest{Point: GenerationStaleLeaseTransitionCheckpointHit,
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation, ResourceClass: spec.ResourceClass,
		ScheduleDigest: schedule.Digest, ChunkIdentity: chunk.Identity, Length: 1}
	read := func(t *testing.T, mode string, request GenerationStaleLeaseTransitionRequest, canceled bool, want uint64) error {
		t.Helper()
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		accounted, ledger, err := readaccounting.Start(bounded, readaccounting.Counts{StoreReadAttempts: 2})
		if err != nil {
			t.Fatal("component read ledger refused")
		}
		if canceled {
			cancel()
		}
		value, readErr := selected.ReadGenerationStaleLeaseTransition(accounted, request)
		counts, finishErr := ledger.Finish()
		if finishErr != nil || counts != (readaccounting.Counts{StoreReadAttempts: want}) {
			t.Fatalf("actual SDK refusal charge changed: mode=%s counts=%+v finish_error=%t", mode, counts, finishErr != nil)
		}
		if mode == "healthy" {
			if readErr != nil || value.ChunkIdentity != chunk.Identity || value.PrivateLeaseTokenDigest != GenerationLeaseTokenDigest(chunk.LeaseToken) {
				t.Fatal("actual SDK healthy transition refused")
			}
		} else if readErr == nil || value != (GenerationStaleLeaseTransition{}) {
			t.Fatal("actual SDK negative gained transition credit")
		}
		t.Logf("sdk_transition mode=%s controls=%d store=%d refused=%t fenced=%t canceled=%t", mode,
			counts.ControlFileReads, counts.StoreReadAttempts, readErr != nil, errors.Is(readErr, storeaccounting.ErrFenced), errors.Is(readErr, storeaccounting.ErrCanceled))
		return readErr
	}
	t.Run("healthy", func(t *testing.T) { _ = read(t, "healthy", request, false, 2) })
	t.Run("invalid_target", func(t *testing.T) {
		bad := request
		bad.ChunkIdentity = "sha256:" + strings.Repeat("1", 64)
		_ = read(t, "invalid_target", bad, false, 0)
	})
	t.Run("fenced_owner", func(t *testing.T) {
		// Parent fence precedes the real owner/client checkpoint protocol.
		if err := controller.Fence(); err != nil {
			t.Fatal("actual SDK parent fence refused")
		}
		if err := owner.checkpoint(ctx); err != nil {
			t.Fatal("actual SDK owner checkpoint refused")
		}
		if err := read(t, "fenced_owner", request, false, 1); !errors.Is(err, storeaccounting.ErrFenced) {
			t.Fatal("actual fenced SDK owner did not preserve its refusal")
		}
	})
	if t.Failed() {
		return
	}
	if err := controller.Advance(); err != nil {
		t.Fatal("actual SDK component phase advance refused")
	}
	if err := owner.Resume(2); err != nil {
		t.Fatal("actual SDK component owner resume refused")
	}
	if err := owner.Check(ctx); err != nil {
		t.Fatal("actual SDK canceled-call control requires a live unfenced owner")
	}
	// Snapshot before the deliberately poisoned canceled-call control. This
	// is measured component state, not claimed closure or native acceptance.
	prefix, err := controller.Snapshot()
	if err != nil || len(prefix.Producers) != 1 || prefix.Producers[0].Calls != 0 || prefix.Producers[0].Transactions != 0 {
		t.Fatal("actual SDK component read tail not drained")
	}
	t.Logf("sdk_component_prefix transactions=%d rows=%d active_calls=%d active_transactions=%d", prefix.Transactions, prefix.Rows, prefix.Producers[0].Calls, prefix.Producers[0].Transactions)
	t.Run("canceled_caller", func(t *testing.T) {
		if err := read(t, "canceled_caller", request, true, 1); !errors.Is(err, storeaccounting.ErrCanceled) {
			t.Fatal("actual canceled SDK caller did not preserve its refusal")
		}
	})
}
